//go:build !windows

package platform

// 非 Windows 文件框的参数拼装：过滤器与标题都要按 zenity / kdialog 各自的格式给，
// 拼错不会报错——对话框只是不筛文件或标题变成空串，用户看不出原因。

import (
	"strings"
	"testing"
)

// TestZenityAndKdialogFilters zenity 是「显示名 | 通配符」、kdialog 是「通配符 | 显示名」，
// 顺序相反，别写成一样。
func TestZenityAndKdialogFilters(t *testing.T) {
	f := FileFilter{Label: "归档", Pattern: "*.aqx"}
	if got, want := zenityFilters([]FileFilter{f}), []string{"--file-filter=归档 | *.aqx"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("zenityFilters = %q, want %q", got, want)
	}
	if got, want := kdialogFilters([]FileFilter{f}), "*.aqx | 归档"; got != want {
		t.Fatalf("kdialogFilters = %q, want %q", got, want)
	}
	// 没有显示名时只给通配符（不留一个空的「| 」）。
	bare := FileFilter{Pattern: "*.json"}
	if got, want := zenityFilters([]FileFilter{bare}), []string{"--file-filter=*.json"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("无标签 zenityFilters = %q, want %q", got, want)
	}
	if got, want := kdialogFilters([]FileFilter{bare}), "*.json"; got != want {
		t.Fatalf("无标签 kdialogFilters = %q, want %q", got, want)
	}
	// 空表 = zenity 不加过滤参数（缺省就是全部文件），kdialog 给「所有文件」。
	if got := zenityFilters(nil); len(got) != 0 {
		t.Fatalf("空表 zenityFilters = %q, want 空", got)
	}
	if got := kdialogFilters(nil); !strings.Contains(got, "*.*") {
		t.Fatalf("空表 kdialogFilters = %q, want 含 *.*", got)
	}
}

// TestZenityFiltersSplitSemicolons 分号分隔的通配符必须拆成空格分隔。
//
// zenity 的模式串是 `NAME | PATTERN1 PATTERN2`；把 `*.aqx;*.json` 原样交给它，得到的是一个
// 筛不出任何文件的伪模式——对话框里一个文件都不显示，而它自己不报错。
func TestZenityFiltersSplitSemicolons(t *testing.T) {
	in := []FileFilter{
		{Label: "会话文件", Pattern: "*.aqx;*.json;*.jsonl"},
		{Label: "所有文件", Pattern: "*.*"},
	}
	got := zenityFilters(in)
	want := []string{"--file-filter=会话文件 | *.aqx *.json *.jsonl", "--file-filter=所有文件 | *.*"}
	if len(got) != len(want) {
		t.Fatalf("zenityFilters = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项 = %q, want %q", i, got[i], want[i])
		}
		if strings.Contains(got[i], ";") {
			t.Fatalf("第 %d 项还带着分号: %q", i, got[i])
		}
	}
	// 多档都要给到，不能只取第一项（导入框有五档格式的扩展名）。
	if kd := kdialogFilters(in); strings.Contains(kd, ";") || !strings.Contains(kd, "*.jsonl") {
		t.Fatalf("kdialogFilters 没把多模式拆开: %q", kd)
	}
}

// TestSaveCommandHasNoOverwritePrompt 保存框不设覆盖确认（与实现和文档同一个口径）。
//
// 导出是「再存一份」：同名时命令面加 -2 后缀并在回执里写清落点。对话框里先问「要覆盖吗」，
// 答「是」之后文件仍然存成 -2，是个无效选择题。zenity 的 --confirm-overwrite 也已被标为
// DEPRECATED、行为不保证。
func TestSaveCommandHasNoOverwritePrompt(t *testing.T) {
	req := SaveRequest{Title: "导出会话", Name: "甲会话.aqx", DefExt: "aqx"}
	args := zenitySaveArgs(req)
	if strings.Contains(strings.Join(args, " "), "--confirm-overwrite") {
		t.Fatal("保存框参数里出现了 --confirm-overwrite")
	}
	// 预填名与标题都还在（删 flag 不该顺手把这些也丢了）。
	if !strings.Contains(strings.Join(args, " "), "--filename=甲会话.aqx") {
		t.Fatalf("缺预填文件名: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "--title=导出会话") {
		t.Fatalf("缺标题: %v", args)
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

// TestAppleScriptString 标题/文件名拼进 AppleScript 时转义：一个引号就够让整段脚本语法
// 错误，而失败会被当成「用户取消」静默吞掉。
func TestAppleScriptString(t *testing.T) {
	if got, want := appleScriptString(`他说"好"`), `"他说\"好\""`; got != want {
		t.Fatalf("appleScriptString = %q, want %q", got, want)
	}
	if got, want := appleScriptString(`C:\x`), `"C:\\x"`; got != want {
		t.Fatalf("反斜杠未转义: %q, want %q", got, want)
	}
}

// TestExtWithDot 缺省扩展名不带点（与 Win32 契约同口径），拼预填名时补上。
func TestExtWithDot(t *testing.T) {
	if got := extWithDot("aqx"); got != ".aqx" {
		t.Fatalf("extWithDot(aqx) = %q, want .aqx", got)
	}
	if got := extWithDot(".aqx"); got != ".aqx" {
		t.Fatalf("extWithDot 不该重复加点: %q", got)
	}
	if got := extWithDot(""); got != "" {
		t.Fatalf("extWithDot(空) = %q, want 空", got)
	}
}
