package main

// sampling_settings.go 设置窗「模型」页采样参数（D204）的读写：快照文本化、保存前校验、
// 泛键写回 model.sampling。
//
// 表单用**字符串**承载而不是 float，是因为这里的「留空」与「写 0」语义不同：留空 = 这个
// 字段根本不发（交服务端默认），写 0 = 明确要 0（温度 0 就是每次取最可能的词）。用 float
// 的零值兼作「没填」就永远表达不出 0。

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// stopsText 停止串列表 → 表单文本（JSON 数组，如 `["\n"]`）。空列表 = 空串（不发送）。
// 用 JSON 而不是逗号分隔：停止串里可以有逗号、空格、换行，任何一种分隔符都会把用户写的
// 串悄悄改成另一个串。
func stopsText(stops []string) string {
	if len(stops) == 0 {
		return ""
	}
	data, err := json.Marshal(stops)
	if err != nil {
		return ""
	}
	return string(data)
}

// numText 指针数值 → 表单文本（nil = 空串 = 不发送；0 照写）。
func numText(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'g', -1, 64)
}

// intText 数值 → 表单文本（0 = 空串 = 不发送）。
func intText(v int) string {
	if v <= 0 {
		return ""
	}
	return strconv.Itoa(v)
}

// parseSamplingForm 表单四段 → samplingConfig（保存前调用）。空串 = 该项不写。
// 只做语法校验；取值范围留给 config 侧同一条口径（写入后由启动校验按住）。
func parseSamplingForm(temp, topP, maxTokens, stop string) (samplingConfig, error) {
	var out samplingConfig
	if v := strings.TrimSpace(temp); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return out, fmt.Errorf("采样温度 %q 不是数字", v)
		}
		out.Temperature = &n
	}
	if v := strings.TrimSpace(topP); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return out, fmt.Errorf("top_p %q 不是数字", v)
		}
		out.TopP = &n
	}
	if v := strings.TrimSpace(maxTokens); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return out, fmt.Errorf("输出上限 %q 须为正整数", v)
		}
		out.MaxTokens = n
	}
	if v := strings.TrimSpace(stop); v != "" {
		var stops []string
		if err := json.Unmarshal([]byte(v), &stops); err != nil {
			return out, fmt.Errorf("停止串须写成 JSON 数组（如 [\"\\n\", \"END\"]）: %w", err)
		}
		for _, s := range stops {
			if s == "" {
				return out, fmt.Errorf("停止串里不能有空串（那不是停止条件）")
			}
		}
		out.Stop = stops
	}
	return out, nil
}

// validateSamplingForm 保存前的语法占位（范围校验见 validateSamplingSettings——那一步
// 要知道协议才判得准：Anthropic 只接 0..1，其余接 0..2）。
func validateSamplingForm(s samplingConfig) error {
	if s.Temperature != nil && (*s.Temperature < 0 || *s.Temperature > 2) {
		return fmt.Errorf("采样温度须在 0–2 之间（留空 = 交服务端默认）")
	}
	if s.TopP != nil && (*s.TopP < 0 || *s.TopP > 1) {
		return fmt.Errorf("top_p 须在 0–1 之间")
	}
	return nil
}

// validateSamplingSettings 保存前的完整校验（D204）：把这次改动叠进**磁盘上那份配置的
// 内存副本**，跑一遍启动期同一条 normalize——写出去的配置必须能过启动校验，否则用户
// 重启后连对话都开不起来（与 provider 应用同口径）。
func validateSamplingSettings(cfgPath string, s samplingConfig) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	cfg.Model.Sampling = s
	return cfg.Model.normalize()
}

// writeSamplingSection 把 patch 的采样四项写回 model.sampling（D204，恒全量写）：
// 空串 = 删掉该键（不发送），有值 = 照写（含 0）。
func writeSamplingSection(generic map[string]any, s samplingConfig) {
	model := modelSection(generic)
	sec := map[string]any{}
	if s.Temperature != nil {
		sec["temperature"] = *s.Temperature
	}
	if s.TopP != nil {
		sec["top_p"] = *s.TopP
	}
	if s.MaxTokens > 0 {
		sec["max_tokens"] = s.MaxTokens
	}
	if len(s.Stop) > 0 {
		stops := make([]any, 0, len(s.Stop))
		for _, v := range s.Stop {
			stops = append(stops, v)
		}
		sec["stop"] = stops
	}
	// 四项全空 = 整段删掉：不发送是缺省形态，留一个空对象只会让人以为「配了但没生效」。
	if len(sec) == 0 {
		delete(model, "sampling")
		return
	}
	model["sampling"] = sec
}

// formSampling 从 patch 解析出采样声明（设置窗侧唯一入口）。
func formSampling(p uigui.SettingsPatch) (samplingConfig, error) {
	s, err := parseSamplingForm(p.SamplingTemperature, p.SamplingTopP, p.SamplingMaxTokens, p.SamplingStop)
	if err != nil {
		return s, err
	}
	if err := validateSamplingForm(s); err != nil {
		return s, err
	}
	return s, nil
}
