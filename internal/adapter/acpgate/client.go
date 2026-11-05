package acpgate

import (
	"context"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// client ACP 客户端的九个方法（`acp.Client`）。本版只真做两件事：收过程通知、
// 回落权限请求；`fs/*` 与 `terminal/*` 一律 -32601——不申报就不承诺（§5.18）。
type client struct {
	m *Manager
	r *run
}

var _ acp.Client = (*client)(nil)

// SessionUpdate 过程通知：数工具调用、记它报过的改动文件、攒最终正文。
//
// 子代理的转录、思考、计划表都不进会话树（§5.18）：这里是**收报告**，不是转播。
func (c *client) SessionUpdate(ctx context.Context, n acp.SessionNotification) error {
	// 放在最前面：下面按 sid 过滤的分支也会 return，那些通知同样带着排空信号。
	c.r.observeNotifyCtx(ctx)
	if sid := c.r.sessionID(); sid != "" && string(n.SessionId) != sid {
		return nil // 不是本次会话的通知：忽略（多路并存时不串味）
	}
	u := n.Update
	switch {
	case u.AgentMessageChunk != nil:
		c.r.appendText(u.AgentMessageChunk.Content)
	case u.ToolCall != nil:
		c.r.addToolCall()
		c.r.addLocations(u.ToolCall.Locations)
	case u.ToolCallUpdate != nil:
		c.r.addLocations(u.ToolCallUpdate.Locations)
	}
	return nil
}

// RequestPermission 它动手前反问：回落到 port.Confirmer（§9/§5.18）。
//
// 无人应答的两种情形当场拒绝并计数：无人值守（Confirmer 已被装饰成一律拒绝），
// 以及用户不在发起这次转交的会话——不让转交卡在等确认上。
func (c *client) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	r := c.r
	cancelled := acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}
	if r.stopKind() != stopNone || ctx.Err() != nil {
		return cancelled, nil
	}

	var denyReason string
	switch {
	case c.m.opts.CurrentSession != nil && r.session != "" && c.m.opts.CurrentSession() != "" &&
		c.m.opts.CurrentSession() != r.session:
		denyReason = i18n.T("acp.deny.not_in_session")
	case c.m.opts.Confirmer == nil:
		denyReason = i18n.T("acp.deny.no_confirmer")
	}
	allow := false
	if denyReason == "" {
		ans, err := c.m.opts.Confirmer.Confirm(ctx, permissionPrompt(r.agent, p))
		switch {
		case err != nil:
			if r.stopKind() != stopNone || ctx.Err() != nil {
				return cancelled, nil
			}
			denyReason = err.Error()
		case ans.Allow:
			allow = true
		default:
			denyReason = strings.TrimSpace(ans.Reason)
			if denyReason == "" {
				denyReason = i18n.T("acp.deny.user_refused")
			}
		}
	}
	if !allow {
		r.addDenied(denyReason)
		if opt, ok := pickOption(p.Options, false); ok {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(opt)}, nil
		}
		return cancelled, nil
	}
	opt, ok := pickOption(p.Options, true)
	if !ok {
		// 没有「允许」类选项：只能拒绝，并说明是对方没给允许项（不是用户拒的）。
		r.addDenied("the agent offered no allow option")
		if rej, ok := pickOption(p.Options, false); ok {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(rej)}, nil
		}
		return cancelled, nil
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(opt)}, nil
}

// pickOption 选一个同类选项：允许优先 allow_once、拒绝优先 reject_once，没有就取第一个同类。
func pickOption(opts []acp.PermissionOption, allow bool) (acp.PermissionOptionId, bool) {
	pref := acp.PermissionOptionKindRejectOnce
	prefix := "reject"
	if allow {
		pref = acp.PermissionOptionKindAllowOnce
		prefix = "allow"
	}
	for _, o := range opts {
		if o.Kind == pref {
			return o.OptionId, true
		}
	}
	for _, o := range opts {
		if strings.HasPrefix(string(o.Kind), prefix) {
			return o.OptionId, true
		}
	}
	return "", false
}

// permissionPrompt 权限确认的用户面文案：**说清边界**——批准一次转交 = 批准那个程序
// 在它自己判断下动手，权限核对它只覆盖它愿意问的那部分（§5.18/§9）。
func permissionPrompt(a Agent, p acp.RequestPermissionRequest) string {
	var b strings.Builder
	b.WriteString(i18n.Tf("acp.prompt.head", "agent", a.Name))
	if t := p.ToolCall.Title; t != nil && strings.TrimSpace(*t) != "" {
		b.WriteString("\n  " + strings.TrimSpace(*t))
	}
	if s := toolCallText(p.ToolCall); s != "" {
		b.WriteString("\n  " + s)
	}
	b.WriteString("\n\n" + i18n.T("acp.prompt.foot"))
	return b.String()
}

// toolCallText 取工具调用的第一段文本内容（标题之外能给用户看的细节）。
func toolCallText(tc acp.ToolCallUpdate) string {
	for _, ct := range tc.Content {
		if ct.Content != nil && ct.Content.Content.Text != nil {
			s := strings.TrimSpace(ct.Content.Content.Text.Text)
			if s != "" {
				return firstLine(s, 300)
			}
		}
	}
	return ""
}

// firstLine 取首行并截断（确认框里放不下长内容）。
func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

// ---- 不申报的能力：一律 -32601 ----

func (c *client) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound("fs/read_text_file")
}

func (c *client) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound("fs/write_text_file")
}

func (c *client) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound("terminal/create")
}

func (c *client) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound("terminal/kill")
}

func (c *client) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound("terminal/output")
}

func (c *client) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound("terminal/release")
}

func (c *client) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound("terminal/wait_for_exit")
}
