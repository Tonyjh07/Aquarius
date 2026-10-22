package main

// 设置窗「联网」页装配根侧测试（D200）：web 段写读往返、密钥只写不回显、保存前校验
// 与启动期 normalizeWeb 同口径。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// webCfg 一份带两个对话 provider 的配置（deepseek 供联网借用，mimo 备用）。
const webCfg = `{
  "model": {
    "providers": [
      {"name": "deepseek", "base_url": "https://api.deepseek.com", "models": [{"name": "deepseek-chat"}]},
      {"name": "mimo", "base_url": "https://api.mimo.ai/v1", "models": [{"name": "mimo-flash"}]}
    ],
    "primary": "deepseek",
    "name": "deepseek-chat"
  },
  "web": {
    "search": {"providers": [{"kind": "brave", "key": "secret:AQUARIUS_BRAVE_API_KEY", "country": "CN"}], "count": 8},
    "fetch": {"providers": [{"kind": "local"}], "max_chars": 16000, "proxy": "http://127.0.0.1:7897"}
  }
}`

func writeWebCfg(t *testing.T) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(webCfg), 0o644); err != nil {
		t.Fatalf("写配置: %v", err)
	}
	return cfgPath
}

// TestReadTextSettingsWeb 读侧：两组提供商按序进快照，密钥只给形态（引用名/明文标记，
// 永不含值）；respect_robots 键缺失 = 遵守。
func TestReadTextSettingsWeb(t *testing.T) {
	s := readTextSettings(writeWebCfg(t))
	if len(s.WebSearchProviders) != 1 || s.WebSearchProviders[0].Kind != "brave" {
		t.Fatalf("搜索提供商 = %+v", s.WebSearchProviders)
	}
	if !s.WebSearchProviders[0].KeySaved || s.WebSearchProviders[0].KeyRef != "secret:AQUARIUS_BRAVE_API_KEY" {
		t.Fatalf("密钥形态 = %+v（应只带引用名）", s.WebSearchProviders[0])
	}
	if s.WebSearchProviders[0].KeyPlain {
		t.Fatal("引用不该被标成明文")
	}
	if s.WebSearchProviders[0].Country != "CN" || s.WebSearchCount != 8 {
		t.Fatalf("国家/条数 = %q/%d", s.WebSearchProviders[0].Country, s.WebSearchCount)
	}
	if len(s.WebFetchProviders) != 1 || s.WebFetchProviders[0].Kind != "local" {
		t.Fatalf("抓取提供商 = %+v", s.WebFetchProviders)
	}
	if s.WebFetchMaxChars != 16000 || s.WebFetchProxy != "http://127.0.0.1:7897" {
		t.Fatalf("抓取参数 = %d/%q", s.WebFetchMaxChars, s.WebFetchProxy)
	}
	if !s.WebFetchRobots {
		t.Fatal("respect_robots 键缺失应读成「遵守」")
	}
	if s.WebFetchPrivate {
		t.Fatal("allow_private_ips 键缺失应读成 false")
	}
	// 明文密钥只标形态，快照里不带值。
	plain := filepath.Join(t.TempDir(), "config.json")
	raw := strings.Replace(webCfg, `"secret:AQUARIUS_BRAVE_API_KEY"`, `"sk-plain-1234"`, 1)
	if err := os.WriteFile(plain, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	ps := readTextSettings(plain)
	if !ps.WebSearchProviders[0].KeyPlain || ps.WebSearchProviders[0].KeySaved {
		t.Fatalf("明文形态 = %+v", ps.WebSearchProviders[0])
	}
	if ps.WebSearchProviders[0].KeyRef != "" {
		t.Fatalf("明文不该带引用名: %q", ps.WebSearchProviders[0].KeyRef)
	}
}

// TestPersistWebSettingsRoundTrip 写读往返：列表顺序、借用的服务、国家语言、条数、
// 本地抓取参数与布尔都按表单落盘；`key` 留空 = 沿用原引用（值不回显也不丢）。
func TestPersistWebSettingsRoundTrip(t *testing.T) {
	cfgPath := writeWebCfg(t)
	p := uigui.SettingsPatch{
		Theme: "system",
		WebSearchProviders: []uigui.WebProviderPatch{
			{Kind: "deepseek", From: "mimo", Count: 5},
			{Kind: "brave", Country: "US", Lang: "en"}, // Key 留空 = 保持原引用
		},
		WebSearchCount:     12,
		WebFetchProviders:  []uigui.WebProviderPatch{{Kind: "deepseek", From: "deepseek"}, {Kind: "local"}},
		WebFetchMaxChars:   9000,
		WebFetchProxy:      "",
		WebFetchUserAgent:  "Aquarius/2.0",
		WebFetchRobots:     false,
		WebFetchPrivate:    true,
		WebFetchTimeoutSec: 45,
	}
	if err := persistSettingsTextKeys(cfgPath, p); err != nil {
		t.Fatalf("persistSettingsTextKeys: %v", err)
	}
	s := readTextSettings(cfgPath)
	if len(s.WebSearchProviders) != 2 {
		t.Fatalf("搜索提供商条数 = %d, want 2", len(s.WebSearchProviders))
	}
	if got := s.WebSearchProviders[0]; got.Kind != "deepseek" || got.From != "mimo" || got.Count != 5 {
		t.Fatalf("搜索第 1 条 = %+v", got)
	}
	if got := s.WebSearchProviders[1]; got.Kind != "brave" || got.Country != "US" || got.Lang != "en" {
		t.Fatalf("搜索第 2 条 = %+v", got)
	}
	// 密钥留空 = 沿用原引用（不是清空、也不是丢成明文）。
	if !s.WebSearchProviders[1].KeySaved || s.WebSearchProviders[1].KeyRef != "secret:AQUARIUS_BRAVE_API_KEY" {
		t.Fatalf("留空应保持原引用: %+v", s.WebSearchProviders[1])
	}
	if s.WebSearchCount != 12 {
		t.Fatalf("搜索条数 = %d, want 12", s.WebSearchCount)
	}
	if len(s.WebFetchProviders) != 2 || s.WebFetchProviders[0].Kind != "deepseek" || s.WebFetchProviders[1].Kind != "local" {
		t.Fatalf("抓取提供商 = %+v", s.WebFetchProviders)
	}
	if s.WebFetchMaxChars != 9000 || s.WebFetchTimeoutSec != 45 {
		t.Fatalf("抓取数值 = %d/%d", s.WebFetchMaxChars, s.WebFetchTimeoutSec)
	}
	if s.WebFetchProxy != "" || s.WebFetchUserAgent != "Aquarius/2.0" {
		t.Fatalf("代理/UA = %q/%q（代理清空应落空值）", s.WebFetchProxy, s.WebFetchUserAgent)
	}
	if s.WebFetchRobots || !s.WebFetchPrivate {
		t.Fatalf("开关 = robots:%v private:%v", s.WebFetchRobots, s.WebFetchPrivate)
	}
	// 写出来的配置必须能过启动校验（否则保存等于把下次启动搞挂）。
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("读配置: %v", err)
	}
	if err := cfg.normalizeWeb(); err != nil {
		t.Fatalf("写回的配置过不了启动校验: %v", err)
	}
}

// TestPersistWebKeepsUnknownKeys 列表项的未知键按同类型的旧条目保留（Q10 口径：用户手写
// 的字段不因一次保存而丢）。
func TestPersistWebKeepsUnknownKeys(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	raw := strings.Replace(webCfg, `{"kind": "local"}`,
		`{"kind": "local", "custom_flag": "keep-me"}`, 1)
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{
		Theme:              "system",
		WebSearchProviders: []uigui.WebProviderPatch{{Kind: "brave"}},
		WebFetchProviders:  []uigui.WebProviderPatch{{Kind: "local"}},
		WebFetchRobots:     true,
	}); err != nil {
		t.Fatalf("persistSettingsTextKeys: %v", err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"custom_flag": "keep-me"`) {
		t.Fatalf("列表项未知键被抹掉:\n%s", data)
	}
}

// TestPersistWebNarrowWriteLeavesWebAlone 窄写回（人格模板等）patch 里没有联网列表
// （nil）时不动 web 段——否则一次模板保存就把用户的联网配置抹平。
func TestPersistWebNarrowWriteLeavesWebAlone(t *testing.T) {
	cfgPath := writeWebCfg(t)
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{Theme: "dark"}); err != nil {
		t.Fatalf("persistSettingsTextKeys: %v", err)
	}
	s := readTextSettings(cfgPath)
	if len(s.WebSearchProviders) != 1 || len(s.WebFetchProviders) != 1 {
		t.Fatalf("窄写回不该动联网配置: %+v / %+v", s.WebSearchProviders, s.WebFetchProviders)
	}
	if s.WebFetchProxy != "http://127.0.0.1:7897" {
		t.Fatalf("代理被清掉: %q", s.WebFetchProxy)
	}
}

// TestPersistWebPartialWriteTouchesOneSide 只给一侧列表的 patch 只写那一侧：搜索侧的
// 保存不该把抓取的代理与开关按零值覆盖。
func TestPersistWebPartialWriteTouchesOneSide(t *testing.T) {
	cfgPath := writeWebCfg(t)
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{
		Theme:              "system",
		WebSearchProviders: []uigui.WebProviderPatch{{Kind: "brave"}},
	}); err != nil {
		t.Fatalf("persistSettingsTextKeys: %v", err)
	}
	s := readTextSettings(cfgPath)
	if s.WebFetchProxy != "http://127.0.0.1:7897" || s.WebFetchMaxChars != 16000 {
		t.Fatalf("抓取参数被搜索侧写回覆盖: %+v", s.WebFetchProxy)
	}
	if len(s.WebFetchProviders) != 1 || s.WebFetchProviders[0].Kind != "local" {
		t.Fatalf("抓取提供商被搜索侧写回覆盖: %+v", s.WebFetchProviders)
	}
}

// TestPersistWebEmptyListClears 空列表 = 清空（该能力不可用），与降级链同口径。
func TestPersistWebEmptyListClears(t *testing.T) {
	cfgPath := writeWebCfg(t)
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{
		Theme:              "system",
		WebSearchProviders: []uigui.WebProviderPatch{},
		WebFetchProviders:  []uigui.WebProviderPatch{},
	}); err != nil {
		t.Fatalf("persistSettingsTextKeys: %v", err)
	}
	s := readTextSettings(cfgPath)
	if len(s.WebSearchProviders) != 0 || len(s.WebFetchProviders) != 0 {
		t.Fatalf("空列表应清空: %+v / %+v", s.WebSearchProviders, s.WebFetchProviders)
	}
	if err := loadConfig2Normalize(cfgPath); err != nil {
		t.Fatalf("清空后应仍可启动: %v", err)
	}
}

// loadConfig2Normalize 读回配置并跑一次联网段校验（辅助：写坏的配置要在这里响）。
func loadConfig2Normalize(cfgPath string) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	return cfg.normalizeWeb()
}

// TestValidateWebSettings 保存前校验与启动期同口径：未知 kind、抓取侧不支持的类型、
// 借用的 provider 不存在、数值越界都拦下且原文件不动。
func TestValidateWebSettings(t *testing.T) {
	cfgPath := writeWebCfg(t)
	base := func() uigui.SettingsPatch {
		return uigui.SettingsPatch{
			Theme:              "system",
			WebSearchProviders: []uigui.WebProviderPatch{{Kind: "deepseek"}},
			WebFetchProviders:  []uigui.WebProviderPatch{{Kind: "local"}},
		}
	}
	cases := []struct {
		name string
		mut  func(*uigui.SettingsPatch)
		want string
	}{
		{"未知搜索类型", func(p *uigui.SettingsPatch) {
			p.WebSearchProviders = []uigui.WebProviderPatch{{Kind: "google"}}
		}, "不认识"},
		{"抓取侧写了 brave", func(p *uigui.SettingsPatch) {
			p.WebFetchProviders = []uigui.WebProviderPatch{{Kind: "brave"}}
		}, "不认识"},
		{"搜索借用失联服务", func(p *uigui.SettingsPatch) {
			p.WebSearchProviders = []uigui.WebProviderPatch{{Kind: "deepseek", From: "nope"}}
		}, "不在 provider 列表内"},
		{"抓取借用失联服务", func(p *uigui.SettingsPatch) {
			p.WebFetchProviders = []uigui.WebProviderPatch{{Kind: "deepseek", From: "nope"}}
		}, "不在 provider 列表内"},
		{"单条条数越界", func(p *uigui.SettingsPatch) {
			p.WebSearchProviders = []uigui.WebProviderPatch{{Kind: "brave", Count: 21}}
		}, "超出范围"},
		{"缺省条数越界", func(p *uigui.SettingsPatch) { p.WebSearchCount = 21 }, "超出范围"},
		{"正文上限越界", func(p *uigui.SettingsPatch) { p.WebFetchMaxChars = 100 }, "超出范围"},
		{"超时越界", func(p *uigui.SettingsPatch) { p.WebFetchTimeoutSec = 601 }, "超出范围"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before, _ := os.ReadFile(cfgPath)
			p := base()
			c.mut(&p)
			err := persistSettingsTextKeys(cfgPath, p)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want 含 %q", err, c.want)
			}
			after, _ := os.ReadFile(cfgPath)
			if string(before) != string(after) {
				t.Error("校验失败后原文件应保持不变")
			}
		})
	}
	// 正常组合应通过。
	if err := persistSettingsTextKeys(cfgPath, base()); err != nil {
		t.Fatalf("正常组合应通过: %v", err)
	}
}
