package i18n

// I7 的第三把锁：出口扫描。
//
// 前两把（`TestNoCJKLiterals` / `TestKeysResolve`）都是从**中文**那一侧推的：源码里有中文就
// 报错。它们有一个盲区——写死的**英文**文案在中文界面上照样漏出去，而且不漏到表里就永远
// 不会被 `en` 侧的检查抓到。实测漏过三处：主输入框占位两处、「拒绝原因」占位一处，中文
// 用户天天看见（`confirm_ui.go`）。
//
// 这一把改从出口推：文案最终都要交给某个绘制函数，那就盯住这些函数**直接吃下的字符串字面量**。
// 出口只认三种来源——`i18n.T/Tf` 取词、变量、空串；其余一律报错。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// materialTextFns `material` 包里把字符串画到屏幕上的构造函数（第 2 个及之后的实参是文案；
// `material.Editor` 的那个是占位）。
var materialTextFns = map[string]bool{
	"Label":     true,
	"Body1":     true,
	"Body2":     true,
	"Caption":   true,
	"Subtitle1": true,
	"Subtitle2": true,
	"Editor":    true,
	"Button":    true,
	"CheckBox":  true,
	"Switch":    true,
}

// uiguiTextFns `uigui` 自绘的文案出口，按被调函数名匹配（接收者写法不固定：`u.hoverTip`、
// `sec`、`f.row` 都在这一类里）。
var uiguiTextFns = map[string]bool{
	"hoverTip": true, // 悬停提示条
	"sec":      true, // 设置页分组小标题行
}

// sinkScannedPrefix 出口扫描的范围：`uigui` 是唯一手绘界面的适配器，`uitui` 的文案经命令表
// 与状态机走，不在这里。
const sinkScannedPrefix = "internal/adapter/uigui/"

// commandMetaFields 命令元数据里**必须是文案表 key** 的字段。
//
// 命令表（`internal/app/commands.go`）里的 `Desc` / `Usage` / `ValueName` 存的是 key 本身，
// 渲染时才 `i18n.T`。写成一句英文时表面上「有字」，切语言就固定成那一句；而 key 拼错则
// 直接把 key 印到 `/help` 上——两种错都只有打开帮助才看得见，所以在这里静态比对一遍。
var commandMetaFields = map[string]bool{
	"Desc":      true,
	"Usage":     true,
	"ValueName": true,
}

// TestNoTextLiterals 守住「界面上说的每一句都来自文案表」的另一面：出口不吃裸英文。
func TestNoTextLiterals(t *testing.T) {
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
			if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || isMasterTable(rel) {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil // 语法错误交给 go build
			}
			if strings.HasPrefix(rel, sinkScannedPrefix) {
				bad = append(bad, textSinkLiterals(fset, rel, f)...)
				bad = append(bad, textFieldLiterals(fset, rel, f)...)
			}
			bad = append(bad, commandMetaLiterals(fset, rel, f)...)
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s: %v", base, err)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("界面出口仍有写死的文案 %d 处（D275/contracts §7.8：出口只认 i18n.T/Tf、变量、空串）：\n"+
			"  %s\n\n"+
			"改法：把这句话搬进对应的区域表（zh 与 en 各一条），代码里留 i18n.T 取词。\n"+
			"纯符号（`×`、`· · ·`、`—`）与空串不在此列；占位类的 key 见 ui.input.placeholder。",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// textSinkLiterals 绘制函数直接吃下的英文字面量。
func textSinkLiterals(fset *token.FileSet, rel string, f *ast.File) []string {
	var bad []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var fnName string
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := fn.X.(*ast.Ident); ok && pkg.Name == "material" {
				if !materialTextFns[fn.Sel.Name] {
					return true
				}
			} else if !uiguiTextFns[fn.Sel.Name] {
				return true
			}
			fnName = fn.Sel.Name
		case *ast.Ident:
			if !uiguiTextFns[fn.Name] {
				return true
			}
			fnName = fn.Name
		default:
			return true
		}
		for _, arg := range call.Args {
			if s, ok := englishLiteral(arg); ok && !allowedSink(rel, s) {
				bad = append(bad, fmt.Sprintf("%s:%d  %s(… %q …)", rel,
					fset.Position(arg.Pos()).Line, fnName, truncate(s, 60)))
			}
		}
		return true
	})
	return bad
}

// textFieldLiterals 结构体字面量里直接赋给 `text` 字段的英文字面量。
//
// 只认**渲染单元**那几种结构体：`rowDesc`（设置页的分组标题/页标题）、`mdBlock`（markdown
// 渲染单元）、`rawContent`（折叠区正文）。不能按字段名一刀切——`inputMsg{text: "/new"}` 的
// `text` 是输入缓冲区的内容，字面量正是它该有的样子。
func textFieldLiterals(fset *token.FileSet, rel string, f *ast.File) []string {
	var bad []string
	ast.Inspect(f, func(n ast.Node) bool {
		comp, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		typ, ok := comp.Type.(*ast.Ident)
		if !ok || !textFieldStructs[typ.Name] {
			return true
		}
		for _, el := range comp.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "text" {
				continue
			}
			if s, ok := englishLiteral(kv.Value); ok {
				bad = append(bad, fmt.Sprintf("%s:%d  %s{text: %q}", rel,
					fset.Position(kv.Value.Pos()).Line, typ.Name, truncate(s, 60)))
			}
		}
		return true
	})
	return bad
}

// textFieldStructs 带渲染文案字段 `text` 的结构体。
var textFieldStructs = map[string]bool{
	"rowDesc":    true,
	"mdBlock":    true,
	"rawContent": true,
}

// sinkAllowed 出口扫描里允许写死的英文，每条都要写理由（同 cjkAllowed：按整串或前缀匹配）。
var sinkAllowed = map[string][]string{
	// 产品名，各语言一致，不翻译。
	"internal/adapter/uigui/settings_page_about.go": {"Aquarius"},
}

// commandMetaLiterals 命令元数据里必须是文案表 key 的字面量。
func commandMetaLiterals(fset *token.FileSet, rel string, f *ast.File) []string {
	var bad []string
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || !commandMetaFields[key.Name] {
			return true
		}
		lit, ok := kv.Value.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true // 变量 / 常量 / i18n.T：由 TestKeysResolve 那一侧管
		}
		s, uerr := strconv.Unquote(lit.Value)
		if uerr != nil || s == "" {
			return true
		}
		if _, ok := zhTexts[s]; !ok {
			bad = append(bad, fmt.Sprintf("%s:%d  %s: %q（表里没有这条 key）", rel,
				fset.Position(lit.Pos()).Line, key.Name, truncate(s, 60)))
		}
		return true
	})
	return bad
}

// englishLiteral 该实参是不是一句写死的英文：非空、且含有拉丁字母。
//
// 只认「有字母」而不是「有空格」：`Reason…` 这类单词占位同样要搬进表；反过来 `×`、`· · ·`、
// `—` 是字形不是文案，`""` 是「本组件不要占位」的显式写法，都放过。
func englishLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil || strings.TrimSpace(s) == "" {
		return "", false
	}
	if !strings.ContainsFunc(s, func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
	}) {
		return "", false
	}
	return s, true
}

// allowedSink 该文件的这条字面量是否已开过口子。同 cjkAllowed：整串相等或作为前缀才算，
// 一条 "Aquarius" 只放过它自己，不放过「Aquarius — 首个中文…… 」这类整句。
func allowedSink(rel, s string) bool {
	for _, frag := range sinkAllowed[rel] {
		if s == frag || strings.HasPrefix(s, frag) {
			return true
		}
	}
	return false
}
