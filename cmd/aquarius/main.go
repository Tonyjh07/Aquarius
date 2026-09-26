// Command aquarius 是 Aquarius 的单二进制入口：
// 装配 config → Secrets/LLM/Store 端口 → 会话 → REPL（DESIGN §8 布局 / §12 M0 验收）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/ingestclip"
	"github.com/Tonyjh07/Aquarius/internal/adapter/ingestfile"
	"github.com/Tonyjh07/Aquarius/internal/adapter/jobproc"
	"github.com/Tonyjh07/Aquarius/internal/adapter/llm"
	"github.com/Tonyjh07/Aquarius/internal/adapter/mcpgate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/memoryfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/notify"
	"github.com/Tonyjh07/Aquarius/internal/adapter/repl"
	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolbuiltin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uitui"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// terminalSuspend 可选前端能力：交出/收回终端（TUI 前端实现，/memory 系统编辑器
// 等全屏外部程序用——审查修复：事件循环仍在读同一 stdin，不释放会互相踩踏；
// repl 无终端态需要管理，不实现即跳过）。
type terminalSuspend interface {
	Suspend() error
	Resume() error
}

// cfgWriteMu 串行化全部 config 写回（/permission、/model、/think、/effort、
// /plugin 与 llm 的 unsupported_params 记录可能并发——读-改-写须互斥防丢更新）。
var cfgWriteMu sync.Mutex

// persistConfig 通用 config 键改写：读入 → mutate 只动目标键（map 级重排保留
// 其余键与注释性空白）→ 唯一临时文件 + 原子换入（覆盖沿用既有权限位）。
// /permission、/model、/think、/effort、/plugin 与 unsupported_params 共用。
func persistConfig(cfgPath string, mutate func(generic map[string]any)) error {
	cfgWriteMu.Lock()
	defer cfgWriteMu.Unlock()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		return fmt.Errorf("解析 %s: %w", cfgPath, err)
	}
	mutate(generic)
	out, err := json.MarshalIndent(generic, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 config: %w", err)
	}
	if err := atomicfile.WriteFile(cfgPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", cfgPath, err)
	}
	return nil
}

// modelSection 取/建 config 的 model 段（各写回闭包共用）。
func modelSection(generic map[string]any) map[string]any {
	model, _ := generic["model"].(map[string]any)
	if model == nil {
		model = map[string]any{}
		generic["model"] = model
	}
	return model
}

// dropTool 摘除指定名字的工具（model.think_tool=false 时隐藏 think，D34）。
func dropTool(tools []port.Tool, name string) []port.Tool {
	out := make([]port.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Spec().Name != name {
			out = append(out, t)
		}
	}
	return out
}

// minOutputReserve 硬保底截断为本轮输出预留的最小 token 数（DESIGN §7.1）。
const minOutputReserve = 1024

// uiFrontend 装配根消费的前端面：repl 与 TUI 同权实现（D33 换壳不换核）。
type uiFrontend interface {
	port.Presenter
	port.Prompter
	port.Confirmer
	// Say 输出一行会话文本（命令输出、启动提示）。
	Say(text string)
	// Prompt 输入提示（TUI 输入行常驻，等价 no-op）。
	Prompt()
	// SetInterrupt 注入 Ctrl+C 行为（repl = no-op 走 os.Interrupt；TUI = 取消当前 Turn）。
	SetInterrupt(fn func())
	// Close 前端收尾（TUI 恢复终端；repl no-op）。
	Close() error
}

// run 程序主体（标准流可注入，便于端到端回放测试）。返回进程退出码。
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("aquarius", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "", "数据目录（默认 ~/.aquarius）")
	modelName := flags.String("model", "", "覆盖 model.name")
	baseURL := flags.String("base-url", "", "覆盖 model.base_url")
	autoYes := flags.Bool("yes", false, "跳过逐次确认（等价对每个确认回答 y；危险操作慎用）")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	// 数据目录（三级覆盖里的 flag 先于 env/config，DESIGN §8）。
	dir := *dataDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(stderr, "无法确定家目录: %v\n", err)
			return 1
		}
		dir = filepath.Join(home, ".aquarius")
	}
	// -data 允许相对路径，启动即锚定为绝对：对外展示的路径（沙盒提示、配置
	// 路径等）必须可直接复制使用，且不受后续"相对路径再次校验"拒绝。
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "创建数据目录 %s: %v\n", dir, err)
		return 1
	}
	// 特权目录（D22）：<dataDir>/sandbox，权限矩阵的免确认 rw 格。
	sandboxDir := filepath.Join(dir, "sandbox")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "创建特权目录 %s: %v\n", sandboxDir, err)
		return 1
	}

	// 配置：不存在 = 首次运行，写模板并指引填写后重跑。
	cfgPath := filepath.Join(dir, "config.json")
	cfg, err := loadConfig(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		if werr := writeDefaultConfig(cfgPath); werr != nil {
			fmt.Fprintf(stderr, "生成配置失败: %v\n", werr)
			return 1
		}
		fmt.Fprintf(stdout, "已生成配置: %s\n请编辑 model.name / model.base_url，并设置密钥环境变量后重新运行。\n"+
			"  api_key 推荐引用格式 secret:<环境变量名>（例如 secret:AQUARIUS_OPENAI_KEY）；也可直接填明文（启动会警告，D35）\n",
			cfgPath)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "读取配置失败: %v\n", err)
		return 1
	}

	// 环境变量与 flag 覆盖（flags > 环境变量 > config）。
	if v := os.Getenv("AQUARIUS_MODEL"); v != "" {
		cfg.Model.Name = v
	}
	if v := os.Getenv("AQUARIUS_BASE_URL"); v != "" {
		cfg.Model.BaseURL = v
	}
	if *modelName != "" {
		cfg.Model.Name = *modelName
	}
	if *baseURL != "" {
		cfg.Model.BaseURL = *baseURL
	}
	if cfg.Model.Name == "" || cfg.Model.BaseURL == "" {
		fmt.Fprintf(stderr, "model.name 与 model.base_url 不可为空\n")
		return 1
	}
	// 思考参数启动校验（D34）：effort 档位与 /effort 命令共用 app.ParseEffort 口径。
	if raw := strings.TrimSpace(cfg.Model.ReasoningEffort); raw != "" {
		effort, err := app.ParseEffort(raw)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
		cfg.Model.ReasoningEffort = effort
	}
	if cfg.UI.Kind == "" {
		cfg.UI.Kind = "tui" // 键缺失与模板同默认（D33）；显式 "repl" 仍可用（测试/e2e 后端）
	}
	if cfg.UI.Kind != "repl" && cfg.UI.Kind != "tui" && cfg.UI.Kind != "gui" {
		fmt.Fprintf(stderr, "ui.kind=%q 仅支持 repl | tui | gui（D33/D43）\n", cfg.UI.Kind)
		return 1
	}
	// MCP 声明校验（D30/D31，启动 fail-fast）：transport/command/url/risk/名字合法。
	for name, srv := range cfg.MCPServers {
		if err := srv.Validate(name); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
	}
	apiKey, err := resolveAPIKey(context.Background(), cfg.Model.APIKey, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	// 权限等级（D22）：缺省 strict；非法值报因退出。
	level := perm.DefaultLevel
	if raw := strings.TrimSpace(cfg.Permissions.Level); raw != "" {
		level, err = perm.Parse(raw)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
	}

	// 权限等级活状态（D22 执行接入）：persistLevel 写回成功后同步，ToolRunner 经 func 读取。
	// atomic 存取（审查修复）：TUI 状态行在事件循环 goroutine 读、/permission 在主
	// goroutine 写——D33 之后"单 goroutine"前提不再成立。
	lvl := newLevelHolder(level)

	// /permission 写回 config（D22）：成功后同步活等级。
	persistLevel := func(l perm.Level) error {
		if err := persistConfig(cfgPath, func(generic map[string]any) {
			perms, _ := generic["permissions"].(map[string]any)
			if perms == nil {
				perms = map[string]any{}
				generic["permissions"] = perms
			}
			perms["level"] = string(l)
		}); err != nil {
			return err
		}
		lvl.Set(l) // 工具链路活等级（D22 执行接入）
		return nil
	}

	// /plugin 状态写回 config 的 plugins 段（D31）：键缺失语义靠省略表达
	//（enabled 缺省启用、granted 缺省空）。
	persistPlugins := func(states map[string]plugin.State) error {
		return persistConfig(cfgPath, func(generic map[string]any) {
			pm := make(map[string]any, len(states))
			for name, st := range states {
				entry := map[string]any{}
				if st.Enabled != nil {
					entry["enabled"] = *st.Enabled
				}
				if len(st.Granted) > 0 {
					entry["granted"] = st.Granted
				}
				pm[name] = entry
			}
			generic["plugins"] = pm
		})
	}

	// /model 切换写回 config 的 model.name（D32）。
	persistModel := func(name string) error {
		return persistConfig(cfgPath, func(generic map[string]any) {
			modelSection(generic)["name"] = name
		})
	}

	// /think /effort 写回 config 的 model 段（D34）。
	persistThink := func(on bool) error {
		return persistConfig(cfgPath, func(generic map[string]any) {
			modelSection(generic)["think"] = on
		})
	}
	persistEffort := func(level string) error {
		return persistConfig(cfgPath, func(generic map[string]any) {
			model := modelSection(generic)
			if level == "" {
				delete(model, "reasoning_effort") // off = 清除（不发送）
				return
			}
			model["reasoning_effort"] = level
		})
	}

	// 端口装配：全部经端口契约注入（内置不享特权，D13）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store, err := storejson.New(filepath.Join(dir, "conversations"))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	// 附件库（DESIGN §4.2）：sha256 内容寻址存 <dir>/attachments；
	// 启动 GC 按全量会话引用清扫（引用收集不完整则跳过，宁可漏清不误删）。
	blobs, err := blobfs.New(filepath.Join(dir, "attachments"))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	runAttachmentGC(ctx, store, blobs, stderr)
	// 记忆（D23 布局）：全局 memories.md + 会话 <id>.memory.md（与会话树同目录）。
	mem, err := memoryfs.New(filepath.Join(dir, port.GlobalMemoryDoc), filepath.Join(dir, "conversations"))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	// 合并记忆（§6.3）：主存储 + 插件 resources 只读投影；宿主建好前 extras 为空。
	// 写/删与 /memory 编辑器仍走主存储（mem.Path 是 memoryfs 专有面）。
	var host *plugin.Host
	mergedMem := &mergedMemory{
		primary: mem,
		extras: func() []port.MemoryStore {
			if host == nil {
				return nil
			}
			ready := host.Ready()
			out := make([]port.MemoryStore, 0, len(ready))
			for _, name := range sortedStoreNames(ready) {
				out = append(out, ready[name].Memory())
			}
			return out
		},
		logf:    func(format string, args ...any) { fmt.Fprintf(stderr, format+"\n", args...) },
		timeout: 5 * time.Second, // 插件投影单次上限（卡死的插件不得拖垮 Turn）
	}
	// 服务端不认的参数记录（D34）：LLM 适配器剥离重试成功后回调，写回 config
	// model.unsupported_params（此后启动直接省略）；写回失败只记日志（下次仍会剥离）。
	noteUnsupported := func(field string) {
		fmt.Fprintf(stderr, "[llm] 服务端不支持参数 %s：已剥离重试，记入 config model.unsupported_params（D34）\n", field)
		if err := persistConfig(cfgPath, func(generic map[string]any) {
			model := modelSection(generic)
			list, _ := model["unsupported_params"].([]any)
			for _, v := range list {
				if v == field {
					return // 已记录
				}
			}
			model["unsupported_params"] = append(list, field)
		}); err != nil {
			fmt.Fprintf(stderr, "[llm] 记录 unsupported_params 失败: %v（下次启动仍会先试发该字段）\n", err)
		}
	}
	client, err := llm.New(llm.Config{
		BaseURL: cfg.Model.BaseURL, APIKey: apiKey, Tokenizer: cfg.Model.Tokenizer,
		UnsupportedParams: cfg.Model.UnsupportedParams,
		NoteUnsupported:   noteUnsupported,
	})
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	// UI 前端（D33/D43）：repl（行式，测试/e2e 后端）、bubbletea TUI（模板默认）或
	// Gio GUI（ui.kind=gui，§15）——同权实现 uiFrontend，换壳不换核（D28 输出器
	// 装饰器自动继承）。TUI 状态行回调经 atomic 读 Agent（构造晚于 UI 创建，
	// 事件循环并发读 → -race 必须）。
	var agentPtr atomic.Pointer[app.Agent]
	var ui uiFrontend
	switch cfg.UI.Kind {
	case "gui":
		// GUI（D43/§15）：悬浮窗事件循环自驱；位置记忆落数据目录（§15.1）。
		// 不进 CI 图形路径（§15.5：GUI 测试全走 headless，门禁/e2e 仍 repl）。
		ui = uigui.New(uigui.Options{
			Status: func() uigui.Status {
				if agent := agentPtr.Load(); agent != nil {
					return uigui.Status{
						Model:  agent.CurrentModel(),
						Level:  lvl.Get().String(),
						Effort: agent.CurrentEffort(), // D34：effort 档位（off 时为空）
					}
				}
				return uigui.Status{}
			},
			PosFile: filepath.Join(dir, "gui_pos.json"),
		})
	case "tui":
		ui = uitui.New(uitui.Options{
			In:  stdin,
			Out: stdout,
			Status: func() uitui.Status {
				if agent := agentPtr.Load(); agent != nil {
					return uitui.Status{
						Model:  agent.CurrentModel(),
						Level:  lvl.Get().String(),
						Effort: agent.CurrentEffort(), // D34：effort 档位（off 时为空）
					}
				}
				return uitui.Status{}
			},
		})
	default:
		ui = repl.New(stdin, stdout)
	}
	defer func() {
		// 先停前端事件循环再写收尾换行：TUI 渲染器写同一 stdout，
		// 直接 Fprintln 会与其并发竞争（-race 复现）。
		_ = ui.Close()
		fmt.Fprintln(stdout)
	}()
	// 输出器扇出（D28/D14）：Presenter 装饰器包住实际 UI，仅 output.notify 开启时装配；
	// app 与 repl 均不感知输出器，Deliver 失败只记日志（§5.7）。
	var outs []port.OutputAdapter
	if cfg.Output.Notify {
		outs = append(outs, notify.New(notifySenderImpl(runtime.GOOS)))
	}
	presenter := &outputsPresenter{
		inner: ui,
		outs:  outs,
		log:   func(err error) { fmt.Fprintf(stderr, "[输出器] %v\n", err) },
	}
	// 逐次确认（§5.10 Confirmer 矩阵）：默认走 REPL 交互；-yes 一律应 y。
	var confirmer port.Confirmer = ui
	if *autoYes {
		confirmer = yesConfirmer{}
	}
	// 内置工具 + ToolRunner 门面（D25：查找/权限判定/确认/超时/裁剪）。
	// 后台任务（DESIGN §5.8/D8）：日志落盘 <dir>/jobs，任务表内存态。
	jobs, err := jobproc.New(filepath.Join(dir, "jobs"))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	toolTimeout := time.Duration(cfg.Limits.ToolTimeoutSec) * time.Second
	if cfg.Limits.ToolTimeoutSec <= 0 {
		toolTimeout = 60 * time.Second
	}
	maxOutput := cfg.Limits.ToolOutputChars
	if maxOutput <= 0 {
		maxOutput = 20000
	}
	builtinTools := toolbuiltin.New(mergedMem, func(name string) (string, bool) {
		p, err := mem.Path(name)
		return p, err == nil
	}, jobs, sandboxDir)
	// think 草稿工具可见性（D34）：默认隐藏（model.think_tool 缺省 false），
	// 配置启用后重启生效——装配期摘除，不做能力探测、运行时零开销。
	if !cfg.Model.ThinkTool {
		builtinTools = dropTool(builtinTools, "think")
	}
	runner := toolrun.New(toolrun.Options{
		Tools:       builtinTools,
		Confirmer:   confirmer,
		Level:       lvl.Get,
		SandboxPath: sandboxDir,
		Timeout:     toolTimeout,
		MaxOutput:   maxOutput,
	})
	// 横切装饰器链（D14/§10，装配根叠加）：审计记每次真实调用，重试包在审计之外
	// （一次用户可见的 Generate = 多条审计行），硬保底截断最内（发给服务端前裁到预算内）。
	audit, err := decorate.NewAudit(filepath.Join(dir, "audit.log"), 0)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer audit.Close()
	var counter port.TokenCounter
	if c, ok := any(client).(port.TokenCounter); ok {
		counter = c // 三级计数链②（D26）：裁剪估算与 Agent 内部共用同一 tokenizer
	}
	maxCtx := cfg.Limits.MaxContextTokens
	if maxCtx <= 0 {
		maxCtx = app.DefaultMaxContextTokens
	}
	fit := func(fctx context.Context, req port.GenerateRequest) (port.GenerateRequest, int) {
		reserve := req.Budget.MaxOutputTokens
		if reserve < minOutputReserve {
			reserve = minOutputReserve
		}
		kept, omitted := app.TrimOldest(fctx, counter, req.Messages, maxCtx-reserve, req.Tools...)
		req.Messages = kept
		return req, omitted
	}
	var gen port.LLM = client
	gen = decorate.NewTruncate(gen, fit, func(omitted int) {
		_ = presenter.Emit(ctx, port.NoticeEvent{
			Text: fmt.Sprintf("硬保底截断：上下文超预算，已省略 %d 条最旧消息", omitted),
		})
	})
	gen = decorate.AuditLLM(gen, audit)
	gen = decorate.NewRetry(gen)
	auditedRunner := decorate.AuditTool(runner, audit)
	ids := systemIDGen{}
	// D37：运行环境块（随 config 快照进 persona）——平台、终端形态与 TERM、特权沙盒目录。
	runtimeEnv := app.RuntimeEnv{
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
		Terminal:   cfg.UI.Kind,
		TERM:       os.Getenv("TERM"),
		SandboxDir: sandboxDir,
		// D38：缺省调用超时随环境块披露（发现性），保留参数口径见 D38。
		ToolTimeoutSec: int(toolTimeout.Seconds()),
	}
	agent, err := app.New(
		app.Deps{
			LLM: gen, UI: presenter, IDs: ids, Clock: systemClock{},
			Tools: auditedRunner, Memory: mergedMem, Blobs: blobs,
		},
		app.Config{
			Model: cfg.Model.Name, System: cfg.SystemPrompt, MaxTurns: cfg.Limits.MaxTurns,
			CompactThreshold: cfg.Limits.CompactThreshold, MaxContextTokens: cfg.Limits.MaxContextTokens,
			Think: cfg.Model.Think, ReasoningEffort: cfg.Model.ReasoningEffort, // D34 初值
			EchoThinking: cfg.Model.EchoThinking == nil || *cfg.Model.EchoThinking, // D42：键缺失 = 回传
			Env:          runtimeEnv,                                               // D37
		},
	)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	agentPtr.Store(agent)                  // TUI 状态行回调读取（atomic，避免与构造竞争）
	runner.Add(agent.ContextCompactTool()) // 装配期注册（agent 依赖 runner，反向补注册）

	// 插件宿主（M4，§6.4）：面刷新先备好（host 与 session 互依，经闭包后绑定）。
	var sess *app.Session
	surfaces := &pluginSurfaces{
		runner:  runner,
		session: &sess,
		ready: func() map[string]plugin.Session {
			if host == nil {
				return nil
			}
			return host.Ready()
		},
		callCtx:    ctx,
		ioTimeout:  10 * time.Second,
		logf:       func(format string, args ...any) { fmt.Fprintf(stderr, format+"\n", args...) },
		registered: map[string][]string{},
		prompts:    map[string][]plugin.PromptInfo{},
	}
	host = plugin.New(plugin.Deps{
		Dial:          mcpgate.NewDialer(envSecrets{}),
		Confirm:       confirmer,
		ConfigServers: cfg.MCPServers,
		PluginsDir:    filepath.Join(dir, "plugins"),
		States:        cfg.Plugins,
		Persist:       persistPlugins,
		Logf:          func(format string, args ...any) { fmt.Fprintf(stderr, format+"\n", args...) },
		OnChange:      surfaces.refresh,
	})

	sess, err = app.NewSession(ctx, app.SessionDeps{
		Store:         store,
		Agent:         agent,
		IDs:           ids,
		Clock:         systemClock{},
		SystemPrompt:  cfg.SystemPrompt,
		Env:           runtimeEnv, // D37：persona 快照带环境块
		Level:         level,
		SandboxPath:   sandboxDir,
		PersistLevel:  persistLevel,
		Confirmer:     confirmer,
		Jobs:          jobs,
		Ingestors:     []port.Ingestor{ingestfile.New(blobs), ingestclip.New(blobs)},
		UI:            presenter,
		OpenMemory:    openMemoryEditor(mem, ui, stdin, stdout, stderr),
		Plugins:       hostAdmin{host},
		ListModels:    func(ctx context.Context) ([]port.ModelInfo, error) { return client.Models(ctx) },
		PersistModel:  persistModel,
		PersistThink:  persistThink,  // D34
		PersistEffort: persistEffort, // D34
	})
	if err != nil {
		if ctx.Err() != nil {
			return 0 // 启动期 Ctrl+C：ctx 早于主循环创建，按取消干净收尾（不报错退出）
		}
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	if ctx.Err() != nil {
		return 0
	}

	// 插件宿主启动（发现 + 授权 + 连接，软启动：单插件失败只记状态）；
	// 退出时优雅关闭（§6.4 #4）。OnChange 已在 Start 内把工具/动态命令同步到位。
	host.Start(ctx)
	defer host.Close()

	ui.Say("Aquarius — 输入 /help 查看命令，/quit 退出；Ctrl+C 取消当前生成")
	// 启动历史回放（D40/§7.4）：恢复的会话把水位→Head 的可见历史重放进转写区，
	// 让用户知道当前是哪个会话、此前聊了什么（新建会话无历史，no-op）。
	if _, rerr := sess.ReplayHistory(ctx); rerr != nil {
		fmt.Fprintf(stderr, "回放历史: %v\n", rerr)
	}
	for {
		ui.Prompt()
		in, err := ui.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return 0 // 收尾换行由 ui.Close 后的 defer 统一处理
			}
			fmt.Fprintf(stderr, "读取输入失败: %v\n", err)
			return 1
		}
		out, herr := func() (string, error) {
			// 每轮独立 ctx（§10 取消语义）：TUI 的 Ctrl+C 经 SetInterrupt 取消本轮；
			// repl 的 os.Interrupt 仍由外层 signal ctx 传导到本 ctx。
			hctx, hcancel := context.WithCancel(ctx)
			ui.SetInterrupt(hcancel)
			defer func() {
				hcancel()
				ui.SetInterrupt(nil)
			}()
			return sess.Handle(hctx, in)
		}()
		if out != "" {
			ui.Say(out)
		}
		if errors.Is(herr, app.ErrQuit) {
			return 0
		}
		if herr != nil {
			_ = ui.Emit(ctx, port.ErrorEvent{Err: herr})
			if ctx.Err() != nil {
				return 0
			}
		}
	}
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

// pickEditor 选择系统编辑器（D24）：$VISUAL → $EDITOR → 平台默认（windows 记事本 / vi）。
func pickEditor(visual, editor, goos string) []string {
	if s := strings.TrimSpace(visual); s != "" {
		return strings.Fields(s)
	}
	if s := strings.TrimSpace(editor); s != "" {
		return strings.Fields(s)
	}
	if goos == "windows" {
		return []string{"notepad"}
	}
	return []string{"vi"}
}

// openMemoryEditor /memory 的装配实现（D24）：文档名 → 磁盘路径（缺失先建空文件）
// → 系统编辑器阻塞打开；保存后下次读取即生效。
// stdio 来自 run() 注入的三流——保持"标准流可注入"的 e2e 契约（不直连 os.Std*）。
func openMemoryEditor(mem *memoryfs.Store, fe uiFrontend, stdin io.Reader, stdout, stderr io.Writer) func(name string) (string, error) {
	return func(name string) (string, error) {
		p, err := mem.Path(name)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return "", fmt.Errorf("创建记忆目录: %w", err)
			}
			if err := os.WriteFile(p, []byte{}, 0o644); err != nil {
				return "", fmt.Errorf("创建记忆文件 %s: %w", p, err)
			}
		}
		// TUI 前端先交出终端再拉起编辑器，返回后收回（审查修复）。
		sus, _ := fe.(terminalSuspend)
		if sus != nil {
			if serr := sus.Suspend(); serr != nil {
				fmt.Fprintf(stderr, "暂停 TUI: %v\n", serr)
			}
			defer func() {
				if rerr := sus.Resume(); rerr != nil {
					fmt.Fprintf(stderr, "恢复 TUI: %v\n", rerr)
				}
			}()
		}
		argv := pickEditor(os.Getenv("VISUAL"), os.Getenv("EDITOR"), runtime.GOOS)
		cmd := exec.Command(argv[0], append(argv[1:], p)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("运行编辑器 %s: %w", argv[0], err)
		}
		return p, nil
	}
}

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

func (yesConfirmer) Confirm(context.Context, string) (bool, error) { return true, nil }

// systemIDGen port.IDGen 的内置实现：复用 domain 的进程内单调 ULID。
type systemIDGen struct{}

var _ port.IDGen = systemIDGen{}

func (systemIDGen) ConversationID() conversation.ID   { return conversation.NewID() }
func (systemIDGen) MessageID() conversation.MessageID { return conversation.NewMessageID() }
func (systemIDGen) CallID() tool.CallID               { return tool.CallID(conversation.NewMessageID()) }
