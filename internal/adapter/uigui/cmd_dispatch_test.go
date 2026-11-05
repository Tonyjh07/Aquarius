package uigui

import (
	"testing"

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
}
