package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// defaultSystem 未配置时的默认 system 提示（记忆索引自 M2 起并入）。
// 面向模型的提示用英文（AGENTS 语言约定）；回复语言由首句指定为跟随用户。
const defaultSystem = "You are Aquarius, a minimalist personal AI assistant. Reply in the user's language, concisely."

// RuntimeEnv 运行环境事实（D37）：随 config 快照进 persona 首节点的英文环境块，
// 让模型开局就知道平台语义、终端形态、特权沙盒位置，而不必踩坑后才学。
type RuntimeEnv struct {
	Platform   string // 如 "windows/amd64"
	Terminal   string // UI 形态：tui / repl（cfg.UI.Kind）
	TERM       string // 终端 TERM 环境变量（可空）
	SandboxDir string // 特权沙盒目录（仅绝对路径才附提示，139d585 口径）
	// ToolTimeoutSec 缺省单次调用超时（D38）；0 = 不附超时说明行。
	ToolTimeoutSec int
}

// PromptModule 模块树节点（D144/S7）：叶 = 仅 Text（命名片段）；组 = Children（子模块名有序
// 列表，可多级嵌套），亦可同时带 Text（组头）。真源在 config `prompts.modules`。
type PromptModule struct {
	Text     string
	Children []string
}

// PromptSpec 人格装配规格（D144/S7）：模块树 + 有序顶层引用 + 兼容单串。
// 由装配根从 config `prompts` 段构造；app 侧只做纯组合、不读配置文件。
type PromptSpec struct {
	// Modules 模块树：name → 节点。用户模块名不得以 `@` 开头（保留给内置模块）。
	Modules map[string]PromptModule
	// Persona 人格 = 有序顶层引用列表；空 = 默认 DefaultPersonaRefs。
	Persona []string
	// Legacy 顶层 system_prompt（兼容）：作为内置 @default 的正文来源。
	Legacy string
}

// DefaultPersonaRefs prompts.persona 为空时的默认顶层引用（D144）。
var DefaultPersonaRefs = []string{"@default", "@env"}

// reservedModulePrefix 内置模块名保留前缀（用户模块不得占用，引擎侧一律以内置为准）。
const reservedModulePrefix = "@"

// builtinEnvRefs 内置 @env 组模块的子模块顺序（@env.date 恒出，其余按事实是否有值）。
var builtinEnvRefs = []string{"@env.platform", "@env.terminal", "@env.sandbox", "@env.timeout", "@env.date"}

// PersonaComposer 人格组合器（D144/S7）：绑定模块规格 + 内置模块动态取值（环境事实 + 时钟）。
// Compose 每次重新展开——@env.date 取 now()，故 persona 快照（会话创建时调用）得到「创建当天」。
type PersonaComposer struct {
	spec PromptSpec
	env  RuntimeEnv
	now  func() time.Time
}

// NewPersonaComposer 组装人格组合器；now 为 nil 时用 time.Now（@env.date 取值）。
func NewPersonaComposer(spec PromptSpec, env RuntimeEnv, now func() time.Time) *PersonaComposer {
	if now == nil {
		now = time.Now
	}
	return &PersonaComposer{spec: spec, env: env, now: now}
}

// Compose 展开人格为最终 system 文本，并返回缺失的模块名（供装配根记 warning、不静默）。
// 展开 = 深度优先：组自身 Text 在前、子模块换行接续（子模块间以换行连接）；顶层引用间以空行
// 连接；**空组不出**（有 children 但子模块全部无文本 ⇒ 整组含组文本一并不输出）；整段全空 ⇒
// 回落内置默认人格。环引用按递归栈截断。
func (c *PersonaComposer) Compose() (string, []string) {
	mods := c.modules()
	refs := c.spec.Persona
	if len(refs) == 0 {
		refs = DefaultPersonaRefs
	}
	var missing []string
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if t := expandModule(ref, mods, &missing, map[string]bool{}); t != "" {
			parts = append(parts, t)
		}
	}
	text := strings.Join(parts, "\n\n")
	if strings.TrimSpace(text) == "" {
		text = defaultSystem
	}
	return text, missing
}

// modules 合并内置模块与用户模块（用户模块名以 `@` 开头的被忽略——保留给内置）。
func (c *PersonaComposer) modules() map[string]PromptModule {
	mods := map[string]PromptModule{
		"@default": {Text: defaultPersonaText(c.spec.Legacy)},
		"@env":     {Text: "Runtime environment:", Children: builtinEnvRefs},
	}
	for name, text := range envLines(c.env) {
		mods[name] = PromptModule{Text: text}
	}
	// 缺席的 @env.* 显式置空（空字段不出行，但不是"缺失引用"——不算 missing）。
	for _, name := range builtinEnvRefs {
		if _, ok := mods[name]; !ok {
			mods[name] = PromptModule{}
		}
	}
	mods["@env.date"] = PromptModule{Text: "- Current date: " + c.now().Format("2006-01-02")}
	for name, m := range c.spec.Modules {
		if strings.HasPrefix(name, reservedModulePrefix) {
			continue
		}
		mods[name] = m
	}
	return mods
}

// defaultPersonaText @default 正文 = system_prompt 或内置默认（D20/D37 兼容）。
func defaultPersonaText(legacy string) string {
	if s := strings.TrimSpace(legacy); s != "" {
		return s
	}
	return defaultSystem
}

// envLines 环境信息内置模块取值（D37 口径）：字段为空不出行（空值不回传）。
func envLines(env RuntimeEnv) map[string]string {
	lines := map[string]string{}
	if env.Platform != "" {
		lines["@env.platform"] = "- Platform: " + env.Platform
	}
	if env.Terminal != "" {
		term := "TERM unset"
		if env.TERM != "" {
			term = "TERM=" + env.TERM
		}
		lines["@env.terminal"] = fmt.Sprintf("- Terminal: %s (%s)", env.Terminal, term)
	}
	// 沙盒提示必须是绝对路径——不能建议一个会被同一规则再拒的相对路径（139d585）。
	if filepath.IsAbs(env.SandboxDir) {
		lines["@env.sandbox"] = fmt.Sprintf("- Privileged sandbox directory: %q — writes there skip confirmation from the strict permission level upward.",
			env.SandboxDir)
	}
	// 超时发现性（D38）：保留参数的口径一句话讲清，避免模型逐个工具试错。
	if env.ToolTimeoutSec > 0 {
		lines["@env.timeout"] = fmt.Sprintf("- Tool timeout: default %ds per call; pass the optional \"timeout_sec\" argument (1-%d) on any tool call to override it.",
			env.ToolTimeoutSec, port.MaxCallTimeoutSec)
	}
	return lines
}

// expandModule 递归展开一个模块引用：组自身文本 + 子模块文本（换行接续）。
// 缺失名记入 missing 并返回空串；seen 为递归栈（截断环引用）。
func expandModule(name string, mods map[string]PromptModule, missing *[]string, seen map[string]bool) string {
	node, ok := mods[name]
	if !ok {
		*missing = append(*missing, name)
		return ""
	}
	if seen[name] {
		return "" // 环引用：截断
	}
	seen[name] = true
	defer delete(seen, name)

	var parts []string
	if s := strings.TrimSpace(node.Text); s != "" {
		parts = append(parts, s)
	}
	if len(node.Children) > 0 {
		childTexts := make([]string, 0, len(node.Children))
		for _, child := range node.Children {
			if t := expandModule(child, mods, missing, seen); t != "" {
				childTexts = append(childTexts, t)
			}
		}
		if len(childTexts) == 0 {
			return "" // 空组：整组（含组文本）不出
		}
		parts = append(parts, strings.Join(childTexts, "\n"))
	}
	return strings.Join(parts, "\n")
}

// waterline 计算装配水位（D21/D20）：persona = path[1] 的 system 节点；
// 摘要 = 其后最后一个 system 节点。返回 (personaIdx, watermarkIdx)，-1 表示无。
func waterline(path []conversation.Message) (personaIdx, watermarkIdx int) {
	personaIdx = -1
	if len(path) > 1 && path[1].Role == conversation.RoleSystem {
		personaIdx = 1
	}
	for i := len(path) - 1; i > personaIdx; i-- {
		if path[i].Role == conversation.RoleSystem {
			return personaIdx, i
		}
	}
	return personaIdx, -1
}

// assemblePath 把 Path() 装配为模型消息序列（DESIGN §7.1，含 D21 水位裁剪）：
// persona（path[1] 的 system 节点）恒回传；最新压缩摘要之上的历史（persona 之外）一律不回传；
// Root 空节点不进上下文。blobs 为 nil 时图片以内联占位文本代替（附件库就绪于 M3）。
// echoThinking 控制思考回传（D42）：true = 置入 assistant 的 PromptMessage.Reasoning
// （适配器映射 reasoning_content），false = 丢弃（仅展示/回放口径仍含思考）。
func assemblePath(ctx context.Context, path []conversation.Message, blobs port.AttachmentStore, echoThinking bool) ([]port.PromptMessage, error) {
	personaIdx, watermarkIdx := waterline(path)
	start := 1 // 起点恒 ≥ 1：Root 不进上下文
	if watermarkIdx >= 0 {
		start = watermarkIdx
	}

	// 进入上下文的节点序列（水位之上）。工具分片的调用与结果同片（D95），
	// 不存在"结果在位/失联"之分，无需跨节点配对集合。

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
		case conversation.RoleAssistant:
			// D95 Turn 粒度：一个节点可含多段"思考/正文 + 工具分片"交错——按 tool 边界
			// 切段投影（调用与结果同片恒成对；Result=nil 的分片不声明也不应答）。
			msgs, err := assembleAssistantNode(ctx, m, blobs, echoThinking)
			if err != nil {
				return nil, err
			}
			out = append(out, msgs...)
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

// assembleAssistantNode 把一个 assistant 节点按工具分片边界切段投影（D95 Turn 粒度）：
// 段 = 连续的思考/正文分片 + 其后的工具分片；「tool 之后再来的 thinking/text」开启新段。
// 每段产出 1 条 assistant 消息（Reasoning=段内思考拼接、Content=段内正文、
// ToolCalls=段内 Result≠nil 的调用）+ N 条 tool 应答消息——与 OpenAI 协议的
// assistant(tool_calls)→tool 应答交错要求一致。全空段不发（如仅 nil 结果分片的轮）。
func assembleAssistantNode(ctx context.Context, m conversation.Message, blobs port.AttachmentStore, echoThinking bool) ([]port.PromptMessage, error) {
	var out []port.PromptMessage
	var (
		reasoning []string
		content   []port.PromptPart
		calls     []tool.Call
		replies   []port.PromptMessage
	)
	flush := func() {
		if len(content) == 0 && len(reasoning) == 0 && len(calls) == 0 {
			reasoning, content, calls, replies = nil, nil, nil, nil
			return
		}
		out = append(out, port.PromptMessage{
			Role:      "assistant",
			Content:   content,
			Reasoning: strings.Join(reasoning, "\n\n"),
			ToolCalls: calls,
		})
		out = append(out, replies...)
		reasoning, content, calls, replies = nil, nil, nil, nil
	}
	for _, p := range m.Content {
		switch p.Kind {
		case conversation.PartThinking:
			if echoThinking { // 关（显式 false）= 丢弃，不回传（D42）
				if len(calls) > 0 || len(replies) > 0 {
					flush() // tool 之后再来的思考：开启新段
				}
				reasoning = append(reasoning, p.Text)
			}
		case conversation.PartText, conversation.PartImage, conversation.PartAudio, conversation.PartDoc:
			if len(calls) > 0 || len(replies) > 0 {
				flush() // tool 之后再来的正文：开启新段
			}
			pp, err := assembleParts(ctx, []conversation.Part{p}, blobs)
			if err != nil {
				return nil, err
			}
			content = append(content, pp...)
		case conversation.PartTool:
			if p.Tool == nil || p.Tool.Result == nil {
				continue // 未执行/被取消：不声明也不应答（API 不接受无应答的 tool_calls）
			}
			calls = append(calls, tool.Call{
				ID:   tool.CallID(p.Tool.CallID),
				Name: p.Tool.Name,
				Args: p.Tool.Args,
			})
			replies = append(replies, port.PromptMessage{
				Role:    "tool",
				CallID:  p.Tool.CallID,
				Content: []port.PromptPart{{Kind: "text", Text: toolResultText(p.Tool.Result)}},
			})
		}
	}
	flush()
	return out, nil
}

// assembleParts 把领域分片装配为提示词分片：
// audio 取转写文本、doc 取提取文本（D10）、image 经 AttachmentStore 内联字节（DESIGN §4.2）；
// thinking 不进 Content——由 assemblePath 经 PromptMessage.Reasoning 独立回传（D42）。
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
		case conversation.PartThinking:
			// 思考走 PromptMessage.Reasoning（D42），不与正文混装。
			continue
		case conversation.PartTool:
			// 工具分片不进正文（D95）：调用经 PromptMessage.ToolCalls、结果经 tool 应答消息。
			continue
		default:
			// 未知分片种类：忽略（前向兼容）。
		}
	}
	return out, nil
}

// thinkingText 提取思考分片文本（D42）：流内分片已合并为至多一段，取首个非空；
// 无思考返回空串（展示侧的同名助手在 uitui/repl 各自实现，出口剥控制序列）。
func thinkingText(parts []conversation.Part) string {
	for _, p := range parts {
		if p.Kind == conversation.PartThinking && strings.TrimSpace(p.Text) != "" {
			return p.Text
		}
	}
	return ""
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
