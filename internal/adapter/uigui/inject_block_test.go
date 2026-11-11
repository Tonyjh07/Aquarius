package uigui

import (
	"strings"
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// TestInjectBlockCommitReplay 注入节点在提交与回放两路都成独立暗块（D166）：
// 来源/触发随块携带、不落 user 盖章（id 恒空 → 不挂分叉条）、空正文不落块。
func TestInjectBlockCommitReplay(t *testing.T) {
	t.Parallel()
	m, u := newTestModel(t)
	u.m = m
	parts := []conversation.Part{{Kind: conversation.PartText, Text: "任务 j1 已完成"}}

	m.commit(conversation.Message{
		ID: "i1", Role: conversation.RoleInject, Source: "job:j1", Trigger: true, Content: parts,
	})
	m.replay(conversation.Message{
		ID: "i2", Role: conversation.RoleInject, Source: "tool:think", Content: parts,
	})
	m.commit(conversation.Message{ // 空正文：不落块（头部单独成行只是噪声）
		ID: "i3", Role: conversation.RoleInject, Source: "user",
	})

	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want 2（提交/回放各一块，空正文跳过）", len(m.blocks))
	}
	first, second := m.blocks[0], m.blocks[1]
	if first.kind != blockInject || second.kind != blockInject {
		t.Fatalf("块种 = %d/%d, want blockInject", first.kind, second.kind)
	}
	if first.source != "job:j1" || !first.trigger {
		t.Fatalf("提交块来源/触发 = %q/%v, want job:j1/true", first.source, first.trigger)
	}
	if second.source != "tool:think" || second.trigger {
		t.Fatalf("回放块来源/触发 = %q/%v, want tool:think/false", second.source, second.trigger)
	}
	for i, b := range m.blocks {
		if b.id != "" || b.part != -1 {
			t.Fatalf("blocks[%d] id/part = %q/%d, want 空/-1（不盖 user 戳、不挂分叉条）", i, b.id, b.part)
		}
	}

	items := u.frameItems()
	if len(items) != 2 || items[0].source != "job:j1" || !items[0].trigger ||
		items[1].source != "tool:think" || items[1].trigger {
		t.Fatalf("frameItems 未带来源/触发: %+v", items)
	}
}

// TestInjectHeadline 折叠行文案：`▸ 类别 · 来源`，展开时箭头朝下。类别按来源前缀分，
// 余下部分不带前缀词（否则读成「工具注入 · 工具 think」）；未知前缀回落成来源标签。
func TestInjectHeadline(t *testing.T) {
	t.Parallel()
	cases := []struct{ source, title string }{
		{"job:j1", i18n.T("ui.inject.kind.job") + injectSep + "j1"},
		{"tool:think", i18n.T("ui.inject.kind.tool") + injectSep + "think"},
		{"clock", i18n.T("ui.inject.kind.clock") + injectSep + i18n.T("port.src.clock")},
		{"user", i18n.T("ui.inject.kind.user")},
		{"weird", "weird"}, // 未知前缀：原样给来源，不猜类别
	}
	for _, tc := range cases {
		if got, want := injectHeadline(tc.source, false), "▸ "+tc.title; got != want {
			t.Fatalf("headline(%q,false) = %q, want %q", tc.source, got, want)
		}
		if got, want := injectHeadline(tc.source, true), "▾ "+tc.title; got != want {
			t.Fatalf("headline(%q,true) = %q, want %q", tc.source, got, want)
		}
	}
}

// TestInjectCollapse 注入块默认折成一行，点开才铺触发状态与正文；再点收起。
// 开合是视图态，不改变块本身。
func TestInjectCollapse(t *testing.T) {
	t.Parallel()
	u := newFrameUI()
	u.m.commit(conversation.Message{
		ID: "i1", Role: conversation.RoleInject, Source: "clock",
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "The date has changed: today is 2026-11-11."}},
	})
	m := u.m

	gtx, _ := frameGtx(input.Source{})
	u.transcript(gtx, 600, 400)
	if u.injectIsOpen(0) {
		t.Fatal("注入块应默认折叠")
	}
	if len(u.injectClicks) != 1 {
		t.Fatalf("折叠行点击件 = %d, want 1", len(u.injectClicks))
	}
	if got := u.blockText(0, 1); got != "" {
		t.Fatalf("折叠时正文不应铺开（行选键无文本）: %q", got)
	}
	collapsedH := u.contentH

	// 展开：触发状态与正文全文都铺开，内容变高。
	u.injectClicks[0].Click()
	u.updateClicks(gtx)
	if !u.injectIsOpen(0) {
		t.Fatal("点击折叠行应展开")
	}
	u.transcript(gtx, 600, 400)
	if u.contentH <= collapsedH {
		t.Fatalf("展开后内容高 = %d，折叠时 = %d（应更高）", u.contentH, collapsedH)
	}
	got := u.blockText(0, 1)
	if !strings.Contains(got, "2026-11-11") {
		t.Fatalf("展开后正文没铺开:\n%s", got)
	}

	// 再点一次收起：块没变。
	u.injectClicks[0].Click()
	u.updateClicks(gtx)
	if u.injectIsOpen(0) {
		t.Fatal("再点一次应收起")
	}
	if len(m.blocks) != 1 || m.blocks[0].kind != blockInject {
		t.Fatalf("开合不应改变块: %+v", m.blocks)
	}
}

// TestInjectOverlayTitleAndSource 遮罩标题与来源行（D166）：注入节点只读，来源行
// 与转写头部同表；触发状态取快照事实。
func TestInjectOverlayTitleAndSource(t *testing.T) {
	t.Parallel()
	ov := &overlayState{id: "i1", role: conversation.RoleInject, source: "poller:p1", trigger: true}
	if got := ov.titleText(); got != i18n.Tf("ui.overlay.title.inject", "id", shortID("i1")) {
		t.Fatalf("titleText = %q", got)
	}
	if got := injectState(ov.trigger); got != i18n.T("ui.state.fired") {
		t.Fatalf("injectState = %q", got)
	}
	if got := injectState(false); got != i18n.T("ui.state.idle") {
		t.Fatalf("injectState(false) = %q", got)
	}
}
