package logging

// L4 的两把锁：stderr 出口与日志条目的形状。
//
// 它们守的是同一件事的两面——「日志只有一条路，且每一行都说得清是谁、怎么处置的」：
//   - stderr 出口：`fmt.Fprint*(…stderr…)` 只许出现在本包（它就是 stderr 出口本身）与
//     `fatal` 那几个「面向用户的最终结论」上。漏了这面，有人会绕过日志再写一行裸文本：
//     没有时间戳、没有上下文，还只上 stderr（GUI 双击启动看不见）。
//   - 条目形状：调用点给出的 `log.*` 事件 key 必须紧跟一个 `log.act.*` 行动 key，且 key 的
//     上下文段与调用点给的 `ctx` 一致。缺行动位的那行等于「只说发生了什么，不说怎么处置」，
//     隔天回看没人知道当时是跳过还是照常继续。
//
// 探的是 AST（同 internal/i18n/lock_test.go 的口径）：注释里的示例不算，整串拼接也算不进来。
// 白名单按仓库相对路径给，目录以 `/` 结尾表示整棵子树；每一条都要写得出理由。

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

// lockRepoRoot 仓库根（internal/logging → ../..）。
const lockRepoRoot = "../.."

// lockTops 参与扫描的顶层目录（与 i18n 的两把锁同一份名单）。
var lockTops = []string{"cmd", "internal", "pluginapi"}

// lockSkipDirs 不参与扫描的目录名：测试数据与本地脚手架不是产品代码。
var lockSkipDirs = map[string]bool{"testdata": true, "spike": true, "temp": true, ".git": true}

// stderrPass 一处允许直接写 stderr 的位置：path 是仓库相对路径（目录以 `/` 结尾），
// fn 非空时只放行这个函数（含它内部的闭包）。
type stderrPass struct {
	path string
	fn   string
	why  string
}

// stderrPasses 直写 stderr 的白名单（plan.md L4）。
//
// 判断口径是「这句话是不是**面向用户的最终结论**、是不是**日志出口本身**」——运行日志不得
// 从这里走；要留痕就去 `note` / `port.Log`。
var stderrPasses = []stderrPass{
	{"internal/logging/", "", "日志包自己：文件开不出时退化成只写 stderr（D276），这里就是那个出口"},
	{"cmd/aquarius/main.go", "fatal", "fatal 要「一句话 + 消息框」，不是一条日志行（D276 边界）"},
	{"cmd/aquarius/welcome.go", "", "向导取消报因：面向用户的最终结论，与 fatal 同一种呈现（同时弹框）"},
	{"internal/adapter/uitui/tui.go", "", "TUI 主循环异常退出的兜底：界面已失效，适配器不 import 日志包"},
	{"cmd/linuxgate/", "", "开发工具：交叉编译门禁，不进产品，日志不跟版本走"},
	{"cmd/iconify/", "", "开发工具：生成图标资源，同上"},
	{"cmd/ci/", "", "开发工具：本机门禁编排，stderr 是给开发者的诊断与退出原因"},
}

// logSinkNames 可能是日志出口的调用名。只用来少扫一点：真正的判据是有没有 `log.*` 字面量
// 落在实参位置上（见 TestLogCallsCarryContextAndAction）。
var logSinkNames = map[string]bool{
	"log": true, "logf": true, "Log": true, "Write": true,
	"Debug": true, "Info": true, "Warn": true, "Error": true, "note": true, "noteText": true,
}

// logCtxIdents `logging.Context` 的常量名（`logging.LLM` / 包内的 `LLM`）。
var logCtxIdents = map[string]bool{
	"Boot": true, "Attach": true, "LLM": true, "Audio": true, "Job": true, "Delegate": true,
	"Plugin": true, "Skill": true, "Character": true, "Session": true, "Perm": true,
	"UI": true, "Web": true,
}

// logCtxValues 上下文的取值（`"boot"` / `"plugin"` …）——由 Contexts 反推，避免两份名单。
var logCtxValues = func() map[string]bool {
	out := make(map[string]bool, len(Contexts))
	for _, c := range Contexts {
		out[string(c)] = true
	}
	return out
}()

// TestStderrWritesHaveOwner 守住「stderr 只有已登记的那几个出口」。
func TestStderrWritesHaveOwner(t *testing.T) {
	var bad []string
	walkLockFiles(t, func(rel, path string) {
		fset, f := parseLockFile(t, path)
		if f == nil {
			return
		}
		v := &stderrVisitor{fset: fset, rel: rel}
		ast.Walk(v, f)
		bad = append(bad, v.bad...)
	})
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("这些地方绕过运行日志直写 stderr %d 处（D276）：\n  %s\n\n"+
			"要留痕就改 note / port.Log（五段式：时间 / 级别 / 上下文 / 事件 / 行动）；\n"+
			"确属「面向用户的最终结论」（fatal、向导取消）才加进 lock_test.go 的 stderrPasses，并写理由。",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// stderrVisitor 带祖先栈的单遍访问：命中时自栈顶向下找所在函数。
type stderrVisitor struct {
	fset *token.FileSet
	rel  string
	stk  []ast.Node
	bad  []string
}

func (v *stderrVisitor) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		v.stk = v.stk[:len(v.stk)-1]
		return nil
	}
	v.stk = append(v.stk, n)
	call, ok := n.(*ast.CallExpr)
	if !ok || !isFmtPrint(call) || !anyArgMentionsStderr(call) {
		return v
	}
	pos := v.fset.Position(call.Pos())
	if stderrAllowed(v.rel, enclosingFunc(v.stk)) {
		return v
	}
	v.bad = append(v.bad, fmt.Sprintf("%s:%d  fmt.%s(…stderr…)",
		v.rel, pos.Line, call.Fun.(*ast.SelectorExpr).Sel.Name))
	return v
}

// TestLogCallsCarryContextAndAction 守住「每一行日志都说得出上下文，且带着处置」。
func TestLogCallsCarryContextAndAction(t *testing.T) {
	var bad []string
	walkLockFiles(t, func(rel, path string) {
		fset, f := parseLockFile(t, path)
		if f == nil {
			return
		}
		consts := fileStringConsts(f)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !logSinkNames[calleeName(call)] {
				return true
			}
			idx := eventArgIndex(call)
			if idx < 0 {
				return true
			}
			event, _ := strconv.Unquote(call.Args[idx].(*ast.BasicLit).Value)
			pos := fset.Position(call.Pos())
			where := fmt.Sprintf("%s:%d  %s", rel, pos.Line, event)

			if msg := checkAction(call, idx); msg != "" {
				bad = append(bad, where+"  "+msg)
			}
			if msg := checkContext(call, idx, event, consts); msg != "" {
				bad = append(bad, where+"  "+msg)
			}
			return true
		})
	})
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("日志条目形状不对 %d 处（D276/contracts §7.9）：\n  %s\n\n"+
			"每一条都要齐五段：级别、上下文、事件 key（`log.<ctx>.<what>`）、行动 key（`log.act.*`）。\n"+
			"事件 key 紧跟的那个实参就是行动位，不许省；上下文段必须是 logging.Contexts 里的一个，\n"+
			"且与调用点给的 ctx 一致。",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// checkAction 事件 key 的下一个实参必须是 `log.act.*` 字面量。
func checkAction(call *ast.CallExpr, idx int) string {
	if idx+1 >= len(call.Args) {
		return "缺行动位（事件 key 后面没有 log.act.*）"
	}
	lit, ok := call.Args[idx+1].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "行动位不是字面量 key（拼出来的行动 key 进不了取词表）"
	}
	act, err := strconv.Unquote(lit.Value)
	if err != nil || !strings.HasPrefix(act, "log.act.") {
		return "行动位不是 log.act.* key"
	}
	return ""
}

// checkContext 上下文两处要对得上：key 的 `log.<ctx>.` 段是已登记的上下文；调用点若给了
// ctx（字面量、`logCtx` 常量、`logging.Xxx`），必须与 key 的段一致。
func checkContext(call *ast.CallExpr, idx int, event string, consts map[string]string) string {
	rest := strings.TrimPrefix(event, "log.")
	seg, _, ok := strings.Cut(rest, ".")
	if !ok || seg == "" {
		return "事件 key 没有 `log.<ctx>.<what>` 的上下文段"
	}
	if !logCtxValues[seg] {
		return fmt.Sprintf("上下文 %q 没在 logging.Contexts 里登记（新增子系统要在 logging 头里加一个）", seg)
	}
	got := ctxBefore(call, idx, consts)
	if got != "" && got != seg {
		return fmt.Sprintf("上下文对不上：key 说 %q，调用点给 %q", seg, got)
	}
	return ""
}

// ctxBefore 在事件 key 之前的实参里从右往左找一个能定下来的上下文。
func ctxBefore(call *ast.CallExpr, idx int, consts map[string]string) string {
	for i := idx - 1; i >= 0; i-- {
		switch x := call.Args[i].(type) {
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				continue
			}
			if s, err := strconv.Unquote(x.Value); err == nil && logCtxValues[s] {
				return s
			}
		case *ast.Ident:
			if x.Name == "logCtx" {
				if v, ok := consts["logCtx"]; ok {
					return v
				}
				continue
			}
			if logCtxIdents[x.Name] {
				return strings.ToLower(x.Name)
			}
		case *ast.SelectorExpr:
			if logCtxIdents[x.Sel.Name] {
				return strings.ToLower(x.Sel.Name)
			}
		}
	}
	return ""
}

// eventArgIndex 实参里第一个 `log.*`（非 `log.act.*`）字面量的下标；没有则 -1。
func eventArgIndex(call *ast.CallExpr) int {
	for i, arg := range call.Args {
		lit, ok := arg.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil || !strings.HasPrefix(s, "log.") || strings.HasPrefix(s, "log.act.") {
			continue
		}
		return i
	}
	return -1
}

// TestContextsAreLowercaseEnums 守住上下文的形态：英文小写枚举，是分类不是文案。
func TestContextsAreLowercaseEnums(t *testing.T) {
	for _, c := range Contexts {
		s := string(c)
		if s == "" || s != strings.ToLower(s) || strings.ContainsAny(s, ". _") {
			t.Errorf("上下文 %q：应是小写英文单词（分类标识，不进文案表，也不许带点或空格）", s)
		}
	}
}

// walkLockFiles 遍历参与扫描的非测试 Go 文件。
func walkLockFiles(t *testing.T, fn func(rel, path string)) {
	t.Helper()
	for _, top := range lockTops {
		base := filepath.Join(lockRepoRoot, top)
		if _, err := os.Stat(base); err != nil {
			continue // 该目录尚未建立
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if lockSkipDirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel := relLockPath(t, path)
			fn(rel, path)
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s: %v", base, err)
		}
	}
}

// parseLockFile 解析单个文件；语法错误交给 go build。
func parseLockFile(t *testing.T, path string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, nil
	}
	return fset, f
}

// relLockPath 仓库相对路径（斜杠分隔）。用 filepath.Rel 而不是 TrimPrefix：root 写成
// "../.." 时前缀里正反斜杠混着，Windows 上匹配不上（同 i18n 那边踩过的坑）。
func relLockPath(t *testing.T, path string) string {
	t.Helper()
	rel, err := filepath.Rel(lockRepoRoot, path)
	if err != nil {
		t.Fatalf("相对路径 %s: %v", path, err)
	}
	return filepath.ToSlash(rel)
}

// isFmtPrint 该调用是不是 fmt.Fprint / Fprintf / Fprintln。
func isFmtPrint(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "fmt" {
		return false
	}
	switch sel.Sel.Name {
	case "Fprint", "Fprintf", "Fprintln":
		return true
	}
	return false
}

// anyArgMentionsStderr 实参里有没有 stderr（`stderr` / `w.stderr` / `os.Stderr`）。
func anyArgMentionsStderr(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		hit := false
		ast.Inspect(arg, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if x.Name == "stderr" || x.Name == "Stderr" {
					hit = true
				}
			case *ast.SelectorExpr:
				if x.Sel.Name == "Stderr" {
					hit = true
				}
			}
			return !hit
		})
		if hit {
			return true
		}
	}
	return false
}

// enclosingFunc 最近的外层函数名（闭包归它所属的那个函数）。
func enclosingFunc(stk []ast.Node) string {
	for i := len(stk) - 1; i >= 0; i-- {
		if fd, ok := stk[i].(*ast.FuncDecl); ok {
			return fd.Name.Name
		}
	}
	return ""
}

// stderrAllowed 该位置是否已登记（why 只给读表的人看，不参与判定）。
func stderrAllowed(rel, fn string) bool {
	for _, p := range stderrPasses {
		if p.path != rel && !(strings.HasSuffix(p.path, "/") && strings.HasPrefix(rel, p.path)) {
			continue
		}
		if p.fn != "" && p.fn != fn {
			continue
		}
		return true
	}
	return false
}

// calleeName 调用的名字：`h.logf` → logf，`w.note` → note。
func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// fileStringConsts 文件里 `名 = "字面量"` 的常量（`logCtx = "plugin"` 这类）。
func fileStringConsts(f *ast.File) map[string]string {
	out := make(map[string]string)
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			if s, err := strconv.Unquote(lit.Value); err == nil {
				out[name.Name] = s
			}
		}
		return true
	})
	return out
}
