package acpgate

// 连通性探测（v0.6.0 的 A1）：起进程 → initialize 握手 → 立刻收掉。
//
// 这一步只回答「这条配置能不能跑通」，不建会话、不发任务、不碰文件。设置窗把它摊给
// 用户看的正是这次握手的回执：对方是谁、说哪个版本的协议、支持哪些登录方式、申报了
// 哪些能力。在它之前，配错只有两种结局——「命令不在 PATH 上」在清单里标红，其余
// 一律等到第一次真正派活才以报错的形式出现。
//
// 三类失败要分开（用户能据此改对地方）：命令找不到、进程起不来、握手失败。前两类是
// 配置与系统的事，第三类是对方的事。

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
}

// 探测失败的三类（errors.Is 判定；界面据此分话术）。都带着原因包一层返回。
var (
	ErrProbeNotFound  = errors.New(i18n.T("acp.probe.err.not_found"))
	ErrProbeStart     = errors.New(i18n.T("acp.probe.err.start"))
	ErrProbeHandshake = errors.New(i18n.T("acp.probe.err.handshake"))
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

	proc, err := startProcess(agent.Command, agent.Args, m.workDir(port.DelegateSpec{}, agent), env)
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
	info := ProbeInfo{
		ProtocolVersion: int(init.ProtocolVersion),
		AuthMethods:     authMethodNames(init.AuthMethods),
		LoadSession:     init.AgentCapabilities.LoadSession,
		PromptImages:    init.AgentCapabilities.PromptCapabilities.Image,
		PromptAudio:     init.AgentCapabilities.PromptCapabilities.Audio,
		PromptEmbedded:  init.AgentCapabilities.PromptCapabilities.EmbeddedContext,
		McpHTTP:         init.AgentCapabilities.McpCapabilities.Http,
		McpSSE:          init.AgentCapabilities.McpCapabilities.Sse,
	}
	if init.AgentInfo != nil {
		info.Title = deref(init.AgentInfo.Title)
		info.Version = init.AgentInfo.Version
	}
	m.noteLearned(agent.Name, info.Title, info.Version) // 顺手记住：清单第二行直接有标题版本
	return info, nil
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

// SessionUpdate 探测不建会话，理论上收不到；收到了也只是丢弃（握手不该被它挡住）。
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
