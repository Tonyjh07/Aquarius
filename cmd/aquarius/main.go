// Command aquarius 是 Aquarius 的单二进制入口：
// 装配 config → Secrets/LLM/Store 端口 → 会话 → REPL（DESIGN §8 布局 / §12 M0 验收）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/ingestclip"
	"github.com/Tonyjh07/Aquarius/internal/adapter/ingestfile"
	"github.com/Tonyjh07/Aquarius/internal/adapter/llm"
	"github.com/Tonyjh07/Aquarius/internal/adapter/memoryfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// fatal 启动期致命错误：stderr 报因并返回退出码 1；控制台不可见（GUI 双击启动，
// D108 已隐藏自建控制台、stderr 无处可看）时补系统消息框（D110 修订④——旧格式
// 与配置错误必须清晰可见，不得静默退出）。
func fatal(stderr io.Writer, format string, args ...any) int {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(stderr, msg)
	notifyFatal(msg)
	return 1
}

// resolveProfile 解析当前 profile 名（D110①/修订②）：--profile flag > 根 config.json
// 指针，两者仅决定「读哪个 profile」。指针不存在 = 首次运行，写指针指向 default；
// 旧单文件 config 经 parsePointerFile 显式报 ErrLegacyConfig（D95 同口径，不做自动迁移）。
// flag 显式选择视为用户断言：指向不存在的 profile 报因列出可用名，不自动创建、不写回指针。
func resolveProfile(dir, flagName string) (string, error) {
	pointerPath := filepath.Join(dir, "config.json")
	var name string
	data, err := os.ReadFile(pointerPath)
	switch {
	case err == nil:
		var perr error
		if name, perr = parsePointerFile(pointerPath, data); perr != nil {
			return "", perr
		}
	case errors.Is(err, os.ErrNotExist):
		if flagName == "" {
			// 首次运行：写指针指向 default（profile 配置模板随后由 run 的模板分支落盘）。
			if werr := os.WriteFile(pointerPath, []byte(pointerContent(defaultProfileName)), 0o644); werr != nil {
				return "", fmt.Errorf("生成 profile 指针 %s: %w", pointerPath, werr)
			}
		}
	default:
		return "", fmt.Errorf("读取 profile 指针 %s: %w", pointerPath, err)
	}
	if flagName != "" {
		if err := validProfileName(flagName); err != nil {
			return "", err
		}
		name = flagName
		avail := listProfiles(dir)
		if !slices.Contains(avail, name) {
			joined := strings.Join(avail, ", ")
			if joined == "" {
				joined = "无"
			}
			return "", fmt.Errorf("--profile %s 不存在（profiles/ 下可用：%s）", name, joined)
		}
		return name, nil
	}
	if name == "" {
		name = defaultProfileName
	}
	return name, nil
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
// uiKindDefault ui.kind 键缺失回落（D51：与模板/文档同默认 = gui；显式值原样保留）。
// 独立成函数 = 缺省口径可测，避免测试真开窗。
func uiKindDefault(kind string) string {
	if kind == "" {
		return "gui"
	}
	return kind
}

// wiring 装配态（S4a）：run() 的装配段拆为方法后共享的上下文——原闭包捕获变
// 字段。bootstrap 填 profile/配置/权限组；assemble_* 填端口组；host/sess 由后续
// 构造回填（记忆投影与 surfaces 经字段地址读取，等价原闭包对局部变量的捕获）。
type wiring struct {
	stdout io.Writer
	stderr io.Writer

	// bootstrap_profile.go 填充
	dir         string // 数据根 <data>
	profileName string
	profileDir  string
	sandboxDir  string
	cfgPath     string
	cfg         *fileConfig
	primary     *providerConfig
	level       perm.Level
	lvl         *levelHolder
	persist     *cfgWriter

	// assemble 段填充
	store     port.ConversationStore
	blobs     port.AttachmentStore
	mem       *memoryfs.Store
	merged    *mergedMemory
	jobs      port.JobManager
	runner    *toolrun.Runner
	audit     *decorate.Audit
	client    *llm.Client
	ui        uiFrontend
	presenter *outputsPresenter

	// buildLLMs 填充（stackDecorators 消费）
	fallbackEntries  []decorate.FallbackEntry
	fallbackCounters map[string]port.TokenCounter

	toolTimeout time.Duration

	// UI 状态行/分叉条的原子槽（D80/§7.5）：UI 先于 Agent/Session 构造，事件循环
	// goroutine 经原子槽并发读（-race 必须）。
	agentPtr atomic.Pointer[app.Agent]
	sessPtr  atomic.Pointer[app.Session]

	// 闭包后绑定
	host *plugin.Host
	sess *app.Session
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// 双击静默启动（D108/§15.1）：自建控制台即判即隐，压短黑框闪现；终端启动
	//（挂载 ≥2）不受影响、日志照常。repl/tui 前端随后恢复显示。
	hideSpawnedConsole()
	flags := flag.NewFlagSet("aquarius", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "", "数据目录（默认 ~/.aquarius）")
	profileFlag := flags.String("profile", "", "选择 profile（进程级覆盖根 config.json 指针，D110；须已存在）")
	modelName := flags.String("model", "", "覆盖 model.name")
	baseURL := flags.String("base-url", "", "覆盖 model.base_url")
	autoYes := flags.Bool("yes", false, "跳过逐次确认（等价对每个确认回答 y；危险操作慎用）")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	// 顺序骨架：profile 解析 → 端口装配 → UI → 工具/装饰器 → Agent/插件宿主/Session
	// → 主循环；各段实现见 bootstrap_profile.go / assemble_ports.go / assemble_ui.go。
	w := &wiring{stdout: stdout, stderr: stderr}
	if err := w.bootstrap(*dataDir, *profileFlag, *modelName, *baseURL); err != nil {
		if errors.Is(err, errFirstRun) {
			return 0 // 首跑已写模板并打印指引（原契约：生成配置后重跑）
		}
		return fatal(stderr, "%v", err)
	}

	// 端口装配：全部经端口契约注入（内置不享特权，D13）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := w.assembleStores(ctx); err != nil {
		return fatal(stderr, "%v", err)
	}
	if err := w.buildLLMs(); err != nil {
		return fatal(stderr, "%v", err)
	}
	w.newUI(stdin, stdout)
	defer func() {
		// 先停前端事件循环再写收尾换行：TUI 渲染器写同一 stdout，
		// 直接 Fprintln 会与其并发竞争（-race 复现）。
		_ = w.ui.Close()
		fmt.Fprintln(stdout)
	}()
	w.buildPresenter()
	// 逐次确认（§5.10 Confirmer 矩阵）：默认走 REPL 交互；-yes 一律应 y。
	var confirmer port.Confirmer = w.ui
	if *autoYes {
		confirmer = yesConfirmer{}
	}
	if err := w.assembleTools(confirmer); err != nil {
		return fatal(stderr, "%v", err)
	}
	gen, err := w.stackDecorators(ctx)
	if err != nil {
		return fatal(stderr, "%v", err)
	}
	defer w.audit.Close()
	auditedRunner := decorate.AuditTool(w.runner, w.audit)
	ids := systemIDGen{}
	// D37：运行环境块（随 config 快照进 persona）——平台、终端形态与 TERM、特权沙盒目录。
	runtimeEnv := app.RuntimeEnv{
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
		Terminal:   w.cfg.UI.Kind,
		TERM:       os.Getenv("TERM"),
		SandboxDir: w.sandboxDir,
		// D38：缺省调用超时随环境块披露（发现性），保留参数口径见 D38。
		ToolTimeoutSec: int(w.toolTimeout.Seconds()),
	}
	agent, err := app.New(
		app.Deps{
			LLM: gen, UI: w.presenter, IDs: ids, Clock: systemClock{},
			Tools: auditedRunner, Memory: w.merged, Blobs: w.blobs,
		},
		app.Config{
			Model: w.cfg.Model.Name, System: w.cfg.SystemPrompt, MaxTurns: w.cfg.Limits.MaxTurns,
			CompactThreshold: w.cfg.Limits.CompactThreshold, MaxContextTokens: w.cfg.Limits.MaxContextTokens,
			Think: w.cfg.Model.Think, ReasoningEffort: w.cfg.Model.ReasoningEffort, // D34 初值
			EchoThinking: w.cfg.Model.EchoThinking == nil || *w.cfg.Model.EchoThinking, // D42：键缺失 = 回传
			Env:          runtimeEnv,                                                   // D37
		},
	)
	if err != nil {
		return fatal(stderr, "%v", err)
	}
	w.agentPtr.Store(agent)                  // TUI 状态行回调读取（atomic，避免与构造竞争）
	w.runner.Add(agent.ContextCompactTool()) // 装配期注册（agent 依赖 runner，反向补注册）

	// 插件宿主（M4，§6.4）：面刷新先备好（host 与 session 互依，经闭包后绑定）。
	w.newPluginHost(ctx, confirmer)

	w.sess, err = app.NewSession(ctx, app.SessionDeps{
		Store:         w.store,
		Agent:         agent,
		IDs:           ids,
		Clock:         systemClock{},
		SystemPrompt:  w.cfg.SystemPrompt,
		Env:           runtimeEnv, // D37：persona 快照带环境块
		Level:         w.level,
		SandboxPath:   w.sandboxDir,
		PersistLevel:  w.persist.persistLevel,
		Confirmer:     confirmer,
		Jobs:          w.jobs,
		Ingestors:     []port.Ingestor{ingestfile.New(w.blobs), ingestclip.New(w.blobs)},
		UI:            w.presenter,
		OpenMemory:    openMemoryEditor(w.mem, w.ui, stdin, stdout, stderr),
		Plugins:       hostAdmin{w.host},
		ListModels:    func(ctx context.Context) ([]port.ModelInfo, error) { return w.client.Models(ctx) },
		ProviderName:  func() string { return w.primary.Name }, // D110②：/model 展示（静态，重启生效）
		PersistModel:  w.persist.persistModel,
		PersistThink:  w.persist.persistThink,  // D34
		PersistEffort: w.persist.persistEffort, // D34
	})
	if err != nil {
		if ctx.Err() != nil {
			return 0 // 启动期 Ctrl+C：ctx 早于主循环创建，按取消干净收尾（不报错退出）
		}
		return fatal(stderr, "%v", err)
	}
	if ctx.Err() != nil {
		return 0
	}
	w.sessPtr.Store(w.sess) // D80/§7.5：会话就绪后发布只读树视图（UI 侧分叉条数据面）

	// 插件宿主启动（发现 + 授权 + 连接，软启动：单插件失败只记状态）；
	// 退出时优雅关闭（§6.4 #4）。OnChange 已在 Start 内把工具/动态命令同步到位。
	w.host.Start(ctx)
	defer w.host.Close()

	// 启动提示：GUI 做成 logo 悬浮 tips（§15.1，不入转写区、初始即空）；
	// repl/TUI 照常一行。
	if w.cfg.UI.Kind != "gui" {
		w.ui.Say("Aquarius — 输入 /help 查看命令，/quit 退出；Ctrl+C 取消当前生成")
	}
	// 启动历史回放（D40/§7.4）：恢复的会话把水位→Head 的可见历史重放进转写区，
	// 让用户知道当前是哪个会话、此前聊了什么（新建会话无历史，no-op）。
	if _, rerr := w.sess.ReplayHistory(ctx); rerr != nil {
		fmt.Fprintf(stderr, "回放历史: %v\n", rerr)
	}
	for {
		w.ui.Prompt()
		in, err := w.ui.Next(ctx)
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
			w.ui.SetInterrupt(hcancel)
			defer func() {
				hcancel()
				w.ui.SetInterrupt(nil)
			}()
			return w.sess.Handle(hctx, in)
		}()
		if out != "" {
			w.ui.Say(out)
		}
		if errors.Is(herr, app.ErrQuit) {
			return 0
		}
		if herr != nil {
			_ = w.ui.Emit(ctx, port.ErrorEvent{Err: herr})
			if ctx.Err() != nil {
				return 0
			}
		}
	}
}
