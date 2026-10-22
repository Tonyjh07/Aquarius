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
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ErrLegacyConfig D110 前的旧单文件 config（沿 storejson ErrLegacyFormat 口径：
// 显式识别报因、绝不静默当零值解析、不做自动迁移——旧文件留存原地由用户手工处置）。
var ErrLegacyConfig = errors.New("检测到旧格式 config")

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
	// MCPServers MCP server 声明（D30/D31，M4 启用；校验见 plugin.MCPServer.Validate）。
	MCPServers map[string]plugin.MCPServer `json:"mcpServers"`
	// Plugins 启停与授权状态（D31，config 与 plugin.json 两种发现源共用）。
	Plugins map[string]plugin.State `json:"plugins"`
}

// promptsConfig 人格模块树（S7/D144）：模块树 + 有序顶层引用。
// 组合经 app.PersonaComposer（模块树递归展开，见 D144）；`system_prompt` 退居内置 @default 正文来源。
type promptsConfig struct {
	// Modules 模块树：name → 节点（叶 = text；组 = children，亦可带 text 作组头）。
	Modules map[string]promptModule `json:"modules"`
	// Persona 人格 = 有序顶层引用列表；空 = 默认 ["@default","@env"]。
	Persona []string `json:"persona"`
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
	spec := app.PromptSpec{Persona: c.Prompts.Persona, Legacy: c.SystemPrompt}
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

// outputConfig 输出器开关（DESIGN §8；M3 启用 notify，tts 见 D179）。
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

// audioConfig 语音配置（D179）：识别与合成各自引用一个 provider——只借它的
// base_url 与密钥池，不继承 kind、能力位，也不进降级链。
type audioConfig struct {
	ASR audioChannel `json:"asr"`
	TTS audioChannel `json:"tts"`
}

// audioChannel 一路语音能力的引用与参数。
// 未配置（provider 为空）= 该能力不可用；上层据此隐藏入口并报因，而不是启动失败。
type audioChannel struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Kind 协议位：chat-audio（空 = 该档，chat/completions 携带音频）。
	Kind string `json:"kind,omitempty"`
	// Voice 预置音色名 / 音色设计描述之外的模式：`-voiceclone` 时是样本文件绝对路径。
	Voice string `json:"voice,omitempty"`
	// Design 音色描述（`-voicedesign` 模型必填，进 user 消息）。
	Design string `json:"design,omitempty"`
	// Style 自然语言风格指令（进 user 消息；空 = 不发）。
	Style string `json:"style,omitempty"`
	// Tags 风格标签（如 "温柔 语速稍快"）：非空时拼成 "(标签)" 前缀置于正文开头。
	Tags string `json:"tags,omitempty"`
}

// kindChatAudio 首发音频协议：chat/completions 携带音频（D179）。
const kindChatAudio = "chat-audio"

// webConfig 联网搜索与抓取（D197）：两组**有序**提供商列表，顺序即优先级。
//
// 空列表 = 该能力不可用：对应工具照常注册（工具清单与设置窗保持稳定），执行时
// 明确报「未配置提供商」，而不是静默返回空结果。
type webConfig struct {
	Search webSearchConfig `json:"search"`
	Fetch  webFetchConfig  `json:"fetch"`
}

// webSearchConfig 搜索侧。
type webSearchConfig struct {
	// Providers 有序提供商：先试第一个，失败换下一个（D197）。
	Providers []webProvider `json:"providers"`
	// Count 缺省返回条数（工具参数可逐次覆盖）；0 = 8。
	Count int `json:"count,omitempty"`
}

// webFetchConfig 抓取侧 + 本地抓取的安全/网络参数。
type webFetchConfig struct {
	// Providers 有序提供商：deepseek（远端）/ local（本地纯 Go）。
	Providers []webProvider `json:"providers"`
	// MaxChars 单次回填正文的缺省上限（字符）；0 = 16000，硬顶 20000。
	MaxChars int `json:"max_chars,omitempty"`
	// Proxy 本地抓取用的代理（如 http://127.0.0.1:7897）；空 = 读 HTTP(S)_PROXY。
	Proxy string `json:"proxy,omitempty"`
	// UserAgent 本地抓取的 UA；空 = 浏览器形态缺省值（很多站点拒绝非浏览器 UA）。
	UserAgent string `json:"user_agent,omitempty"`
	// RespectRobots 本地抓取是否遵守 robots.txt；键缺失 = 遵守。
	RespectRobots *bool `json:"respect_robots,omitempty"`
	// AllowPrivateIPs 放开回环/私网目标（调试本机服务用）；缺省关闭。
	AllowPrivateIPs bool `json:"allow_private_ips,omitempty"`
	// TimeoutSec 本地抓取超时秒；0 = 30。
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// webProvider 一家提供商（kind 决定怎么连，其余字段按 kind 取用）。
type webProvider struct {
	// Kind 提供商类型：搜索侧 deepseek | brave，抓取侧 deepseek | local。
	Kind string `json:"kind"`
	// From 复用某个 model.providers 的 base_url 与密钥池（deepseek 缺省 = "deepseek"）。
	From string `json:"from,omitempty"`
	// Key 直接给密钥引用（secret:名 或明文）；brave 必填。
	Key string `json:"key,omitempty"`
	// BaseURL 覆盖端点前缀（自建网关/镜像）。
	BaseURL string `json:"base_url,omitempty"`
	// Count 这一家的缺省条数（覆盖 web.search.count）。
	Count int `json:"count,omitempty"`
	// Country / Lang 结果国家与语言（仅 brave；空 = 服务端缺省）。
	Country string `json:"country,omitempty"`
	Lang    string `json:"lang,omitempty"`
}

// 联网提供商类型标识。
const (
	webKindDeepSeek = "deepseek"
	webKindBrave    = "brave"
	webKindLocal    = "local"
)

// normalizeWeb 校验联网配置（启动期一次）：只拦「写错了」的声明——未知 kind、
// 抓取侧写了只做搜索的 kind、deepseek 引用了不存在的 provider。
// 密钥缺失不在这里报错（装配根警告并跳过该家，其余提供商继续可用）。
func (c *fileConfig) normalizeWeb() error {
	for i, p := range c.Web.Search.Providers {
		switch p.Kind {
		case webKindDeepSeek:
			if c.Model.byName(webProviderFrom(p)) == nil {
				return fmt.Errorf("web.search.providers[%d].from=%q 不在 model.providers 内"+
					"（deepseek 搜索借它的 base_url 与密钥池）", i, webProviderFrom(p))
			}
		case webKindBrave:
			// 只借 key（或 base_url），不引用对话 provider。
		default:
			return fmt.Errorf("web.search.providers[%d].kind=%q 不认识（搜索侧支持 %s / %s）",
				i, p.Kind, webKindDeepSeek, webKindBrave)
		}
	}
	for i, p := range c.Web.Fetch.Providers {
		switch p.Kind {
		case webKindDeepSeek:
			if c.Model.byName(webProviderFrom(p)) == nil {
				return fmt.Errorf("web.fetch.providers[%d].from=%q 不在 model.providers 内"+
					"（deepseek 抓取借它的 base_url 与密钥池）", i, webProviderFrom(p))
			}
		case webKindLocal:
		default:
			return fmt.Errorf("web.fetch.providers[%d].kind=%q 不认识（抓取侧支持 %s / %s）",
				i, p.Kind, webKindDeepSeek, webKindLocal)
		}
	}
	if n := c.Web.Search.Count; n < 0 || n > 20 {
		return fmt.Errorf("web.search.count=%d 超出范围（1-20；0 = 缺省 8）", n)
	}
	if n := c.Web.Fetch.MaxChars; n < 0 || n > 20000 {
		return fmt.Errorf("web.fetch.max_chars=%d 超出范围（500-20000；0 = 缺省 16000）", n)
	}
	return nil
}

// webProviderFrom 该提供商引用的对话 provider 名（deepseek 缺省按 kind 同名找）。
func webProviderFrom(p webProvider) string {
	if s := strings.TrimSpace(p.From); s != "" {
		return s
	}
	return p.Kind
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

// providerConfig 单个 provider 接入（D173 起含协议与密钥池）：承载「怎么连」。
type providerConfig struct {
	// Name provider 唯一标识（model.primary / model.fallback 按名引用）。
	Name string `json:"name"`
	// Kind 协议（D173）：openai-chat（空 = 该档）| openai-responses | anthropic。
	// 决定装配根造哪个适配器；base_url 是路径前缀，适配器在其后追加各自端点。
	Kind string `json:"kind,omitempty"`
	// BaseURL 端点路径前缀（如 https://api.openai.com/v1）。
	BaseURL string `json:"base_url"`
	// Keys 密钥池（D173）：按序轮换，逐把独立冷却。空 = 一条空引用（回落默认 secret）。
	Keys []keyConfig `json:"keys"`
	// Models 模型条目：名字 + 能力位。首个 = 该 provider 的缺省请求模型。
	Models []modelEntry `json:"models"`
	// Tokenizer 本地 tokenizer.json 路径（精确计数②，D26）；空 = 通用估算③。
	Tokenizer string `json:"tokenizer"`
	// UnsupportedParams 服务端已知不认的请求参数（D34 自动记录，启动注入省略）。
	// D173：只对 openai-chat 生效（另两种协议是自家规范，参数写错就是 bug）。
	UnsupportedParams []string `json:"unsupported_params,omitempty"`
}

// keyConfig 密钥池条目（D173）。Label 只用于界面展示（「主账号」「备用」），不参与
// 解析；APIKey 的取值规则与旧 provider.api_key 完全一致（D35/D151/D161）。
type keyConfig struct {
	Label  string `json:"label,omitempty"`
	APIKey string `json:"api_key"`
}

// modelEntry 模型条目（D173）：名字 + 能力位 + 上下文窗口，不继承 provider。
type modelEntry struct {
	Name string `json:"name"`
	// Caps 能力位：nil = 键缺失（按协议缺省，DESIGN §8 的表）；非 nil = 以声明为准
	// （空数组 = 三项全关——「没写」与「写了空」是两回事，所以要指针区分）。
	Caps *[]string `json:"caps,omitempty"`
	// ContextWindow 该模型的上下文窗口 tokens（D177）；0/省略 = 不声明，预算只看全局
	// limits.max_context_tokens。这是模型的硬上限，不是「想用多少」——想省钱的旋钮仍
	// 是全局那项（见 effectiveMaxContext）。
	ContextWindow int `json:"context_window,omitempty"`
	// Enabled 是否启用（D180）：键缺失 = 启用（旧配置零迁移）；显式 false = 保留声明但
	// 不参与选型（不再出现在降级链选项与缺省模型选择里）。用指针区分「没写」与「写了
	// false」——写回时只有 false 才落键，配置不会因为一次应用长出满屏 enabled: true。
	Enabled *bool `json:"enabled,omitempty"`
}

// isEnabled 该模型条目是否启用（D180：键缺失 = 启用）。
func (m *modelEntry) isEnabled() bool { return m.Enabled == nil || *m.Enabled }

// fallbackEntry 降级链一项（D173）：provider + 可选模型。Model 空 = 该 provider 的
// models[0]；同一 provider 可重复出现（同家不同型号兜底），但二元组不得重复。
type fallbackEntry struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
}

// modelConfig 模型接入配置（D173 v2：能力位下沉到模型、密钥池、逐项降级链）。
type modelConfig struct {
	// Providers provider 列表，至少一条。
	Providers []providerConfig `json:"providers"`
	// Primary 当前生效 provider（providers 中的 name；缺省 = 首个）。
	Primary string `json:"primary,omitempty"`
	// Fallback 有序降级顺序（Q5：仅网络类错误触发；二元组不得重复、不含 primary 当前模型）。
	Fallback []fallbackEntry `json:"fallback,omitempty"`
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

// 协议标识（D173）；空 kind 归一为 openai-chat。
const (
	kindOpenAIChat      = "openai-chat"
	kindOpenAIResponses = "openai-responses"
	kindAnthropic       = "anthropic"
)

// protocolKind 归一后的协议标识（空 = openai-chat）。
func (p *providerConfig) protocolKind() string {
	if k := strings.TrimSpace(p.Kind); k != "" {
		return k
	}
	return kindOpenAIChat
}

// defaultCaps 协议缺省能力位（D173，DESIGN §8 的表）：只在模型条目省略 caps 时使用。
func defaultCaps(kind string) port.ModelCaps {
	switch kind {
	case kindOpenAIResponses:
		return port.ModelCaps{Tools: true, Reasoning: true, Vision: true}
	case kindAnthropic:
		// Messages API 全系支持图片与工具；extended thinking 要显式给 budget_tokens，
		// 旧模型不接受——不猜。
		return port.ModelCaps{Tools: true, Vision: true}
	default:
		// openai-chat：思考参数不认时有「剥离重试 + unsupported_params」兜底（D34），
		// 猜错的代价只是一次往返；图片各端点差异太大，不猜。
		return port.ModelCaps{Tools: true, Reasoning: true}
	}
}

// capsOf 解析某模型条目的能力位：键缺失 = 协议缺省；写了就以声明为准。
// 未识别的名字忽略（集合只有三个词；写错一个拼写不该把整条声明作废）。
func (p *providerConfig) capsOf(m modelEntry) port.ModelCaps {
	if m.Caps == nil {
		return defaultCaps(p.protocolKind())
	}
	return port.CapsFromNames(*m.Caps)
}

// modelByName 按名取模型条目（未命中返回 nil）。
func (p *providerConfig) modelByName(name string) *modelEntry {
	for i := range p.Models {
		if p.Models[i].Name == name {
			return &p.Models[i]
		}
	}
	return nil
}

// models0 该 provider 的缺省请求模型：首个**启用**的条目（D180）。启动校验会拒掉
// 「一家一个都没启用」的配置，所以正常路径上这里总能拿到名字；末尾的兜底只针对运行期
// 手改内存的行（界面全部取消勾选但还没应用时，类名解析不该炸）。
func (p *providerConfig) models0() string {
	for i := range p.Models {
		if p.Models[i].isEnabled() {
			return p.Models[i].Name
		}
	}
	return p.Models[0].Name
}

// enabledModels 该 provider 参与选型的模型名（D180：禁用项保留声明但不进可选清单）。
func (p *providerConfig) enabledModels() []string {
	out := make([]string, 0, len(p.Models))
	for i := range p.Models {
		if p.Models[i].isEnabled() {
			out = append(out, p.Models[i].Name)
		}
	}
	return out
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

// entryCaps 链上一项的能力位（未声明的模型名按协议缺省，与 currentCaps 同口径）。
func (m *modelConfig) entryCaps(provider, model string) port.ModelCaps {
	p := m.byName(provider)
	if p == nil {
		return port.ModelCaps{}
	}
	var e *modelEntry
	if model == "" {
		e = p.modelByName(p.models0()) // 缺省模型 = 首个启用项（D180）
	} else {
		e = p.modelByName(model)
	}
	if e == nil {
		return defaultCaps(p.protocolKind())
	}
	return p.capsOf(*e)
}

// currentCaps primary 当前模型自己的能力位（不含链上其他环节）。
func (m *modelConfig) currentCaps() port.ModelCaps {
	return m.entryCaps(m.primaryProvider().Name, m.Name)
}

// chainCaps 整条降级链的生效能力（D173：三项各自取与）。降级发生在半路上——图片
// 那时已经进了请求体，剥不掉；工具与思考同口径（发出去就收不回）。
func (m *modelConfig) chainCaps() port.ModelCaps {
	caps := m.currentCaps()
	for _, fb := range m.Fallback {
		c := m.entryCaps(fb.Provider, fb.Model)
		caps.Tools = caps.Tools && c.Tools
		caps.Reasoning = caps.Reasoning && c.Reasoning
		caps.Vision = caps.Vision && c.Vision
	}
	return caps
}

// enrichModels 用配置补齐服务端模型列表里缺的元信息（D173/D177）：服务端 `/models`
// 只返回名字，能力位与上下文窗口只能来自 config。只填不删——列表的可用性以服务端为准，
// 但声明过的元信息要能显示出来，否则 `/model` 里满屏都是没有标注的裸名字。
//
// D180：**声明过但未启用**的模型不进可选清单（「保留声明但不参与选型」）；当前模型例外
// ——它是正在跑的事实，藏起来只会让人以为配错了。
func (m *modelConfig) enrichModels(infos []port.ModelInfo) []port.ModelInfo {
	p := m.primaryProvider()
	out := make([]port.ModelInfo, 0, len(infos))
	for _, info := range infos {
		if e := p.modelByName(info.Name); e != nil {
			if !e.isEnabled() && info.Name != m.Name {
				continue
			}
			info.Caps = p.capsOf(*e)
			info.ContextWindow = e.ContextWindow
		}
		out = append(out, info)
	}
	return out
}

// modelChain 降级链逐环快照（D173：/model 标注「哪一环拉低了整链」）。
// 首项 = primary 的当前模型，其后按 fallback 顺序。
func (m *modelConfig) modelChain() []app.ModelLink {
	p := m.primaryProvider()
	links := []app.ModelLink{{
		Provider: p.Name, Model: m.Name, Caps: m.currentCaps(), Primary: true,
	}}
	for _, fb := range m.Fallback {
		model := fb.Model
		if model == "" {
			if tp := m.byName(fb.Provider); tp != nil {
				model = tp.models0()
			}
		}
		links = append(links, app.ModelLink{
			Provider: fb.Provider, Model: model, Caps: m.entryCaps(fb.Provider, fb.Model),
		})
	}
	return links
}

// currentContextWindow primary 当前模型声明的上下文窗口（0 = 未声明）。
func (m *modelConfig) currentContextWindow() int {
	p := m.primaryProvider()
	e := p.modelByName(m.Name)
	if e == nil {
		return 0
	}
	return e.ContextWindow
}

// effectiveMaxContext 装配用的上下文预算（D177）：min(当前模型的窗口, 全局预算)。
// 全局那项是用户给自己定的上限（想省钱就调小），模型窗口是硬上限（超了服务端直接
// 拒）；两者都生效、谁小听谁的。模型未声明窗口 = 全局值（升级前行为）。
//
// 为什么不参与整链取与：装配发生在请求发出前，那时用的是 primary 的当前模型；
// 降级换模型是半路发生的，按大窗口装好再换小窗口模型只能靠服务端报错。压缩阈值、
// /usage 占用、截断水位三处必须同源，否则「显示还有空间」与「真发得出去」会打架。
func (m *modelConfig) effectiveMaxContext(global int) int {
	if w := m.currentContextWindow(); w > 0 && (global <= 0 || w < global) {
		return w
	}
	return global
}

// narrowNote 链窄于当前模型时的提示（空 = 没窄）。启动期打一行警告——D173：加一条
// 弱模型会静默削能力，不能让人自己发现，所以要指名是哪一环。
func (m *modelConfig) narrowNote() string {
	cur, chain := m.currentCaps(), m.chainCaps()
	var lost []string
	if cur.Tools && !chain.Tools {
		lost = append(lost, "工具调用")
	}
	if cur.Reasoning && !chain.Reasoning {
		lost = append(lost, "思考")
	}
	if cur.Vision && !chain.Vision {
		lost = append(lost, "图片输入")
	}
	if len(lost) == 0 {
		return ""
	}
	note := fmt.Sprintf("降级链窄于当前模型 %s 的能力，整链按不支持 %s 处理",
		m.Name, strings.Join(lost, "、"))
	var weak []string
	for _, l := range m.modelChain()[1:] {
		var missing []string
		if cur.Tools && !l.Caps.Tools {
			missing = append(missing, "工具")
		}
		if cur.Reasoning && !l.Caps.Reasoning {
			missing = append(missing, "思考")
		}
		if cur.Vision && !l.Caps.Vision {
			missing = append(missing, "图片")
		}
		if len(missing) > 0 {
			weak = append(weak, fmt.Sprintf("%s/%s 缺 %s", l.Provider, l.Model, strings.Join(missing, "、")))
		}
	}
	if len(weak) > 0 {
		note += "（" + strings.Join(weak, "；") + "）"
	}
	return note + "；检查 model.fallback 各模型的 caps"
}

// normalize 应用缺省值并 fail-fast 校验 model 段（D173，启动期调用）：
// primary 缺省 = providers 首个；model.name 缺省 = primary.models[0]。
func (m *modelConfig) normalize() error {
	if len(m.Providers) == 0 {
		return fmt.Errorf("model.providers 不可为空（provider 已改为列表；若为旧格式请手工迁移，见 docs/configuration.md）")
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
		switch p.protocolKind() {
		case kindOpenAIChat, kindOpenAIResponses, kindAnthropic:
		default:
			return fmt.Errorf("model.providers[%d] (%s) 的 kind %q 不认识（可选：%s / %s / %s）",
				i, p.Name, p.Kind, kindOpenAIChat, kindOpenAIResponses, kindAnthropic)
		}
		if p.BaseURL == "" {
			return fmt.Errorf("model.providers[%d] (%s) 的 base_url 不可为空", i, p.Name)
		}
		if len(p.Models) == 0 || strings.TrimSpace(p.Models[0].Name) == "" {
			return fmt.Errorf("model.providers[%d] (%s) 的 models 不可为空（首个 = 缺省请求模型）", i, p.Name)
		}
		modelSeen := map[string]bool{}
		enabled := 0
		for j := range p.Models {
			e := &p.Models[j]
			e.Name = strings.TrimSpace(e.Name)
			if e.Name == "" {
				return fmt.Errorf("model.providers[%d] (%s) 的 models[%d].name 不可为空", i, p.Name, j)
			}
			if modelSeen[e.Name] {
				return fmt.Errorf("model.providers[%d] (%s) 的模型 %q 重复", i, p.Name, e.Name)
			}
			modelSeen[e.Name] = true
			if e.isEnabled() {
				enabled++
			}
			if e.ContextWindow < 0 {
				return fmt.Errorf("model.providers[%d] (%s) 的模型 %q 的 context_window 不可为负数（%d）；不声明就留空",
					i, p.Name, e.Name, e.ContextWindow)
			}
		}
		if enabled == 0 {
			return fmt.Errorf("model.providers[%d] (%s) 的模型全部未启用；至少启用一个（enabled: false 只是清单占位，不参与选型）",
				i, p.Name)
		}
		for j := range p.Keys {
			p.Keys[j].Label = strings.TrimSpace(p.Keys[j].Label)
			p.Keys[j].APIKey = strings.TrimSpace(p.Keys[j].APIKey)
		}
	}
	if m.Primary == "" {
		m.Primary = m.Providers[0].Name
	}
	if m.byName(m.Primary) == nil {
		return fmt.Errorf("model.primary %q 不在 model.providers 内", m.Primary)
	}
	if m.Name == "" {
		m.Name = m.primaryProvider().models0()
	}
	fbSeen := map[string]bool{}
	for _, fb := range m.Fallback {
		fb.Provider = strings.TrimSpace(fb.Provider)
		fb.Model = strings.TrimSpace(fb.Model)
		if fb.Provider == "" {
			return fmt.Errorf("model.fallback 每项都要写 provider")
		}
		if m.byName(fb.Provider) == nil {
			return fmt.Errorf("model.fallback %q 不在 model.providers 内", fb.Provider)
		}
		if fb.Provider == m.Primary && (fb.Model == "" || fb.Model == m.Name) {
			return fmt.Errorf("model.fallback 不应含当前生效的 %s/%s（那就是 primary 本身）", fb.Provider, m.Name)
		}
		key := fb.Provider + "/" + fb.Model
		if fbSeen[key] {
			return fmt.Errorf("model.fallback 重复项 %q", key)
		}
		fbSeen[key] = true
	}
	return nil
}

// normalizeAudio 语音配置归一与校验（D179；在 model.normalize 之后调用）。
// 引用的 provider **不存在不算启动失败**：语音是可选能力，按「不可用 + 明确报因」
// 处理，用户配错一行不该连对话都开不起来。协议词与语种写错则 fail-fast——那是
// 永远不会生效的配置，留着只会让人以为开了。
func (c *fileConfig) normalizeAudio() error {
	c.Input.Language = strings.ToLower(strings.TrimSpace(c.Input.Language))
	switch c.Input.Language {
	case "":
		c.Input.Language = "auto"
	case "auto", "zh", "en":
	default:
		return fmt.Errorf("input.language=%q 仅支持 auto | zh | en", c.Input.Language)
	}
	for name, ch := range map[string]*audioChannel{"audio.asr": &c.Audio.ASR, "audio.tts": &c.Audio.TTS} {
		ch.Provider = strings.TrimSpace(ch.Provider)
		ch.Model = strings.TrimSpace(ch.Model)
		ch.Kind = strings.TrimSpace(ch.Kind)
		ch.Voice = strings.TrimSpace(ch.Voice)
		ch.Design = strings.TrimSpace(ch.Design)
		ch.Style = strings.TrimSpace(ch.Style)
		ch.Tags = strings.TrimSpace(ch.Tags)
		if ch.Provider == "" {
			continue // 未配置 = 该能力不可用
		}
		if ch.Kind == "" {
			ch.Kind = kindChatAudio
		}
		if ch.Kind != kindChatAudio {
			return fmt.Errorf("%s.kind=%q 不认识（当前支持 %s）", name, ch.Kind, kindChatAudio)
		}
		if ch.Model == "" {
			return fmt.Errorf("%s.model 不可为空（配置了 provider 就要写明音频模型名）", name)
		}
	}
	return nil
}

// audioProvider 解析一路语音能力引用的 provider（D179）。
// 返回 (nil, 原因) = 该能力不可用；原因面向用户，可直接展示。
func (c *fileConfig) audioProvider(which string) (*providerConfig, string) {
	var ch *audioChannel
	switch which {
	case "asr":
		ch = &c.Audio.ASR
	case "tts":
		ch = &c.Audio.TTS
	default:
		return nil, fmt.Sprintf("未知的语音能力 %q", which)
	}
	if ch.Provider == "" {
		return nil, "未配置语音服务"
	}
	p := c.Model.byName(ch.Provider)
	if p == nil {
		return nil, fmt.Sprintf("语音服务引用的 provider %q 不在 model.providers 内", ch.Provider)
	}
	return p, ""
}

// uiConfig UI 形态：repl | tui | gui（D33/D43；模板与键缺省为 tui，repl 为测试/e2e
// 后端）+ GUI 全局呼出快捷键 + 主题档 + 缩放/字号/窗口尺寸三旋钮（D90，全热生效）
// + 欢迎引导标记（D141）。
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
	// Welcomed 欢迎 / 首次运行引导是否已看（D141/§15.7）：键缺失 = 未看——启动后自动
	// 打开欢迎窗一次（引导完成或用户显式关闭即置位）；入口另在设置窗。
	Welcomed bool `json:"welcomed,omitempty"`
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
      { "name": "openai",
        "kind": "openai-chat",
        "base_url": "https://api.openai.com/v1",
        "keys": [ { "label": "主账号", "api_key": "secret:AQUARIUS_OPENAI_KEY" } ],
        "models": [
          { "name": "gpt-4o-mini", "context_window": 128000 },
          { "name": "gpt-4o", "caps": ["tools", "reasoning", "vision"], "context_window": 128000 }
        ],
        "tokenizer": "",
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
  "tools": { "disabled": ["think"] },
  "skills": { "dirs": [] },
  "ui": { "kind": "gui", "theme": "system", "scale": 1.0, "font_size": 15, "window_width": 0, "window_height": 0, "welcomed": false },
  "system_prompt": "",
  "prompts": { "modules": {}, "persona": [] },
  "input": { "language": "auto" },
  "audio": { "asr": {}, "tts": {} },
  "output": { "tts": false, "notify": true },
  "web": {
    "search": { "providers": [], "count": 8 },
    "fetch": { "providers": [ { "kind": "local" } ], "max_chars": 16000 }
  },
  "mcpServers": {},
  "plugins": {},
  "permissions": { "level": "strict", "unattended": false },
  "limits": {
    "max_turns": 32,
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

// detectLegacyModelConfig 旧形状判别（D110② 单 provider 平铺 / D173 之前 provider 列表
// 单值密钥）：两种都报 ErrLegacyConfig 与手工改写指引。Unmarshal 对旧键静默零值——
// 不先判别的话，旧配置会「看起来正常」地丢光模型与密钥（D95 影子结构同口径）。
func detectLegacyModelConfig(path string, data []byte) error {
	var probe struct {
		Model struct {
			Providers []map[string]json.RawMessage `json:"providers"`
			Fallback  []json.RawMessage            `json:"fallback"`
			BaseURL   string                       `json:"base_url"`
			APIKey    string                       `json:"api_key"`
			Tokenizer string                       `json:"tokenizer"`
			Provider  string                       `json:"provider"`
		} `json:"model"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil // 非 JSON：交由后续解析报因
	}
	m := probe.Model
	if len(m.Providers) == 0 {
		if m.BaseURL == "" && m.APIKey == "" && m.Tokenizer == "" && m.Provider == "" {
			return nil // 无 model 段：交由后续校验（providers 不可为空）
		}
		return fmt.Errorf("%w：%s 的 model 段为单 provider 平铺字段（检测到 base_url/api_key/tokenizer/provider）。\n"+
			"provider 须写成列表（model.providers）：\n"+
			"  \"model\": { \"providers\": [ { \"name\": \"openai\", \"kind\": \"openai-chat\",\n"+
			"    \"base_url\": \"…\", \"keys\": [ { \"api_key\": \"secret:…\" } ],\n"+
			"    \"models\": [ { \"name\": \"…\" } ], \"tokenizer\": \"\", \"unsupported_params\": [] } ],\n"+
			"    \"primary\": \"openai\", \"fallback\": [], \"name\": \"…\", … }\n"+
			"请手工改写（不做自动迁移），原平铺字段退役；字段说明见 docs/configuration.md。",
			ErrLegacyConfig, path)
	}
	// D173 前的 provider 列表：api_key 是单值字符串、models 是字符串数组、vision 挂在
	// provider 上。三者任一出现即判旧形状。
	for i, p := range m.Providers {
		_, hasKey := p["api_key"]
		_, hasVision := p["vision"]
		var models []json.RawMessage
		if raw, ok := p["models"]; ok {
			_ = json.Unmarshal(raw, &models)
		}
		oldModels := len(models) > 0 && len(models[0]) > 0 && models[0][0] == '"'
		if hasKey || hasVision || oldModels {
			return fmt.Errorf("%w：%s 的 model.providers[%d] 是旧形状（api_key 单值 / models 字符串数组 / provider 上的 vision）。\n"+
				"模型与提供商已改版：provider 承载「怎么连」，模型承载「能做什么」。\n"+
				"  \"model\": { \"providers\": [ { \"name\": \"openai\", \"kind\": \"openai-chat\",\n"+
				"    \"base_url\": \"…\",\n"+
				"    \"keys\": [ { \"label\": \"主账号\", \"api_key\": \"secret:…\" } ],\n"+
				"    \"models\": [ { \"name\": \"gpt-4o-mini\" },\n"+
				"                 { \"name\": \"gpt-4o\", \"caps\": [\"tools\",\"reasoning\",\"vision\"] } ],\n"+
				"    \"tokenizer\": \"\", \"unsupported_params\": [] } ],\n"+
				"    \"primary\": \"openai\",\n"+
				"    \"fallback\": [ { \"provider\": \"mimo\", \"model\": \"mimo-v2.6-flash\" } ],\n"+
				"    \"name\": \"…\", … }\n"+
				"迁移要点：api_key → keys[]（多把即密钥池）；models 字符串 → 对象（能力位写进 caps，\n"+
				"省略 caps 按协议缺省）；provider 上的 vision 移到对应模型的 caps；fallback 字符串 →\n"+
				"provider/model 二元组（省略 model = 该 provider 的 models[0]）。\n"+
				"请手工改写（不做自动迁移）；字段说明见 docs/configuration.md。",
				ErrLegacyConfig, path, i)
		}
	}
	for _, fb := range m.Fallback {
		if len(fb) > 0 && fb[0] == '"' {
			return fmt.Errorf("%w：%s 的 model.fallback 仍是 provider 名字数组。\n"+
				"降级链已改为逐项指定模型：\"fallback\": [ { \"provider\": \"mimo\", \"model\": \"mimo-v2.6-flash\" } ]\n"+
				"（省略 model = 该 provider 的 models[0]）。请手工改写（不做自动迁移），见 docs/configuration.md。",
				ErrLegacyConfig, path)
		}
	}
	return nil
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
				"profile 已改为目录化：根 config.json 仅存当前 profile 指针（{\"profile\":\"<name>\"}），\n"+
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
		return "", fmt.Errorf("%s 缺 profile 键（指针格式：{\"profile\":\"<name>\"}）", path)
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
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = defaultSecretRef
	}
	if !strings.HasPrefix(ref, "secret:") {
		secretBoundWarn(stderr, provider)
		return ref, nil
	}
	name, err := secretName(ref)
	if err != nil {
		return "", fmt.Errorf("provider %q: %w", provider, err)
	}
	return sec.Get(ctx, name)
}

// keyRefs 该 provider 的密钥引用清单（D173）：空 = 一条空引用（沿用 D35 的缺省回落）。
// 空串条目保留——「没填」与「不填」在密钥池里的语义都是「回落缺省引用」，但位置
// 有意义（第 N 把决定派生名后缀）。
func (p *providerConfig) keyRefs() []string {
	if len(p.Keys) == 0 {
		return []string{""}
	}
	out := make([]string, 0, len(p.Keys))
	for _, k := range p.Keys {
		out = append(out, strings.TrimSpace(k.APIKey))
	}
	return out
}

// resolveKeyAt 解析第 i 把钥匙（越界回落缺省引用）。label 只用于报因，不回显值。
func (p *providerConfig) resolveKeyAt(ctx context.Context, i int, sec port.Secrets, stderr io.Writer) (string, error) {
	refs := p.keyRefs()
	if i < 0 || i >= len(refs) {
		i = 0
	}
	return resolveAPIKey(ctx, p.Name, refs[i], sec, stderr)
}

// keyLabelAt 第 i 把钥匙的展示名（D173）：用户填的 label 优先，空则「第 N 把」。
// 只用于界面与日志，不参与解析。
func (p *providerConfig) keyLabelAt(i int) string {
	if i >= 0 && i < len(p.Keys) {
		if l := strings.TrimSpace(p.Keys[i].Label); l != "" {
			return l
		}
	}
	return fmt.Sprintf("第 %d 把", i+1)
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

// cfgWriter 把 UI/命令面触发的 config 写回收拢为一处（S4a）：path 即当前 profile
// 的 config.json，lvl 供 persistLevel 写回成功后同步活等级。
type cfgWriter struct {
	path       string
	lvl        *levelHolder
	unattended *unattendedHolder
}

// persistLevel 实现 /permission 写回 config（D22）：成功后同步活等级。
func (c *cfgWriter) persistLevel(l perm.Level) error {
	if err := persistConfig(c.path, func(generic map[string]any) {
		perms, _ := generic["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
			generic["permissions"] = perms
		}
		perms["level"] = string(l)
	}); err != nil {
		return err
	}
	c.lvl.Set(l) // 工具链路活等级（D22 执行接入）
	return nil
}

// persistUnattended 实现 /unattended 与菜单/设置窗开关写回 config 的
// permissions.unattended（D185 口径四）：成功后同步活开关，工具与提问链路即时生效。
func (c *cfgWriter) persistUnattended(on bool) error {
	if err := persistConfig(c.path, func(generic map[string]any) {
		perms, _ := generic["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
			generic["permissions"] = perms
		}
		perms["unattended"] = on
	}); err != nil {
		return err
	}
	if c.unattended != nil {
		c.unattended.Set(on)
	}
	return nil
}

// persistPlugins 实现 /plugin 状态写回 config 的 plugins 段（D31）：键缺失语义靠
// 省略表达（enabled 缺省启用、granted 缺省空、trusted 缺省未信任）。
// trusted 是 D138 的启动信任指纹：**随 profile 走**（本文件就在 profiles/<name>/ 下）——两个
// profile 对同一个插件可以有各自的信任决定，不会因切 profile 而串味。
func (c *cfgWriter) persistPlugins(states map[string]plugin.State) error {
	return persistConfig(c.path, func(generic map[string]any) {
		pm := make(map[string]any, len(states))
		for name, st := range states {
			entry := map[string]any{}
			if st.Enabled != nil {
				entry["enabled"] = *st.Enabled
			}
			if len(st.Granted) > 0 {
				entry["granted"] = st.Granted
			}
			if st.Trusted != "" {
				entry["trusted"] = st.Trusted
			}
			pm[name] = entry
		}
		generic["plugins"] = pm
	})
}

// persistModel 实现 /model 切换写回 config 的 model.name（D32）。
func (c *cfgWriter) persistModel(name string) error {
	return persistConfig(c.path, func(generic map[string]any) {
		modelSection(generic)["name"] = name
	})
}

// persistThink / persistEffort 实现 /think /effort 写回 config 的 model 段（D34；
// effort 空档 = 清除键，不发送）。
func (c *cfgWriter) persistThink(on bool) error {
	return persistConfig(c.path, func(generic map[string]any) {
		modelSection(generic)["think"] = on
	})
}

func (c *cfgWriter) persistEffort(level string) error {
	return persistConfig(c.path, func(generic map[string]any) {
		model := modelSection(generic)
		if level == "" {
			delete(model, "reasoning_effort") // off = 清除（不发送）
			return
		}
		model["reasoning_effort"] = level
	})
}

// persistToolsDisabled 写回 config 的 tools.disabled（S6/D143）：只在设置窗动开关时
// 调用（推导态不写盘）；空集 = 显式空数组（≠ 键缺失的推导 ["think"]，见 builtinDisabled）。
func (c *cfgWriter) persistToolsDisabled(disabled []string) error {
	return persistConfig(c.path, func(generic map[string]any) {
		tools, _ := generic["tools"].(map[string]any)
		if tools == nil {
			tools = map[string]any{}
			generic["tools"] = tools
		}
		if len(disabled) == 0 {
			disabled = []string{} // 显式空数组（≠ 键缺失推导 ["think"]；nil 会编码成 null）
		}
		tools["disabled"] = disabled
	})
}
