package uigui

import (
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uicommon"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestUICommandTableSelfCheck 动作表自检：名字不重复、触发面有说明、对外只给副本。
// 与装配根的 TestUICommandNamesMatchCatalog 互补，那边管「表 ⊆ 内核清单」。
func TestUICommandTableSelfCheck(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, e := range uiCommandTable {
		if e.Name == "" || e.From == "" {
			t.Fatalf("登记不完整: %+v", e)
		}
		if seen[e.Name] {
			t.Fatalf("命令 %q 登记了两次，表里按命令去重", e.Name)
		}
		seen[e.Name] = true
	}
	if len(seen) == 0 {
		t.Fatal("空表，自检没有意义")
	}
	names := UICommandNames()
	if len(names) != len(seen) {
		t.Fatalf("UICommandNames 返回 %d 条，表里有 %d 条", len(names), len(seen))
	}
	names[0] = "changed-by-caller"
	if UICommandNames()[0] == "changed-by-caller" {
		t.Fatal("UICommandNames 返回了表本身，调用方能改到登记表")
	}
}

// TestPostCommand 统一投递口：正常投一条，缓冲满时返回 false 且不阻塞。
func TestPostCommand(t *testing.T) {
	t.Parallel()
	u := &UI{inCh: make(chan port.UserInput, 1)}
	if !postCommand(u, uiCmdSwitch, "C42") {
		t.Fatal("缓冲有空位却没投出去")
	}
	if postCommand(u, uiCmdNew) {
		t.Fatal("缓冲满时应返回 false")
	}
	in := <-u.inCh
	if in.Command == nil || in.Command.Name != uiCmdSwitch ||
		len(in.Command.Args) != 1 || in.Command.Args[0] != "C42" {
		t.Fatalf("inCh = %+v, want /switch C42", in)
	}
	if postCommand(nil, uiCmdNew) {
		t.Fatal("空 UI 应返回 false，不该 panic")
	}
}

// TestCommandLine 文本路径的拼行：无参不带尾空格，多参按空格分隔。
func TestCommandLine(t *testing.T) {
	t.Parallel()
	if got := commandLine(uiCmdNew); got != "/new" {
		t.Fatalf("commandLine(new) = %q", got)
	}
	if got := commandLine(uiCmdGoto, "n1", "--keep"); got != "/goto n1 --keep" {
		t.Fatalf("commandLine(goto, n1, --keep) = %q", got)
	}
	// 含空白的参数必须圈起来，否则文本路径按空白切分会把它切成两段（导入文件路径的
	// 典型形态：`C:\My Docs\a.aqx`）。用同一把切分器验证，而不是只比字符串。
	line := commandLine(uiCmdImport, `C:\My Docs\a.aqx`)
	if fields := uicommon.CommandFields(line); len(fields) != 2 || fields[1] != `C:\My Docs\a.aqx` {
		t.Fatalf("含空白参数被切坏了: line=%q fields=%q", line, fields)
	}
	// 空参数要留下一个空位，不能被当成「没有这个参数」。
	if fields := uicommon.CommandFields(commandLine(uiCmdEdit, "")); len(fields) != 2 || fields[1] != "" {
		t.Fatalf("空参数丢失: %q", fields)
	}
	// 全角空格也是 CommandFields 的分隔符（unicode.IsSpace），中文路径里出现它时同样要圈。
	if fields := uicommon.CommandFields(commandLine(uiCmdImport, "C:\\我的\u3000文档\\a.aqx")); len(fields) != 2 {
		t.Fatalf("全角空格被当成分隔符: %q", fields)
	}
}
