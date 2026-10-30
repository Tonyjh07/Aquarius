package uigui

// 设置窗的 provider 编辑区：provider / 密钥 / 模型 / 降级链四类表单，及其构造、摘要与加载。

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// providerForm 单个 provider 的编辑控件组（D110②；本窗 goroutine 独占）。S11-7② 起
// 折叠成一张卡片（D176 口径二）：收起态只露一行摘要（`rowProvSummary`），展开才露出
// 下面的字段行。S11-7③ 起展开区含**密钥池**（多把，`keyForms`）与**模型能力表**
// （每行 name + 三个能力勾选 + 上下文窗口 + 「探测能力」按钮，`modelForms`）。
// 同一时刻只展开一张（展开多张又回到「一堵墙」）。
type providerForm struct {
	orig        string // 快照原名（Apply 保留无关键；"" = 新增）
	name        widget.Editor
	kind        selectField // 协议（openai-chat / openai-responses / anthropic）
	baseURL     widget.Editor
	tokenizer   widget.Editor
	unsupported widget.Editor // 逗号分隔
	test        widget.Clickable
	modelsBtn   widget.Clickable // 「获取模型」（D153）：拉 /models 并列候选
	del         widget.Clickable
	setPrimary  widget.Clickable
	// 折叠卡片（S11-7②/D176 口径二）：toggle = 摘要行点击件；expanded 由表单级
	// provOpen 决定。
	toggle widget.Clickable
	// 密钥池（S11-7③/D173）：每把一组（label + 值只写不回显 + 删除），尾部「＋ 添加」。
	keyForms []*providerKeyForm
	keyAdd   widget.Clickable
	// 模型列表（S11-7③/D173；D181 起一张表装全部模型）：每行一组（启用 + 名字 + 三能力
	// 勾选 + 上下文窗口 + 探测 + 删除），尾部「＋ 添加模型」；行数不设上限，界面按
	// provModelPageSize 分页（modelPage = 当前页，越界由 Rows 夹回）。
	modelForms []*providerModelForm
	modelAdd   widget.Clickable
	modelPage  int
	// 分页按钮（D181）：行数超过一页时才画（provModelPageSize）。
	modelPagePrev widget.Clickable
	modelPageNext widget.Clickable
	// 服务端清单一行的反馈（自动拉取 / 「获取模型」共用）：成功报新增了几个、
	// 失败报原因；空 = 还没拉过。只挂在本卡片里，不占全局状态行。
	fetchNote string
	fetchErr  bool
}

// providerKeyForm 密钥池单槽编辑控件组（S11-7③）。
type providerKeyForm struct {
	snap  ProviderKeySnapshot // 快照（读侧形态：已入库 / 明文 / 空；值不回显）
	label widget.Editor       // 展示名（空 = 「第 N 把」）
	value widget.Editor       // 值（只写不回显；空 = 保持；secret: 前缀 = 照写引用；否则明文入库）
	del   widget.Clickable
}

// providerModelForm 模型能力表单行编辑控件组（S11-7③）：启用勾选 + 能力位三勾选 +
// 上下文窗口 + 探测按钮。base* 记录填表基线（快照生效值），只有用户动过勾选才把能力位
// 固化成显式声明（键缺失 = 协议缺省；不动就保持「缺省」这个形态）。
type providerModelForm struct {
	enabled     widget.Bool // D180：是否启用（勾选态；不启用 = 保留声明但不参与选型）
	name        widget.Editor
	capsTools   widget.Bool
	capsReason  widget.Bool
	capsVision  widget.Bool
	explicit    bool // 快照里该条目本来就是显式声明（用于摘要/回灌判定，不改变写回路径）
	baseTools   bool // 填表基线（判定「用户是否动过勾选」）
	baseReason  bool
	baseVision  bool
	contextWin  widget.Editor // 上下文窗口 tokens（空/0 = 不声明，跟随全局）
	costIn      widget.Editor // 输入单价（每百万 token；空/0 = 不声明）
	costOut     widget.Editor // 输出单价（每百万 token；空/0 = 不声明）
	probe       widget.Clickable
	del         widget.Clickable
	probeBusy   bool   // 探测在途（按钮右侧转圈；结果回灌即收）
	probeStatus string // 探测反馈（成功绿/错误红，行内）
	probeErr    bool
}

// touched 用户是否动过能力勾选（与填表基线比对）。
func (mf *providerModelForm) touched() bool {
	return mf.capsTools.Value != mf.baseTools ||
		mf.capsReason.Value != mf.baseReason ||
		mf.capsVision.Value != mf.baseVision
}

// providerSummary 折叠卡片收起态的一行摘要：名字 · 协议 · N 个模型 · M 把钥匙 · 是否主。
// 名字与协议取自**当前编辑值**（改动过程中也如实反映），钥匙数取编辑区行数。
func (pf *providerForm) providerSummary(primary bool) string {
	name := strings.TrimSpace(pf.name.Text())
	if name == "" {
		name = "（未命名）"
	}
	switch strings.TrimSpace(pf.kind.value()) {
	case "openai-responses":
		name += " · responses"
	case "anthropic":
		name += " · anthropic"
	default:
		name += " · chat"
	}
	keys := len(pf.keyForms)
	if keys < 1 {
		keys = 1 // 0 条声明也按缺省引用算一把（读侧同口径）
	}
	s := fmt.Sprintf("%s · %d 个模型 · %d 把钥匙", name, len(pf.modelForms), keys)
	if primary {
		s += " · ★ 主"
	}
	return s
}

// fallbackForm 降级链一项的编辑控件组（D176 口径三；本窗 goroutine 独占）。
// 提供者与模型都得能改：provider 走枚举选择器（选项即 f.chainChoices），模型是单行
// 编辑档（可填未声明的名字——链是运行时路由）。
type fallbackForm struct {
	provName string        // 当前 provider 名（选项表里没有它时的兜底）
	prov     selectField   // 提供者（选项来自 chainChoices）
	model    widget.Editor // 模型名；空 = 取该 provider 的缺省模型
	up       widget.Clickable
	down     widget.Clickable
	del      widget.Clickable
}

// chainProvider 该项当前 provider 名（选择「不指定」= 空名；选项表为空 = 用保留的原始名）。
func (ff *fallbackForm) chainProvider() string {
	switch v := ff.prov.value(); {
	case v == chainPickNone:
		return ""
	case v != "":
		return v
	default:
		return ff.provName
	}
}

// chainPickNone 选择器里「不指定提供者」那一项的键值。
//
// 它不能与「空 provider 名」共用一个值：空串在 chainChoices 里是「没这项」，而这一项
// 是用户明确选的「不指定」。用一个不会与真名撞车的哨兵值区分两者。
const chainPickNone = "\x00none"

// testOutcome 异步探针结果（testResults 单消费者 = 帧循环）：连通测试 / 「获取模型」/
// 「探测能力」共用一条通道，靠 kind 区分（同一 provider 的多次动作不该交错成一条状态行）。
type testOutcome struct {
	kind    int // probePing / probeModels / probeCaps
	name    string
	model   string // kind == probeCaps：被探测的模型名
	err     error
	ms      int64        // 耗时（毫秒；只报成功，失败不报时延）
	models  []string     // kind == probeModels：候选（已去重保序）
	auto    bool         // kind == probeModels：切到本页时的自动拉取（结果只与本卡片有关）
	outcome ProbeOutcome // kind == probeCaps：探测结论
}

const (
	probePing   = iota // 连通测试
	probeModels        // 获取模型列表
	probeCaps          // 探测能力（D174/S11-7③）
)

// addProviderForm 追加一个空白 provider 编辑组（帧循环与测试共用；Apply 校验必填项）。
// S11-7②：新条目直接展开（刚建好就等着填，收起态反而要多点一次）。
func (f *settingsForm) addProviderForm() {
	f.provs = append(f.provs, newProviderForm())
	f.provOpen = len(f.provs) - 1 // 展开新条目（同时收起其它）
}

// newProviderForm 空白 provider 编辑组（字段单行档 + 默认一个模型行与一把钥匙）。
func newProviderForm() *providerForm {
	pf := &providerForm{
		kind: selectField{displays: kindLevels, values: kindValues},
	}
	for _, ed := range []*widget.Editor{&pf.name, &pf.baseURL, &pf.tokenizer, &pf.unsupported} {
		ed.SingleLine = true
	}
	pf.keyForms = append(pf.keyForms, newProviderKeyForm(ProviderKeySnapshot{}))
	pf.modelForms = append(pf.modelForms, newModelRow("", "", true))
	return pf
}

// newProviderKeyForm 由快照造一把钥匙的编辑控件组（值恒空——只写不回显，D35）。
func newProviderKeyForm(k ProviderKeySnapshot) *providerKeyForm {
	kf := &providerKeyForm{snap: k}
	kf.label.SingleLine = true
	kf.value.SingleLine = true
	kf.value.Mask = '•'
	kf.label.SetText(k.Label)
	return kf
}

// defaultCapsFor 某协议的缺省能力位（与装配根按协议给的缺省同口径，§8）：
// 只用于**新建模型行**时把勾选初值画成「缺省生效的样子」——这些行尚未写进 config，
// 拿不到生效值。键缺失的语义（写回时 Caps 仍为 nil）不受影响。
func defaultCapsFor(kind string) []string {
	switch kind {
	case "openai-responses":
		return []string{port.CapTools, port.CapReasoning, port.CapVision}
	case "anthropic":
		return []string{port.CapTools, port.CapVision}
	default: // openai-chat
		return []string{port.CapTools, port.CapReasoning}
	}
}

// newProviderModelForm 由快照造一个模型行的编辑控件组；勾选初值 = 快照生效能力
// （缺省时按协议缺省给全），并记下基线用于判定用户是否动过（动了才固化显式声明）。
func newProviderModelForm(m ProviderModelSnapshot) *providerModelForm {
	mf := &providerModelForm{explicit: m.CapsExplicit}
	mf.enabled.Value = m.Enabled
	mf.name.SingleLine = true
	mf.contextWin.SingleLine = true
	mf.costIn.SingleLine = true
	mf.costOut.SingleLine = true
	mf.name.SetText(m.Name)
	for _, c := range m.Caps {
		switch c {
		case port.CapTools:
			mf.capsTools.Value = true
		case port.CapReasoning:
			mf.capsReason.Value = true
		case port.CapVision:
			mf.capsVision.Value = true
		}
	}
	mf.baseTools, mf.baseReason, mf.baseVision = mf.capsTools.Value, mf.capsReason.Value, mf.capsVision.Value
	if m.ContextWindow > 0 {
		mf.contextWin.SetText(strconv.Itoa(m.ContextWindow))
	}
	// 单价（D205）：只在真的声明过时回显（0 = 未声明 = 留空），避免满屏「0」被当成免费。
	if m.CostIn > 0 {
		mf.costIn.SetText(strconv.FormatFloat(m.CostIn, 'f', -1, 64))
	}
	if m.CostOut > 0 {
		mf.costOut.SetText(strconv.FormatFloat(m.CostOut, 'f', -1, 64))
	}
	return mf
}

// newModelRow 新建一个模型行（「＋ 添加模型」/「获取模型」/候选 chip 三处共用）：按该
// provider 当前协议把缺省能力位画进勾选初值（未声明 → 写回时仍为键缺失）。enabled 区分
// 来路（D180）：用户主动加的行默认启用；自动拉回的清单默认不启用，由用户挑。
func newModelRow(name, kind string, enabled bool) *providerModelForm {
	return newProviderModelForm(ProviderModelSnapshot{Name: name, Caps: defaultCapsFor(kind), Enabled: enabled})
}

// loadProviders 以快照重建 provider 编辑区（开窗填表；快照空 = 无条目，留新增入口）。
// S11-7②：重建后全收起（展开态不跨重建保留——它是界面状态，重建即回到稳定落点）。
func (f *settingsForm) loadProviders(snap []ProviderSnapshot) {
	f.provs = make([]*providerForm, 0, len(snap))
	f.primaryIdx = 0
	f.provOpen = -1
	for i, p := range snap {
		pf := newProviderForm()
		pf.orig = p.Name
		pf.name.SetText(p.Name)
		pf.kind.setValue(p.Kind)
		pf.baseURL.SetText(p.BaseURL)
		pf.tokenizer.SetText(p.Tokenizer)
		pf.unsupported.SetText(p.Unsupported)
		// 密钥池与模型能力表按快照重建（值恒空、能力勾选取生效值、记下基线）。
		pf.keyForms = make([]*providerKeyForm, 0, len(p.Keys))
		for _, k := range p.Keys {
			pf.keyForms = append(pf.keyForms, newProviderKeyForm(k))
		}
		if len(pf.keyForms) == 0 {
			pf.keyForms = append(pf.keyForms, newProviderKeyForm(ProviderKeySnapshot{}))
		}
		pf.modelForms = make([]*providerModelForm, 0, len(p.Models))
		for _, m := range p.Models {
			pf.modelForms = append(pf.modelForms, newProviderModelForm(m))
		}
		if len(pf.modelForms) == 0 {
			pf.modelForms = append(pf.modelForms, newModelRow("", p.Kind, true))
		}
		if p.Primary {
			f.primaryIdx = i
		}
		f.provs = append(f.provs, pf)
	}
}

// loadChainChoices 降级链的 provider 选项（名字 + 启用的模型名）：取自 provider 编辑区
// 快照（同一份事实），故「模型服务」页改动后重开窗即同步；本窗内改 provider 列表不
// 影响链选项（那类改动要重启才生效，运行态本就不同步）。未启用的模型不进候选（D180）。
//
// 同时带上端点与**密钥引用名**（D222）：联网/语音页的「填入」按钮靠它把「借」变成
// 「抄一份能改的」。只抄引用名，值永不出编辑区（D35）。
func (f *settingsForm) loadChainChoices(snap []ProviderSnapshot) {
	f.chainChoices = make([]ProviderChoice, 0, len(snap))
	for _, p := range snap {
		names := make([]string, 0, len(p.Models))
		for _, m := range p.Models {
			if !m.Enabled {
				continue
			}
			names = append(names, m.Name)
		}
		choice := ProviderChoice{Name: p.Name, Models: names, BaseURL: p.BaseURL}
		if len(p.Keys) > 0 && p.Keys[0].Saved {
			choice.KeyRef = p.Keys[0].Ref
		}
		f.chainChoices = append(f.chainChoices, choice)
	}
}

// loadFallback 以快照重建降级链（开窗填表；空链 = 只留「添加」入口）。
func (f *settingsForm) loadFallback(chain []FallbackLink, choices []ProviderChoice) {
	f.snapChain = append([]FallbackLink(nil), chain...)
	f.chain = make([]*fallbackForm, 0, len(chain))
	for _, l := range chain {
		f.chain = append(f.chain, newFallbackForm(l, choices))
	}
}

// setAudioProviderChoices 语音页 provider 选择器选项：首项「未配置」，其后为 provider
// 快照里的名字；当前值不在快照内时补一项，避免开窗即把它悄悄改成「未配置」。
func (f *settingsForm) setAudioProviderChoices(cur string, c *selectField) {
	displays := []string{"未配置"}
	values := []string{""}
	for _, p := range f.chainChoices {
		if p.Name == "" || slices.Contains(values, p.Name) {
			continue
		}
		displays = append(displays, p.Name)
		values = append(values, p.Name)
	}
	if cur != "" && !slices.Contains(values, cur) {
		displays = append(displays, cur)
		values = append(values, cur)
	}
	c.displays, c.values = displays, values
	c.setValue(cur) // "" 命中首项 = 未配置
}

// addFallbackForm 追加一项空白降级链（帧循环与测试共用；provider 缺省取首个选项）。
func (f *settingsForm) addFallbackForm() {
	f.chain = append(f.chain, newFallbackForm(FallbackLink{}, f.chainChoices))
}

// newFallbackForm 由一项链配置造编辑控件组：选项 = 各 provider 名，末尾补一项「不指定」。
//
// 当前名不在选项里时（provider 被删、或手写的失联引用）也补进选项表——开窗直接保存
// 不会把它悄悄改成别的 provider。空名且已声明过（新增项）时缺省指向首个 provider。
func newFallbackForm(l FallbackLink, choices []ProviderChoice) *fallbackForm {
	displays := make([]string, 0, len(choices)+1)
	values := make([]string, 0, len(choices)+1)
	for _, c := range choices {
		if c.Name == "" || slices.Contains(values, c.Name) {
			continue
		}
		displays = append(displays, c.Name)
		values = append(values, c.Name)
	}
	cur := strings.TrimSpace(l.Provider)
	if cur != "" && !slices.Contains(values, cur) {
		displays = append(displays, cur)
		values = append(values, cur)
	}
	displays = append(displays, "（不指定）")
	values = append(values, chainPickNone)

	ff := &fallbackForm{provName: cur}
	ff.prov = selectField{displays: displays, values: values}
	if cur == "" && len(values) > 1 {
		ff.prov.idx = 0 // 新增项缺省指向首个 provider
	} else {
		ff.prov.setValue(cur) // 未命中 = 停在上次位置（不指定在末项）
	}
	ff.model.SingleLine = true
	if s := strings.TrimSpace(l.Model); s != "" {
		ff.model.SetText(s)
	}
	return ff
}
