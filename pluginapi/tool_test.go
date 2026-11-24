package pluginapi

import (
	"strings"
	"testing"
)

// TestValidToolName 工具名文法（`[A-Za-z0-9_-]{1,64}`）：模型端点只认这一套，宿主照着
// 这条在装配期拒载，插件作者不用等协议层 400。
func TestValidToolName(t *testing.T) {
	ok := []string{"mem_1", "a", "Mem-1_x", strings.Repeat("a", ToolNameMaxLen)}
	for _, name := range ok {
		if !ValidToolName(name) {
			t.Errorf("该收的没收：%q", name)
		}
	}
	bad := []string{
		"", "部署脚本", "mem 1", "mem.1", "mcp:server:tool", "mem/1",
		strings.Repeat("a", ToolNameMaxLen+1),
	}
	for _, name := range bad {
		if ValidToolName(name) {
			t.Errorf("该拒的没收：%q", name)
		}
	}
}
