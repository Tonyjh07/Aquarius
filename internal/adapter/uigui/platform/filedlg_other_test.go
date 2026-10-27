//go:build !windows

package platform

// 非 Windows 文件框的参数拼装：过滤器与标题都要按 zenity / kdialog 各自的格式给，
// 拼错不会报错——对话框只是不筛文件或标题变成空串，用户看不出原因。

import "testing"

// TestZenityAndKdialogFilters zenity 是「显示名 | 通配符」、kdialog 是「通配符 | 显示名」，
// 顺序相反，别写成一样。
func TestZenityAndKdialogFilters(t *testing.T) {
	f := FileFilter{Label: "归档", Pattern: "*.aqx"}
	if got, want := zenityFilter([]FileFilter{f}), "--file-filter=归档 | *.aqx"; got != want {
		t.Fatalf("zenityFilter = %q, want %q", got, want)
	}
	if got, want := kdialogFilter(f), "*.aqx | 归档"; got != want {
		t.Fatalf("kdialogFilter = %q, want %q", got, want)
	}
	// 没有显示名时只给通配符（不留一个空的「| 」）。
	bare := FileFilter{Pattern: "*.json"}
	if got, want := zenityFilter([]FileFilter{bare}), "--file-filter=*.json"; got != want {
		t.Fatalf("无标签 zenityFilter = %q, want %q", got, want)
	}
	if got, want := kdialogFilter(bare), "*.json"; got != want {
		t.Fatalf("无标签 kdialogFilter = %q, want %q", got, want)
	}
	// 空表 = 不加过滤参数（zenity 的缺省就是全部文件）。
	if got := zenityFilter(nil); got != "" {
		t.Fatalf("空表 zenityFilter = %q, want 空", got)
	}
}

// TestFirstFilter 过滤器缺省回落「所有文件」：对话框不能没有过滤器。
func TestFirstFilter(t *testing.T) {
	if got := firstFilter(nil); got.Pattern != "*.*" || got.Label == "" {
		t.Fatalf("空表 firstFilter = %+v, want 所有文件", got)
	}
	in := []FileFilter{{Label: "甲", Pattern: "*.a"}, {Label: "乙", Pattern: "*.b"}}
	if got := firstFilter(in); got.Label != "甲" {
		t.Fatalf("firstFilter = %+v, want 第一项", got)
	}
}

// TestOrDefault 标题为空时取缺省值（对话框标题不能是空串）。
func TestOrDefault(t *testing.T) {
	if got := orDefault("", "选择文件"); got != "选择文件" {
		t.Fatalf("orDefault 空 = %q", got)
	}
	if got := orDefault("我的标题", "选择文件"); got != "我的标题" {
		t.Fatalf("orDefault 非空 = %q", got)
	}
}
