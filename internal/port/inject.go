package port

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// TrustLevel 注入内容的信任级（D170）。
//
// 为什么不按 Source 前缀推断：前缀是**生产者自报的字符串**，MCP 工具名与插件名都进这条路径，
// 谁能造出 `job:` 开头就谁的内容被当成宿主事实读——那是 fail-open。信任必须由调用方**显式声明**，
// 前缀继续只用于展示与审计。
type TrustLevel int

const (
	// TrustExternal 外部内容（缺省）：工具输出、插件、MCP、日志尾部。投影为 user + 数据围栏。
	TrustExternal TrustLevel = iota
	// TrustHost 宿主事实：用户自己的动作、任务状态/退出码、观察器命中。投影为 system/user 不套围栏。
	TrustHost
)

// Injection 一条待注入会话的上下文（D150）：来源 + 内容分片 + 触发口径。
// 值语义；调用方经 Injector 交给内核，内核负责落地为 `Role=inject` 节点。
type Injection struct {
	// Source 注入来源（D150）：`job:<id>` / `tool:<name>` / `plugin:<name>` / `poller:<id>` / `user`。
	// 只作展示与审计（不进模型上下文）；未知前缀按普通来源处理，不设白名单。
	Source string
	// Trust 信任级（D170）：决定装配投影角色。零值 = 外部内容（fail-closed）——
	// 生产者若忘声明，代价是内容被当成数据而不是指令，不会反过来。
	Trust TrustLevel
	// Parts 注入内容分片——与 user 同规则（text/image/audio/doc；thinking/tool 一律拒），
	// 由内核在入队前校验：坏包当场报给调用方，不让它无声落进树。
	Parts []conversation.Part
	// Trigger 是否随附起一轮响应：nil = 由内核按来源取默认（任务/观察/手动注入默认起，
	// 工具/插件注入默认不起）；非 nil = 调用方显式覆盖。
	Trigger *bool
	// Session 目标会话（D167，仅任务收尾/观察器等跨轮生产者使用）：空 = 落地时取当前会话；
	// 非空而落地时当前会话已不是它，整条丢弃——切换会话的瞬间不给别的会话塞报告。
	// 树变更仍只发生在会话 goroutine（§7.5），这里只是排空时的归属校验。
	Session conversation.ID
}

// Injector 注入端口（D150）：工具/MCP/观察器等外部生产者把上下文交回会话的唯一入口。
// 实现方是内核（app 侧 `Session`），与 TreeView 同款反向端口。
//
// 并发（§10）：实现必须**只入队**（非阻塞），树变更仍只发生在会话 goroutine 里——
// 外部 goroutine 不得直接改树。队列已满时立即返回错误，不阻塞调用方。
type Injector interface {
	Inject(ctx context.Context, in Injection) error
}

// InjectSourceLabel 注入来源的用户面措辞（D150/D166）：把来源前缀翻成「手动 / 任务 <id> /
// 观察 <id> / 工具 <名> / 插件 <名>」，未知回落原文——用户读到的是来自哪儿，不是内部键。
// 命令输出、主窗转写头部与遮罩来源行共用这一张表（措辞一致是验收点）。
func InjectSourceLabel(source string) string {
	if source == "" {
		return "未知来源"
	}
	if source == "user" {
		return "手动"
	}
	name, rest, ok := splitInjectSource(source)
	if !ok {
		return source
	}
	switch name {
	case "user": // 防御：`user:<x>` 少见形态也归「手动」（不静默吞掉余下）
		return "手动 " + rest
	case "job":
		return "任务 " + rest
	case "poller":
		return "观察 " + rest
	case "tool":
		return "工具 " + rest
	case "plugin":
		return "插件 " + rest
	case "skill":
		return "技能 " + rest
	default:
		return source
	}
}

// splitInjectSource 拆 `前缀:余下`：余下可含冒号（如 MCP 工具名），首个冒号即分界。
func splitInjectSource(source string) (name, rest string, ok bool) {
	for i := 0; i < len(source); i++ {
		if source[i] == ':' {
			return source[:i], source[i+1:], true
		}
	}
	return source, "", false
}
