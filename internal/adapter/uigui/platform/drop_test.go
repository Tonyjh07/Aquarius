package platform

// 拖拽路径解析测试（D187 口径三）：单文件 / 多文件 / 含空格与中文 / 代理对。

import "testing"

// dropOf 把若干路径编成「每项一段 UTF-16 + NUL」的取项函数。
func dropOf(paths ...string) func(i int) []uint16 {
	items := make([][]uint16, 0, len(paths))
	for _, p := range paths {
		items = append(items, utf16z(p))
	}
	return func(i int) []uint16 {
		if i < 0 || i >= len(items) {
			return nil
		}
		return items[i]
	}
}

// TestDropSingle 单个文件：路径原样（含末尾 NUL 已去掉）。
func TestDropSingle(t *testing.T) {
	got := collectDropPaths(1, dropOf(`C:\docs\报告.pdf`))
	if len(got) != 1 || got[0] != `C:\docs\报告.pdf` {
		t.Fatalf("got %q", got)
	}
}

// TestDropMultiple 多个文件：保持拖入顺序。
func TestDropMultiple(t *testing.T) {
	want := []string{`C:\a\1.png`, `C:\a\2.txt`, `C:\a\3.pdf`}
	got := collectDropPaths(len(want), dropOf(want...))
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDropSpacesAndUnicode 路径里的空格与中文必须原样保留（不能 TrimSpace 掉）。
func TestDropSpacesAndUnicode(t *testing.T) {
	path := `C:\Users\me\My Documents\会议 记录 🚀.docx`
	got := collectDropPaths(1, dropOf(path))
	if len(got) != 1 || got[0] != path {
		t.Fatalf("got %q, want %q", got, path)
	}
}

// TestDropSkipsEmpty 取不到 / 空项的项被跳过，不影响其余项。
func TestDropSkipsEmpty(t *testing.T) {
	got := collectDropPaths(3, func(i int) []uint16 {
		switch i {
		case 0:
			return nil
		case 1:
			return []uint16{'C', ':', 0} // 有效但很短
		default:
			return []uint16{0} // 空项
		}
	})
	if len(got) != 1 || got[0] != "C:" {
		t.Fatalf("got %q", got)
	}
	if got := collectDropPaths(0, dropOf("x")); got != nil {
		t.Fatalf("n=0 应为空: %q", got)
	}
	if got := collectDropPaths(2, nil); got != nil {
		t.Fatalf("raw=nil 应为空: %q", got)
	}
}
