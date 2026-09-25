package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMCPFixture 假 MCP server：echo 工具 + greet prompt（验收用最小面）。
func newMCPFixture() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "0.1.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "echo", Description: "echo text"},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			Text string `json:"text"`
		}) (*mcp.CallToolResult, map[string]string, error) {
			return nil, map[string]string{"echo": in.Text}, nil
		})
	s.AddPrompt(&mcp.Prompt{
		Name:        "greet",
		Description: "greeting prompt",
		Arguments:   []*mcp.PromptArgument{{Name: "who", Required: true}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.TextContent{Text: "Hello " + req.Params.Arguments["who"]}},
		}}, nil
	})
	return s
}

// writeMCPConfig 可运行 config：模型 + 一个 streamable-http MCP 声明（无能力，免授权）。
func writeMCPConfig(t *testing.T, dir, llmURL, model, mcpURL string) {
	t.Helper()
	cfg := fmt.Sprintf(`{
  "model": {"provider":"openai-compatible","name":%q,"base_url":%q,"api_key":"secret:AQ_E2E_KEY"},
  "ui": {"kind":"repl"},
  "mcpServers": {"fake": {"transport":"streamable-http","url":%q}},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, model, llmURL, mcpURL)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("AQ_E2E_KEY", "test-key")
}

// TestRunPluginMCPFullChain M4 验收（DESIGN §11 e2e / §12）：装配假 MCP server →
// 启动即连接（/plugin 就绪）→ 模型经 mcp:fake:echo 工具完成发现/调用/回填全链路 →
// /plugin 展示调用统计 → /mcp:fake:greet 动态命令渲染为用户输入走完整 Turn。
func TestRunPluginMCPFullChain(t *testing.T) {
	fixture := newMCPFixture()
	mcpSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return fixture }, nil))
	defer mcpSrv.Close()

	llmSrv, reqs := rawScriptServer(t, [][]string{
		{toolCallData("c1", "mcp:fake:echo", `{"text":"ping"}`)},
		{contentData("执行完成")},
		{contentData("greeted")},
	})
	dir := t.TempDir()
	writeMCPConfig(t, dir, llmSrv.URL, "m", mcpSrv.URL)

	var out bytes.Buffer
	stdin := "帮我调用工具\n/plugin\n/mcp:fake:greet tony\n/quit\n"
	if code := run([]string{"-data", dir}, strings.NewReader(stdin), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"[tool ok]", // MCP 工具执行回填
		"执行完成",      // 第二轮回答
		"[ready]",   // /plugin：启动即连接
		"fake",      // 服务器名
		"config/streamable-http",
		"调用1",             // §6.4 #5 调用统计
		"/mcp:fake:greet", // 动态命令在列
		"greeted",         // prompt 触发的回答
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout 缺 %q: %q", want, got)
		}
	}
	if len(*reqs) != 3 {
		t.Fatalf("llm requests = %d, want 3（工具调用/回答/prompt 轮）", len(*reqs))
	}
	// 请求1 带 mcp: 工具声明（发现）。
	if !strings.Contains(string((*reqs)[0].body), "mcp:fake:echo") {
		t.Fatalf("请求1 缺 mcp 工具声明: %.300s", (*reqs)[0].body)
	}
	// 请求2 回填 MCP 结果（tool 消息里是 JSON 转义后的 structured content 文本）。
	if !strings.Contains(string((*reqs)[1].body), "\\\"echo\\\":\\\"ping\\\"") {
		t.Fatalf("请求2 缺 MCP 工具结果: %.400s", (*reqs)[1].body)
	}
	// 请求3 的用户消息 = prompt 渲染文本（greet 命令 → Handle(Text)）。
	if !strings.Contains(string((*reqs)[2].body), "Hello tony") {
		t.Fatalf("请求3 缺 prompt 渲染输入: %.400s", (*reqs)[2].body)
	}
}
