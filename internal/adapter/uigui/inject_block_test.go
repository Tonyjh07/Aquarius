package uigui

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// TestInjectBlockCommitReplay 注入节点在提交与回放两路都成独立暗块（D166）：
// 来源/触发随块携带、不落 user 盖章（id 恒空 → 不挂分叉条）、空正文不落块。
func TestInjectBlockCommitReplay(t *testing.T) {
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

// TestInjectHeadline 卡片头部口径（D166）：来源经友好化表、状态记实际发生。
func TestInjectHeadline(t *testing.T) {
	cases := []struct {
		source  string
		trigger bool
		want    string
	}{
		{"job:j1", true, "注入 · 任务 j1 · 已触发响应"},
		{"tool:think", false, "注入 · 工具 think · 未触发"},
		{"user", true, "注入 · 手动 · 已触发响应"},
		{"", false, "注入 · 未知来源 · 未触发"},
	}
	for _, tc := range cases {
		if got := injectHeadline(tc.source, tc.trigger); got != tc.want {
			t.Fatalf("headline(%q,%v) = %q, want %q", tc.source, tc.trigger, got, tc.want)
		}
	}
}

// TestInjectOverlayTitleAndSource 遮罩标题与来源行（D166）：注入节点只读，来源行
// 与转写头部同表；触发状态取快照事实。
func TestInjectOverlayTitleAndSource(t *testing.T) {
	ov := &overlayState{id: "i1", role: conversation.RoleInject, source: "poller:p1", trigger: true}
	if got := ov.titleText(); !strings.HasPrefix(got, "注入上下文 · ") {
		t.Fatalf("titleText = %q", got)
	}
	if got := injectState(ov.trigger); got != "已触发响应" {
		t.Fatalf("injectState = %q", got)
	}
	if got := injectState(false); got != "未触发" {
		t.Fatalf("injectState(false) = %q", got)
	}
}
