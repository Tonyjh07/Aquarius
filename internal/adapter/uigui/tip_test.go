package uigui

import (
	"image"
	"strings"
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestFactsCardLines 事实卡内容（D82/S1-1g，§15.1）：五段口径——profile · 会话（标题 +
// ID 前缀 + 模型/档位）· 上下文占用（pct + 计数方式）· 累计用量 · 上轮实测（有实测才显）。
func TestFactsCardLines(t *testing.T) {
	u := newFrameUI()
	u.opts.Status = func() Status {
		return Status{
			Model: "gpt-x", Level: "standard", Effort: "high", Profile: "default",
			Facts: port.SessionFacts{
				Title: "你好世界", ConvID: "abcdefgh1234",
				CtxTokens: 32000, CtxMax: 64000,
				SumIn: 100, SumOut: 20, LastIn: 90, LastOut: 15,
			},
		}
	}
	lines := u.factsCard()
	if len(lines) != 4 { // 五段并四行：模型并入会话行
		t.Fatalf("lines = %d, want 4:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for _, want := range []struct{ ln, sub string }{
		{lines[0], "default"}, {lines[0], "你好世界"}, {lines[0], "abcdefgh"}, {lines[0], "gpt-x（standard，high）"},
		{lines[1], "32000/64000"}, {lines[1], "50.0%"}, {lines[1], "估算"},
		{lines[2], "in 100 / out 20"},
		{lines[3], "上轮"}, {lines[3], "in 90 / out 15"},
	} {
		if !strings.Contains(want.ln, want.sub) {
			t.Fatalf("行 %q 缺 %q", want.ln, want.sub)
		}
	}
}

// TestFactsCardVariants 事实卡分支措辞（D82）：精确计数、无 effort、无上轮实测、
// profile 空回落 default。
func TestFactsCardVariants(t *testing.T) {
	u := newFrameUI()
	u.opts.Status = func() Status {
		return Status{
			Level: "standard",
			Facts: port.SessionFacts{
				Title: "分支", ConvID: "short", CtxTokens: 800, CtxMax: 64000,
				CtxExact: true, SumIn: 0, SumOut: 0,
			},
		}
	}
	lines := u.factsCard()
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3（无模型/无上轮实测）:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "default · 分支（short）") {
		t.Fatalf("line0 = %q", lines[0])
	}
	if !strings.Contains(lines[1], "精确") {
		t.Fatalf("line1 = %q（应标精确）", lines[1])
	}
	if strings.Contains(strings.Join(lines, "\n"), "上轮") {
		t.Fatal("无实测不应显示上轮行")
	}
}

// TestFactsCardUnready 快照未就绪（零值）→ nil，调用方回退启动提示（D82）。
func TestFactsCardUnready(t *testing.T) {
	u := newFrameUI() // opts.Status = nil
	if lines := u.factsCard(); lines != nil {
		t.Fatalf("无 Status 回调应得 nil, got %v", lines)
	}
	u.opts.Status = func() Status { return Status{} }
	if lines := u.factsCard(); lines != nil {
		t.Fatalf("零值快照应得 nil, got %v", lines)
	}
}

// TestHoverCardRegistersShape 多行卡绘制并登记形状（D82）：一次 hoverCard 只登记
// 一条底板形状；单行包装 hoverTip 同款（泛化不破坏既有调用点）。
func TestHoverCardRegistersShape(t *testing.T) {
	u := newFrameUI()
	u.frameSize = image.Pt(winWidthDp, winHeightDp)
	gtx, _ := frameGtx(input.Source{})

	before := len(u.shapes)
	u.hoverCard(gtx, 400, []string{"第一行", "第二行更长一些", "三"}, false)
	if len(u.shapes) != before+1 {
		t.Fatalf("shapes = %d, want %d（恰登记一条底板）", len(u.shapes), before+1)
	}
	u.hoverTip(gtx, 400, "单行", true)
	if len(u.shapes) != before+2 {
		t.Fatalf("hoverTip 包装后 shapes = %d, want %d", len(u.shapes), before+2)
	}
}

// TestShortID 会话 ID 展示前缀（D82）：长 ID 截 8 位、短 ID 原样。
func TestShortID(t *testing.T) {
	if got := shortID("abcdefgh1234"); got != "abcdefgh" {
		t.Fatalf("shortID = %q", got)
	}
	if got := shortID("abc"); got != "abc" {
		t.Fatalf("shortID = %q", got)
	}
}
