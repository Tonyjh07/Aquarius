package port

// 设置窗采样参数的表单解析（D231）。
//
// 为什么这一段在 port：同一份「四段文本 → 采样声明」的规则原先写了两遍——设置窗在
// 保存前校验一遍（好让用户立刻看到错误、不合法就不落盘），装配根在写回时再解析一遍
// （拿到真正要落盘的值）。两份实现意味着两套报错措辞与两处范围数字，改一处就会变成
// 「界面放行了、写回拒收」或者反过来。
//
// 为什么报错是中文：这条路径的输出只出现在设置窗的反馈行里，从不拼进提示词或工具
// 回填，属于面向用户的界面文案（进入模型上下文的字符串才要求英文）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// ParseSamplingText 设置窗的四段采样文本 → 采样声明。
//
// 空串 = 该项不发送（交服务端默认），"0" = 真的发 0——两者在表单里必须能区分，所以输入
// 是字符串而不是数值（用数值的零值兼作「没填」就永远表达不出 0）。
//
// 范围取 SettingsTemperature* / SettingsTopP*：这里管「表单允许用户填到哪」，协议更严的
// 限制（Anthropic 的 temperature 只接 0..1）由启动校验按协议再判一层。
//
// 停止串用 JSON 数组承载（如 `["\n", "END"]`），不能用逗号分隔：停止串里可以有逗号、
// 空格、换行，任何一种分隔符都会把用户写的串悄悄改成另一个串。串**不 trim**——"\n"、
// " " 本身就是合法停止条件，去掉首尾空白等于换了个串；只拒绝空串。
func ParseSamplingText(temperature, topP, maxTokens, stop string) (Sampling, error) {
	var out Sampling
	if v := strings.TrimSpace(temperature); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return out, errors.New(i18n.Tf("port.err.temperature_nan", "v", strconv.Quote(v)))
		}
		if n < SettingsTemperatureMin || n > SettingsTemperatureMax {
			return out, errors.New(i18n.Tf("port.err.temperature_range",
				"min", strconv.FormatFloat(SettingsTemperatureMin, 'g', -1, 64),
				"max", strconv.FormatFloat(SettingsTemperatureMax, 'g', -1, 64)))
		}
		out.Temperature = &n
	}
	if v := strings.TrimSpace(topP); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return out, errors.New(i18n.Tf("port.err.top_p_nan", "v", strconv.Quote(v)))
		}
		if n < SettingsTopPMin || n > SettingsTopPMax {
			return out, errors.New(i18n.Tf("port.err.top_p_range",
				"min", strconv.FormatFloat(SettingsTopPMin, 'g', -1, 64),
				"max", strconv.FormatFloat(SettingsTopPMax, 'g', -1, 64)))
		}
		out.TopP = &n
	}
	if v := strings.TrimSpace(maxTokens); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return out, errors.New(i18n.Tf("port.err.max_tokens", "v", strconv.Quote(v)))
		}
		out.MaxTokens = n
	}
	if v := strings.TrimSpace(stop); v != "" {
		var stops []string
		if err := json.Unmarshal([]byte(v), &stops); err != nil {
			return out, fmt.Errorf("%s: %w", i18n.T("port.err.stop_json"), err)
		}
		for _, s := range stops {
			if s == "" {
				return out, errors.New(i18n.T("port.err.stop_empty"))
			}
		}
		out.Stop = stops
	}
	return out, nil
}
