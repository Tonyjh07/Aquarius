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

import (
	"fmt"
	"image"
	"image/color"
	"strconv"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
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
		return fmt.Errorf("请填写 API 密钥：可填 secret:<环境变量名> 引用环境变量，或直接填明文（启动时会警告）")
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

// TutorialTips 教程页「基本操作」段（只读；文案契约测试锁定）。呼出键单独动态生成
// （见 tutorialRows——用户可能改过 `ui.hotkey`）。两个菜单行以「、」逐项列出真实项集，
// 有对照测试（菜单改了教程不改即测试失败）。
func TutorialTips() []string {
	return []string{
		"发送消息：Enter；换行：Shift+Enter",
		"取消当前生成：Ctrl+C；复制选中文本：Ctrl+C",
		"Esc：关补全浮层 / 取消编辑 / 收起展开态",
		"消息右键：编辑、编辑并转移历史、编辑并复制历史、重新生成、复制、引用、查看原文",
		"球右键：新对话、权限、消息历史、设置、置顶、隐藏、退出",
		"托盘右键：显示 / 隐藏输入窗、窗口置顶、设置、会话历史、退出",
		"完整命令清单见 /help；接入参数与主题、快捷键都在「设置」窗（本引导可从设置窗重开）。",
	}
}

// TutorialCommands 教程页「常用命令」段（只读；每项 = {用例, 一句话说明}）。命令名以
// `internal/app` 的 commandCatalog 为单一事实源——但**装配前宿主没有内核**（不能从
// port.CommandCatalog 取），故此处静态复刻；`cmd/aquarius` 有对照测试防止与真实清单脱节。
func TutorialCommands() [][2]string {
	return [][2]string{
		{"/new [标题]", "新建会话"},
		{"/list", "列出会话"},
		{"/switch <id前缀>", "切换到既有会话"},
		{"/model [name]", "查看可用模型 / 切换并写回 config"},
		{"/permission [等级]", "查看 / 切换权限等级"},
		{"/edit <id> [--keep|--copy] <文本>", "修订某条消息（开新分支）"},
		{"/memory [会话id]", "用系统编辑器打开记忆文件"},
		{"/usage", "查看 token 用量"},
		{"/jobs", "后台任务管理"},
		{"/compact", "触发上下文压缩"},
	}
}

const (
	tutHead = iota // 分组标题
	tutTip         // 提示行
	tutCmd         // 命令行（左 = 用例，右 = 说明）
)

// tutorialRow 教程页一行。
type tutorialRow struct {
	kind        int
	left, right string
}

// tutorialRows 教程页行计划（每帧现算；hotkey = 生效的呼出快捷键——用户可能改过
// `ui.hotkey`，空 = 默认 Alt+A，§15.1）。
func tutorialRows(hotkey string) []tutorialRow {
	rows := []tutorialRow{{kind: tutHead, left: "基本操作"}}
	rows = append(rows, tutorialRow{kind: tutTip, left: "呼出 / 收起输入栏：" + hotkey + "（可在设置窗改 ui.hotkey）"})
	for _, t := range TutorialTips() {
		rows = append(rows, tutorialRow{kind: tutTip, left: t})
	}
	rows = append(rows, tutorialRow{kind: tutHead, left: "常用命令"})
	for _, c := range TutorialCommands() {
		rows = append(rows, tutorialRow{kind: tutCmd, left: c[0], right: c[1]})
	}
	return rows
}

// hotkeyLabel 教程页展示的呼出快捷键（快照空 = 默认 Alt+A）。
func (f *welcomeForm) hotkeyLabel() string {
	if h := strings.TrimSpace(f.env.snap.Hotkey); h != "" {
		return h
	}
	return "Alt+A"
}

// welcomeFrame 欢迎窗单帧（常规窗形态：窗体面铺底，无形状位图/羽化，§15.7）。
// 事件先于渲染取尽（Clicked 自带 Update；点击触发重绘）。
//
// 版式（D153，模拟稿）：白面圆角面板 + 灰底 → 页头（标题 + 副标题 + 编号圆徽步骤条）
// → 正文（接入页 = 一张四行分组卡 + 卡外的「测试连通」与内联结果）→ 页脚（hairline + 三钮）。
func welcomeFrame(gtx layout.Context, th *material.Theme, f *welcomeForm) layout.Dimensions {
	f.drain()
	for f.next.Clicked(gtx) {
		f.onNext()
	}
	for f.prev.Clicked(gtx) {
		f.onPrev()
	}
	for f.save.Clicked(gtx) {
		f.onSave()
	}
	for f.skip.Clicked(gtx) {
		f.onSkip()
	}
	for f.testBtn.Clicked(gtx) {
		f.onTest()
	}
	for f.modelsBtn.Clicked(gtx) {
		f.onFetchModels()
	}
	for i := range f.candClicks {
		if !f.candClicks[i].Clicked(gtx) {
			continue
		}
		if i < len(f.cands) {
			f.models.SetText(f.cands[i]) // 点 chip = 替换成单选（但算「用户已改」）
			f.modelsOrig = f.models.Text()
		}
	}
	pp := paperOf()
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			// 灰底 + 白面圆角面板：面板本身是「纸」，窗体底只露一圈边（D146 面阶）。
			size := gtx.Constraints.Max
			paint.FillShape(gtx.Ops, pp.sunken, clip.Rect(image.Rectangle{Max: size}).Op())
			return layout.Dimensions{Size: size}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(14)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Background{}.Layout(gtx,
					func(gtx layout.Context) layout.Dimensions {
						r := image.Rectangle{Max: gtx.Constraints.Min}
						paperCard(gtx, pp, r, gtx.Dp(unit.Dp(pp.rWin)), pp.card)
						return layout.Dimensions{Size: r.Max}
					},
					func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return layout.Inset{
											Top: unit.Dp(22), Bottom: unit.Dp(16),
											Left: unit.Dp(22), Right: unit.Dp(22),
										}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
											return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
												layout.Rigid(welcomeHeader(th, f)),
												layout.Rigid(func(gtx layout.Context) layout.Dimensions {
													return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, welcomeIssues(th, f))
												}),
											)
										})
									}),
									// 页头与正文之间一条**贯通面板**的 hairline（模拟稿位置）。
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return welcomeHairline(gtx, 0)
									}),
								)
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{
									Left: unit.Dp(22), Right: unit.Dp(22),
									Top: unit.Dp(16), Bottom: unit.Dp(12),
								}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return f.pageBody(gtx, th)
								})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(22), Right: unit.Dp(22)}.Layout(gtx, welcomeStatusLine(th, f))
							}),
							layout.Rigid(welcomeFooter(th, f)),
						)
					},
				)
			})
		}),
	)
}

// welcomeHeader 标题 + 副标题 + 步骤条（编号圆徽）。
func welcomeHeader(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return paperTitle(th, "欢迎使用 Aquarius")(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperNoteSp(th), "三步完成接入；密钥只写不回显。")
				l.Color = paperOf().text2
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, l.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						welcomeStep(th, 1, "接入模型", f.page == welcomeAccess),
						welcomeStep(th, 2, "教程", f.page == welcomeTutorial),
						welcomeStep(th, 3, "完成", f.page == welcomeDone),
					)
				})
			}),
		)
	}
}

// welcomeStep 单步指示：编号圆徽（当前步实心 accent + 白字；其余描边空心）+ 步骤名。
func welcomeStep(th *material.Theme, no int, label string, active bool) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		d := gtx.Dp(unit.Dp(22))
		return layout.Inset{Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min, gtx.Constraints.Max = image.Pt(d, d), image.Pt(d, d)
					return layout.Stack{Alignment: layout.Center}.Layout(gtx,
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							circle := clip.Ellipse{Max: image.Pt(d, d)}
							if active {
								paint.FillShape(gtx.Ops, pp.accent, circle.Op(gtx.Ops))
							} else {
								paint.FillShape(gtx.Ops, pp.hairline, circle.Op(gtx.Ops))
								b := gtx.Dp(1)
								paint.FillShape(gtx.Ops, pp.card,
									clip.Ellipse{Min: image.Pt(b, b), Max: image.Pt(d-b, d-b)}.Op(gtx.Ops))
							}
							return layout.Dimensions{Size: image.Pt(d, d)}
						}),
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							l := material.Label(th, paperMicroSp(th), strconv.Itoa(no))
							l.Color = pp.text3
							if active {
								l.Color = pp.accentFg
							}
							return l.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperNoteSp(th), label)
					l.Color = pp.text3
					if active {
						l.Color = pp.text
						l.Font.Weight = font.Bold
					}
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, l.Layout)
				}),
			)
		})
	})
}

// welcomeIssues 问题清单提示（装配前宿主填；空 = 不占位）。
func welcomeIssues(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if len(f.env.snap.Issues) == 0 {
			return layout.Dimensions{}
		}
		l := material.Body2(th, "配置尚未完成："+strings.Join(f.env.snap.Issues, "；"))
		l.Color = paperOf().danger
		return l.Layout(gtx)
	}
}

// welcomeStatusLine 状态行（反馈 / 默认提示）。页脚之上、hairline 之下；空文本时不占高。
func welcomeStatusLine(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		text, col := f.status, paperOf().text2
		if text == "" {
			col = paperOf().text3
			// 接入页不放默认提示：模拟稿在卡下留白，密钥说明已经在输入框占位文字里；
			// 页脚之上只在真有反馈（校验/保存/应用失败）时出现一行。
			if f.page == welcomeDone {
				text = "保存后写回配置；provider / 密钥类改动需重启进程生效。"
				if f.env.setup {
					text = "保存后立刻继续启动。"
				}
			}
		} else if f.err {
			col = paperOf().danger
		}
		if text == "" {
			return layout.Dimensions{} // 无反馈就不占高（页脚贴到正文内衬上）
		}
		l := material.Label(th, paperNoteSp(th), text)
		l.Color = col
		return l.Layout(gtx)
	}
}

// welcomeFooter 页脚：hairline 分隔 + 左「跳过此项」/ 右「上一步」+「下一步」。
// 与正文同底色（Paper 里不出现第二块大底色），只用一条 hairline 分层。
func welcomeFooter(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		skipLabel := "跳过此项"
		if !f.env.setup {
			skipLabel = "关闭"
		}
		primary := "下一步"
		if f.page == welcomeDone {
			primary = "保存"
			if f.env.setup {
				primary = "保存并继续"
			}
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				h := gtx.Dp(1)
				w := gtx.Constraints.Max.X
				paint.FillShape(gtx.Ops, pp.hairline, clip.Rect(image.Rect(0, 0, w, h)).Op())
				return layout.Dimensions{Size: image.Pt(w, h)}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(14), Left: unit.Dp(22), Right: unit.Dp(22)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					children := []layout.FlexChild{
						layout.Rigid(welcomeGhostBtn(th, &f.skip, skipLabel)),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: gtx.Constraints.Min}
						}),
					}
					if f.page > welcomeAccess {
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, welcomeBtn(th, &f.prev, "上一步", false))
						}))
					} else {
						// 首屏也占位（禁用态）：位置固定，翻页时按钮不跳。
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, paperButton(th, &f.prev, "上一步", paperBtnSecondary, false))
						}))
					}
					if f.page == welcomeDone {
						children = append(children, layout.Rigid(welcomeBtn(th, &f.save, primary, true)))
					} else {
						children = append(children, layout.Rigid(welcomeBtn(th, &f.next, primary, true)))
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
				})
			}),
		)
	}
}

// welcomeGhostBtn 无底按钮（页脚「跳过此项」）：透明底 + 次文字色，不打主操作配色。
func welcomeGhostBtn(th *material.Theme, c *widget.Clickable, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		b := material.Button(th, c, label)
		b.TextSize = paperNoteSp(th)
		b.CornerRadius = unit.Dp(pp.rCtl)
		b.Inset = layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(12), Right: unit.Dp(12)}
		b.Background, b.Color = color.NRGBA{}, pp.text2
		return b.Layout(gtx)
	}
}

// welcomeBtn 按钮（primary = 强调底 + 白字；次要 = 下沉面）。
func welcomeBtn(th *material.Theme, c *widget.Clickable, label string, primary bool) layout.Widget {
	kind := paperBtnSecondary
	if primary {
		// 强调色分两档：品牌色 #00AEEF 承载白字只有 2.53:1，按钮必须用深一档。
		kind = paperBtnPrimary
	}
	return paperButton(th, c, label, kind, true)
}

// pageBody 当前页正文。
func (f *welcomeForm) pageBody(gtx layout.Context, th *material.Theme) layout.Dimensions {
	switch f.page {
	case welcomeAccess:
		return f.accessPage(gtx, th)
	case welcomeTutorial:
		return f.tutorialPage(gtx, th)
	default:
		return f.donePage(gtx, th)
	}
}

// accessPage 接入模型页（D153，模拟稿）：一张四行分组卡 + 卡外「测试连通」与内联结果。
func (f *welcomeForm) accessPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "Provider", editorBox(th, &f.name, "provider 名，如 openai"))
		},
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "接口地址", editorBox(th, &f.baseURL, "如 https://api.openai.com/v1"))
		},
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "API 密钥", editorBox(th, &f.apiKey, "secret:环境变量名 或明文；只写不回显"))
		},
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "模型名", func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, editorBox(th, &f.models, "逗号可多条；首个 = 缺省请求模型")),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx,
							paperButton(th, &f.modelsBtn, "获取模型", paperBtnSecondary, true))
					}),
				)
			})
		},
	}
	// 候选挂在**卡内**模型行下方（模拟稿位置）：它是这一行的补充说明，不是卡外的另一
	// 个区块——放到卡外会被一条 card 边界与「模型名」割开。
	if f.modelsHint != "" || len(f.cands) > 0 {
		last := rows[len(rows)-1]
		rows[len(rows)-1] = func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(last),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return welcomeCands(gtx, th, f)
					})
				}),
			)
		}
	}
	kids := []layout.FlexChild{
		layout.Rigid(welcomePara(th, "填写 OpenAI 兼容端点的接入参数；保存后立即用于对话。")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return welcomeCard(gtx, th, rows)
			})
		}),
	}
	kids = append(kids,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(14), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(welcomeBtn(th, &f.testBtn, "测试连通", false)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return welcomeProbe(gtx, th, f)
						})
					}),
				)
			})
		}),
	)
	// 内容比窗高时（候选 chip 会多占两三行）**必须可滚**：不可滚的话内容会压到页脚、
	// 甚至把「测试连通」整行盖掉（离屏实测：chip 行直接叠在按钮上）。这里用裸
	// layout.List 而不是 material.List——后者会常驻画一条滚动条，静态表单里像故障。
	return (&f.accessList).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
	})
}

// welcomeCard 分组卡：一张圆角卡包住全部行，行间 hairline 分隔（左内缩 14dp）。
// 行内容用 fieldRow（标签列 + 控件铺满），行内上下 14dp。
func welcomeCard(gtx layout.Context, th *material.Theme, rows []layout.Widget) layout.Dimensions {
	pp := paperOf()
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			r := image.Rectangle{Max: gtx.Constraints.Min}
			if m := gtx.Constraints.Max.X; m > r.Max.X {
				r.Max.X = m
			}
			paperCard(gtx, pp, r, gtx.Dp(unit.Dp(pp.rCard)), pp.card)
			return layout.Dimensions{Size: r.Max}
		},
		func(gtx layout.Context) layout.Dimensions {
			kids := make([]layout.FlexChild, 0, len(rows)*2)
			for i, row := range rows {
				i, row := i, row
				if i > 0 {
					kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						h := gtx.Dp(1)
						w := gtx.Constraints.Max.X
						paint.FillShape(gtx.Ops, pp.hairline,
							clip.Rect(image.Rect(gtx.Dp(14), 0, w-gtx.Dp(14), h)).Op())
						return layout.Dimensions{Size: image.Pt(w, h)}
					}))
				}
				kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top: unit.Dp(10), Bottom: unit.Dp(10), Left: unit.Dp(14), Right: unit.Dp(14),
					}.Layout(gtx, row)
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
		},
	)
}

// welcomeCands 候选行：说明文字与 chip 同排流式折行（模拟稿「已自动填入 2 个候选 ·
// 点选替换   gpt-5.2  gpt-5.2-mini」）。排布规则见 paperChipFlow。
func welcomeCands(gtx layout.Context, th *material.Theme, f *welcomeForm) layout.Dimensions {
	hint := f.modelsHint
	if hint == "" {
		hint = "点选替换模型名"
	}
	return paperChipFlow(gtx, th, hint, f.cands, f.candClicks)
}

// welcomeHairline 一条贯通卡面宽度的 hairline。pad = 当前左右内衬，线向两侧各伸出 pad，
// 于是分隔线能画到面板边缘（模拟稿的页头/页脚分隔线都是整幅贯通）。
func welcomeHairline(gtx layout.Context, pad unit.Dp) layout.Dimensions {
	h := gtx.Dp(1)
	w := gtx.Constraints.Max.X
	p := gtx.Dp(pad)
	paint.FillShape(gtx.Ops, paperOf().hairline, clip.Rect(image.Rect(-p, 0, w+p, h)).Op())
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// welcomeProbe 「测试连通」右侧的内联结果：绿点 + 成功时延 / 红字失败原因。
func welcomeProbe(gtx layout.Context, th *material.Theme, f *welcomeForm) layout.Dimensions {
	pp := paperOf()
	if f.probeBusy {
		l := material.Label(th, paperNoteSp(th), "正在测试…")
		l.Color = pp.text3
		return l.Layout(gtx)
	}
	if f.probe == "" {
		return layout.Dimensions{}
	}
	col := pp.ok
	if f.probeErr {
		col = pp.danger
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			d := gtx.Dp(unit.Dp(7))
			gtx.Constraints.Min, gtx.Constraints.Max = image.Pt(d, d), image.Pt(d, d)
			paint.FillShape(gtx.Ops, col, clip.Ellipse{Max: image.Pt(d, d)}.Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(d, d)}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperNoteSp(th), f.probe)
			l.Color = pp.text2
			return layout.Inset{Left: unit.Dp(7)}.Layout(gtx, l.Layout)
		}),
	)
}

// tutorialPage 教程页（滚动列表：操作提示 + 常用命令）。
func (f *welcomeForm) tutorialPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	rows := tutorialRows(f.hotkeyLabel())
	return material.List(th, &f.tutList).Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		r := rows[i]
		return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			switch r.kind {
			case tutHead:
				l := material.Subtitle1(th, r.left)
				l.Color = th.Fg
				return l.Layout(gtx)
			case tutCmd:
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						w := gtx.Dp(unit.Dp(190))
						gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
						l := material.Label(th, paperNoteSp(th), r.left)
						l.Color = paperOf().text
						return l.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, paperNoteSp(th), r.right)
						l.Color = paperOf().text2
						return l.Layout(gtx)
					}),
				)
			default:
				l := material.Label(th, paperNoteSp(th), r.left)
				l.Color = paperOf().text3
				return l.Layout(gtx)
			}
		})
	})
}

// donePage 完成页：写盘摘要（密钥只报形态、不报值）。
func (f *welcomeForm) donePage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	p := f.patch()
	keyForm := f.keyForm(p.APIKey)
	row := func(label, value string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, label, func(gtx layout.Context) layout.Dimensions {
					l := material.Body1(th, value)
					l.Color = th.Fg
					return l.Layout(gtx)
				})
			})
		})
	}
	model := p.Models
	if i := strings.Index(model, ","); i > 0 {
		model = model[:i] + " 等"
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(welcomePara(th, "将要写入 config 的接入参数（确认无误后保存）：")),
		row("Provider", p.Provider),
		row("接口地址", p.BaseURL),
		row("模型", model),
		row("API 密钥", keyForm),
	)
}

// keyForm 密钥形态描述（**不回显值**，D35）。
func (f *welcomeForm) keyForm(apiKey string) string {
	switch {
	case strings.HasPrefix(apiKey, "secret:"):
		return "环境变量引用 secret:" + strings.TrimPrefix(apiKey, "secret:") + "（密钥不落盘）"
	case apiKey != "":
		return "明文（写入 config，启动会警告）"
	default:
		return "保持原值（未改）"
	}
}

// welcomePara 段落文本。
func welcomePara(th *material.Theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := material.Body1(th, text)
		l.Color = th.Fg
		return l.Layout(gtx)
	}
}

// newUISideWelcome in-app 宿主表单（主窗宿主的 winWelcome 次窗）：完成/显式关闭 = 关窗
// （closeWin 由宿主给——次窗句柄的关闭路径），ProviderMgr 语义为「重启生效」。
func newUISideWelcome(mgr WelcomeManager, closeWin func()) *welcomeForm {
	env := welcomeEnv{mgr: mgr, done: func(SetupOutcome) { closeWin() }}
	if mgr != nil {
		env.snap = mgr.Snapshot()
	}
	return newWelcomeForm(env)
}

// RunSetupWindow 装配前的独立向导窗（阻塞至完成/关闭）。**宿主可注入**：cmd/aquarius 经
// 包级 var 持有它，测试换假实现（首跑测试不得真开窗）。
// 窗口形态与次窗一致（Decorated 常规窗；platform.New 惰性——不会提前装托盘/热键）。
func RunSetupWindow(req SetupRequest) (SetupOutcome, error) {
	if req.Mgr == nil {
		return SetupCancelled, fmt.Errorf("向导窗：数据面未接线")
	}
	plat := newPlatform(Options{Theme: req.Theme}, true)
	p := resolvePalette(req.Theme, plat.DarkMode())
	applyPalette(p) // 装配前：主窗 goroutine 尚未启动，写活动槽无并发（同 newUI 口径）
	applyPaper(resolvePaper(req.Theme, p.isDark()))
	th := newTheme(plat.SystemFontCandidates())
	th.Palette.Fg, th.Palette.Bg = p.fg, p.bg

	snap := req.Mgr.Snapshot()
	snap.Issues = req.Issues
	snap.Welcomed = false // 装配前宿主：本次一定是要引导（不是「已看、想再看」）

	w := new(app.Window)
	outcome := SetupCancelled
	var once sync.Once
	env := welcomeEnv{mgr: req.Mgr, setup: true, snap: snap}
	env.done = func(out SetupOutcome) {
		once.Do(func() {
			outcome = out
			w.Perform(system.ActionClose) // 关窗 → DestroyEvent → 事件循环退出
		})
	}
	f := newWelcomeForm(env)
	f.env.inval = w.Invalidate

	width, height, minW, minH := winWelcome.geometry()
	w.Option(
		app.Title(winWelcome.title()),
		app.Size(unit.Dp(width), unit.Dp(height)),
		app.MinSize(unit.Dp(minW), unit.Dp(minH)),
		app.Decorated(true), // 常规装饰窗（不接主窗形裁/ULW/置顶等机制，§15.7）
	)
	var ops op.Ops
	for {
		ev := w.Event()
		switch e := ev.(type) {
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			welcomeFrame(gtx, th, f)
			e.Frame(&ops)
		case app.DestroyEvent:
			return outcome, e.Err
		}
	}
}
