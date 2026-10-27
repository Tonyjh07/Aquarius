package exchange

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// TestMaterializeReassignsIDs 源 ID 只用来建边：两次导入同一份数据得到两棵互不相干的树，
// 源里的 ID 一个都不带进来（DESIGN §5.16「落成新会话」）。
func TestMaterializeReassignsIDs(t *testing.T) {
	in := importedTree{
		Title: "重分配",
		Nodes: []importedNode{
			{SrcID: "src-1", Role: conversation.RoleUser,
				Parts: []conversation.Part{{Kind: conversation.PartText, Text: "你好"}}},
			{SrcID: "src-2", Role: conversation.RoleAssistant, Parent: "src-1",
				Parts: []conversation.Part{{Kind: conversation.PartText, Text: "在"}}},
		},
	}
	a, err := materialize(in)
	if err != nil {
		t.Fatalf("materialize#1: %v", err)
	}
	b, err := materialize(in)
	if err != nil {
		t.Fatalf("materialize#2: %v", err)
	}
	if a.ID == b.ID {
		t.Fatalf("两次导入用了同一个会话 ID: %s", a.ID)
	}
	srcIDs := map[string]bool{"src-1": true, "src-2": true}
	seen := map[conversation.MessageID]bool{}
	for _, c := range []*conversation.Conversation{a, b} {
		for _, m := range c.Snapshot().Nodes {
			if srcIDs[string(m.ID)] {
				t.Fatalf("源 ID %q 被带进了新树", m.ID)
			}
			if seen[m.ID] && c == b {
				t.Fatalf("节点 ID %q 在两次导入里重复", m.ID)
			}
			seen[m.ID] = true
		}
	}
}

// TestMaterializeHeadFallback 源里没声明当前节点时取最深的一条链，不随机挑。
func TestMaterializeHeadFallback(t *testing.T) {
	in := importedTree{Nodes: []importedNode{
		{SrcID: "a", Role: conversation.RoleUser,
			Parts: []conversation.Part{{Kind: conversation.PartText, Text: "一"}}},
		{SrcID: "b", Role: conversation.RoleAssistant, Parent: "a",
			Parts: []conversation.Part{{Kind: conversation.PartText, Text: "二"}}},
		{SrcID: "c", Role: conversation.RoleUser, Parent: "b",
			Parts: []conversation.Part{{Kind: conversation.PartText, Text: "三"}}},
	}}
	c, err := materialize(in)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	path := c.Path()
	if len(path) != 4 || path[3].Content[0].Text != "三" {
		t.Fatalf("Head 链 = %+v, want 到底部那条（三）", path)
	}
}

// TestMaterializeRejectsBadInput checkImported 的三条：空 ID、重复 ID、不认的角色。
func TestMaterializeRejectsBadInput(t *testing.T) {
	ok := []conversation.Part{{Kind: conversation.PartText, Text: "x"}}
	cases := map[string]struct {
		nodes []importedNode
		want  string
	}{
		"空 ID":  {[]importedNode{{SrcID: "", Role: conversation.RoleUser, Parts: ok}}, "没有 ID"},
		"重复 ID": {[]importedNode{{SrcID: "a", Role: conversation.RoleUser, Parts: ok}, {SrcID: "a", Role: conversation.RoleAssistant, Parts: ok}}, "重复"},
		"不认角色":  {[]importedNode{{SrcID: "a", Role: conversation.Role("critic"), Parts: ok}}, "角色"},
		"没有消息":  {nil, "没有消息"},
	}
	for name, tc := range cases {
		_, err := materialize(importedTree{Nodes: tc.nodes})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want 含 %q", name, err, tc.want)
		}
	}
}

// TestMaterializeRejectsTooManyNodes 节点数超限报因，不静默截断。
func TestMaterializeRejectsTooManyNodes(t *testing.T) {
	nodes := make([]importedNode, 0, maxImportNodes+1)
	prev := ""
	for i := 0; i <= maxImportNodes; i++ {
		id := fmt.Sprintf("n%d", i)
		nodes = append(nodes, importedNode{
			SrcID:  id,
			Role:   conversation.RoleUser,
			Parts:  []conversation.Part{{Kind: conversation.PartText, Text: "x"}},
			Parent: prev,
		})
		prev = id
	}
	_, err := materialize(importedTree{Nodes: nodes})
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("err = %v, want 节点数上限", err)
	}
}

// TestMaterializeRejectsTooMuchText 正文合计超限报因。
func TestMaterializeRejectsTooMuchText(t *testing.T) {
	// 一条就超：正文上限是 64 MB，这里给 64 MB + 1 字节的一次分配（不逐条堆，免得测试变慢）。
	big := strings.Repeat("a", maxImportTextBytes+1)
	_, err := materialize(importedTree{Nodes: []importedNode{
		{SrcID: "a", Role: conversation.RoleUser,
			Parts: []conversation.Part{{Kind: conversation.PartText, Text: big}}},
	}})
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("err = %v, want 正文上限", err)
	}
}

// TestMaterializeRejectsDanglingParent 悬空父交给 Restore 自检报因，不「尽力修复」。
func TestMaterializeRejectsDanglingParent(t *testing.T) {
	_, err := materialize(importedTree{Nodes: []importedNode{
		{SrcID: "a", Role: conversation.RoleUser, Parent: "nope",
			Parts: []conversation.Part{{Kind: conversation.PartText, Text: "x"}}},
	}})
	if err == nil || !strings.Contains(err.Error(), "父节点") {
		t.Fatalf("err = %v, want 父节点不在文件里", err)
	}
}

// TestNodeTimeFallsBack 时间为零时退回会话创建时间（字段缺失不等于 1970 年）。
func TestNodeTimeFallsBack(t *testing.T) {
	fallback := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	if got := nodeTime(importedNode{}, fallback); !got.Equal(fallback) {
		t.Fatalf("nodeTime(零值) = %v, want %v", got, fallback)
	}
	given := time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := nodeTime(importedNode{Created: given}, fallback); !got.Equal(given) {
		t.Fatalf("nodeTime(有值) = %v, want %v", got, given)
	}
}

// TestBuildEdgesOrder 兄弟顺序按源里 children 的次序（D152：图上左→右）。
func TestBuildEdgesOrder(t *testing.T) {
	nodes := []importedNode{
		{SrcID: "r", Children: []string{"b", "a"}, Role: conversation.RoleUser,
			Parts: []conversation.Part{{Kind: conversation.PartText, Text: "r"}}},
		{SrcID: "a", Parent: "r", Role: conversation.RoleAssistant,
			Parts: []conversation.Part{{Kind: conversation.PartText, Text: "a"}}},
		{SrcID: "b", Parent: "r", Role: conversation.RoleAssistant,
			Parts: []conversation.Part{{Kind: conversation.PartText, Text: "b"}}},
	}
	// "" 是树外挂点（Root），与源里的顶层节点是两个不同的 ID。
	newIDs := map[string]conversation.MessageID{"": "R", "r": "X", "a": "A", "b": "B"}
	edges, err := buildEdges(nodes, newIDs, "R")
	if err != nil {
		t.Fatalf("buildEdges: %v", err)
	}
	var kids []conversation.MessageID
	for _, e := range edges {
		if e.Parent == "X" {
			kids = e.Childs
		}
	}
	if len(kids) != 2 || kids[0] != "B" || kids[1] != "A" {
		t.Fatalf("兄弟顺序 = %v, want [B A]（按源里 children 的次序）", kids)
	}
	// 顶层节点自己仍要挂到 Root 下（不然它没有父边）。
	found := false
	for _, e := range edges {
		if e.Parent == "R" && len(e.Childs) == 1 && e.Childs[0] == "X" {
			found = true
		}
	}
	if !found {
		t.Fatalf("顶层节点没挂到 Root 下: %+v", edges)
	}
}
