package uigui

import (
	"image"

	"gioui.org/gesture"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// layout 悬浮窗布局：背景（兜底 + 整窗拖动）| 转写区（手工布局 + 滚动）/ 状态行 /
// 输入栏——自底向上定高，全部绝对坐标登记形状（§15.1/D44/D62）。
// 本函数也被 fadeCompose 以零值 Source 二次调用（纯渲染，无事件消费）；移窗不在这里
// 下发——帧内只记账，由 commitWinGeom 统一 flush（D55）。
func (u *UI) layout(gtx layout.Context) layout.Dimensions {
	if u.focusPending && !u.collapsed {
		u.focusPending = false
		gtx.Execute(key.FocusCmd{Tag: &u.editor}) // 唤出（托盘/快捷键）后焦点进输入栏
	}
	if !u.collapsed {
		u.updateSel(gtx)    // D91 跨块拖选消费者：先于编辑器（Ctrl+C 抢先，焦点纪律同帧生效）
		u.updateCompl(gtx)  // D103 补全浮层消费者：先于编辑器（Enter 接管 SubmitEvent、Esc 让渡）
		u.updateEditor(gtx) // 收起态不消费按键（编辑器不可见，防隐形收字）
	}
	u.updateClicks(gtx)
	u.updateDrag(gtx)
	u.updateLogo(gtx)
	if !u.collapsed {
		u.updateBubbleRight(gtx) // D92：气泡右键消费者（事件仅展开态注册，收起态无）
	}
	// D71 滚轮手势钉点复评：收起/停靠/光标移位 = 手势结束 → 解除钉点、恢复逐像素
	// 穿透。事件静默时无帧可跑：钉点残留原像素至下一帧，任意本窗事件到达即自愈。
	if u.wheelCap && (u.collapsed || u.docked || u.cursorPos() != u.wheelAnchor) {
		u.wheelCap = false
	}
	switch {
	case u.expandAn.active:
		// D54：动画期间不做停靠评估（球位在动）。布防只在**展开方向**清；收起方向
		// 保留 endDrag 抬手时的布防证据（「曾悬停」，§15.1）——收起动画 540ms 内
		// 光标通常已移开，清掉则动画结束首帧重新布防要求光标在球上 → 不悬停就
		// 永不停靠（收回后不自动吸附/停靠的实测缺陷）。
		if u.expandAn.expand {
			u.dockArm = false
		}
	case u.collapsed || u.docked:
		u.evalDockFrame() // D50：收起/停靠态逐帧评估停靠（光标直采，layout 前置状态已更新）
	default:
		u.dockArm = false // 展开态不可停靠：清布防残留（防收起瞬间误触发滑出）
	}
	u.armHeartbeat() // D50：布防心跳（移出球后无指针事件 → 主动唤帧完成移开判定）

	size := gtx.Constraints.Max
	u.frameMetric = gtx.Metric
	u.frameSize = size
	u.shapes = u.shapes[:0]
	// D54：淡出带缺省 = §15.3 静息顶带（收起态无转写区，带只作兜底口径）。
	u.bandTop, u.bandBottom = 0, gtx.Dp(fadeBandDp)
	// D79：底部矮带缺省**关**（收起态无转写区）——区间空即无带；展开路径按转写区底重设。
	u.bandLowTop, u.bandLowEnd = size.Y, size.Y

	// D54：collapsed 是逻辑态、即时翻转；动画中走全量 layout 带几何插值（收尾 barP=0
	// 时几何 == layoutCollapsed 的球，切换无缝）。
	if u.collapsed && !u.expandAn.active {
		u.layoutCollapsed(gtx, size)
		return layout.Dimensions{Size: size}
	}

	// 输入行带（D90 单源）+ 展开态增高（D106：与 inputBar 共用 inputExtra 单源，两遍
	// layout 同帧一致）；statusH 照旧在带之上。
	inputH := gtx.Dp(inputRowBandDp) + u.inputExtraPx(gtx)
	statusH := 0
	if u.statusText() != "" {
		statusH = gtx.Dp(statusChipDp + statusGapDp)
	}
	transH := size.Y - inputH - statusH
	if transH < 0 {
		transH = 0
	}
	// D54 消息揭示带：msgP 驱动带顶从 transH（全隐）升到 0（静息，与 §15.3 顶带重合），
	// 带底夹在 transH 内（不压状态行/输入行）。静态读，两遍 layout 同帧同值。
	_, msgP := u.expandProgress()
	bandPx, lowPx := u.bandHeightsClamped(gtx, transH) // D90：极矮窗带高夹 ≤ transH/4
	u.bandTop, u.bandBottom = revealBand(msgP, transH, bandPx)
	// D79 底部矮带：**常驻转写区底缘** [transH−带高, transH)——底缘内容渐隐不硬切；
	// 带止于 transH（状态行/输入栏在其下，不受淡化）。揭示动画（D54）期间照常驻留。
	u.bandLowTop, u.bandLowEnd = transH-lowPx, transH

	dims := layout.Stack{Alignment: layout.N}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			// 兜底背景（非 Windows 降级形态——Windows 上位图以 alpha 表达间隙，
			// 淡出源渲染跳过底色）+ 拖动把手带 = 状态行 + 输入栏（[transH, 窗底)）。
			// **转写区不注册拖层**（§15.3 只滚不拖窗；D63：gesture.Drag 超出 slop
			// 即 pointer.Grab 且先到先得——拖层若覆盖气泡，会在行选手势前抢走
			// Drag/Release 事件，选区永远无法延伸）。
			if !u.inFadePass {
				paint.Fill(gtx.Ops, windowBg)
			}
			st := clip.Rect{Min: image.Pt(0, transH), Max: size}.Push(gtx.Ops)
			u.drag.Add(gtx.Ops)
			st.Pop()
			return layout.Dimensions{Size: size}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if transH > 0 {
				u.transcript(gtx, size.X, transH)
			}
			if statusH > 0 {
				off := op.Offset(image.Pt(0, transH)).Push(gtx.Ops)
				u.statusChip(gtx, size.X, statusH, transH)
				off.Pop()
			}
			off := op.Offset(image.Pt(0, transH+statusH)).Push(gtx.Ops)
			u.inputBar(gtx, size.X, transH+statusH)
			off.Pop()
			return layout.Dimensions{Size: size}
		}),
	)
	return dims
}

// bandHeightsClamped 当前帧顶/底带高（D90）：极矮窗（transH ≯ 0 或带高 > transH/4）时
// 带高夹 ≤ transH/4——防整片转写区被带吞掉（fade 层 clampedLowBand 预留的尺寸约束
// 归口在此收口）。带写入（layout bandTop/bandBottom/bandLowTop/bandLowEnd）与头部/
// 尾部留白（transcript pad/lowPad）同源取值，两遍 layout 同帧同值。
func (u *UI) bandHeightsClamped(gtx layout.Context, transH int) (top, low int) {
	if transH <= 0 {
		return 0, 0
	}
	top, low = gtx.Dp(fadeBandDp), gtx.Dp(fadeBandBottomDp)
	if maxBand := transH / 4; top > maxBand {
		top = maxBand
	}
	if maxBand := transH / 4; low > maxBand {
		low = maxBand
	}
	return top, low
}

// layoutCollapsed 收起态（§15.1 单组件"左键 logo 收起回球"）：只渲染 logo 悬浮球——
// 球位 = 展开态（无状态行）的 logo 位置（换形不跳动），窗口尺寸不变，球外区域位图
// alpha=0 透明且点击穿透；拖动把手 = 整窗背景层（事件只能落在球像素上），单击球
// （位移小于 dragClickSlackPx）再展开、移动则拖窗。
func (u *UI) layoutCollapsed(gtx layout.Context, size image.Point) {
	if !u.inFadePass {
		paint.Fill(gtx.Ops, windowBg)
	}
	dst := clip.Rect{Max: size}.Push(gtx.Ops)
	u.drag.Add(gtx.Ops)
	// D72：收起态热区 = 球矩形（注册整窗 clip——事件本只落球像素，D62；抬起按球
	// 矩形门控，拖到窗内透明处/窗外不弹）。
	u.logoRight.add(gtx.Ops, ballRect(size, gtx.Dp))
	dst.Pop()

	// 球 = 展开态 logo 圆钮本身（D49 三段式：⌀ = 行高、x = 侧边距、y = 行内 logo 位）
	// → 换形不跳动；窗口尺寸不变，球外区域位图 alpha=0 透明。D50：ballRect 与停靠锚点同源。
	r := ballRect(size, gtx.Dp)
	drawLogo(gtx, r) // §15.2 logo 实装（品牌色圆钮 + 内嵌图标）
	u.record(r, r.Dx()/2, brandColor, image.Rectangle{Max: u.frameSize})
}

// updateScroll 滚动手势 + 当帧边界钳制 + 尾随（§15.3 流式内容贴底）。
// d>0 = 向下滚（往新内容）；d<0 = 上滚离开底部 → 停止尾随；滚回底部 → 恢复。
// 边界由本函数钳制（含 fling 溢出）——不交给 ScrollRange（见下方滞后说明）。
func (u *UI) updateScroll(gtx layout.Context, viewH, total int) {
	overflow := total - viewH
	if overflow < 0 {
		overflow = 0
	}
	// 滚动范围按**轴向**绑定（gesture.Scroll.Update 的 scrollx/scrolly 两参）：垂直手势
	// 累加 e.Scroll.Y，夹取范围必须给 scrolly——Gio 自家 layout/list.go 即此法
	//（`Axis==Vertical` 时把 bounds 换到 Y）。绑到 scrollx 会被 Y 侧 {0,0} 夹成 0，
	// 滚轮永不生效（实测缺陷：手工滚动从未生效）。
	// 范围只按 overflow 取、**不随 scrollPx 走**：过滤器在事件到来时取的是上一帧登记的
	// 边界，随位置走会滞后一帧——边界处反向的首格会被旧边界误夹成 0 而丢失；反正边界
	// 由下面的钳制负责，过滤器只需够宽（|d| ≤ overflow 一格不可能超过）。
	d := u.transcriptScroll.Update(gtx.Metric, gtx.Source, gtx.Now, gesture.Vertical,
		pointer.ScrollRange{}, // 横向不滚（X 夹到 0）
		pointer.ScrollRange{Min: -overflow, Max: overflow})
	if d != 0 {
		// D71 滚轮手势钉点：真有增量 = 手势进行中；锚点随当前光标刷新（本帧顶部
		// 复评若已因移位解除，此处即在新位置重挂）。钉点由 fadePresent 落笔。
		u.wheelCap = true
		u.wheelAnchor = u.cursorPos()
	}
	u.scrollPx += d
	if u.scrollPx < 0 {
		u.scrollPx = 0
	}
	if u.scrollPx > overflow {
		u.scrollPx = overflow
	}
	if d < 0 {
		u.followTail = false
	} else if d > 0 && u.scrollPx >= overflow {
		u.followTail = true
	}
	if u.followTail {
		u.scrollPx = overflow // 尾随：新内容永远贴底
	}
}
