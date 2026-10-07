//go:build windows

package mcpgate

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tonyjh07/Aquarius/internal/plugin"
)

// TestTransportForHidesStdioWindow D118 复现（D114 连带回归）：stdio MCP server 子进程
// 长驻，双击启动 FreeConsole 后无控制台可继承 → **每个 server 各开一个可见控制台窗**、
// 活多久开多久。server I/O 全走 stdio 管道 → 构造传输时压 CREATE_NO_WINDOW（单旗，
// 不误伤 server 自己拉起的 GUI 子窗）。
func TestTransportForHidesStdioWindow(t *testing.T) {
	tr, err := transportFor(context.Background(), nil, plugin.Decl{
		MCPConfig: plugin.MCPConfig{
			Transport: plugin.TransportStdio,
			Command:   "cmd",
			Args:      []string{"/c", "echo hi"},
		},
	})
	if err != nil {
		t.Fatalf("transportFor: %v", err)
	}
	ct, ok := tr.(*mcp.CommandTransport)
	if !ok {
		t.Fatalf("transport = %T, want *mcp.CommandTransport", tr)
	}
	if ct.Command == nil {
		t.Fatal("stdio 传输应带命令")
	}
	if ct.Command.SysProcAttr == nil {
		t.Fatal("stdio server 子进程应挂 SysProcAttr（D118：不新开可见控制台）")
	}
	if ct.Command.SysProcAttr.CreationFlags&0x08000000 == 0 { // CREATE_NO_WINDOW
		t.Errorf("CreationFlags = %#x, want 携带 CREATE_NO_WINDOW(0x8000000)",
			ct.Command.SysProcAttr.CreationFlags)
	}
}
