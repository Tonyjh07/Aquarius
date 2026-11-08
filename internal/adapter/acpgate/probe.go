package acpgate

// 连通性探测（v0.6.0 的 A1）：起进程 → initialize 握手 → 建一次会话 → 立刻收掉。
//
// 这次探测回答「这条配置能不能跑通」。设置窗把它的回执摊给用户看：对方是谁、说
// 哪个版本的协议、支持哪些登录方式、申报了哪些能力。会话是顺带建的——能选哪些模型
// 只在 `session/new` 的响应里，而这正是用户填 `options` 时最缺的东西（D287）；建完
// 不发任务、不碰文件、立刻收掉。
//
// 在这之前，配错只有两种结局——「命令不在 PATH 上」在清单里标红，其余一律等到第一次
// 真正派活才以报错的形式出现。
//
// 四类失败要分开（用户能据此改对地方）：命令找不到、进程起不来、握手失败、建不了会话。
// 前两类是配置与系统的事，后两类是对方的事。「没登录」落在最后一类：混进「握手失败」，
// 用户会去查命令和参数，而真正的原因在登录。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ProbeValue 一个可填的配置值。Value 才是要写进配置的那个；Name 是对方界面上显示的名字，
// 两者经常不同——kimi 显示 `trae/deepseek-v4.1-flash`，而要填的是 `gw/...`。
type ProbeValue struct {
	Value string
	Name  string
}

// ProbeOption 探测回执里的一项会话配置：该项叫什么、当前是什么、能填哪些。
type ProbeOption struct {
	ID       string
	Category string // model / mode / thought_level；对方不给就空
	Current  string
	Values   []ProbeValue
}

// ProbeInfo 一次探测的回执（设置窗直接摊开显示这几项）。
type ProbeInfo struct {
	ProtocolVersion int
	Title           string // agentInfo.title（对方可以不报）
	Version         string
	AuthMethods     []string // 登录方式的名字（id 与 name 都给，展示用 name）
	LoadSession     bool
	PromptImages    bool
	PromptAudio     bool
	PromptEmbedded  bool
	McpHTTP         bool
	McpSSE          bool
	// Options 会话配置项（D287）：用户在 `delegate.agents[].options` 里能填什么、
	// 当前值是什么。这些只在建会话的响应里，探测顺带取回来一次。
	Options []ProbeOption
}

// 探测失败的四类（errors.Is 判定；界面据此分话术）。都带着原因包一层返回。
//
// 建会话单列一类：没登录的代理在这一步回 auth required，而握手是成功的——混进
// 「握手失败」会把用户指向错误的地方。
var (
	ErrProbeNotFound  = errors.New(i18n.T("acp.probe.err.not_found"))
	ErrProbeStart     = errors.New(i18n.T("acp.probe.err.start"))
	ErrProbeHandshake = errors.New(i18n.T("acp.probe.err.handshake"))
	ErrProbeSession   = errors.New(i18n.T("acp.probe.err.session"))
)

// ProbeTimeout 缺省探测总闸。握手是本地进程间的一次往返，慢到十秒以上就是对方有问题。
const ProbeTimeout = 15 * time.Second

// Probe 探测一个代理：起进程、握手、关掉。名字不存在报错；停用的也允许探测——
// 它是「这条配置跑不跑得通」的诊断，不是「能不能派活」的承诺。
func (m *Manager) Probe(ctx context.Context, name string) (ProbeInfo, error) {
	var agent Agent
	found := false
	for _, a := range m.opts.Agents {
		if a.Name == name {
			agent, found = a, true
			break
		}
	}
	if !found {
		return ProbeInfo{}, fmt.Errorf("%s", i18n.Tf("cmd.delegate.err.agent_missing",
			"name", fmt.Sprintf("%q", name)))
	}
	if _, err := m.opts.LookPath(agent.Command); err != nil {
		return ProbeInfo{}, fmt.Errorf("%w: %s", ErrProbeNotFound, agent.Command)
	}
	env, err := m.resolveEnv(agent.Env)
	if err != nil {
		return ProbeInfo{}, fmt.Errorf("%w: %v", ErrProbeStart, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	wd := m.workDir(port.DelegateSpec{}, agent)
	proc, err := startProcess(agent.Command, agent.Args, wd, env)
	if err != nil {
		return ProbeInfo{}, fmt.Errorf("%w: %v", ErrProbeStart, err)
	}
	defer proc.terminate(m.opts.Grace)

	conn := acp.NewClientSideConnection(probeClient{}, proc.stdin, proc.stdout)
	conn.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	// 对端提前退出（崩溃、命令认不出来）时让阻塞中的 Initialize 尽快返回：
	// 只等 ctx 超时会把「进程立刻死了」报成「十五秒没响应」。
	go func() {
		select {
		case <-conn.Done():
			cancel()
		case <-ctx.Done():
		}
	}()

	init, err := conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: 1})
	if err != nil {
		return ProbeInfo{}, fmt.Errorf("%w: %v%s", ErrProbeHandshake, err, stderrTailOf(proc))
	}
	// 建一次会话再收掉：会话配置项（能选哪些模型）只在这里拿得到，而用户填 options
	// 时最缺的正是这份清单。未登录的代理也在这里才暴露出来。
	ns, err := conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        wd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		return ProbeInfo{}, fmt.Errorf("%w: %v%s", ErrProbeSession, err, stderrTailOf(proc))
	}
	info := ProbeInfo{
		ProtocolVersion: int(init.ProtocolVersion),
		AuthMethods:     authMethodNames(init.AuthMethods),
		LoadSession:     init.AgentCapabilities.LoadSession,
		PromptImages:    init.AgentCapabilities.PromptCapabilities.Image,
		PromptAudio:     init.AgentCapabilities.PromptCapabilities.Audio,
		PromptEmbedded:  init.AgentCapabilities.PromptCapabilities.EmbeddedContext,
		McpHTTP:         init.AgentCapabilities.McpCapabilities.Http,
		McpSSE:          init.AgentCapabilities.McpCapabilities.Sse,
		Options:         probeOptions(ns.ConfigOptions),
	}
	if init.AgentInfo != nil {
		info.Title = deref(init.AgentInfo.Title)
		info.Version = init.AgentInfo.Version
	}
	m.noteLearned(agent.Name, info.Title, info.Version) // 顺手记住：清单第二行直接有标题版本
	return info, nil
}

// probeOptions 把会话配置项摊成设置窗能显示的形状（select 才列：开关形状没有候选）。
func probeOptions(snap []acp.SessionConfigOption) []ProbeOption {
	out := make([]ProbeOption, 0, len(snap))
	for i := range snap {
		o := snap[i].Select
		if o == nil {
			continue
		}
		po := ProbeOption{ID: string(o.Id), Current: string(o.CurrentValue)}
		if o.Category != nil {
			po.Category = string(*o.Category)
		}
		if u := o.Options.Ungrouped; u != nil {
			for _, v := range *u {
				po.Values = append(po.Values, ProbeValue{Value: string(v.Value), Name: v.Name})
			}
		}
		if g := o.Options.Grouped; g != nil {
			for _, grp := range *g {
				for _, v := range grp.Options {
					po.Values = append(po.Values, ProbeValue{Value: string(v.Value), Name: v.Name})
				}
			}
		}
		out = append(out, po)
	}
	return out
}

// authMethodNames 登录方式的名字（优先 name，没有再退回 id）。SDK 把 AuthMethod 做成
// 三支的联合体，三支各自带 id/name，这里统一取一遍。
func authMethodNames(methods []acp.AuthMethod) []string {
	out := make([]string, 0, len(methods))
	for _, m := range methods {
		id, name := authMethodLabel(m)
		switch {
		case strings.TrimSpace(name) != "":
			out = append(out, strings.TrimSpace(name))
		case id != "":
			out = append(out, id)
		}
	}
	return out
}

// authMethodLabel 取一个登录方式的 id 与名字（三支联合体共用的两个字段）。
func authMethodLabel(m acp.AuthMethod) (id, name string) {
	switch {
	case m.Agent != nil:
		return m.Agent.Id, m.Agent.Name
	case m.EnvVar != nil:
		return m.EnvVar.Id, m.EnvVar.Name
	case m.Terminal != nil:
		return m.Terminal.Id, m.Terminal.Name
	}
	return "", ""
}

// stderrTailOf 探测失败时带上对方 stderr 的尾巴：握手失败最常见的原因（参数不对、
// 版本不兼容、缺依赖）都只写在它自己的 stderr 里。没有内容就返回空串。
func stderrTailOf(p *process) string {
	if p == nil {
		return ""
	}
	p.waitStderr(time.Second)
	tail := strings.TrimSpace(p.stderrTail())
	if tail == "" {
		return ""
	}
	return ": " + tail
}

// probeClient 探测用的客户端：什么也不做。
//
// 握手阶段对方不该发任何请求；真发了就是它在探我们申报了哪些能力——这时按「没申报、
// 不接受」回绝，正好与 acpgate 自己的口径一致（不申报就不承诺）。
type probeClient struct{}

var errProbeNoCaps = errors.New("the probe client advertises no client capabilities")

func (probeClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errProbeNoCaps
}

func (probeClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errProbeNoCaps
}

func (probeClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
}

// SessionUpdate 探测里建会话只为了读配置项，收不到通知；收到了也只是丢弃。
func (probeClient) SessionUpdate(context.Context, acp.SessionNotification) error { return nil }

func (probeClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errProbeNoCaps
}

func (probeClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errProbeNoCaps
}

func (probeClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errProbeNoCaps
}

func (probeClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errProbeNoCaps
}

func (probeClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errProbeNoCaps
}
