package port

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// Injection 一条待注入会话的上下文（D150）：来源 + 内容分片 + 触发口径。
// 值语义；调用方经 Injector 交给内核，内核负责落地为 `Role=inject` 节点。
type Injection struct {
	// Source 注入来源（D150）：`job:<id>` / `tool:<name>` / `plugin:<name>` / `poller:<id>` / `user`。
	// 只作展示与审计（不进模型上下文）；未知前缀按普通来源处理，不设白名单。
	Source string
	// Parts 注入内容分片——与 user 同规则（text/image/audio/doc；thinking/tool 一律拒），
	// 由内核在入队前校验：坏包当场报给调用方，不让它无声落进树。
	Parts []conversation.Part
	// Trigger 是否随附起一轮响应：nil = 由内核按来源取默认（任务/轮询/手动注入默认起，
	// 工具/插件注入默认不起）；非 nil = 调用方显式覆盖。
	Trigger *bool
}

// Injector 注入端口（D150）：工具/MCP/轮询器等外部生产者把上下文交回会话的唯一入口。
// 实现方是内核（app 侧 `Session`），与 TreeView 同款反向端口。
//
// 并发（§10）：实现必须**只入队**（非阻塞），树变更仍只发生在会话 goroutine 里——
// 外部 goroutine 不得直接改树。队列已满时立即返回错误，不阻塞调用方。
type Injector interface {
	Inject(ctx context.Context, in Injection) error
}
