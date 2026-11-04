package main

// lang_test.go 界面语言的选择与生效（D275/§7.8）：`AQUARIUS_UI_LANG` 环境变量 > `ui.lang`
// > auto，非法值启动即报。取词口本身的语义见 internal/i18n。

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// TestUiLangSetting 覆盖顺序：环境变量非空（去空白后）压过配置；空则回落到配置原样
// （空串 = auto，交给 i18n.Parse 解释）。
func TestUiLangSetting(t *testing.T) {
	cases := []struct {
		name, env, cfg, want string
	}{
		{name: "环境变量压过配置", env: "en", cfg: "zh", want: "en"},
		{name: "环境变量去空白", env: "  en  ", cfg: "zh", want: "en"},
		{name: "环境变量空用配置", env: "", cfg: "en", want: "en"},
		{name: "两边都空", env: "   ", cfg: "", want: ""},
	}
	for _, c := range cases {
		if got := uiLangSetting(c.env, c.cfg); got != c.want {
			t.Errorf("%s: uiLangSetting(%q, %q) = %q, want %q", c.name, c.env, c.cfg, got, c.want)
		}
	}
}

// TestUILangEnvSelectsEnglish 首跑（还没有配置文件，也没有 ui.lang）时，界面语言由环境
// 变量定：这是「测试不依赖本机语言」的唯一把手，也是便携安装临时换语言的入口。
func TestUILangEnvSelectsEnglish(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEEPSEEK_API_KEY", "") // 模板密钥取不到 ⇒ 配置不完整 ⇒ 走向导窗
	t.Setenv("AQUARIUS_UI_LANG", "en")
	defer swapSetupWindowRunner(func(uigui.SetupRequest) (uigui.SetupOutcome, error) {
		return uigui.SetupCancelled, nil // 取消：不写盘，退出 0
	})()

	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	if got := out.String(); !strings.Contains(got, "Wrote config") {
		t.Fatalf("ui.lang=en 下首跑回执应为英文: %q", got)
	}
	// 中文泄漏金丝雀：临时切到 zh 表取「已生成配置」回执的原文（到第一个占位符为止），
	// 断言 en 输出里不出现——路径等动态值不在此钉。
	prev := i18n.Current()
	i18n.Use(i18n.SelectionZh)
	zhWritten := i18n.T("boot.config_written")
	i18n.Use(i18n.Selection(prev))
	if zhPrefix, _, _ := strings.Cut(zhWritten, "{"); strings.Contains(out.String(), zhPrefix) {
		t.Fatalf("ui.lang=en 下仍出现中文: %q", out.String())
	}
}

// TestUILangRejectsUnknown 非法 ui.lang 启动即报（与 ui.kind 同口径），并且要指出是哪个键
// ——否则用户只看到「启动失败」，不知道该改哪一处。
func TestUILangRejectsUnknown(t *testing.T) {
	dir := t.TempDir()
	writeProfileLayout(t, dir, `{
  "model": {"providers":[{"name":"openai-compatible","base_url":"http://127.0.0.1:1","keys":[{"api_key":"secret:AQ_E2E_KEY"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui": {"kind":"repl", "lang":"fr"},
  "limits": {"max_turns": 1, "max_context_tokens": 64000, "tool_output_chars": 2000, "tool_timeout_sec": 10}
}`)
	t.Setenv("AQ_E2E_KEY", "test-key")
	t.Setenv("AQUARIUS_UI_LANG", "") // 让配置里的非法值真被读到

	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1（stderr = %s）", code, errBuf.String())
	}
	if got := errBuf.String(); !strings.Contains(got, "ui.lang") || !strings.Contains(got, "fr") {
		t.Fatalf("stderr 应指出 ui.lang=fr 非法: %q", got)
	}
}
