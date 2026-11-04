package uigui

// 欢迎 / 首次运行引导窗（D141/§15.7）：**同一套帧、两个宿主**——
//  1. 装配前的独立向导窗（`RunSetupWindow`）：配置不完整（某条 provider 的 `secret:` 取不到）
//     时，`cmd/aquarius` 在**内核装配之前**先开它；保存即写 config，随后同进程继续启动；
//     取消 = 交回 SetupCancelled（调用方退出 + 指引）。宿主可注入（包级 var），测试不真开窗。
//  2. 主窗宿主的 `winWelcome` 次窗：设置窗「打开」入口 + 「`ui.welcomed` 缺失」的兜底
//     自动开一次（D263 起 welcomed 缺位的启动已由装配前向导接管，正常到不了这里）。
//
// 数据面经 `WelcomeManager`（消费方定义的小接口，装配根实现——同 SettingsSnapshot /
// ProviderManager 口径）；表单状态仅本宿主 goroutine 读写，不跨窗共享（§15.7 并发模型）。
// **密钥只写不回显**（快照结构无密钥字段，D35）。

// 本文件只留契约类型、表单状态与动作；文案见 welcome_tutorial.go，帧骨架见
// welcome_view.go，各页正文见 welcome_pages.go。

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/legal"
)

// WelcomeSnapshot 欢迎窗快照（开窗现取；**密钥永不进快照**，D35）。
type WelcomeSnapshot struct {
	Provider string   // 当前 primary provider 名（首跑 = 模板 openai）
	BaseURL  string   // 该 provider 的 base_url
	Models   string   // 逗号分隔（首个 = 该 provider 的缺省请求模型）
	Welcomed bool     // ui.welcomed（键缺失 = 未看）
	Hotkey   string   // ui.hotkey（教程页展示；空 = Alt+A）
	Lang     string   // ui.lang（第 1 页语言三态初值；空 = auto）
	Consent  int      // ui.consent_version（已同意的条款版本；0 = 从未同意）
	Issues   []string // 「配置不完整」问题清单（缺密钥等）——由装配前宿主填，窗内首条提示
}

// WelcomePatch 欢迎窗提交（单条 provider 全量覆盖 + 置 `ui.welcomed`）。
type WelcomePatch struct {
	Provider string
	BaseURL  string
	APIKey   string // 只写不回显：空 = 保持原值（D35）
	Models   string // 逗号分隔；首个 = 缺省请求模型
	// ConsentVersion 本次同意到的条款版本（legal.Version）；Apply 一并写回。
	ConsentVersion int
}

// WelcomeManager 欢迎窗数据面（消费方定义；装配根实现）。
type WelcomeManager interface {
	// Snapshot 开窗现取（密钥不回显）。
	Snapshot() WelcomeSnapshot
	// Apply 写回 `model.providers[0]`（覆盖）+ primary + `ui.welcomed`；失败报因、不落盘。
	Apply(p WelcomePatch) error
	// Test 连通性测试（真实最小请求；调用方 goroutine 负责，不进帧循环）。
	Test(p WelcomePatch) error
	// Models 拉取该 provider 的模型列表（只读；调用方 goroutine 负责）。
	Models(p WelcomePatch) ([]string, error)
	// MarkWelcomed 仅置 `ui.welcomed`（用户显式关闭 = 已看，不再自动弹）。
	MarkWelcomed() error
	// SetLang 写回 `ui.lang`（D278 第 1 页）；归一失败的取值由实现拒写。
	SetLang(lang string) error
	// SetConsent 仅写回 `ui.consent_version`（重过第 2 页时用：此时 provider 已经配好，
	// 走 Apply 会把整条 provider 再覆盖一遍）。
	SetConsent(version int) error
}

// SetupOutcome 装配前向导窗的结束原因。
type SetupOutcome int

const (
	// SetupCancelled 用户取消 / 关窗：调用方按「配置仍不完整」处置（退出 + 指引）。
	SetupCancelled SetupOutcome = iota
	// SetupDone 已写盘：调用方重新读配置、同进程继续启动。
	SetupDone
)

// String 结束原因（诊断/测试可读）。
func (o SetupOutcome) String() string {
	if o == SetupDone {
		return i18n.T("ui.welcome.outcome_done")
	}
	return i18n.T("ui.welcome.outcome_cancelled")
}

// SetupRequest 装配前向导窗请求（cmd/aquarius 在「配置不完整」时调用）。
type SetupRequest struct {
	Theme  string // ui.theme（system | light | dark；空 = system）
	Mgr    WelcomeManager
	Issues []string // 问题清单（首屏提示：缺哪个 provider 的哪个环境变量）
	// ConsentOnly 只补一次同意（D278 决策 4）：引导已完成、配置齐全，只因条款改版而来。
	// 窗体从第 2 页起，页脚只有「同意并继续」。
	ConsentOnly bool
}

// welcomePage 向导页（D278：五页固定顺序，可回退）。
type welcomePage int

const (
	// welcomeIntro 第 1 页：品牌、欢迎词与界面语言。
	welcomeIntro welcomePage = iota
	// welcomeTerms 第 2 页：用户协议与隐私政策 + 勾选。
	welcomeTerms
	// welcomeAccess 第 3 页：接入模型（可跳过）。
	welcomeAccess
	// welcomeTutorial 第 4 页：操作指引。
	welcomeTutorial
	// welcomeDone 第 5 页：完成（摘要 + 保存）。
	welcomeDone
)

// welcomePageCount 总页数（步进条的 n/N 与翻页边界共用一处）。
const welcomePageCount = int(welcomeDone) + 1

// welcomePageName 页名（每帧取词：第 1 页切语言后它要跟着变）。
func welcomePageName(p welcomePage) string {
	switch p {
	case welcomeIntro:
		return i18n.T("ui.welcome.step.intro")
	case welcomeTerms:
		return i18n.T("ui.welcome.step.terms")
	case welcomeAccess:
		return i18n.T("ui.welcome.step.access")
	case welcomeTutorial:
		return i18n.T("ui.welcome.step.tutorial")
	default:
		return i18n.T("ui.welcome.step.done")
	}
}

// welcomeEnv 帧环境（**与 *UI 解耦**：装配前的宿主没有 UI 实例）。
type welcomeEnv struct {
	mgr   WelcomeManager
	setup bool               // true = 装配前的独立向导窗
	snap  WelcomeSnapshot    // 开窗快照（填表基准 + 问题提示）
	done  func(SetupOutcome) // 结束宿主（幂等；由表单保证只调一次）
	inval func()             // 异步测试结果回灌重绘（headless = nil）
	// consentOnly 只重过第 2 页（D278 决策 4：条款改版后其余页不再打扰）。
	// 此时第 2 页是唯一的页，页脚只留「同意并继续」，没有上一步与退出。
	consentOnly bool
	retitle     func() // 切语言后重设窗标题（装配前宿主给；其余宿主 nil = 只记录）
}

// welcomeForm 表单状态（宿主 goroutine 独占）。
type welcomeForm struct {
	env  welcomeEnv
	page welcomePage
	// 接入模型页控件 + 教程页滚动列表。
	name, baseURL, apiKey, models widget.Editor
	tutList                       widget.List
	// consent 第 2 页的勾选闸门（D278）：未勾选时「继续」不可点。
	consent                widget.Bool
	testBtn                widget.Clickable
	modelsBtn              widget.Clickable // 「获取模型」（D153）
	prev, next, save, skip widget.Clickable
	// 反馈（本 goroutine 独占；"" = 默认提示）。
	status string
	err    bool
	// 连通性内联结果（模拟稿「● 连通正常 · 128 ms」）：与表单错误的展示位置分开——
	// 测试结果属于「测试连通」这一行，不是整页的错误提示。
	probe     string
	probeErr  bool
	probeBusy bool
	// 候选（D153）：点击 chip = 用该名字替换模型名行。
	cands      []string
	candClicks []widget.Clickable
	modelsHint string // 候选行说明（"已自动填入 N 个候选 · 点选替换"）
	modelsOrig string // 开窗基线：只在没被用户改过时自动填入
	// 每页各用一条滚动列表：内容比窗高时（候选 chip 占几行、200% 字号下正文折成十几行）
	// 不能压到页脚。用 layout.List 而非 material.List / widget.List：后两者常驻画滚动条
	// （静态表单里一条灰色竖条看着像故障），前者只在滚轮/拖动时移动内容。
	// introList / termsList 在 200% 字号下是必需品：离屏实测第 1 页正文被页脚切在半行上。
	introList, termsList, accessList, doneList layout.List
	// 第 2 页的法务正文档（进入本页时按当前语言载入；语言变了重取）。
	terms       []termsRow
	termsLang   i18n.Lang
	termsLoaded bool
	termsErr    error
	// 第 1 页的语言三态（D278）：lang = 当前选择，lang0 = 已落盘的那个（相等 = 没有待写的改动）。
	// 只在本宿主的 goroutine 里读写，与设置窗的 ui.lang 行同一个取值域（auto | zh | en）。
	lang     i18n.Selection
	lang0    i18n.Selection
	langBtns [3]widget.Clickable
	// 连通性测试 / 获取模型的异步结果（测试 goroutine 写、帧循环排空）。
	testResults chan testOutcome
	// finished 结果已交（幂等：按钮连点/关窗竞争只结束一次）。
	finished bool
}

// langSelections 第 1 页三态的取值序（与 welcomeLangLabels 的文案序一一对应：auto/zh/en）。
var langSelections = [3]i18n.Selection{i18n.SelectionAuto, i18n.SelectionZh, i18n.SelectionEn}

// welcomeLangLabels 第 1 页三态展示名：后两项与设置窗共用（endonym 不翻译），
// auto 一项本页专用——设置窗里是下拉的一整行，写「Follow system」够宽；这里三等分
// 的一颗按钮在 200% 字号下会把它折成两行，选中项因此比另两项高出一截（离屏实测）。
func welcomeLangLabels() []string {
	labels := uiLangLevels()
	labels[0] = strings.TrimSpace(i18n.T("ui.welcome.lang.auto"))
	return labels
}

// newWelcomeForm 以快照构造表单（开窗与装配前宿主共用口径）。
func newWelcomeForm(env welcomeEnv) *welcomeForm {
	f := &welcomeForm{env: env, testResults: make(chan testOutcome, 4)}
	for _, ed := range []*widget.Editor{&f.name, &f.baseURL, &f.apiKey, &f.models} {
		ed.SingleLine = true // 单行档（Enter 无提交语义，推进走按钮）
	}
	// 构造不变量（S4c 教训：material.List 未设轴向 = Horizontal）。
	f.tutList.Axis = layout.Vertical
	f.introList.Axis = layout.Vertical
	f.termsList.Axis = layout.Vertical
	f.accessList.Axis = layout.Vertical
	f.doneList.Axis = layout.Vertical
	f.name.SetText(env.snap.Provider)
	f.baseURL.SetText(env.snap.BaseURL)
	f.models.SetText(env.snap.Models)
	f.modelsOrig = env.snap.Models
	// 语言三态初值 = 快照里的 ui.lang；解析失败按 auto 处理（配置里的非法值由启动期
	// fail-fast 拦，走到这里说明是装配前宿主读到的旧值，不该让向导打不开）。
	if sel, err := i18n.Parse(env.snap.Lang); err == nil {
		f.lang = sel
	} else {
		f.lang = i18n.SelectionAuto
	}
	f.lang0 = f.lang
	if env.consentOnly {
		f.page = welcomeTerms
	}
	return f
}

// pickLang 第 1 页选语言（D278）：**装配前当场生效**——文案在装配前由 i18n 全局表提供，
// 页头、页名、页脚按钮每帧现取词，所以一次 Use 就把整窗换成新语言，窗标题额外重设一次。
// 装配后（in-app 次窗）只记录：主窗的文案已散进各视图与测量，热切语言属于启动期决策（D275）。
func (f *welcomeForm) pickLang(sel i18n.Selection) {
	if sel == f.lang {
		return
	}
	f.lang = sel
	f.status, f.err = "", false
	if !f.env.setup {
		return
	}
	i18n.Use(sel)
	if f.env.retitle != nil {
		f.env.retitle()
	}
}

// saveLang 落盘（只写变化过的那个）：语言是用户的第一个选择，**离开第 1 页就写**——
// 而不是等到末页保存。中途取消（配置不完整、退出向导）时语言选择照样留着，下次进来
// 不用再选一遍。写失败不阻断翻页以外的事：翻页/保存会把它当页面错误报出来。
func (f *welcomeForm) saveLang() error {
	if f.lang == f.lang0 || f.env.mgr == nil {
		return nil
	}
	if err := f.env.mgr.SetLang(string(f.lang)); err != nil {
		return err
	}
	f.lang0 = f.lang
	return nil
}

// patch 当前填入 → 提交（apiKey 只写不回显，空 = 保持原值）。
func (f *welcomeForm) patch() WelcomePatch {
	return WelcomePatch{
		Provider:       strings.TrimSpace(f.name.Text()),
		BaseURL:        strings.TrimSpace(f.baseURL.Text()),
		APIKey:         strings.TrimSpace(f.apiKey.Text()),
		Models:         strings.TrimSpace(f.models.Text()),
		ConsentVersion: legal.Version,
	}
}

// accessErr 接入页校验（推进 / 测试 / 保存前调用）。问题清单非空 = 本次是为「密钥取不到」
// 而来（装配根的 missingKeys 逐条都是密钥项），密钥不填就起不来——引用或明文都收。
func (f *welcomeForm) accessErr() error {
	p := f.patch()
	if p.Provider == "" {
		return errors.New(i18n.T("ui.welcome.err.provider_empty"))
	}
	if p.BaseURL == "" {
		return errors.New(i18n.T("ui.welcome.err.base_url_empty"))
	}
	if p.Models == "" {
		return errors.New(i18n.T("ui.welcome.err.models_empty"))
	}
	if f.env.setup && len(f.env.snap.Issues) > 0 && p.APIKey == "" {
		return errors.New(i18n.T("ui.welcome.err.key_required"))
	}
	return nil
}

// fail 置错误反馈。
func (f *welcomeForm) fail(err error) {
	f.err = true
	f.status = err.Error()
}

// finish 结束宿主（幂等）。
func (f *welcomeForm) finish(out SetupOutcome) {
	if f.finished {
		return
	}
	f.finished = true
	if f.env.done != nil {
		f.env.done(out)
	}
}

// pageErr 离开**当前页**前的闸门（下一页按钮与保存前都走它）。
// 第 2 页要勾选同意；第 3 页要接入参数齐全；其余页无闸门。
func (f *welcomeForm) pageErr() error {
	switch f.page {
	case welcomeTerms:
		if !f.consent.Value {
			return errors.New(i18n.T("ui.welcome.err.consent_required"))
		}
	case welcomeAccess:
		return f.accessErr()
	}
	return nil
}

// canAdvance 当前页的「继续」是否可以点：页脚的禁用态与 onNext 的闸门用同一判据，
// 不给「按钮亮着但点了报错」留口子。
func (f *welcomeForm) canAdvance() bool { return f.pageErr() == nil }

// onNext 下一页（先过当前页闸门）。
func (f *welcomeForm) onNext() {
	if err := f.saveLang(); err != nil {
		f.fail(err)
		return
	}
	if err := f.pageErr(); err != nil {
		f.fail(err)
		return
	}
	// 只重过第 2 页时，本页的「继续」就是「同意并继续」：直接落盘收尾。
	if f.env.consentOnly {
		f.onSave()
		return
	}
	if f.page < welcomeDone {
		f.page++
	}
	f.status, f.err = "", false
}

// onPrev 上一页（第 1 页无上一步）。
func (f *welcomeForm) onPrev() {
	if f.page > welcomeIntro {
		f.page--
	}
	f.status, f.err = "", false
}

// onSave 保存：Apply（providers[0] + primary + ui.welcomed）→ 结束宿主。
// 校验不过退回对应页并把原因写在状态行（不静默失败）：同意没勾退回第 2 页，
// 接入参数不齐退回第 3 页——用户能直接看见是哪一页缺东西。
func (f *welcomeForm) onSave() {
	if f.env.mgr == nil {
		f.fail(errors.New(i18n.T("ui.welcome.err.not_wired")))
		return
	}
	if err := f.saveLang(); err != nil {
		f.page = welcomeIntro
		f.fail(err)
		return
	}
	if !f.consent.Value {
		f.page = welcomeTerms
		f.fail(errors.New(i18n.T("ui.welcome.err.consent_required")))
		return
	}
	// 只为条款改版重过一次（D278 决策 4）：provider 早就配好了，只写同意版本，
	// 不走 Apply——那会把整条 providers[0] 拿旧快照再覆盖一遍。
	if f.env.consentOnly {
		if err := f.env.mgr.SetConsent(legal.Version); err != nil {
			f.page = welcomeTerms
			f.fail(err)
			return
		}
		f.finish(SetupDone)
		return
	}
	if err := f.accessErr(); err != nil {
		f.page = welcomeAccess
		f.fail(err)
		return
	}
	if err := f.env.mgr.Apply(f.patch()); err != nil {
		f.page = welcomeAccess
		f.fail(err)
		return
	}
	f.finish(SetupDone)
}

// onSkip 跳过 / 关闭：装配前 = 交回取消（调用方退出 + 指引）；in-app = 置
// `ui.welcomed` 后关窗（显式关闭即「已看」，不再自动弹；置位失败不阻断关窗）。
func (f *welcomeForm) onSkip() {
	// 语言选择在这一步单独落盘（不管后面是走是留）：它是用户已经做完的一个决定，
	// 不该因为「配置还没填完就退出」一起丢掉。失败静默——窗马上就关了。
	_ = f.saveLang()
	// 只重过第 2 页时没有「以后再说」：不同意就不能继续用（页脚也不给这个按钮，
	// 这里兜一手窗标题栏关闭）。
	if f.env.consentOnly {
		f.finish(SetupCancelled)
		return
	}
	if f.env.setup {
		f.finish(SetupCancelled)
		return
	}
	if f.env.mgr != nil {
		_ = f.env.mgr.MarkWelcomed()
	}
	f.finish(SetupDone)
}

// onTest 连通性测试（真实最小请求；goroutine 跑，结果经通道回灌帧循环）。
// 结果落在「测试连通」这一行的内联状态里（模拟稿「● 连通正常 · 128 ms」），不占整页
// 错误行——测试成功不是「页面级反馈」，写在页脚会像保存结果。
func (f *welcomeForm) onTest() {
	if f.env.mgr == nil {
		f.fail(errors.New(i18n.T("ui.welcome.err.not_wired")))
		return
	}
	if err := f.accessErr(); err != nil {
		f.fail(err)
		return
	}
	p := f.patch()
	f.probeBusy, f.probe, f.probeErr = true, "", false
	go func() {
		start := time.Now()
		err := f.env.mgr.Test(p)
		f.post(testOutcome{kind: probePing, name: p.Provider, err: err,
			ms: time.Since(start).Milliseconds()})
	}()
}

// onFetchModels 「获取模型」：拉一次 /models，结果回灌为候选 chip（D153）。
func (f *welcomeForm) onFetchModels() {
	if f.env.mgr == nil {
		f.fail(errors.New(i18n.T("ui.welcome.err.not_wired")))
		return
	}
	if err := f.accessErr(); err != nil {
		f.fail(err)
		return
	}
	p := f.patch()
	f.modelsHint = i18n.T("ui.welcome.models_fetching")
	go func() {
		start := time.Now()
		models, err := f.env.mgr.Models(p)
		f.post(testOutcome{kind: probeModels, name: p.Provider, err: err,
			ms: time.Since(start).Milliseconds(), models: models})
	}()
}

// post 投递异步结果（槽满丢最旧：只是状态反馈，不积压），并唤醒帧循环。
func (f *welcomeForm) post(out testOutcome) {
	select {
	case f.testResults <- out:
	default:
		select {
		case <-f.testResults:
		default:
		}
		f.testResults <- out
	}
	if f.env.inval != nil {
		f.env.inval()
	}
}

// drain 帧循环排空异步结果（最新一条覆盖展示）。
func (f *welcomeForm) drain() {
	for {
		select {
		case r := <-f.testResults:
			if r.kind == probeModels {
				f.applyModels(r)
				continue
			}
			f.probeBusy = false
			if r.err != nil {
				f.probeErr = true
				f.probe = i18n.Tf("ui.welcome.probe_failed", "err", r.err.Error())
			} else {
				f.probeErr = false
				f.probe = i18n.Tf("ui.welcome.probe_ok", "ms", strconv.FormatInt(r.ms, 10))
			}
		default:
			return
		}
	}
}

// applyModels 「获取模型」结果：候选列成 chip；模型名行仍是基线内容时自动填入前 8 个
// （用户手写过就不动，避免一次网络结果冲掉他刚敲的名字）。
func (f *welcomeForm) applyModels(r testOutcome) {
	if r.err != nil {
		f.cands, f.candClicks = nil, nil
		f.modelsHint = i18n.Tf("ui.welcome.models_failed", "err", r.err.Error())
		return
	}
	if len(r.models) == 0 {
		f.cands, f.candClicks = nil, nil
		f.modelsHint = i18n.T("ui.welcome.models_empty")
		return
	}
	total := len(r.models)
	fill := r.models
	if len(fill) > relModelAutoFill {
		fill = fill[:relModelAutoFill]
	}
	pristine := strings.TrimSpace(f.models.Text()) == strings.TrimSpace(f.modelsOrig)
	if pristine {
		f.models.SetText(strings.Join(fill, ", "))
		f.modelsOrig = f.models.Text()
	}
	if len(r.models) > provModelCandMax {
		r.models = r.models[:provModelCandMax]
	}
	f.cands = r.models
	f.candClicks = make([]widget.Clickable, len(r.models))
	if !pristine {
		f.modelsHint = i18n.Tf("ui.welcome.models_picked", "n", total)
		return
	}
	if total > len(fill) {
		f.modelsHint = i18n.Tf("ui.welcome.models_filled_total", "n", len(fill), "total", total)
	} else {
		f.modelsHint = i18n.Tf("ui.welcome.models_filled", "n", len(fill))
	}
}
