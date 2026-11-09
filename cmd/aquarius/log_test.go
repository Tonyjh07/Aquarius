package main

// log_test.go 运行日志的开关与落点（D276/§7.9）：级别 `-log-level` > `AQUARIUS_LOG_LEVEL` >
// `ui.log_level`，形态只认 `--log-format`，全量落 profile 自己的 logs/。日志包本身的渲染、
// 轮转与降级见 internal/logging。

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/logging"
)

// TestLogLevelSetting 三层覆盖顺序：flag 压过环境变量，环境变量压过配置；全空交给解析取 info。
func TestLogLevelSetting(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, flagVal, env, cfg, want string
	}{
		{name: "flag 最高", flagVal: "debug", env: "warn", cfg: "error", want: "debug"},
		{name: "flag 去空白", flagVal: " debug ", env: "warn", cfg: "error", want: "debug"},
		{name: "环境变量次之", flagVal: "", env: "warn", cfg: "error", want: "warn"},
		{name: "配置兜底", flagVal: "  ", env: "", cfg: "error", want: "error"},
		{name: "全空用缺省", flagVal: "", env: " ", cfg: "", want: ""},
	}
	for _, c := range cases {
		if got := logLevelSetting(c.flagVal, c.env, c.cfg); got != c.want {
			t.Errorf("%s: logLevelSetting(%q, %q, %q) = %q, want %q",
				c.name, c.flagVal, c.env, c.cfg, got, c.want)
		}
	}
}

// TestOpenLogWritesUnderProfile 落点随 profile 隔离：profiles/<p>/logs/ 下按天一个文件，
// 级别生效（配置 debug 时 debug 行进得去）。
func TestOpenLogWritesUnderProfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var errBuf bytes.Buffer
	w := &wiring{stderr: &errBuf, profileDir: dir, cfg: &fileConfig{}}
	w.cfg.UI.LogLevel = "debug"
	if err := w.openLog("", ""); err != nil {
		t.Fatalf("openLog: %v", err)
	}
	defer func() { _ = w.log.Close() }()

	w.log.Debug(logging.Boot, "log.act.skip", "")

	entries, err := os.ReadDir(filepath.Join(dir, "logs"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("没落 profile 的 logs/：%v（%v）", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "logs", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "DEBUG") || !strings.Contains(string(data), "boot") {
		t.Fatalf("debug 行没写进文件：%q", string(data))
	}
	if errBuf.Len() != 0 {
		t.Fatalf("debug 行不该上 stderr：%q", errBuf.String())
	}
}

// TestOpenLogFlagOverridesConfig flag 改级别时配置里的值不生效（排查时不必先改配置）。
func TestOpenLogFlagOverridesConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &wiring{stderr: &bytes.Buffer{}, profileDir: dir, cfg: &fileConfig{}}
	w.cfg.UI.LogLevel = "error"
	if err := w.openLog("debug", "json"); err != nil {
		t.Fatalf("openLog: %v", err)
	}
	defer func() { _ = w.log.Close() }()

	w.log.Debug(logging.Boot, "log.act.skip", "")

	entries, _ := os.ReadDir(filepath.Join(dir, "logs"))
	if len(entries) == 0 {
		t.Fatal("没落文件")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "logs", entries[0].Name()))
	if !strings.Contains(string(data), `"level":"debug"`) {
		t.Fatalf("flag 没压过配置，或 JSON 形态没生效：%q", string(data))
	}
}

// TestOpenLogEchoesForTerminalGUI 终端回显（D288）：gui 从终端启动时 info 也打到 stdout；
// repl 的终端就是界面，不许回显。
func TestOpenLogEchoesForTerminalGUI(t *testing.T) {
	if !consoleVisible() {
		t.Skip("本机没有可见控制台（双击启动口径），回显按设计关闭")
	}
	cases := []struct {
		kind  string
		echo  bool
		title string
	}{
		{kind: "gui", echo: true, title: "gui 回显"},
		{kind: "repl", echo: false, title: "repl 不回显"},
	}
	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			dir := t.TempDir()
			var out, errBuf bytes.Buffer
			w := &wiring{stdout: &out, stderr: &errBuf, profileDir: dir, cfg: &fileConfig{}}
			w.cfg.UI.Kind = c.kind
			if err := w.openLog("", ""); err != nil {
				t.Fatalf("openLog: %v", err)
			}
			defer func() { _ = w.log.Close() }()

			w.log.Info(logging.Boot, "log.act.skip", "")
			got := out.String()
			if c.echo && !strings.Contains(got, "INFO") {
				t.Fatalf("gui 的 info 应回显到终端：%q", got)
			}
			if !c.echo && got != "" {
				t.Fatalf("%s 不该回显：%q", c.kind, got)
			}
		})
	}
}

// TestLogSettingRejectsUnknown 级别写错启动即报：静默回落等于用户以为在看 warn 以上的日志。
func TestLogSettingRejectsUnknown(t *testing.T) {
	dir := t.TempDir()
	writeProfileLayout(t, dir, `{
  "model": {"providers":[{"name":"openai-compatible","base_url":"http://127.0.0.1:1","keys":[{"api_key":"secret:AQ_E2E_KEY"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui": {"kind":"repl", "log_level":"wran"},
  "limits": {"max_turns": 1, "max_context_tokens": 64000, "tool_output_chars": 2000, "tool_timeout_sec": 10}
}`)
	t.Setenv("AQ_E2E_KEY", "test-key")
	t.Setenv("AQUARIUS_LOG_LEVEL", "") // 让配置里的非法值真被读到

	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1（stderr = %s）", code, errBuf.String())
	}
	if got := errBuf.String(); !strings.Contains(got, "wran") {
		t.Fatalf("stderr 应指出非法级别：%q", got)
	}
}

// testWiringLog 造一个只写内存的 wiring：日志 warn 以上同时进返回的缓冲区（断言不用起进程）。
// 级别取 debug，文件关掉（Dir 空）——测试只关心「有没有按五段打出来」。
func testWiringLog(t *testing.T) (*wiring, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	w := &wiring{stderr: &buf}
	w.log = logging.Open(logging.Options{Stderr: &buf, Level: logging.LevelDebug})
	t.Cleanup(func() { _ = w.log.Close() })
	return w, &buf
}
