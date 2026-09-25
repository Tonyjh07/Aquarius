package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 现成 MCP server（D30 生态验收）：@modelcontextprotocol/server-everything——
// 官方参考实现，stdio 与 streamableHttp 双传输、含 tools/prompts/resources 全面。
// 面（v2.0.0 实测）：tool `echo(message)`、prompt `args-prompt(city必填)`、demo:// 资源。
const realMCPNpx = "@modelcontextprotocol/server-everything"

// TestRealMCPEverythingAcceptance DESIGN §12 M4 验收线："stdio 与 streamable HTTP
// 各接一个现成 MCP server 全链路可用"。依赖 npx（首拉需网络）；默认跳过，
// AQUARIUS_E2E_REAL_MCP=1 启用。
func TestRealMCPEverythingAcceptance(t *testing.T) {
	if os.Getenv("AQUARIUS_E2E_REAL_MCP") != "1" {
		t.Skip("真实 MCP server 验收（AQUARIUS_E2E_REAL_MCP=1 启用；首次需网络拉取 npx 包）")
	}

	t.Run("stdio", func(t *testing.T) {
		llmSrv, reqs := rawScriptServer(t, [][]string{
			{toolCallData("c1", "mcp:everything:echo", `{"message":"real-server-ok"}`)},
			{contentData("收到")},
			{contentData("beijing-answered")},
		})
		dir := t.TempDir()
		writeRealMCPConfig(t, dir, llmSrv.URL, "m", fmt.Sprintf(
			`{"transport":"stdio","command":"npx","args":["-y",%q]}`, realMCPNpx))

		var out bytes.Buffer
		stdin := "调用真实工具\n/plugin\n/mcp:everything:args-prompt 北京\n/quit\n"
		if code := run([]string{"-data", dir}, strings.NewReader(stdin), &out, io.Discard); code != 0 {
			t.Fatalf("code = %d, out = %q", code, out.String())
		}
		got := out.String()
		for _, want := range []string{
			"[tool ok]",                      // 真实 server 工具执行
			"real-server-ok",                 // echo 回显
			"[ready]", "everything", "stdio", // 启动即连接
			"调用1",                         // 调用统计
			"/mcp:everything:args-prompt", // 动态命令发现
			"beijing-answered",            // prompt 触发的回答
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("stdout 缺 %q: %q", want, got)
			}
		}
		if len(*reqs) != 3 {
			t.Fatalf("requests = %d, want 3", len(*reqs))
		}
		if !strings.Contains(string((*reqs)[0].body), "mcp:everything:echo") {
			t.Fatal("请求1 缺真实 server 的工具声明")
		}
		if !strings.Contains(string((*reqs)[1].body), "real-server-ok") {
			t.Fatalf("请求2 缺真实工具结果: %.400s", (*reqs)[1].body)
		}
		if !strings.Contains(string((*reqs)[2].body), "北京") {
			t.Fatalf("请求3 缺 prompt 渲染输入: %.400s", (*reqs)[2].body)
		}
	})

	t.Run("streamable-http", func(t *testing.T) {
		port := startEverythingHTTP(t)

		llmSrv, reqs := rawScriptServer(t, [][]string{
			{toolCallData("c1", "mcp:everything:echo", `{"message":"real-http-ok"}`)},
			{contentData("http-done")},
		})
		dir := t.TempDir()
		writeRealMCPConfig(t, dir, llmSrv.URL, "m", fmt.Sprintf(
			`{"transport":"streamable-http","url":"http://localhost:%d/mcp"}`, port))

		var out bytes.Buffer
		stdin := "调用真实http工具\n/quit\n"
		if code := run([]string{"-data", dir}, strings.NewReader(stdin), &out, io.Discard); code != 0 {
			t.Fatalf("code = %d, out = %q", code, out.String())
		}
		got := out.String()
		for _, want := range []string{"[tool ok]", "real-http-ok", "http-done"} {
			if !strings.Contains(got, want) {
				t.Fatalf("stdout 缺 %q: %q", want, got)
			}
		}
		if !strings.Contains(string((*reqs)[1].body), "real-http-ok") {
			t.Fatalf("请求2 缺真实工具结果: %.400s", (*reqs)[1].body)
		}
	})
}

// writeRealMCPConfig 可运行 config：指定 mcpServers 条目的 JSON 片段。
func writeRealMCPConfig(t *testing.T, dir, llmURL, model, mcpEntryJSON string) {
	t.Helper()
	cfg := fmt.Sprintf(`{
  "model": {"provider":"openai-compatible","name":%q,"base_url":%q,"api_key":"secret:AQ_E2E_KEY"},
  "ui": {"kind":"repl"},
  "mcpServers": {"everything": %s},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, model, llmURL, mcpEntryJSON)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("AQ_E2E_KEY", "test-key")
}

// startEverythingHTTP 以 streamableHttp 模式拉起现成 server，返回其端口；
// 轮询 /mcp 且响应须含 serverInfo（MCP initialize 真握手——防止命中端口上的
// 无关服务给出误导性通过）才认定就绪；退出时按平台清理整棵进程树。
func startEverythingHTTP(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("npx", "-y", realMCPNpx, "streamableHttp")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	setupAcceptCmd(cmd) // unix: 独立进程组（整组清理）；windows: taskkill /T 兜底
	if err := cmd.Start(); err != nil {
		t.Fatalf("start npx: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			killAcceptCmd(cmd)
		}
	})
	// 端口从启动日志解析（缺省 3001，实测 server-everything v2 固定值）；
	// 经 channel 传递避免与轮询循环的数据竞争。
	const defaultPort = 3001
	port := defaultPort
	portCh := make(chan int, 4)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			if idx := strings.Index(line, "port "); idx >= 0 {
				if p, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(line[idx+5:], "."))); err == nil {
					portCh <- p
				}
			}
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second) // 首拉包 + 启动
	for time.Now().Before(deadline) {
		select {
		case p := <-portCh:
			port = p
		default:
		}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
			fmt.Sprintf("http://localhost:%d/mcp", port),
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if resp, err := client.Do(req); err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), "serverInfo") {
				return port
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("streamableHttp server 未在时限内就绪")
	return 0
}
