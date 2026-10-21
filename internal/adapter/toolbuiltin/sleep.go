package toolbuiltin

import (
	"context"
	"fmt"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// sleepTool 等待工具（D39）：给模型一个可控停顿——控制节奏。
// 等后台任务用 job_wait（D189），不用 sleep 猜时长。
type sleepTool struct{}

func (t *sleepTool) Spec() tool.Spec {
	return tool.Spec{
		Name: "sleep",
		Description: fmt.Sprintf("Wait for N seconds to pace yourself. Cancellable via interruption. "+
			"Do not use it to wait for a background task - call job_wait for that; do not use it to wait for an "+
			"external state to change (a CI run, a log line) - declare watch_start for that. "+
			"Sleeps longer than the default tool timeout must pass a matching \"timeout_sec\" argument (1-%d) on the call.", port.MaxCallTimeoutSec),
		Schema: jsonSchema(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"seconds": {"type": "integer", "description": "seconds to wait (1-%d)"}
			},
			"required": ["seconds"]
		}`, port.MaxCallTimeoutSec)),
		Risk: tool.Safe,
	}
}

func (t *sleepTool) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	var a struct {
		Seconds int `json:"seconds"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	if a.Seconds < 1 || a.Seconds > port.MaxCallTimeoutSec {
		return tool.Result{}, fmt.Errorf("seconds must be 1-%d, got %d", port.MaxCallTimeoutSec, a.Seconds)
	}
	select {
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	case <-time.After(time.Duration(a.Seconds) * time.Second):
		return okResult(fmt.Sprintf("slept %ds", a.Seconds)), nil
	}
}
