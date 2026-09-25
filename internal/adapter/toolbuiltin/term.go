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
		Description: "同步执行终端命令行（超时由运行限额统一控制；输出保头尾截断）。" +
			"Windows 经 cmd /c，其余经 sh -c；退出码非 0 视为失败并带回输出。",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "要执行的完整命令行"}
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
		return tool.Result{}, fmt.Errorf("缺少 command")
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
		fmt.Sprintf("\n…[输出过长，已省略中间 %d 字符，原文共 %d 字符]\n", len(r)-max, len(r)) +
		string(r[len(r)-keep:])
}
