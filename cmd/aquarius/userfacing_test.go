package main

// 铁律第 10 条（D146）的可执行守卫：用户面字符串里不得出现开发决策编号 / 里程碑 / 内部迭代标记。
//
// 扫的是**字符串字面量**（注释与 `_test.go` 不在此列——编号在注释里是维护者索引，
// 在测试消息里是溯源锚点）。这是「决策编号不进用户面」唯一的自动化防线：
// 新增 CLI 报错、命令描述、界面提示时若顺手写上（D35），这条测试会直接失败。

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// userFacingIDPattern 命中即视为漏进用户面的内部标记：
//   - D + 至少两位数字（D19 / D145；要求词边界，避免 Base64、D1 这类误报）
//   - § + 数字（DESIGN 节号）
//   - 里程碑 M0–M4（同样的词边界）
var userFacingIDPattern = regexp.MustCompile(`\bD\d{2,}\b|§\d|\bM[0-4]\b`)

// userFacingDocs 用户面 Markdown：面向使用者、不含内部编号的文档（docs/README.md 是
// 贡献者的文档地图，会拿编号当例子讲，故不在此列；发布说明逐版累积，按 glob 全扫）。
// 新增用户面文档时把路径加进来——否则这条守卫管不到它。
var userFacingDocs = []string{
	"README.md",
	"docs/guide.md",
	"docs/usage.md",
	"docs/configuration.md",
	"docs/storage.md",
}

// TestNoDecisionIDsInUserFacingDocs 守住用户面文档：正文（含代码块——那是用户会看到的
// 命令与报错原文）里出现决策编号 / 节号 / 里程碑即失败。
//
// 与字符串字面量守卫互补：那条防代码往界面上写编号，这条防文档正文里写。两边都失效时
// 用户就会读到「（D34）」这种只有维护者看得懂的东西。
func TestNoDecisionIDsInUserFacingDocs(t *testing.T) {
	root := filepath.Join("..", "..")
	docs := append([]string{}, userFacingDocs...)
	releases, err := filepath.Glob(filepath.Join(root, "docs", "releases", "*.md"))
	if err != nil {
		t.Fatalf("列发布说明: %v", err)
	}
	for _, p := range releases {
		if rel, rerr := filepath.Rel(root, p); rerr == nil {
			docs = append(docs, filepath.ToSlash(rel))
		}
	}
	var bad []string
	for _, rel := range docs {
		path := filepath.Join(root, filepath.FromSlash(rel))
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("读取 %s: %v", rel, rerr)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if m := userFacingIDPattern.FindString(line); m != "" {
				bad = append(bad, fmt.Sprintf("%s:%d  →  %s  in  %s", rel, i+1, m, strings.TrimSpace(line)))
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("用户面文档里仍有开发决策编号 / 里程碑（铁律第 10 条）：\n  %s\n"+
			"改成行为与后果的说明；编号只留在贡献者面文档与代码注释里。", strings.Join(bad, "\n  "))
	}
}

// TestNoDecisionIDsInUserFacingStrings 遍历 cmd/ 与 internal/ 的非测试源码，
// 解析出所有字符串字面量（含 raw string），命中内部标记即失败。
func TestNoDecisionIDsInUserFacingStrings(t *testing.T) {
	root := filepath.Join("..", "..") // cmd/aquarius → 仓库根
	var bad []string
	for _, top := range []string{"cmd", "internal", "pluginapi"} {
		base := filepath.Join(root, top)
		if _, serr := os.Stat(base); serr != nil && errors.Is(serr, fs.ErrNotExist) {
			continue // 该目录尚未建立（如 pluginapi/v1 后移出里程碑）
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// 开发脚手架不面向用户；插件契约目录只放类型声明。
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
				return nil // 语法错误交给 go build，这里只做文案守卫
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, uerr := strconv.Unquote(lit.Value)
				if uerr != nil {
					return true
				}
				if m := userFacingIDPattern.FindString(s); m != "" {
					pos := fset.Position(lit.Pos())
					bad = append(bad, pos.String()+"  →  "+m+"  in  "+strings.TrimSpace(s))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s: %v", base, err)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("用户面字符串里仍有开发决策编号 / 里程碑（铁律第 10 条 / D146）：\n  %s\n"+
			"改为行为说明，编号只写进注释。", strings.Join(bad, "\n  "))
	}
}

// TestUserFacingIDPatternSanity 守住正则本身：能命中典型编号，也不误伤普通文案。
func TestUserFacingIDPatternSanity(t *testing.T) {
	hit := []string{"密钥只写不回显（D35）", "见 §7.3", "桌面通知（回答完成时，M3）", "D145 后应只留限额"}
	for _, s := range hit {
		if userFacingIDPattern.FindString(s) == "" {
			t.Fatalf("应命中：%q", s)
		}
	}
	miss := []string{"Base64 编码", "gpt-5.2-mini", "D 盘根目录", "ID 为 3f1c9a02", "MCP 插件管理"}
	for _, s := range miss {
		if m := userFacingIDPattern.FindString(s); m != "" {
			t.Fatalf("不应命中 %q（实际命中 %q）", s, m)
		}
	}
}
