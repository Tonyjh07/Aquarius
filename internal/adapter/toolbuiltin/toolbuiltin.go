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

// Deps 内置工具的装配依赖（全部来自端口，内置不享特权，D13）。
type Deps struct {
	Memory  port.MemoryStore     // 记忆存储；nil = memory_* 报未配置
	PathOf  PathOf               // 记忆文档名 → 磁盘路径（FileTarget 申报）
	Jobs    port.JobManager      // 任务端口（term_exec / job_*）
	Sandbox string               // 特权沙盒目录（相对路径报错时给可行动的替代提示）
	Blobs   port.AttachmentStore // 附件库：file_read 读图片时落盘（nil = 图片不可用）
	Vision  bool                 // 当前模型是否接受图片输入（D169/D173：装配根按整链 caps 取与；false 必须明确报因）
	Capture port.Capture         // 屏幕捕获（D178；nil = 截图工具明确报「本构建未提供」）
	Asker   port.Asker           // 提问通道（D184；nil = ask_user 明确报「本构建未提供」）
	Watches port.WatchManager    // 观察器（D190）：watch_* 工具；nil = 明确报「本构建未提供」。
}

// New 返回全部内置工具实例（main 装配进 ToolRunner；实例无状态）。
// 缺依赖时对应工具执行会明确报"本构建未提供该能力"，而不是 panic。
func New(d Deps) []port.Tool {
	return allTools(d)
}

// allTools 内置工具清单（装配序 = Spec 注册序 = Names 序；S6/D143 词表对照的
// 单一事实源——New 与 Names/DisplayName 不得各自维护一份）。
func allTools(d Deps) []port.Tool {
	return []port.Tool{
		&memoryList{mem: d.Memory},
		&memoryRead{mem: d.Memory},
		&memorySearch{mem: d.Memory},
		&memoryWrite{mem: d.Memory, pathOf: d.PathOf},
		&fileRead{sandbox: d.Sandbox, blobs: d.Blobs, vision: d.Vision},
		&fileList{sandbox: d.Sandbox},
		&fileSearch{sandbox: d.Sandbox},
		&fileEdit{sandbox: d.Sandbox},
		&fileWrite{sandbox: d.Sandbox},
		&fileDelete{sandbox: d.Sandbox},
		&thinkTool{},
		&sleepTool{},
		&termExec{jobs: d.Jobs},
		&jobStart{jobs: d.Jobs},
		&jobWait{jobs: d.Jobs},
		&jobList{jobs: d.Jobs},
		&jobStatus{jobs: d.Jobs},
		&jobLogs{jobs: d.Jobs},
		&jobKill{jobs: d.Jobs},
		&windowList{capture: d.Capture},
		&screenCapture{capture: d.Capture, blobs: d.Blobs, vision: d.Vision},
		&watchStart{watches: d.Watches},
		&watchList{watches: d.Watches},
		&watchStop{watches: d.Watches},
		&askUser{asker: d.Asker},
	}
}

// Names 内置工具名清单（装配序；S6/D143 工具管理页清单的事实源）。
func Names() []string {
	all := allTools(Deps{})
	out := make([]string, 0, len(all))
	for _, t := range all {
		out = append(out, t.Spec().Name)
	}
	return out
}

// displayNames 内置工具中文词表（S6/D143 工具管理页展示；仅 UI 文案，非契约）。
var displayNames = map[string]string{
	"memory_list":    "记忆索引",
	"memory_read":    "读取记忆",
	"memory_search":  "检索记忆",
	"memory_write":   "写入记忆",
	"file_read":      "读取文件",
	"file_list":      "列出目录",
	"file_search":    "检索文件",
	"file_edit":      "编辑文件",
	"file_write":     "写入文件",
	"file_delete":    "删除文件",
	"think":          "思考草稿",
	"sleep":          "等待",
	"term_exec":      "执行命令",
	"job_start":      "启动任务",
	"job_wait":       "等待任务",
	"job_list":       "任务列表",
	"job_status":     "任务状态",
	"job_logs":       "任务日志",
	"job_kill":       "结束任务",
	"window_list":    "窗口列表",
	"screen_capture": "屏幕截图",
	"watch_start":    "开始观察",
	"watch_list":     "观察列表",
	"watch_stop":     "停止观察",
	"ask_user":       "提问",
}

// DisplayName 内置工具中文词表查询（未收录 = 原名字面）。
func DisplayName(name string) string {
	if s, ok := displayNames[name]; ok {
		return s
	}
	return name
}

// requireJobs 任务类工具的管理器校验（未装配时明确报错，不 panic）。
func requireJobs(jobs port.JobManager, name string) (port.JobManager, error) {
	if jobs == nil {
		return nil, fmt.Errorf("%s: background tasks are not available in this build", name)
	}
	return jobs, nil
}

// decodeArgs 解析工具调用的 JSON 参数；空参数按空对象。
func decodeArgs(call tool.Call, v any) error {
	if len(call.Args) == 0 {
		return nil
	}
	if err := json.Unmarshal(call.Args, v); err != nil {
		return fmt.Errorf("arguments are not a valid JSON object: %w", err)
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
		return "", fmt.Errorf("no session context; only the global memory %s is accessible", port.GlobalMemoryDoc)
	}
	if name == port.SessionMemoryDoc(sid) {
		return name, nil
	}
	return "", fmt.Errorf("memory_* can only access the global memory %s and the current session's memory %s",
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

// 空态三档（D171）：模型靠工具名就能区分语义，不需要「no hits / no matches /
// no memory documents yet」这类同义分叉。
const (
	// emptyEntries 列举类工具没有条目（目录为空、没有记忆文档、没有后台任务）。
	emptyEntries = "(no entries)"
	// emptyMatches 检索类工具没有命中（文件名搜索、记忆关键词检索）。
	emptyMatches = "(no matches)"
)

// 声明 port 接口实现（编译期护栏）。
var (
	_ port.Tool        = (*memoryList)(nil)
	_ port.Tool        = (*memoryRead)(nil)
	_ port.Tool        = (*memorySearch)(nil)
	_ port.Tool        = (*memoryWrite)(nil)
	_ port.FileTarget  = (*memoryWrite)(nil)
	_ port.Tool        = (*fileRead)(nil)
	_ port.FileTarget  = (*fileRead)(nil)
	_ port.Tool        = (*fileList)(nil)
	_ port.FileTarget  = (*fileList)(nil)
	_ port.Tool        = (*fileSearch)(nil)
	_ port.FileTarget  = (*fileSearch)(nil)
	_ port.Tool        = (*fileEdit)(nil)
	_ port.FileTarget  = (*fileEdit)(nil)
	_ port.Tool        = (*fileWrite)(nil)
	_ port.FileTarget  = (*fileWrite)(nil)
	_ port.Tool        = (*fileDelete)(nil)
	_ port.FileTarget  = (*fileDelete)(nil)
	_ port.Tool        = (*thinkTool)(nil)
	_ port.Tool        = (*sleepTool)(nil)
	_ port.Tool        = (*termExec)(nil)
	_ port.Tool        = (*jobStart)(nil)
	_ port.Tool        = (*jobList)(nil)
	_ port.Tool        = (*jobStatus)(nil)
	_ port.Tool        = (*jobLogs)(nil)
	_ port.Tool        = (*jobKill)(nil)
	_ port.Tool        = (*windowList)(nil)
	_ port.Tool        = (*screenCapture)(nil)
	_ port.Tool        = (*askUser)(nil)
	_ port.Interactive = (*askUser)(nil)
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
