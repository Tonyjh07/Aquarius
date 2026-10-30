package uigui

// 遮罩的绘制：帧骨架与卡片框、页签与胶囊、正文、页脚三档按钮与配色。

import (
	"image"
	"image/color"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// overlayFrame 遮罩单帧（D139）：scrim 铺满视口 + 居中卡片。无遮罩时零尺寸空操作。
func (st *historyState) overlayFrame(gtx layout.Context, th *material.Theme, u *UI) layout.Dimensions {
	ov := st.ov
	if ov == nil {
		return layout.Dimensions{}
	}
	// 整窗命中区 + 路由锚点：抬起/悬停事件落到本 tag（点空白关闭的判定源）。
	// **这一处就是隔离的主机制**：Gio 的命中按链式栈遍历，排在链顶的整窗输入区会连带
	// 遮蔽背景（左栏 / 右图 / 状态栏实测都收不到指针事件）。背景侧的显式不取是纵深防御。
	stack := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	defer stack.Pop()
	gtx.Event(key.FocusFilter{Target: &ov.tag}) // 呈现页的键盘落点可获焦
	event.Op(gtx.Ops, &ov.tag)
	ov.events(gtx, st)
	if st.ov == nil { // 事件里关了：本帧不再画
		return layout.Dimensions{}
	}
	ov.actions(gtx, st, u)
	if st.ov == nil {
		return layout.Dimensions{}
	}
	card := overlayCardBoxFor(gtx, ov.persona)
	ov.card = card
	// scrim：铺满视口的半透暗色（背景仍在其下渲染 → 观感是「压暗一层」）。
	paint.FillShape(gtx.Ops, color.NRGBA{A: overlayScrimAlpha},
		clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
	// 遮罩卡片是浮层：阴影必须先于卡片铺（自小而大），否则会被卡片自身盖掉。
	cardRad := gtx.Dp(unit.Dp(paperOf().rCard))
	paperShadow(gtx, card, cardRad, 14, 8, 0x38)
	// 卡片：**显式定尺定位**到卡片盒。不能走 `layout.Center`——它只在 `Constraints.Min`
	// 内居中，而 `layout.Stacked` 会把子项的 Min 清成 0（stack.go），于是「居中」退化成
	// 画在窗口左上角：底板与内容贴左上，`ov.card`（命中盒/点空白判定）却仍在正中，二者
	// 错位（2026-10-15 实测：卡片自报尺寸 = 卡片盒，但 center 偏移 = 0）。
	// Min 也一并定成卡片盒：卡内内容按盒宽排布（长消息在盒内换行，不撑窗）。
	gtx.Constraints.Min, gtx.Constraints.Max = card.Size(), card.Size()
	trans := op.Offset(card.Min).Push(gtx.Ops)
	dims := overlayCard(gtx, th, ov, st, u)
	trans.Pop()
	dims.Size = card.Size() // 上报卡片盒（本层是 Stacked 子项，尺寸只影响自身记录）
	return dims
}

// overlayCardBox 卡片盒（居中、限宽限高：长消息在卡内滚，不撑窗）。
func overlayCardBox(gtx layout.Context) image.Rectangle {
	return overlayCardBoxFor(gtx, false)
}

// overlayCardBoxFor 按节点类型取卡片盒（D154）：人格模式上限放宽到 640dp / 视口 85%，
// 其余节点沿用 560dp / 70%。
func overlayCardBoxFor(gtx layout.Context, persona bool) image.Rectangle {
	margin := gtx.Dp(overlayCardMargindp)
	avail := gtx.Constraints.Max
	maxW := avail.X - 2*margin
	limW, limHf := overlayCardMaxWdp, float64(overlayCardMaxHFac)
	if persona {
		limW, limHf = overlayPersonaMaxWdp, overlayPersonaMaxHFac
	}
	if w := gtx.Dp(limW); maxW > w {
		maxW = w
	}
	if maxW < gtx.Dp(overlayCardMinWdp) {
		maxW = avail.X // 视口本身太窄：铺满不留边
	}
	maxH := int(float64(avail.Y) * limHf)
	if lim := avail.Y - 2*margin; maxH > lim {
		maxH = lim
	}
	if maxH < 1 {
		maxH = avail.Y
	}
	card := image.Rect(0, 0, maxW, maxH)
	return card.Add(image.Pt((avail.X-card.Dx())/2, (avail.Y-card.Dy())/2))
}

// overlayCard 卡片内容：标题行 + 页签行 + 内容区（呈现/源码）+ 底行（方式三选一 + 关闭/保存）。
func overlayCard(gtx layout.Context, th *material.Theme, ov *overlayState, st *historyState, u *UI) layout.Dimensions {
	pp := paperOf()
	paperPanel(gtx, image.Rectangle{Max: gtx.Constraints.Max},
		gtx.Dp(unit.Dp(pp.rCard)), gtx.Dp(1), pp.card, pp.hairline)
	return layout.UniformInset(overlayCardPadDp).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return overlayTitle(gtx, th, ov)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return overlayTabs(gtx, th, ov)
			}),
		}
		// 注入节点（D166）：页签下方一行来源 + 触发状态——两个页签都看得到「这条从哪来」。
		if ov.role == conversation.RoleInject {
			children = append(children,
				layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return overlayInjectSource(gtx, th, ov)
				}))
		}
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return overlayBody(gtx, th, ov, st, u)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return overlayFooter(gtx, th, ov)
			}),
		)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// overlayInjectSource 注入节点的来源行（D166）：「来源：<友好化来源> · 已触发响应/未触发」。
// 与主窗转写头部共用 port.InjectSourceLabel，措辞恒一致。
func overlayInjectSource(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	l := material.Caption(th, "来源："+port.InjectSourceLabel(ov.source)+" · "+injectState(ov.trigger))
	l.Color = paperOf().text2
	return l.Layout(gtx)
}

// overlayTitle 标题行：角色词 +「消息」+ 节点 ID 前缀（ID 与「原文 · <id>」同款口径）。
func overlayTitle(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	l := material.Label(th, paperBodySp(th), ov.titleText())
	l.Color = paperOf().text
	return l.Layout(gtx)
}

// titleText 标题文案（2026-10-15 用户拍板的口径）：角色词取**快照 Role**（不是 blockKind——
// root 与 system 都落 blockPlain，会把根节点写成「系统」）；根节点是会话容器、不是一条消息，
// 故只写「会话根 · <id>」，与图例「◯会话根」同词。Role 未知（快照未就绪）= 系统消息。
func (ov *overlayState) titleText() string {
	if ov.persona {
		return "人格节点 · " + shortID(string(ov.id))
	}
	switch ov.role {
	case conversation.RoleUser:
		return "用户消息 · " + shortID(string(ov.id))
	case conversation.RoleAssistant:
		return "助手消息 · " + shortID(string(ov.id))
	case conversation.RoleRoot:
		return "会话根 · " + shortID(string(ov.id))
	case conversation.RoleInject: // D166：注入上下文（只读；来源行在页签下方）
		return "注入上下文 · " + shortID(string(ov.id))
	default:
		return "系统消息 · " + shortID(string(ov.id))
	}
}

// overlayTabs 页签行：呈现 / 源码（人格节点多一档「默认模板」，D154）。
func overlayTabs(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	children := []layout.FlexChild{
		layout.Rigid(overlayPillBtn(&ov.tabRendered, th, "呈现", ov.page == overlayPageRendered)),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(overlayPillBtn(&ov.tabSource, th, "源码", ov.page == overlayPageSource)),
	}
	if ov.persona {
		children = append(children,
			layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
			layout.Rigid(overlayPillBtn(&ov.tabTemplate, th, "默认模板", ov.page == overlayPageTemplate)))
	}
	return layout.Flex{}.Layout(gtx, children...)
}

// overlayPill 卡内胶囊/按钮的**底板**（先量后画的正确姿势）：内容先量尺寸，底板按**内容盒**
// 画在内容之下。
//
// **不能**在 Inset 内用 `gtx.Constraints.Max` 当底板宽度（2026-10-15 用户截图报障）：
// 内衬先推了偏移，底板于是**从内衬之后起笔**、再被 `widget.Clickable` 的剪切
// （`clip.Rect{Max: dims.Size}`，见 gio widget/button.go）在右侧切平 ⇒ 看上去「文字贴着
// 左边、右边多出一块、右角是直角」。实测（1.25 缩放，未选中页签）：左内衬 2px / 右 14px，
// 修好后两侧相等。
func overlayPill(gtx layout.Context, bg color.NRGBA, radius unit.Dp, content layout.Widget) layout.Dimensions {
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			sz := gtx.Constraints.Min // Background 已把 Min 设成内容尺寸
			paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(radius)).Op(gtx.Ops))
			return layout.Dimensions{Size: sz}
		},
		content,
	)
}

// overlayPillColors 胶囊配色：选中 = 深一档强调底 + 白字（AA 达标）；未选中 = 下沉面 + 次级字。
func overlayPillColors(th *material.Theme, on bool) (bg, fg color.NRGBA) {
	pp := paperOf()
	if on {
		return pp.accent, pp.accentFg
	}
	return pp.sunken, pp.text2
}

// overlayPillBtn 卡内胶囊钮（页签行与底行「应用方式三选一」共用一套控件语言：
// 选中反色、未选中次要底；2026-10-15 用户拍板统一）。
func overlayPillBtn(c *widget.Clickable, th *material.Theme, text string, on bool) layout.Widget {
	bg, fg := overlayPillColors(th, on)
	return func(gtx layout.Context) layout.Dimensions {
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return overlayPill(gtx, bg, overlayPillRadiusDp, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(10), Right: unit.Dp(10)}.
					Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Caption(th, text)
						l.Color = fg
						return l.Layout(gtx)
					})
			})
		})
	}
}

// overlayBody 内容区：呈现页签 = 主窗同款 markdown 渲染只读预览；源码页签 = 多行编辑器；
// 模板页签 = 人格编辑区（与设置窗「系统提示词」页同一份实现，D154）。
func overlayBody(gtx layout.Context, th *material.Theme, ov *overlayState, st *historyState, u *UI) layout.Dimensions {
	if ov.page == overlayPageTemplate {
		return overlayTemplateBody(gtx, th, ov)
	}
	if ov.page == overlayPageSource {
		if ov.focusNeeded {
			ov.focusNeeded = false
			gtx.Execute(key.FocusCmd{Tag: &ov.ed})
		}
		for {
			evt, ok := ov.ed.Update(gtx)
			if !ok {
				break
			}
			if _, isSubmit := evt.(widget.SubmitEvent); isSubmit && ov.canSave() {
				ov.submit(u, st)
				return layout.Dimensions{}
			}
		}
		return ov.srcList.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			return material.Editor(th, &ov.ed, "").Layout(gtx)
		})
	}
	if ov.focusSink {
		ov.focusSink = false
		gtx.Execute(key.FocusCmd{Tag: &ov.tag})
	}
	blocks := ov.shown()
	return ov.shownList.Layout(gtx, len(blocks), func(gtx layout.Context, i int) layout.Dimensions {
		w := mdBlockWidgetTh(th, blocks[i], nil, 0, image.Point{})
		return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, w)
	})
}

// overlayFooter 底行：应用方式三选一（只读节点无）+ 关闭 / 保存。方式钮 = 与页签同款胶囊。
// 方式钮上方一行说明**当前这一档的实际后果**：三档各不相同（旧分支留不留、后续挂到谁
// 下面），说明随选中的方式即时切换，用户不必凭「转移/复制」两个词去猜。
// 人格节点（D154）：方式锁 Carry（另两档禁用 + 一行说明），模板页签换成两枚模板钮。
func overlayFooter(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	row := func(gtx layout.Context) layout.Dimensions { return overlayFooterRow(gtx, th, ov) }
	hint := ov.modeHint()
	if hint == "" {
		return row(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(th, hint)
			l.Color = paperOf().text3
			l.MaxLines = 2
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, l.Layout)
		}),
		layout.Rigid(row),
	)
}

// modeHint 当前应用方式的后果说明（增删改遮罩的「会发生什么」）：只读节点没有方式可选、
// 模板页签谈的是人格模板而非这一段消息，都返回空串。
func (ov *overlayState) modeHint() string {
	switch {
	case ov.persona && ov.page == overlayPageTemplate:
		return ""
	case ov.persona:
		return "人格固定「转移历史」：旧人格留成无子叶，现有对话整体转到新人格下。"
	case !ov.editable:
		return ""
	case !ov.hasChildren:
		// 没有后续内容时三档等价：都是造一个同级新版本、旧节点留在原分支。
		return "该节点之后还没有内容：三种方式结果相同，都是留下旧节点、把视图移到新版本。"
	}
	switch ov.mode {
	case conversation.Carry:
		return "转移历史：旧节点留成无子叶，后续内容改挂到新版本下。"
	case conversation.Clone:
		return "复制历史：旧分支原样保留，新版本下另挂一份后续副本。"
	default:
		if ov.role == conversation.RoleUser {
			return "另起新分支：旧分支原样保留，在新版本下重新生成回答。"
		}
		return "另起新分支：旧节点与其后续留在原分支，视图移到新版本。"
	}
}

// overlayFooterRow 底行按钮本身（说明行由 overlayFooter 叠在它上面）。
func overlayFooterRow(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	children := []layout.FlexChild{}
	switch {
	case ov.persona && ov.page == overlayPageTemplate:
		children = append(children,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return overlayTplStatus(gtx, th, ov)
			}),
			layout.Rigid(overlayBtn(&ov.btnSaveTpl, th, "保存模板", false, ov.prompt != nil)),
			layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
			layout.Rigid(overlayBtn(&ov.btnApply, th, "应用到本会话", true, ov.prompt != nil)),
		)
	case ov.persona:
		// 改人格必须保留后续历史：三档都画出来（不是隐藏），另两档禁用——用户要
		// 知道「另外两种应用方式存在，但这里不适用」，而不是以为功能被砍了。
		modes := []struct {
			c    *widget.Clickable
			text string
			m    conversation.KeepMode
		}{
			{&ov.modeFresh, "另起新分支", conversation.Fresh},
			{&ov.modeCarry, "转移历史", conversation.Carry},
			{&ov.modeClone, "复制历史", conversation.Clone},
		}
		for i, md := range modes {
			if i > 0 {
				children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout))
			}
			children = append(children, layout.Rigid(overlayPillBtnMode(md.c, th, md.text, ov.mode == md.m, ov.mode == md.m)))
		}
		children = append(children,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Spacer{}.Layout(gtx)
			}),
			layout.Rigid(overlayBtn(&ov.btnSave, th, "保存", true, ov.canSave())),
		)
	case ov.editable:
		modes := []struct {
			c    *widget.Clickable
			text string
			m    conversation.KeepMode
		}{
			{&ov.modeFresh, "另起新分支", conversation.Fresh},
			{&ov.modeCarry, "转移历史", conversation.Carry},
			{&ov.modeClone, "复制历史", conversation.Clone},
		}
		for i, md := range modes {
			if i > 0 {
				children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout))
			}
			children = append(children, layout.Rigid(overlayPillBtn(md.c, th, md.text, ov.mode == md.m)))
		}
		children = append(children,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Spacer{}.Layout(gtx)
			}),
			layout.Rigid(overlayBtn(&ov.btnSave, th, "保存", true, ov.canSave())),
		)
	default:
		children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(th, "该节点只读")
			l.Color = paperOf().text2
			return l.Layout(gtx)
		}))
	}
	children = append(children,
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout), // 与页签行同款间距：两钮贴在一起会读成一块
		layout.Rigid(overlayBtn(&ov.btnClose, th, "关闭", false, true)))
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// overlayPillBtnMode 方式胶囊的可禁用变体（人格节点锁 Carry，D154）：选中档仍用强调底，
// 禁用档用下沉面 + 三级字（点击不响应——调用方只在非 persona 分支消费 Clicked）。
func overlayPillBtnMode(c *widget.Clickable, th *material.Theme, text string, on, enabled bool) layout.Widget {
	if enabled {
		return overlayPillBtn(c, th, text, on)
	}
	pp := paperOf()
	return func(gtx layout.Context) layout.Dimensions {
		return overlayPill(gtx, pp.sunken, overlayPillRadiusDp, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(10), Right: unit.Dp(10)}.
				Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(th, text)
					l.Color = pp.text3
					return l.Layout(gtx)
				})
		})
	}
}

// overlayPillRadiusDp 卡内胶囊圆角。
const overlayPillRadiusDp unit.Dp = 6

// overlayBtnColors 底行动作钮配色：primary = 反色强调；disabled = 次要底 + 暗字
// （D139 ⑤「空文本禁用保存」的可见形态）。
func overlayBtnColors(th *material.Theme, primary, enabled bool) (bg, fg color.NRGBA) {
	pp := paperOf()
	switch {
	case !enabled:
		return pp.sunken, pp.text3
	case primary:
		return pp.accent, pp.accentFg
	default:
		return pp.sunken, pp.text
	}
}

// overlayBtn 底行动作钮（primary = 反色强调；enabled=false 呈禁用态，点击亦为 no-op）。
func overlayBtn(c *widget.Clickable, th *material.Theme, text string, primary, enabled bool) layout.Widget {
	bg, fg := overlayBtnColors(th, primary, enabled)
	return func(gtx layout.Context) layout.Dimensions {
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return overlayPill(gtx, bg, overlayPillRadiusDp, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(12), Right: unit.Dp(12)}.
					Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Caption(th, text)
						l.Color = fg
						return l.Layout(gtx)
					})
			})
		})
	}
}
