package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// terminalSuspend 可选前端能力：交出/收回终端（TUI 前端实现，/memory 系统编辑器
// 等全屏外部程序用——审查修复：事件循环仍在读同一 stdin，不释放会互相踩踏；
// repl 无终端态需要管理，不实现即跳过）。
type terminalSuspend interface {
	Suspend() error
	Resume() error
}

// sessionTree 会话树只读视图的装配侧代理（D80/§7.5，前置 A）：Session 构造晚于 UI，
// 故经原子槽间接取用；零开销包装，Branches/Tail 直接转发（快照读本身无锁，§15.5）。
type sessionTree struct{ p *atomic.Pointer[app.Session] }

var _ port.TreeView = sessionTree{}

func (t sessionTree) Branches(id conversation.MessageID) (port.BranchInfo, bool) {
	s := t.p.Load()
	if s == nil {
		return port.BranchInfo{}, false
	}
	return s.Branches(id)
}

func (t sessionTree) Tail(id conversation.MessageID) (conversation.MessageID, bool) {
	s := t.p.Load()
	if s == nil {
		return "", false
	}
	return s.Tail(id)
}

// sessionCommands 命令清单只读视图的装配侧代理（D103/S2b-1）：Session 构造晚于 UI，
// 故经原子槽间接取用；未就绪返回空清单（补全浮层不出现）。
type sessionCommands struct{ p *atomic.Pointer[app.Session] }

var _ port.CommandCatalog = sessionCommands{}

func (c sessionCommands) Commands() []port.CommandInfo {
	s := c.p.Load()
	if s == nil {
		return nil
	}
	return s.Commands()
}

// envSecrets port.Secrets 的内置实现：按名读环境变量（矩阵 DESIGN §5.10）。
// 返回值只在进程内传递，不落日志、不进会话树。
type envSecrets struct{}

var _ port.Secrets = envSecrets{}

// levelHolder 权限等级活状态（D22 执行接入）：/permission 写回成功后 Set，
// ToolRunner 经 Get 读取。atomic.Value 存取——TUI 状态行回调（事件循环 goroutine）
// 与 REPL 主 goroutine 并发读写（D33 引入第二 goroutine 打破"单 goroutine"前提，
// 审查修复，与 agentPtr 同口径）。
type levelHolder struct{ v atomic.Value } // perm.Level

// newLevelHolder 构造并写入初值。
func newLevelHolder(l perm.Level) *levelHolder {
	h := &levelHolder{}
	h.v.Store(l)
	return h
}

func (h *levelHolder) Get() perm.Level {
	v, _ := h.v.Load().(perm.Level)
	return v
}

func (h *levelHolder) Set(l perm.Level) { h.v.Store(l) }

func (envSecrets) Get(_ context.Context, name string) (string, error) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return "", fmt.Errorf("环境变量 %s 未设置（config 的 api_key 引用它）", name)
	}
	return v, nil
}

// systemClock port.Clock 的内置实现：系统时钟。
type systemClock struct{}

var _ port.Clock = systemClock{}

func (systemClock) Now() time.Time { return time.Now() }

// yesConfirmer port.Confirmer 的内置实现：-yes 下所有确认一律同意（§5.10 矩阵）。
type yesConfirmer struct{}

var _ port.Confirmer = yesConfirmer{}

func (yesConfirmer) Confirm(context.Context, string) (port.ConfirmAnswer, error) {
	return port.ConfirmAnswer{Allow: true}, nil
}

// systemIDGen port.IDGen 的内置实现：复用 domain 的进程内单调 ULID。
type systemIDGen struct{}

var _ port.IDGen = systemIDGen{}

func (systemIDGen) ConversationID() conversation.ID   { return conversation.NewID() }
func (systemIDGen) MessageID() conversation.MessageID { return conversation.NewMessageID() }
func (systemIDGen) CallID() tool.CallID               { return tool.CallID(conversation.NewMessageID()) }
