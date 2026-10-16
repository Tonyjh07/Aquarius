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
	"strings"
	"sync"

	"gioui.org/app"
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
	prev, next, save, skip        widget.Clickable
	// 反馈（本 goroutine 独占；"" = 默认提示）。
	status string
	err    bool
	// 连通性测试异步结果（测试 goroutine 写、帧循环排空）。
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
	f.tutList.Axis = layout.Vertical // 构造不变量（S4c 教训：material.List 未设轴向 = Horizontal）
	f.name.SetText(env.snap.Provider)
	f.baseURL.SetText(env.snap.BaseURL)
	f.models.SetText(env.snap.Models)
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
		return fmt.Errorf("请填写 API 密钥：可填 secret:<环境变量名> 引用，或直接填明文（启动会警告，D35）")
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
	f.status, f.err = "正在测试连通性…", false
	go func() {
		err := f.env.mgr.Test(p)
		select {
		case f.testResults <- testOutcome{name: p.Provider, err: err}:
		default: // 槽满丢最旧（测试结果是最简反馈，不积压）
			select {
			case <-f.testResults:
			default:
			}
			f.testResults <- testOutcome{name: p.Provider, err: err}
		}
		if f.env.inval != nil {
			f.env.inval()
		}
	}()
}

// drain 帧循环排空测试结果（最新一条覆盖展示）。
func (f *welcomeForm) drain() {
	for {
		select {
		case r := <-f.testResults:
			if r.err != nil {
				f.err = true
				f.status = "连通测试 " + r.name + " 失败：" + r.err.Error()
			} else {
				f.err = false
				f.status = "连通测试 " + r.name + " 通过"
			}
		default:
			return
		}
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

// welcomeFrame 欢迎窗单帧（常规窗形态：windowBg 铺底，无形状位图/羽化，§15.7）。
// 事件先于渲染取尽（Clicked 自带 Update；点击触发重绘）。
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
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, windowBg,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.UniformInset(unit.Dp(18)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(welcomeHeader(th, f)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, welcomeIssues(th, f))
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return f.pageBody(gtx, th)
						})
					}),
					layout.Rigid(welcomeStatusLine(th, f)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, welcomeFooter(th, f))
					}),
				)
			})
		}),
	)
}

// welcomeHeader 标题 + 步骤条。
func welcomeHeader(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.Subtitle1(th, "欢迎使用 Aquarius").Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx,
						welcomeStep(th, "① 接入模型", f.page == welcomeAccess),
						welcomeStep(th, "② 教程", f.page == welcomeTutorial),
						welcomeStep(th, "③ 完成", f.page == welcomeDone),
					)
				})
			}),
		)
	}
}

// welcomeStep 单步指示（当前步用品牌色）。
func welcomeStep(th *material.Theme, label string, active bool) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, label)
			if active {
				l.Color = brandColor
			} else {
				l.Color = textDim
			}
			return l.Layout(gtx)
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
		l.Color = textError
		return l.Layout(gtx)
	}
}

// welcomeStatusLine 状态行（反馈 / 默认提示）。
func welcomeStatusLine(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		text, col := f.status, textSystem
		if text == "" {
			col = textDim
			if f.page == welcomeDone {
				text = "保存后写回 config；provider / 密钥类改动需重启进程生效。"
				if f.env.setup {
					text = "保存后立刻继续启动。"
				}
			} else {
				text = "密钥只写不回显：可填 secret:<环境变量名> 引用，或直接填明文（D35）。"
			}
		} else if f.err {
			col = textError
		}
		l := material.Body2(th, text)
		l.Color = col
		return l.Layout(gtx)
	}
}

// welcomeFooter 底部按钮：左 = 跳过/关闭，右 = 上一步 + 下一步/保存。
func welcomeFooter(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		skipLabel := "跳过"
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
		children := []layout.FlexChild{
			layout.Rigid(welcomeBtn(th, &f.skip, skipLabel, false)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
		}
		if f.page > welcomeAccess {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, welcomeBtn(th, &f.prev, "上一步", false))
			}))
		}
		if f.page == welcomeDone {
			children = append(children, layout.Rigid(welcomeBtn(th, &f.save, primary, true)))
		} else {
			children = append(children, layout.Rigid(welcomeBtn(th, &f.next, primary, true)))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	}
}

// welcomeBtn 按钮（primary = 品牌底白字）。
func welcomeBtn(th *material.Theme, c *widget.Clickable, label string, primary bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := material.Button(th, c, label)
		b.TextSize = unit.Sp(13)
		if primary {
			b.Background = brandColor
			b.Color = whiteText
		}
		return b.Layout(gtx)
	}
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

// accessPage 接入模型页：四问 + 连通性测试。
func (f *welcomeForm) accessPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(welcomePara(th, "填写 OpenAI 兼容端点的接入参数。保存后写入 model.providers（覆盖首条）并设为主 provider。")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "Provider", editorBox(th, &f.name, "provider 名，如 openai"))
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "接口地址", editorBox(th, &f.baseURL, "如 https://api.openai.com/v1"))
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "API 密钥", editorBox(th, &f.apiKey, "secret:环境变量名 或明文；只写不回显"))
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "模型名", editorBox(th, &f.models, "逗号可多条；首个 = 缺省请求模型"))
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, welcomeBtn(th, &f.testBtn, "测试连通", false))
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
						l := material.Body2(th, r.left)
						l.Color = textSystem
						return l.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, r.right)
						l.Color = textDim
						return l.Layout(gtx)
					}),
				)
			default:
				l := material.Body2(th, r.left)
				l.Color = textDim
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
