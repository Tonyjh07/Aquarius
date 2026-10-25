package main

// sampling_settings_test.go 设置窗采样参数的装配根侧（D204）：表单文本化与解析、保存前
// 校验（落盘配置必须过启动期同一条 normalize）、写回 model.sampling、以及「保存后整份
// 换掉快照」这条热生效路径（refreshSampling / samplingFor）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// samplingCfgPath 一份最小可启动配置（chat 协议，两个模型，逐模型覆盖一份）。
func samplingCfgPath(t *testing.T, extra string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{
  "model": {
    "providers": [
      {"name": "a", "base_url": "http://a/v1", "models": [
        {"name": "m1"},
        {"name": "cold", "sampling": {"temperature": 0}}
      ]}
    ],
    "primary": "a",
    "name": "m1"` + extra + `
  },
  "ui": {"kind": "repl"}
}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("写配置: %v", err)
	}
	return cfgPath
}

// TestSamplingFormTextRoundTrip 表单文本化与解析对上（D204）：留空 = 不发送、0 = 真的发 0、
// 停止串走 JSON 数组。解析规则与设置窗的前置校验是同一份实现（port.ParseSamplingText）。
func TestSamplingFormTextRoundTrip(t *testing.T) {
	zero := 0.0
	s := samplingConfig{Temperature: &zero, TopP: fp(0.9), MaxTokens: 256, Stop: []string{"\n", "END"}}
	got, err := port.ParseSamplingText(numText(s.Temperature), numText(s.TopP), intText(s.MaxTokens), stopsText(s.Stop))
	if err != nil {
		t.Fatalf("解析: %v", err)
	}
	if got.Temperature == nil || *got.Temperature != 0 {
		t.Fatalf("temperature 0 应保住: %v", got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.9 || got.MaxTokens != 256 {
		t.Fatalf("其余项 = %+v", got)
	}
	if len(got.Stop) != 2 || got.Stop[0] != "\n" || got.Stop[1] != "END" {
		t.Fatalf("停止串 = %q", got.Stop)
	}

	// 全留空 = 全不发送（不是「发 0」）。
	empty, err := port.ParseSamplingText("", "", "", "")
	if err != nil {
		t.Fatalf("空表单: %v", err)
	}
	if empty.Temperature != nil || empty.TopP != nil || empty.MaxTokens != 0 || empty.Stop != nil {
		t.Fatalf("空表单应全不发送: %+v", empty)
	}

	// 停止串必须写成 JSON 数组：逗号分隔会被当成一个整串（含逗号）——正是要避免的歧义。
	if _, err := port.ParseSamplingText("", "", "", "a,b"); err == nil {
		t.Fatal("非 JSON 停止串应报错")
	}
	if _, err := port.ParseSamplingText("", "", "", `[""]`); err == nil ||
		!strings.Contains(err.Error(), "空串") {
		t.Fatalf("空停止串 = %v", err)
	}
	if _, err := port.ParseSamplingText("abc", "", "", ""); err == nil {
		t.Fatal("温度非数字应报错")
	}
	if _, err := port.ParseSamplingText("", "", "0", ""); err == nil {
		t.Fatal("输出上限 0 应报错（留空才是不发送）")
	}
}

// TestSamplingFormRange 表单侧范围（D204 口径四）：温度 0–2、top_p 0–1。协议细分在
// validateSamplingSettings 里按合并后的值判（Anthropic 只接 0–1）。
func TestSamplingFormRange(t *testing.T) {
	for _, tc := range []struct {
		name string
		temp string
		topP string
		ok   bool
	}{
		{"边界 0", "0", "0", true},
		{"上界 2", "2", "1", true},
		{"温度超界", "2.1", "", false},
		{"温度负", "-0.1", "", false},
		{"top_p 超界", "", "1.1", false},
	} {
		_, err := port.ParseSamplingText(tc.temp, tc.topP, "", "")
		if tc.ok && err != nil {
			t.Errorf("%s: 应通过，报 %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: 应报错", tc.name)
		}
	}
}

// TestSamplingSettingsWriteBack 写回往返（D204）：四项整段替换、空 = 删段；窄写回
// （SamplingSet = false）不碰采样；逐模型覆盖原样保留。
func TestSamplingSettingsWriteBack(t *testing.T) {
	cfgPath := samplingCfgPath(t, "")
	p := uigui.SettingsPatch{
		Theme: "system", SamplingSet: true,
		SamplingTemperature: "0", SamplingTopP: "0.9", SamplingMaxTokens: "256",
		SamplingStop: `["\n","END"]`,
	}
	if err := persistSettingsTextKeys(cfgPath, p); err != nil {
		t.Fatalf("写回: %v", err)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if cfg.Model.Sampling.Temperature == nil || *cfg.Model.Sampling.Temperature != 0 {
		t.Fatalf("温度 0 应落盘: %+v", cfg.Model.Sampling)
	}
	if cfg.Model.Sampling.MaxTokens != 256 || len(cfg.Model.Sampling.Stop) != 2 {
		t.Fatalf("写回 = %+v", cfg.Model.Sampling)
	}
	// 逐模型覆盖不受整链缺省影响。
	if e := cfg.Model.Providers[0].modelByName("cold"); e == nil || e.Sampling == nil {
		t.Fatalf("逐模型覆盖被抹掉: %+v", cfg.Model.Providers[0].Models)
	}
	// 读回快照：0 显示成 "0"，不是空串。
	snap := readTextSettings(cfgPath)
	if snap.SamplingTemperature != "0" || snap.SamplingTopP != "0.9" || snap.SamplingMaxTokens != "256" {
		t.Fatalf("快照 = (%q, %q, %q)", snap.SamplingTemperature, snap.SamplingTopP, snap.SamplingMaxTokens)
	}

	// 窄写回（不设 SamplingSet）：采样段原封不动。
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{Theme: "dark"}); err != nil {
		t.Fatalf("窄写回: %v", err)
	}
	cfg, _ = loadConfig(cfgPath)
	if cfg.Model.Sampling.MaxTokens != 256 || cfg.Model.Sampling.Temperature == nil {
		t.Fatalf("窄写回不该碰采样: %+v", cfg.Model.Sampling)
	}

	// 全空 = 删段（回到「全不发送」）。
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{Theme: "dark", SamplingSet: true}); err != nil {
		t.Fatalf("清空: %v", err)
	}
	cfg, _ = loadConfig(cfgPath)
	if cfg.Model.Sampling.Temperature != nil || cfg.Model.Sampling.MaxTokens != 0 {
		t.Fatalf("清空后仍有余项: %+v", cfg.Model.Sampling)
	}
	// 逐模型覆盖照旧（清的是整链缺省，不是各条目的覆盖）。
	if e := cfg.Model.Providers[0].modelByName("cold"); e == nil || e.Sampling == nil {
		t.Fatalf("清整链缺省不该动逐模型覆盖: %+v", cfg.Model.Providers[0].Models)
	}
}

// TestSamplingSettingsRejectBadConfig 保存前校验（D204 口径五）：落盘配置必须能过启动期
// 同一条 normalize——超协议范围/负值在这里就被拦下，且原文件不动。
func TestSamplingSettingsRejectBadConfig(t *testing.T) {
	// Anthropic 只接 0–1：同值在 chat 合法，在这里必须拒。
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{"model":{"providers":[{"name":"a","kind":"anthropic","base_url":"http://a/v1","models":[{"name":"m1"}]}],"primary":"a","name":"m1"},"ui":{"kind":"repl"}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("写配置: %v", err)
	}
	if err := validateSamplingSettings(cfgPath, samplingConfig{Temperature: fp(1.5)}); err == nil ||
		!strings.Contains(err.Error(), "temperature") {
		t.Fatalf("Anthropic 温度 1.5 = %v, want 报因", err)
	}
	err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{
		Theme: "system", SamplingSet: true, SamplingTemperature: "1.5",
	})
	if err == nil {
		t.Fatal("超范围采样应拒绝写回")
	}
	raw, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(raw), `"sampling"`) {
		t.Fatalf("拒绝后不该落盘:\n%s", raw)
	}
}

// TestSamplingRefreshHotApply 热生效（D204 口径六）：保存后换掉快照，下一次取到的就是新值；
// 逐模型覆盖按模型名命中（含 /model 切过去的名字）。
func TestSamplingRefreshHotApply(t *testing.T) {
	cfgPath := samplingCfgPath(t, "")
	w := &wiring{cfgPath: cfgPath}
	w.cfg, _ = loadConfig(cfgPath)
	w.sampling.Store(w.cfg.Model.samplingTables())

	// 装配时的快照：缺省为空、cold 覆盖 0。
	if got := w.samplingFor("m1"); got.Temperature != nil || got.MaxTokens != 0 {
		t.Fatalf("初始缺省 = %+v", got)
	}
	if got := w.samplingFor("cold"); got.Temperature == nil || *got.Temperature != 0 {
		t.Fatalf("初始覆盖 = %v", got.Temperature)
	}

	// 保存新的整链缺省 → 热生效。
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{
		Theme: "system", SamplingSet: true,
		SamplingTemperature: "0.3", SamplingMaxTokens: "512",
	}); err != nil {
		t.Fatalf("写回: %v", err)
	}
	w.refreshSampling()
	got := w.samplingFor("m1")
	if got.Temperature == nil || *got.Temperature != 0.3 || got.MaxTokens != 512 {
		t.Fatalf("热生效后缺省 = %+v", got)
	}
	// 逐模型覆盖仍压过缺省（cold 的温度是它自己写死的 0）。
	if got := w.samplingFor("cold"); got.Temperature == nil || *got.Temperature != 0 {
		t.Fatalf("覆盖应压过新缺省: %v", got.Temperature)
	}
	if got := w.samplingFor("cold"); got.MaxTokens != 512 {
		t.Fatalf("覆盖没写的项应继承新缺省: %d", got.MaxTokens)
	}
	// 未声明的名字：只拿缺省（不 panic）。
	if got := w.samplingFor("nope"); got.Temperature == nil || *got.Temperature != 0.3 {
		t.Fatalf("未声明模型 = %+v", got)
	}
}

// TestSamplingRefreshKeepsOldOnBadFile 读不回来就保留上一份（D204）：配置刚写成功，读失败
// 多半是外部并发改坏——不该让下一次请求直接发不出参数。
func TestSamplingRefreshKeepsOldOnBadFile(t *testing.T) {
	cfgPath := samplingCfgPath(t, "")
	w := &wiring{cfgPath: cfgPath}
	w.cfg, _ = loadConfig(cfgPath)
	w.sampling.Store(w.cfg.Model.samplingTables())

	if err := os.WriteFile(cfgPath, []byte("{ 坏掉的 json"), 0o644); err != nil {
		t.Fatalf("写坏文件: %v", err)
	}
	w.refreshSampling()
	if w.sampling.Load() == nil {
		t.Fatal("读失败不该把快照清成 nil")
	}
	if got := w.samplingFor("m1"); got.Temperature != nil {
		t.Fatalf("应保留上一份空缺省: %+v", got)
	}
}

// TestSamplingAgentCallbackUsesSnapshot Agent 侧回调口径（D204）：SamplingFor 非空就按模型名
// 现取（不缓存），nil 才回落到装配时那份。
func TestSamplingAgentCallbackUsesSnapshot(t *testing.T) {
	cfgPath := samplingCfgPath(t, "")
	w := &wiring{cfgPath: cfgPath}
	w.cfg, _ = loadConfig(cfgPath)
	w.sampling.Store(w.cfg.Model.samplingTables())

	var got []port.Sampling
	fn := func(model string) port.Sampling {
		s := w.samplingFor(model)
		got = append(got, s)
		return s
	}
	fn("m1")
	fn("cold")
	if len(got) != 2 {
		t.Fatalf("调用次数 = %d", len(got))
	}
	if got[0].Temperature != nil {
		t.Fatalf("m1 不该有温度: %v", got[0].Temperature)
	}
	if got[1].Temperature == nil || *got[1].Temperature != 0 {
		t.Fatalf("cold 应取到覆盖 0: %v", got[1].Temperature)
	}
}
