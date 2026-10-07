package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
)

// ErrLegacyConfig D110 前的旧单文件 config（沿 storejson ErrLegacyFormat 口径：
// 显式识别报因、绝不静默当零值解析、不做自动迁移——旧文件留存原地由用户手工处置）。
var ErrLegacyConfig = errors.New("检测到旧格式 config（D110 前）")

// defaultProfileName 首次运行创建的 profile 名（指针缺省指向它）。
const defaultProfileName = "default"

// legacyConfigKeys D110 前单文件 config 的顶层键：根 config.json 任一出现即判旧格式
// （encoding/json 对未知键静默零值——旧文件「看起来正常」地丢光配置，必须显式识别，D95 同口径）。
var legacyConfigKeys = []string{
	"model", "ui", "mcpServers", "plugins", "permissions",
	"limits", "system_prompt", "output", "input",
}

// fileConfig config.json 中当前里程碑消费的部分。
// DESIGN §8 的其余键（input 等）同样写入模板但由对应里程碑启用，
// encoding/json 对未知键宽容。
type fileConfig struct {
	Model        modelConfig       `json:"model"`
	UI           uiConfig          `json:"ui"`
	SystemPrompt string            `json:"system_prompt"` // 人格：空 = 内置默认（快照进 persona 首节点，D20）
	Permissions  permissionsConfig `json:"permissions"`
	Output       outputConfig      `json:"output"`
	Limits       limitsConfig      `json:"limits"`
	// MCPServers MCP server 声明（D30/D31，M4 启用；校验见 plugin.MCPServer.Validate）。
	MCPServers map[string]plugin.MCPServer `json:"mcpServers"`
	// Plugins 启停与授权状态（D31，config 与 plugin.json 两种发现源共用）。
	Plugins map[string]plugin.State `json:"plugins"`
}

// outputConfig 输出器开关（DESIGN §8；M3 启用 notify，tts 见 D27 解析但不启用）。
// 键缺失 = false（保守：老配置不自动弹通知，模板默认 notify=true）。
type outputConfig struct {
	Notify bool `json:"notify"`
	TTS    bool `json:"tts"`
}

// permissionsConfig 权限配置（D22：仅等级预设；allow/ask 明细键已移除，矩阵外一律 ask）。
type permissionsConfig struct {
	Level string `json:"level"` // 空 = strict（perm.DefaultLevel）
}

// providerConfig 单个 provider 接入（D110②，openai-compatible 端点一套连接参数）。
type providerConfig struct {
	// Name provider 唯一标识（model.primary / model.fallback 按名引用）。
	Name string `json:"name"`
	// BaseURL openai-compatible 端点地址。
	BaseURL string `json:"base_url"`
	// APIKey "secret:<环境变量名>" 引用（port.Secrets 按名取用）或明文（D35：
	// 启动打印警告、不回显）；空值回落默认 secret:AQUARIUS_OPENAI_KEY。逐 provider 独立。
	APIKey string `json:"api_key"`
	// Models 可用模型清单（advisory：/model 与设置窗补全用；首个 = 该 provider
	// 的缺省请求模型——fallback 降级时使用，D110 修订③）。
	Models []string `json:"models"`
	// Tokenizer 本地 tokenizer.json 路径（精确计数②，D26）；空 = 通用估算③。
	Tokenizer string `json:"tokenizer"`
	// UnsupportedParams 服务端已知不认的请求参数（D34 自动记录，启动注入省略）。
	UnsupportedParams []string `json:"unsupported_params,omitempty"`
}

// modelConfig 模型接入配置（D110② provider 列表化：原单 provider 平铺字段
// provider/base_url/api_key/tokenizer/unsupported_params 随批退役）。
type modelConfig struct {
	// Providers provider 列表，至少一条。
	Providers []providerConfig `json:"providers"`
	// Primary 当前生效 provider（providers 中的 name；缺省 = 首个）。
	Primary string `json:"primary,omitempty"`
	// Fallback 有序降级顺序（Q5/D110 修订③：仅网络类错误触发；名字须在 providers
	// 内、不含 primary 与重复项）。
	Fallback []string `json:"fallback,omitempty"`
	// Name 当前模型名（在 primary 的 models 内；缺省 = primary.models[0]）。
	Name string `json:"name"`
	// Think 原生思考总开关（/think 写回，D34）；nil = 键缺失（展示为开、不发布尔）。
	Think *bool `json:"think,omitempty"`
	// ReasoningEffort 推理档位（/effort 写回，D34）；空 = 不发送。
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ThinkTool think 草稿工具可见性（D34）；默认 false（隐藏），改后重启生效。
	ThinkTool bool `json:"think_tool,omitempty"`
	// EchoThinking 思考回传开关（D42）；nil = 键缺失 = 回传（DeepSeek 等兼容端点带
	// tools 时强制回传 reasoning_content，缺失即 400）；显式 false = 不回传，改后重启生效。
	EchoThinking *bool `json:"echo_thinking,omitempty"`
}

// byName 按名取 provider（未命中返回 nil）。
func (m *modelConfig) byName(name string) *providerConfig {
	for i := range m.Providers {
		if m.Providers[i].Name == name {
			return &m.Providers[i]
		}
	}
	return nil
}

// primaryProvider 当前生效 provider（normalize 校验后调用）。
func (m *modelConfig) primaryProvider() *providerConfig {
	if p := m.byName(m.Primary); p != nil {
		return p
	}
	return &m.Providers[0]
}

// normalize 应用缺省值并 fail-fast 校验 model 段（D110②，启动期调用）：
// primary 缺省 = providers 首个；model.name 缺省 = primary.models[0]。
func (m *modelConfig) normalize() error {
	if len(m.Providers) == 0 {
		return fmt.Errorf("model.providers 不可为空（D110②：provider 列表化，原平铺 base_url/api_key 已退役；" +
			"若为旧格式请手工迁移，见 docs/configuration.md）")
	}
	seen := map[string]bool{}
	for i := range m.Providers {
		p := &m.Providers[i]
		if p.Name == "" {
			return fmt.Errorf("model.providers[%d].name 不可为空", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("model.providers[%d].name %q 重复", i, p.Name)
		}
		seen[p.Name] = true
		if p.BaseURL == "" {
			return fmt.Errorf("model.providers[%d] (%s) 的 base_url 不可为空", i, p.Name)
		}
		if len(p.Models) == 0 || p.Models[0] == "" {
			return fmt.Errorf("model.providers[%d] (%s) 的 models 不可为空（首个 = 缺省请求模型）", i, p.Name)
		}
	}
	if m.Primary == "" {
		m.Primary = m.Providers[0].Name
	}
	if m.byName(m.Primary) == nil {
		return fmt.Errorf("model.primary %q 不在 model.providers 内", m.Primary)
	}
	fbSeen := map[string]bool{}
	for _, fb := range m.Fallback {
		if fb == m.Primary {
			return fmt.Errorf("model.fallback 不应含当前 provider %q", fb)
		}
		if fbSeen[fb] {
			return fmt.Errorf("model.fallback 重复项 %q", fb)
		}
		fbSeen[fb] = true
		if m.byName(fb) == nil {
			return fmt.Errorf("model.fallback %q 不在 model.providers 内", fb)
		}
	}
	if m.Name == "" {
		m.Name = m.primaryProvider().Models[0]
	}
	return nil
}

// uiConfig UI 形态：repl | tui | gui（D33/D43；模板与键缺省为 tui，repl 为测试/e2e
// 后端）+ GUI 全局呼出快捷键 + 主题档 + 缩放/字号/窗口尺寸三旋钮（D90，全热生效）。
type uiConfig struct {
	Kind string `json:"kind"`
	// Hotkey GUI 全局呼出快捷键（§15.1，如 "Alt+A"）；空 = 默认 Alt+A。仅 ui.kind=gui 用。
	Hotkey string `json:"hotkey,omitempty"`
	// Theme GUI 深浅主题（§15.4/D61）：system | light | dark；空 = system（跟随系统，
	// 其余未识别值亦按 system 处理）。仅 ui.kind=gui 用。
	Theme string `json:"theme,omitempty"`
	// Scale 元素缩放倍率（D90/§15.8）：默认 1.0；UI 侧夹 [0.75,2.5]，越界回落 1.0。
	Scale float64 `json:"scale,omitempty"`
	// FontSize 正文字号 sp（D90/§15.8）：默认 15；UI 侧夹 [10,28]，最终字号 = FontSize × Scale。
	FontSize float64 `json:"font_size,omitempty"`
	// WindowWidth/WindowHeight 主窗像素尺寸（D90/§15.8）：0 = 缺省 608×460dp 现行为；
	// 配置后按字面 px 建窗，UI 侧按布局地板与粗界夹取。
	WindowWidth  int `json:"window_width,omitempty"`
	WindowHeight int `json:"window_height,omitempty"`
}

// limitsConfig 运行限额（DESIGN §8）。
type limitsConfig struct {
	MaxContextTokens int     `json:"max_context_tokens"`
	MaxTurns         int     `json:"max_turns"`
	CompactThreshold float64 `json:"compact_threshold"` // 自动压缩阈值（D21 轨2，M2）
	ToolOutputChars  int     `json:"tool_output_chars"` // 工具结果截断（ToolRunner，M2）
	ToolTimeoutSec   int     `json:"tool_timeout_sec"`  // 工具超时秒（ToolRunner，M2）
}

// defaultConfig 首次运行写入的模板：DESIGN §8 示例的可运行子集
// （ui.kind 取 gui——D51 模板默认 = 悬浮球前端，tui 显式指定、repl 为测试/e2e
// 后端；mcpServers/plugins 为 M4 MCP 接入的声明与状态，缺省皆空）。
const defaultConfig = `{
  "model": {
    "providers": [
      { "name": "openai", "base_url": "https://api.openai.com/v1",
        "api_key": "secret:AQUARIUS_OPENAI_KEY",
        "models": ["gpt-4o-mini"], "tokenizer": "",
        "unsupported_params": [] }
    ],
    "primary": "openai",
    "fallback": [],
    "name": "gpt-4o-mini",
    "think": true,
    "reasoning_effort": "",
    "think_tool": false,
    "echo_thinking": true
  },
  "ui": { "kind": "gui", "theme": "system", "scale": 1.0, "font_size": 15, "window_width": 0, "window_height": 0 },
  "system_prompt": "",
  "input": { "asr": "whisper-api", "mic": true },
  "output": { "tts": false, "notify": true },
  "mcpServers": {},
  "plugins": {},
  "permissions": { "level": "strict" },
  "limits": {
    "max_turns": 8,
    "max_context_tokens": 64000,
    "compact_threshold": 0.7,
    "tool_output_chars": 20000,
    "tool_timeout_sec": 60
  }
}
`

// loadConfig 读取并解析配置；model 段为 D110 前单 provider 平铺字段时显式报
// ErrLegacyConfig（detectLegacyModelConfig：Unmarshal 对旧键静默零值，必须先判别）。
func loadConfig(path string) (*fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	if err := detectLegacyModelConfig(path, data); err != nil {
		return nil, err
	}
	var cfg fileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	return &cfg, nil
}

// detectLegacyModelConfig 旧单 provider 平铺字段判别（D110② 破坏性）：model 段含
// 平铺 base_url/api_key/tokenizer/provider 且无 providers → 报 ErrLegacyConfig 与
// 手工改写指引（D95 影子结构同口径，绝不静默丢配置）。
func detectLegacyModelConfig(path string, data []byte) error {
	var probe struct {
		Model struct {
			Providers []json.RawMessage `json:"providers"`
			BaseURL   string            `json:"base_url"`
			APIKey    string            `json:"api_key"`
			Tokenizer string            `json:"tokenizer"`
			Provider  string            `json:"provider"`
		} `json:"model"`
	}
	if err := json.Unmarshal(data, &probe); err != nil || len(probe.Model.Providers) > 0 {
		return nil // 非 JSON / 无 model 段 / 已是新格式：交由后续解析与校验处置
	}
	m := probe.Model
	if m.BaseURL == "" && m.APIKey == "" && m.Tokenizer == "" && m.Provider == "" {
		return nil
	}
	return fmt.Errorf("%w：%s 的 model 段为单 provider 平铺字段（检测到 base_url/api_key/tokenizer/provider）。\n"+
		"D110② 起 provider 列表化（DESIGN §8）：\n"+
		"  \"model\": { \"providers\": [ { \"name\": \"openai\", \"base_url\": \"…\",\n"+
		"    \"api_key\": \"secret:…\", \"models\": [\"…\"], \"tokenizer\": \"\", \"unsupported_params\": [] } ],\n"+
		"    \"primary\": \"openai\", \"fallback\": [], \"name\": \"…\", … }\n"+
		"请手工改写（不做自动迁移），原平铺字段退役；字段说明见 docs/configuration.md。",
		ErrLegacyConfig, path)
}

// writeDefaultConfig 落首次运行模板。
func writeDefaultConfig(path string) error {
	return os.WriteFile(path, []byte(defaultConfig), 0o644)
}

// pointerTemplate 根 config.json 的指针模板（D110①：仅存当前 profile 名）。
const pointerTemplate = `{
  "profile": "default"
}
`

// pointerContent 指定 profile 名的指针文件内容（切换面写回用，D110③）。
func pointerContent(name string) string {
	return fmt.Sprintf("{\n  \"profile\": %q\n}\n", name)
}

// validProfileName profile 名合法性（目录名安全子集）：非空、无路径分隔/盘符/控制
// 字符、非 . ..、长度 ≤64。指针解析与设置窗新建/复制共用，防手改与注入。
func validProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("profile 名不可为空")
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("profile 名首尾不可含空白: %q", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("profile 名非法: %q", name)
	}
	if strings.ContainsAny(name, `/\:`) || len(name) > 64 {
		return fmt.Errorf("profile 名不可含 / \\ : 且长度 ≤64: %q", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("profile 名不可含控制字符: %q", name)
		}
	}
	return nil
}

// parsePointerFile 解析根 config.json 指针（D110①），返回当前 profile 名。
// 旧单文件 config 显式判别报 ErrLegacyConfig 并给出手工迁移步骤——普通 Unmarshal
// 会把旧键静默丢成零值指针，「看起来正常」地换到空 profile，绝不允许（D95 同口径）。
func parsePointerFile(path string, data []byte) (string, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("解析 %s: %w", path, err)
	}
	for _, k := range legacyConfigKeys {
		if _, ok := raw[k]; ok {
			return "", fmt.Errorf("%w：%s 含旧格式键 %q。\n"+
				"D110 起 profile 目录化：根 config.json 仅存当前 profile 指针（{\"profile\":\"<name>\"}），\n"+
				"主配置落 profiles/<name>/config.json。请手工处置（不做自动迁移），例如迁移到 %s profile：\n"+
				"  1. 把 %s 移动为 %s\n"+
				"  2. 新建根 %s，内容：{\"profile\": \"%s\"}\n"+
				"若该 profile 已存在请改用其他名字或手工合并；字段说明见 docs/configuration.md。",
				ErrLegacyConfig, path, k, defaultProfileName, path,
				filepath.Join(filepath.Dir(path), "profiles", defaultProfileName, "config.json"),
				path, defaultProfileName)
		}
	}
	v, ok := raw["profile"]
	if !ok {
		return "", fmt.Errorf("%s 缺 profile 键（指针格式：{\"profile\":\"<name>\"}，D110）", path)
	}
	name, ok := v.(string)
	if !ok || name == "" {
		return "", fmt.Errorf("%s 的 profile 键须为非空字符串", path)
	}
	if err := validProfileName(name); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return name, nil
}

// listProfiles 枚举 profiles/ 下可用的 profile 名（含 config.json 的目录）。
// 目录不存在 = 无可用 profile（返回空）。
func listProfiles(dir string) []string {
	entries, err := os.ReadDir(filepath.Join(dir, "profiles"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "profiles", e.Name(), "config.json")); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// secretName 校验 secret 引用并返回环境变量名。
func secretName(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	name := strings.TrimPrefix(ref, "secret:")
	if ref == name || name == "" {
		return "", fmt.Errorf("api_key 的 secret: 引用缺少环境变量名，当前为 %q（或直接填明文，D35）", ref)
	}
	return name, nil
}

// resolveAPIKey 解析某 provider 的 api_key（D35，文件内值优先；D110② 逐 provider 独立）：
//   - 空 → 回落默认引用 secret:AQUARIUS_OPENAI_KEY（经 Secrets 按名取用；
//     环境变量缺失由 Secrets 报因）
//   - secret:<环境变量名> → 经 port.Secrets 按名取用
//   - 其余 → 视为明文直接使用，启动打印警告（**不回显密钥值**）
func resolveAPIKey(ctx context.Context, provider, ref string, stderr io.Writer) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "secret:AQUARIUS_OPENAI_KEY"
	}
	if !strings.HasPrefix(ref, "secret:") {
		fmt.Fprintf(stderr, "警告：provider %q 的 api_key 为明文（D35）——密钥直接落在 config.json，"+
			"本机任意可读进程、备份与同步都可能取用；建议改用 \"secret:<环境变量名>\" 引用环境变量。\n", provider)
		return ref, nil
	}
	name, err := secretName(ref)
	if err != nil {
		return "", fmt.Errorf("provider %q: %w", provider, err)
	}
	return (envSecrets{}).Get(ctx, name)
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
