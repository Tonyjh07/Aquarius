package plugin

import (
	"strings"
	"testing"
)

// TestMCPServerValidate 声明校验：形态必需字段、transport/risk 取值、服务器名合法性。
func TestMCPServerValidate(t *testing.T) {
	stdio := MCPServer{MCPConfig: MCPConfig{Transport: TransportStdio, Command: "srv", Args: []string{"--stdio"}}}
	httpOK := MCPServer{MCPConfig: MCPConfig{Transport: TransportHTTP, URL: "https://m.example/mcp"}}
	cases := []struct {
		label  string
		server string // Validate 的服务器名入参
		srv    MCPServer
		ok     bool
	}{
		{"stdio 完整", "web", stdio, true},
		{"http 完整", "docs", httpOK, true},
		{"risk=confirm", "r", MCPServer{MCPConfig: stdio.MCPConfig, Risk: RiskConfirm}, true},
		{"空 transport", "x", MCPServer{}, false},
		{"未知 transport", "x", MCPServer{MCPConfig: MCPConfig{Transport: "ftp"}}, false},
		{"stdio 缺 command", "x", MCPServer{MCPConfig: MCPConfig{Transport: TransportStdio}}, false},
		{"http 缺 url", "x", MCPServer{MCPConfig: MCPConfig{Transport: TransportHTTP}}, false},
		{"未知 risk", "x", MCPServer{MCPConfig: stdio.MCPConfig, Risk: "wild"}, false},
		{"名字含冒号", "a:b", stdio, false},
		{"名字含空格", "a b", stdio, false},
		{"空名字", "", stdio, false},
	}
	for _, tc := range cases {
		err := tc.srv.Validate(tc.server)
		if tc.ok && err != nil {
			t.Errorf("%s: %v, want ok", tc.label, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: want error", tc.label)
		}
	}
}

// TestStateDefaults 状态默认值（D31）：键缺失 = 启用；granted 精确匹配。
func TestStateDefaults(t *testing.T) {
	if !(State{}).IsEnabled() {
		t.Fatal("enabled 键缺失应默认启用")
	}
	on, off := true, false
	if !(State{Enabled: &on}).IsEnabled() || (State{Enabled: &off}).IsEnabled() {
		t.Fatal("enabled 显式值未生效")
	}
	s := State{Granted: []string{"network"}}
	if !s.HasGranted("network") || s.HasGranted("fs") {
		t.Fatal("granted 判定错误")
	}
}

// TestParseManifest plugin.json 解析与校验（§6.4 #2）。
func TestParseManifest(t *testing.T) {
	ok := `{"name":"web-search","mcp":{"transport":"stdio","command":"web-search-mcp","args":["--stdio"],"env":{"K":"secret:K"}},"capabilities":["network"],"risk":"safe"}`
	m, err := ParseManifest([]byte(ok))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Name != "web-search" || m.MCP.Command != "web-search-mcp" ||
		len(m.Capabilities) != 1 || m.Risk != "safe" {
		t.Fatalf("manifest = %+v", m)
	}

	bad := []struct {
		name string
		json string
		want string
	}{
		{"坏 json", `{`, "解析"},
		{"缺 name", `{"mcp":{"transport":"stdio","command":"x"}}`, "plugin.json"},
		{"名字含冒号", `{"name":"a:b","mcp":{"transport":"stdio","command":"x"}}`, "冒号"},
		{"mcp 非法", `{"name":"x","mcp":{"transport":"stdio"}}`, "command"},
		{"risk 非法", `{"name":"x","mcp":{"transport":"stdio","command":"c"},"risk":"wild"}`, "risk"},
	}
	for _, tc := range bad {
		if _, err := ParseManifest([]byte(tc.json)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want 含 %q", tc.name, err, tc.want)
		}
	}
}
