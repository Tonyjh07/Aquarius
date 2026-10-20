package uigui

// think_test.go D182 思考块折叠：定稿块默认只留一行「已思考 N 秒」，点开才铺正文；
// 流式中不折叠（还没到定稿态）；开合是视图态——不进 model、清屏复位。

import (
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestThinkHeaderText 折叠行文案：▸/▾ + 耗时（回放无耗时只写「已思考」）；流式写
// 「思考中…」且不带开合指示（那时还没有可折叠的内容边界）。
func TestThinkHeaderText(t *testing.T) {
	cases := []struct {
		it   blockView
		open bool
		want string
	}{
		{blockView{kind: blockThinking, secs: 12}, false, "▸ 已思考 12 秒"},
		{blockView{kind: blockThinking, secs: 12}, true, "▾ 已思考 12 秒"},
		{blockView{kind: blockThinking, secs: 0}, false, "▸ 已思考 0 秒"},
		{blockView{kind: blockThinking, secs: -1}, false, "▸ 已思考"},
		{blockView{kind: blockThinking, secs: -1}, true, "▾ 已思考"},
		{blockView{kind: blockThinking, live: true}, false, "思考中…"},
	}
	for _, c := range cases {
		if got := thinkHeaderText(c.it, c.open); got != c.want {
			t.Fatalf("thinkHeaderText(%+v, %v) = %q, want %q", c.it, c.open, got, c.want)
		}
	}
}

// TestThinkCollapseDefault 定稿思考默认折叠：正文不铺开（高度只有折叠行、行选键不挂），
// 但折叠行仍是可点的开合热区。
func TestThinkCollapseDefault(t *testing.T) {
	u := newFrameUI()
	u.m.addMsg(blockThinking, "想一想", "a1")
	u.m.blocks[0].secs = 5

	gtx, _ := frameGtx(input.Source{})
	u.transcript(gtx, 600, 400)

	if u.thinkIsOpen(0) {
		t.Fatal("定稿思考块应默认折叠")
	}
	if len(u.thinkClicks) != 1 {
		t.Fatalf("折叠行点击件 = %d, want 1（折叠行整行可点）", len(u.thinkClicks))
	}
	if got := u.blockText(0, 1); got != "" {
		t.Fatalf("折叠时正文不应铺开（行选键无文本）: %q", got)
	}
	collapsedH := u.contentH
	if len(u.shapes) != 1 {
		t.Fatalf("shapes = %d, want 1（单张暗卡片）", len(u.shapes))
	}

	// 展开：正文铺开 → 内容变高、行选键可复制。
	u.thinkClicks[0].Click()
	u.updateClicks(gtx)
	if !u.thinkIsOpen(0) {
		t.Fatal("点击折叠行应展开")
	}
	u.transcript(gtx, 600, 400)
	if u.contentH <= collapsedH {
		t.Fatalf("展开后内容高 = %d, 折叠时 = %d（应更高）", u.contentH, collapsedH)
	}
	if got := u.blockText(0, 1); got != "想一想" {
		t.Fatalf("展开后可选正文 = %q, want 想一想", got)
	}

	// 再点一次收起。
	u.thinkClicks[0].Click()
	u.updateClicks(gtx)
	if u.thinkIsOpen(0) {
		t.Fatal("再点一次应收起")
	}
	u.transcript(gtx, 600, 400)
	if u.contentH != collapsedH {
		t.Fatalf("收起后内容高 = %d, want %d（回到折叠态）", u.contentH, collapsedH)
	}
}

// TestThinkLiveNeverCollapses 流式思考不折叠：正文始终铺开、不建点击件（折叠行只在
// 定稿后才有意义——流式中途收起会让用户看不见正在想什么）。
func TestThinkLiveNeverCollapses(t *testing.T) {
	u := newFrameUI()
	u.m.handleEvent(port.DeltaEvent{Delta: port.Delta{Reasoning: true, Text: "先想一想"}})

	gtx, _ := frameGtx(input.Source{})
	u.transcript(gtx, 600, 400)

	if len(u.thinkClicks) != 0 {
		t.Fatalf("流式思考不应建折叠点击件: %d", len(u.thinkClicks))
	}
	if got := u.blockText(0, 1); got != "先想一想" {
		t.Fatalf("流式思考正文应铺开: %q", got)
	}
}

// TestThinkOpenResetsOnClear 开合态是视图态（同 chip 口径）：清屏后块序从 0 重排，
// 上一会话某个序位的展开不应被新会话的同序位块继承。
func TestThinkOpenResetsOnClear(t *testing.T) {
	u := newFrameUI()
	u.m.addMsg(blockThinking, "想一想", "a1")
	u.thinkOpen = map[int]bool{0: true}

	u.m.clear()
	if u.thinkIsOpen(0) {
		t.Fatal("清屏后思考块开合态应复位（默认折叠）")
	}

	u.m.handleEvent(port.HistoryEvent{Message: conversation.Message{
		ID: "a2", Role: conversation.RoleAssistant,
		Content: []conversation.Part{{Kind: conversation.PartThinking, Text: "新的思考"}},
	}})
	gtx, _ := frameGtx(input.Source{})
	u.transcript(gtx, 600, 400)
	if got := u.blockText(0, 1); got != "" {
		t.Fatalf("新会话思考块应默认折叠, 却铺开了正文: %q", got)
	}
}
