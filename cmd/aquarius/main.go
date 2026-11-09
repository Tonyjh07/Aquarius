// Command aquarius 是 Aquarius 的单二进制入口：
// 装配 config → Secrets/LLM/Store 端口 → 会话 → REPL（§8 布局 / §12 M0 验收）。
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

	"github.com/Tonyjh07/Aquarius/internal/adapter/acpgate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/characterfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/exchange"
	"github.com/Tonyjh07/Aquarius/internal/adapter/ingestclip"
	"github.com/Tonyjh07/Aquarius/internal/adapter/ingestfile"
	"github.com/Tonyjh07/Aquarius/internal/adapter/ingestmic"
	"github.com/Tonyjh07/Aquarius/internal/adapter/jobproc"
	"github.com/Tonyjh07/Aquarius/internal/adapter/memoryfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/outputtts"
	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/skillfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uicommon"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/logging"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// version 构建版本号：发布构建用 `-ldflags "-X main.version=v0.1.0"` 注入，开发构建
// 保持 dev。只进 `-version` 输出与欢迎信息，不参与任何行为分支。
var version = "dev"

func main() {
	// 常驻内存策略：压低 GC 摆动倍数 + 装泄漏保险上限（GOMEMLIMIT / GOGC 显式设置时尊重用户口径）。
	applyMemoryPolicy()
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
			if werr := writeConfigFile(pointerPath, []byte(pointerContent(defaultProfileName))); werr != nil {
				return "", fmt.Errorf("%s: %w",
					i18n.Tf("cmd.profile.err.write_pointer", "path", pointerPath), werr)
			}
		}
	default:
		return "", fmt.Errorf("%s: %w",
			i18n.Tf("cmd.profile.err.read_pointer", "path", pointerPath), err)
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
				joined = i18n.T("ui.common.none")
			}
			return "", fmt.Errorf("%s", i18n.Tf("cmd.profile.err.not_found",
				"name", name, "available", joined))
		}
		return name, nil
	}
	if name == "" {
		name = defaultProfileName
	}
	return name, nil
}

// minOutputReserve 硬保底截断为本轮输出预留的最小 token 数（§7.1）。
const minOutputReserve = 1024

// uiFrontend 装配根消费的前端面：repl 与 TUI 同权实现（D33 换壳不换核）。
type uiFrontend interface {
	port.Presenter
	port.Prompter
	port.Confirmer
	port.Asker
	// Say 输出一行会话文本（命令输出、启动提示）。
	Say(text string)
	// Prompt 输入提示（TUI 输入行常驻，等价 no-op）。
	Prompt()
	// SetInterrupt 注入 Ctrl+C 行为（repl = no-op 走 os.Interrupt；TUI = 取消当前 Turn）。
	SetInterrupt(fn func())
	// Wake 空闲唤醒（D167）：后台任务完成等外部生产者敲醒 Next；不是用户输入。
	Wake()
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

// uiLangSetting 选出真正生效的 `ui.lang` 写法：`AQUARIUS_UI_LANG` 环境变量 > 配置 > 空
// （空 = auto，D275/§7.8）。环境变量这一层不长在配置里，是因为首跑路径还没有配置文件，
// 而界面语言必须在写模板之前就定下来；便携安装也用它临时换语言而不改配置。
func uiLangSetting(env, cfg string) string {
	if v := strings.TrimSpace(env); v != "" {
		return v
	}
	return cfg
}

// logLevelSetting 选出真正生效的日志级别：`-log-level` > `AQUARIUS_LOG_LEVEL` > `ui.log_level`
// （D276/§7.9）。三层都空 = 交给 ParseLevel 取 info。环境变量这一层是给「改配置太慢」的
// 现场用的：级别只在排查时调，改完启动就生效。
func logLevelSetting(flagVal, env, cfg string) string {
	for _, v := range []string{flagVal, env, cfg} {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// stderrProxy 转发到「此刻的」w.stderr：TUI/GUI 启动后会重绑这个字段（D114 恢复控制台），
// 日志句柄若在装配期抓住旧 writer，之后的行就写去了已废弃的流。
type stderrProxy struct{ w *wiring }

func (p stderrProxy) Write(b []byte) (int, error) { return p.w.stderr.Write(b) }

// pendingLog 打开日志之前攒下的一条（bootstrap 段：profile 已经知道，级别还没读出来）。
type pendingLog struct {
	level  logging.Level
	ctx    logging.Context
	event  string
	action string
	msg    string // 非空 = 整句已给（不经 event 取词）
	kv     []any
}

// note 记一条运行日志。日志句柄还没开就先排队：bootstrap 读配置那一段已经在报因
// （语音配了却连不上、链比模型窄），而级别要从同一份配置里读——就地打一行只能落 stderr，
// GUI 双击启动等于没留痕。openLog 之后按原顺序补记，级别过滤照着走。
func (w *wiring) note(lv logging.Level, ctx logging.Context, event, action string, kv ...any) {
	w.emit(pendingLog{level: lv, ctx: ctx, event: event, action: action, kv: kv})
}

// noteText 同 note，但整句已由调用方给出（句子本身就是事件的那几处：降级说明、回放失败、
// 明文密钥）。
func (w *wiring) noteText(lv logging.Level, ctx logging.Context, event, action, msg string) {
	w.emit(pendingLog{level: lv, ctx: ctx, event: event, action: action, msg: msg})
}

func (w *wiring) emit(p pendingLog) {
	if w.log == nil {
		w.pendingLog = append(w.pendingLog, p)
		return
	}
	w.log.Write(p.level, p.ctx, p.event, p.action, p.msg, p.kv...)
}

// logFrom 注入给适配器与内核组件的结构化日志出口（`port.Log`，D276/L3）：适配器给级别、
// 上下文与两个 key，这里翻成 logging 的类型再交 note——适配器不许 import 日志包，翻这一下
// 就是那条边界的落点。认不出的级别按 warn：宁可多报一行，也不能把一条错误静默成 info。
func (w *wiring) logFrom(lv port.LogLevel, ctx, event, act string, kv ...any) {
	level := logging.LevelWarn
	switch lv {
	case port.LogDebug:
		level = logging.LevelDebug
	case port.LogInfo:
		level = logging.LevelInfo
	case port.LogWarn:
		level = logging.LevelWarn
	case port.LogError:
		level = logging.LevelError
	}
	w.note(level, logging.Context(ctx), event, act, kv...)
}

// wiring 装配态（S4a）：run() 的装配段拆为方法后共享的上下文——原闭包捕获变
// 字段。bootstrap 填 profile/配置/权限组；assemble_* 填端口组；host/sess 由后续
// 构造回填（记忆投影与 surfaces 经字段地址读取，等价原闭包对局部变量的捕获）。
type wiring struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	// log 运行日志（D276/§7.9）：bootstrap 之后开，全量落盘、warn 以上同时上 stderr。
	// pendingLog 是开日志之前攒下的启动告警，Open 之后按序补记（见 note）。
	log        *logging.Logger
	pendingLog []pendingLog

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
	policy      *policyHolder
	unattended  *unattendedHolder
	persist     *cfgWriter
	// firstRun 本次为该 profile 首次运行（已写模板，D141）；setupTried 装配前向导窗
	// 本进程只尝试一次（向导后仍不完整即报因退出，不循环弹窗）。
	firstRun   bool
	setupTried bool
	// keys 输入栏三键（§5.19 / ui.keys）：bootstrap 解析并校验（撞键/非法键名启动即报），
	// 装配时注入两个前端。repl 不用它——那里一行就是一次发送，没有「生成中途再说话」。
	keys uicommon.KeyBindings
	// lang 本次启动生效的界面语言（D275/§7.8）：bootstrap 定好后写进取词口，这里留一份
	// 只读副本，供启动日志与诊断说话（前端取词直接走 i18n.T，不再传参）。
	lang i18n.Lang

	// 本机密钥库（D151/D161）：secrets 装配根读链（库里没有才查同名环境变量）、
	// secretsBin 库本体（界面明文密钥的写入口）。bootstrap 填（profileDir 一就位就有）。
	secrets    *secretChain
	secretsBin *secretbin.Store

	// assemble 段填充
	store    port.ConversationStore
	blobs    port.AttachmentStore
	mem      *memoryfs.Store
	merged   *mergedMemory
	jobs     port.JobManager
	jobMgr   *jobproc.Manager // 具体实现：关停时等已结束任务的收尾写完
	jobWatch *app.JobWatcher
	watcher  *app.Watcher
	// 外部代理转交（§5.18/D244）：delegates 是套了收尾观察者装饰器的面（工具与命令共用），
	// delegateMgr 保留具体实现只为关停时终止进程组；delegateAgents 是可用清单快照。
	delegates      port.Delegate
	delegateMgr    *acpgate.Manager
	delegateWatch  *app.DelegateWatcher
	delegateAgents []port.DelegateInfo
	skills         *skillfs.Catalog
	// skillInstall 技能安装面内核（D202；装配根在 skillsadmin.go 适配两套消费方接口）。
	skillInstall *skillfs.Installer
	// characters 角色卡目录（§5.17）：`<profile>/characters`；读侧实现 port.CharacterCatalog，
	// 导入 / 删除实现 app.CharacterAdmin（命令面与设置窗共用同一份）。
	characters *characterfs.Store
	// exchange 会话进出适配器（/export 与 /import，§5.16）：导入侧要往附件库
	// 回填归档里的字节，故与 blobs 同期装配。
	exchange  *exchange.Exchange
	runner    *toolrun.Runner
	audit     *decorate.Audit
	client    port.LLM
	ui        uiFrontend
	presenter *outputsPresenter

	// assembleAudio 填充（D179）：录音/播放恒在，识别与合成按配置现建；mic 是
	// /mic 与界面语音键共用的摄取器。
	recorder port.Recorder
	player   port.AudioPlayer
	asr      port.Transcriber
	ttsSynth port.Synthesizer
	mic      *ingestmic.Ingestor
	ttsOut   *outputtts.Adapter

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

	// S6/D143：工具与 MCP 管理面——内置工具禁用集的运行态持有（启动推导，开关热更）
	// 与快照修订号（host.OnChange + 内置变更递增；设置窗按 Revision 重建行）。
	disabledTools *disabledHolder
	toolsRev      atomic.Int64

	// S9-6b/D161：密钥库管理面快照修订号（设置窗按 Revision 重建条目行；每次写入/删除
	// 递增）。库本体是 secretsBin（本地文件 IO，无进程内缓存）。
	secretsRev atomic.Int64

	// D202/D203：技能管理面快照修订号（安装/卸载/更新/重扫/目录增删都递增）。
	skillsRev atomic.Int64

	// D204：采样参数热生效快照（整链缺省 + 逐模型覆盖）。装配时填一份，设置窗
	// 「保存并应用」后整份换掉——Agent 每轮按当前模型名现取，不必重建。
	sampling atomic.Pointer[samplingTables]
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// 双击静默启动（D108/§15.1，D114）：自建控制台即判即脱离（FreeConsole——WT 默认
	// 终端下可见终端随会话销毁关闭）；终端启动（挂载 ≥2）不受影响、日志照常。
	// repl/tui 前端随后恢复控制台并重绑 w.stdin/w.stdout/w.stderr。
	hideSpawnedConsole()
	flags := flag.NewFlagSet("aquarius", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "", i18n.T("cmd.boot.flag.data"))
	profileFlag := flags.String("profile", "", i18n.T("cmd.boot.flag.profile"))
	modelName := flags.String("model", "", i18n.T("cmd.boot.flag.model"))
	baseURL := flags.String("base-url", "", i18n.T("cmd.boot.flag.base_url"))
	logLevel := flags.String("log-level", "", i18n.T("cmd.boot.flag.log_level"))
	logFormat := flags.String("log-format", "", i18n.T("cmd.boot.flag.log_format"))
	showVersion := flags.Bool("version", false, i18n.T("cmd.boot.flag.version"))
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "Aquarius %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return 0
	}

	// 顺序骨架：profile 解析 → 端口装配 → UI → 工具/装饰器 → Agent/插件宿主/Session
	// → 主循环；各段实现见 bootstrap_profile.go / assemble_ports.go / assemble_ui.go。
	w := &wiring{stdin: stdin, stdout: stdout, stderr: stderr}
	if err := w.bootstrap(*dataDir, *profileFlag, *modelName, *baseURL); err != nil {
		if errors.Is(err, errFirstRun) {
			return 0 // 首跑已写模板并打印指引（原契约：生成配置后重跑）
		}
		return fatal(stderr, "%v", err)
	}

	// 运行日志（D276/§7.9）：profile 已定、目录已建，从这里起所有运行期事件都走它。
	// 开不出文件不拦启动——Open 自己降级到 stderr 并记一条 warn。
	if err := w.openLog(*logLevel, *logFormat); err != nil {
		return fatal(stderr, "%v", err) // 级别/形态写错是用户能改的错，按启动失败报
	}
	defer w.log.Close()

	// 端口装配：全部经端口契约注入（内置不享特权，D13）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// D141：装配前欢迎向导窗——gui 且「配置不完整」（primary/fallback 的 secret: 取不到）
	// 时先开窗引导；保存后重新读配置、同进程继续启动（取消 = 退出 0）。见 setupWizard。
	if code, done := w.setupWizard(ctx, *dataDir, *profileFlag, *modelName, *baseURL); done {
		return code
	}

	if err := w.assembleStores(ctx); err != nil {
		return fatal(stderr, "%v", err)
	}
	w.assembleAudio(ctx)
	defer w.closeAudio()
	if err := w.buildLLMs(); err != nil {
		return fatal(stderr, "%v", err)
	}
	w.newUI(w.stdin, w.stdout) // D114：bootstrap 恢复控制台后可能已重绑（脱离→AllocConsole）
	defer func() {
		// 先停前端事件循环再写收尾换行：TUI 渲染器写同一 stdout，
		// 直接 Fprintln 会与其并发竞争（-race 复现）。走 w.stdout（D114：
		// 恢复路径重绑后的控制台流）。
		_ = w.ui.Close()
		fmt.Fprintln(w.stdout)
	}()
	w.buildPresenter()
	// 逐次确认（§5.11 Confirmer 矩阵）与提问通道（D184）：默认都走前端交互面。
	tc := toolChannelsFor(w.ui, w.unattended.Get)
	if err := w.assembleTools(ctx, tc.toolConfirmer, tc.toolAsker); err != nil {
		return fatal(stderr, "%v", err)
	}
	// 任务管理器在观察者之后关（defer 后进先出）：先停报告，再等已结束任务的收尾写完
	// （退出行、日志句柄、台账终态），退出后不再动 jobs 目录（§5.8）。
	defer w.jobMgr.Close()
	// D189：先停任务收尾观察者再关 UI（defer 后进先出）——唤醒不会落到已关闭的前端。
	defer w.jobWatch.Close()
	// D190：观察器同样在 UI 之前停掉（它的报告会唤醒前端）。
	defer w.watcher.Close()
	// §5.18/D262：转交先停收尾观察者（不再注入），退出时再主动终止进程组——ACP 会话
	// 不能恢复（本版不做 session/load），留着只有一个后果：一路失去监督的子代理继续
	// 按自己的判断改文件。
	defer w.delegateMgr.Close()
	defer w.delegateWatch.Close()
	gen, err := w.stackDecorators(ctx)
	if err != nil {
		return fatal(stderr, "%v", err)
	}
	defer w.audit.Close()
	auditedRunner := decorate.AuditTool(w.runner, w.audit)
	ids := systemIDGen{}
	// D37：运行环境块（随 config 快照进 persona）——平台、终端形态与 TERM、特权沙盒目录。
	runtimeEnv := w.runtimeEnv()
	// 人格模块树（D144/S7）：Agent 兜底提示（每请求展开）与会话 persona（创建时展开，@env.date = 当天）。
	personaSpec := w.cfg.promptSpec()
	agentPersona := app.NewPersonaComposer(personaSpec, runtimeEnv, nil)
	sessPersona := app.NewPersonaComposer(personaSpec, runtimeEnv, systemClock{}.Now)
	if _, missing := agentPersona.Compose(); len(missing) > 0 {
		w.log.Warn(logging.Boot, "log.boot.persona_missing", "log.act.skip_missing",
			"missing", missing)
	}
	// D204：采样参数快照（整链缺省 + 逐模型覆盖）——Agent 每轮按模型名现取。
	w.sampling.Store(w.cfg.Model.samplingTables())
	agent, err := app.New(
		app.Deps{
			LLM: gen, UI: w.presenter, IDs: ids, Clock: systemClock{},
			Tools: auditedRunner, Memory: w.merged, Blobs: w.blobs,
		},
		app.Config{
			Model: w.cfg.Model.Name, Persona: agentPersona, MaxTurns: w.cfg.Limits.MaxTurns,
			CompactThreshold: w.cfg.Limits.CompactThreshold, MaxContextTokens: w.maxContextBudget(), // D177：取小
			Think: w.cfg.Model.Think, ReasoningEffort: w.cfg.Model.ReasoningEffort, // D34 初值
			EchoThinking: w.cfg.Model.EchoThinking == nil || *w.cfg.Model.EchoThinking, // D42：键缺失 = 回传
			Caps:         w.cfg.Model.chainCaps(),                                      // D173：降级链取与
			// §5.17/D247：会话尾部指令（卡的 post_history_instructions）是否随每轮装配发出。
			ApplyPostHistory: w.cfg.applyPostHistory(),
			// D204：采样参数走原子快照——整链缺省 + 逐模型覆盖；设置窗保存后换掉整份即热生效。
			Sampling:    w.cfg.Model.Sampling.toSampling(),
			SamplingFor: w.samplingFor,
		},
	)
	if err != nil {
		return fatal(stderr, "%v", err)
	}
	w.agentPtr.Store(agent)                  // TUI 状态行回调读取（atomic，避免与构造竞争）
	w.runner.Add(agent.ContextCompactTool()) // 装配期注册（agent 依赖 runner，反向补注册）

	// 插件宿主（M4，§6.4）：面刷新先备好（host 与 session 互依，经闭包后绑定）。
	w.newPluginHost(ctx, tc.confirmer)

	w.sess, err = app.NewSession(ctx, app.SessionDeps{
		Store:          w.store,
		Agent:          agent,
		IDs:            ids,
		Clock:          systemClock{},
		Persona:        sessPersona, // D144/S7：新建会话时展开人格模块树（@env.date = 创建当天）
		Level:          w.level,
		SandboxPath:    w.sandboxDir,
		Perms:          permSettings{w: w},
		Confirmer:      tc.confirmer,
		Jobs:           w.jobs,
		Delegate:       w.delegates, // §5.18：/delegate list|kill（派活是模型经工具做的）
		Watches:        w.watcher,
		Skills:         w.skills,             // D201：/skill 与 skill 工具同一份清单
		SkillAdmin:     skillsAdminApp{w: w}, // D202：/skill install|remove|update
		WatchCancel:    watchCancel{w: w},    // D262：删除会话与关停时撤两个轮询器；切换不撤
		Log:            w.logFrom,
		Ingestors:      []port.Ingestor{ingestfile.New(w.blobs), ingestclip.New(w.blobs), w.mic},
		UI:             w.presenter,
		Wake:           w.uiWake, // D264：带回执的注入自己敲一下，等排空才返回
		Memories:       memoryOpener{w: w},
		Plugins:        hostAdmin{h: w.host, auditPath: filepath.Join(w.profileDir, "audit.log")}, // S6/D143：Logs 读审计日志 tail
		Models:         modelCatalog{w: w},                                                        // D32/D173/D174：/model 读侧 + 切换 + 探测
		ModelSwitch:    modelCatalog{w: w},                                                        // D32：/model 切换写回
		Thinking:       thinkSettings{w: w},                                                       // D34：/think 与 /effort 写回
		Exporter:       w.exchange,                                                                // 会话进出：/export
		Importer:       w.exchange,                                                                // 会话进出：/import
		ExportsDir:     filepath.Join(w.profileDir, "exports"),                                    // 缺省落点（随 profile 隔离）
		Characters:     w.characters,                                                              // 角色卡只读清单（/character，§5.17）
		CharacterAdmin: w.characters,                                                              // §5.17：/character import|remove
		CharacterOpts:  w.cfg.characterOptions(),                                                  // §5.17：三处默认开的投影开关由 config 解析
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
		w.ui.Say(i18n.T("cmd.boot.banner"))
	}
	// 启动对账（D262）：上次退出时没来得及收尾的任务，补一条「它当时在跑/已结束」的
	// 事实注入；紧接着的历史回放会把它铺出来。台账读即清，补过就不会再报一次。
	if items, jerr := w.jobs.Unfinished(ctx); jerr != nil {
		w.note(logging.LevelWarn, logging.Job, "log.job.ledger_unreadable",
			"log.act.continue", "err", jerr)
	} else if rerr := w.sess.ReportUnfinished(ctx, items); rerr != nil {
		w.note(logging.LevelWarn, logging.Job, "log.job.unfinished_report_failed",
			"log.act.continue", "err", rerr)
	}
	// 转交对账（D269）：上次退出时还在跑的转交，进程早被终止，报告也不会自己回来——
	// 补一条事实注入（同时给用户一行提示，否则模型忽然知道某件活的存在、用户却不知道）。
	if w.delegateMgr != nil {
		if items, derr := w.delegateMgr.Unfinished(ctx); derr != nil {
			w.note(logging.LevelWarn, logging.Delegate, "log.delegate.ledger_unreadable",
				"log.act.continue", "err", derr)
		} else if len(items) > 0 {
			if rerr := w.sess.ReportUnfinishedDelegates(ctx, items); rerr != nil {
				w.note(logging.LevelWarn, logging.Delegate, "log.delegate.unfinished_report_failed",
					"log.act.continue", "err", rerr)
			}
			w.ui.Say(i18n.Tf("cmd.boot.interrupted_delegates", "n", len(items)))
		}
	}
	// 启动历史回放（D40/§7.4）：恢复的会话把水位→Head 的可见历史重放进转写区，
	// 让用户知道当前是哪个会话、此前聊了什么（新建会话无历史，no-op）。
	if _, rerr := w.sess.ReplayHistory(ctx); rerr != nil {
		// 这句话已经由 app 侧包好（`err.replay_history` 前缀），整句交给日志，不再套一层。
		w.noteText(logging.LevelWarn, logging.Session, "log.session.replay_failed",
			"log.act.continue", rerr.Error())
	}
	// replay 停止键中断后立刻要发的那句引导（D274）：有它就不等用户再敲一遍。
	return w.mainLoop(ctx)
}

// mainLoop 是 run() 的主输入循环。抽出来只搬循环本身：装配期登记的 defer 全部留在
// run() 里，退出时的后进先出顺序（先停前端、再收后台观察者）才不会被打乱。
func (w *wiring) mainLoop(ctx context.Context) int {
	var replay string
	for {
		var in port.UserInput
		if replay != "" {
			in = port.UserInput{Text: replay}
			replay = ""
		} else {
			w.ui.Prompt()
			var err error
			in, err = w.ui.Next(ctx)
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					return 0 // 收尾换行由 ui.Close 后的 defer 统一处理
				}
				w.note(logging.LevelError, logging.UI, "log.ui.input_failed",
					"log.act.exit", "err", err)
				return 1
			}
		}
		out, herr := func() (string, error) {
			// 每轮独立 ctx（§10 取消语义）：TUI 的 Ctrl+C 经 SetInterrupt 取消本轮；
			// repl 的 os.Interrupt 仍由外层 signal ctx 传导到本 ctx。
			hctx, hcancel := context.WithCancel(ctx)
			// 停止键：取消本轮的同时静音（D179：正在播报的回答不该继续念）。
			w.ui.SetInterrupt(func() {
				hcancel()
				w.silence()
			})
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
		// 停止键中断，而嘴边还有一句引导（D274）：不把它当成「本轮出错」，直接拿这句
		// 重新起一轮——用户按停止的意思是「先别跑这个，听我说这句话」，不是「我要走」。
		if ctx.Err() == nil && w.sess.Stopped() {
			if text, ok := w.sess.TakeSteer(); ok {
				w.ui.Say(i18n.T("cmd.boot.steer_retry"))
				replay = text
				continue
			}
		}
		if herr != nil {
			_ = w.ui.Emit(ctx, port.ErrorEvent{Err: herr})
			if ctx.Err() != nil {
				return 0
			}
		}
	}
}
