package plugingo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// 每个转换面配一条断言（plugin-design.md §11 第 2 条）。测的是语义：值原样搬过去、
// 该丢的丢了、默认值没被悄悄填上。

func TestRequestConversion(t *testing.T) {
	temp := 0.0
	req := port.GenerateRequest{
		Model: "m1",
		Messages: []port.PromptMessage{{
			Role:               port.PromptAssistant,
			Content:            []port.PromptPart{{Kind: port.PromptText, Text: "hi"}},
			Reasoning:          "thinking-text",
			ReasoningSignature: "anthropic:sig",
			ToolCalls:          []tool.Call{{ID: "c1", Name: "t", Args: json.RawMessage(`{"a":1}`)}},
			ServerBlocks:       []json.RawMessage{json.RawMessage(`{"type":"read"}`)},
		}},
		Tools:  []tool.Spec{{Name: "t", Schema: json.RawMessage(`{"type":"object"}`), Risk: tool.Confirm}},
		Params: port.Sampling{Temperature: &temp, MaxTokens: 128, Stop: []string{"\n"}},
		Budget: port.TokenBudget{MaxOutputTokens: 256, MaxCostUSD: 0.5},
		Title:  true,
	}
	got := toV1Request(req)
	if got.Model != "m1" || !got.Title || got.Budget.MaxOutputTokens != 256 || got.Budget.MaxCostUSD != 0.5 {
		t.Fatalf("顶层字段没搬过去：%+v", got)
	}
	msg := got.Messages[0]
	if msg.Role != v1.PromptAssistant || msg.Reasoning != "thinking-text" || msg.ReasoningSignature != "anthropic:sig" {
		t.Fatalf("消息字段没搬过去：%+v", msg)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "c1" {
		t.Fatalf("工具调用没搬过去：%+v", msg.ToolCalls)
	}
	if len(msg.ServerBlocks) != 1 || string(msg.ServerBlocks[0]) != `{"type":"read"}` {
		t.Fatalf("服务端块没原样过去：%q", msg.ServerBlocks)
	}
	if len(got.Tools) != 1 || got.Tools[0].Risk != v1.RiskConfirm {
		t.Fatalf("工具声明或风险档不对：%+v", got.Tools)
	}
	// temperature 的 0 是有意义取值：指针必须还在，且不是同一个指针（防插件改到内核配置）
	if got.Params.Temperature == nil || *got.Params.Temperature != 0 {
		t.Fatal("temperature=0 被当成「不发送」了")
	}
	if got.Params.Temperature == req.Params.Temperature {
		t.Fatal("指针被直接交出去了，插件写一下就改到内核配置")
	}
}

func TestRequestDropsToolsWhenServerTools(t *testing.T) {
	req := port.GenerateRequest{
		Tools:       []tool.Spec{{Name: "local"}},
		ServerTools: []string{"deepseek_read_url"},
	}
	got := toV1Request(req)
	if len(got.Tools) != 0 {
		t.Fatal("声明服务端工具时不能同时给本地 function tools（协议会 400）")
	}
	if len(got.ServerTools) != 1 {
		t.Fatalf("服务端工具丢了：%+v", got.ServerTools)
	}
}

func TestDeltaConversion(t *testing.T) {
	d := v1.Delta{
		Text:               "chunk",
		ReasoningSignature: []byte("responses:xyz"),
		Usage:              &v1.Usage{InputTokens: 10, OutputTokens: 2, CacheRead: 4, Thinking: 1},
		ToolCalls:          []v1.ToolCallDelta{{Index: 0, ID: "c1", Name: "t", ArgsDelta: `{"a"`}},
		Server:             &v1.ServerBlock{Tool: "read", Kind: "read_link", URL: "u", Raw: []byte(`{"raw":1}`)},
	}
	got := toPortDelta(d)
	if got.Text != "chunk" || got.ReasoningSignature != "responses:xyz" {
		t.Fatalf("文本或签名不对：%+v", got)
	}
	if got.Usage == nil || got.Usage.InputTokens != 10 || got.Usage.CacheRead != 4 || got.Usage.Thinking != 1 {
		t.Fatalf("用量不对：%+v", got.Usage)
	}
	if got.Usage.CostUSD != 0 {
		t.Fatal("CostUSD 不该由插件给（折算归内核）")
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ArgsDelta != `{"a"` {
		t.Fatalf("工具增量不对：%+v", got.ToolCalls)
	}
	if got.Server == nil || string(got.Server.Raw) != `{"raw":1}` || got.Server.Kind != "read_link" {
		t.Fatalf("服务端块不对：%+v", got.Server)
	}
}

func TestToolConversion(t *testing.T) {
	call := tool.Call{ID: "c9", Name: "t", Args: json.RawMessage(`{"x":1}`)}
	vc := toV1ToolCall(call)
	if vc.ID != "c9" || vc.Name != "t" || string(vc.Args) != `{"x":1}` {
		t.Fatalf("调用没搬过去：%+v", vc)
	}
	res := toPortToolResult(call.ID, v1.ToolResult{
		OK: true, Output: "done", Fallback: "https://x",
		Images: []v1.ImageRef{{Hash: "h", MIME: "image/png", Size: 3}},
	})
	if res.CallID != "c9" || !res.OK || res.Output != "done" || res.Fallback != "https://x" {
		t.Fatalf("结果没搬回去：%+v", res)
	}
	if len(res.Images) != 1 || res.Images[0].Hash != "h" || res.Images[0].Size != 3 {
		t.Fatalf("图片引用不对：%+v", res.Images)
	}
}

func TestConfirmConversion(t *testing.T) {
	in := v1.ConfirmRequest{
		Origin: "myp", Tool: "write", Op: v1.OpWrite, HasOp: true,
		Target: "/tmp/a", Risk: v1.RiskConfirm, HasRisk: true,
		Prompt: "写 /tmp/a？", CanPersist: true,
	}
	got := toPortConfirmRequest(in)
	if got.Op != perm.OpWrite || !got.HasOp || got.Risk != tool.Confirm || !got.HasRisk {
		t.Fatalf("Op/Risk 投影不对：%+v", got)
	}
	if got.Origin != port.ConfirmOrigin("myp") || got.Target != "/tmp/a" || !got.CanPersist {
		t.Fatalf("其余字段没搬过去：%+v", got)
	}

	// 不涉及文件操作：v1 是空串 + HasOp=false，内核侧 HasOp=false
	if q := toPortConfirmRequest(v1.ConfirmRequest{Tool: "x"}); q.HasOp || q.HasRisk {
		t.Fatalf("零值被当成了「涉及读、风险 safe」：%+v", q)
	}
	// Op 空但 HasOp=true 是不合法组合，按不涉及处理
	if q := toPortConfirmRequest(v1.ConfirmRequest{HasOp: true}); q.HasOp {
		t.Fatal("Op 为空时 HasOp 应归零")
	}

	ans := toV1ConfirmAnswer(port.ConfirmAnswer{Allow: true, Level: "full", Persist: true})
	if !ans.Allow || ans.Level != "full" || !ans.Persist {
		t.Fatalf("应答没搬回去：%+v", ans)
	}
}

func TestInputAndOutputConversion(t *testing.T) {
	raw := port.RawInput{
		Kind: "file", Text: "看看", File: "a.png",
		Blob:        &conversation.BlobRef{Hash: "h1", MIME: "image/png", Size: 9},
		Attachments: []port.Attachment{{Path: "b.txt"}},
		Seconds:     30,
	}
	vraw := toV1RawInput(raw)
	if vraw.Kind != "file" || vraw.File != "a.png" || vraw.Seconds != 30 {
		t.Fatalf("原始输入没搬过去：%+v", vraw)
	}
	if vraw.Blob == nil || vraw.Blob.Hash != "h1" || vraw.Blob.Size != 9 {
		t.Fatalf("Blob 引用不对：%+v", vraw.Blob)
	}
	if len(vraw.Attachments) != 1 || vraw.Attachments[0].Path != "b.txt" {
		t.Fatalf("附件列表不对：%+v", vraw.Attachments)
	}

	// 树上的工具分片不进 v1；正文与思考分片照搬
	parts := []conversation.Part{
		{Kind: conversation.PartTool, Tool: &conversation.ToolPart{Name: "t"}},
		{Kind: conversation.PartText, Text: "正文"},
		{Kind: conversation.PartThinking, Text: "想", Signature: "anthropic:s"},
	}
	out := toV1OutputRequest(port.OutputRequest{
		Message: conversation.Message{ID: "m1", Role: conversation.RoleAssistant},
		Parts:   parts,
	})
	if out.MessageID != "m1" || out.Role != "assistant" {
		t.Fatalf("消息标识不对：%+v", out)
	}
	if len(out.Parts) != 2 {
		t.Fatalf("工具分片应被丢掉，剩下 2 片，得到 %d", len(out.Parts))
	}
	if out.Parts[1].Kind != v1.PartThinking || string(out.Parts[1].Signature) != "anthropic:s" {
		t.Fatalf("思考分片的签名没带上：%+v", out.Parts[1])
	}

	// 反向：摄取结果里的空分片丢掉，其余回树
	rep := toPortIngestReport(v1.IngestReport{
		Parts: []v1.Part{{Kind: ""}, {Kind: v1.PartText, Text: "extracted"}},
		Note:  "transcribed",
	})
	if len(rep.Parts) != 1 || rep.Parts[0].Text != "extracted" || rep.Note != "transcribed" {
		t.Fatalf("摄取结果不对：%+v", rep)
	}
}

func TestSpeechAndCommandConversion(t *testing.T) {
	seg := toV1SpeechSegment(port.SpeechSegment{PCM: []byte{1, 2}, At: 32 * time.Millisecond})
	if len(seg.PCM) != 2 || seg.At != 32*time.Millisecond {
		t.Fatalf("语音段不对：%+v", seg)
	}
	back := toPortSpeechSegment(seg)
	if len(back.PCM) != 2 || back.At != seg.At {
		t.Fatalf("回收不对：%+v", back)
	}

	info := port.CommandInfo{
		Names: []string{"memory", "mem"}, Usage: "<id>", Desc: "看记忆",
		Params: []port.CommandParam{{Name: "--keep", Kind: port.ParamFlag, Repeatable: true}},
	}
	spec := toV1CommandSpec(info)
	if len(spec.Names) != 2 || spec.Names[0] != "memory" {
		t.Fatalf("命令名不对：%+v", spec)
	}
	if len(spec.Params) != 1 || spec.Params[0].Kind != v1.ParamFlag || !spec.Params[0].Repeatable {
		t.Fatalf("参数声明不对：%+v", spec.Params)
	}
	round := toPortCommandSpec(spec)
	if round.Usage != "<id>" || len(round.Params) != 1 || round.Params[0].Kind != port.ParamFlag {
		t.Fatalf("往返后不对：%+v", round)
	}
}

func TestSessionCrossesBothWays(t *testing.T) {
	// 内核 → 插件：port 的键搬到 v1 的键
	kctx := port.WithSessionID(context.Background(), conversation.ID("s-1"))
	pctx := toV1Session(kctx)
	id, ok := v1.SessionFrom(pctx)
	if !ok || id != "s-1" {
		t.Fatalf("会话没搬到插件侧：%q ok=%v", id, ok)
	}

	// 插件 → 内核（Host 的入参 ctx 可能是插件侧派生出来的）
	back := toPortSession(pctx)
	kid, ok := port.SessionIDFrom(back)
	if !ok || kid != conversation.ID("s-1") {
		t.Fatalf("会话没搬回内核侧：%q ok=%v", kid, ok)
	}

	// 没有会话时两侧都不该凭空造一个值
	if got, ok := v1.SessionFrom(toV1Session(context.Background())); ok || got != "" {
		t.Fatalf("无会话被凭空填成 %q", got)
	}
	if got, ok := port.SessionIDFrom(toPortSession(context.Background())); ok || got != "" {
		t.Fatalf("无会话被凭空填成 %q", got)
	}
}
