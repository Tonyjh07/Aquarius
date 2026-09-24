package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// defaultSystem 未配置时的默认 system 提示（记忆索引自 M2 起并入）。
const defaultSystem = "你是 Aquarius，一个面向个人的极简 AI 助手。用与用户相同的语言简洁回复。"

// historyToolMarker 失联 tool 结果的内联标注（DESIGN §4.1 不变量 2）。
const historyToolMarker = "〔历史工具结果〕"

// assemblePath 把 Path() 装配为模型消息序列（DESIGN §7.1，含 D21 水位裁剪）：
// persona（path[1] 的 system 节点）恒回传；最新压缩摘要之上的历史（persona 之外）一律不回传；
// Root 空节点不进上下文。blobs 为 nil 时图片以内联占位文本代替（附件库就绪于 M3）。
func assemblePath(ctx context.Context, path []conversation.Message, blobs port.AttachmentStore) ([]port.PromptMessage, error) {
	// 水位判定（D21）：persona = path[1] 的 system 节点；摘要 = 其后最后一个 system 节点。
	personaIdx := -1
	if len(path) > 1 && path[1].Role == conversation.RoleSystem {
		personaIdx = 1
	}
	watermarkIdx := -1
	for i := len(path) - 1; i > personaIdx; i-- {
		if path[i].Role == conversation.RoleSystem {
			watermarkIdx = i
			break
		}
	}
	start := 1 // 起点恒 ≥ 1：Root 不进上下文
	if watermarkIdx >= 0 {
		start = watermarkIdx
	}

	// 进入上下文的 assistant 声明的调用集合（水位之上的声明随历史消失，不算在位）。
	declared := map[tool.CallID]bool{}
	for i := start; i < len(path); i++ {
		for _, call := range path[i].ToolCalls {
			declared[call.ID] = true
		}
	}

	// 遍历序列：有水位时先单独补发 persona（它不在 [start:] 内，恒回传，D20/D21）。
	seq := make([]int, 0, len(path))
	if watermarkIdx >= 0 && personaIdx == 1 {
		seq = append(seq, personaIdx)
	}
	for i := start; i < len(path); i++ {
		seq = append(seq, i)
	}

	var out []port.PromptMessage
	for _, i := range seq {
		m := path[i]
		switch m.Role {
		case conversation.RoleRoot:
			continue // Root 空节点不进上下文（D19）
		case conversation.RoleSystem:
			// 系统节点（persona / 压缩摘要）映射为 system 角色（D20/D21）。
			parts, err := assembleParts(ctx, m.Content, blobs)
			if err != nil {
				return nil, err
			}
			out = append(out, port.PromptMessage{Role: "system", Content: parts})
		case conversation.RoleTool:
			text := toolResultText(m.ToolResult)
			if declared[m.ToolResult.CallID] {
				out = append(out, port.PromptMessage{
					Role:    "tool",
					CallID:  string(m.ToolResult.CallID),
					Content: []port.PromptPart{{Kind: "text", Text: text}},
				})
				continue
			}
			// 失联 tool 结果（引用的 assistant 不在本路径，如被 Revise 隔离）：
			// 按文本内联并标注，避免服务端因孤立 tool_call_id 报 400。
			out = append(out, port.PromptMessage{
				Role:    "assistant",
				Content: []port.PromptPart{{Kind: "text", Text: historyToolMarker + "\n" + text}},
			})
		case conversation.RoleAssistant:
			// 空节点（如 error 终态占位）不进上下文；
			// 带调用的助手消息必须保留（与后续 tool 应答配对）。
			if len(m.Content) == 0 && len(m.ToolCalls) == 0 {
				continue
			}
			parts, err := assembleParts(ctx, m.Content, blobs)
			if err != nil {
				return nil, err
			}
			out = append(out, port.PromptMessage{Role: "assistant", Content: parts, ToolCalls: m.ToolCalls})
		default: // user
			if len(m.Content) == 0 {
				continue
			}
			parts, err := assembleParts(ctx, m.Content, blobs)
			if err != nil {
				return nil, err
			}
			out = append(out, port.PromptMessage{Role: "user", Content: parts})
		}
	}
	return out, nil
}

// assembleParts 把领域分片装配为提示词分片：
// audio 取转写文本、doc 取提取文本（D10）、image 经 AttachmentStore 内联字节（DESIGN §4.2）。
func assembleParts(ctx context.Context, parts []conversation.Part, blobs port.AttachmentStore) ([]port.PromptPart, error) {
	out := make([]port.PromptPart, 0, len(parts))
	for _, p := range parts {
		switch p.Kind {
		case conversation.PartText:
			out = append(out, port.PromptPart{Kind: "text", Text: p.Text})
		case conversation.PartAudio:
			// 模型只见文本，音频留附件库回放（DESIGN §4.2）。
			if p.Transcript != "" {
				out = append(out, port.PromptPart{Kind: "text", Text: p.Transcript})
			}
		case conversation.PartDoc:
			// 提取文本（截断）已存于节点（D10）。
			if p.Text != "" {
				out = append(out, port.PromptPart{Kind: "text", Text: p.Text})
			}
		case conversation.PartImage:
			if p.Ref == nil {
				continue
			}
			if blobs == nil {
				out = append(out, port.PromptPart{Kind: "text", Text: fmt.Sprintf("〔图片：%s〕", p.Ref.Name)})
				continue
			}
			rc, err := blobs.Get(ctx, *p.Ref)
			if err != nil {
				return nil, fmt.Errorf("读取附件 %s: %w", p.Ref.Name, err)
			}
			data, rerr := io.ReadAll(rc)
			_ = rc.Close()
			if rerr != nil {
				return nil, fmt.Errorf("读取附件 %s: %w", p.Ref.Name, rerr)
			}
			out = append(out, port.PromptPart{Kind: "image", MIME: p.Ref.MIME, Data: data})
		default:
			// 未知分片种类：忽略（前向兼容）。
		}
	}
	return out, nil
}

// toolResultText 工具结果的提示词文本。
// Output/Err 均为不可信数据，只作文本回填给模型、不执行（DESIGN §9）。
func toolResultText(res *tool.Result) string {
	if res == nil {
		return "（无输出）"
	}
	text := strings.TrimSpace(res.Output)
	if res.OK {
		if text == "" {
			return "（无输出）"
		}
		return text
	}
	e := res.Err
	if e == "" {
		e = "工具执行失败"
	}
	out := "错误: " + e
	if text != "" {
		out += "\n" + text
	}
	return out
}
