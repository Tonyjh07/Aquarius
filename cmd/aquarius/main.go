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
	"slices"
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

// providerSection 定位 model.providers 中指定名字的条目（D110②）：
// primary 缺省 = model.primary 键，再缺省 = 首个条目。未命中返回 nil。
func providerSection(generic map[string]any, name string) map[string]any {
	model := modelSection(generic)
	providers, _ := model["providers"].([]any)
	if len(providers) == 0 {
		return nil
	}
	target := name
	if target == "" {
		target, _ = model["primary"].(string)
	}
	for _, pv := range providers {
		p, ok := pv.(map[string]any)
		if !ok {
			continue
		}
		if n, _ := p["name"].(string); target == "" || n == target {
			return p
		}
	}
	return nil
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
			if werr := os.WriteFile(pointerPath, []byte(pointerTemplate), 0o644); werr != nil {
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

	// 数据目录（三级覆盖里的 flag 先于 env/config，DESIGN §8）。
	dir := *dataDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fatal(stderr, "无法确定家目录: %v", err)
		}
		dir = filepath.Join(home, ".aquarius")
	}
	// -data 允许相对路径，启动即锚定为绝对：对外展示的路径（沙盒提示、配置
	// 路径等）必须可直接复制使用，且不受后续"相对路径再次校验"拒绝。
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fatal(stderr, "创建数据目录 %s: %v", dir, err)
	}

	// profile 解析（D110①/修订②）：--profile > 根指针；根 config.json 仅存指针，
	// 会话/记忆/附件等数据全部落 profiles/<name>/ 整目录隔离（Q1）。
	profileName, err := resolveProfile(dir, *profileFlag)
	if err != nil {
		return fatal(stderr, "%v", err)
	}
	profileDir := filepath.Join(dir, "profiles", profileName)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return fatal(stderr, "创建 profile 目录 %s: %v", profileDir, err)
	}
	// 特权目录（D22）：<profile>/sandbox，权限矩阵的免确认 rw 格（随 profile 隔离）。
	sandboxDir := filepath.Join(profileDir, "sandbox")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return fatal(stderr, "创建特权目录 %s: %v", sandboxDir, err)
	}

	// profile 配置：不存在 = 该 profile 首次运行，写模板并指引填写后重跑。
	cfgPath := filepath.Join(profileDir, "config.json")
	cfg, err := loadConfig(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		if werr := writeDefaultConfig(cfgPath); werr != nil {
			return fatal(stderr, "生成配置失败: %v", werr)
		}
		fmt.Fprintf(stdout, "已生成配置: %s\n当前 profile: %s（指针: %s）\n"+
			"请编辑 model.providers（base_url / models / api_key），并设置密钥环境变量后重新运行。\n"+
			"  api_key 推荐引用格式 secret:<环境变量名>（例如 secret:AQUARIUS_OPENAI_KEY）；也可直接填明文（启动会警告，D35）\n",
			cfgPath, profileName, filepath.Join(dir, "config.json"))
		return 0
	}
	if err != nil {
		return fatal(stderr, "读取配置失败: %v", err)
	}

	// provider 列表校验与缺省归一（D110②，启动 fail-fast）：primary 缺省 = 首个，
	// model.name 缺省 = primary.models[0]。
	if err := cfg.Model.normalize(); err != nil {
		return fatal(stderr, "%v", err)
	}
	primary := cfg.Model.primaryProvider()

	// 环境变量与 flag 覆盖（flags > 环境变量 > config；D110 修订②：-model 覆盖
	// model.name，-base-url 覆盖 primary provider 的 base_url——调试逃生口）。
	if v := os.Getenv("AQUARIUS_MODEL"); v != "" {
		cfg.Model.Name = v
	}
	if v := os.Getenv("AQUARIUS_BASE_URL"); v != "" {
		primary.BaseURL = v
	}
	if *modelName != "" {
		cfg.Model.Name = *modelName
	}
	if *baseURL != "" {
		primary.BaseURL = *baseURL
	}
	// 思考参数启动校验（D34）：effort 档位与 /effort 命令共用 app.ParseEffort 口径。
	if raw := strings.TrimSpace(cfg.Model.ReasoningEffort); raw != "" {
		effort, err := app.ParseEffort(raw)
		if err != nil {
			return fatal(stderr, "%v", err)
		}
		cfg.Model.ReasoningEffort = effort
	}
	cfg.UI.Kind = uiKindDefault(cfg.UI.Kind)
	if cfg.UI.Kind != "repl" && cfg.UI.Kind != "tui" && cfg.UI.Kind != "gui" {
		return fatal(stderr, "ui.kind=%q 仅支持 repl | tui | gui（D33/D43）", cfg.UI.Kind)
	}
	if cfg.UI.Kind != "gui" {
		restoreConsole() // repl/tui 以终端为界面，双击启动也必须可见（D108）
	}
	// MCP 声明校验（D30/D31，启动 fail-fast）：transport/command/url/risk/名字合法。
	for name, srv := range cfg.MCPServers {
		if err := srv.Validate(name); err != nil {
			return fatal(stderr, "%v", err)
		}
	}

	// 权限等级（D22）：缺省 strict；非法值报因退出。
	level := perm.DefaultLevel
	if raw := strings.TrimSpace(cfg.Permissions.Level); raw != "" {
		level, err = perm.Parse(raw)
		if err != nil {
			return fatal(stderr, "%v", err)
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

	store, err := storejson.New(filepath.Join(profileDir, "conversations"))
	if err != nil {
		return fatal(stderr, "%v", err)
	}
	// 附件库（DESIGN §4.2）：sha256 内容寻址存 <profile>/attachments（随 profile
	// 隔离，D110 修订①）；启动 GC 按全量会话引用清扫（引用收集不完整则跳过，宁可漏清不误删）。
	blobs, err := blobfs.New(filepath.Join(profileDir, "attachments"))
	if err != nil {
		return fatal(stderr, "%v", err)
	}
	runAttachmentGC(ctx, store, blobs, stderr)
	// 记忆（D23 布局）：全局 memories.md + 会话 <id>.memory.md（与会话树同目录）。
	mem, err := memoryfs.New(filepath.Join(profileDir, port.GlobalMemoryDoc), filepath.Join(profileDir, "conversations"))
	if err != nil {
		return fatal(stderr, "%v", err)
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
	// 服务端不认的参数记录（D34/D110②）：LLM 适配器剥离重试成功后回调，写回 config
	// providers[<name>].unsupported_params（此后启动直接省略）；写回失败只记日志
	//（下次仍会剥离）。按 provider 名定位条目——fallback（D110②）会按 provider 各建
	// client，回调须记到产生剥离的那一条。
	noteUnsupportedFor := func(providerName string) func(string) {
		return func(field string) {
			fmt.Fprintf(stderr, "[llm] 服务端不支持参数 %s：已剥离重试，记入 config providers[%s].unsupported_params（D34）\n", field, providerName)
			if err := persistConfig(cfgPath, func(generic map[string]any) {
				p := providerSection(generic, providerName)
				if p == nil {
					return // providers 结构被外部改动：放弃记录，下次启动仍会剥离
				}
				list, _ := p["unsupported_params"].([]any)
				for _, v := range list {
					if v == field {
						return // 已记录
					}
				}
				p["unsupported_params"] = append(list, field)
			}); err != nil {
				fmt.Fprintf(stderr, "[llm] 记录 unsupported_params 失败: %v（下次启动仍会先试发该字段）\n", err)
			}
		}
	}
	// provider 客户端构建（D110②/修订③）：primary 与 fallback[] 各建一具——
	// base_url/api_key/tokenizer 构造期固化（port.LLM 不可热换）。密钥逐 provider
	// 解析（D35），含 fallback——避免降级时才发现缺配置。
	buildClient := func(p *providerConfig) (*llm.Client, port.TokenCounter, error) {
		key, kerr := resolveAPIKey(context.Background(), p.Name, p.APIKey, stderr)
		if kerr != nil {
			return nil, nil, kerr
		}
		c, cerr := llm.New(llm.Config{
			BaseURL: p.BaseURL, APIKey: key, Tokenizer: p.Tokenizer,
			UnsupportedParams: p.UnsupportedParams,
			NoteUnsupported:   noteUnsupportedFor(p.Name),
		})
		if cerr != nil {
			return nil, nil, cerr
		}
		var counter port.TokenCounter
		if cc, ok := any(c).(port.TokenCounter); ok {
			counter = cc
		}
		return c, counter, nil
	}
	client, primaryCounter, err := buildClient(primary)
	if err != nil {
		return fatal(stderr, "%v", err)
	}
	// 降级链条目（D110 修订③）：Model 空 = 透传 req.Model（primary——agent 每轮
	// 注入当前模型，热切换不失效）；降级目标 = 其 models[0]。
	fallbackEntries := []decorate.FallbackEntry{{Name: primary.Name, LLM: client}}
	fallbackCounters := map[string]port.TokenCounter{primary.Name: primaryCounter}
	for _, fbName := range cfg.Model.Fallback {
		p := cfg.Model.byName(fbName)
		c, counter, ferr := buildClient(p)
		if ferr != nil {
			return fatal(stderr, "%v", ferr)
		}
		fallbackEntries = append(fallbackEntries, decorate.FallbackEntry{
			Name: fbName, LLM: c, Model: p.Models[0],
		})
		fallbackCounters[fbName] = counter
	}
	// UI 前端（D33/D43/D51）：Gio GUI（默认，§15）、bubbletea TUI（ui.kind=tui）或
	// repl（行式，测试/e2e 后端）——同权实现 uiFrontend，换壳不换核（D28 输出器
	// 装饰器自动继承）。TUI 状态行回调经 atomic 读 Agent（构造晚于 UI 创建，
	// 事件循环并发读 → -race 必须）。
	var agentPtr atomic.Pointer[app.Agent]
	// sessPtr 会话原子槽（D80/§7.5）：UI（含分叉条）先于 Session 构造，故经原子槽
	// 间接取只读树视图——UI 事件循环 goroutine 可与构造并发调用（同 agentPtr 口径）。
	var sessPtr atomic.Pointer[app.Session]
	var ui uiFrontend
	// 设置窗读写（§15.7/D60-D61）：快照 = config 文本键（文件即事实源）+ 运行态
	//（等级活槽与 Agent 访问器，同状态行口径）；写 = 单一 patch 回调——文本键泛键
	// 改写 + 内核命令计划（设置窗经输入通道与键入同路径串行执行，壳内不旁路）。
	settingsSnapshot := func() uigui.SettingsSnapshot {
		s := readTextSettings(cfgPath)
		s.Permission = lvl.Get().String()
		if agent := agentPtr.Load(); agent != nil {
			s.Model = agent.CurrentModel()
			s.Think = agent.ThinkOn()
			s.Effort = agent.CurrentEffort()
		}
		return s
	}
	applySettings := func(p uigui.SettingsPatch) ([]port.Command, error) {
		if err := persistSettingsTextKeys(cfgPath, p); err != nil {
			return nil, fmt.Errorf("写回 config: %w", err)
		}
		return settingsCommands(settingsSnapshot(), p), nil
	}
	switch cfg.UI.Kind {
	case "gui":
		// GUI（D43/§15）：悬浮窗事件循环自驱；位置记忆落数据目录（§15.1）。
		// 不进 CI 图形路径（§15.5：GUI 测试全走 headless，门禁/e2e 仍 repl）。
		ui = uigui.New(uigui.Options{
			Status: func() uigui.Status {
				if agent := agentPtr.Load(); agent != nil {
					st := uigui.Status{
						Model:  agent.CurrentModel(),
						Level:  lvl.Get().String(),
						Effort: agent.CurrentEffort(), // D34：effort 档位（off 时为空）
						// D82：logo 事实卡（§15.1/S1-1g）——D110 起为真实 profile 名，
						// 会话事实取 app 侧发布快照（无锁原子读，§15.5 同口径）。
						Profile: profileName,
					}
					if sess := sessPtr.Load(); sess != nil {
						st.Facts = sess.Facts()
					}
					return st
				}
				return uigui.Status{}
			},
			Settings:      settingsSnapshot,
			ApplySettings: applySettings,
			Tree:          sessionTree{p: &sessPtr},                  // D80/§7.5：分叉条只读数据面
			Commands:      sessionCommands{p: &sessPtr},              // D103/S2b-1：补全浮层命令清单只读数据面
			PosFile:       filepath.Join(profileDir, "gui_pos.json"), // 位置记忆随 profile 走（D110 修订①）
			Hotkey:        cfg.UI.Hotkey,                             // 全局呼出快捷键（§15.1；空 = 默认 Alt+A）
			Theme:         cfg.UI.Theme,                              // 主题档 system|light|dark（§15.4/D61；空 = system）
			Scale:         cfg.UI.Scale,                              // 元素缩放倍率（D90/§15.8；0 = 1.0，UI 侧夹取）
			FontSize:      cfg.UI.FontSize,                           // 正文字号 sp（D90；0 = 15）
			WindowWidth:   cfg.UI.WindowWidth,                        // 主窗像素尺寸（D90；0 = 缺省 608×460dp）
			WindowHeight:  cfg.UI.WindowHeight,
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
	// 后台任务（DESIGN §5.8/D8）：日志落盘 <profile>/jobs，任务表内存态。
	jobs, err := jobproc.New(filepath.Join(profileDir, "jobs"))
	if err != nil {
		return fatal(stderr, "%v", err)
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
	audit, err := decorate.NewAudit(filepath.Join(profileDir, "audit.log"), 0)
	if err != nil {
		return fatal(stderr, "%v", err)
	}
	defer audit.Close()
	maxCtx := cfg.Limits.MaxContextTokens
	if maxCtx <= 0 {
		maxCtx = app.DefaultMaxContextTokens
	}
	// fit 闭包工厂：逐 provider 用各自 tokenizer 计数（fallback client 可配不同
	// tokenizer；est 链见上恒取 primary）。
	fitFor := func(cnt port.TokenCounter) func(context.Context, port.GenerateRequest) (port.GenerateRequest, int) {
		return func(fctx context.Context, req port.GenerateRequest) (port.GenerateRequest, int) {
			reserve := req.Budget.MaxOutputTokens
			if reserve < minOutputReserve {
				reserve = minOutputReserve
			}
			kept, omitted := app.TrimOldest(fctx, cnt, req.Messages, maxCtx-reserve, req.Tools...)
			req.Messages = kept
			return req, omitted
		}
	}
	truncateNotice := func(omitted int) {
		_ = presenter.Emit(ctx, port.NoticeEvent{
			Text: fmt.Sprintf("硬保底截断：上下文超预算，已省略 %d 条最旧消息", omitted),
		})
	}
	// 每 provider 一条内链：硬保底截断最内（发给服务端前裁到预算内）→ 审计（记每次
	// 真实调用）→ 同 provider 重试（外）；跨 provider 降级（D110②/Q5/修订③）罩全体
	// 最外——重试在内、降级在外（铁律 9：装饰器只在装配根叠加）。
	stacked := make([]decorate.FallbackEntry, 0, len(fallbackEntries))
	for _, e := range fallbackEntries {
		var inner port.LLM = e.LLM
		inner = decorate.NewTruncate(inner, fitFor(fallbackCounters[e.Name]), truncateNotice)
		inner = decorate.AuditLLM(inner, audit)
		inner = decorate.NewRetry(inner)
		stacked = append(stacked, decorate.FallbackEntry{Name: e.Name, LLM: inner, Model: e.Model})
	}
	var gen port.LLM = stacked[0].LLM
	if len(stacked) > 1 {
		gen = decorate.NewFallback(stacked, func(from, to string) {
			_ = presenter.Emit(ctx, port.NoticeEvent{
				Text: fmt.Sprintf("模型服务 %s 网络类错误，本轮已降级到 %s（Q5：状态行一次性提示）", from, to),
			})
		})
	}
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
		return fatal(stderr, "%v", err)
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
		PluginsDir:    filepath.Join(profileDir, "plugins"),
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
		ProviderName:  func() string { return primary.Name }, // D110②：/model 展示（静态，重启生效）
		PersistModel:  persistModel,
		PersistThink:  persistThink,  // D34
		PersistEffort: persistEffort, // D34
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
	sessPtr.Store(sess) // D80/§7.5：会话就绪后发布只读树视图（UI 侧分叉条数据面）

	// 插件宿主启动（发现 + 授权 + 连接，软启动：单插件失败只记状态）；
	// 退出时优雅关闭（§6.4 #4）。OnChange 已在 Start 内把工具/动态命令同步到位。
	host.Start(ctx)
	defer host.Close()

	// 启动提示：GUI 做成 logo 悬浮 tips（§15.1，不入转写区、初始即空）；
	// repl/TUI 照常一行。
	if cfg.UI.Kind != "gui" {
		ui.Say("Aquarius — 输入 /help 查看命令，/quit 退出；Ctrl+C 取消当前生成")
	}
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

func (yesConfirmer) Confirm(context.Context, string) (port.ConfirmAnswer, error) {
	return port.ConfirmAnswer{Allow: true}, nil
}

// systemIDGen port.IDGen 的内置实现：复用 domain 的进程内单调 ULID。
type systemIDGen struct{}

var _ port.IDGen = systemIDGen{}

func (systemIDGen) ConversationID() conversation.ID   { return conversation.NewID() }
func (systemIDGen) MessageID() conversation.MessageID { return conversation.NewMessageID() }
func (systemIDGen) CallID() tool.CallID               { return tool.CallID(conversation.NewMessageID()) }
