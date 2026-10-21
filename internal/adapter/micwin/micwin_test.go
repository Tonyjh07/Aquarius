package micwin

import (
	"testing"
	"time"
)

// TestClampDuration 录音时长夹取（D179：缺省 60s、硬顶 4 分钟）。
func TestClampDuration(t *testing.T) {
	cases := []struct {
		in, want time.Duration
	}{
		{0, DefaultDuration},
		{-time.Second, DefaultDuration},
		{10 * time.Second, 10 * time.Second},
		{MaxDuration, MaxDuration},
		{MaxDuration + time.Minute, MaxDuration},
	}
	for _, c := range cases {
		if got := clampDuration(c.in); got != c.want {
			t.Errorf("clampDuration(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
