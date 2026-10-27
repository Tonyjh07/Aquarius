package exchange

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestImportOpenWebUIHistoryShape 旧版形状：`chat.history.messages` 是对象，
// `currentId` 指当前节点。结构与 head 都要对。
func TestImportOpenWebUIHistoryShape(t *testing.T) {
	const doc = `{
	  "title": "外层标题",
	  "chat": {
	    "title": "会话标题",
	    "history": {
	      "currentId": "a1",
	      "messages": {
	        "r": {"id": "r", "parentId": null, "childrenIds": ["u"], "role": "assistant", "content": ""},
	        "u": {"id": "u", "parentId": "r", "childrenIds": ["a1", "a2"], "role": "user", "content": "你好", "timestamp": 1700000001},
	        "a1": {"id": "a1", "parentId": "u", "childrenIds": [], "role": "assistant", "content": "回答一", "timestamp": 1700000002},
	        "a2": {"id": "a2", "parentId": "u", "childrenIds": [], "role": "assistant", "content": "回答二", "timestamp": 1700000003}
	      }
	    }
	  }
	}`
	c, rep, err := New(Options{}).Import(context.Background(), strings.NewReader(doc),
		port.ExchangeFormat{Name: "openwebui"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if c.Title != "会话标题" {
		t.Fatalf("标题 = %q, want 会话标题（内层的优先）", c.Title)
	}
	if rep.Nodes != 3 || c.NodeCount() != 4 {
		t.Fatalf("节点数 = %d/%d, want 3 条消息 + Root", rep.Nodes, c.NodeCount())
	}
	path := c.Path()[1:]
	if len(path) != 2 || path[1].Content[0].Text != "回答一" {
		t.Fatalf("Head 链 = %+v, want u→a1", path)
	}
	// 旁支仍在：分叉数应为 1。
	branches := 0
	for _, e := range c.Snapshot().Edges {
		if len(e.Childs) > 1 {
			branches++
		}
	}
	if branches != 1 {
		t.Fatalf("分叉数 = %d, want 1：%+v", branches, c.Snapshot().Edges)
	}
}

// TestImportOpenWebUIArrayShape 新版形状：`chat.messages` 是数组，每项自带 parentId。
// 时间戳是 Unix 秒。
func TestImportOpenWebUIArrayShape(t *testing.T) {
	const doc = `{
	  "title": "数组形状",
	  "chat": {
	    "messages": [
	      {"id": "u", "parentId": null, "childrenIds": ["a"], "role": "user", "content": "问", "timestamp": 1700000001},
	      {"id": "a", "parentId": "u", "childrenIds": [], "role": "assistant", "content": "答", "timestamp": 1700000002}
	    ]
	  }
	}`
	c, rep, err := New(Options{}).Import(context.Background(), strings.NewReader(doc),
		port.ExchangeFormat{Name: "openwebui"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if c.Title != "数组形状" {
		t.Fatalf("标题 = %q", c.Title)
	}
	if rep.Nodes != 2 {
		t.Fatalf("节点数 = %d, want 2", rep.Nodes)
	}
	path := c.Path()[1:]
	if len(path) != 2 || path[0].Content[0].Text != "问" {
		t.Fatalf("Head 链 = %+v", path)
	}
	if path[0].CreatedAt.Unix() != 1700000001 {
		t.Fatalf("时间戳 = %v, want 1700000001（Unix 秒）", path[0].CreatedAt.Unix())
	}
}

// TestImportOpenWebUISkipsEmptyShell 它的导出里也常有空壳节点（没有正文）：不建空节点，
// 但子节点要接上，否则链路断。
func TestImportOpenWebUISkipsEmptyShell(t *testing.T) {
	const doc = `{
	  "chat": {
	    "history": {
	      "currentId": "a",
	      "messages": {
	        "r": {"id": "r", "parentId": null, "childrenIds": ["u"], "role": "assistant", "content": ""},
	        "u": {"id": "u", "parentId": "r", "childrenIds": ["shell"], "role": "user", "content": "问"},
	        "shell": {"id": "shell", "parentId": "u", "childrenIds": ["a"], "role": "assistant", "content": ""},
	        "a": {"id": "a", "parentId": "shell", "childrenIds": [], "role": "assistant", "content": "答"}
	      }
	    }
	  }
	}`
	c, _, err := New(Options{}).Import(context.Background(), strings.NewReader(doc),
		port.ExchangeFormat{Name: "openwebui"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if c.NodeCount() != 3 {
		t.Fatalf("节点数 = %d, want 3（Root + 问 + 答；空壳不建节点）", c.NodeCount())
	}
	path := c.Path()[1:]
	if len(path) != 2 || path[1].Content[0].Text != "答" {
		t.Fatalf("链路 = %+v, want 问→答", path)
	}
}

// TestImportOpenWebUIRejects 坏文件报因：空会话、悬空父、无法解析。
func TestImportOpenWebUIRejects(t *testing.T) {
	cases := map[string]struct {
		doc  string
		want string
	}{
		"没有消息":    {`{"chat":{}}`, "没有消息"},
		"悬空父":     {`{"chat":{"messages":[{"id":"a","parentId":"nope","role":"user","content":"x"}]}}`, "父节点"},
		"不是 JSON": {`not json`, "解析"},
	}
	for name, tc := range cases {
		_, _, err := New(Options{}).Import(context.Background(), strings.NewReader(tc.doc),
			port.ExchangeFormat{Name: "openwebui"})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want 含 %q", name, err, tc.want)
		}
	}
}

// TestOpenWebUIIsReadOnlyOpenWebUI 只认不写：导出必须明确拒绝，并指出该用哪一份代替。
func TestOpenWebUIIsReadOnlyOpenWebUI(t *testing.T) {
	_, err := New(Options{}).Export(context.Background(),
		conversation.New(conversation.ID("c1"), "t"), "openwebui", nopWriter{})
	if err == nil || !strings.Contains(err.Error(), "chatgpt") {
		t.Fatalf("err = %v, want 指向 chatgpt 那份", err)
	}
}

// TestDetectOpenWebUI 文件头认 openwebui；`{"messages":[]}`（openai 形状）不该被它抢走。
func TestDetectOpenWebUI(t *testing.T) {
	ex := New(Options{})
	if f, ok := ex.Detect("chat.json", []byte(`{"title":"t","chat":{"history":{"messages":{}}}}`)); !ok || f.Name != "openwebui" {
		t.Fatalf("带 history 的 chat 应认成 openwebui: %+v %v", f, ok)
	}
	if f, ok := ex.Detect("chat.json", []byte(`{"chat":{"messages":[`)); !ok || f.Name != "openwebui" {
		t.Fatalf("截断但带 chat 的头部也应认成 openwebui: %+v %v", f, ok)
	}
	if f, ok := ex.Detect("x.json", []byte(`{"messages":[]}`)); !ok || f.Name != "openai" {
		t.Fatalf("裸 messages 数组应认成 openai: %+v %v", f, ok)
	}
}

// nopWriter 丢弃写入（只要错误）。
type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
