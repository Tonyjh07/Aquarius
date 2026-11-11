package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uicommon"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/logging"
)

// errFirstRun 首次运行哨兵：bootstrap 已写好 profile 配置模板并打印指引，
// run 据此按退出码 0 结束（沿袭首跑契约：生成配置后重跑）。哨兵文本只做控制流
// （errors.Is 判定），不展示给用户，故不随界面语言变。
var errFirstRun = errors.New("bootstrap: first run, config template written")

// resolveDataDir 解析数据根目录：flag > ~/.aquarius（三级覆盖里的 flag 先于
// env/config，§8）。
func resolveDataDir(dataDir string) (string, error) {
	dir := dataDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%s: %w", i18n.T("cmd.boot.err.home_dir"), err)
		}
		dir = filepath.Join(home, ".aquarius")
	}
	// -data 允许相对路径，启动即锚定为绝对：对外展示的路径（沙盒提示、配置
	// 路径等）必须可直接复制使用，且不受后续"相对路径再次校验"拒绝。
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.Tf("cmd.boot.err.mkdir_data", "path", dir), err)
	}
	return dir, nil
}

// bootstrap 完成 profile 解析与 profile 配置加载（D110①/修订②）：--profile > 根
// 指针；根 config.json 仅存指针，会话/记忆/附件等数据全部落 profiles/<name>/
// 整目录隔离（Q1）。成功后填好 wiring 的 profile/配置/权限字段；该 profile 首次
// 运行写模板并置 firstRun——**gui 形态继续装配**（缺项由 run() 的欢迎向导窗接管，
// D141），tui/repl 打完指引返回 errFirstRun（run 按退出码 0 结束）。
func (w *wiring) bootstrap(dataDir, profileFlag, modelName, baseURL string) error {
	// D141：同一进程内本函数可能被调用两次（装配前向导保存后重读配置）——首跑标记
	// 每次重算，否则第二次会再报一遍「已生成配置」。
	w.firstRun = false
	dir, err := resolveDataDir(dataDir)
	if err != nil {
		return err
	}
	w.dir = dir
	// 数据目录说明（D291）：写完这一步任何 profile 都还没碰，换 profile 也只需重写一次。
	// 它只是给人看的手册，写不出来不拦启动。
	w.writeDataReadme()

	profileName, err := resolveProfile(dir, profileFlag)
	if err != nil {
		return err
	}
	w.profileName = profileName
	profileDir := filepath.Join(dir, "profiles", profileName)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return fmt.Errorf("%s: %w", i18n.Tf("cmd.boot.err.mkdir_profile", "path", profileDir), err)
	}
	w.profileDir = profileDir
	// 特权目录（D22）：<profile>/sandbox，权限矩阵的免确认 rw 格（随 profile 隔离）。
	sandboxDir := filepath.Join(profileDir, "sandbox")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return fmt.Errorf("%s: %w", i18n.Tf("cmd.boot.err.mkdir_sandbox", "path", sandboxDir), err)
	}
	w.sandboxDir = sandboxDir
	// 技能兜底根（D201）：<profile>/skills 存在即生效（用户把技能目录放这儿最省事）；
	// 预先建好空目录，用户不必自己找路径。
	skillsDir := filepath.Join(profileDir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return fmt.Errorf("%s: %w", i18n.Tf("cmd.boot.err.mkdir_skills", "path", skillsDir), err)
	}

	// 本机密钥库（D151/D161）：密钥库落在 profile 内（随 profile 隔离），读链 = 库 → 同名
	// 环境变量；装配前向导窗（setupWizard）要用它探测缺密钥，故在此就位——profileDir 一
	// 定，两处（探测与真实构造）就是同一份链。库解不开不阻断：警告 + 环境变量兜底。
	bin, err := secretbin.New(profileDir)
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("cmd.boot.err.secret_store"), err)
	}
	w.secretsBin = bin
	w.secrets = newSecretChain(bin, w.secretsWarn)

	// profile 配置：不存在 = 该 profile 首次运行，写模板。D141：**gui 不再「写模板即退出」**
	// ——模板落盘后照常装配，配置缺项（模板的密钥引用通常取不到）由 run() 的欢迎向导窗
	// 接管；tui/repl 保持旧契约（控制台已恢复、指引可见 ⇒ 打印指引 + errFirstRun，见本节末）。
	cfgPath := filepath.Join(profileDir, "config.json")
	w.cfgPath = cfgPath
	cfg, err := loadConfig(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		if werr := writeDefaultConfig(cfgPath); werr != nil {
			return fmt.Errorf("%s: %w", i18n.T("cmd.boot.err.write_config"), werr)
		}
		w.firstRun = true
		if cfg, err = loadConfig(cfgPath); err != nil {
			return fmt.Errorf("%s: %w",
				i18n.Tf("cmd.boot.err.read_new_config", "path", cfgPath), err)
		}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("cmd.boot.err.read_config"), err)
	}

	// provider 列表校验与缺省归一（D173，启动 fail-fast）：primary 缺省 = 首个，
	// model.name 缺省 = primary.models[0]。
	if err := cfg.Model.normalize(); err != nil {
		return err
	}
	if err := cfg.normalizeAudio(); err != nil {
		return err
	}
	if err := cfg.normalizeWeb(); err != nil {
		return err
	}
	if err := cfg.normalizeSkills(); err != nil {
		return err
	}
	// 模型辅助决策（D290）：未知 mode 与越界阈值在这里就拦下；端点缺失留到装配根。
	if opts, err := cfg.decideOptions(); err != nil {
		return err
	} else {
		w.decide = opts
	}
	// D279/§7.8：占位文字折成单行；超长截断（保存路径拒写，这里只保证启动能起来）。
	cfg.UI.InputPlaceholder = placeholderForStartup(cfg.UI.InputPlaceholder)
	// 语音不可用的原因只在「用户明显想用」时提示：配了 provider 却找不到，或开了
	// 播报却没配置。完全没碰语音的配置不该每次启动都挨一行警告。
	// （这一句与下面两句都在 openLog 之前：先排队，日志开了补记，见 wiring.note。）
	if _, why := cfg.audioProvider("tts"); why != "" && cfg.Output.TTS {
		w.note(logging.LevelWarn, logging.Audio, "log.audio.tts_unconfigured",
			"log.act.voice_off", "why", why)
	}
	// 本地识别不需要端点与密钥，这里只提示「配了却连不上」的那种。
	if cfg.Audio.ASR.Kind != kindASRWhisper {
		if _, why := cfg.audioProvider("asr"); why != "" && cfg.Audio.ASR.configured() {
			w.note(logging.LevelWarn, logging.Audio, "log.audio.asr_unavailable",
				"log.act.voice_input_off", "why", why)
		}
	}
	w.cfg = cfg
	primary := cfg.Model.primaryProvider()
	w.primary = primary

	// 环境变量与 flag 覆盖（flags > 环境变量 > config；D110 修订②：-model 覆盖
	// model.name，-base-url 覆盖 primary provider 的 base_url——调试逃生口）。
	if v := os.Getenv("AQUARIUS_MODEL"); v != "" {
		cfg.Model.Name = v
	}
	// 空态（D278 决策 3）：没有 primary 就没有可覆盖的 base_url——这是调试逃生口，
	// 不是「新建一家 provider」的入口（那要走设置窗或向导）。
	if v := os.Getenv("AQUARIUS_BASE_URL"); v != "" && primary != nil {
		primary.BaseURL = v
	}
	if modelName != "" {
		cfg.Model.Name = modelName
	}
	if baseURL != "" && primary != nil {
		primary.BaseURL = baseURL
	}
	// 能力位警告放在覆盖之后：-model / AQUARIUS_MODEL 可能换成链上另一个模型，
	// 生效能力随之变化，警告得按最终生效的那个算（D173）。整句由 narrowNote 给（设置窗
	// 的降级说明与这里同源），所以走 noteText：句子本身就是事件，不再套一层 `警告：`。
	if note := cfg.Model.narrowNote(); note != "" {
		w.noteText(logging.LevelWarn, logging.LLM, "log.llm.chain_narrowed",
			"log.act.continue", note)
	}
	// 思考参数启动校验（D34）：effort 档位与 /effort 命令共用 app.ParseEffort 口径。
	if raw := strings.TrimSpace(cfg.Model.ReasoningEffort); raw != "" {
		effort, err := app.ParseEffort(raw)
		if err != nil {
			return err
		}
		cfg.Model.ReasoningEffort = effort
	}
	cfg.UI.Kind = uiKindDefault(cfg.UI.Kind)
	if cfg.UI.Kind != "repl" && cfg.UI.Kind != "tui" && cfg.UI.Kind != "gui" {
		return fmt.Errorf("%s", i18n.Tf("cmd.boot.err.ui_kind", "kind", cfg.UI.Kind))
	}
	// 界面语言（D275/§7.8）：环境变量 > ui.lang > auto。非法值启动即报（与 ui.kind 同口径：
	// 等界面出来了才发现语言没生效，比启动失败难查）。定一次就写进取词口，之后整支进程
	// 不再变——文案在构造期散进各视图与测量，热切换等于重建整个壳。
	sel, err := i18n.Parse(uiLangSetting(os.Getenv("AQUARIUS_UI_LANG"), cfg.UI.Lang))
	if err != nil {
		return err
	}
	w.lang = i18n.Use(sel)
	// 输入栏三键（§5.19/D272）：非法键名与撞键在启动时报因——等用户按下那个键才发现没
	// 反应，比启动失败难查得多。
	keys, err := uicommon.ParseKeyBindings(cfg.UI.Keys.Send, cfg.UI.Keys.Steer, cfg.UI.Keys.Newline)
	if err != nil {
		return err
	}
	w.keys = keys
	if cfg.UI.Kind != "gui" {
		if restoreConsole() { // repl/tui 以终端为界面，双击启动也必须可用（D108/D114）
			// D114：脱离后原句柄失效，重挂后的控制台流经 wiring 下发（前端与编辑器子进程）。
			w.stdin, w.stdout, w.stderr = os.Stdin, os.Stdout, os.Stderr
		}
	}
	// MCP 声明校验（D30/D31，启动 fail-fast）：transport/command/url/risk/名字合法。
	for name, srv := range cfg.MCPServers {
		if err := srv.Validate(name); err != nil {
			return err
		}
	}

	// 权限等级（D22）：缺省 strict；非法值报因退出。
	level := perm.DefaultLevel
	if raw := strings.TrimSpace(cfg.Permissions.Level); raw != "" {
		level, err = perm.Parse(raw)
		if err != nil {
			return err
		}
	}
	w.level = level

	// 权限等级活状态（D22 执行接入）：persistLevel 写回成功后同步，ToolRunner 经 func 读取。
	// atomic 存取（审查修复）：TUI 状态行在事件循环 goroutine 读、/permission 在主
	// goroutine 写——D33 之后"单 goroutine"前提不再成立。
	w.lvl = newLevelHolder(level)
	// 例外表（D280）：与等级同一处解析，拼错即退出。
	overrides, perr := parsePolicyOverrides(cfg.Permissions.Overrides)
	if perr != nil {
		return perr
	}
	w.policy = newPolicyHolder(overrides)
	w.unattended = newUnattendedHolder(cfg.Permissions.Unattended)
	w.persist = &cfgWriter{path: cfgPath, lvl: w.lvl, unattended: w.unattended}

	// D141：首跑落模板的可见回执（gui 双击时只控制台可见；指引全文只在 tui/repl 打）。
	// gui 继续装配——缺密钥由 run() 的向导窗接管；tui/repl 保持旧契约（指引 + 退出 0）。
	if w.firstRun {
		fmt.Fprint(w.stdout, i18n.Tf("boot.config_written",
			"path", cfgPath, "profile", profileName, "pointer", filepath.Join(dir, "config.json")))
		if cfg.UI.Kind != "gui" {
			fmt.Fprint(w.stdout, i18n.T("boot.first_run.hint"))
			return errFirstRun
		}
	}
	return nil
}

// openLog 开运行日志（D276/§7.9）：级别 `-log-level` > `AQUARIUS_LOG_LEVEL` > `ui.log_level`，
// 形态只认 `--log-format`（排查时才切，不落配置）。落在 profile 自己的 logs/ 下，与
// `audit.log`、`jobs/` 同一隔离口径；开不出文件不拦启动（logging.Open 降级到 stderr 并记一条）。
// 终端回显（D288）只给 gui：从终端启动时整份日志同时打到那块屏幕，双击启动没有屏幕可打；
// tui/repl 的终端本身就是界面，插日志会破屏。
func (w *wiring) openLog(levelFlag, formatFlag string) error {
	level, err := logging.ParseLevel(logLevelSetting(levelFlag, os.Getenv("AQUARIUS_LOG_LEVEL"), w.cfg.UI.LogLevel))
	if err != nil {
		return err
	}
	format, err := logging.ParseFormat(formatFlag)
	if err != nil {
		return err
	}
	opts := logging.Options{
		Dir:    filepath.Join(w.profileDir, "logs"),
		Level:  level,
		Format: format,
		Stderr: stderrProxy{w: w},
	}
	if w.cfg.UI.Kind == "gui" && consoleVisible() {
		opts.Echo = stdoutProxy{w: w}
	}
	w.log = logging.Open(opts)
	// bootstrap 段攒下的启动告警（级别刚从同一份配置里读出来）：按原顺序补记，只有够格的
	// 才会上 stderr——顺序与「就地打一行」时一致。
	for _, p := range w.pendingLog {
		w.log.Write(p.level, p.ctx, p.event, p.action, p.msg, p.kv...)
	}
	w.pendingLog = nil
	return nil
}
