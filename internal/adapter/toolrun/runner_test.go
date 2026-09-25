package toolrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

// stubTool 固定返回的工具；可选实现 FileTarget。
type stubTool struct {
	spec   tool.Spec
	target string  // 非空 = 申报文件目标（op 恒 write，够用）
	op     perm.Op // 申报的操作（默认 write）
	out    string
	delay  time.Duration // 模拟慢工具
	err    error
}

func (s *stubTool) Spec() tool.Spec { return s.spec }

func (s *stubTool) Execute(ctx context.Context, _ tool.Call) (tool.Result, error) {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return tool.Result{}, ctx.Err()
		}
	}
	if s.err != nil {
		return tool.Result{}, s.err
	}
	return tool.Result{OK: true, Output: s.out}, nil
}

func (s *stubTool) Target(context.Context, tool.Call) (string, perm.Op, bool) {
	if s.target == "" {
		return "", 0, false
	}
	op := s.op
	return s.target, op, true
}

// scriptConf 记录提示并按脚本应答的确认器。
type scriptConf struct {
	answers []bool
	asked   []string
	err     error
}

func (c *scriptConf) Confirm(_ context.Context, prompt string) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	c.asked = append(c.asked, prompt)
	if len(c.answers) == 0 {
		return false, errors.New("scriptConf: 没有对应脚本的确认请求")
	}
	a := c.answers[0]
	c.answers = c.answers[1:]
	return a, nil
}

func fileTool(name, path string, op perm.Op, risk tool.Risk, out string) *stubTool {
	return &stubTool{spec: tool.Spec{Name: name, Risk: risk}, target: path, op: op, out: out}
}

func execTool(name string, risk tool.Risk, out string) *stubTool {
	return &stubTool{spec: tool.Spec{Name: name, Risk: risk}, out: out}
}

func c(name, args string) tool.Call {
	return tool.Call{ID: tool.CallID("c1"), Name: name, Args: json.RawMessage(args)}
}

func levelFn(l perm.Level) func() perm.Level { return func() perm.Level { return l } }

// ---------------------------------------------------------------------------
// 查找与回填
// ---------------------------------------------------------------------------

func TestExecuteUnknownTool(t *testing.T) {
	r := New(Options{})
	res, err := r.Execute(context.Background(), c("nope", `{}`))
	if err != nil || res.OK || !strings.Contains(res.Err, "未知工具") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestSpecsRegistrationOrderAndReplace(t *testing.T) {
	a := execTool("a", tool.Safe, "a")
	b := execTool("b", tool.Safe, "b")
	b2 := execTool("b", tool.Safe, "b2")
	r := New(Options{Tools: []port.Tool{a, b}})
	r.Add(b2) // 同名覆盖
	specs, err := r.Specs(context.Background())
	if err != nil || len(specs) != 2 || specs[0].Name != "a" || specs[1].Name != "b" {
		t.Fatalf("specs=%+v err=%v", specs, err)
	}
	res, _ := r.Execute(context.Background(), c("b", `{}`))
	if res.Output != "b2" {
		t.Fatalf("覆盖未生效: %+v", res)
	}
}

func TestExecuteToolErrorPropagates(t *testing.T) {
	r := New(Options{Tools: []port.Tool{&stubTool{
		spec: tool.Spec{Name: "boom", Risk: tool.Safe}, err: errors.New("炸了"),
	}}})
	_, err := r.Execute(context.Background(), c("boom", `{}`))
	if err == nil || !strings.Contains(err.Error(), "炸了") {
		t.Fatalf("err=%v", err)
	}
}

// ---------------------------------------------------------------------------
// 权限矩阵（D22 两列分工）
// ---------------------------------------------------------------------------

// TestFileColumnMatrix 文件类只看路径格：sandbox 内/外 × 四档等级。
func TestFileColumnMatrix(t *testing.T) {
	sbx := mustAbs(t, "sandbox")
	inside := filepath.Join(sbx, "new.txt")
	outside := mustAbs(t, "elsewhere.txt")

	cases := []struct {
		name    string
		level   perm.Level
		path    string
		wantAsk bool
	}{
		{"strict sandbox 内免确认", perm.Strict, inside, false},
		{"strict sandbox 外询问", perm.Strict, outside, true},
		{"read-only 任何写询问", perm.ReadOnly, inside, true},
		{"permissive 任何写免", perm.Permissive, outside, false},
		{"full-access 任何写免", perm.FullAccess, outside, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conf := &scriptConf{answers: []bool{true}}
			r := New(Options{
				Tools:       []port.Tool{fileTool("fw", tc.path, perm.OpWrite, tool.Confirm, "ok")},
				Confirmer:   conf,
				Level:       levelFn(tc.level),
				SandboxPath: sbx,
			})
			res, err := r.Execute(context.Background(), c("fw", `{}`))
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if gotAsk := len(conf.asked) > 0; gotAsk != tc.wantAsk {
				t.Fatalf("ask = %v, want %v（asked=%v res=%+v）", gotAsk, tc.wantAsk, conf.asked, res)
			}
		})
	}
}

// TestFileReadAlwaysAllowed 文件读全等级免确认（读格恒 r）。
func TestFileReadAlwaysAllowed(t *testing.T) {
	for _, l := range perm.Levels {
		conf := &scriptConf{}
		r := New(Options{
			Tools:     []port.Tool{fileTool("fr", mustAbs(t, "any.txt"), perm.OpRead, tool.Safe, "data")},
			Confirmer: conf, Level: levelFn(l), SandboxPath: mustAbs(t, "sbx"),
		})
		res, err := r.Execute(context.Background(), c("fr", `{}`))
		if err != nil || !res.OK || len(conf.asked) != 0 {
			t.Fatalf("%s: res=%+v err=%v asked=%v", l, res, err, conf.asked)
		}
	}
}

// TestExecColumnMatrix 执行类只看工具列：Safe 全免；Confirm 仅 full-access 免。
func TestExecColumnMatrix(t *testing.T) {
	wantAsk := map[perm.Level]bool{
		perm.ReadOnly: true, perm.Strict: true, perm.Permissive: true, perm.FullAccess: false,
	}
	for _, l := range perm.Levels {
		conf := &scriptConf{answers: []bool{true}}
		r := New(Options{
			Tools:     []port.Tool{execTool("term", tool.Confirm, "ran")},
			Confirmer: conf, Level: levelFn(l),
		})
		if _, err := r.Execute(context.Background(), c("term", `{}`)); err != nil {
			t.Fatalf("%s: %v", l, err)
		}
		if got := len(conf.asked) > 0; got != wantAsk[l] {
			t.Fatalf("%s: ask = %v, want %v", l, got, wantAsk[l])
		}
		// Safe 全等级免确认。
		conf2 := &scriptConf{}
		r2 := New(Options{
			Tools:     []port.Tool{execTool("think", tool.Safe, "ok")},
			Confirmer: conf2, Level: levelFn(l),
		})
		if _, err := r2.Execute(context.Background(), c("think", `{}`)); err != nil {
			t.Fatalf("%s safe: %v", l, err)
		}
		if len(conf2.asked) != 0 {
			t.Fatalf("%s safe 不应确认", l)
		}
	}
}

// TestConfirmDeny 报 OK=false 且带"用户拒绝"（回填模型，§10）。
func TestConfirmDeny(t *testing.T) {
	conf := &scriptConf{answers: []bool{false}}
	toolRan := false
	r := New(Options{
		Tools:     []port.Tool{&stubTool{spec: tool.Spec{Name: "fw", Risk: tool.Confirm}}},
		Confirmer: conf, Level: levelFn(perm.Strict),
	})
	_ = toolRan
	res, err := r.Execute(context.Background(), c("fw", `{"p":1}`))
	if err != nil || res.OK || !strings.Contains(res.Err, "用户拒绝") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(conf.asked) != 1 || !strings.Contains(conf.asked[0], "fw") {
		t.Fatalf("asked=%v", conf.asked)
	}
}

// TestConfirmPromptCarriesPath 确认提示带目标路径与参数（用户看得见）。
func TestConfirmPromptCarriesPath(t *testing.T) {
	conf := &scriptConf{answers: []bool{false}}
	r := New(Options{
		Tools:       []port.Tool{fileTool("memory_write", mustAbs(t, "m.md"), perm.OpWrite, tool.Confirm, "")},
		Confirmer:   conf,
		Level:       levelFn(perm.Strict),
		SandboxPath: mustAbs(t, "sbx"),
	})
	_, _ = r.Execute(context.Background(), c("memory_write", `{"name":"memories.md"}`))
	if len(conf.asked) != 1 ||
		!strings.Contains(conf.asked[0], "m.md") ||
		!strings.Contains(conf.asked[0], "memories.md") {
		t.Fatalf("prompt=%v", conf.asked)
	}
}

// TestAskWithoutConfirmerFailClosed 未配置确认器：报错而非静默放行。
func TestAskWithoutConfirmerFailClosed(t *testing.T) {
	r := New(Options{
		Tools: []port.Tool{&stubTool{spec: tool.Spec{Name: "fw", Risk: tool.Confirm}}},
		Level: levelFn(perm.Strict),
	})
	if _, err := r.Execute(context.Background(), c("fw", `{}`)); err == nil ||
		!strings.Contains(err.Error(), "Confirmer") {
		t.Fatalf("err=%v", err)
	}
}

// TestConfirmErrorPropagates 确认器报错（含 ctx 取消）原样上抛。
func TestConfirmErrorPropagates(t *testing.T) {
	r := New(Options{
		Tools:     []port.Tool{&stubTool{spec: tool.Spec{Name: "fw", Risk: tool.Confirm}}},
		Confirmer: &scriptConf{err: context.Canceled},
		Level:     levelFn(perm.Strict),
	})
	if _, err := r.Execute(context.Background(), c("fw", `{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

// TestLevelSwitchLive Level 是函数：运行时切换立即生效（/permission 语义）。
func TestLevelSwitchLive(t *testing.T) {
	cur := perm.Strict
	conf := &scriptConf{}
	r := New(Options{
		Tools:     []port.Tool{&stubTool{spec: tool.Spec{Name: "fw", Risk: tool.Confirm}}},
		Confirmer: conf, Level: func() perm.Level { return cur },
	})
	// strict → 询问（脚本走尽即报错，先给一个答案）
	conf.answers = []bool{true}
	if _, err := r.Execute(context.Background(), c("fw", `{}`)); err != nil {
		t.Fatalf("strict: %v", err)
	}
	cur = perm.FullAccess
	if _, err := r.Execute(context.Background(), c("fw", `{}`)); err != nil {
		t.Fatalf("full-access: %v", err)
	}
	if len(conf.asked) != 1 {
		t.Fatalf("切到 full-access 后不应再问: %v", conf.asked)
	}
}

// ---------------------------------------------------------------------------
// 超时与裁剪
// ---------------------------------------------------------------------------

// blockTool 无视 ctx 的阻塞工具（模拟卡死的系统调用/慢盘）。
type blockTool struct {
	spec tool.Spec
	d    time.Duration
}

func (s *blockTool) Spec() tool.Spec { return s.spec }

func (s *blockTool) Execute(context.Context, tool.Call) (tool.Result, error) {
	time.Sleep(s.d)
	return tool.Result{OK: true, Output: "late"}, nil
}

// TestExecuteHardTimeout 工具无视 ctx 阻塞时，runner 仍在超时点回填 OK=false 返回
// （P1-3：协作式超时对阻塞型 IO 无效 → 旁路 goroutine 硬超时）。
func TestExecuteHardTimeout(t *testing.T) {
	r := New(Options{
		Tools:   []port.Tool{&blockTool{spec: tool.Spec{Name: "blocked", Risk: tool.Safe}, d: time.Second}},
		Timeout: 30 * time.Millisecond,
	})
	start := time.Now()
	res, err := r.Execute(context.Background(), c("blocked", `{}`))
	if err != nil {
		t.Fatalf("超时应作结果回填而非错误: %v", err)
	}
	if res.OK || !strings.Contains(res.Err, "超时") {
		t.Fatalf("res=%+v", res)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("硬超时未生效: %s", time.Since(start))
	}
	// 迟到的结果落进带缓冲通道：无接收方也不阻塞（goroutine 可自行结束）。
}

// TestExecuteTimeout 慢工具超时 → OK=false 带"超时"；父 ctx 未被误伤。
func TestExecuteTimeout(t *testing.T) {
	r := New(Options{
		Tools:   []port.Tool{&stubTool{spec: tool.Spec{Name: "slow", Risk: tool.Safe}, delay: time.Second}},
		Timeout: 30 * time.Millisecond,
	})
	start := time.Now()
	res, err := r.Execute(context.Background(), c("slow", `{}`))
	if err != nil {
		t.Fatalf("超时应作结果回填而非错误: %v", err)
	}
	if res.OK || !strings.Contains(res.Err, "超时") {
		t.Fatalf("res=%+v", res)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("未按超时返回: %s", time.Since(start))
	}
}

// TestExecuteParentCancelPropagates 父 ctx 取消原样上抛（区别于超时）。
func TestExecuteParentCancelPropagates(t *testing.T) {
	r := New(Options{
		Tools:   []port.Tool{&stubTool{spec: tool.Spec{Name: "slow", Risk: tool.Safe}, delay: time.Second}},
		Timeout: 5 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Execute(ctx, c("slow", `{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

// TestTrimOutput 结果按 rune 截断并标注原长；Err 同样裁剪；上限为 0 不裁剪。
func TestTrimOutput(t *testing.T) {
	long := strings.Repeat("あ", 100)
	r := New(Options{Tools: []port.Tool{&stubTool{
		spec: tool.Spec{Name: "t", Risk: tool.Safe}, out: long,
	}}, MaxOutput: 10})
	res, err := r.Execute(context.Background(), c("t", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := []rune(strings.Split(res.Output, "\n")[0]); len(got) != 10 {
		t.Fatalf("trimmed 前段 = %d runes", len(got))
	}
	if !strings.Contains(res.Output, "原文 100 字符") {
		t.Fatalf("缺截断标注: %q", res.Output)
	}

	r2 := New(Options{Tools: []port.Tool{&stubTool{
		spec: tool.Spec{Name: "t", Risk: tool.Safe}, out: long,
	}}})
	res2, _ := r2.Execute(context.Background(), c("t", `{}`))
	if res2.Output != long {
		t.Fatal("maxOut=0 不应裁剪")
	}
}

// TestTrimErrToo Err 文本同样受裁剪保护。
func TestTrimErrToo(t *testing.T) {
	r := New(Options{MaxOutput: 5})
	if got := trim(strings.Repeat("x", 50), r.maxOut); len([]rune(strings.Split(got, "\n")[0])) != 5 {
		t.Fatalf("trim err = %q", got)
	}
}

// TestInSandboxBoundary 前缀判定含边界：sandbox 与 sandbox2 不互相覆盖。
func TestInSandboxBoundary(t *testing.T) {
	r := New(Options{SandboxPath: mustAbs(t, "sbx")})
	if !r.inSandbox(mustAbs(t, "sbx") + "/a.txt") {
		t.Fatal("sandbox 内应命中")
	}
	if r.inSandbox(mustAbs(t, "sbx2") + "/a.txt") {
		t.Fatal("sbx2 不在 sandbox 内")
	}
	if !r.inSandbox(mustAbs(t, "sbx")) {
		t.Fatal("sandbox 自身应命中")
	}
	if r.inSandbox("") {
		t.Fatal("空路径不在 sandbox")
	}
}

// TestInSandboxNonexistentPath 尚不存在的路径按最长存在前缀解析：
// sandbox 内的新文件（file_write 写新文件）必须命中，外部新路径不命中。
func TestInSandboxNonexistentPath(t *testing.T) {
	base := t.TempDir()
	sbx := filepath.Join(base, "sbx")
	if err := os.MkdirAll(sbx, 0o755); err != nil {
		t.Fatalf("mkdir sandbox: %v", err)
	}
	r := New(Options{SandboxPath: sbx})

	if !r.inSandbox(filepath.Join(sbx, "new", "deep", "f.txt")) {
		t.Fatal("sandbox 内尚不存在的深层路径应命中")
	}
	if r.inSandbox(filepath.Join(base, "outside", "new", "f.txt")) {
		t.Fatal("sandbox 外尚不存在的路径不应命中")
	}
}

// TestInSandboxResolvesSymlinks §14 遗留：词法判定不解析符号链接——
// sandbox 内链到外部的链接（逃逸面）不算特权；外部链进 sandbox 的链接落点在特权格内。
// 本机无法创建符号链接（Windows 非开发者模式/无权限）时跳过。
func TestInSandboxResolvesSymlinks(t *testing.T) {
	base := t.TempDir()
	sbx := filepath.Join(base, "sbx")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{sbx, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	escape := filepath.Join(sbx, "escape") // sandbox 内 → 外部
	if err := os.Symlink(outside, escape); err != nil {
		t.Skipf("本机无法创建符号链接: %v", err)
	}
	enter := filepath.Join(base, "enter") // 外部 → sandbox
	if err := os.Symlink(sbx, enter); err != nil {
		t.Skipf("本机无法创建符号链接: %v", err)
	}
	r := New(Options{SandboxPath: sbx})

	if r.inSandbox(filepath.Join(escape, "f.txt")) {
		t.Fatal("sandbox 内链接指向外部，写出落点在外，不应算特权目录")
	}
	if !r.inSandbox(filepath.Join(enter, "f.txt")) {
		t.Fatal("外部链接指向 sandbox，落点在特权目录内应命中")
	}
	if r.inSandbox(filepath.Join(escape, "new", "f.txt")) {
		t.Fatal("经链接逃逸的尚不存在路径不应命中（尾部段拼回后仍在外部）")
	}
}

// mustAbs 绝对化测试路径。
func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}
