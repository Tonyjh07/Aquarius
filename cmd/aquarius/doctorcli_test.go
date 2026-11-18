package main

// doctorcli_test.go `-doctor` 命令行体检（§7.14/D306）：结论与 `/doctor` 共用同一份代码，
// 所以这里只测这条路径独有的东西——不写盘、退出码、profile 解析，以及 MCP 一项换成
// 声明校验之后的三个面。检查项本身的细节在 doctor_test.go。

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// pinLang 把界面语言钉在中文并在用例结束时还原：i18n 是进程级状态，run 会按配置或
// 系统语言改它，不还原就会让同一进程里排在后面的用例跟着变。
func pinLang(t *testing.T) {
	t.Helper()
	before := i18n.Current()
	t.Setenv("AQUARIUS_UI_LANG", string(i18n.LangZh))
	t.Cleanup(func() {
		if before == i18n.LangEn {
			i18n.Use(i18n.SelectionEn)
			return
		}
		i18n.Use(i18n.SelectionZh)
	})
}

// writeCleanProfile 造一个「配置好着、其余都空」的 profile（体检应当全绿）。
func writeCleanProfile(t *testing.T, dir string) string {
	t.Helper()
	profileDir := filepath.Join(dir, "profiles", defaultProfileName)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := `{"model":{"providers":[{"name":"p","models":[{"name":"m"}]}]}}`
	if err := os.WriteFile(filepath.Join(profileDir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return profileDir
}

// TestDoctorModeCleanRun 配置好、其余空：一行通过、退出码 0，并且一个文件都不新增。
func TestDoctorModeCleanRun(t *testing.T) {
	pinLang(t)
	dir := t.TempDir()
	profileDir := writeCleanProfile(t, dir)

	var out, errOut bytes.Buffer
	code := run([]string{"-data", dir, "-doctor"}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("退出码 = %d，stdout = %q，stderr = %q", code, out.String(), errOut.String())
	}
	head := strings.SplitN(i18n.T("session.doctor.clean"), "{", 2)[0]
	if !strings.Contains(out.String(), head) {
		t.Fatalf("全绿应当只回一行：%q", out.String())
	}
	// 只读：会话、日志、导出目录都不该被建出来；根指针也不该被写。
	for _, name := range []string{"conversations", "logs", "exports"} {
		if _, err := os.Stat(filepath.Join(profileDir, name)); !os.IsNotExist(err) {
			t.Fatalf("体检建出了 %s（这条路径承诺不写盘）", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("体检写了根指针（应当只读地按 default 查）")
	}
}

// TestDoctorModeBrokenConfig 配置解析不了：退出码 1，七项全列，配置一项给异常与修法。
func TestDoctorModeBrokenConfig(t *testing.T) {
	pinLang(t)
	dir := t.TempDir()
	profileDir := writeCleanProfile(t, dir)
	if err := os.WriteFile(filepath.Join(profileDir, "config.json"), []byte(`{"model":`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out, errOut bytes.Buffer
	if code := run([]string{"-data", dir, "-doctor"}, nil, &out, &errOut); code != 1 {
		t.Fatalf("有异常项时退出码应当是 1，实际 %d：%q", code, out.String())
	}
	s := out.String()
	if !strings.Contains(s, i18n.T("session.doctor.name.config")) ||
		!strings.Contains(s, i18n.T("session.doctor.state.fail")) {
		t.Fatalf("没报出配置异常：%q", s)
	}
	if !strings.Contains(s, i18n.T("session.doctor.name.mcp")) {
		t.Fatalf("有问题时应当七项全列：%q", s)
	}
}

// TestDoctorModeDoesNotCreateDataDir 数据目录都不存在时也只读：不建目录，报配置异常退出。
func TestDoctorModeDoesNotCreateDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	var out, errOut bytes.Buffer
	if code := run([]string{"-data", dir, "-doctor"}, nil, &out, &errOut); code != 1 {
		t.Fatalf("没有配置时退出码应当是 1，实际 %d：%q", code, out.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("体检把数据目录建出来了")
	}
}

// TestDoctorModeUnknownProfile `--profile` 写错：按启动同一条规则报因，不体检。
func TestDoctorModeUnknownProfile(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	if code := run([]string{"-data", dir, "-profile", "nope", "-doctor"}, nil, &out, &errOut); code != 1 {
		t.Fatalf("profile 不存在时退出码应当是 1，实际 %d", code)
	}
	if !strings.Contains(errOut.String(), "nope") {
		t.Fatalf("没点名写错的 profile：%q", errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("没查出结论就不该有体检输出：%q", out.String())
	}
}

// TestCheckMCPDeclared 命令行模式的 MCP 一项看三面：没有声明 = 正常且说清不连接；
// 声明不合法 = 异常并给修法；配置读不出来 = 注意，指向配置那一项。
func TestCheckMCPDeclared(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	w := &wiring{cfgPath: path}

	if got := checkMCPDeclared(w); got.State != app.HealthWarn {
		t.Fatalf("配置读不出来应当记注意：%+v", got)
	}

	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := checkMCPDeclared(w); got.State != app.HealthOK || got.Detail != i18n.T("cmd.doctor.mcp_cli_none") {
		t.Fatalf("没有声明应当正常：%+v", got)
	}

	good := `{"mcpServers":{"ok":{"transport":"stdio","command":"srv"}}}`
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := checkMCPDeclared(w); got.State != app.HealthOK {
		t.Fatalf("合法声明应当正常：%+v", got)
	}

	bad := `{"mcpServers":{"ok":{"transport":"stdio","command":"srv"},"bad name":{"transport":"stdio","command":"srv"}}}`
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := checkMCPDeclared(w)
	if got.State != app.HealthFail || got.Fix == "" {
		t.Fatalf("非法声明应当异常并给修法：%+v", got)
	}
}

// TestDoctorExitCode 退出码只认异常：注意项说的是「还能用」，不该让定时体检天天报警。
func TestDoctorExitCode(t *testing.T) {
	cases := []struct {
		name   string
		checks []app.HealthCheck
		want   int
	}{
		{"all-ok", []app.HealthCheck{{State: app.HealthOK}}, 0},
		{"warn-only", []app.HealthCheck{{State: app.HealthOK}, {State: app.HealthWarn}}, 0},
		{"has-fail", []app.HealthCheck{{State: app.HealthWarn}, {State: app.HealthFail}}, 1},
	}
	for _, c := range cases {
		if got := doctorExitCode(c.checks); got != c.want {
			t.Fatalf("%s: 退出码 = %d, want %d", c.name, got, c.want)
		}
	}
}
