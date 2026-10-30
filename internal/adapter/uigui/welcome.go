package uigui

// 欢迎 / 首次运行引导窗（D141/§15.7）：**同一套帧、两个宿主**——
//  1. 装配前的独立向导窗（`RunSetupWindow`）：配置不完整（某条 provider 的 `secret:` 取不到）
//     时，`cmd/aquarius` 在**内核装配之前**先开它；保存即写 config，随后同进程继续启动；
//     取消 = 交回 SetupCancelled（调用方退出 + 指引）。宿主可注入（包级 var），测试不真开窗。
//  2. 主窗宿主的 `winWelcome` 次窗：设置窗「打开」入口 + 「首跑 / `ui.welcomed` 缺失」时自动开一次。
//
// 数据面经 `WelcomeManager`（消费方定义的小接口，装配根实现——同 SettingsSnapshot /
// ProviderManager 口径）；表单状态仅本宿主 goroutine 读写，不跨窗共享（§15.7 并发模型）。
// **密钥只写不回显**（快照结构无密钥字段，D35）。

// 本文件只留契约类型、表单状态与动作；文案见 welcome_tutorial.go，帧骨架见
// welcome_view.go，各页正文见 welcome_pages.go。

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"
)

// WelcomeSnapshot 欢迎窗快照（开窗现取；**密钥永不进快照**，D35）。
type WelcomeSnapshot struct {
	Provider string   // 当前 primary provider 名（首跑 = 模板 openai）
	BaseURL  string   // 该 provider 的 base_url
	Models   string   // 逗号分隔（首个 = 该 provider 的缺省请求模型）
	Welcomed bool     // ui.welcomed（键缺失 = 未看）
	Hotkey   string   // ui.hotkey（教程页展示；空 = Alt+A）
	Issues   []string // 「配置不完整」问题清单（缺密钥等）——由装配前宿主填，窗内首条提示
}

// WelcomePatch 欢迎窗提交（单条 provider 全量覆盖 + 置 `ui.welcomed`）。
type WelcomePatch struct {
	Provider string
	BaseURL  string
	APIKey   string // 只写不回显：空 = 保持原值（D35）
	Models   string // 逗号分隔；首个 = 缺省请求模型
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
		return "已完成"
	}
	return "已取消"
}

// SetupRequest 装配前向导窗请求（cmd/aquarius 在「配置不完整」时调用）。
type SetupRequest struct {
	Theme  string // ui.theme（system | light | dark；空 = system）
	Mgr    WelcomeManager
	Issues []string // 问题清单（首屏提示：缺哪个 provider 的哪个环境变量）
}

// welcomePage 向导页。
type welcomePage int

const (
	welcomeAccess   welcomePage = iota // 接入模型（三问）
	welcomeTutorial                    // 教程
	welcomeDone                        // 完成（摘要 + 保存）
)

// welcomeEnv 帧环境（**与 *UI 解耦**：装配前的宿主没有 UI 实例）。
type welcomeEnv struct {
	mgr   WelcomeManager
	setup bool               // true = 装配前的独立向导窗
	snap  WelcomeSnapshot    // 开窗快照（填表基准 + 问题提示）
	done  func(SetupOutcome) // 结束宿主（幂等；由表单保证只调一次）
	inval func()             // 异步测试结果回灌重绘（headless = nil）
}

// welcomeForm 表单状态（宿主 goroutine 独占）。
type welcomeForm struct {
	env  welcomeEnv
	page welcomePage
	// 接入模型页控件 + 教程页滚动列表。
	name, baseURL, apiKey, models widget.Editor
	tutList                       widget.List
	testBtn                       widget.Clickable
	modelsBtn                     widget.Clickable // 「获取模型」（D153）
	prev, next, save, skip        widget.Clickable
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
	// 接入页 / 完成页各用一条滚动列表：内容比窗高时（候选 chip 占几行）不能压到页脚。
	// 用 layout.List 而非 material.List / widget.List：后两者常驻画滚动条（静态表单里
	// 一条灰色竖条看着像故障），前者只在滚轮/拖动时移动内容。
	accessList, doneList layout.List
	// 连通性测试 / 获取模型的异步结果（测试 goroutine 写、帧循环排空）。
	testResults chan testOutcome
	// finished 结果已交（幂等：按钮连点/关窗竞争只结束一次）。
	finished bool
}

// newWelcomeForm 以快照构造表单（开窗与装配前宿主共用口径）。
func newWelcomeForm(env welcomeEnv) *welcomeForm {
	f := &welcomeForm{env: env, testResults: make(chan testOutcome, 4)}
	for _, ed := range []*widget.Editor{&f.name, &f.baseURL, &f.apiKey, &f.models} {
		ed.SingleLine = true // 单行档（Enter 无提交语义，推进走按钮）
	}
	// 构造不变量（S4c 教训：material.List 未设轴向 = Horizontal）。
	f.tutList.Axis = layout.Vertical
	f.accessList.Axis = layout.Vertical
	f.doneList.Axis = layout.Vertical
	f.name.SetText(env.snap.Provider)
	f.baseURL.SetText(env.snap.BaseURL)
	f.models.SetText(env.snap.Models)
	f.modelsOrig = env.snap.Models
	return f
}

// patch 当前填入 → 提交（apiKey 只写不回显，空 = 保持原值）。
func (f *welcomeForm) patch() WelcomePatch {
	return WelcomePatch{
		Provider: strings.TrimSpace(f.name.Text()),
		BaseURL:  strings.TrimSpace(f.baseURL.Text()),
		APIKey:   strings.TrimSpace(f.apiKey.Text()),
		Models:   strings.TrimSpace(f.models.Text()),
	}
}

// accessErr 接入页校验（推进 / 测试 / 保存前调用）。问题清单非空 = 本次是为「密钥取不到」
// 而来（装配根的 missingKeys 逐条都是密钥项），密钥不填就起不来——引用或明文都收。
func (f *welcomeForm) accessErr() error {
	p := f.patch()
	if p.Provider == "" {
		return fmt.Errorf("provider 名不可为空")
	}
	if p.BaseURL == "" {
		return fmt.Errorf("接口地址（base_url）不可为空")
	}
	if p.Models == "" {
		return fmt.Errorf("模型名不可为空（逗号可多条，首个 = 缺省请求模型）")
	}
	if f.env.setup && len(f.env.snap.Issues) > 0 && p.APIKey == "" {
		return fmt.Errorf("请填写 API 密钥：可填 secret:<环境变量名> 引用环境变量，或直接填明文（保存时转入本机加密密钥库）")
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

// onNext 下一页（离开接入页前校验）。
func (f *welcomeForm) onNext() {
	if f.page == welcomeAccess {
		if err := f.accessErr(); err != nil {
			f.fail(err)
			return
		}
	}
	if f.page < welcomeDone {
		f.page++
	}
	f.status, f.err = "", false
}

// onPrev 上一页。
func (f *welcomeForm) onPrev() {
	if f.page > welcomeAccess {
		f.page--
	}
	f.status, f.err = "", false
}

// onSave 保存：Apply（providers[0] + primary + ui.welcomed）→ 结束宿主。
// 校验不过退回接入页并把原因写在状态行（不静默失败）。
func (f *welcomeForm) onSave() {
	if f.env.mgr == nil {
		f.fail(fmt.Errorf("欢迎窗未接线（数据面缺失）"))
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
		f.fail(fmt.Errorf("欢迎窗未接线（数据面缺失）"))
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
		f.fail(fmt.Errorf("欢迎窗未接线（数据面缺失）"))
		return
	}
	if err := f.accessErr(); err != nil {
		f.fail(err)
		return
	}
	p := f.patch()
	f.modelsHint = "正在获取模型列表…"
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
				f.probe = "连通失败：" + r.err.Error()
			} else {
				f.probeErr = false
				f.probe = "连通正常 · " + strconv.FormatInt(r.ms, 10) + " ms"
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
		f.modelsHint = "获取模型列表失败：" + r.err.Error()
		return
	}
	if len(r.models) == 0 {
		f.cands, f.candClicks = nil, nil
		f.modelsHint = "服务端未返回任何模型"
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
		f.modelsHint = "已取回 " + strconv.Itoa(total) + " 个候选 · 点选替换（未覆盖已填内容）"
		return
	}
	if total > len(fill) {
		f.modelsHint = "已自动填入 " + strconv.Itoa(len(fill)) + " 个候选（共 " +
			strconv.Itoa(total) + " 个）· 点选替换"
	} else {
		f.modelsHint = "已自动填入 " + strconv.Itoa(len(fill)) + " 个候选 · 点选替换"
	}
}
