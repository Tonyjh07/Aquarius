package port

import "testing"

// fp 采样参数指针形（D204：nil = 不发送、非 nil 照发，含 0）。
func fp(v float64) *float64 { return &v }

// TestSamplingOverlay 逐模型覆盖的合并口径（D204 口径二）：只改写了的那几项，
// 其余继承缺省；0 是**真值**不是「没写」；Stop 非空整体替换。
func TestSamplingOverlay(t *testing.T) {
	base := Sampling{Temperature: fp(0.7), TopP: fp(0.9), MaxTokens: 512, Stop: []string{"\n"}}

	// 全空覆盖 = 原样继承（模型条目只声明了别的属性时走这条）。
	if got := base.Overlay(Sampling{}); got.Temperature != base.Temperature ||
		got.TopP != base.TopP || got.MaxTokens != 512 || len(got.Stop) != 1 {
		t.Fatalf("空覆盖应完全继承: %+v", got)
	}

	// 温度写 0 = 真的要 0（这是本决策的核心：零值不能兼作「不发送」）。
	got := base.Overlay(Sampling{Temperature: fp(0)})
	if got.Temperature == nil || *got.Temperature != 0 {
		t.Fatalf("temperature 0 应覆盖成指针 0: %v", got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.9 {
		t.Fatalf("没写的 top_p 应继承: %v", got.TopP)
	}

	// 只写 max_tokens：其余不动，Stop 整体替换（不是拼接）。
	got = base.Overlay(Sampling{MaxTokens: 128, Stop: []string{"END"}})
	if got.MaxTokens != 128 || len(got.Stop) != 1 || got.Stop[0] != "END" {
		t.Fatalf("max_tokens / stop 覆盖 = %+v", got)
	}
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Fatalf("temperature 应继承缺省: %v", got.Temperature)
	}

	// top_p 写 0（合法取值：只留最高概率那一个 token）。
	got = base.Overlay(Sampling{TopP: fp(0)})
	if got.TopP == nil || *got.TopP != 0 {
		t.Fatalf("top_p 0 应覆盖成指针 0: %v", got.TopP)
	}
}

// TestSamplingOverlayKeepsThinking 思考三件套（会话级开关）同样按「非空才覆盖」合并：
// 逐模型声明通常不写它们，不能让合并把 /think 的当前值清掉。
func TestSamplingOverlayKeepsThinking(t *testing.T) {
	on := true
	base := Sampling{ReasoningEffort: "high", Thinking: &on}
	got := base.Overlay(Sampling{Temperature: fp(0.2)})
	if got.ReasoningEffort != "high" || got.Thinking == nil || !*got.Thinking {
		t.Fatalf("思考参数应继承: %+v", got)
	}
}
