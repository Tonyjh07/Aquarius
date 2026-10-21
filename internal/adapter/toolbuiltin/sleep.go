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
			"A sleep longer than the default per-call tool timeout must raise that timeout on the same call "+
			"(the timeout_sec call option, 1-%d).", port.MaxCallTimeoutSec),
		// seconds 声明自己的边界，但**不声明 timeout_sec**：那是调用级的保留参数（D38），
		// schema 一旦声明它，runner 就把它当作工具自有语义、不再按次覆盖超时——
		// 300s 的 sleep 会被缺省 60s 掐掉（schema 声明与行为就此相反）。
		Schema: jsonSchema(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"seconds": {"type": "integer", "minimum": 1, "maximum": %d, "description": "seconds to wait (1-%d)"}
			},
			"required": ["seconds"]
		}`, port.MaxCallTimeoutSec, port.MaxCallTimeoutSec)),
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
