package app

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestCommandCatalogCoversExecCommand 静态清单与 execCommand 分支必须一一对应（D103②
// 单一事实源：help/补全数据与可执行命令不漂移）。
//
// 断言集**从 session.go 的 switch 解析得来**，不手抄：手工维护的 want 表正是当初漏掉
// /help 的原因（清单说自己是单一事实源，实际有两处手工维护）。新增命令只改一处就会
// 在这里红或自然通过；改错地方（只在 switch 加分支、或只在表里加条目）则必红。
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

	want := execCommandCases(t)
	for n := range want {
		if !got[n] {
			t.Errorf("execCommand 有分支 %q 但清单里没有（补全浮层将看不到它）", n)
		}
	}
	for n := range got {
		if !want[n] {
			t.Errorf("清单里有 %q 但 execCommand 没有对应分支（不可执行的幽灵命令）", n)
		}
	}
}

// execCommandCases 用 AST 读 session.go 里 execCommand 的 switch 分支名。
func execCommandCases(t *testing.T) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "session.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 session.go 失败: %v", err)
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "execCommand" {
			fn = fd
			break
		}
	}
	if fn == nil {
		t.Fatal("session.go 里找不到 execCommand")
	}
	var sw *ast.SwitchStmt
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if sw != nil {
			return false // 只取最外层的命令分派 switch
		}
		if s, ok := n.(*ast.SwitchStmt); ok {
			sw = s
			return false
		}
		return true
	})
	if sw == nil {
		t.Fatal("execCommand 里找不到 switch")
	}
	out := map[string]bool{}
	for _, stmt := range sw.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, expr := range cc.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			name, unquoteErr := strconv.Unquote(lit.Value)
			if unquoteErr != nil {
				t.Fatalf("无法解析分支名 %s: %v", lit.Value, unquoteErr)
			}
			out[name] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("execCommand 的分支为空，对照无意义")
	}
	return out
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
		"/edit <id> [--keep|--copy] [--part N] [--attach <路径>]... [--drop N]... [--drop-all] [<文本>]",
		mcpFamilyLine,
		"/mcp:srv:probe", "探测一下",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help 缺 %q:\n%s", want, out)
		}
	}
}
