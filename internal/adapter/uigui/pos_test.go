package uigui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPosRecRoundTrip 位置记忆含置顶态（§15.1 置顶开关）：写读一致；旧文件
// （无 top_most 键）读出 nil = 缺省置顶口径。
func TestPosRecRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gui_pos.json")

	tm := false
	savePos(path, posRec{X: 10, Y: 20, TopMost: &tm})
	p, ok := loadPos(path)
	if !ok {
		t.Fatal("loadPos 应成功")
	}
	if p.X != 10 || p.Y != 20 || p.TopMost == nil || *p.TopMost {
		t.Fatalf("roundtrip = %+v, want {10 20 TopMost=false}", p)
	}

	// 旧格式（无 top_most 键）→ nil（缺省置顶，不被零值误判为非置顶）。
	if err := os.WriteFile(path, []byte(`{"X":1,"Y":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, ok = loadPos(path)
	if !ok || p.X != 1 || p.Y != 2 || p.TopMost != nil {
		t.Fatalf("旧文件 = %+v,%v want TopMost nil", p, ok)
	}

	// 文件缺失 → 未找到（不报错，走缺省）。
	if _, ok := loadPos(filepath.Join(t.TempDir(), "none.json")); ok {
		t.Fatal("缺失文件应返回未找到")
	}
}
