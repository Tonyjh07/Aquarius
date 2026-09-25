package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// TestSleepSpec D39：sleep 是 Safe 工具，描述告知 timeout_sec 配套用法（发现性）。
func TestSleepSpec(t *testing.T) {
	spec := (&sleepTool{}).Spec()
	if spec.Name != "sleep" || spec.Risk != tool.Safe {
		t.Fatalf("spec = %+v, want name=sleep risk=Safe", spec)
	}
	if !strings.Contains(spec.Description, "timeout_sec") {
		t.Fatalf("description 应提示配套 timeout_sec: %q", spec.Description)
	}
}

// TestSleepWaitsAndReports 按秒等待并回填实际时长。
func TestSleepWaitsAndReports(t *testing.T) {
	start := time.Now()
	res, err := (&sleepTool{}).Execute(context.Background(),
		tool.Call{Args: json.RawMessage(`{"seconds":1}`)})
	if err != nil || !res.OK || res.Output != "slept 1s" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if d := time.Since(start); d < time.Second || d > 3*time.Second {
		t.Fatalf("等待 = %s, want ≈1s", d)
	}
}

// TestSleepRejectsOutOfRange 值域外/缺参报错回填（模型可纠正），不静默等待。
func TestSleepRejectsOutOfRange(t *testing.T) {
	for _, args := range []string{`{"seconds":0}`, `{"seconds":-1}`, `{"seconds":3601}`, `{}`} {
		_, err := (&sleepTool{}).Execute(context.Background(), tool.Call{Args: json.RawMessage(args)})
		if err == nil || !strings.Contains(err.Error(), "seconds must be 1-3600") {
			t.Fatalf("args=%s err=%v, want 值域报错", args, err)
		}
	}
	// 非整数：decodeArgs 层报 JSON 错误。
	if _, err := (&sleepTool{}).Execute(context.Background(), tool.Call{Args: json.RawMessage(`{"seconds":"1"}`)}); err == nil {
		t.Fatal("非整数 seconds 应报错")
	}
}

// TestSleepInterruptible 取消立即中断（不等满 3600s）——Ctrl+C/调用超时可打断。
func TestSleepInterruptible(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := (&sleepTool{}).Execute(ctx, tool.Call{Args: json.RawMessage(`{"seconds":3600}`)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("取消未及时中断: %s", time.Since(start))
	}
}
