package main

// sampling_settings.go 设置窗「模型」页采样参数（D204）的读写：快照文本化、保存前校验、
// 泛键写回 model.sampling。
//
// 表单用**字符串**承载而不是 float，是因为这里的「留空」与「写 0」语义不同：留空 = 这个
// 字段根本不发（交服务端默认），写 0 = 明确要 0（温度 0 就是每次取最可能的词）。用 float
// 的零值兼作「没填」就永远表达不出 0。

import (
	"encoding/json"
	"strconv"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/port"
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

// formSampling 从 patch 解析出采样声明（设置窗侧唯一入口）。解析规则（含范围与停止串
// 的 JSON 形态）在 port.ParseSamplingText——设置窗的前置校验与这里的写回解析是同一份
// 实现，不会出现「界面放行、写回拒收」。
func formSampling(p uigui.SettingsPatch) (samplingConfig, error) {
	s, err := port.ParseSamplingText(p.SamplingTemperature, p.SamplingTopP, p.SamplingMaxTokens, p.SamplingStop)
	if err != nil {
		return samplingConfig{}, err
	}
	return samplingConfig{
		Temperature: s.Temperature,
		TopP:        s.TopP,
		MaxTokens:   s.MaxTokens,
		Stop:        s.Stop,
	}, nil
}
