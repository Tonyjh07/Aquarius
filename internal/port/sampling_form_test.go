package port

// 采样表单解析（D231）的归属在 port，所以范围与措辞的边界由这里守：设置窗的前置校验
// 与写回侧解析都调它，两边不会再各写一份。

import (
	"strings"
	"testing"
)

// TestParseSamplingTextEmptyMeansNotSent 留空 = 该项不发送，与「写 0」严格区分。
func TestParseSamplingTextEmptyMeansNotSent(t *testing.T) {
	s, err := ParseSamplingText("", "", "", "")
	if err != nil {
		t.Fatalf("全留空应合法: %v", err)
	}
	if s.Temperature != nil || s.TopP != nil || s.MaxTokens != 0 || s.Stop != nil {
		t.Fatalf("全留空应全不发送: %+v", s)
	}

	zero, err := ParseSamplingText("0", "0", "", "")
	if err != nil {
		t.Fatalf("温度 0 应合法: %v", err)
	}
	if zero.Temperature == nil || *zero.Temperature != 0 {
		t.Fatalf("温度 0 应真的带上: %v", zero.Temperature)
	}
	if zero.TopP == nil || *zero.TopP != 0 {
		t.Fatalf("top_p 0 应真的带上: %v", zero.TopP)
	}
}

// TestParseSamplingTextRanges 上下界含端点，越界报因；协议细分不在这里（Anthropic 的
// temperature 0..1 由启动校验按协议再判一层）。
func TestParseSamplingTextRanges(t *testing.T) {
	ok := []struct{ temp, topP string }{
		{"0", "0"},
		{"2", "1"},
	}
	for _, c := range ok {
		if _, err := ParseSamplingText(c.temp, c.topP, "", ""); err != nil {
			t.Errorf("temp=%q top_p=%q 应通过，报 %v", c.temp, c.topP, err)
		}
	}
	bad := []struct{ name, temp, topP, maxTokens string }{
		{"温度超上界", "2.1", "", ""},
		{"温度负", "-0.1", "", ""},
		{"温度非数字", "abc", "", ""},
		{"top_p 超上界", "", "1.1", ""},
		{"top_p 负", "", "-0.1", ""},
		{"top_p 非数字", "", "abc", ""},
		{"输出上限 0", "", "", "0"},
		{"输出上限负", "", "", "-1"},
		{"输出上限非数字", "", "", "abc"},
	}
	for _, c := range bad {
		if _, err := ParseSamplingText(c.temp, c.topP, c.maxTokens, ""); err == nil {
			t.Errorf("%s：应报因", c.name)
		}
	}
}

// TestParseSamplingTextStop 停止串只认 JSON 数组，且不 trim 串内容（"\n"、" " 本身就是
// 合法停止条件）；空串元素拒收。
func TestParseSamplingTextStop(t *testing.T) {
	s, err := ParseSamplingText("", "", "", `["\n", " ", "END"]`)
	if err != nil {
		t.Fatalf("JSON 数组应合法: %v", err)
	}
	if len(s.Stop) != 3 || s.Stop[0] != "\n" || s.Stop[1] != " " || s.Stop[2] != "END" {
		t.Fatalf("停止串被改动: %q", s.Stop)
	}

	// 逗号分隔会被当成一个整串（含逗号）——正是 JSON 要避免的歧义。
	if _, err := ParseSamplingText("", "", "", "a,b"); err == nil {
		t.Error("非 JSON 停止串应报因")
	}
	if _, err := ParseSamplingText("", "", "", `[""]`); err == nil ||
		!strings.Contains(err.Error(), "空串") {
		t.Errorf("空停止串应点名空串，得到 %v", err)
	}
}

// TestParseSamplingTextMessageIsUserFacing 报错文案面向设置窗反馈行：中文、带区间，
// 不含内部编号。这条守的是「port 里为什么会出现中文」——它输出的不是模型上下文。
func TestParseSamplingTextMessageIsUserFacing(t *testing.T) {
	_, err := ParseSamplingText("9", "", "", "")
	if err == nil {
		t.Fatal("超界应报因")
	}
	msg := err.Error()
	if !strings.Contains(msg, "采样温度") {
		t.Errorf("文案应点名哪一项: %q", msg)
	}
	if strings.Contains(msg, "D2") || strings.Contains(msg, "§") {
		t.Errorf("文案不应带内部编号: %q", msg)
	}
}
