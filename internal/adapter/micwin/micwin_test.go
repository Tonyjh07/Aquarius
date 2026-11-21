package micwin

import (
	"testing"
	"time"
)

// TestClampDuration 录音时长夹取（D179：缺省 60s、硬顶 4 分钟；D316：硬顶可按后端收紧）。
func TestClampDuration(t *testing.T) {
	r := New(Options{})
	cases := []struct {
		in, want time.Duration
	}{
		{0, DefaultDuration},
		{-time.Second, DefaultDuration},
		{10 * time.Second, 10 * time.Second},
		{defaultMaxDuration, defaultMaxDuration},
		{defaultMaxDuration + time.Minute, defaultMaxDuration},
	}
	for _, c := range cases {
		if got := r.clampDuration(c.in); got != c.want {
			t.Errorf("clampDuration(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestClampDurationCapped 硬顶收紧（D316：deepseek 识别单段上限 120 秒）：超过新顶的
// 请求夹到新顶，缺省时长不受影响。
func TestClampDurationCapped(t *testing.T) {
	r := New(Options{MaxDuration: 120 * time.Second})
	if got := r.clampDuration(4 * time.Minute); got != 120*time.Second {
		t.Errorf("clamp(4m) = %v, want 120s", got)
	}
	if got := r.clampDuration(0); got != DefaultDuration {
		t.Errorf("clamp(0) = %v, want 缺省 %v", got, DefaultDuration)
	}
	if got := r.clampDuration(60 * time.Second); got != 60*time.Second {
		t.Errorf("clamp(60s) = %v, want 60s", got)
	}
}
