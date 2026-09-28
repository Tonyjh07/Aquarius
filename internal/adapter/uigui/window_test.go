package uigui

import (
	"fmt"
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// newShapeUI 形裁登记用的最小 UI（frameMetric 供淡出带剔除换算；bandBottom = 带底，
// 生产里由 layout 每帧写入——D54 动画前恒为 §15.3 静息顶带底 Dp(fadeBandDp)）。
func newShapeUI(pxPerDp float32) *UI {
	m := unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp}
	return &UI{frameMetric: m, bandBottom: m.Dp(fadeBandDp)}
}

// TestRecordStoresOutlineAndClip 形裁登记（D44/D45）：存真轮廓（未按视口/带裁剪——
// 羽化环沿此取边）+ 可见裁剪区；带内剔除、裁剪后为空、零圆角不登记。
func TestRecordStoresOutlineAndClip(t *testing.T) {
	u := newShapeUI(1) // Dp(56) = 56px 淡出带
	viewport := image.Rect(0, 0, 100, 300)

	// ① 完全在淡出带内 → 剔除（overlay 带渐变接管）。
	u.record(image.Rect(10, 0, 90, 50), 12, brandColor, viewport)
	if len(u.shapes) != 0 {
		t.Fatalf("带内矩形不应登记: %+v", u.shapes)
	}
	// ② 跨带 → 存**真轮廓**（不裁到带）+ 裁剪区 + 底色（区外由环自行裁剪）。
	u.record(image.Rect(10, 20, 90, 120), 12, brandColor, viewport)
	if len(u.shapes) != 1 || u.shapes[0].outline != image.Rect(10, 20, 90, 120) ||
		u.shapes[0].clip != viewport || u.shapes[0].radius != 12 || u.shapes[0].fill != brandColor {
		t.Fatalf("跨带矩形应存真轮廓 + 裁剪区 + 底色: %+v", u.shapes)
	}
	// ③ 带下、视口内 → 原样登记。
	u.record(image.Rect(8, 150, 92, 200), 12, brandColor, viewport)
	if len(u.shapes) != 2 || u.shapes[1].outline != image.Rect(8, 150, 92, 200) {
		t.Fatalf("视口内矩形应原样登记: %+v", u.shapes)
	}
	// ④ 视口外（滚动到上方）→ 裁空剔除。
	u.record(image.Rect(8, -60, 92, -10), 12, brandColor, viewport)
	if len(u.shapes) != 2 {
		t.Fatalf("视口外不应登记: %+v", u.shapes)
	}
	// ⑤ 零圆角（如被裁成细条的异常态）→ 剔除。
	u.record(image.Rect(8, 150, 92, 200), 0, brandColor, viewport)
	if len(u.shapes) != 2 {
		t.Fatalf("零圆角不应登记: %+v", u.shapes)
	}
}

// TestRegionShapesInsetAndCuts 形裁推导（D45–D48/§15.1）：真实边内缩（响应式 featherWidth，
// 按**真轮廓**短边——跨带元素裁剪后短边会变，按裁剪尺寸算会与渐隐带错位）且圆角同步收窄
// （同心内缩圆角）；带/视口裁切边不内缩（内缩会露出渐隐不覆盖的洞）；带顶裁切标 sqTop。
func TestRegionShapesInsetAndCuts(t *testing.T) {
	m := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	viewport := image.Rect(0, 0, 100, 300)
	// ① 带下、完全在视口内：四边都是真边 → 全内缩（半径 12-ins）。
	ins := featherWidth(m, 80, 60)
	got := regionShapes(nil, []drawShape{
		{outline: image.Rect(10, 100, 90, 160), clip: viewport, radius: 12},
	}, 56, m)
	want := shapePhys{x: int32(10 + ins), y: int32(100 + ins), w: int32(80 - 2*ins), h: int32(60 - 2*ins), ellipse: int32((12 - ins) * 2)}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("带下矩形 = %+v, want %+v（ins=%d）", got, want, ins)
	}
	// ② 顶边被带裁切：顶边不缩（= 带底），其余内缩，标 sqTop；内缩量仍按**真轮廓** 80×140。
	ins2 := featherWidth(m, 80, 140)
	got = regionShapes(nil, []drawShape{
		{outline: image.Rect(10, 20, 90, 160), clip: viewport, radius: 12},
	}, 56, m)
	want = shapePhys{x: int32(10 + ins2), y: 56, w: int32(80 - 2*ins2), h: int32(160 - ins2 - 56), ellipse: int32((12 - ins2) * 2), sqTop: true}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("跨带矩形 = %+v, want %+v（ins=%d）", got, want, ins2)
	}
	// ③ 底边被视口裁切：底边不缩，其余内缩。
	ins3 := featherWidth(m, 80, 200)
	got = regionShapes(nil, []drawShape{
		{outline: image.Rect(10, 100, 90, 320), clip: viewport, radius: 12},
	}, 56, m)
	want = shapePhys{x: int32(10 + ins3), y: int32(100 + ins3), w: int32(80 - 2*ins3), h: int32(300 - (100 + ins3)), ellipse: int32((12 - ins3) * 2)}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("视口底裁矩形 = %+v, want %+v（ins=%d）", got, want, ins3)
	}
	// ④ 极扁元素：内缩夹到短边 1/3，region 不退化（元素不被整块吃掉）。
	ins4 := featherWidth(m, 80, 6)
	got = regionShapes(nil, []drawShape{
		{outline: image.Rect(10, 100, 90, 106), clip: viewport, radius: 2},
	}, 56, m)
	if len(got) != 1 || int(got[0].h) != 6-2*ins4 || int(got[0].y) != 100+ins4 {
		t.Fatalf("极扁矩形应夹到短边 1/3、region 合法: %+v（ins=%d）", got, ins4)
	}
	// ⑤ 视口裁空 → 剔除。
	got = regionShapes(nil, []drawShape{
		{outline: image.Rect(10, 400, 90, 460), clip: viewport, radius: 12},
	}, 56, m)
	if len(got) != 0 {
		t.Fatalf("裁剪为空不应输出: %+v", got)
	}
}

// TestFeatherWidthResponsive 渐隐带宽响应式（D47/D48）：受 [Dp(featherMinDp), Dp(featherMaxDp)]
// 与短边 1/3（防 region 退化）约束，且**随 DPI 同步放大**——随元素尺寸/窗口缩放/DPI 自动
// 跟随，不写死 px。断言写成"由常量推导的界"，改羽化宽参数时测试不需重写。
func TestFeatherWidthResponsive(t *testing.T) {
	one := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	two := unit.Metric{PxPerDp: 2, PxPerSp: 2}
	for _, c := range [][2]int{{200, 40}, {400, 80}, {6, 6}, {90, 3}, {90, 6}, {2000, 2000}} {
		w, h := c[0], c[1]
		short := w
		if h < short {
			short = h
		}
		f1 := featherWidth(one, w, h)
		if f1 > one.Dp(featherMaxDp) || f1 > short/3 {
			t.Fatalf("1× featherWidth(%d,%d)=%d 越界（上限 Dp(%d)=%d、短边 1/3=%d）",
				w, h, f1, featherMaxDp, one.Dp(featherMaxDp), short/3)
		}
		if lim := min(one.Dp(featherMinDp), short/3); f1 < lim {
			t.Fatalf("1× featherWidth(%d,%d)=%d < 下界 %d", w, h, f1, lim)
		}
		// 同一逻辑元素在 2× DPI（物理尺寸翻倍）→ 带宽不小于 1×（非写死 px）。
		f2 := featherWidth(two, 2*w, 2*h)
		if f2 < f1 {
			t.Fatalf("2× featherWidth(%d,%d)=%d 应 ≥ 1× 的 %d", 2*w, 2*h, f2, f1)
		}
		if f2 > two.Dp(featherMaxDp) || f2 > 2*short/3 {
			t.Fatalf("2× featherWidth(%d,%d)=%d 越界（上限 Dp(%d)=%d、短边 1/3=%d）",
				2*w, 2*h, f2, featherMaxDp, two.Dp(featherMaxDp), 2*short/3)
		}
	}
	// 大元素落到**Dp 上限**（随 DPI 走，证明是响应式而非固定 px）。
	if got, want := featherWidth(one, 2000, 2000), one.Dp(featherMaxDp); got != want {
		t.Fatalf("大元素 featherWidth = %d, want %d（= Dp(featherMaxDp)）", got, want)
	}
}

// TestShapesEqual 形裁去重判定：相等才免重建。
func TestShapesEqual(t *testing.T) {
	a := []shapePhys{{x: 1, y: 2, w: 3, h: 4, ellipse: 8}}
	b := []shapePhys{{x: 1, y: 2, w: 3, h: 4, ellipse: 8}}
	if !shapesEqual(a, b) {
		t.Fatal("相同应相等")
	}
	if shapesEqual(a, nil) {
		t.Fatal("长度不同不应相等")
	}
	b[0].w = 9
	if shapesEqual(a, b) {
		t.Fatal("内容不同不应相等")
	}
	if !shapesEqual(nil, nil) {
		t.Fatal("双空应相等")
	}
}

// TestInputRowRectsFollowsDesign 输入行三段几何（D49/§15.2）：1:1 还原 canvas（元素 48、
// 间距 12、边距 16 → 默认窗宽 608 下行 576、输入区恰 456）；窗口宽变化只伸缩胶囊
// （logo 锚左、send 锚右，圆钮直径/间距/字号不动）。
func TestInputRowRectsFollowsDesign(t *testing.T) {
	// 设计稿 1:1 契约（canvas：48/12/16/20、行 576 + 双侧 16 = 608）。
	if inputRowDp != 48 || inputGapDp != 12 || inputPadDp != 16 || inputIconDp != 20 || winWidthDp != 608 {
		t.Fatalf("常量偏离设计稿: row=%d gap=%d pad=%d icon=%d win=%d（DESIGN §15.2/D49）",
			inputRowDp, inputGapDp, inputPadDp, inputIconDp, winWidthDp)
	}
	const top, margin = 8, 16
	rowH, gap := inputRowDp, inputGapDp
	w := winWidthDp
	logo, pill, send := inputRowRects(w, top, rowH, gap, margin)

	// ① 三段等高；logo 贴左边距；输入区 = 设计稿 456；间隙/右边距逐值对应。
	if logo.Dy() != rowH || pill.Dy() != rowH || send.Dy() != rowH {
		t.Fatalf("三段应等高 %d: logo=%v pill=%v send=%v", rowH, logo, pill, send)
	}
	if logo != image.Rect(margin, top, margin+rowH, top+rowH) {
		t.Fatalf("logo 位 = %v, want 贴左边距", logo)
	}
	if got := pill.Dx(); got != 456 {
		t.Fatalf("输入区宽 = %d, want 456（canvas 1:1）", got)
	}
	if l, s := pill.Min.X-logo.Max.X, send.Min.X-pill.Max.X; l != gap || s != gap {
		t.Fatalf("间隙 = %d/%d, want %d/%d", l, s, gap, gap)
	}
	if m := w - send.Max.X; m != margin {
		t.Fatalf("右边距 = %d, want %d", m, margin)
	}

	// ② 响应式：窗口加宽 100——logo 锚左边距、send 锚右边距，胶囊左缘不动、吃掉全部增量。
	logo2, pill2, send2 := inputRowRects(w+100, top, rowH, gap, margin)
	if logo2 != logo {
		t.Fatalf("logo 应锚定左边距: %v, want %v", logo2, logo)
	}
	if send2 != send.Add(image.Pt(100, 0)) {
		t.Fatalf("send 应锚定右边距: %v, want %v", send2, send.Add(image.Pt(100, 0)))
	}
	if pill2.Min.X != pill.Min.X || pill2.Dx() != pill.Dx()+100 {
		t.Fatalf("胶囊应左缘不动、吃掉全部增量: %v 宽 %d, want 左 %d 宽 %d",
			pill2, pill2.Dx(), pill.Min.X, pill.Dx()+100)
	}

	// ③ 最窄窗口（winMinWidth）胶囊仍有内容区。
	_, pill3, _ := inputRowRects(winMinWidth, top, rowH, gap, margin)
	if pill3.Dx() <= 0 || pill3.Dx() >= pill.Dx() {
		t.Fatalf("winMinWidth 下胶囊 = %d，应在 (0, %d)", pill3.Dx(), pill.Dx())
	}
}

// TestFrameItemsLive 帧内容（渲染视图）：定稿块 + 实时思考 + 流式草稿顺序。
func TestFrameItemsLive(t *testing.T) {
	u := &UI{}
	u.m = newModel(u)
	u.m.add(blockUser, "你好")
	u.m.think.WriteString("想")
	u.m.drafting = true
	u.m.draft.WriteString("答")

	items := u.frameItems()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	if items[0].kind != blockUser || items[0].live {
		t.Fatalf("items[0] = %+v", items[0])
	}
	if items[1].kind != blockThinking || !items[1].live {
		t.Fatalf("items[1] = %+v（实时思考应 live）", items[1])
	}
	if items[2].kind != blockAssistant || !items[2].live || items[2].text != "答" {
		t.Fatalf("items[2] = %+v（流式草稿应 live）", items[2])
	}
}

// newFrameUI 帧级无窗口测试用的最小 UI（不起事件循环，与 TestFrameItemsLive 同法——
// §15.5：GUI 只进无窗口 headless 测试）。followTail 初值对齐 newUI。
func newFrameUI() *UI {
	u := &UI{followTail: true}
	u.m = newModel(u)
	u.th = newTheme()
	return u
}

// frameGtx 单帧布局上下文（608×460 @1x；src 传零值 = 禁用态，同 fadeCompose 的二次布局）。
func frameGtx(src input.Source) (layout.Context, *op.Ops) {
	ops := new(op.Ops)
	return layout.Context{
		Ops:         ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(winWidthDp, winHeightDp)),
		Source:      src,
		Now:         time.Now(),
	}, ops
}

// TestFrameCommitsScreenStateBeforeSubmit D55 帧提交次序契约：屏幕态（移窗/LWA_ALPHA/
// 形裁）与 overlay 呈现必须全部排在绘制提交**之前**——Gio 在 Present(1,0)（阻塞到下一
// vblank）之前就 ack 本帧，之后经 Window.Run 排队的 Win32 调用只能等 Present 返回才被
// 窗口线程服务，故排在 e.Frame 之后的一切屏幕态都会晚一个 vblank 合成（动画/拖动中
// "元素边缘滞后于元素"的实测根因）。
func TestFrameCommitsScreenStateBeforeSubmit(t *testing.T) {
	u := newFrameUI()
	u.m.add(blockAssistant, "首帧有内容 → 形裁有可提交的并集")
	var phases []string
	u.framePhase = func(p string) { phases = append(phases, p) }
	u.movePending = true  // 帧内已发生位移（拖动 / 停靠动画）
	u.alphaPending = true // 帧内已发生透明度改动

	gtx, _ := frameGtx(input.Source{})
	u.frame(gtx, func() {
		phases = append(phases, "submit")
		if u.movePending || u.alphaPending {
			t.Error("绘制提交时屏幕态仍挂起：本帧移窗/alpha 未 flush")
		}
	})

	want := []string{"compose", "commit", "present", "submit"}
	if len(phases) != len(want) {
		t.Fatalf("阶段 = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("阶段 = %v, want %v", phases, want)
		}
	}
	if u.frameSize == (image.Point{}) {
		t.Fatal("compose/commit 之前 layout 未跑（frameSize 未定）")
	}
}

// TestTranscriptManualScroll 消息区手动滚动（§15.3）：滚轮在转写区上滚离底 → 内容上移、
// 停止尾随；滚回底部 → 恢复尾随（新消息重新贴底）。方向口径：d>0 = 向新内容（尾随），
// d<0 = 上滚离开底部——与 Gio Windows 侧的滚轮反号、X11 Button4/5 映射一致。
func TestTranscriptManualScroll(t *testing.T) {
	u := newFrameUI()
	for i := 0; i < 40; i++ {
		u.m.add(blockAssistant, fmt.Sprintf("第 %02d 条：内容高过视口，制造滚动余量。", i))
	}
	var q input.Router
	frame := func() {
		gtx, ops := frameGtx(q.Source())
		u.layout(gtx)
		q.Frame(ops) // 提交 hit 树与过滤器（Gio 也在 render 之后、Present 之前做这一步）
	}

	// ① 帧 1：登记滚动手势区与过滤器，初始尾随贴底。
	frame()
	if !u.followTail {
		t.Fatal("初始应尾随贴底")
	}
	if u.contentH == 0 || u.scrollPx == 0 {
		t.Fatalf("内容应高过视口: contentH=%d scrollPx=%d", u.contentH, u.scrollPx)
	}
	bottom := u.scrollPx

	// ② 上滚 60px：位置上移、停止尾随。过滤器范围不随 scrollPx 走（见 updateScroll），
	// 故事件一到即可被接受，无需先等一帧刷新边界。
	q.Queue(pointer.Event{Kind: pointer.Scroll, Position: f32.Pt(300, 200), Scroll: f32.Pt(0, -60)})
	frame()
	if u.scrollPx != bottom-60 {
		t.Fatalf("上滚后 scrollPx = %d, want %d", u.scrollPx, bottom-60)
	}
	if u.followTail {
		t.Fatal("上滚离开底部应停止尾随")
	}

	// ③ 分两步下滚回底部：半程不恢复尾随，回到 overflow 才恢复（新消息重新贴底）。
	q.Queue(pointer.Event{Kind: pointer.Scroll, Position: f32.Pt(300, 200), Scroll: f32.Pt(0, 40)})
	frame()
	if u.scrollPx != bottom-20 || u.followTail {
		t.Fatalf("下滚 40: scrollPx=%d followTail=%v, want %d/false", u.scrollPx, u.followTail, bottom-20)
	}
	q.Queue(pointer.Event{Kind: pointer.Scroll, Position: f32.Pt(300, 200), Scroll: f32.Pt(0, 20)})
	frame()
	if u.scrollPx != bottom || !u.followTail {
		t.Fatalf("滚回底部应恢复尾随: scrollPx=%d followTail=%v, want %d/true",
			u.scrollPx, u.followTail, bottom)
	}
}
