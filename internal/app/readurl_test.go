package app

// readurl_test.go 服务端读链接（D310）
//
// 三件事各守一条：触发条件（地址在本轮用户消息里、链上认这一项、开关没关）、
// 形态代价（切了就不带本地工具——协议里两类工具不能共存）、续写闸门（用过就不能续）。

import (
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestURLInLastUserMessage 只认最后一条用户消息里的地址。
func TestURLInLastUserMessage(t *testing.T) {
	c := newConv(t) // Root + user "hi"
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "ok"}}); err != nil {
		t.Fatalf("append a: %v", err)
	}
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{
		{Kind: conversation.PartText, Text: "看看 https://example.com/a 这一页"},
	}); err != nil {
		t.Fatalf("append u: %v", err)
	}
	if !urlInLastUserMessage(c, []string{"https://example.com/a"}) {
		t.Fatal("地址在本轮用户消息里应命中")
	}
	if urlInLastUserMessage(c, []string{"https://example.com/b"}) {
		t.Fatal("没出现的地址不该命中")
	}
	if urlInLastUserMessage(c, nil) {
		t.Fatal("空清单不该命中")
	}
}

// TestServerReadURLSwitchesShape 切形态：声明服务端工具，且这一轮不带本地工具。
func TestServerReadURLSwitchesShape(t *testing.T) {
	conv := newConv(t)
	if _, err := conv.Append(conversation.RoleUser, []conversation.Part{
		{Kind: conversation.PartText, Text: "读 https://example.com/a"},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	a := newAgent(t, &scriptLLM{}, &recorder{}, Deps{Tools: &fakeRunner{specs: []tool.Spec{{Name: "web_fetch", Schema: tool.NoArgsSchema}}}}, Config{
		ReadURL:    true,
		ServerCaps: port.ServerCaps{ReadURL: true},
	})
	buf := newTurnBuffer("m1")
	buf.retryURLs = []string{"https://example.com/a"}

	req, _, err := a.buildRequestTracked(t.Context(), conv, buf)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(req.ServerTools) != 1 || req.ServerTools[0] != port.ServerCapReadURL {
		t.Fatalf("ServerTools = %v", req.ServerTools)
	}
	if len(req.Tools) != 0 {
		t.Fatalf("服务端阅读轮不该带本地工具，实际 %d 项", len(req.Tools))
	}
	if len(buf.retryURLs) != 0 {
		t.Fatal("retryURLs 消费后应清空")
	}
}

// TestServerReadURLGates 三道闸门任一不成立就不切：开关、能力、地址在位。
func TestServerReadURLGates(t *testing.T) {
	conv := newConv(t)
	if _, err := conv.Append(conversation.RoleUser, []conversation.Part{
		{Kind: conversation.PartText, Text: "读 https://example.com/a"},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	cases := []struct {
		name string
		cfg  Config
		urls []string
	}{
		{"开关关掉", Config{ReadURL: false, ServerCaps: port.ServerCaps{ReadURL: true}}, []string{"https://example.com/a"}},
		{"链上不认", Config{ReadURL: true, ServerCaps: port.ServerCaps{}}, []string{"https://example.com/a"}},
		{"地址不在本轮消息里", Config{ReadURL: true, ServerCaps: port.ServerCaps{ReadURL: true}}, []string{"https://example.com/b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newAgent(t, &scriptLLM{}, &recorder{}, Deps{Tools: &fakeRunner{specs: []tool.Spec{{Name: "web_fetch", Schema: tool.NoArgsSchema}}}}, tc.cfg)
			buf := newTurnBuffer("m1")
			buf.retryURLs = tc.urls
			req, _, err := a.buildRequestTracked(t.Context(), conv, buf)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if len(req.ServerTools) != 0 {
				t.Fatalf("不该切形态，实际声明了 %v", req.ServerTools)
			}
		})
	}
}

// TestRespondRejectsServerTool 用过服务端阅读的回答不能续写。
func TestRespondRejectsServerTool(t *testing.T) {
	m := conversation.Message{Content: []conversation.Part{
		{Kind: conversation.PartServer, Server: &conversation.ServerPart{Tool: port.ServerCapReadURL, Raw: []byte(`{}`)}},
	}}
	if !usesServerTool(m) {
		t.Fatal("含服务端块的节点应被认出")
	}
	plain := conversation.Message{Content: []conversation.Part{{Kind: conversation.PartText, Text: "ok"}}}
	if usesServerTool(plain) {
		t.Fatal("普通节点不该被判成用过服务端工具")
	}
}
