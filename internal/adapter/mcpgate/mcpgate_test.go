package mcpgate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ---------------------------------------------------------------------------
// 假 MCP server（go-sdk server 端；http 形态经 httptest、stdio 形态经辅助进程）
// ---------------------------------------------------------------------------

type echoIn struct {
	Text string `json:"text"`
}

// newTestServer 测试 server：echo/fail/hang 工具、文本与二进制资源、带参 prompt。
func newTestServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fake-mcp", Version: "0.1.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "echo", Description: "echo text"},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, map[string]string, error) {
			return nil, map[string]string{"echo": in.Text}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "fail", Description: "always fails"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
			return nil, nil, errors.New("intentional failure")
		})
	mcp.AddTool(s, &mcp.Tool{Name: "hang", Description: "blocks until canceled"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
			// 有界阻塞：等客户端取消；兜底 2s 自行返回，避免拖死测试收尾。
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(2 * time.Second):
				return nil, map[string]string{"late": "too late"}, nil
			}
		})
	s.AddResource(&mcp.Resource{
		Name: "notes", URI: "fake://notes", Description: "记事本", MIMEType: "text/plain",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
			{URI: "fake://notes", MIMEType: "text/plain", Text: "buy milk\nsecond line"},
		}}, nil
	})
	s.AddResource(&mcp.Resource{
		Name: "blob", URI: "fake://blob", MIMEType: "application/octet-stream",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
			{URI: "fake://blob", MIMEType: "application/octet-stream", Blob: []byte{1, 2, 3}},
		}}, nil
	})
	s.AddPrompt(&mcp.Prompt{
		Name:        "greet",
		Description: "greeting prompt",
		Arguments: []*mcp.PromptArgument{
			{Name: "who", Required: true},
			{Name: "mood", Required: false},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		text := "Hello " + req.Params.Arguments["who"]
		if m := req.Params.Arguments["mood"]; m != "" {
			text += "（" + m + "）"
		}
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.TextContent{Text: text}},
		}}, nil
	})
	return s
}

// fakeSecrets port.Secrets 测试替身。
type fakeSecrets struct{ vals map[string]string }

func (f fakeSecrets) Get(_ context.Context, name string) (string, error) {
	v, ok := f.vals[name]
	if !ok {
		return "", errors.New("no secret " + name)
	}
	return v, nil
}

// httpDecl 起一个 streamable-http 假 server 并返回可拨的声明。
// handler 与 server 实例在请求之外构造一次：每请求重建会丢会话状态（session not found）。
func httpDecl(t *testing.T, extra func(*testing.T, plugin.Decl) plugin.Decl) plugin.Decl {
	t.Helper()
	srvImpl := newTestServer()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srvImpl }, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	d := plugin.Decl{
		Name: "fake",
		MCPConfig: plugin.MCPConfig{
			Transport: plugin.TransportHTTP,
			URL:       srv.URL,
		},
		Source: plugin.SourceConfig,
	}
	if extra != nil {
		d = extra(t, d)
	}
	return d
}

// dial 测试内拨号（无 Secrets）。
func dial(t *testing.T, d plugin.Decl) *Server {
	t.Helper()
	s, err := Dial(context.Background(), nil, d)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ---------------------------------------------------------------------------
// 发现 / 调用 / 授权外的协议映射
// ---------------------------------------------------------------------------

// TestDialHTTPTools 发现：tools/list 投影为 mcp:<server>:<tool>，风险与 schema 正确。
func TestDialHTTPTools(t *testing.T) {
	d := httpDecl(t, func(t *testing.T, d plugin.Decl) plugin.Decl {
		d.Headers = map[string]string{"Authorization": "Bearer test-token"}
		d.Risk = plugin.RiskConfirm
		return d
	})
	secrets := fakeSecrets{vals: map[string]string{}}
	s, err := Dial(context.Background(), secrets, d)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer s.Close()

	tools, err := s.Tools(context.Background())
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	byName := map[string]tool.Spec{}
	for _, tt := range tools {
		byName[tt.Spec().Name] = tt.Spec()
	}
	echo, ok := byName["mcp:fake:echo"]
	if !ok {
		t.Fatalf("缺 mcp:fake:echo: %v", byName)
	}
	if echo.Description != "echo text" || echo.Risk != tool.Confirm {
		t.Fatalf("echo spec = %+v, want confirm 风险与描述", echo)
	}
	var schema map[string]any
	if err := json.Unmarshal(echo.Schema, &schema); err != nil || schema["type"] != "object" {
		t.Fatalf("schema = %s（%v），want object", echo.Schema, err)
	}
	for _, want := range []string{"mcp:fake:fail", "mcp:fake:hang"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("缺 %s", want)
		}
	}
}

// TestToolExecute 调用：成功回填 Output；服务端工具错误经 error 上交（runner 转 OK:false）。
func TestToolExecute(t *testing.T) {
	s := dial(t, httpDecl(t, nil))
	tools, err := s.Tools(context.Background())
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	var echo, fail port.Tool
	for _, tt := range tools {
		switch tt.Spec().Name {
		case "mcp:fake:echo":
			echo = tt
		case "mcp:fake:fail":
			fail = tt
		}
	}

	args, _ := json.Marshal(map[string]any{"text": "ping"})
	res, err := echo.Execute(context.Background(), tool.Call{ID: "c1", Name: "mcp:fake:echo", Args: args})
	if err != nil || !res.OK {
		t.Fatalf("echo = %+v, %v", res, err)
	}
	if !strings.Contains(res.Output, "ping") {
		t.Fatalf("output = %q, want 含 ping（structured content）", res.Output)
	}

	_, err = fail.Execute(context.Background(), tool.Call{ID: "c2", Name: "mcp:fake:fail"})
	if err == nil || !strings.Contains(err.Error(), "intentional failure") {
		t.Fatalf("fail err = %v, want 服务端错误文本", err)
	}

	st := s.Stats()
	if st.Calls != 2 || st.Errors != 1 {
		t.Fatalf("stats = %+v, want Calls=2 Errors=1", st)
	}
}

// TestToolExecuteTimeout 超时：父 ctx 截止 → 错误携带 DeadlineExceeded（runner 判超时类）。
func TestToolExecuteTimeout(t *testing.T) {
	s := dial(t, httpDecl(t, nil))
	tools, err := s.Tools(context.Background())
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	var hang port.Tool
	for _, tt := range tools {
		if tt.Spec().Name == "mcp:fake:hang" {
			hang = tt
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = hang.Execute(ctx, tool.Call{ID: "c1", Name: "mcp:fake:hang"})
	if err == nil {
		t.Fatal("want timeout error")
	}
	// ctx 截止应立即返回（远早于服务端 2s 兜底），证明 ctx 贯穿到 CallTool。
	if time.Since(start) > time.Second {
		t.Fatalf("未及时返回: %v", time.Since(start))
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded 链", err)
	}
}

// TestResourceStore resources 只读投影：索引/读取/拒绝写/关键词检索。
func TestResourceStore(t *testing.T) {
	s := dial(t, httpDecl(t, nil))
	mem := s.Memory()

	entries, err := mem.Index(context.Background())
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	names := map[string]string{}
	for _, e := range entries {
		names[e.Name] = e.Summary
	}
	if sum, ok := names["fake://notes"]; !ok || !strings.Contains(sum, "记事本") {
		t.Fatalf("索引缺 notes 或摘要不含描述: %v", names)
	}

	doc, err := mem.Read(context.Background(), "fake://notes")
	if err != nil || !strings.Contains(doc.Content, "buy milk") {
		t.Fatalf("read = %+v, %v", doc, err)
	}
	if _, err := mem.Read(context.Background(), "fake://blob"); err == nil || !strings.Contains(err.Error(), "二进制") {
		t.Fatalf("二进制资源应拒绝: %v", err)
	}
	if err := mem.Write(context.Background(), port.MemoryDoc{Name: "x"}); err == nil || !strings.Contains(err.Error(), "只读") {
		t.Fatalf("写应拒绝: %v", err)
	}
	if err := mem.Remove(context.Background(), "x"); err == nil {
		t.Fatal("删除应拒绝")
	}
	hits, err := mem.Search(context.Background(), "记事")
	if err != nil || len(hits) != 1 || hits[0].Name != "fake://notes" {
		t.Fatalf("hits = %+v, %v", hits, err)
	}
	if hits, _ := mem.Search(context.Background(), "不存在的词"); len(hits) != 0 {
		t.Fatalf("miss 应为空: %+v", hits)
	}
}

// TestRenderPrompt prompts：参数位次映射、必填缺失报用法、文本拼接。
func TestRenderPrompt(t *testing.T) {
	s := dial(t, httpDecl(t, nil))

	prompts, err := s.Prompts(context.Background())
	if err != nil || len(prompts) != 1 || prompts[0].Name != "greet" {
		t.Fatalf("prompts = %+v, %v", prompts, err)
	}

	got, err := s.RenderPrompt(context.Background(), "greet", []string{"tony"})
	if err != nil || !strings.Contains(got, "Hello tony") {
		t.Fatalf("render = %q, %v", got, err)
	}
	got, err = s.RenderPrompt(context.Background(), "greet", []string{"tony", "开心"})
	if err != nil || !strings.Contains(got, "（开心）") {
		t.Fatalf("两参 render = %q, %v", got, err)
	}

	_, err = s.RenderPrompt(context.Background(), "greet", nil)
	if err == nil || !strings.Contains(err.Error(), "用法:") || !strings.Contains(err.Error(), "<who>") {
		t.Fatalf("缺必填参数应报用法: %v", err)
	}
}

// TestResolveSecrets secret: 引用经 port.Secrets 解析；缺失/明文混用正确。
func TestResolveSecrets(t *testing.T) {
	ctx := context.Background()
	secrets := fakeSecrets{vals: map[string]string{"TOK": "s3cret"}}
	got, err := resolveRefs(ctx, secrets, map[string]string{
		"Authorization": "secret:TOK",
		"X-Plain":       "plain-value",
	})
	if err != nil || got["Authorization"] != "s3cret" || got["X-Plain"] != "plain-value" {
		t.Fatalf("got = %v, %v", got, err)
	}
	if _, err := resolveRefs(ctx, secrets, map[string]string{"K": "secret:MISSING"}); err == nil {
		t.Fatal("缺失密钥应报错")
	}
	if _, err := resolveRefs(ctx, nil, map[string]string{"K": "secret:TOK"}); err == nil {
		t.Fatal("无 Secrets 端口应报错")
	}
}

// ---------------------------------------------------------------------------
// stdio 传输（辅助进程）
// ---------------------------------------------------------------------------

// TestHelperMCPServer 测试辅助进程：以 stdio 跑假 MCP server（仅 env 门控时激活）。
func TestHelperMCPServer(t *testing.T) {
	if os.Getenv("AQUARIUS_MCPGATE_HELPER") != "1" {
		t.Skip("helper process（由 TestDialStdio 拉起）")
	}
	if err := newTestServer().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "helper server: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// TestHelperCrashServer 连接成功后 100ms 自杀（真进程崩溃模拟，仅 env 门控）。
func TestHelperCrashServer(t *testing.T) {
	if os.Getenv("AQUARIUS_MCPGATE_CRASH_HELPER") != "1" {
		t.Skip("helper process（由 TestDialStdioCrashWait 拉起）")
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(1)
	}()
	_ = newTestServer().Run(context.Background(), &mcp.StdioTransport{})
	os.Exit(0)
}

// TestDialStdioCrashWait 崩溃检测原语（§6.4 #4 重启的前提）：
// 子进程异常退出后 Wait 返回非空报因，宿主据此计次重启。
func TestDialStdioCrashWait(t *testing.T) {
	d := plugin.Decl{
		Name: "crash",
		MCPConfig: plugin.MCPConfig{
			Transport: plugin.TransportStdio,
			Command:   os.Args[0],
			Args:      []string{"-test.run=TestHelperCrashServer"},
			Env:       map[string]string{"AQUARIUS_MCPGATE_CRASH_HELPER": "1"},
		},
		Source: plugin.SourceConfig,
	}
	s, err := Dial(context.Background(), nil, d)
	if err != nil {
		t.Fatalf("dial stdio: %v", err)
	}
	start := time.Now()
	werr := s.Wait()
	if werr == nil {
		t.Fatal("进程异常退出，Wait 应返回报因")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Wait 迟迟未返回: %v", time.Since(start))
	}
	_ = s.Close()
}

// TestDialStdio stdio 传输端到端：spawn 子进程 → 发现/调用 → 优雅关闭。
func TestDialStdio(t *testing.T) {
	d := plugin.Decl{
		Name: "fake",
		MCPConfig: plugin.MCPConfig{
			Transport: plugin.TransportStdio,
			Command:   os.Args[0],
			Args:      []string{"-test.run=TestHelperMCPServer"},
			Env:       map[string]string{"AQUARIUS_MCPGATE_HELPER": "1"},
		},
		Source: plugin.SourceConfig,
	}
	s, err := Dial(context.Background(), nil, d)
	if err != nil {
		t.Fatalf("dial stdio: %v", err)
	}
	tools, err := s.Tools(context.Background())
	if err != nil || len(tools) == 0 {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	if tools[0].Spec().Name != "mcp:fake:echo" && len(tools) < 3 {
		t.Fatalf("tools = %d", len(tools))
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
