package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestCommandTableSelfCheck 注册表自检（D245）：名字不重复、每条记录有描述与处理函数、
// 别名与主名指向同一条记录。此前这里从 execCommand 的 switch 反推分支名做双向断言——
// switch 已随注册表退役，断言对象改成表自己。
func TestCommandTableSelfCheck(t *testing.T) {
	if len(commandTable) == 0 {
		t.Fatal("命令注册表为空，自检无意义")
	}
	seen := map[string]int{}
	for i, e := range commandTable {
		if len(e.Names) == 0 {
			t.Fatalf("第 %d 条记录没有名字", i)
		}
		if e.Desc == "" {
			t.Fatalf("命令 %v 缺描述（help 行与补全浮层都要用）", e.Names)
		}
		if e.Run == nil {
			t.Fatalf("命令 %v 没有处理函数（不可执行的幽灵命令）", e.Names)
		}
		for _, n := range e.Names {
			if strings.ContainsAny(n, " \t/") {
				t.Fatalf("命令名 %q 含空白或斜杠（命令名不含斜杠，见 port.CommandInfo）", n)
			}
			if j, dup := seen[n]; dup {
				t.Fatalf("命令名 %q 在第 %d 与第 %d 条记录里重复", n, j, i)
			}
			seen[n] = i
		}
	}

	// 索引与表一致：每个名字都能查回它自己的那条记录（别名与主名同记录）。
	for i, e := range commandTable {
		for _, n := range e.Names {
			got, ok := commandIndex[n]
			if !ok {
				t.Fatalf("索引里的名字 %q 查不到", n)
			}
			if got != &commandTable[i] {
				t.Fatalf("名字 %q 指向了别的记录", n)
			}
		}
	}
	if len(commandIndex) != len(seen) {
		t.Fatalf("索引 %d 个名字, want %d", len(commandIndex), len(seen))
	}
}

// TestCommandNamesAreDispatched 表里每条命令（含每个别名）都能经 Handle 分派到——
// 不认识的名字会落到「未知命令」分支，那说明记录与索引脱节。
func TestCommandNamesAreDispatched(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	for _, e := range commandTable {
		for _, n := range e.Names {
			if _, ok := commandIndex[n]; !ok {
				t.Fatalf("命令 %q 不在索引里", n)
			}
		}
	}
	// /help 是最安全的探针：它只读表、不碰会话状态，且必须能命中。
	out, err := s.execCommand(context.Background(), port.Command{Name: "help"})
	if err != nil {
		t.Fatalf("execCommand(help) = %v", err)
	}
	if !strings.Contains(out, "/help") {
		t.Fatalf("help 输出里没有 /help 自己:\n%s", out)
	}
	if _, err := s.execCommand(context.Background(), port.Command{Name: "不存在的命令"}); err == nil ||
		!strings.Contains(err.Error(), "未知命令") {
		t.Fatalf("未知命令 err = %v, want 未知命令", err)
	}
}

// TestCommandAliasesAreEquivalent 别名与主名走同一条记录：/exit 与 /quit 都返回退出请求，
// 参数与行为不分叉（D245：别名是同一记录的多名字，不是第二条记录）。
func TestCommandAliasesAreEquivalent(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	quit, exit := commandIndex["quit"], commandIndex["exit"]
	if quit == nil || exit == nil {
		t.Fatal("quit/exit 未同时入索引")
	}
	if quit != exit {
		t.Fatal("quit 与 exit 应是同一条记录（别名不是第二条命令）")
	}
	for _, name := range []string{"quit", "exit"} {
		if _, err := s.execCommand(context.Background(), port.Command{Name: name}); err != ErrQuit {
			t.Fatalf("/%s err = %v, want ErrQuit", name, err)
		}
	}
}

// TestSessionCommandsPort Commands() 快照（D103②）：静态在前；动态缀尾按名排序；
// 未带 meta 的动态命令按名生成无描述条目。
func TestSessionCommandsPort(t *testing.T) {
	s := &Session{}
	if got := s.Commands(); len(got) != len(commandTable) {
		t.Fatalf("静态清单 %d 条, want %d", len(got), len(commandTable))
	}
	s.SetDynamicCommands(map[string]CommandHandler{
		"mcp:beta:probe": func(context.Context, []string) (string, error) { return "", nil },
		"mcp:alpha:ask":  func(context.Context, []string) (string, error) { return "", nil },
	}, map[string]port.CommandInfo{
		"mcp:beta:probe": {Names: []string{"mcp:beta:probe"}, Desc: "探测"},
	})
	got := s.Commands()
	if len(got) != len(commandTable)+2 {
		t.Fatalf("清单 %d 条, want %d（静态+2 动态）", len(got), len(commandTable)+2)
	}
	dyn := got[len(commandTable):]
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
		"/edit <id> [--keep|--copy] [--part N] [--attach <路径>]... [--drop N]... [--drop-all] [<文本>]",
		mcpFamilyLine,
		"/mcp:srv:probe", "探测一下",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help 缺 %q:\n%s", want, out)
		}
	}
}
