package main

// UI 命令名对账锁：内核静态命令表的正本在 internal/app，界面投递点只认
// internal/adapter/uigui/cmd_dispatch.go 里登记的常量。两处分开维护，这条测试同时查
// 表里的名字内核都认、源码投递点都用登记常量、常量没有多出来或漏登记。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/app"
)

const uiCommandDir = "../../internal/adapter/uigui"

// uiCommandConstants 读界面常量表，拿到 uiCmdXxx → 命令名。锁本身不手抄名字，
// 否则手抄的期望表会把错误固化成断言。
func uiCommandConstants(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(uiCommandDir, "cmd_dispatch.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse cmd_dispatch.go: %v", err)
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			val, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				t.Fatalf("unquote %s: %v", lit.Value, uerr)
			}
			for _, n := range vs.Names {
				if strings.HasPrefix(n.Name, "uiCmd") {
					out[n.Name] = val
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no uiCmd constants found; the scan would be meaningless")
	}
	return out
}

func isPortCommandLit(x *ast.CompositeLit) bool {
	sel, ok := x.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "port" && sel.Sel.Name == "Command"
}

func calledName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

// scanUIDispatch 扫所有界面生产源码的投递点，收集用到的常量并记下绕过登记的位置。
// cmd_dispatch.go 自己跳过：那里的 port.Command{Name: name} 用的是函数参数。
func scanUIDispatch(t *testing.T, consts map[string]string) (map[string]bool, []string) {
	t.Helper()
	fset := token.NewFileSet()
	used := map[string]bool{}
	var bad []string
	check := func(e ast.Expr) {
		id, ok := e.(*ast.Ident)
		if !ok {
			bad = append(bad, fmt.Sprintf("%s: command name is not a uiCmd constant", fset.Position(e.Pos())))
			return
		}
		if _, ok := consts[id.Name]; !ok {
			bad = append(bad, fmt.Sprintf("%s: %s is not a registered uiCmd constant", fset.Position(e.Pos()), id.Name))
			return
		}
		used[id.Name] = true
	}
	werr := filepath.WalkDir(uiCommandDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Base(path) == "cmd_dispatch.go" {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CompositeLit:
				if !isPortCommandLit(x) {
					return true
				}
				for _, e := range x.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || key.Name != "Name" {
						continue
					}
					check(kv.Value)
				}
			case *ast.CallExpr:
				idx := -1
				switch calledName(x.Fun) {
				case "postCommand":
					idx = 1
				case "commandLine":
					idx = 0
				}
				if idx >= 0 && len(x.Args) > idx {
					check(x.Args[idx])
				}
			}
			return true
		})
		return nil
	})
	if werr != nil {
		t.Fatalf("walk uigui sources: %v", werr)
	}
	return used, bad
}

// TestUICommandNamesMatchCatalog 界面表名 ⊆ 内核清单，且源码只用登记的常量。
func TestUICommandNamesMatchCatalog(t *testing.T) {
	t.Parallel()
	consts := uiCommandConstants(t)

	table := map[string]bool{}
	for _, name := range uigui.UICommandNames() {
		if table[name] {
			t.Errorf("duplicated entry %q in uiCommandTable", name)
		}
		table[name] = true
	}
	if len(table) != len(consts) {
		t.Errorf("uiCommandTable has %d entries but there are %d uiCmd constants", len(table), len(consts))
	}
	for id, name := range consts {
		if !table[name] {
			t.Errorf("constant %s (%q) is not registered in uiCommandTable", id, name)
		}
	}

	known := map[string]bool{}
	for _, ci := range app.CommandCatalog() {
		for _, n := range ci.Names {
			known[n] = true
		}
	}
	if len(known) == 0 {
		t.Fatal("kernel command catalog is empty; the comparison would be meaningless")
	}
	for name := range table {
		if !known[name] {
			t.Errorf("uigui dispatches %q but the kernel command table has no such command", name)
		}
	}

	used, bad := scanUIDispatch(t, consts)
	for _, b := range bad {
		t.Error(b)
	}
	for id := range consts {
		if !used[id] {
			t.Errorf("constant %s is registered but no dispatch uses it", id)
		}
	}
}
