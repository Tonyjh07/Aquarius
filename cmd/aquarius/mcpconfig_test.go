package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfigMCPFields mcpServers 声明与 plugins 状态的解析（D30/D31）。
func TestLoadConfigMCPFields(t *testing.T) {
	dir := t.TempDir()
	cfg := `{
  "model": {"provider":"openai-compatible","name":"m","base_url":"http://x","api_key":"secret:K"},
  "mcpServers": {
    "web": {"transport":"stdio","command":"web-mcp","args":["--s"],"capabilities":["network"],"risk":"safe"},
    "docs": {"transport":"streamable-http","url":"https://m.example/mcp","headers":{"Authorization":"secret:TOK"}}
  },
  "plugins": {
    "web": {"enabled": false, "granted": ["network"]},
    "docs": {"granted": []}
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(c.MCPServers) != 2 {
		t.Fatalf("mcpServers = %d, want 2", len(c.MCPServers))
	}
	web := c.MCPServers["web"]
	if web.Transport != "stdio" || web.Command != "web-mcp" || len(web.Capabilities) != 1 || web.Risk != "safe" {
		t.Fatalf("web = %+v", web)
	}
	docs := c.MCPServers["docs"]
	if docs.Transport != "streamable-http" || docs.URL == "" || docs.Headers["Authorization"] != "secret:TOK" {
		t.Fatalf("docs = %+v", docs)
	}
	if c.Plugins["web"].IsEnabled() {
		t.Fatal("web 应为显式 disabled")
	}
	if !c.Plugins["web"].HasGranted("network") {
		t.Fatal("web 应已授权 network")
	}
	if !c.Plugins["docs"].IsEnabled() {
		t.Fatal("docs 键存在但无 enabled 应默认启用")
	}
	// 模板含 plugins 空对象（首次运行生成的键）。
	if !strings.Contains(defaultConfig, `"plugins": {}`) {
		t.Fatal("模板应含 plugins 键")
	}
}

// TestRunRejectsInvalidMCPConfig 非法 MCP 声明启动即报因（fail-fast）。
func TestRunRejectsInvalidMCPConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want string
	}{
		{"缺 transport", `"mcpServers": {"x": {"command": "c"}}`, "transport"},
		{"stdio 缺 command", `"mcpServers": {"x": {"transport": "stdio"}}`, "command"},
		{"http 缺 url", `"mcpServers": {"x": {"transport": "streamable-http"}}`, "url"},
		{"名字含冒号", `"mcpServers": {"a:b": {"transport": "stdio", "command": "c"}}`, "冒号"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			raw := `{
  "model": {"provider":"openai-compatible","name":"m","base_url":"http://x","api_key":"secret:K"},
  ` + tc.cfg + `
}`
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			var out, errBuf bytes.Buffer
			if code := run([]string{"-data", dir}, strings.NewReader("/quit\n"), &out, &errBuf); code != 1 {
				t.Fatalf("code = %d, want 1（stderr: %s）", code, errBuf.String())
			}
			if !strings.Contains(errBuf.String(), tc.want) {
				t.Fatalf("stderr = %q, want 含 %q", errBuf.String(), tc.want)
			}
		})
	}
}
