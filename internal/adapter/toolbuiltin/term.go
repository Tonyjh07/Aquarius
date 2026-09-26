package toolbuiltin

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// maxTermOut term_exec 输出的保头尾上限（DESIGN §4.3"输出截断保头尾"）：
// 头尾各半，总量略小于常见 tool_output_chars（20000），Runner 的统一裁剪
// 通常不再二次截断；用户调小限额时由 Runner 兜底。
const maxTermOut = 16000

// termExec 终端命令同步执行。执行类工具：不实现 FileTarget，
// 按工具列判定（D22 两列分工：Risk=Confirm，仅 full-access 免确认）。
type termExec struct{ jobs port.JobManager }

var _ port.Tool = (*termExec)(nil)

func (t *termExec) Spec() tool.Spec {
	return tool.Spec{
		Name: "term_exec",
		Description: "Run a terminal command line synchronously (timeout is governed by the run limits; output is truncated head-and-tail " +
			"and transcoded to UTF-8, so there is no need to switch the console code page with chcp). " +
			"Windows runs via cmd /c, others via sh -c; a non-zero exit is treated as failure and brings back the output.",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "the full command line to run"}
			},
			"required": ["command"]
		}`),
		Risk: tool.Confirm,
	}
}

func (t *termExec) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err // 入口即响应取消（DESIGN §10）
	}
	jobs, err := requireJobs(t.jobs, "term_exec")
	if err != nil {
		return tool.Result{}, err
	}
	var a struct {
		Command string `json:"command"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	command := strings.TrimSpace(a.Command)
	if command == "" {
		return tool.Result{}, fmt.Errorf("missing command")
	}
	// 模型给出的是完整命令行字符串：包一层平台 shell 执行（不做分词，
	// 引号/管道/重定向语义交给 shell，用户经 Confirmer 逐次过目）。
	res, err := jobs.Run(ctx, shellSpec(command))
	if err != nil {
		return tool.Result{}, err // ctx 取消等装配级错误由 Runner 归类上抛
	}
	if res.CallID == "" {
		res.CallID = call.ID
	}
	res.Output = trimHeadTail(res.Output, maxTermOut)
	return res, nil
}

// shellSpec 平台 shell 包装。
func shellSpec(command string) port.JobSpec {
	if runtime.GOOS == "windows" {
		return port.JobSpec{Command: "cmd", Args: []string{"/c", command}}
	}
	return port.JobSpec{Command: "/bin/sh", Args: []string{"-c", command}}
}

// trimHeadTail 保头尾截断：头尾各 keep/2，中段以标注代替并给出原长。
// 省略数按实际保留量（2×keep）计——max 为奇数时 keep*2 = max-1，
// 若按 len-max 会少报 1（§14 M3 遗留）。
func trimHeadTail(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	keep := max / 2
	return string(r[:keep]) +
		fmt.Sprintf("\n…[output too long: middle %d chars omitted, %d chars total]\n", len(r)-2*keep, len(r)) +
		string(r[len(r)-keep:])
}
