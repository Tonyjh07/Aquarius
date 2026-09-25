// Package mcpgate MCP（Tier-2，D4）client → 端口投影（DESIGN §6.3，D30 go-sdk）：
//
//	tools/list|call     → port.Tool（命名 mcp:<server>:<tool>，失败经 runner 翻译 OK:false）
//	resources/list|read → port.MemoryStore 只读投影（写仍走自有记忆）
//	prompts/list|get    → 纯文本渲染（app 暴露 /mcp:<server>:<prompt> 动态命令）
//	sampling 反向调用    → 不提供能力即拒绝（D5）
//
// 本包实现 plugin.Session（消费方接口）；Secrets 经 NewDialer 由装配根注入（§9）。
package mcpgate

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// clientIdentity 宿主在 initialize 自报的身份（§6.3 版本协商由 sdk 完成，
// 不兼容在 Connect 处报因 → 拒载并报因）。
var clientIdentity = &mcp.Implementation{Name: "aquarius", Version: "0.1.0"}

// Server 一个已连接的 MCP server（plugin.Session 实现）。
type Server struct {
	name    string
	risk    tool.Risk
	cli     *mcp.Client
	cs      *mcp.ClientSession
	prompts []*mcp.Prompt // prompts/list 快照（Dial 时拉取；变化待重连生效）

	mu    sync.Mutex
	stats plugin.Stats
}

var _ plugin.Session = (*Server)(nil)

// NewDialer 产出注入 Secrets 的连接器（装配根用；Dialer 契约见 plugin.Dialer）。
func NewDialer(secrets port.Secrets) plugin.Dialer {
	return func(ctx context.Context, d plugin.Decl) (plugin.Session, error) {
		return Dial(ctx, secrets, d)
	}
}

// Dial 连接声明的 server：stdio 起子进程 / streamable-http 拨端点；
// initialize 握手与能力协商由 sdk 完成，失败原样报因（拒载）。
func Dial(ctx context.Context, secrets port.Secrets, d plugin.Decl) (*Server, error) {
	if err := d.MCPConfig.Validate(d.Name); err != nil {
		return nil, err
	}
	t, err := transportFor(ctx, secrets, d)
	if err != nil {
		return nil, err
	}
	cli := mcp.NewClient(clientIdentity, nil)
	cs, err := cli.Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("连接插件 %s: %w", d.Name, err)
	}
	s := &Server{name: d.Name, risk: d.ToolRisk(), cli: cli, cs: cs}
	// prompts 快照：尽力而为（未声明 prompts 能力的 server 合法，留空即可）。
	if list, err := cs.ListPrompts(ctx, nil); err == nil {
		s.prompts = list.Prompts
	}
	return s, nil
}

// transportFor 按声明构造传输（env/headers 的 secret: 引用经 Secrets 解析，§9）。
func transportFor(ctx context.Context, secrets port.Secrets, d plugin.Decl) (mcp.Transport, error) {
	switch d.Transport {
	case plugin.TransportStdio:
		cmd := exec.Command(d.Command, d.Args...)
		env, err := resolveRefs(ctx, secrets, d.Env)
		if err != nil {
			return nil, fmt.Errorf("插件 %s 环境变量: %w", d.Name, err)
		}
		// 子进程环境 = 父环境剔除 AQUARIUS_* + 声明项（审查修复：宿主密钥
		//（api_key 等按本项目命名约定存 AQUARIUS_*）不得随继承外泄给插件；
		// PATH/TEMP 等系统变量照常继承，npx/node 才能跑）。
		cmd.Env = childEnv(env)
		return &mcp.CommandTransport{Command: cmd}, nil
	case plugin.TransportHTTP:
		hdr, err := resolveRefs(ctx, secrets, d.Headers)
		if err != nil {
			return nil, fmt.Errorf("插件 %s 请求头: %w", d.Name, err)
		}
		return &mcp.StreamableClientTransport{
			Endpoint:   d.URL,
			HTTPClient: &http.Client{Transport: &headerTransport{headers: hdr}},
			// MVP 关闭独立 SSE 流：只做请求-响应（服务端主动通知面 v1 不消费，§6.3）。
			DisableStandaloneSSE: true,
		}, nil
	default:
		return nil, fmt.Errorf("插件 %s: 未知 transport %q", d.Name, d.Transport)
	}
}

// childEnv 构造 stdio 子进程环境：父环境剔除 AQUARIUS_*（本项目密钥命名约定——
// 宿主密钥不随继承外泄给插件，审查修复）后加声明项（secret: 已解析为明文，
// 只在子进程环境出现）。PATH/TEMP 等其余变量照常继承，保证 npx/node 等可运行。
func childEnv(declared map[string]string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+len(declared))
	for _, kv := range base {
		if i := strings.IndexByte(kv, '='); i > 0 && strings.HasPrefix(kv[:i], "AQUARIUS_") {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range declared {
		out = append(out, k+"="+v)
	}
	return out
}

// resolveRefs 解析 map 值中的 "secret:<环境变量名>" 引用（§9：真实值只经 port.Secrets
// 按名取用，不落配置/日志）；非引用值原样透传。
func resolveRefs(ctx context.Context, secrets port.Secrets, m map[string]string) (map[string]string, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if name, ok := strings.CutPrefix(v, "secret:"); ok {
			if secrets == nil {
				return nil, fmt.Errorf("%s 需要 Secrets 端口但未配置", name)
			}
			val, err := secrets.Get(ctx, name)
			if err != nil {
				return nil, fmt.Errorf("取密钥 %s: %w", name, err)
			}
			out[k] = val
			continue
		}
		out[k] = v
	}
	return out, nil
}

// headerTransport 在请求上附加声明的 headers（streamable-http 认证等）。
type headerTransport struct {
	headers map[string]string
}

func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	for k, v := range t.headers {
		clone.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(clone)
}

// Wait 阻塞至连接终止（宿主崩溃检测入口）。
func (s *Server) Wait() error { return s.cs.Wait() }

// Close 优雅关闭（sdk 终止 stdio 子进程/断开 HTTP）。
func (s *Server) Close() error { return s.cs.Close() }

// Memory resources 只读投影（§6.3）。
func (s *Server) Memory() port.MemoryStore { return resourceStore{s: s} }

// Stats 调用统计快照（§6.4 #5）。
func (s *Server) Stats() plugin.Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// recordCall 记一次工具调用（耗时与成败，供 /plugin list）。
func (s *Server) recordCall(d time.Duration, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.Calls++
	if !ok {
		s.stats.Errors++
	}
	s.stats.LastMS = d.Milliseconds()
}
