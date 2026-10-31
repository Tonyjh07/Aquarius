package main

// model 段（D173）：provider、密钥池、模型条目、能力位、降级链、采样与单价——声明结构
// 加全部推导（缺省能力位、整链能力取与、降级链快照）与启动期校验（normalize）。

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

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
	// Headers 附加到每次 chat 请求的自定义头（D253）：兼容端点（自建网关/镜像）需要的
	// 路由或计费头。**只对 openai-chat 生效**（同 UnsupportedParams 口径）。值原样发送，
	// 同名时覆盖适配器的默认头与鉴权头。
	Headers map[string]string `json:"headers,omitempty"`
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
	// Caps 能力位：nil = 键缺失（按协议缺省，§8 的表）；非 nil = 以声明为准
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
	// Sampling 逐模型采样参数覆盖（D204）：只写想改的那几项，其余继承 model.sampling
	// （Overlay 合并）。键缺失 = 完全继承。
	Sampling *samplingConfig `json:"sampling,omitempty"`
	// CostIn / CostOut 单价，**每百万 token**（D205，与厂商报价同口径）；0 = 未声明。
	CostIn  float64 `json:"cost_in,omitempty"`
	CostOut float64 `json:"cost_out,omitempty"`
}

// isEnabled 该模型条目是否启用（D180：键缺失 = 启用）。
func (m *modelEntry) isEnabled() bool { return m.Enabled == nil || *m.Enabled }

// fallbackEntry 降级链一项（D173）：provider + 可选模型。Model 空 = 该 provider 的
// models[0]；同一 provider 可重复出现（同家不同型号兜底），但二元组不得重复。
type fallbackEntry struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
}

// samplingConfig 采样参数声明（D204）。**浮点用指针**：键缺失 / null = 不发送（交服务端
// 默认），写 0 就是真的发 0——temperature 0（要确定性输出）不能与「没写」共用零值。
type samplingConfig struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   int      `json:"max_tokens,omitempty"` // 0 = 不发送（真实上限仍由本轮预算决定）
	Stop        []string `json:"stop,omitempty"`       // 空 = 不发送
}

// toSampling 声明 → 端口采样参数。
//
// Stop **原样保留**（不做 trim）：`"\n"`、`" "` 这类本身就是合法停止串，去掉首尾空白
// 会把用户写的串改成另一个串；只拒绝**空串**（那没法当停止条件）。
func (s samplingConfig) toSampling() port.Sampling {
	return port.Sampling{
		Temperature: s.Temperature,
		TopP:        s.TopP,
		MaxTokens:   s.MaxTokens,
		Stop:        append([]string(nil), s.Stop...),
	}
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
	// Sampling 整链缺省采样参数（D204）；键缺失 = 全不发送。逐模型覆盖见
	// modelEntry.Sampling（Overlay 合并）。
	Sampling samplingConfig `json:"sampling,omitempty"`
}

// 协议标识（D173）；空 kind 归一为 openai-chat。
const (
	kindOpenAIChat      = "openai-chat"
	kindOpenAIResponses = "openai-responses"
	kindAnthropic       = "anthropic"
)

// temperatureMaxAnthropic Anthropic Messages API 只接 temperature 0..1（OpenAI 两协议接 0..2，
// 见 port.SettingsTemperatureMax）。这是协议差异，不是「允许用户填到哪」——上限更宽的那档
// 由设置窗与启动校验共用同一张表，协议细分只在这里再收紧。
const temperatureMaxAnthropic = 1.0

// protocolKind 归一后的协议标识（空 = openai-chat）。
func (p *providerConfig) protocolKind() string {
	if k := strings.TrimSpace(p.Kind); k != "" {
		return k
	}
	return kindOpenAIChat
}

// defaultCaps 协议缺省能力位（D173，§8 的表）：只在模型条目省略 caps 时使用。
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

// samplingTables 采样参数的热生效快照（D204）：整链缺省 + 逐模型覆盖。装配根保存一个
// 原子指针，Agent 每轮按当前模型名现取；设置窗「保存并应用」后换掉整份即可热生效，
// 不必重建 Agent。
type samplingTables struct {
	base    port.Sampling
	byModel map[string]port.Sampling
}

// For 取某模型的生效采样参数：缺省叠该模型的覆盖（没覆盖就只返回缺省）。
func (t *samplingTables) For(model string) port.Sampling {
	if t == nil {
		return port.Sampling{}
	}
	if o, ok := t.byModel[model]; ok {
		return t.base.Overlay(o)
	}
	return t.base
}

// samplingTables 从模型配置现算一份快照。
func (m *modelConfig) samplingTables() *samplingTables {
	return &samplingTables{base: m.Sampling.toSampling(), byModel: m.samplingOverrides()}
}

// samplingFor 某模型（provider+model 二元组）的生效采样参数（D204）：model.sampling 作
// 整链缺省，该模型条目自带的覆盖只改它写了的项。
//
// 降级路径要用**降级目标自己那份**：模型名在装饰器里才确定，而参数得跟着改。
func (m *modelConfig) samplingFor(provider, model string) port.Sampling {
	base := m.Sampling.toSampling()
	p := m.byName(provider)
	if p == nil {
		return base
	}
	var e *modelEntry
	if model == "" {
		e = p.modelByName(p.models0())
	} else {
		e = p.modelByName(model)
	}
	if e == nil || e.Sampling == nil {
		return base
	}
	return base.Overlay(e.Sampling.toSampling())
}

// samplingOverrides 逐模型覆盖表（D204）：模型名 → 该条目的覆盖（不含整链缺省）。
// Agent 每轮按当前模型名合并，故这张表只放「这个模型与缺省不同的那几项」。
// 同名模型出现在多个 provider 时取首个声明（primary 在列表前，符合直觉）。
func (m *modelConfig) samplingOverrides() map[string]port.Sampling {
	out := map[string]port.Sampling{}
	for _, p := range m.Providers {
		for _, e := range p.Models {
			if e.Sampling == nil {
				continue
			}
			if _, seen := out[e.Name]; seen {
				continue
			}
			out[e.Name] = e.Sampling.toSampling()
		}
	}
	return out
}

// currentSampling primary 当前模型的生效采样参数（Agent 每轮注入的那份）。
func (m *modelConfig) currentSampling() port.Sampling {
	return m.samplingFor(m.primaryProvider().Name, m.Name)
}

// costFor 某模型的单价（D205，每百万 token）；未声明 = 零值（界面不显示费用）。
func (m *modelConfig) costFor(provider, model string) (in, out float64) {
	p := m.byName(provider)
	if p == nil {
		return 0, 0
	}
	var e *modelEntry
	if model == "" {
		e = p.modelByName(p.models0())
	} else {
		e = p.modelByName(model)
	}
	if e == nil {
		return 0, 0
	}
	return e.CostIn, e.CostOut
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
			// D205：单价每百万 token，来自 config 声明（服务端 /models 不报价格）。
			info.CostIn, info.CostOutUSD = e.CostIn, e.CostOut
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

// validateSampling 采样参数的取值范围校验（D204，启动期一次）。只拦「服务端必然拒收」的
// 声明，不替用户改值——温度 1.5 被静默截到 1.0，比启动报错更难查（用户以为设的是 1.5）。
//
// 协议差异：Chat Completions / Responses 接受 temperature 0..2；Anthropic Messages API
// 只接受 0..1。top_p 三协议都是 0..1。
func validateSampling(s port.Sampling, kind, where string) error {
	if t := s.Temperature; t != nil {
		hi := port.SettingsTemperatureMax
		if kind == kindAnthropic {
			hi = temperatureMaxAnthropic
		}
		if *t < port.SettingsTemperatureMin || *t > hi {
			return fmt.Errorf("%s 的 sampling.temperature = %v 超出范围（该协议接受 0..%g）", where, *t, hi)
		}
	}
	if p := s.TopP; p != nil && (*p < port.SettingsTopPMin || *p > port.SettingsTopPMax) {
		return fmt.Errorf("%s 的 sampling.top_p = %v 超出范围（接受 0..%g）", where, *p, port.SettingsTopPMax)
	}
	if s.MaxTokens < 0 {
		return fmt.Errorf("%s 的 sampling.max_tokens 不可为负数（%d）；不限制就留空", where, s.MaxTokens)
	}
	for _, v := range s.Stop {
		if v == "" {
			return fmt.Errorf("%s 的 sampling.stop 含空串（停止串不能为空）", where)
		}
	}
	return nil
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
			// 采样参数（D204）：逐模型按**合并后**的生效值校验（该模型自己的覆盖叠在
			// model.sampling 上）——只校验将来真会发出去的那份。
			var overlay port.Sampling
			if e.Sampling != nil {
				overlay = e.Sampling.toSampling()
			}
			if err := validateSampling(m.Sampling.toSampling().Overlay(overlay), p.protocolKind(), p.Name+" / "+e.Name); err != nil {
				return err
			}
			// 单价（D205）：负数没有意义，也不该被当成「未声明」静默吞掉。
			if e.CostIn < 0 || e.CostOut < 0 {
				return fmt.Errorf("model.providers[%d] (%s) 的模型 %q 的 cost_in / cost_out 不可为负数",
					i, p.Name, e.Name)
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
