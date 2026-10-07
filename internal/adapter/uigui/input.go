package uigui

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// inputBar 输入栏（§15.2 骨架）：logo（拖拽把手）｜编辑器（确认态 = 提示行）｜
// 发送/停止；确认态整体切换为 [允许/拒绝] 按钮组（非模态）。
// inputBar 输入行（D49 三段式，§15.2）：[logo ⌀48] 12 [输入胶囊] 12 [send ⌀48]——三段等高
// 独立成形（各自 record → vis/羽化，间隙透明且点击穿透），中间胶囊吃掉全部剩余宽度
// （响应式：窗口宽变化只伸缩它，字号/圆钮尺寸不随窗口变）。坐标原点 = 输入行段左上，
// w = 窗口宽（均经 gtx.Dp 换算为物理 px）。D54：绘制顺序 = 胶囊 → 右钮 → logo（动画
// p=0 时 logo 盖住前两者），几何按展开进度插值。
func (u *UI) inputBar(gtx layout.Context, w, absY int) {
	rowH := gtx.Dp(inputRowDp)
	// D106：展开态胶囊原地增高（瞬时切换；动画期照常 48——D76 几何不动）。
	pillH := rowH
	if extra := u.inputExtraDp(); extra > 0 {
		pillH = rowH + extra
	}
	// 圆钮贴胶囊底缘：展开态下移 extra（常态 extra=0 = D49 原位、D76 不变量不动）。
	logo, pillEnd, sendEnd := inputRowRects(w, gtx.Dp(pillTopDp)+pillH-rowH, rowH,
		gtx.Dp(inputGapDp), gtx.Dp(sideMarginDp))
	// D54/D76 展开/收起几何：send 按 barP 从 logo 插值到终位（p=0 = 收起球、p=1 = D49 终位、
	// 过冲 p>1 越出终位再回落），胶囊按 send 分段导出（缩/长段双间隙恒 12、平移段成圆同步
	// 合球）；send 先收界（D57）、胶囊随夹后 send 导出 → 贴边夹掉后双间隙仍恒 12。
	pill, send := rowRectsFromSend(logo, pillEnd, sendEnd, u.barP(), w)
	if pillH != rowH {
		pill.Min.Y -= pillH - rowH // 胶囊顶缘上移（底缘与圆钮对齐）；圆角恒 24dp
	}
	clipRect := image.Rectangle{Max: u.frameSize}
	inAnim := u.expandAn.active

	// 输入胶囊（**先画**，D54 绘制顺序 = 胶囊 → 右钮 → logo）：内容**按终位整盒排版**
	// （原点 + 终宽都取 pillEnd → 文字图标不挤压、不位移），按当前胶囊矩形裁剪；
	// D77 显隐另走内容 alpha 时间线（动画期隐藏、bar 完成后淡入），裁剪只管几何揭示。
	// α 由 stepExpand 定帧（pillAlpha，两遍 layout 同帧同值）；仅 α<1 压组透明层
	//（胶囊底板不透明 → 位图命中/焦点/手势不变）。展开态（D106）内容盒与原点改取
	// 实际胶囊矩形（多行编辑从顶缘排）。
	contentBox, contentOrigin := pillEnd.Size(), pillEnd.Min
	if pillH != rowH {
		contentBox, contentOrigin = pill.Size(), pill.Min
	}
	paint.FillShape(gtx.Ops, pillBg, clip.UniformRRect(pill, rowH/2).Op(gtx.Ops))
	pst := clip.UniformRRect(pill, rowH/2).Push(gtx.Ops)
	inner := op.Offset(contentOrigin).Push(gtx.Ops)
	gtxC := gtx
	gtxC.Constraints = layout.Exact(contentBox)
	if al := u.pillAlpha; al >= 1 {
		u.pillContent(gtxC)
	} else {
		layer := paint.PushOpacity(gtx.Ops, float32(al))
		u.pillContent(gtxC)
		layer.Pop()
	}
	inner.Pop()
	pst.Pop()
	u.record(pill.Add(image.Pt(0, absY)), rowH/2, pillBg, clipRect)

	// 右圆钮（恒在，几何不随状态变、动画期只平移，D49）：idle = 发送、生成中 = 停止
	// ——确认态不特判（D86：工具确认发生在 Turn 内，停止键照常可点，取消整轮语义
	// 不变、迟到应答缓冲兜底；/rm 等非生成中确认遇 idle 为发送键，submitEditor 拦截）。
	fill, stop, cl := brandColor, false, &u.sendBtn
	if u.generating.Load() {
		fill, stop, cl = textError, true, &u.stopBtn
	}
	off := op.Offset(send.Min).Push(gtx.Ops)
	gtxS := gtx
	gtxS.Constraints = layout.Exact(send.Size())
	actionCircle(gtxS, cl, fill, stop)
	off.Pop()
	u.record(send.Add(image.Pt(0, absY)), rowH/2, fill, clipRect)

	// logo 圆钮（**最后画**，D54）：p=0 时盖住胶囊/右钮 → 像素与 layoutCollapsed 的球
	// 一致（收尾切收起态无缝）；p=1 三段不重叠、顺序无副作用。悬停 tips、拖动移窗、单击互切。
	drawLogo(gtx, logo)
	gst := clip.Rect(logo).Push(gtx.Ops)
	u.logoDrag.Add(gtx.Ops)
	u.logoRight.add(gtx.Ops, logo) // D72：右键热区 = logo 钮矩形
	gst.Pop()
	u.record(logo.Add(image.Pt(0, absY)), rowH/2, brandColor, clipRect)

	// 悬浮 tips（§15.1 启动提示 / §15.2 发送·停止键）：独立底板元素随位图。
	// 显隐 = 事件态 × 光标直采（D53）：分层窗按像素 alpha 命中，光标移到透明像素/
	// 窗外后零 pointer 事件，Hover 收不到 Leave → 实测移开不消；直采离钮即熄，
	// tipShown 并入 heartbeatNeed 唤帧复评（D50 心跳底座复用）。动画期抑制（D54）。
	shown := false
	if !inAnim {
		cur := u.cursorPos()
		// D85：logo 门控 = 矩形直采 × WindowFromPoint 命中直证（事件态不可靠——
		// Enter 在「窗口出现于静止光标下/首次悬停」场景永不投递，仅 Press 会送）。
		if u.cursorHitsLogo(cur) {
			// D82/S1-1g：事实快照就绪则显多行事实卡，未就绪（零值）回退启动提示。
			if lines := u.factsCard(); len(lines) > 0 {
				u.hoverCard(gtx, absY, lines, false)
			} else {
				u.hoverTip(gtx, absY, startupHint, false)
			}
			shown = true
		}
		// D104：附件槽 tooltip（D85 直采；暂存态换 chip、无 tooltip——chip 自示意）。
		if !shown && u.m.confirm == nil && u.m.stagedFile == "" &&
			u.cursorHitsRect(attachSlotRect(pillEnd, gtx.Dp(inputPadDp), rowH,
				gtx.Dp(inputIconDp)).Add(image.Pt(0, absY)), cur) {
			u.hoverTip(gtx, absY, "附件", false)
			shown = true
		}
		if u.m.confirm != nil {
			// D86：确认态三钮 tips（图标无文字，tooltip 承担可发现性）——布局几何
			// 直采 × OS 命中直证（D85 口径；Clickable.Hovered 事件态在本窗不可靠）。
			if txt, ok := u.confirmTipAt(cur); ok {
				u.hoverTip(gtx, absY, txt, true)
				shown = true
			}
		} else if u.sendBtn.Hovered() && !u.generating.Load() &&
			u.overInputBtn(true, cur) {
			u.hoverTip(gtx, absY, "发送", true)
			shown = true
		}
		if u.generating.Load() && u.stopBtn.Hovered() &&
			u.overInputBtn(true, cur) {
			// D86：确认态恢复（右圆停止键不再置灰）。
			u.hoverTip(gtx, absY, "停止", true)
			shown = true
		}
	}
	u.tipShown = shown
	// D103 补全浮层（tips 之后画——盖住转写区下部，胶囊上方锚定）：chrome 形状整窗
	// 登记不受淡化带作用；行矩形随帧重登记（fade pass 同几何复登，bubbleRects 同构）。
	u.drawCompl(gtx, absY, pill)
}

// inputExtraDp 展开态输入带增高量（D106 单源，dp）：胶囊增高超出常排 48 的部分。
// 动画期恒 0（D76 几何不动）。
func (u *UI) inputExtraDp() int {
	if !u.expanded || u.expandAn.active {
		return 0
	}
	return int(inputPillExpandDp - inputRowDp)
}

// inputExtraPx 增高量换算（layout 与 inputBar 同帧同值——经同一 gtx.Metric）。
func (u *UI) inputExtraPx(gtx layout.Context) int {
	return gtx.Dp(unit.Dp(u.inputExtraDp()))
}

// inputRowRects 三段几何（纯逻辑，可测，D49/§15.2）：拓扑 = 边距 | logo | 间隙 | 胶囊 |
// 间隙 | send | 边距——两圆钮贴边距定宽（⌀ = rowH），胶囊吃掉全部剩余宽度（`grow`）。
// 参数均为已换算的 px。
func inputRowRects(w, top, rowH, gap, margin int) (logo, pill, send image.Rectangle) {
	logo = image.Rect(margin, top, margin+rowH, top+rowH)
	send = image.Rect(w-margin-rowH, top, w-margin, top+rowH)
	pill = image.Rect(margin+rowH+gap, top, w-margin-rowH-gap, top+rowH)
	return
}

// startupHint 启动提示（装配根对 GUI 不再发 Say 启动行，§15.1）。
const startupHint = "Aquarius — 输入 /help 查看命令，/quit 退出；Ctrl+C 取消当前生成"

// tipBg 悬浮 tips 底色（实色——位图合成下元素必须自带底板）。
var tipBg = color.NRGBA{R: 0x26, G: 0x2A, B: 0x2E, A: 0xFF}

// hoverTip 悬浮提示卡片：画在胶囊上沿之上（输入栏段局部坐标，可为负 → 溢出到
// 转写区之上，无遮挡裁剪）；自带底板并登记形状。rightAlign=右对齐到胶囊内边距
// （发送键），false=左对齐（logo）。单行 = hoverCard 的单行特例（D82）。
func (u *UI) hoverTip(gtx layout.Context, absY int, text string, rightAlign bool) {
	u.hoverCard(gtx, absY, []string{text}, rightAlign)
}

// hoverCard 多行悬浮提示卡（D82/S1-1g，泛化自单行 tips）：texts 逐行左对齐、宽度取
// 最宽行；画在胶囊上沿之上（输入栏段局部坐标，可为负 → 溢出到转写区之上，无遮挡
// 裁剪）；自带底板并登记形状（形状规则同单行）。rightAlign=右对齐到胶囊内边距
// （发送键），false=左对齐（logo）。
func (u *UI) hoverCard(gtx layout.Context, absY int, texts []string, rightAlign bool) {
	if len(texts) == 0 {
		return
	}
	type line struct {
		op op.CallOp
		w  int
		h  int
	}
	lines := make([]line, 0, len(texts))
	w, h := 0, 0
	for _, text := range texts {
		m := op.Record(gtx.Ops)
		cap := material.Caption(u.th, text)
		cap.Color = whiteText
		dims := cap.Layout(gtx)
		lines = append(lines, line{op: m.Stop(), w: dims.Size.X, h: dims.Size.Y})
		w = max(w, dims.Size.X)
		h += dims.Size.Y
	}
	padX, padY := gtx.Dp(tipsPadXDp), gtx.Dp(tipsPadYDp)
	radius := gtx.Dp(tipsRadiusDp)
	x := gtx.Dp(sideMarginDp)
	if rightAlign {
		x = gtx.Constraints.Max.X - gtx.Dp(sideMarginDp) - w - 2*padX
		if x < 0 {
			x = 0
		}
	}
	y := gtx.Dp(pillTopDp) - h - 2*padY - gtx.Dp(tipsUpGapDp)
	bgRect := image.Rectangle{
		Min: image.Pt(x, y),
		Max: image.Pt(x+w+2*padX, y+h+2*padY),
	}
	st := clip.UniformRRect(bgRect, radius).Push(gtx.Ops)
	paint.Fill(gtx.Ops, tipBg)
	ly := bgRect.Min.Y + padY
	for _, ln := range lines {
		inner := op.Offset(image.Pt(bgRect.Min.X+padX, ly)).Push(gtx.Ops)
		ln.op.Add(gtx.Ops)
		inner.Pop()
		ly += ln.h
	}
	st.Pop()
	u.record(bgRect.Add(image.Pt(0, absY)), radius, tipBg, image.Rectangle{Max: u.frameSize})
}

// updateEditor 消费编辑器事件（Enter → SubmitEvent → 提交）。material.Editor.Layout
// 内部也会 Update 但丢弃返回的 SubmitEvent——必须在渲染前自己循环取尽。
// D92：编辑态额外拉取 Esc = 取消（清目标与编辑框；与 D91 选区 Escape 清除同为广播
// 过滤器，同按时二者皆发生——语义相容）。
func (u *UI) updateEditor(gtx layout.Context) {
	if u.m.confirm != nil {
		u.updateReasonEditor(gtx) // D86：确认态 = 原因编辑器消费按键（主编辑器隐藏）
		return
	}
	if u.m.editTarget != "" {
		for {
			ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
			if !ok {
				break
			}
			if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
				if u.complEsc { // D103：浮层开启期 Esc 先关浮层（complUpdate 已处理），不取消编辑
					u.complEsc = false
					continue
				}
				u.cancelEdit()
				break
			}
		}
	}
	// D106：展开态 Esc = 收起（文本保留压平）；优先级让浮层（complEsc/complOpenNow）
	// 与编辑态（上方分支）——本过滤器 Focus 限编辑器，编辑器无 Esc 语义无冲突面。
	if u.expanded && u.m.editTarget == "" && !u.complOpenNow && !u.complEsc {
		for {
			ev, ok := gtx.Event(key.Filter{Focus: &u.editor, Name: key.NameEscape})
			if !ok {
				break
			}
			if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
				u.setExpanded(false)
				break
			}
		}
	}
	if !u.inFadePass {
		u.caretFocused = gtx.Focused(&u.editor) // 真窗 pass 捕获（fade pass 零 Source 恒 false）
	}
	// 编辑器不再每帧回投常驻焦点（D63）：无条件的 FocusCmd 会与行获焦竞态——
	// Selectable.Focused() 滞后一帧，编辑器会把刚点选的行的焦点抢回（选区隐没、
	// Ctrl+C 失效）。焦点来源收口为：focusPending（呼出/展开，anim.go）、编辑器
	// 自带点击取焦、newUI 初始焦点。
	for {
		evt, ok := u.editor.Update(gtx)
		if !ok {
			break
		}
		if _, isSubmit := evt.(widget.SubmitEvent); isSubmit {
			if u.complOpenNow { // D103：浮层开启期 Enter = 补全判定（一致即提交、否则补全）
				u.complEnter()
			} else {
				u.submitEditor()
			}
		}
	}
}

// drawCaret 淡出源渲染的 caret 自绘（D62）：material.Editor 的 caret 由 gtx.Focused
// 门控（零值 Source 恒 false，见 editor.layout），fade pass 里编辑器不画 caret——按
// CaretCoords（相对编辑器原点、y = 基线）补画一根 2px 实心杆，高度按行高近似
// （CaretInfo 未导出，asc/desc 用 0.8/0.2 行高拆分）。常显不闪：闪烁由真窗 pass 的
// InvalidateCmd 驱动，fade pass 无事件源、帧率随唤帧走，跟闪会冻在半相位。
func (u *UI) drawCaret(gtx layout.Context, dims layout.Dimensions) {
	c := u.editor.CaretCoords()
	rect := caretRect(c, u.caretLineH(gtx))
	rect.Min.X = max(rect.Min.X, 0) // 空文本时 caret 贴原点，杆宽一半越出编辑器盒
	if rect.Empty() {
		return
	}
	cl := clip.Rect(rect).Push(gtx.Ops) // 只裁 caret 杆——paint.Fill 覆盖整个当前裁剪区
	paint.Fill(gtx.Ops, u.th.Palette.Fg)
	cl.Pop()
}

// caretRect 光标杆矩形（D106 修订⑴）：基线 c.Y、按行高 lineH 的 0.8/0.2 拆分——
// 高度只随**行高**、不随内容高（原实现用编辑器内容高，多行下光标正比于行数伸长）。
func caretRect(c f32.Point, lineH int) image.Rectangle {
	asc := int(float64(lineH) * 0.8)
	return image.Rect(int(c.X)-1, int(c.Y)-asc, int(c.X)+1, int(c.Y)-asc+lineH)
}

// caretLineH 编辑器单行行高（D106 修订⑴）：同字号单行量测——多行下内容高 = 整块高
// 不可用。字号与 pillContent 的编辑器设置（15sp）保持一致。
func (u *UI) caretLineH(gtx layout.Context) int {
	_, dims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
		s := material.Body2(u.th, "行")
		s.TextSize = unit.Sp(15)
		return s.Layout(gtx)
	})
	return dims.Size.Y
}

// updateReasonEditor 消费确认态原因编辑器事件（D86）：Enter → SubmitEvent → 拒绝
// （附原因；允许/提升必经按钮）。焦点一次性让入（reasonFocus，主编辑器确认态隐藏
// ——不转移则键入落空），沿 D63 口径不回投常驻焦点。
func (u *UI) updateReasonEditor(gtx layout.Context) {
	if !u.inFadePass {
		u.caretFocused = gtx.Focused(&u.reasonEd) // 真窗 pass 捕获（同主编辑器）
	}
	if u.reasonFocus {
		u.reasonFocus = false
		gtx.Execute(key.FocusCmd{Tag: &u.reasonEd})
	}
	for {
		evt, ok := u.reasonEd.Update(gtx)
		if !ok {
			break
		}
		if _, isSubmit := evt.(widget.SubmitEvent); isSubmit {
			u.m.replyConfirm(port.ConfirmAnswer{Reason: u.confirmReason()})
		}
	}
}

// drawReasonCaret 原因编辑器 fade pass caret 自绘（drawCaret 的 reasonEd 版，D86）。
func (u *UI) drawReasonCaret(gtx layout.Context, dims layout.Dimensions) {
	c := u.reasonEd.CaretCoords()
	asc := int(float64(dims.Size.Y) * 0.8)
	rect := image.Rect(int(c.X)-1, int(c.Y)-asc, int(c.X)+1, int(c.Y)+dims.Size.Y-asc)
	rect.Min.X = max(rect.Min.X, 0)
	if rect.Empty() {
		return
	}
	cl := clip.Rect(rect).Push(gtx.Ops)
	paint.Fill(gtx.Ops, u.th.Palette.Fg)
	cl.Pop()
}

// confirmReason 取原因文本（TrimSpace + 控制序列消毒，同确认问句口径）。
func (u *UI) confirmReason() string {
	return sanitizeControl(strings.TrimSpace(u.reasonEd.Text()))
}

// submitEditor 提交编辑器内容（发送键与 Enter 同路）；确认态无动作——D86：应答经
// 三钮/原因框 Enter，拦截以防主编辑器旧草稿被 submit 误当 y/N 应答。
func (u *UI) submitEditor() {
	if u.m.confirm != nil {
		return
	}
	text := strings.TrimSpace(u.editor.Text())
	if text == "" && u.m.stagedFile == "" {
		if u.m.editTarget != "" { // D92：编辑态空提交 = 取消
			u.cancelEdit()
		}
		return
	}
	u.editor.SetText("")
	u.m.submit(text)
	// D106 修订⑥：发送后自动收起——长文输完即走，胶囊回常态（编辑态与仅附件提交同路；
	// 文本已清、焦点随 setExpanded 保持）。
	if u.expanded {
		u.setExpanded(false)
	}
}

// cancelEdit 退出编辑态（D92）：清目标节点与编辑框（Esc / 空提交共用）；分片序号随收
// （D107② 生命周期与 editTarget 同步）。
func (u *UI) cancelEdit() {
	u.m.editTarget = ""
	u.m.editMode = conversation.Fresh
	u.m.editPart = -1
	u.editor.SetText("")
}

// setExpanded 切换展开态（D106；修订⑶ 收起保留换行）：编辑器 SingleLine 随动、焦点
// 保持；切换不触 buffer——已有 \n 原样保留（单行胶囊内显示溢出裁剪可接受，数据保真
// 优先：提交恒发真实文本，重新展开即完整多行）。
func (u *UI) setExpanded(on bool) {
	if u.expanded == on {
		return
	}
	u.expanded = on
	u.editor.SingleLine = !on
	u.focusPending = true
}
