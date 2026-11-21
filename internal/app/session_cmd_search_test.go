package app

// session_cmd_search_test.go `/search` 的命令面行为（D322）：
// 标题命中、正文命中（行带节点 ID 与预览）、--regex、--tag 交集过滤、思考分片不搜、
// 空结果、用法错（无参数 / 非法正则）。

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// searchSeed 两会话夹具：甲（标题含「工作」、正文提到「咖啡」）、乙（标题「闲聊」、
// 正文含「量子隧穿」、另一节点只带思考分片）。
func searchSeed(t *testing.T) (*Session, *memStore, string, string) {
	t.Helper()
	s, store, _ := newBatchSession(t, "工作备忘", "闲聊")
	seed := func(title, text string, parts ...conversation.Part) {
		t.Helper()
		// 当前会话用活树（execSearch 对当前会话读 s.cur、其余走 store.Load——生产里
		// 活树就是权威），其余从库里取副本再存回去。
		c := s.cur
		if c.Title != title {
			var err error
			c, err = store.Load(context.Background(), conversation.ID(memID(t, store, title)))
			if err != nil {
				t.Fatalf("load %q: %v", title, err)
			}
		}
		if _, err := c.Append(conversation.RoleUser, parts); err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := store.Save(context.Background(), c); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	seed("工作备忘", "", conversation.Part{Kind: conversation.PartText, Text: "楼下咖啡店明天涨价"})
	seed("闲聊", "", conversation.Part{Kind: conversation.PartText, Text: "聊到量子隧穿"})
	// 思考分片只能挂 assistant 节点：直接构造 committed 节点入树。
	yi := memConv(t, store, "闲聊")
	root, _ := yi.Find(conversation.MessageID(yi.ID)) // Root 的 ID = 会话 ID
	th := conversation.Message{ID: conversation.MessageID("T1"), Parent: root.ID, Role: conversation.RoleAssistant,
		Content: []conversation.Part{{Kind: conversation.PartThinking, Text: "秘密思考不搜"}}}
	if err := yi.AppendCommitted(th); err != nil {
		t.Fatalf("append thinking: %v", err)
	}
	if err := store.Save(context.Background(), yi); err != nil {
		t.Fatalf("save thinking: %v", err)
	}
	return s, store, memID(t, store, "工作备忘"), memID(t, store, "闲聊")
}

// TestSearchTitleAndContent 标题命中给会话行；正文命中行带节点 ID 与预览；
// 思考分片不作命中来源。
func TestSearchTitleAndContent(t *testing.T) {
	s, store, idJia, idYi := searchSeed(t)
	yi := memConv(t, store, "闲聊")

	// 标题命中：只给会话行。
	out, err := handleCmd(s, "search", "备忘")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(out, idJia) || !strings.Contains(out, "工作备忘") {
		t.Fatalf("search title = %q, want 会话 ID 与标题", out)
	}
	// 标题命中不该带正文预览（那是正文命中行的事）。
	if strings.Contains(out, "咖啡") {
		t.Fatalf("search title = %q, 不该扫出正文行", out)
	}

	// 正文命中：行带会话 ID + 节点 ID + 预览。
	out, err = handleCmd(s, "search", "量子")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(out, idYi) || !strings.Contains(out, "闲聊") {
		t.Fatalf("search content = %q, want 会话 ID 与标题", out)
	}
	nodeID := ""
	for _, m := range yi.Messages() {
		if m.Role != conversation.RoleRoot && strings.Contains(searchNodeText(m), "量子") {
			nodeID = string(m.ID)
		}
	}
	if nodeID == "" || !strings.Contains(out, nodeID) {
		t.Fatalf("search content = %q, want 命中节点 %q", out, nodeID)
	}

	// 思考分片不搜：搜「秘密思考」无命中。
	out, err = handleCmd(s, "search", "秘密思考")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if out != i18n.T("tree.search.no_match") {
		t.Fatalf("search thinking = %q, want 无命中", out)
	}
}

// TestSearchRegexAndTag --regex 按正则（不分大小写）；--tag 与 /list --tag 同一交集口径。
func TestSearchRegexAndTag(t *testing.T) {
	s, _, idJia, idYi := searchSeed(t)

	out, err := handleCmd(s, "search", "量.+穿", "--regex")
	if err != nil {
		t.Fatalf("search regex: %v", err)
	}
	if !strings.Contains(out, idYi) {
		t.Fatalf("search regex = %q, want 命中乙", out)
	}

	// 两个会话的 user 节点都带「hi」（构造期默认问候）：不加 --tag 两处都中。
	out, err = handleCmd(s, "search", "hi")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(out, idJia) || !strings.Contains(out, idYi) {
		t.Fatalf("search = %q, want 两处都命中", out)
	}
	// 只给甲打标签后 --tag 收窄到甲。
	s2, store, _, _ := searchSeed(t)
	if _, err := handleCmd(s2, "tag", memID(t, store, "工作备忘"), "工作"); err != nil {
		t.Fatalf("tag: %v", err)
	}
	out, err = handleCmd(s2, "search", "hi", "--tag", "工作")
	if err != nil {
		t.Fatalf("search --tag: %v", err)
	}
	if !strings.Contains(out, memID(t, store, "工作备忘")) || strings.Contains(out, memID(t, store, "闲聊")) {
		t.Fatalf("search --tag = %q, want 只搜甲", out)
	}
}

// TestSearchUsageErrors 无参数与非法正则都报用法错、不搜。
func TestSearchUsageErrors(t *testing.T) {
	s, _, _, _ := searchSeed(t)
	if _, err := handleCmd(s, "search"); err == nil || err.Error() != i18n.T("usage.search") {
		t.Fatalf("search 无参数 err = %v, want 用法错", err)
	}
	if _, err := handleCmd(s, "search", "([", "--regex"); err == nil || err.Error() != i18n.T("usage.search") {
		t.Fatalf("search 非法正则 err = %v, want 用法错", err)
	}
}
