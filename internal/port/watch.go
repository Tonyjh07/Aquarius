package port

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// WatchKind 观察器的探针类型（D190）：模型声明看什么，内核按节奏取样。
type WatchKind string

const (
	WatchCommand WatchKind = "command" // 跑一条命令看输出
	WatchFile    WatchKind = "file"    // 看一个文件的内容（不存在 = 空）
	WatchHTTP    WatchKind = "http"    // GET 一个地址看响应
)

// WatchNotify 命中后的行为（D190）。
type WatchNotify string

const (
	// WatchNotifyOnce 命中即报一次并结束观察（缺省）。等的是「某件事发生」。
	WatchNotifyOnce WatchNotify = "match"
	// WatchNotifyChange 每次**重新**命中都报（一直命中着不重复），直到期限或次数用完。
	// 等的是「又变成这样了」。
	WatchNotifyChange WatchNotify = "change"
)

// WatchProbe 一次探针取样描述。
type WatchProbe struct {
	Kind    WatchKind
	Target  string // 命令行文本 / 文件路径 / URL
	WorkDir string // 命令的工作目录（其余类型忽略）；空 = 当前进程目录
}

// WatchProbeResult 一次取样结果。
//
// 只有**取不到**（进程起不来、连接失败、路径非法）才返回 error。非零退出码、非 2xx
// 都是「取到了」，由判据去决定它算不算命中——探针不该替模型判断好坏。
type WatchProbeResult struct {
	Status int    // HTTP 状态码 / 命令退出码；文件恒 0
	Output string // 取样正文（已按上限裁剪）
}

// WatchProber 探针端口（D190）：观察器按模型声明的节奏调用它。
//
// 实现必须尊重 ctx（期限/取消），且一次取样有上限——模型可能让它读一个很大的文件
// 或一个不会结束的地址。
type WatchProber interface {
	Probe(ctx context.Context, spec WatchProbe) (WatchProbeResult, error)
}

// WatchID 观察器标识（进程内自增，不持久化）。
type WatchID string

// WatchSpec 一条观察器的声明（D190）：看什么、怎么算命中、多久看一次、什么时候放弃。
type WatchSpec struct {
	Kind      WatchKind
	Target    string
	Match     string // 正则；空 = 用取样内容的哈希变化当判据
	Notify    WatchNotify
	Interval  time.Duration // 固定间隔，不做退避
	MaxChecks int
	Deadline  time.Duration
	WorkDir   string
}

// WatchState 观察器的运行态快照（只读；供 watch_list 与 /watch 展示）。
type WatchState struct {
	ID        WatchID
	Spec      WatchSpec
	Checks    int // 已完成取样次数
	StartedAt time.Time
	EndsAt    time.Time // 期限；零值 = 不限（仅测试/内部装配）
}

// WatchManager 观察器端口（D190）：模型经 watch_* 工具声明，用户经 /watch 查/停。
//
// 与 JobManager 分开：任务有终态（完成事件靠 Wait），观察器等的是**外部不确定状态**，
// 只能按模型给的节奏去看，`poller:` 前缀就是它的注入来源。
type WatchManager interface {
	Start(ctx context.Context, spec WatchSpec) (WatchState, error)
	List(ctx context.Context) ([]WatchState, error)
	Stop(ctx context.Context, id WatchID) error
}

// ResolveWatchID 按标识解析观察器（精确优先，其次唯一前缀），口径与任务一致。
func ResolveWatchID(list []WatchState, arg string) (WatchID, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", errors.New("missing watch id")
	}
	ids := make([]WatchID, len(list))
	for i, w := range list {
		ids[i] = w.ID
	}
	id, hits, ok := MatchPrefix(ids, arg)
	switch {
	case ok:
		return id, nil
	case len(hits) == 0:
		return "", fmt.Errorf("no watch %q", arg)
	default:
		return "", fmt.Errorf("ambiguous watch id %q (matches %s); use a longer prefix", arg, strings.Join(hits, ", "))
	}
}
