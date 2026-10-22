package llm

// D195：内部工具名 mcp:<server>:<tool> 在出站边界换成端点安全别名，回传再换回来。

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func TestWireBaseName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"file_read", "file_read"},
		{"think", "think"},
		{"mcp:terminal:terminal_open", "mcp_terminal_terminal_open"},
		{"mcp:web.search:fetch.v2", "mcp_web_search_fetch_v2"},
		{"a b\tc", "a_b_c"},
		{"mcp:中文:工具", "mcp______________"}, // 多字节逐字节落下划线（只求可移植，不求可逆）
	}
	for _, c := range cases {
		if got := wireBaseName(c.in); got != c.want {
			t.Errorf("wireBaseName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWireBaseNameLongIsStableAndCapped(t *testing.T) {
	long := "mcp:" + strings.Repeat("s", 40) + ":" + strings.Repeat("t", 40)
	got := wireBaseName(long)
	if len(got) > wireNameLimit {
		t.Fatalf("len = %d, want <= %d (%q)", len(got), wireNameLimit, got)
	}
	if again := wireBaseName(long); again != got {
		t.Fatalf("同名两次不一致: %q / %q", got, again)
	}
	other := wireBaseName(strings.ReplaceAll(long, "t", "u"))
	if other == got {
		t.Fatalf("不同原名截断后撞名: %q", got)
	}
}

func TestWireNamesRoundTripAndCollision(t *testing.T) {
	specs := []tool.Spec{
		{Name: "mcp:a:b"},
		{Name: "mcp_a_b"}, // 清洗后与上一条相同
		{Name: "think"},
	}
	wn := newWireNames(specs)
	seen := map[string]bool{}
	for _, s := range specs {
		alias := wn.Wire(s.Name)
		if alias == "" || len(alias) > wireNameLimit {
			t.Fatalf("别名非法: %q → %q", s.Name, alias)
		}
		for i := 0; i < len(alias); i++ {
			c := alias[i]
			ok := c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			if !ok {
				t.Fatalf("别名含非法字符: %q（来自 %q）", alias, s.Name)
			}
		}
		if seen[alias] {
			t.Fatalf("碰撞未消歧: %q", alias)
		}
		seen[alias] = true
		if back := wn.Local(alias); back != s.Name {
			t.Fatalf("回译 = %q, want %q", back, s.Name)
		}
	}
	// 顺序无关：同一集合换个声明顺序，别名不变。
	rev := newWireNames([]tool.Spec{specs[2], specs[1], specs[0]})
	if rev.Wire("mcp:a:b") != wn.Wire("mcp:a:b") || rev.Wire("mcp_a_b") != wn.Wire("mcp_a_b") {
		t.Fatal("别名不应依赖声明顺序")
	}
}

func TestWireNamesUnknownPassthrough(t *testing.T) {
	wn := newWireNames([]tool.Spec{{Name: "think"}})
	if got := wn.Local("model_invented_tool"); got != "model_invented_tool" {
		t.Fatalf("未知别名 = %q, want 原样", got)
	}
	// 历史里已下线的工具：按规则现算别名，且仍是端点安全名。
	if got := wn.Wire("mcp:gone:tool"); got != "mcp_gone_tool" {
		t.Fatalf("未登记内部名 = %q, want %q", got, "mcp_gone_tool")
	}
}

// strictRequest 三条协议共用的请求：声明 + 历史调用都用 MCP 内部名。
func strictRequest() port.GenerateRequest {
	const name = "mcp:terminal:terminal_open"
	return port.GenerateRequest{
		Model: "probe",
		Messages: []port.PromptMessage{
			{Role: "system", Content: []port.PromptPart{{Kind: "text", Text: "sys"}}},
			{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}},
			{Role: "assistant", ToolCalls: []tool.Call{{ID: "c1", Name: name, Args: json.RawMessage(`{"mode":"local"}`)}}},
			{Role: "tool", CallID: "c1", Content: []port.PromptPart{{Kind: "text", Text: "ok"}}},
		},
		Tools: []tool.Spec{{Name: name, Description: "open a terminal", Schema: json.RawMessage(`{"type":"object"}`)}},
	}
}

// TestEncodersKeepToolNamesPortable 三条协议的请求体都不许出现带冒号的工具名
// （DeepSeek/OpenAI 一类的严格端点会 400），且声明与历史调用用同一个别名。
func TestEncodersKeepToolNamesPortable(t *testing.T) {
	const alias = "mcp_terminal_terminal_open"
	req := strictRequest()
	encoders := map[string]func(*testing.T){
		"chat": func(t *testing.T) {
			body, err := encodeChatRequest(req, true, nil)
			if err != nil {
				t.Fatalf("chat encode: %v", err)
			}
			assertPortable(t, "chat", body, alias)
			var got struct {
				Messages []struct {
					ToolCalls []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, m := range got.Messages {
				for _, c := range m.ToolCalls {
					found = true
					if c.Function.Name != alias {
						t.Fatalf("chat 历史调用名 = %q, want %q", c.Function.Name, alias)
					}
				}
			}
			if !found {
				t.Fatal("chat 请求缺少历史工具调用")
			}
		},
		"anthropic": func(t *testing.T) {
			body, err := encodeAnthropicRequest(req)
			if err != nil {
				t.Fatalf("anthropic encode: %v", err)
			}
			assertPortable(t, "anthropic", body, alias)
		},
		"responses": func(t *testing.T) {
			body, err := encodeResponsesRequest(req)
			if err != nil {
				t.Fatalf("responses encode: %v", err)
			}
			assertPortable(t, "responses", body, alias)
		},
	}
	for name, run := range encoders {
		t.Run(name, run)
	}
}

func assertPortable(t *testing.T, protocol string, body []byte, alias string) {
	t.Helper()
	if !bytes.Contains(body, []byte(alias)) {
		t.Fatalf("%s 请求体缺端点别名 %q: %s", protocol, alias, body)
	}
	if bytes.Contains(body, []byte(`mcp:terminal`)) {
		t.Fatalf("%s 请求体泄出带冒号工具名: %s", protocol, body)
	}
}

// TestStreamLocalizesToolCallName 回传别名 → 内部名：会话树与派发只认 mcp:<server>:<tool>。
func TestStreamLocalizesToolCallName(t *testing.T) {
	const alias = "mcp_terminal_terminal_open"
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"` + alias + `","arguments":"{}"}}]}}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	req := strictRequest()
	s := newStream(t.Context(), func() {}, nopCloser{strings.NewReader(body)}, newWireNames(req.Tools))
	d, err := s.Recv()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if len(d.ToolCalls) != 1 || d.ToolCalls[0].Name != "mcp:terminal:terminal_open" {
		t.Fatalf("tool call = %+v, want 内部名", d.ToolCalls)
	}
	// 未知名原样留给 runner 报未知工具。
	s2 := newStream(t.Context(), func() {}, nopCloser{strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c2\",\"function\":{\"name\":\"made_up\"}}]}}]}\n\n")},
		newWireNames(req.Tools))
	d2, err := s2.Recv()
	if err != nil || len(d2.ToolCalls) != 1 || d2.ToolCalls[0].Name != "made_up" {
		t.Fatalf("未知名 = %+v, %v", d2.ToolCalls, err)
	}
}

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }
