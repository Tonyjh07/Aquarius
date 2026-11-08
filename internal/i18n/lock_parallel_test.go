package i18n

// 语言槽用例不得并行的锁。
//
// i18n.Use 改的是进程级语言槽。Go 的语义是：带 t.Parallel() 的顶层测试要等所有非并行
// 顶层测试跑完才统一开跑，所以切语言槽的用例只要保持不并行，就串行执行、互不串台。
// 一旦有人给这类用例加上 t.Parallel()，两个用例会同时改同一个槽，取词结果随调度变，
// 报错看着像文案坏了，很难查。
//
// 判据按文件给，不按函数给：切语言槽的调用可能藏在 helper 里（`useLang(sel)` 一类），
// 而语言槽是进程级的——同文件里不切语言的用例与切语言的用例并行，读到的语言同样不确定。
// 凡文件里出现 Use 调用，该文件所有顶层测试都不许加 t.Parallel()。
//
// 已知盲区：以后若经新的包装函数切语言（不再直接调 Use），本锁管不到，靠评审兜底。
// 遍历口径与 I6 / L4 的锁一致，复用 i18nRepoRoot 等常量。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestNoParallelTestsInLangSwitchFiles(t *testing.T) {
	var bad []string
	for _, top := range i18nScannedTops {
		base := filepath.Join(i18nRepoRoot, top)
		if _, err := os.Stat(base); err != nil {
			continue // 该目录尚未建立
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if i18nSkippedDirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			rel := relPath(t, i18nRepoRoot, path)
			if !strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil // 语法错误交给 go build
			}
			if !fileCallsUse(f) {
				return nil
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Body == nil {
					continue
				}
				if !strings.HasPrefix(fn.Name.Name, "Test") || !hasTestingTParam(fn) {
					continue
				}
				if callsParallel(fn.Body) {
					bad = append(bad, fmt.Sprintf("%s:%d %s", rel, fset.Position(fn.Pos()).Line, fn.Name.Name))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s: %v", base, err)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("切语言槽的文件里有 %d 个测试加了 t.Parallel()（进程级语言槽不能并行）：\n  %s\n\n"+
			"改法：这些文件的顶层测试一律不加 t.Parallel()，让 Go 把它们排在并行批次之前串行跑完。\n"+
			"确需并行时先改产品代码，把语言槽从进程级收进测试各自的实例。",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// fileCallsUse 判断文件里有没有直接调用 Use（外部包写 i18n.Use，本包写 Use）。
// 只看 AST 节点，注释与字符串里的示例不算。
func fileCallsUse(f *ast.File) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if id, ok := fun.X.(*ast.Ident); ok && id.Name == "i18n" && fun.Sel.Name == "Use" {
				found = true
			}
		case *ast.Ident:
			if f.Name.Name == "i18n" && fun.Name == "Use" {
				found = true
			}
		}
		return !found
	})
	return found
}

// hasTestingTParam 只认顶层 func TestXxx(t *testing.T)；TestMain(m *testing.M) 与
// 普通 helper 不在锁内。
func hasTestingTParam(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		typ := field.Type // *testing.T 是 StarExpr 包着 SelectorExpr
		if star, ok := typ.(*ast.StarExpr); ok {
			typ = star.X
		}
		sel, ok := typ.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "T" {
			continue
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "testing" {
			return true
		}
	}
	return false
}

// callsParallel 找 t.Parallel()；接收者名字不写死，换个变量名也能抓到。
func callsParallel(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Parallel" {
			if _, ok := sel.X.(*ast.Ident); ok {
				found = true
			}
		}
		return !found
	})
	return found
}
