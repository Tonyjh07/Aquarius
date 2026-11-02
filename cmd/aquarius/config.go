package main

// config 的结构面：fileConfig 的字段与它直接调用的转换（人格组合、角色卡开关、备份
// 代数），加上权限、界面、限额三个小节。`secret:` 引用解析与外部服务端点解析也在这
// ——两处都被模型、语音、联网共用，跟着其中一家走会读成那一家专用。
//
// 其余四面各在自己的文件：文件的读写与档案在 config_io.go、model 段在 config_model.go、
// audio 与 web 各一个。

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fileConfig config.json 中当前里程碑消费的部分。
// §8 的其余键（input 等）同样写入模板但由对应里程碑启用，
// encoding/json 对未知键宽容。
type fileConfig struct {
	Model        modelConfig       `json:"model"`
	UI           uiConfig          `json:"ui"`
	SystemPrompt string            `json:"system_prompt"` // 人格兜底单串：空 = 内置默认（D20；S7/D144 起由 prompts 段优先组合）
	Prompts      promptsConfig     `json:"prompts"`       // 人格模块化（S7/D144）：模块库 + 有序组合 + 兜底模块
	Permissions  permissionsConfig `json:"permissions"`
	Output       outputConfig      `json:"output"`
	Input        inputConfig       `json:"input"` // 录音语种提示（D179）
	Audio        audioConfig       `json:"audio"` // 语音识别与合成（D179）
	Web          webConfig         `json:"web"`   // 联网搜索与抓取（D197）
	Limits       limitsConfig      `json:"limits"`
	// Tools 工具面（S6/D143）：内置工具禁用清单（热生效）。
	Tools toolsConfig `json:"tools"`
	// Skills 技能目录（D201）：额外技能根（绝对路径、按声明序，先命中者胜）。
	Skills skillsConfig `json:"skills"`
	// Characters 角色卡投影开关（§5.17/D247）：只影响**之后新建**的角色会话；已开始的会话
	// 人格与尾部指令已快照/落盘（`apply_post_history` 例外，它决定发不发，立即生效）。
	Characters charactersConfig `json:"characters"`
	// Backup 备份轮转（D254）：会话树写盘前保留的 .bak 代数，用户可自定义。
	Backup backupConfig `json:"backup"`
	// Delegate 外部代理转交（§5.18/D244）：agents 是唯一事实源（不内置预设）。
	Delegate delegateConfig `json:"delegate"`
	// MCPServers MCP server 声明（D30/D31，M4 启用；校验见 plugin.MCPServer.Validate）。
	MCPServers map[string]plugin.MCPServer `json:"mcpServers"`
	// Plugins 启停与授权状态（D31，config 与 plugin.json 两种发现源共用）。
	Plugins map[string]plugin.State `json:"plugins"`
}

// backupConfig 备份轮转（D254）。只有会话树用它——密钥库按自己的单代口径走。
type backupConfig struct {
	// Keep 保留的备份代数（>=1）；0/省略 = 默认 3。
	Keep int `json:"keep,omitempty"`
}

// backupKeep 有效的备份代数：键缺失或 <=0 一律归默认 3（D254）。
func (c *fileConfig) backupKeep() int {
	if c.Backup.Keep < 1 {
		return 3
	}
	return c.Backup.Keep
}

// promptsConfig 人格模块树（S7/D144）：模块树 + 有序顶层引用。
// 组合经 app.PersonaComposer（模块树递归展开，见 D144）；`system_prompt` 退居内置 @default 正文来源。
type promptsConfig struct {
	// Modules 模块树：name → 节点（叶 = text；组 = children，亦可带 text 作组头）。
	Modules map[string]promptModule `json:"modules"`
	// Persona 人格 = 有序顶层引用列表；空 = 默认 ["@default","@env"]。
	Persona []string `json:"persona"`
	// UserName 用户希望在卡里被怎么称呼 = `{{user}}` / `<USER>` 的取值（§7.7）；空则
	// 取 "User"。只在装配/投影时替换，不进领域。
	UserName string `json:"user_name,omitempty"`
}

// charactersConfig 角色卡投影开关（§5.17/D247）。两个默认开的开关用 *bool：键缺失 = 开
// （老配置没有这一段时不该把常驻世界书与示例一并关掉）。
type charactersConfig struct {
	// PreferSystemPrompt 替换态：卡的 system_prompt 顶掉用户人格引用（`@env` 保留）。
	// 默认关——用户的人格承载着他自己的规则与安全约束，静默替换会造成真实损失。
	PreferSystemPrompt bool `json:"prefer_system_prompt,omitempty"`
	// ApplyPostHistory 每轮把会话 Tail（卡的 post_history_instructions）追加在历史之后；
	// 键缺失 = 发（默认开）。
	ApplyPostHistory *bool `json:"apply_post_history,omitempty"`
	// InjectConstantBook 常驻世界书条目进 `@char.book`；键缺失 = 注入（默认开）。
	InjectConstantBook *bool `json:"inject_constant_book,omitempty"`
	// InjectExamples mes_example 落注入节点；键缺失 = 注入（默认开）。
	InjectExamples *bool `json:"inject_examples,omitempty"`
}

// characterOptions 角色卡投影开关（§5.17/D247）：把 config 三态（键缺失/显式）解析成
// app.CharacterOptions。三处默认开的开关键缺失即视为开。
func (c *fileConfig) characterOptions() app.CharacterOptions {
	on := func(p *bool) bool { return p == nil || *p }
	return app.CharacterOptions{
		PreferSystemPrompt: c.Characters.PreferSystemPrompt,
		InjectConstantBook: on(c.Characters.InjectConstantBook),
		InjectExamples:     on(c.Characters.InjectExamples),
	}
}

// applyPostHistory 会话尾部指令开关（§5.17/D247）：键缺失 = 发。
func (c *fileConfig) applyPostHistory() bool {
	return c.Characters.ApplyPostHistory == nil || *c.Characters.ApplyPostHistory
}

// promptModule 模块树节点（D144）。
type promptModule struct {
	// Text 叶正文（组亦可带——作组头）。
	Text string `json:"text,omitempty"`
	// Children 子模块名有序列表（组；可多级嵌套）。
	Children []string `json:"children,omitempty"`
}

// promptSpec 构造人格装配规格（D144/S7）：模块树 + 有序顶层引用 + 兼容单串（system_prompt → @default 正文）。
func (c *fileConfig) promptSpec() app.PromptSpec {
	spec := app.PromptSpec{Persona: c.Prompts.Persona, Legacy: c.SystemPrompt, UserName: c.Prompts.UserName}
	if len(c.Prompts.Modules) > 0 {
		spec.Modules = make(map[string]app.PromptModule, len(c.Prompts.Modules))
		for name, m := range c.Prompts.Modules {
			spec.Modules[name] = app.PromptModule{Text: m.Text, Children: m.Children}
		}
	}
	return spec
}

// toolsConfig 工具面配置（S6/D143）。
type toolsConfig struct {
	// Disabled 内置工具禁用清单（含 think 草稿工具与 context_compact）；nil = 键缺失
	// → 默认推导（兼容 model.think_tool=true 则空集，否则 ["think"]——D34 现行为）。
	// 只在设置窗动开关时写键（推导态不写盘）。
	Disabled []string `json:"disabled,omitempty"`
}

// skillsConfig 技能配置（D201）：dirs = 追加技能根。
//
// 只有「额外的目录」：<profile>/skills 恒为最后兜底，不需要写在这里。路径必须是绝对路径
// （本仓没有工作区/cwd 概念，相对路径无处可依，启动期直接报错而不是猜）。
type skillsConfig struct {
	Dirs []string `json:"dirs,omitempty"`
}

// outputConfig 输出器开关（§8；M3 启用 notify，tts 见 D179）。
// 键缺失 = false（保守：老配置不自动弹通知，模板默认 notify=true）。
type outputConfig struct {
	Notify bool `json:"notify"`
	TTS    bool `json:"tts"`
}

// inputConfig 输入侧配置（D179）：目前只有录音语种提示。
type inputConfig struct {
	// Language 语种提示，只喂给 ASR：auto | zh | en；空 = auto。
	Language string `json:"language,omitempty"`
}

// normalizeSkills 校验技能目录（启动期一次）：只收绝对路径。
//
// 为什么不「相对 → 转绝对」：本仓没有工作区概念，相对路径的解释要依赖进程 cwd，
// 那是同一个配置在不同启动方式下指向不同技能——静默的不可复现比启动报错更糟。
func (c *fileConfig) normalizeSkills() error {
	out := make([]string, 0, len(c.Skills.Dirs))
	seen := map[string]bool{}
	for i, d := range c.Skills.Dirs {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if !filepath.IsAbs(d) {
			return fmt.Errorf("skills.dirs[%d]=%q 不是绝对路径（技能目录必须写绝对路径）", i, d)
		}
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	c.Skills.Dirs = out
	return nil
}

// permissionsConfig 权限配置（D22：仅等级预设；allow/ask 明细键已移除，矩阵外一律 ask）。
type permissionsConfig struct {
	Level string `json:"level"` // 空 = strict（perm.DefaultLevel）
	// Unattended 无人值守（D185）：true 时模型发起的越界调用当场拒绝并说明，不阻塞轮次。
	// 权限档本身不变——它只关掉「等人点确认」这一环。缺省 false。
	Unattended bool `json:"unattended,omitempty"`
}

// serviceEndpoint 一路外部服务「怎么连」：端点 + 密钥引用（D222）。
type serviceEndpoint struct {
	// BaseURL 端点前缀（已 TrimSpace；空 = 没配，由调用方报因）。
	BaseURL string
	// KeyRef `secret:名` 或明文（空 = 调用方回落缺省引用）。
	KeyRef string
	// Borrowed 借用的 model.providers 条目名（空 = 没借）。只用于报因与键名。
	Borrowed string
}

// resolveEndpoint 解析一路外部服务的端点与密钥（D222）：
//
//	显式字段 → 借 model.providers 里的条目 → 密钥回落 secret:<fallbackSecret>
//
// 借来的值只补**缺的那一项**：显式写了 base_url 就只借密钥，反之亦然。`from` 指定的
// 条目不存在时，只有「端点与密钥都靠借」才报错——用户既然自己写了端点，就没必要
// 因为一条失效的引用把这家整个跳过。
func (c *fileConfig) resolveEndpoint(baseURL, apiKey, from, fallbackSecret string) (serviceEndpoint, error) {
	ep := serviceEndpoint{
		BaseURL: strings.TrimSpace(baseURL),
		KeyRef:  strings.TrimSpace(apiKey),
	}
	from = strings.TrimSpace(from)
	needBase, needKey := ep.BaseURL == "", ep.KeyRef == ""
	if from != "" && (needBase || needKey) {
		pc := c.Model.byName(from)
		if pc == nil {
			if needBase && needKey {
				return ep, fmt.Errorf("引用的服务 %q 不在 model.providers 内"+
					"（先在「模型服务」页添加，或直接填端点与密钥）", from)
			}
		} else {
			ep.Borrowed = from
			if needBase {
				ep.BaseURL = strings.TrimSpace(pc.BaseURL)
			}
			if needKey {
				ep.KeyRef = pc.keyRefs()[0]
			}
		}
	}
	if ep.BaseURL == "" {
		return ep, fmt.Errorf("端点未配置（填 base_url，或引用一个 model.providers 里的条目）")
	}
	if ep.KeyRef == "" && fallbackSecret != "" {
		ep.KeyRef = "secret:" + fallbackSecret
	}
	return ep, nil
}

// uiConfig UI 形态：repl | tui | gui（D33/D43；模板与键缺省为 tui，repl 为测试/e2e
// 后端）+ GUI 全局呼出快捷键 + 主题档 + 缩放/字号/窗口尺寸三旋钮（D90，全热生效）
// + 欢迎引导标记（D141）。
type uiConfig struct {
	Kind string `json:"kind"`
	// Lang 界面语言（D275/§7.8）：auto | zh | en；空 = auto（读系统界面语言，认不出回落
	// zh）。启动时定一次，改完重启；AQUARIUS_UI_LANG 环境变量优先于本键。
	Lang string `json:"lang,omitempty"`
	// Hotkey GUI 全局呼出快捷键（§15.1，如 "Alt+A"）；空 = 默认 Alt+A。仅 ui.kind=gui 用。
	Hotkey string `json:"hotkey,omitempty"`
	// Theme GUI 深浅主题（§15.4/D61）：system | light | dark；空 = system（跟随系统，
	// 其余未识别值亦按 system 处理）。仅 ui.kind=gui 用。
	Theme string `json:"theme,omitempty"`
	// Scale 元素缩放倍率（D90/§15.8）：默认 1.0；界面按 port.SettingsScale* 夹取，越界回落
	// 1.0（这是显示口径的兜底，不是拒绝启动——手改过配置的旧值仍照旧被夹到可用档位）。
	Scale float64 `json:"scale,omitempty"`
	// FontSize 正文字号 sp（D90/§15.8）：默认 15；界面按 port.SettingsFontSize* 夹取，
	// 最终字号 = FontSize × Scale。
	FontSize float64 `json:"font_size,omitempty"`
	// WindowWidth/WindowHeight 主窗像素尺寸（D90/§15.8）：0 = 缺省 608×460dp 现行为；
	// 配置后按字面 px 建窗，界面按 port.SettingsWindow* 粗界与布局地板夹取。
	WindowWidth  int `json:"window_width,omitempty"`
	WindowHeight int `json:"window_height,omitempty"`
	// Keys 输入栏三键（§5.19/D272）：发送 / 引导 / 换行。空字段取缺省
	//（Enter / Shift+Enter / Ctrl+Enter）；三个值必须互不相同、键名合法，否则启动报错。
	Keys uiKeysConfig `json:"keys,omitempty"`
	// Welcomed 欢迎 / 首次运行引导是否已看（D141/§15.7）：键缺失 = 未看——启动后自动
	// 打开欢迎窗一次（引导完成或用户显式关闭即置位）；入口另在设置窗。
	Welcomed bool `json:"welcomed,omitempty"`
}

// uiKeysConfig 输入栏三键写法（`修饰键+键`，键名同 ui.hotkey）。
type uiKeysConfig struct {
	Send    string `json:"send,omitempty"`
	Steer   string `json:"steer,omitempty"`
	Newline string `json:"newline,omitempty"`
}

// limitsConfig 运行限额（§8）。
type limitsConfig struct {
	MaxContextTokens int     `json:"max_context_tokens"`
	MaxTurns         int     `json:"max_turns"`
	CompactThreshold float64 `json:"compact_threshold"` // 自动压缩阈值（D21 轨2，M2）
	ToolOutputChars  int     `json:"tool_output_chars"` // 工具结果截断（ToolRunner，M2）
	ToolTimeoutSec   int     `json:"tool_timeout_sec"`  // 工具超时秒（ToolRunner，M2）
}

// defaultSecretRef 密钥引用缺省回落（D35）：api_key 为空时按它取用（resolveAPIKey 与
// 一键导入共用同一口径——不一致会让「启动用环境变量、导入却跳过」看起来像漏导入）。
const defaultSecretRef = "secret:AQUARIUS_OPENAI_KEY"

// secretName 校验 secret 引用并返回环境变量名。
func secretName(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	name := strings.TrimPrefix(ref, "secret:")
	if ref == name || name == "" {
		return "", fmt.Errorf("api_key 的 secret: 引用缺少环境变量名，当前为 %q（或改为直接填明文）", ref)
	}
	return name, nil
}

// resolveAPIKey 解析某把密钥引用（D35，文件内值优先；D173 逐 provider、逐钥匙独立）：
//   - 空 → 回落默认引用 secret:AQUARIUS_OPENAI_KEY（经 Secrets 按名取用；
//     库与同名环境变量都缺由 Secrets 报因）
//   - secret:<名> → 经 port.Secrets 按名取用（**先本机密钥库、再同名环境变量**，D151/D161）
//   - 其余 → 视为明文直接使用，启动打印警告（**不回显密钥值**）
func resolveAPIKey(ctx context.Context, provider, ref string, sec port.Secrets, stderr io.Writer) (string, error) {
	return resolveAPIKeyTo(ctx, provider, ref, sec, stderr)
}

// resolvePatchKey 界面草稿凭据的一次性解析（探测路径专用，D210）：口径与 resolveAPIKey
// 完全一致，只是**不打明文警告**。
//
// 为什么：那句警告说的是「密钥直接落在 config.json」——一项**已写盘**的事实。但探测用的
// 是编辑框里的草稿，它根本还没落盘（保存时才入库、config 只留引用）；拿它警告等于告诉用户
// 一件没发生的事。叠加一个现实后果：欢迎窗的「测试连通」与「获取模型」各探一次，同一份
// 草稿会被警告两遍（用户实测日志里就是两条一模一样的话）。落盘形态的警告仍由真实构造路径
// （resolveAPIKey）负责，那里每次启动最多一遍。
func resolvePatchKey(ctx context.Context, provider, ref string, sec port.Secrets) (string, error) {
	return resolveAPIKeyTo(ctx, provider, ref, sec, nil)
}

// resolveAPIKeyTo 解析主体；warn == nil = 只在内存里用一次、不报落盘形态。
func resolveAPIKeyTo(ctx context.Context, provider, ref string, sec port.Secrets, warn io.Writer) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = defaultSecretRef
	}
	if !strings.HasPrefix(ref, "secret:") {
		if warn != nil {
			secretBoundWarn(warn, provider)
		}
		return ref, nil
	}
	name, err := secretName(ref)
	if err != nil {
		return "", fmt.Errorf("provider %q: %w", provider, err)
	}
	return sec.Get(ctx, name)
}
