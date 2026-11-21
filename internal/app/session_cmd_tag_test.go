package app

// session_cmd_tag_test.go 会话标签与置顶的命令面行为（D313）：
// /tag 加删与显示、/list --tag 交集过滤、置顶排最前且带 ▲、非当前会话不碰 s.cur、
// 分叉不带走标签与置顶。

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// memConv 取内存库里那份会话副本（保存即深拷贝，读它的字段才是落盘后的真值）。
func memConv(t *testing.T, m *memStore, title string) *conversation.Conversation {
	t.Helper()
	for _, c := range m.convs {
		if c.Title == title {
			return c
		}
	}
	t.Fatalf("memStore: no conversation titled %q", title)
	return nil
}

// memID 标题对应的会话 ID（/tag 与 /pin 的参数要用真 ID，标题不是 ID）。
func memID(t *testing.T, m *memStore, title string) string {
	return string(memConv(t, m, title).ID)
}

// TestTagAddShowRemoveCycle 打标签 → /list 行尾带 [标签] → 显示 → 删除 → 清空，
// 全程 /list --tag 只列带该标签的会话（验收口径）。
func TestTagAddShowRemoveCycle(t *testing.T) {
	s, store, _ := newBatchSession(t, "甲", "乙") // 当前 = 乙
	if _, err := handleCmd(s, "tag", memID(t, store, "甲"), "工作", "重要"); err != nil {
		t.Fatalf("tag add: %v", err)
	}
	if got := memConv(t, store, "甲").Tags; len(got) != 2 || got[0] != "工作" || got[1] != "重要" {
		t.Fatalf("tags = %v, want [工作 重要]（保序）", got)
	}
	out, err := handleCmd(s, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "[工作 重要]") {
		t.Fatalf("list = %q, want 行尾标签", out)
	}
	// 交集过滤：带「工作」的只有甲。
	out, err = handleCmd(s, "list", "--tag", "工作")
	if err != nil {
		t.Fatalf("list --tag: %v", err)
	}
	if !strings.Contains(out, "甲") || strings.Contains(out, "乙") {
		t.Fatalf("list --tag = %q, want 只列甲", out)
	}
	// 显示与重复打标签（幂等）。
	out, err = handleCmd(s, "tag", memID(t, store, "甲"))
	if err != nil {
		t.Fatalf("tag show: %v", err)
	}
	if want := i18n.Tf("tree.tag.show", "id", memID(t, store, "甲"), "tags", "工作 重要"); !strings.Contains(out, want) {
		t.Fatalf("tag show = %q, want %q", out, want)
	}
	if _, err := handleCmd(s, "tag", memID(t, store, "甲"), "工作"); err != nil {
		t.Fatalf("tag re-add: %v", err)
	}
	if got := memConv(t, store, "甲").Tags; len(got) != 2 {
		t.Fatalf("re-add duplicated: %v, want 2 个", got)
	}
	// 删除：--rm 一个，剩一个；再删就清空。
	if _, err := handleCmd(s, "tag", memID(t, store, "甲"), "--rm", "重要"); err != nil {
		t.Fatalf("tag rm: %v", err)
	}
	if got := memConv(t, store, "甲").Tags; len(got) != 1 || got[0] != "工作" {
		t.Fatalf("tags after rm = %v, want [工作]", got)
	}
	if _, err := handleCmd(s, "tag", memID(t, store, "甲"), "--rm", "工作"); err != nil {
		t.Fatalf("tag rm last: %v", err)
	}
	out, err = handleCmd(s, "tag", memID(t, store, "甲"))
	if err != nil {
		t.Fatalf("tag show empty: %v", err)
	}
	if want := i18n.Tf("tree.tag.none", "id", memID(t, store, "甲")); !strings.Contains(out, want) {
		t.Fatalf("tag show = %q, want %q", out, want)
	}
	out, err = handleCmd(s, "list", "--tag", "工作")
	if err != nil {
		t.Fatalf("list --tag empty: %v", err)
	}
	if want := i18n.T("tree.list.no_match"); !strings.Contains(out, want) {
		t.Fatalf("list --tag = %q, want %q", out, want)
	}
}

// TestPinTogglesAndSortsFirst 置顶切换：开 → /list 置顶行最前且带 ▲（可与 * 叠加）；
// 再切一次取消。
func TestPinTogglesAndSortsFirst(t *testing.T) {
	s, store, _ := newBatchSession(t, "甲", "乙") // 当前 = 乙（内存替身里 UpdatedAt 同刻，按 ID 排）
	out, err := handleCmd(s, "pin", memID(t, store, "甲"))
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	if want := i18n.Tf("tree.pin.on", "id", memID(t, store, "甲")); !strings.Contains(out, want) {
		t.Fatalf("pin on = %q, want %q", out, want)
	}
	out, err = handleCmd(s, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	lines := strings.Split(out, "\n")
	if !strings.HasPrefix(lines[0], "▲") || !strings.Contains(lines[0], "甲") {
		t.Fatalf("第一行 = %q, want 置顶的甲带 ▲", lines[0])
	}
	for _, ln := range lines[1:] {
		if strings.Contains(ln, "▲") {
			t.Fatalf("未置顶行不该带 ▲: %q", ln)
		}
	}
	// 置顶打在当前会话上：▲ 与 * 叠加。
	if _, err := handleCmd(s, "pin", memID(t, store, "乙")); err != nil {
		t.Fatalf("pin current: %v", err)
	}
	out, err = handleCmd(s, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "乙") && !strings.Contains(ln, "▲*") {
			t.Fatalf("当前且置顶行 = %q, want ▲*", ln)
		}
	}
	if _, err := handleCmd(s, "pin", memID(t, store, "甲")); err != nil {
		t.Fatalf("pin off: %v", err)
	}
	if memConv(t, store, "甲").Pinned {
		t.Fatal("second /pin must unpin 甲")
	}
}

// TestTagPinOtherConversationLeavesCurrent 对非当前会话打标签/置顶：走载入-改-存，
// 当前会话不落盘、不被换掉（照 /title 的 --id 模板）。
func TestTagPinOtherConversationLeavesCurrent(t *testing.T) {
	s, store, _ := newBatchSession(t, "甲", "乙")
	before := s.Current().Snapshot()
	saves := store.saves
	if _, err := handleCmd(s, "tag", memID(t, store, "甲"), "工作"); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if _, err := handleCmd(s, "pin", memID(t, store, "甲")); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if s.Current().ID != before.ID {
		t.Fatalf("current switched to %s, want unchanged", s.Current().ID)
	}
	after := s.Current().Snapshot()
	if len(after.Tags) != 0 || after.Pinned {
		t.Fatalf("current conversation must be untouched: %+v", after)
	}
	if store.saves != saves+2 {
		t.Fatalf("saves = %d, want +2（只有目标会话落盘）", store.saves)
	}
}

// TestForkLeavesTagsAndPinBehind 分叉不带源会话的标签与置顶（D313 决策 5）。
func TestForkLeavesTagsAndPinBehind(t *testing.T) {
	s, _, u, _, _ := forkFixture(t)
	s.Current().Tags = []string{"工作"}
	s.Current().Pinned = true
	if _, err := handleCmd(s, "fork", string(u)); err != nil {
		t.Fatalf("fork: %v", err)
	}
	forked := s.Current()
	if len(forked.Tags) != 0 || forked.Pinned {
		t.Fatalf("fork tags/pinned = %v/%v, want empty/false", forked.Tags, forked.Pinned)
	}
}
