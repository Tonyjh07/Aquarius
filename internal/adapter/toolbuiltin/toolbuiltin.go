// Package toolbuiltin 实现 DESIGN §4.3 的内置工具（memory_* / file_* / think），
// 全部经 port.Tool 契约接入（D13 内置不享特权）：权限判定、确认、超时与结果裁剪
// 由 ToolRunner 统一执行（D25），本包只做"参数解析 + 干活 + 申报文件目标"。
//
// 会话作用域：memory_* 按文档名寻址，但只允许操作全局与**当前会话**两份记忆
// （DESIGN §4.4）——当前会话 ID 经 port.SessionIDFrom 从工具执行上下文取。
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// PathOf 文档名 → 磁盘路径（memory_write 申报 FileTarget 用）。
// 由装配根注入（与 memoryfs 同源，D23 布局单一事实源）；nil 或未命中 = 不申报。
type PathOf func(name string) (string, bool)

// New 返回全部内置工具实例（main 装配进 ToolRunner；实例无状态）。
// jobs 为任务端口（term_exec / job_* 用）：装配根必传；测试不关心时可传 nil，
// 此类调用执行会明确报"未配置任务管理器"而非 panic。
// sandbox 为特权沙盒目录（装配根注入）：仅用于 file_* 相对路径报错时给出
// 可行动的替代提示（D34 反馈；空 = 不提示，测试缺省）。
func New(mem port.MemoryStore, pathOf PathOf, jobs port.JobManager, sandbox string) []port.Tool {
	return []port.Tool{
		&memoryList{mem: mem},
		&memoryRead{mem: mem},
		&memorySearch{mem: mem},
		&memoryWrite{mem: mem, pathOf: pathOf},
		&fileRead{sandbox: sandbox},
		&fileList{sandbox: sandbox},
		&fileSearch{sandbox: sandbox},
		&fileWrite{sandbox: sandbox},
		&fileDelete{sandbox: sandbox},
		&thinkTool{},
		&termExec{jobs: jobs},
		&jobStart{jobs: jobs},
		&jobList{jobs: jobs},
		&jobStatus{jobs: jobs},
		&jobLogs{jobs: jobs},
		&jobKill{jobs: jobs},
	}
}

// requireJobs 任务类工具的管理器校验（未装配时明确报错，不 panic）。
func requireJobs(jobs port.JobManager, name string) (port.JobManager, error) {
	if jobs == nil {
		return nil, fmt.Errorf("%s: 未配置任务管理器（port.JobManager）", name)
	}
	return jobs, nil
}

// decodeArgs 解析工具调用的 JSON 参数；空参数按空对象。
func decodeArgs(call tool.Call, v any) error {
	if len(call.Args) == 0 {
		return nil
	}
	if err := json.Unmarshal(call.Args, v); err != nil {
		return fmt.Errorf("参数不是合法 JSON 对象: %w", err)
	}
	return nil
}

// requireSessionDoc 校验记忆文档名只指向全局或当前会话（DESIGN §4.4），返回规范后的名字。
func requireSessionDoc(ctx context.Context, name string) (string, error) {
	if name == port.GlobalMemoryDoc {
		return name, nil
	}
	sid, ok := port.SessionIDFrom(ctx)
	if !ok {
		return "", fmt.Errorf("无会话上下文，只能访问全局记忆 %s", port.GlobalMemoryDoc)
	}
	if name == port.SessionMemoryDoc(sid) {
		return name, nil
	}
	return "", fmt.Errorf("memory_* 只能访问全局记忆 %s 与当前会话记忆 %s",
		port.GlobalMemoryDoc, port.SessionMemoryDoc(sid))
}

// allowedDocNames 全局 + 当前会话两个名字的白名单。
func allowedDocNames(ctx context.Context) map[string]bool {
	m := map[string]bool{port.GlobalMemoryDoc: true}
	if sid, ok := port.SessionIDFrom(ctx); ok {
		m[port.SessionMemoryDoc(sid)] = true
	}
	return m
}

// okResult 构造成功结果。
func okResult(out string) tool.Result { return tool.Result{OK: true, Output: out} }

// 声明 port 接口实现（编译期护栏）。
var (
	_ port.Tool       = (*memoryList)(nil)
	_ port.Tool       = (*memoryRead)(nil)
	_ port.Tool       = (*memorySearch)(nil)
	_ port.Tool       = (*memoryWrite)(nil)
	_ port.FileTarget = (*memoryWrite)(nil)
	_ port.Tool       = (*fileRead)(nil)
	_ port.FileTarget = (*fileRead)(nil)
	_ port.Tool       = (*fileList)(nil)
	_ port.FileTarget = (*fileList)(nil)
	_ port.Tool       = (*fileSearch)(nil)
	_ port.FileTarget = (*fileSearch)(nil)
	_ port.Tool       = (*fileWrite)(nil)
	_ port.FileTarget = (*fileWrite)(nil)
	_ port.Tool       = (*fileDelete)(nil)
	_ port.FileTarget = (*fileDelete)(nil)
	_ port.Tool       = (*thinkTool)(nil)
	_ port.Tool       = (*termExec)(nil)
	_ port.Tool       = (*jobStart)(nil)
	_ port.Tool       = (*jobList)(nil)
	_ port.Tool       = (*jobStatus)(nil)
	_ port.Tool       = (*jobLogs)(nil)
	_ port.Tool       = (*jobKill)(nil)
)

// 解析辅助（Target 申报共用）：取 path 参数并规约为绝对路径。
// sandbox 仅供绝对路径校验失败时构造提示（Target 层丢弃错误，Execute 层原样上抛）。
func targetPath(call tool.Call, sandbox string) (string, bool) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return "", false
	}
	p, err := requireAbs(sandbox, a.Path)
	if err != nil {
		return "", false
	}
	return p, true
}
