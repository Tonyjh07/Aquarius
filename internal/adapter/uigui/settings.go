package uigui

// 设置窗（§15.7/D60 核心档 + §15.4/D61 主题档）：表单状态、保存流与帧渲染。
// 并发口径（§15.7）：表单状态仅次窗事件循环 goroutine 读写、不跨窗共享；持久化经
// ApplySettings 单一写回调（装配根实现），内核命令经 inCh 排队到主循环与键入同
// 路径串行执行（壳内不旁路），主题/快捷键热生效经消息与原子槽（同跨窗模型）。

import (
	"image"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// SettingsSnapshot 设置窗核心档快照（开窗现取；同 Status 口径线程安全回调）。
// Permission/Model/Think/Effort 由装配根取运行态（等级活槽与 Agent 访问器），
// 其余取 config 文件；密钥永不进快照（D35 只写不回显）。
type SettingsSnapshot struct {
	Permission string // 规范值 read-only|strict|permissive|full-access（perm.Level）
	Provider   string
	BaseURL    string
	Model      string
	Think      bool
	Effort     string // "" = 未设（D34 清除态）
	Hotkey     string // "" = 默认 Alt+A
	Theme      string // "" = system
}

// SettingsPatch 保存提交（表单全量；APIKey 仅在用户填写时非空 = 只写不回显）。
type SettingsPatch struct {
	Permission string
	Provider   string
	BaseURL    string
	Model      string
	APIKey     string // "" = 不改
	Think      bool
	Effort     string
	Hotkey     string
	Theme      string
}

// 枚举档（展示值 + 键值；内核 /permission /effort 的校验为单一事实源，此处仅渲染循环）。
var (
	// permLevels 权限档（§5/D22，与 domain/perm.Level 对齐）。
	permLevels = []string{"read-only", "strict", "permissive", "full-access"}
	// effortLevels 推理档位（D34）：首项 = 未设（键值 "" = 清除态）。
	effortLevels = []string{"未设", "minimal", "low", "medium", "high"}
	effortValues = []string{"", "minimal", "low", "medium", "high"}
	// themeLevels 主题档展示与键值（§15.4/D61：system|light|dark）。
	themeLevels = []string{"跟随系统", "浅色", "深色"}
	themeValues = []string{"system", "light", "dark"}
)

// cycleField 点击循环枚举档（material v0.10 无下拉框）：按钮显示当前项，点击进一档。
type cycleField struct {
	click    widget.Clickable
	displays []string
	values   []string
	idx      int
}

// setValue 按键值定位；未命中保持当前（快照异常值不致越界）。
func (c *cycleField) setValue(v string) {
	for i, x := range c.values {
		if x == v {
			c.idx = i
			return
		}
	}
}

// value 当前键值（空表返回 ""）。
func (c *cycleField) value() string {
	if len(c.values) == 0 {
		return ""
	}
	return c.values[c.idx]
}

// advance 消费点击进一档（帧渲染前调用——Clickable.Clicked 自带 Update）。
func (c *cycleField) advance(gtx layout.Context) {
	if len(c.values) == 0 {
		return
	}
	for c.click.Clicked(gtx) {
		c.idx = (c.idx + 1) % len(c.values)
	}
}

// settingsRows 表单行数（与 settingsForm 行渲染的 switch 对齐——改行数两处同步）。
const settingsRows = 13

// fieldH 文本档统一高度（dp）：单行编辑器 + 上下留白（material 无内建输入框边框）。
const fieldH = 40

// fieldLabelW 标签列宽（dp）：对齐各行控件起点。
const fieldLabelW = 92

// settingsForm 设置窗表单状态（次窗 goroutine 独占；控件即状态，不跨窗共享）。
type settingsForm struct {
	list     widget.List
	provider widget.Editor
	model    widget.Editor
	baseURL  widget.Editor
	apiKey   widget.Editor
	hotkey   widget.Editor
	think    widget.Bool
	perm     cycleField
	effort   cycleField
	theme    cycleField
	save     widget.Clickable

	snap     SettingsSnapshot // 开窗快照（保存 diff 基准 + 变更提示）
	status   string           // 保存反馈（本 goroutine 独占；"" = 默认提示）
	err      bool             // 反馈为错误态（红字）
	readOnly bool             // 无写回调：保存仅报只读占位
	focused  bool             // 首帧焦点入首档（其余点击自聚焦，editor.go FocusCmd）
}

// newSettingsForm 开窗现取快照填表：读回调缺失 = 空表单，写回调缺失 = 只读占位。
func newSettingsForm(u *UI) *settingsForm {
	f := &settingsForm{
		perm:   cycleField{displays: permLevels, values: permLevels},
		effort: cycleField{displays: effortLevels, values: effortValues},
		theme:  cycleField{displays: themeLevels, values: themeValues},
	}
	for _, ed := range []*widget.Editor{&f.provider, &f.model, &f.baseURL, &f.apiKey, &f.hotkey} {
		ed.SingleLine = true // 表单单行档（Enter 无提交语义，保存走按钮）
	}
	f.readOnly = u.opts.ApplySettings == nil
	if u.opts.Settings == nil {
		return f
	}
	s := u.opts.Settings()
	if s.Permission == "" {
		s.Permission = "strict" // 快照未就绪（Agent 晚于 UI 构造）对齐默认档 perm.DefaultLevel，防误发切换
	}
	f.snap = s
	f.provider.SetText(s.Provider)
	f.model.SetText(s.Model)
	f.baseURL.SetText(s.BaseURL)
	// api_key 只写不回显（D35）：字段恒空，留空 = 保持不变。
	f.hotkey.SetText(s.Hotkey)
	f.think.Value = s.Think
	f.perm.setValue(s.Permission)
	f.effort.setValue(s.Effort)
	f.theme.setValue(s.Theme) // "" 未命中 → 首项 = 跟随系统
	return f
}

// saveSettings 保存流（渲染外的可 headless 测段）：组装 patch → 写回调（装配根持久化
// 文本键并返回内核命令计划）→ 命令经 inCh 排队（与键入同路径串行执行、回执进转写区；
// 队列 256 深、主循环持续排空，阻塞投递不致死锁）→ 主题/快捷键 GUI 侧热生效 →
// 反馈文案写回表单。
func (u *UI) saveSettings(f *settingsForm) {
	if f.readOnly || u.opts.ApplySettings == nil {
		f.err = true
		f.status = "设置窗未接线（只读占位）"
		return
	}
	p := SettingsPatch{
		Permission: f.perm.value(),
		Provider:   strings.TrimSpace(f.provider.Text()),
		BaseURL:    strings.TrimSpace(f.baseURL.Text()),
		Model:      strings.TrimSpace(f.model.Text()),
		APIKey:     strings.TrimSpace(f.apiKey.Text()),
		Think:      f.think.Value,
		Effort:     f.effort.value(),
		Hotkey:     strings.TrimSpace(f.hotkey.Text()),
		Theme:      f.theme.value(),
	}
	if p.Model == "" {
		p.Model = f.snap.Model // 空模型名 = 保持当前
	}
	if p.Hotkey != "" {
		if _, err := parseHotkey(p.Hotkey); err != nil {
			f.err = true
			f.status = "快捷键非法：" + err.Error()
			return
		}
	}
	cmds, err := u.opts.ApplySettings(p)
	if err != nil {
		f.err = true
		f.status = "保存失败：" + err.Error()
		return
	}
	for i := range cmds {
		c := cmds[i]
		u.inCh <- port.UserInput{Command: &c}
	}
	if p.Theme != f.snap.Theme {
		_ = u.post(themeMsg{mode: p.Theme}) // 主窗主题热生效（事件循环串行，§15.4）
	}
	if p.Hotkey != f.snap.Hotkey {
		u.setHotkey(p.Hotkey) // 原子槽 + 托盘线程重注册（失败回退 Ctrl+Alt+A，§15.1）
	}
	f.err = false
	f.status = "已保存"
	switch {
	case p.APIKey != "":
		f.status = "已保存（密钥已写入 config，重启生效）"
	case p.Provider != f.snap.Provider || p.BaseURL != f.snap.BaseURL:
		f.status = "已保存（模型服务地址改动重启生效）"
	}
	f.snap = SettingsSnapshot{
		Permission: p.Permission, Provider: p.Provider, BaseURL: p.BaseURL,
		Model: p.Model, Think: p.Think, Effort: p.Effort, Hotkey: p.Hotkey, Theme: p.Theme,
	}
}

// setHotkey 更新快捷键配置原子槽并请求托盘线程重注册（设置窗保存；注册归属托盘
// 线程——RegisterHotKey 归调用线程，跨线程只能投消息，§15.1）。headless/非窗口
// 平台 = 槽更新即止。
func (u *UI) setHotkey(hk string) {
	u.hotkeyCfg.Store(hk)
	reRegisterHotkey()
}

// settingsFrame 设置窗单帧：滚动表单（§15.7/D60 核心档数据面）。事件先于渲染取尽
// （Clicked 自带 Update；点击触发重绘，新值随后续帧呈现）。常规窗不做形状位图/羽化
// （§15.7 形态）：windowBg 铺底，与主窗内容面同底。
func settingsFrame(gtx layout.Context, th *material.Theme, u *UI, f *settingsForm) layout.Dimensions {
	for f.save.Clicked(gtx) {
		u.saveSettings(f)
	}
	f.perm.advance(gtx)
	f.effort.advance(gtx)
	f.theme.advance(gtx)
	if !f.focused {
		f.focused = true
		gtx.Execute(key.FocusCmd{Tag: &f.provider}) // 开窗焦点入首档；点击切换（editor.go 自聚焦）
	}
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, windowBg,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return material.List(th, &f.list).Layout(gtx, settingsRows, f.row(th))
		}),
	)
}

// row 第 i 行渲染（material.List 回调；行序与 settingsRows 对齐）。
func (f *settingsForm) row(th *material.Theme) func(layout.Context, int) layout.Dimensions {
	return func(gtx layout.Context, i int) layout.Dimensions {
		return layout.UniformInset(unit.Dp(14)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			switch i {
			case 0:
				return material.H6(th, "设置").Layout(gtx)
			case 1:
				return fieldRow(gtx, th, "权限等级", cycleBtn(th, &f.perm))
			case 2:
				return material.CheckBox(th, &f.think, "原生思考（/think 总开关，关时忽略推理档位）").Layout(gtx)
			case 3:
				return fieldRow(gtx, th, "推理档位", cycleBtn(th, &f.effort))
			case 4:
				return material.Subtitle1(th, "模型服务").Layout(gtx)
			case 5:
				return fieldRow(gtx, th, "提供方", editorBox(th, &f.provider, "如 openai-compatible"))
			case 6:
				return fieldRow(gtx, th, "模型名", editorBox(th, &f.model, "如 gpt-4o-mini（/model 同口径热切）"))
			case 7:
				return fieldRow(gtx, th, "接口地址", editorBox(th, &f.baseURL, "如 https://api.openai.com/v1"))
			case 8:
				return fieldRow(gtx, th, "API 密钥", editorBox(th, &f.apiKey, "只写不回显；留空保持不变（D35）"))
			case 9:
				return material.Subtitle1(th, "外观与呼出").Layout(gtx)
			case 10:
				return fieldRow(gtx, th, "全局快捷键", editorBox(th, &f.hotkey, "如 Alt+A（空 = 默认）"))
			case 11:
				return fieldRow(gtx, th, "主题", cycleBtn(th, &f.theme))
			default:
				return f.actionsRow(gtx, th)
			}
		})
	}
}

// fieldRow 一行：定宽标签列 + 控件（基线对齐，各行控件起点一致）。
func fieldRow(gtx layout.Context, th *material.Theme, label string, control layout.Widget) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = fieldLabelW
			gtx.Constraints.Max.X = fieldLabelW
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Body1(th, label).Layout(gtx)
			})
		}),
		layout.Flexed(1, control),
	)
}

// cycleBtn 循环档按钮（当前展示值；点击进一档——material v0.10 无下拉框）。
func cycleBtn(th *material.Theme, c *cycleField) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		label := ""
		if len(c.displays) > 0 {
			label = c.displays[c.idx%len(c.displays)]
		}
		b := material.Button(th, &c.click, label)
		b.TextSize = unit.Sp(13)
		return b.Layout(gtx)
	}
}

// editorBox 文本档输入区（layout.Widget，供 fieldRow 装配）：定高满宽底衬
// （material v0.10 编辑器无内建边框——底衬即可编辑标记）。列表行纵向约束无界，
// 尺寸取行约束宽 + 固定高，不按 Max 铺底。
func editorBox(th *material.Theme, ed *widget.Editor, hint string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		size := image.Point{X: gtx.Constraints.Max.X, Y: fieldH}
		paint.FillShape(gtx.Ops, th.Bg, clip.Rect(image.Rectangle{Max: size}).Op())
		gtx.Constraints = layout.Exact(size)
		layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return material.Editor(th, ed, hint).Layout(gtx)
		})
		return layout.Dimensions{Size: size}
	}
}

// actionsRow 保存键 + 反馈文案（成功绿/错误红 = 转写区语义色；空 = 默认提示）。
func (f *settingsForm) actionsRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline, Spacing: layout.SpaceStart}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			b := material.Button(th, &f.save, "保存并应用")
			b.Background = brandColor
			b.Color = whiteText
			b.TextSize = unit.Sp(14)
			return b.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				text := f.status
				col := textSystem
				switch {
				case text == "":
					text, col = "保存后立即生效；模型服务地址与密钥改动需重启。", textDim
				case f.err:
					col = textError
				}
				l := material.Body2(th, text)
				l.Color = col
				return l.Layout(gtx)
			})
		}),
	)
}
