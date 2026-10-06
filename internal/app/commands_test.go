package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestCommandCatalogCoversExecCommand 静态清单覆盖 execCommand 全部静态分支（D103②
// 单一事实源：help/补全数据与可执行命令不漂移；/help 自身与 /mcp 家族说明行不入清单）。
func TestCommandCatalogCoversExecCommand(t *testing.T) {
	got := map[string]bool{}
	for _, ci := range commandCatalog {
		for _, n := range ci.Names {
			if got[n] {
				t.Fatalf("命令 %s 重复入表", n)
			}
			got[n] = true
			if ci.Desc == "" {
				t.Fatalf("命令 %s 缺描述", n)
			}
		}
	}
	// execCommand 的静态分支（help 不入清单——help 输出本身不列 /help）。
	want := map[string]bool{
		"new": true, "list": true, "switch": true, "title": true, "goto": true,
		"edit": true, "regen": true, "branch": true, "rm": true, "exit": true,
		"compact": true, "permission": true, "memory": true, "usage": true,
		"jobs": true, "quit": true, "plugin": true, "model": true,
		"think": true, "effort": true,
	}
	for n := range want {
		if !got[n] {
			t.Fatalf("清单缺命令 %s", n)
		}
	}
	for n := range got {
		if !want[n] {
			t.Fatalf("清单多出不可执行命令 %s", n)
		}
	}
}

// TestSessionCommandsPort Commands() 快照（D103②）：静态在前；动态缀尾按名排序；
// 未带 meta 的动态命令按名生成无描述条目。
func TestSessionCommandsPort(t *testing.T) {
	s := &Session{}
	if got := s.Commands(); len(got) != len(commandCatalog) {
		t.Fatalf("静态清单 %d 条, want %d", len(got), len(commandCatalog))
	}
	s.SetDynamicCommands(map[string]CommandHandler{
		"mcp:beta:probe": func(context.Context, []string) (string, error) { return "", nil },
		"mcp:alpha:ask":  func(context.Context, []string) (string, error) { return "", nil },
	}, map[string]port.CommandInfo{
		"mcp:beta:probe": {Names: []string{"mcp:beta:probe"}, Desc: "探测"},
	})
	got := s.Commands()
	if len(got) != len(commandCatalog)+2 {
		t.Fatalf("清单 %d 条, want %d（静态+2 动态）", len(got), len(commandCatalog)+2)
	}
	dyn := got[len(commandCatalog):]
	if dyn[0].Names[0] != "mcp:alpha:ask" || dyn[1].Names[0] != "mcp:beta:probe" {
		t.Fatalf("动态序 = %s, %s", dyn[0].Names[0], dyn[1].Names[0])
	}
	if dyn[0].Desc != "" || dyn[1].Desc != "探测" {
		t.Fatalf("动态描述 = %q / %q", dyn[0].Desc, dyn[1].Desc)
	}
}

// TestHelpFromCatalog /help 表驱动渲染（D103②）：别名一行、家族尾行、动态逐条列出。
func TestHelpFromCatalog(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	s.SetDynamicCommands(map[string]CommandHandler{
		"mcp:srv:probe": func(context.Context, []string) (string, error) { return "", nil },
	}, map[string]port.CommandInfo{
		"mcp:srv:probe": {Names: []string{"mcp:srv:probe"}, Desc: "探测一下"},
	})
	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "help"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"/quit, /exit", "退出",
		"/edit <id> [--keep|--copy] [--part N] <文本>",
		mcpFamilyLine,
		"/mcp:srv:probe", "探测一下",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help 缺 %q:\n%s", want, out)
		}
	}
}
