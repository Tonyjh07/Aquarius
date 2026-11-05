package main

// 超长函数闸门：不手抄函数清单（那会把错误固化成断言），只卡两个数字。超过 100 行的
// 非测试函数总数不得增长，单个函数不得越过上限。拆分现有函数是净收益，所以总数只允许
// 减少；确实要新增长函数，先拆，再在提交说明里写明理由。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	longFuncThreshold = 100
	longFuncBaseline  = 32 // 2026-11-05 收口后的实测值，只允许减少
	longFuncMaxLines  = 230
)

func TestLongFunctionsDoNotGrow(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	type entry struct {
		where string
		lines int
	}
	var long []entry
	maxLines := 0
	for _, top := range []string{"cmd", "internal", "pluginapi"} {
		base := filepath.Join(root, top)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		werr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "testdata", "spike", "temp":
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return perr
			}
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				n := fset.Position(fd.End()).Line - fset.Position(fd.Pos()).Line + 1
				if n > maxLines {
					maxLines = n
				}
				if n > longFuncThreshold {
					rel, _ := filepath.Rel(root, path)
					where := fmt.Sprintf("%s:%d %s", filepath.ToSlash(rel),
						fset.Position(fd.Pos()).Line, fd.Name.Name)
					long = append(long, entry{where, n})
				}
			}
			return nil
		})
		if werr != nil {
			t.Fatalf("walk %s: %v", base, werr)
		}
	}
	if len(long) == 0 {
		t.Fatal("没有扫到任何函数，闸门会形同虚设")
	}
	if len(long) > longFuncBaseline {
		t.Errorf("超 %d 行的非测试函数有 %d 个，基线是 %d；新增的请先拆开",
			longFuncThreshold, len(long), longFuncBaseline)
		for _, e := range long {
			t.Logf("  %s: %d 行", e.where, e.lines)
		}
	}
	if maxLines > longFuncMaxLines {
		t.Errorf("最长函数 %d 行，超过上限 %d", maxLines, longFuncMaxLines)
	}
}
