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
			"  api_key 采用引用格式 secret:<环境变量名>，例如 secret:AQUARIUS_OPENAI_KEY 对应环境变量 AQUARIUS_OPENAI_KEY\n",
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
	if cfg.UI.Kind == "" {
		cfg.UI.Kind = "repl"
	}
	if cfg.UI.Kind != "repl" && cfg.UI.Kind != "tui" {
		fmt.Fprintf(stderr, "ui.kind=%q 仅支持 repl | tui（D33）\n", cfg.UI.Kind)
		return 1
	}
	// MCP 声明校验（D30/D31，启动 fail-fast）：transport/command/url/risk/名字合法。
	for name, srv := range cfg.MCPServers {
		if err := srv.Validate(name); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
	}
	secretRef, err := secretName(cfg.Model.APIKey)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	apiKey, err := (envSecrets{}).Get(context.Background(), secretRef)
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

	// 权限等级活状态（D22 执行接入）：persistLevel 写回成功后同步，ToolRunner 经 func 读取
	//（REPL 单 goroutine 顺序调用，无并发）。
	lvl := &levelHolder{level: level}

	// /permission 写回 config（D22）：map 级重排保留其余配置键；成功后同步活等级。
	persistLevel := func(l perm.Level) error {
		data, rerr := os.ReadFile(cfgPath)
		if rerr != nil {
			return rerr
		}
		var generic map[string]any
		if rerr := json.Unmarshal(data, &generic); rerr != nil {
			return fmt.Errorf("解析 %s: %w", cfgPath, rerr)
		}
		perms, _ := generic["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
			generic["permissions"] = perms
		}
		perms["level"] = string(l)
		out, rerr := json.MarshalIndent(generic, "", "  ")
		if rerr != nil {
			return fmt.Errorf("编码 config: %w", rerr)
		}
		// 唯一临时文件 + 原子换入：无半截文件，覆盖沿用既有权限位（§14 遗留）。
		if rerr := atomicfile.WriteFile(cfgPath, append(out, '\n'), 0o644); rerr != nil {
			return fmt.Errorf("写入 %s: %w", cfgPath, rerr)
		}
		lvl.Set(l) // 工具链路活等级（D22 执行接入）
		return nil
	}

	// /plugin 状态写回 config 的 plugins 段（D31）：map 级重排保留其余配置键；
	// 键缺失语义靠省略表达（enabled 缺省启用、granted 缺省空）。
	persistPlugins := func(states map[string]plugin.State) error {
		data, rerr := os.ReadFile(cfgPath)
		if rerr != nil {
			return rerr
		}
		var generic map[string]any
		if rerr := json.Unmarshal(data, &generic); rerr != nil {
			return fmt.Errorf("解析 %s: %w", cfgPath, rerr)
		}
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
		out, rerr := json.MarshalIndent(generic, "", "  ")
		if rerr != nil {
			return fmt.Errorf("编码 config: %w", rerr)
		}
		if rerr := atomicfile.WriteFile(cfgPath, append(out, '\n'), 0o644); rerr != nil {
			return fmt.Errorf("写入 %s: %w", cfgPath, rerr)
		}
		return nil
	}

	// /model 切换写回 config 的 model.name（D32）：map 级重排保留其余配置键，
	// 原子换入与 /permission 同口径。
	persistModel := func(name string) error {
		data, rerr := os.ReadFile(cfgPath)
		if rerr != nil {
			return rerr
		}
		var generic map[string]any
		if rerr := json.Unmarshal(data, &generic); rerr != nil {
			return fmt.Errorf("解析 %s: %w", cfgPath, rerr)
		}
		model, _ := generic["model"].(map[string]any)
		if model == nil {
			model = map[string]any{}
			generic["model"] = model
		}
		model["name"] = name
		out, rerr := json.MarshalIndent(generic, "", "  ")
		if rerr != nil {
			return fmt.Errorf("编码 config: %w", rerr)
		}
		if rerr := atomicfile.WriteFile(cfgPath, append(out, '\n'), 0o644); rerr != nil {
			return fmt.Errorf("写入 %s: %w", cfgPath, rerr)
		}
		return nil
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
	client, err := llm.New(llm.Config{
		BaseURL: cfg.Model.BaseURL, APIKey: apiKey, Tokenizer: cfg.Model.Tokenizer,
	})
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	// UI 前端（D33）：repl（行式，测试/e2e 后端）或 bubbletea TUI（模板默认）——
	// 同权实现 uiFrontend，换壳不换核（未来 GUI 复用同一套 port 契约，§14）。
	// TUI 状态行回调经 atomic 读 Agent（构造晚于 UI 创建，事件循环并发读 → -race 必须）。
	var agentPtr atomic.Pointer[app.Agent]
	var ui uiFrontend
	switch cfg.UI.Kind {
	case "tui":
		ui = uitui.New(uitui.Options{
			In:  stdin,
			Out: stdout,
			Status: func() uitui.Status {
				if agent := agentPtr.Load(); agent != nil {
					return uitui.Status{Model: agent.CurrentModel(), Level: lvl.Get().String()}
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
	runner := toolrun.New(toolrun.Options{
		Tools: toolbuiltin.New(mergedMem, func(name string) (string, bool) {
			p, err := mem.Path(name)
			return p, err == nil
		}, jobs),
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
	agent, err := app.New(
		app.Deps{
			LLM: gen, UI: presenter, IDs: ids, Clock: systemClock{},
			Tools: auditedRunner, Memory: mergedMem, Blobs: blobs,
		},
		app.Config{
			Model: cfg.Model.Name, System: cfg.SystemPrompt, MaxTurns: cfg.Limits.MaxTurns,
			CompactThreshold: cfg.Limits.CompactThreshold, MaxContextTokens: cfg.Limits.MaxContextTokens,
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
		Store:        store,
		Agent:        agent,
		IDs:          ids,
		Clock:        systemClock{},
		SystemPrompt: cfg.SystemPrompt,
		Level:        level,
		SandboxPath:  sandboxDir,
		PersistLevel: persistLevel,
		Confirmer:    confirmer,
		Jobs:         jobs,
		Ingestors:    []port.Ingestor{ingestfile.New(blobs), ingestclip.New(blobs)},
		UI:           presenter,
		OpenMemory:   openMemoryEditor(mem, stdin, stdout, stderr),
		Plugins:      hostAdmin{host},
		ListModels:   func(ctx context.Context) ([]port.ModelInfo, error) { return client.Models(ctx) },
		PersistModel: persistModel,
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
// ToolRunner 经 Get 读取。REPL 单 goroutine 顺序调用，无需加锁。
type levelHolder struct{ level perm.Level }

func (h *levelHolder) Get() perm.Level  { return h.level }
func (h *levelHolder) Set(l perm.Level) { h.level = l }

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
func openMemoryEditor(mem *memoryfs.Store, stdin io.Reader, stdout, stderr io.Writer) func(name string) (string, error) {
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
