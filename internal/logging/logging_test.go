package logging

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// at 固定时刻（带 +08:00），时间列断言用它——本地时区在不同机器上不同，测试不能依赖。
func at() time.Time {
	return time.Date(2026, 11, 2, 18, 20, 31, 482*int(time.Millisecond), time.FixedZone("CST", 8*3600))
}

// newTestLogger 建一个落临时目录、stderr 收进缓冲的日志（格式、级别按参数）。
func newTestLogger(t *testing.T, lv Level, f Format) (*Logger, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	var stderr bytes.Buffer
	l := Open(Options{Dir: dir, Level: lv, Format: f, Stderr: &stderr, Now: at})
	t.Cleanup(func() { _ = l.Close() })
	return l, &stderr, dir
}

// logFile 读某个日期的落盘内容（不存在则空串）。
func logFile(t *testing.T, dir, day string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filePrefix+day+".log"))
	if err != nil {
		return ""
	}
	return string(data)
}

// TestRenderText 五段的文本形态：时间 / 级别 / 上下文 / 事件 / 行动，外加 kv 尾巴。
func TestRenderText(t *testing.T) {
	l, stderr, _ := newTestLogger(t, LevelDebug, FormatText)
	l.Warn(LLM, "log.llm.key_unavailable", "log.act.skip_key_this_run",
		"provider", "openai", "n", 2, "err", "401")

	got := stderr.String()
	if !strings.HasPrefix(got, "2026-11-02 18:20:31.482 WARN  llm      ") {
		t.Fatalf("前缀不对：%q", got)
	}
	// 整行按生产同样的组成方式从文案表拼：事件描述 + 行动位 + kv 尾巴。
	want := "2026-11-02 18:20:31.482 WARN  llm      " +
		i18n.Tf("log.llm.key_unavailable", "provider", "openai", "n", 2, "err", "401") +
		"  → " + i18n.T("log.act.skip_key_this_run") +
		"  provider=openai n=2 err=401\n"
	if got != want {
		t.Fatalf("整行不匹配：got %q, want %q", got, want)
	}
}

// TestContextColumnWide 上下文列定宽 8：最长的标识（delegate）不与后面粘在一起。
func TestContextColumnWide(t *testing.T) {
	l, stderr, _ := newTestLogger(t, LevelDebug, FormatText)
	l.Warn(Delegate, "log.act.skip", "")
	line := stderr.String()
	if !strings.Contains(line, "WARN  delegate ") {
		t.Fatalf("上下文列未按 8 宽对齐：%q", line)
	}
}

// TestTextWithoutAction 没有行动位时不印箭头——空箭头比缺一段更让人以为日志坏了。
func TestTextWithoutAction(t *testing.T) {
	l, stderr, _ := newTestLogger(t, LevelDebug, FormatText)
	l.Info(Boot, "log.act.skip", "")
	if strings.Contains(stderr.String(), "→") {
		t.Fatalf("空行动不该出箭头：%q", stderr.String())
	}
}

// TestKVQuoting 值里有空白要圈起来，否则会被读成下一个字段。
func TestKVQuoting(t *testing.T) {
	l, stderr, _ := newTestLogger(t, LevelDebug, FormatText)
	l.Warn(Boot, "log.boot.file_unavailable", "log.act.stderr_only",
		"path", "C:\\a b\\logs", "err", "permission denied")
	got := stderr.String()
	if !strings.Contains(got, "path=«C:\\a b\\logs»") || !strings.Contains(got, "err=«permission denied»") {
		t.Fatalf("含空白的值未加圈：%q", got)
	}
}

// TestJSONShapeAndKeys JSON 形态：ctx/event/action 是 key，msg/act 是译文。
func TestJSONShapeAndKeys(t *testing.T) {
	defer i18n.Use(i18n.SelectionZh)
	i18n.Use(i18n.SelectionZh)

	l, stderr, _ := newTestLogger(t, LevelDebug, FormatJSON)
	l.Warn(LLM, "log.llm.param_rejected", "log.act.recorded_unsupported",
		"field", "top_p", "provider", "deepseek")

	var rec map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &rec); err != nil {
		t.Fatalf("不是一行 JSON：%v（%q）", err, stderr.String())
	}
	if rec["ctx"] != "llm" || rec["event"] != "log.llm.param_rejected" ||
		rec["action"] != "log.act.recorded_unsupported" {
		t.Fatalf("key 字段不对：%v", rec)
	}
	if rec["level"] != "warn" || rec["ts"] != "2026-11-02T18:20:31.482+08:00" {
		t.Fatalf("级别或时间不对：%v", rec)
	}
	if rec["msg"] != i18n.Tf("log.llm.param_rejected", "field", "top_p") {
		t.Fatalf("msg 不是译文：%v", rec["msg"])
	}
	kv, ok := rec["kv"].(map[string]any)
	if !ok || kv["field"] != "top_p" || kv["provider"] != "deepseek" {
		t.Fatalf("kv 不对：%v", rec["kv"])
	}
}

// TestJSONKeysStableAcrossLanguages 切语言只改 msg/act，不改 key——贴日志排障时 key 得读得懂。
func TestJSONKeysStableAcrossLanguages(t *testing.T) {
	defer i18n.Use(i18n.SelectionZh)

	capture := func(sel i18n.Selection) map[string]any {
		i18n.Use(sel)
		l, stderr, _ := newTestLogger(t, LevelDebug, FormatJSON)
		l.Warn(Boot, "log.boot.persona_missing", "log.act.skip_missing", "modules", "a,b")
		var rec map[string]any
		if err := json.Unmarshal(stderr.Bytes(), &rec); err != nil {
			t.Fatalf("不是一行 JSON：%v", err)
		}
		return rec
	}
	zh, en := capture(i18n.SelectionZh), capture(i18n.SelectionEn)
	for _, k := range []string{"ctx", "event", "action", "level"} {
		if zh[k] != en[k] {
			t.Errorf("%s 随语言变了：%v vs %v", k, zh[k], en[k])
		}
	}
	if zh["msg"] == en["msg"] || zh["act"] == en["act"] {
		t.Fatalf("译文没跟着语言走：zh=%v en=%v", zh["msg"], en["msg"])
	}
	if !strings.Contains(en["msg"].(string), "persona module tree") {
		t.Fatalf("en 的 msg 不是英文：%v", en["msg"])
	}
}

// TestLevelFilter 文件按级别过；stderr 固定 warn 以上，与 --log-level 无关。
func TestLevelFilter(t *testing.T) {
	l, stderr, dir := newTestLogger(t, LevelWarn, FormatText)
	l.Debug(Session, "log.act.skip", "")
	l.Info(Session, "log.act.skip", "")
	l.Warn(Session, "log.act.skip", "")
	l.Error(Session, "log.act.skip", "")

	file := logFile(t, dir, "2026-11-02")
	if strings.Count(file, "\n") != 2 {
		t.Fatalf("文件应只有 2 行（warn/error）：%q", file)
	}
	if strings.Contains(file, "DEBUG") || strings.Contains(file, "INFO") {
		t.Fatalf("低于门槛的行进了文件：%q", file)
	}
	if strings.Count(stderr.String(), "\n") != 2 {
		t.Fatalf("stderr 应只有 2 行：%q", stderr.String())
	}
}

// TestDebugLevelWritesFile stderr 不跟着放开：debug 只进文件。
func TestDebugLevelWritesFile(t *testing.T) {
	l, stderr, dir := newTestLogger(t, LevelDebug, FormatText)
	l.Debug(Attach, "log.act.skip", "")

	if !strings.Contains(logFile(t, dir, "2026-11-02"), "DEBUG") {
		t.Fatal("debug 行没进文件")
	}
	if stderr.Len() != 0 {
		t.Fatalf("debug 行不该上 stderr：%q", stderr.String())
	}
}

// TestDailyCut 跨零点换文件。
func TestDailyCut(t *testing.T) {
	dir := t.TempDir()
	now := at()
	var mu sync.Mutex
	l := Open(Options{Dir: dir, Level: LevelInfo, Now: func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}})
	defer func() { _ = l.Close() }()

	l.Info(Boot, "log.act.skip", "")
	mu.Lock()
	now = now.Add(24 * time.Hour)
	mu.Unlock()
	l.Info(Boot, "log.act.skip", "")

	for _, day := range []string{"2026-11-02", "2026-11-03"} {
		if strings.Count(logFile(t, dir, day), "\n") != 1 {
			t.Errorf("%s 应有 1 行，实际 %q", day, logFile(t, dir, day))
		}
	}
}

// TestRotate 超上限轮转一代到 .1，且不丢写。
func TestRotate(t *testing.T) {
	dir := t.TempDir()
	l := Open(Options{Dir: dir, Level: LevelInfo, Now: at, CapBytes: 300})
	defer func() { _ = l.Close() }()
	for i := 0; i < 20; i++ {
		l.Info(Boot, "log.act.skip", "")
	}
	day := "2026-11-02"
	cur := logFile(t, dir, day)
	backup, err := os.ReadFile(filepath.Join(dir, filePrefix+day+".log.1"))
	if err != nil {
		t.Fatalf("没有轮转出 .1：%v", err)
	}
	if len(cur) == 0 || len(backup) == 0 {
		t.Fatalf("轮转两侧都该有内容：cur=%d backup=%d", len(cur), len(backup))
	}
	// 只保留一代：写到第三轮之后，早先的行按设计就没了。这里查的是「没把行写坏」。
	for _, line := range strings.Split(strings.TrimSuffix(string(backup), "\n"), "\n") {
		if !strings.HasPrefix(line, "2026-11-02 18:20:31.482 INFO  boot     ") {
			t.Fatalf("轮转后的行不完整：%q", line)
		}
	}
}

// TestPurgeOld 超期文件（含轮转出来的 .1）在打开时清掉，未超期的留着。
func TestPurgeOld(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, filePrefix+"2026-01-01.log")
	oldRotated := filepath.Join(dir, filePrefix+"2026-01-01.log.1")
	fresh := filepath.Join(dir, filePrefix+"2026-11-01.log")
	for _, p := range []string{old, oldRotated, fresh} {
		if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stale := at().Add(-30 * 24 * time.Hour)
	for _, p := range []string{old, oldRotated} {
		if err := os.Chtimes(p, stale, stale); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{fresh, filepath.Join(dir, "README.md")} {
		if err := os.WriteFile(p, []byte("keep\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	l := Open(Options{Dir: dir, Level: LevelInfo, Now: at})
	defer func() { _ = l.Close() }()

	for _, p := range []string{old, oldRotated} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("超期文件没清掉：%s", p)
		}
	}
	for _, p := range []string{fresh, filepath.Join(dir, "README.md")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("不该删的文件被删了：%s", p)
		}
	}
}

// TestDegradeWhenDirUnopenable 目录开不出来时退化成只写 stderr，并记一条 warn。
func TestDegradeWhenDirUnopenable(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	l := Open(Options{Dir: blocker, Level: LevelDebug, Stderr: &stderr, Now: at})
	defer func() { _ = l.Close() }()

	// 报因整句出自文案表模板；{err} 是平台相关的系统错误文本，锁到 err 之前的整段。
	wantDegrade := i18n.Tf("log.boot.file_unavailable", "path", blocker, "err", "")
	if !strings.Contains(stderr.String(), wantDegrade) {
		t.Fatalf("没报降级：%q", stderr.String())
	}
	stderr.Reset()
	l.Warn(Boot, "log.act.skip", "")
	if !strings.Contains(stderr.String(), "WARN") {
		t.Fatalf("降级后 warn 仍应上 stderr：%q", stderr.String())
	}
}

// TestConcurrent 多 goroutine 写同一份文件：不交错、不丢行（-race 也要干净）。
func TestConcurrent(t *testing.T) {
	l, _, dir := newTestLogger(t, LevelInfo, FormatText)
	const n, per = 8, 25
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				l.Info(Job, "log.act.skip", "")
			}
		}()
	}
	wg.Wait()

	file := logFile(t, dir, "2026-11-02")
	if got := strings.Count(file, "\n"); got != n*per {
		t.Fatalf("行数 = %d, want %d", got, n*per)
	}
	for _, line := range strings.Split(strings.TrimSuffix(file, "\n"), "\n") {
		if !strings.HasPrefix(line, "2026-11-02 18:20:31.482 INFO  job      ") {
			t.Fatalf("行被写坏：%q", line)
		}
	}
}

// TestParseLevel 非法值报错（不静默回落）：用户写了 wran，得让他知道没生效。
func TestParseLevel(t *testing.T) {
	cases := map[string]Level{"": LevelInfo, "debug": LevelDebug, "INFO": LevelInfo,
		"warn": LevelWarn, "error": LevelError, " WARN ": LevelWarn}
	for raw, want := range cases {
		got, err := ParseLevel(raw)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v；want %v", raw, got, err, want)
		}
	}
	if _, err := ParseLevel("wran"); err == nil {
		t.Error("非法级别没报错")
	}
}

// TestParseFormat 形态同理。
func TestParseFormat(t *testing.T) {
	if f, err := ParseFormat(""); err != nil || f != FormatText {
		t.Errorf("空串应得 text：%v, %v", f, err)
	}
	if f, err := ParseFormat("json"); err != nil || f != FormatJSON {
		t.Errorf("json 没认出来：%v, %v", f, err)
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("非法形态没报错")
	}
}

// TestNilLoggerSafe 零值句柄不炸（装配失败路径上仍可能有人调它）。
func TestNilLoggerSafe(t *testing.T) {
	var l *Logger
	l.Info(Boot, "log.act.skip", "")
	if err := l.Close(); err != nil {
		t.Fatalf("nil 句柄 Close 应返回 nil：%v", err)
	}
}

// TestNoFileOnlyStderr Dir 为空时只写 stderr。
func TestNoFileOnlyStderr(t *testing.T) {
	var stderr bytes.Buffer
	l := Open(Options{Level: LevelDebug, Stderr: &stderr, Now: at})
	defer func() { _ = l.Close() }()
	l.Warn(Boot, "log.act.skip", "")
	if !strings.Contains(stderr.String(), "WARN") {
		t.Fatalf("空 Dir 不该影响 stderr：%q", stderr.String())
	}
}
