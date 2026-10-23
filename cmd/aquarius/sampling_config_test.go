package main

// sampling_config_test.go 采样参数与单价（D204/D205）：解析、两级覆盖合并、范围校验、
// 单价补齐到 ModelInfo。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fp 采样参数指针形（D204）。
func fp(v float64) *float64 { return &v }

// TestSamplingTwoLevelMerge 两级覆盖（D204 口径二）：model.sampling 是整链缺省，
// models[].sampling 只改写了的那几项；`temperature: 0` 是真值不是「没写」。
func TestSamplingTwoLevelMerge(t *testing.T) {
	m := modelConfig{
		Sampling: samplingConfig{Temperature: fp(0.8), TopP: fp(0.95), MaxTokens: 1024, Stop: []string{"\n"}},
		Providers: []providerConfig{{
			Name: "a", BaseURL: "http://a",
			Models: []modelEntry{
				{Name: "plain"},
				{Name: "cold", Sampling: &samplingConfig{Temperature: fp(0)}},
				{Name: "short", Sampling: &samplingConfig{MaxTokens: 64, Stop: []string{"END"}}},
			},
		}},
	}
	if err := m.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	m.Name = "plain"

	// 没写覆盖：完全继承整链缺省。
	got := m.currentSampling()
	if got.Temperature == nil || *got.Temperature != 0.8 || got.MaxTokens != 1024 || len(got.Stop) != 1 {
		t.Fatalf("缺省继承 = %+v", got)
	}

	// 写 0：真的发 0（这正是用指针的理由）。
	m.Name = "cold"
	got = m.currentSampling()
	if got.Temperature == nil || *got.Temperature != 0 {
		t.Fatalf("temperature 0 应保留为指针 0: %v", got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.95 {
		t.Fatalf("没写的 top_p 应继承: %v", got.TopP)
	}

	// 只改 max_tokens / stop：其余继承。
	m.Name = "short"
	got = m.currentSampling()
	if got.MaxTokens != 64 || len(got.Stop) != 1 || got.Stop[0] != "END" {
		t.Fatalf("覆盖项 = %+v", got)
	}
	if got.Temperature == nil || *got.Temperature != 0.8 {
		t.Fatalf("temperature 应继承缺省: %v", got.Temperature)
	}

	// 按二元组直接取（降级路径用这条）。
	got = m.samplingFor("a", "cold")
	if got.Temperature == nil || *got.Temperature != 0 {
		t.Fatalf("samplingFor 应命中逐模型覆盖: %v", got.Temperature)
	}
	// 未声明的名字：落回整链缺省（不 panic、不返回零值）。
	got = m.samplingFor("a", "unknown-model")
	if got.Temperature == nil || *got.Temperature != 0.8 {
		t.Fatalf("未声明模型应继承缺省: %v", got.Temperature)
	}
}

// TestSamplingKeysMissing 键缺失 = 全不发送（nil / 0 / 空数组），不能变成「发 0」。
func TestSamplingKeysMissing(t *testing.T) {
	var s samplingConfig
	if err := json.Unmarshal([]byte(`{"max_tokens":0}`), &s); err != nil {
		t.Fatalf("解析: %v", err)
	}
	got := s.toSampling()
	if got.Temperature != nil || got.TopP != nil || got.MaxTokens != 0 || got.Stop != nil {
		t.Fatalf("键缺失应全不发送: %+v", got)
	}

	// 显式写 0：指针 0（要真发）。
	if err := json.Unmarshal([]byte(`{"temperature":0,"top_p":0}`), &s); err != nil {
		t.Fatalf("解析: %v", err)
	}
	got = s.toSampling()
	if got.Temperature == nil || *got.Temperature != 0 || got.TopP == nil || *got.TopP != 0 {
		t.Fatalf("显式 0 应保留: %+v", got)
	}
}

// TestSamplingValidateRanges 范围校验（D204 口径四）：Anthropic 只接 0..1，其余 0..2；
// top_p 三协议都是 0..1。超范围启动报因，不静默截断。
func TestSamplingValidateRanges(t *testing.T) {
	base := func() modelConfig {
		return modelConfig{
			Sampling: samplingConfig{Temperature: fp(1.5)},
			Providers: []providerConfig{{
				Name: "a", BaseURL: "http://a", Kind: kindAnthropic,
				Models: []modelEntry{{Name: "m1"}},
			}},
		}
	}
	c := base()
	err := c.normalize()
	if err == nil || !strings.Contains(err.Error(), "temperature") {
		t.Fatalf("Anthropic 温度 1.5 应启动报因: %v", err)
	}

	// 同值在 chat 协议下合法（0..2）。
	c = base()
	c.Providers[0].Kind = kindOpenAIChat
	if err := c.normalize(); err != nil {
		t.Fatalf("chat 温度 1.5 应合法: %v", err)
	}

	// top_p > 1 任何协议都拒。
	c = base()
	c.Providers[0].Kind = kindOpenAIChat
	c.Sampling = samplingConfig{TopP: fp(1.5)}
	if err := c.normalize(); err == nil || !strings.Contains(err.Error(), "top_p") {
		t.Fatalf("top_p 1.5 应启动报因: %v", err)
	}

	// 逐模型覆盖同样按合并后的值校验：缺省合法、覆盖超范围也要拦。
	c = modelConfig{
		Sampling: samplingConfig{Temperature: fp(0.7)},
		Providers: []providerConfig{{
			Name: "a", BaseURL: "http://a", Kind: kindAnthropic,
			Models: []modelEntry{{Name: "m1", Sampling: &samplingConfig{Temperature: fp(1.4)}}},
		}},
	}
	if err := c.normalize(); err == nil || !strings.Contains(err.Error(), "temperature") {
		t.Fatalf("逐模型覆盖超范围应报因: %v", err)
	}

	// 负数与空停止串。
	c = base()
	c.Providers[0].Kind = kindOpenAIChat
	c.Sampling = samplingConfig{MaxTokens: -1}
	if err := c.normalize(); err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("负 max_tokens 应报因: %v", err)
	}
	c.Sampling = samplingConfig{Stop: []string{"\n", ""}}
	if err := c.normalize(); err == nil || !strings.Contains(err.Error(), "stop") {
		t.Fatalf("空停止串应报因: %v", err)
	}
	// 只有空白字符的串是合法停止串（用户可能就是要在空白处停），不该被当成写错。
	c.Sampling = samplingConfig{Stop: []string{"\n", " "}}
	if err := c.normalize(); err != nil {
		t.Fatalf("空白停止串应合法: %v", err)
	}
}

// TestModelCostEnrich 单价（D205）：每百万 token 从 config 补齐到 ModelInfo；未声明保持 0
// （界面据此不显示费用，不当作免费）。
func TestModelCostEnrich(t *testing.T) {
	m := modelConfig{
		Providers: []providerConfig{{
			Name: "a", BaseURL: "http://a",
			Models: []modelEntry{
				{Name: "priced", CostIn: 2.5, CostOut: 10},
				{Name: "free"},
			},
		}},
	}
	if err := m.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	got := m.enrichModels([]port.ModelInfo{{Name: "priced"}, {Name: "free"}, {Name: "unlisted"}})
	if len(got) != 3 {
		t.Fatalf("enrich = %+v", got)
	}
	if got[0].CostIn != 2.5 || got[0].CostOutUSD != 10 {
		t.Fatalf("单价未补齐: %+v", got[0])
	}
	if got[1].CostIn != 0 || got[1].CostOutUSD != 0 || got[2].CostIn != 0 {
		t.Fatalf("未声明单价应保持 0: %+v", got)
	}
	// 负数单价是写错了，不是「未声明」。
	m.Providers[0].Models[1].CostIn = -1
	if err := m.normalize(); err == nil || !strings.Contains(err.Error(), "cost_in") {
		t.Fatalf("负单价应报因: %v", err)
	}
}

// TestSamplingConfigRoundTrip 设置窗/高级页写回后仍能读回（JSON 键名与结构稳定）。
func TestSamplingConfigRoundTrip(t *testing.T) {
	src := `{"temperature":0,"top_p":0.9,"max_tokens":256,"stop":["END"]}`
	var s samplingConfig
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatalf("解析: %v", err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("编码: %v", err)
	}
	for _, want := range []string{`"temperature":0`, `"top_p":0.9`, `"max_tokens":256`, `"stop":["END"]`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("编码缺 %s: %s", want, data)
		}
	}
}
