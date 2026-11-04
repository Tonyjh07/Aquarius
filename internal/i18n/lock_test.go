package i18n

// I4 的两把锁：字面量扫描与缺键扫描。
//
// 它们守的是同一件事的两面——「界面上说的每一句都来自文案表」，缺一面就会漏：
//   - 字面量扫描：`_test.go` 之外的源码里不许再出现中文（除母版表与白名单）。漏了这面，
//     新写的文案绕过表，切到 en 时那一句还是中文。
//   - 缺键扫描：代码里引用的 key 必须在表里。漏了这面，`T` 会把 key 原样印到界面上
//     （`ui.chat.foo`），而缺键不报错，只有打开那个窗口才看得见。
//
// 探的是**字符串字面量**（AST），不是正则扫行：注释里的中文是维护者索引，不算文案；
// 反引号里的长句要一起看，否则 raw string 会成为后门。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// i18nRepoRoot 仓库根（internal/i18n → ../..）。
const i18nRepoRoot = "../.."

// i18nScannedTops 参与扫描的顶层目录。新增顶层包时要加进来，否则锁管不到它。
var i18nScannedTops = []string{"cmd", "internal", "pluginapi"}

// i18nSkippedDirs 不参与扫描的目录名。
//
//   - `testdata` / `spike` / `temp`：测试数据与本地脚手架，不是产品代码；
//   - `docsync` / `iconify` / `linuxgate`：仓库自用的开发者工具（刷文档标记、生成图标、
//     交叉编译门禁），读者是改代码的人，文案不跟界面语言走。
var i18nSkippedDirs = map[string]bool{
	"testdata": true, "spike": true, "temp": true, ".git": true,
	"docsync": true, "iconify": true, "linuxgate": true,
}

// cjkAllowed 允许留在代码里的中文字面量，每条都要写理由。
//
// 键是仓库相对路径（斜杠分隔），值是**内容片段**而不是文件名：同一个文件里后加的文案
// 仍会被抓住。这是刻意选的粒度——整文件豁免等于给那份文件开后门。片段按「整串相等或以
// 它开头」匹配，所以 "行" 只放过正好是 "行" 的那一条，不会放过「行高」「一行」。
var cjkAllowed = map[string][]string{
	// 选语言 / 选日志级别之前就要报的错：这两个包不能依赖文案表（鸡生蛋），而且这两句
	// 只在配置写错时出现一次，读者是改配置的人。
	"internal/i18n/i18n.go":       {"ui.lang=%q 仅支持 auto | zh | en"},
	"internal/logging/logging.go": {"日志级别 %q 仅支持", "日志形态 %q 仅支持"},

	// `/model probe` 的关键词表：匹配对象是**服务端报错原文与模型回复**，与界面语言无关。
	// 搬进表后，界面切 en 时这些词不再参与匹配，探测结论会变差——它们是数据，不是文案。
	"internal/adapter/llm/probe.go": {
		"红", "红色", "棕", "橙", "灰", "白", "粉", "紫", "绿", "蓝", "青", "黄", "黑",
		"看不到", "看不见", "没有图", "未提供图片", "不是图片", "图片不支持", "无法看到", "图片",
	},
	"internal/adapter/llm/openai.go": {"不支持", "无法识别", "未支持"},

	// 诊断：读者是改代码的人，界面上看不到（ico 解析失败退回默认图标、羽化观测打印、
	// 位图解析细节、托盘与热键的观测打印、GDI 句柄释放告警）。位图那几条的外层调用方
	// 已经套了一句「读取剪贴板图片失败」，用户看到的是那句。
	"internal/adapter/uigui/icon.go":                   {"ico: "},
	"internal/adapter/uigui/fade.go":                   {"[fade] "},
	"internal/adapter/uigui/platform/clipimg.go":       {"位图", "不支持的位深", "不支持的位图压缩方式"},
	"internal/adapter/uigui/platform/shell_windows.go": {"[tray]", "[hotkey]"},
	"internal/adapter/uigui/platform/tray_windows.go":  {"[tray]"},
	"internal/adapter/uigui/platform/win32_windows.go": {
		"[dib]", "DeleteObject 位图失败", "DeleteDC 失败",
	},

	// 行高量测用的假文本：只喂给 measure，用来算 baseline，不出现在任何界面上。
	"internal/adapter/uigui/input.go": {"行"},

	// 界面语言选项里的中文自名（D275）：语言名按惯例用自己的语言写，不翻译。进了表就有
	// 两份译文各写一遍的风险，而且 en 表里出现中文会被 TestEnHasNoCJK 拦下——所以它留在
	// 代码里，由这条豁免放行。"English" 无中文，不需列。
	"internal/adapter/uigui/settings.go": {"简体中文"},
}

// i18nDynamicKeyPrefixes 拼接取词的已知前缀。
//
// 静态解析看不进 `"ui.settings.page." + Key()` 这种拼法，所以前缀在这里登记，另一侧由
// 使用方的测试逐项断言（uigui 的 TestPageKeysResolve）——两边合起来才是闭环：这里保证
// 前缀下的 key 都是英文标识，那边保证每个页面标识都取得到词。
var i18nDynamicKeyPrefixes = []string{
	"ui.settings.page.",
}

// TestNoCJKLiterals 守住「界面文案只来自文案表」：非测试源码里出现中文字面量即失败。
func TestNoCJKLiterals(t *testing.T) {
	root := i18nRepoRoot
	var bad []string
	for _, top := range i18nScannedTops {
		base := filepath.Join(root, top)
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
			rel := relPath(t, root, path)
			if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			if isMasterTable(rel) {
				return nil // zh_*.go 就是中文的落点；en_*.go 由同构测试管
			}
			bad = append(bad, cjkLiteralsIn(t, rel, path)...)
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s: %v", base, err)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("非测试源码里仍有中文 %d 处（D275/contracts §7.8：文案只许在 internal/i18n/zh*.go）：\n"+
			"  %s\n\n"+
			"改法：把这句话搬进对应的区域表，代码里留 i18n.T/Tf 取词。\n"+
			"确实不该进表的（诊断、内部断言、选语言之前就要报的错）才进 lock_test.go 的 cjkAllowed，并写理由。",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// TestKeysResolve 守住「代码引用的 key 都在表里」。
func TestKeysResolve(t *testing.T) {
	keys, err := referencedKeys(t)
	if err != nil {
		t.Fatalf("%v", err)
	}
	var missing []string
	for key, where := range keys {
		if _, ok := zhTexts[key]; !ok {
			missing = append(missing, fmt.Sprintf("%s  (引用处 %s)", key, where))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("代码引用了表里没有的 key（界面会直接显示 key 本身）：\n  %s\n\n"+
			"补进对应区域表的 zh 与 en 两侧；拼错的 key 改引用。", strings.Join(missing, "\n  "))
	}
}

// TestDynamicKeyPrefixesResolve 守住拼接取词的前缀：登记的前缀下，key 必须是英文标识，
// 且确实有 key 用着它（否则前缀已废弃，该从白名单里删掉）。
func TestDynamicKeyPrefixesResolve(t *testing.T) {
	for _, prefix := range i18nDynamicKeyPrefixes {
		n := 0
		for key := range zhTexts {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			n++
			suffix := strings.TrimPrefix(key, prefix)
			if suffix == "" || strings.ContainsFunc(suffix, func(r rune) bool {
				return r == '.' || unicode.Is(unicode.Han, r) || unicode.IsUpper(r)
			}) {
				t.Errorf("前缀 %q 下的 key %q：后缀应是单个小写英文标识（与 Key() 的返回值逐字相等）", prefix, key)
			}
		}
		if n == 0 {
			t.Errorf("前缀 %q 下没有任何 key：要么前缀写错，要么这条白名单该删了", prefix)
		}
	}
}

// TestNoOrphanKeys 守住「表里没有没人用的 key」：母版里有、源码里从未出现即失败。
//
// 判据是**字符串字面量等于 key**，不是「有 i18n.T 调用」：日志（`log.boot.*`）、命令目录
// 的元数据都经变量传给调用方，只认调用点会误杀一大片。拼接取词（`"ui.settings.page." + Key()`）
// 由 i18nDynamicKeyPrefixes 放行。
//
// 孤儿 key 的成本不是运行时的几个字节，是下一个人读表时要判断「这条还在用吗」。
func TestNoOrphanKeys(t *testing.T) {
	used, err := allStringLiterals(t)
	if err != nil {
		t.Fatalf("%v", err)
	}
	var orphan []string
	for key := range zhTexts {
		if used[key] {
			continue
		}
		dynamic := false
		for _, prefix := range i18nDynamicKeyPrefixes {
			if strings.HasPrefix(key, prefix) {
				dynamic = true
				break
			}
		}
		if !dynamic {
			orphan = append(orphan, key)
		}
	}
	if len(orphan) > 0 {
		sort.Strings(orphan)
		t.Fatalf("表里有 %d 条没人用的 key：\n  %s\n\n"+
			"删掉它（连同 en 侧的同一 key）；如果它其实经拼接取词，把前缀登记进 i18nDynamicKeyPrefixes。",
			len(orphan), strings.Join(orphan, "\n  "))
	}
}

// TestEnExpansionReport 不是断言，是给「en 下的布局走查」的一份清单：
// 英文比中文长的行最容易在手绘布局里溢出，先按倍数列出来，人再看。
func TestEnExpansionReport(t *testing.T) {
	type row struct {
		key       string
		zh, en    string
		expansion float64
	}
	var rows []row
	for key, zh := range zhTexts {
		en := enTexts[key]
		rz, re := displayWidth(zh), displayWidth(en)
		if re < 60 {
			continue // 短句再膨胀也放得下；真正会溢出的是一行装不下的那些
		}
		if exp := float64(re) / float64(rz); exp >= 1.2 {
			rows = append(rows, row{key, zh, en, exp})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].expansion > rows[j].expansion })
	t.Logf("英文宽度 ≥60 格且比中文宽 1.2 倍以上的文案 %d 条（走查时优先看这些所在的行）：", len(rows))
	for i, r := range rows {
		if i >= 40 {
			t.Logf("  ……其余 %d 条略", len(rows)-40)
			break
		}
		t.Logf("  %.2fx  %-40s %q → %q", r.expansion, r.key, r.zh, r.en)
	}
}

// isMasterTable 界面语言表本身：中文的落点。
func isMasterTable(rel string) bool {
	if !strings.HasPrefix(rel, "internal/i18n/") {
		return false
	}
	base := filepath.Base(rel)
	return strings.HasPrefix(base, "zh") || strings.HasPrefix(base, "en")
}

// cjkLiteralsIn 解析单个文件，列出含中文的字符串字面量。
func cjkLiteralsIn(t *testing.T, rel, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil // 语法错误交给 go build
	}
	skip := exemptLits(f)
	var bad []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || skip[lit.Pos()] {
			return true
		}
		s, uerr := strconv.Unquote(lit.Value)
		if uerr != nil || !hasHan(s) || allowedLiteral(rel, s) {
			return true
		}
		pos := fset.Position(lit.Pos())
		bad = append(bad, fmt.Sprintf("%s:%d  %q", rel, pos.Line, truncate(s, 60)))
		return true
	})
	return bad
}

// exemptLits 两类不进表的实参：
//
//   - `panic(...)`：内部一致性断言，读者是改代码的人（I2 口径），不上界面；
//   - `Logf(...)` / `logf(...)`：运行日志的注入回调。日志正文归 `log.*` 段且由 L3 迁到
//     `internal/logging`（D276），在那之前搬进界面表只是把同一句话换两次地方。
//     日志文案的扫描锁是 L4 的事，别在这里兜。
func exemptLits(f *ast.File) map[token.Pos]bool {
	out := make(map[token.Pos]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name != "panic" {
				return true
			}
		case *ast.SelectorExpr:
			if fn.Sel.Name != "Logf" && fn.Sel.Name != "logf" {
				return true
			}
		default:
			return true
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok {
					out[lit.Pos()] = true
				}
				return true
			})
		}
		return true
	})
	return out
}

// referencedKeys 收集源码里的 i18n.T / i18n.Tf 字面量 key。
func referencedKeys(t *testing.T) (map[string]string, error) {
	t.Helper()
	out := make(map[string]string)
	for _, top := range i18nScannedTops {
		base := filepath.Join(i18nRepoRoot, top)
		if _, err := os.Stat(base); err != nil {
			continue
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
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil
			}
			rel := relPath(t, i18nRepoRoot, path)
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "i18n" {
					return true
				}
				if sel.Sel.Name != "T" && sel.Sel.Name != "Tf" {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true // 拼接取词：由 i18nDynamicKeyPrefixes 那一侧管
				}
				key, uerr := strconv.Unquote(lit.Value)
				if uerr != nil {
					return true
				}
				if _, seen := out[key]; !seen {
					out[key] = fmt.Sprintf("%s:%d", rel, fset.Position(lit.Pos()).Line)
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("遍历 %s: %w", base, err)
		}
	}
	return out, nil
}

// isTableFile 该文件是不是文案表的定义（internal/i18n 下的 zh_*.go / en_*.go）。
func isTableFile(rel string) bool {
	if !strings.HasPrefix(rel, "internal/i18n/") {
		return false
	}
	name := path.Base(rel)
	return strings.HasPrefix(name, "zh_") || strings.HasPrefix(name, "en_")
}

// allStringLiterals 收集仓库里所有字符串字面量（含测试：测试引用的 key 也是活的）。
func allStringLiterals(t *testing.T) (map[string]bool, error) {
	t.Helper()
	out := make(map[string]bool)
	for _, top := range i18nScannedTops {
		base := filepath.Join(i18nRepoRoot, top)
		if _, err := os.Stat(base); err != nil {
			continue
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
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			// 文案表的定义文件本身不算「用」：连同它们一起扫的话，表里每一条 key 都被自己
			// 的定义命中一次，孤儿检查永远是绿的（实测：塞一条假 key 进去也不报）。
			if rel, rerr := filepath.Rel(i18nRepoRoot, path); rerr == nil && isTableFile(filepath.ToSlash(rel)) {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, uerr := strconv.Unquote(lit.Value); uerr == nil {
						out[v] = true
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("遍历 %s: %w", base, err)
		}
	}
	return out, nil
}

// allowedLiteral 该文件是否为这条字面量开了口子。片段按「整串相等或作为前缀」匹配：
// 用 Contains 的话，一条 "行" 就把该文件里所有带「行」的文案都放过去了。
func allowedLiteral(rel, s string) bool {
	for _, frag := range cjkAllowed[rel] {
		if s == frag || strings.HasPrefix(s, frag) {
			return true
		}
	}
	return false
}

func hasHan(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) })
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// relPath 仓库相对路径（斜杠分隔）。
//
// 不用 TrimPrefix(root + separator)：root 写成 "../.."，拼接后前缀里正反斜杠混着，
// 在 Windows 上匹配不上，rel 就会带着 "../../" 一路传下去——母版表因此被当成普通文件扫，
// 白白报出上千条。
func relPath(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("相对路径 %s: %v", path, err)
	}
	return filepath.ToSlash(rel)
}

// displayWidth 按**显示宽度**估算：CJK 与全角标点占两格，其余占一格。
//
// 按 rune 个数算倍率没有意义——「会话」是两个字、｢session｣ 是七个字符，可比的是屏幕宽度，
// 不是字符数。这个估算只用来排序走查清单，不参与断言。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r >= 0x1100 && r <= 0x115F, // 谚文字母
			r >= 0x2E80 && r <= 0xA4CF, // CJK 部首、假名、汉字
			r >= 0xAC00 && r <= 0xD7A3, // 谚文音节
			r >= 0xF900 && r <= 0xFAFF, // CJK 兼容
			r >= 0xFE30 && r <= 0xFE4F, // CJK 兼容形式
			r >= 0xFF00 && r <= 0xFF60, // 全角 ASCII
			r >= 0xFFE0 && r <= 0xFFE6:
			w += 2
		default:
			w++
		}
	}
	return w
}
