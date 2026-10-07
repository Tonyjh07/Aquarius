package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
)

// errFirstRun 首次运行哨兵：bootstrap 已写好 profile 配置模板并打印指引，
// run 据此按退出码 0 结束（沿袭首跑契约：生成配置后重跑）。
var errFirstRun = errors.New("bootstrap: 首次运行，已生成配置模板")

// resolveDataDir 解析数据根目录：flag > ~/.aquarius（三级覆盖里的 flag 先于
// env/config，DESIGN §8）。
func resolveDataDir(dataDir string) (string, error) {
	dir := dataDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法确定家目录: %w", err)
		}
		dir = filepath.Join(home, ".aquarius")
	}
	// -data 允许相对路径，启动即锚定为绝对：对外展示的路径（沙盒提示、配置
	// 路径等）必须可直接复制使用，且不受后续"相对路径再次校验"拒绝。
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建数据目录 %s: %w", dir, err)
	}
	return dir, nil
}

// bootstrap 完成 profile 解析与 profile 配置加载（D110①/修订②）：--profile > 根
// 指针；根 config.json 仅存指针，会话/记忆/附件等数据全部落 profiles/<name>/
// 整目录隔离（Q1）。成功后填好 wiring 的 profile/配置/权限字段；该 profile 首次
// 运行时写模板并返回 errFirstRun。
func (w *wiring) bootstrap(dataDir, profileFlag, modelName, baseURL string) error {
	dir, err := resolveDataDir(dataDir)
	if err != nil {
		return err
	}
	w.dir = dir

	profileName, err := resolveProfile(dir, profileFlag)
	if err != nil {
		return err
	}
	w.profileName = profileName
	profileDir := filepath.Join(dir, "profiles", profileName)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return fmt.Errorf("创建 profile 目录 %s: %w", profileDir, err)
	}
	w.profileDir = profileDir
	// 特权目录（D22）：<profile>/sandbox，权限矩阵的免确认 rw 格（随 profile 隔离）。
	sandboxDir := filepath.Join(profileDir, "sandbox")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return fmt.Errorf("创建特权目录 %s: %w", sandboxDir, err)
	}
	w.sandboxDir = sandboxDir

	// profile 配置：不存在 = 该 profile 首次运行，写模板并指引填写后重跑。
	cfgPath := filepath.Join(profileDir, "config.json")
	w.cfgPath = cfgPath
	cfg, err := loadConfig(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		if werr := writeDefaultConfig(cfgPath); werr != nil {
			return fmt.Errorf("生成配置失败: %w", werr)
		}
		fmt.Fprintf(w.stdout, "已生成配置: %s\n当前 profile: %s（指针: %s）\n"+
			"请编辑 model.providers（base_url / models / api_key），并设置密钥环境变量后重新运行。\n"+
			"  api_key 推荐引用格式 secret:<环境变量名>（例如 secret:AQUARIUS_OPENAI_KEY）；也可直接填明文（启动会警告，D35）\n",
			cfgPath, profileName, filepath.Join(dir, "config.json"))
		return errFirstRun
	}
	if err != nil {
		return fmt.Errorf("读取配置失败: %w", err)
	}

	// provider 列表校验与缺省归一（D110②，启动 fail-fast）：primary 缺省 = 首个，
	// model.name 缺省 = primary.models[0]。
	if err := cfg.Model.normalize(); err != nil {
		return err
	}
	w.cfg = cfg
	primary := cfg.Model.primaryProvider()
	w.primary = primary

	// 环境变量与 flag 覆盖（flags > 环境变量 > config；D110 修订②：-model 覆盖
	// model.name，-base-url 覆盖 primary provider 的 base_url——调试逃生口）。
	if v := os.Getenv("AQUARIUS_MODEL"); v != "" {
		cfg.Model.Name = v
	}
	if v := os.Getenv("AQUARIUS_BASE_URL"); v != "" {
		primary.BaseURL = v
	}
	if modelName != "" {
		cfg.Model.Name = modelName
	}
	if baseURL != "" {
		primary.BaseURL = baseURL
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
		return fmt.Errorf("ui.kind=%q 仅支持 repl | tui | gui（D33/D43）", cfg.UI.Kind)
	}
	if cfg.UI.Kind != "gui" {
		restoreConsole() // repl/tui 以终端为界面，双击启动也必须可见（D108）
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
	w.persist = &cfgWriter{path: cfgPath, lvl: w.lvl}
	return nil
}
