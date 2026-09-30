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
	"gioui.org/widget"
)

// newShapeUI 形裁登记用的最小 UI（frameMetric 供淡出带剔除换算；bandBottom = 带底，
// 生产里由 layout 每帧写入——D54 动画前恒为 §15.3 静息顶带底 Dp(fadeBandDp)）。
func newShapeUI(pxPerDp float32) *UI {
	m := unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp}
	return &UI{frameMetric: m, bandBottom: m.Dp(fadeBandDp)}
}

// TestRecordStoresOutlineAndClip 形状登记（D44/D45/D62）：存真轮廓（未按视口/带裁剪——
// 羽化沿此取边）+ 可见裁剪区；裁剪后为空、零圆角不登记；带内元素**必须登记**
// （D62：带渐变只作用于登记形状的像素，取代旧「带整行单绘」）。
func TestRecordStoresOutlineAndClip(t *testing.T) {
	u := newShapeUI(1) // Dp(56) = 56px 淡出带
	viewport := image.Rect(0, 0, 100, 300)

	// ① 完全在淡出带内 → 登记（揭示带内元素按 g(y) 呈现，D62）。
	u.record(image.Rect(10, 0, 90, 50), 12, brandColor, viewport)
	if len(u.shapes) != 1 {
		t.Fatalf("带内矩形应登记: %+v", u.shapes)
	}
	// ② 跨带 → 存**真轮廓**（不裁到带）+ 裁剪区 + 底色（区外由 vis 自行裁剪）。
	u.record(image.Rect(10, 20, 90, 120), 12, brandColor, viewport)
	if len(u.shapes) != 2 || u.shapes[1].outline != image.Rect(10, 20, 90, 120) ||
		u.shapes[1].clip != viewport || u.shapes[1].radius != 12 || u.shapes[1].fill != brandColor {
		t.Fatalf("跨带矩形应存真轮廓 + 裁剪区 + 底色: %+v", u.shapes)
	}
	// ③ 带下、视口内 → 原样登记。
	u.record(image.Rect(8, 150, 92, 200), 12, brandColor, viewport)
	if len(u.shapes) != 3 || u.shapes[2].outline != image.Rect(8, 150, 92, 200) {
		t.Fatalf("视口内矩形应原样登记: %+v", u.shapes)
	}
	// ④ 视口外（滚动到上方）→ 裁空剔除。
	u.record(image.Rect(8, -60, 92, -10), 12, brandColor, viewport)
	if len(u.shapes) != 3 {
		t.Fatalf("视口外不应登记: %+v", u.shapes)
	}
	// ⑤ 零圆角（如被裁成细条的异常态）→ 剔除。
	u.record(image.Rect(8, 150, 92, 200), 0, brandColor, viewport)
	if len(u.shapes) != 3 {
		t.Fatalf("零圆角不应登记: %+v", u.shapes)
	}
}

// TestFeatherWidthResponsive 渐隐带宽响应式（D47/D48/D70）：受 [Dp(featherMinDp),
// Dp(featherMaxDp)] 与短边 1/3（防极小元素被整带吃掉）约束，且**随 DPI 同步放大**——
// Dp 换算保证不写死 px。D70 起带宽按输入胶囊统一、不再随元素尺寸变（统一性由
// TestFeatherWidthUnifiedToInputCapsule 断言）。断言写成"由常量推导的界"，改参数不需重写。
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
	// 带宽随 DPI 同步放大（Dp 换算 → 非固定 px；D70 后元素尺寸不再影响带宽）。
	f1, f2 := featherWidth(one, 2000, 2000), featherWidth(two, 4000, 4000)
	if f2 <= f1 {
		t.Fatalf("DPI 响应：2× 大元素 featherWidth=%d 应 > 1× 的 %d", f2, f1)
	}
}

// TestFeatherWidthUnifiedToInputCapsule 羽化带宽全元素统一（D70，用户实测：消息气泡
// 边缘与输入胶囊边缘羽化不一致）：带宽恒 = 输入胶囊的带宽 round(Dp(inputRowDp)×ratio)，
// 不随元素自身短边变化——多行气泡（短边大）此前封顶 3dp、chip（短边小）不足 1dp，
// 与胶囊各得不同带宽。修前本测试红（气泡/球≠胶囊值）。
func TestFeatherWidthUnifiedToInputCapsule(t *testing.T) {
	// 单行气泡 / 多行气泡 / 输入胶囊 / chip / 球（dp 尺寸）。
	sizes := [][2]int{{200, 34}, {300, 150}, {456, 48}, {80, 20}, {60, 60}}
	for _, m := range []unit.Metric{
		{PxPerDp: 1, PxPerSp: 1},
		{PxPerDp: 1.5, PxPerSp: 1.5},
		{PxPerDp: 2, PxPerSp: 2},
	} {
		ref := int(float64(m.Dp(inputRowDp))*featherRatio + 0.5)
		for _, c := range sizes {
			w, h := m.Dp(unit.Dp(c[0])), m.Dp(unit.Dp(c[1]))
			if got := featherWidth(m, w, h); got != ref {
				t.Errorf("featherWidth(%g, %d, %d) = %d, want %d（= 输入胶囊带宽，D70 统一）",
					m.PxPerDp, w, h, got, ref)
			}
		}
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

// TestOvershootStaysInFrame 过冲几何不越窗（D57）：easeOutBack 峰值处右钮沿 D54 同式
// 外推越过窗宽（实测 send 右缘 620 > 窗宽 608）→ 按钮越出会被位图缓冲边缘切平（D62 后
// 同旧形裁的窗界裁切观感）。整链路（inputBar 插值 → record）断言轮廓恒在窗内。
func TestOvershootStaysInFrame(t *testing.T) {
	u := newFrameUI()
	u.m.add(blockAssistant, "撑起转写区，让输入行落到正常绝对坐标")
	// 先起动画再翻 collapsed（beginExpand 同序）：静止态由 collapsed 推导，翻早了起点
	// 读成 1/1 → 动画无距离、瞬时完成。
	u.collapsed = true
	u.startExpandAnim(true)
	u.collapsed = false
	u.stepExpand(u.expandAn.start.Add(165 * time.Millisecond)) // 输入栏阶段峰值（barP≈1.053）
	if barP, _ := u.expandProgress(); barP <= 1 {
		t.Fatalf("该时刻应处于过冲段: barP=%v", barP)
	}
	gtx, _ := frameGtx(input.Source{})
	u.layout(gtx)
	if len(u.shapes) == 0 {
		t.Fatal("无登记形状")
	}
	for _, s := range u.shapes {
		if s.outline.Min.X < 0 || s.outline.Max.X > u.frameSize.X {
			t.Fatalf("登记轮廓越窗: %v (frame=%v)", s.outline, u.frameSize)
		}
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

// TestFrameCommitBeforeSubmitOverlayAfter D62 次序契约（单通道）：全帧合成先行（唯一
// 慢段），移窗一拍 flush 在**绘制提交之前**（fadePresent 取实测窗口矩形定位），ULW 提交
// 殿后。D44–D59 期「形裁/overlay 与 e.Frame 按通道分边」的约束随三通道退役作废（D62）：
// 位图 alpha 即形状/命中，单通道无亚帧错位，次序仅剩工程约束。
func TestFrameCommitBeforeSubmitOverlayAfter(t *testing.T) {
	u := newFrameUI()
	u.m.add(blockAssistant, "首帧有内容 → 合成有可提交的位图")
	var phases []string
	u.framePhase = func(p string) { phases = append(phases, p) }
	u.movePending = true // 帧内已发生位移（拖动 / 停靠动画）

	gtx, _ := frameGtx(input.Source{})
	pendingAtSubmit := false
	u.frame(gtx, func() {
		phases = append(phases, "submit")
		pendingAtSubmit = u.movePending
	})

	want := []string{"compose", "commit", "submit", "present"}
	if len(phases) != len(want) {
		t.Fatalf("阶段 = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("阶段 = %v, want %v", phases, want)
		}
	}
	if pendingAtSubmit {
		t.Error("绘制提交时移窗仍挂起：commitWinGeom 排到了 e.Frame 之后（present 定位会失准）")
	}
	if u.movePending {
		t.Error("帧结束移窗仍挂起：本帧位移未 flush")
	}
	if u.frameSize == (image.Point{}) {
		t.Fatal("compose/commit 之前 layout 未跑（frameSize 未定）")
	}
}

// TestFadePresentSubmitsBitmap D62：fadePresent 把整窗位图与当前不透明度一并交
// mainPresent（SourceConstantAlpha = u.alpha）；合成失败（composed=false）不提交，
// 保持上一帧位图。经 presentMain 槽注入捕获（生产恒 mainPresent）。
func TestFadePresentSubmitsBitmap(t *testing.T) {
	u := newFrameUI()
	u.hwnd = 1 // 非 0 即过门控（windowRectPx 无真句柄 → 回退 u.x/u.y）
	u.frameSize = image.Pt(100, 80)
	u.fadeBuf = make([]byte, 100*80*4)
	u.fadeBuf[3] = 0xFF // 一个非零像素
	u.x, u.y, u.alpha = 11, 22, 178

	var gotX, gotY, gotW, gotH int32
	var gotAlpha byte
	var gotBits []byte
	calls := 0
	presentMain = func(x, y, w, h int32, bits []byte, alpha byte) bool {
		calls++
		gotX, gotY, gotW, gotH, gotAlpha, gotBits = x, y, w, h, alpha, bits
		return true
	}
	defer func() { presentMain = mainPresent }()

	u.fadePresent(false)
	if calls != 0 {
		t.Fatal("合成失败不应提交（保持上一帧）")
	}
	u.fadePresent(true)
	if calls != 1 {
		t.Fatalf("应提交一次, got %d", calls)
	}
	if gotX != 11 || gotY != 22 || gotW != 100 || gotH != 80 {
		t.Fatalf("提交矩形 = (%d,%d)+%dx%d, want (11,22)+100x80", gotX, gotY, gotW, gotH)
	}
	if gotAlpha != 178 {
		t.Fatalf("SourceConstantAlpha = %d, want 178（u.alpha 直传）", gotAlpha)
	}
	if len(gotBits) != 100*80*4 || gotBits[3] != 0xFF {
		t.Fatal("位图应为整窗缓冲")
	}
}

// ---- D78 启动显隐时序（挂接即隐藏 → 首帧 ULW 成功即揭示）----

// TestPresentableDuringReveal 合成/提交门（D78）：主窗隐藏时不提交（hideMain 语义——
// 防隐藏后仍有帧把位图唤回）；唯**启动揭示期**例外——隐藏是我方所为，须照常合成提交，
// 否则永不首帧、永不揭示。
func TestPresentableDuringReveal(t *testing.T) {
	u := newFrameUI()
	if u.presentable() != mainVisible() {
		t.Fatalf("无揭示待定：presentable 应等于 mainVisible()（平台口径），got %v want %v",
			u.presentable(), mainVisible())
	}
	u.revealPending.Store(true)
	if !u.presentable() {
		t.Fatal("揭示期应放行合成/提交（否则永无首帧）")
	}
}

// TestFirstPresentRevealsWindow 揭示时机（D78）：首帧 ULW 提交成功 → 恰好揭示一次并清
// 揭示待定；提交失败（GPU 降级）不揭示、待定保持（拍板：降级保持隐藏，下帧重试）；
// 已揭示不重复。
func TestFirstPresentRevealsWindow(t *testing.T) {
	u := newFrameUI()
	u.hwnd = 1
	u.frameSize = image.Pt(100, 80)
	u.fadeBuf = make([]byte, 100*80*4)
	u.x, u.y = 0, 0

	oldPresent, oldReveal := presentMain, revealMain
	defer func() { presentMain, revealMain = oldPresent, oldReveal }()
	reveals := 0
	revealMain = func(uintptr) { reveals++ }

	u.revealPending.Store(true)
	presentMain = func(int32, int32, int32, int32, []byte, byte) bool { return false }
	u.fadePresent(true)
	if reveals != 0 || !u.revealPending.Load() {
		t.Fatalf("提交失败不应揭示、待定保持: reveals=%d pending=%v", reveals, u.revealPending.Load())
	}

	presentMain = func(int32, int32, int32, int32, []byte, byte) bool { return true }
	u.fadePresent(true)
	if reveals != 1 {
		t.Fatalf("首帧提交成功应揭示一次, got %d", reveals)
	}
	if u.revealPending.Load() {
		t.Fatal("揭示后待定应清除")
	}
	u.fadePresent(true)
	if reveals != 1 {
		t.Fatalf("揭示应一次性, got %d", reveals)
	}
}

// TestSelForRowState 行选状态缓存（D63）：按行序 get-or-create，跨调用同指针
// （选中态/焦点跨帧持久）；越界增长幂等。
func TestSelForRowState(t *testing.T) {
	u := newFrameUI()
	a := u.selFor(0)
	b := u.selFor(0)
	if a != b {
		t.Fatal("同行序应返回同一 Selectable 实例")
	}
	if u.selFor(2) == a {
		t.Fatal("不同行序应为不同实例")
	}
	if len(u.selRows) != 3 {
		t.Fatalf("应增长到 3 个: %d", len(u.selRows))
	}
}

// TestCompositeAssistantBubble 单回复单气泡（D66）：一条多块助手消息在 transcript 中
// 占一行——行选键数 = 块数、气泡底板每消息登记一个矩形。
func TestCompositeAssistantBubble(t *testing.T) {
	u := newFrameUI()
	u.m.add(blockUser, "问")
	u.m.add(blockAssistant, "好的：\n\n- 一项\n- 二项\n\n```go\ndone\n```")

	gtx, _ := frameGtx(input.Source{})
	u.transcript(gtx, 600, 400)
	// 行选键：用户 1 + 助手复合行 4 块 = 5。
	if len(u.selRows) != 5 {
		t.Fatalf("selRows 键数 = %d, want 5（D66 双键挂接）", len(u.selRows))
	}
	// 形状：每消息一个矩形（用户气泡 + 助手气泡），复合行不再逐块拆底板。
	if len(u.shapes) != 2 {
		t.Fatalf("登记形状 = %d, want 2", len(u.shapes))
	}
	// 复合行应作为气泡底板渲染（与段落同 pillBg 系）。
	_, _, _, _, bubble := u.rowStyle(gtx, u.frameItems()[1], func(int) *widget.Selectable { return nil })
	if !bubble {
		t.Fatal("助手复合行应为气泡")
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

// TestTranscriptTopHeadroom D74 顶部可滚空白：滚动内容头部垫一个**淡出带高**（fadeBandDp）
// 的 pad——滚到最上时首行完整落在带下可读，否则首行困在带内、`scrollPx` 不可为负而永远
// 半透明（= 淡出遮挡内容）。pad 计入 contentH/overflow（可多滚一个带高），底钉与尾随语义
// 不变（空白随内容滚，非视口固定留白——后者渐隐失效）。
func TestTranscriptTopHeadroom(t *testing.T) {
	u := newFrameUI()
	for i := 0; i < 40; i++ {
		u.m.add(blockAssistant, fmt.Sprintf("第 %02d 条：内容高过视口，需要顶部 headroom。", i))
	}
	gtx, _ := frameGtx(input.Source{})
	const w, h = 600, 400
	pad := gtx.Dp(fadeBandDp)

	// 行高总和（复用生产量高；pad 单列不混入行）——用于锁 contentH 契约。
	rowsTotal := 0
	items := u.frameItems()
	gap := gtx.Dp(rowGapDp)
	for i := range items {
		rowsTotal += u.measureRow(gtx, items[i], w, func(int) *widget.Selectable { return nil }).height()
	}
	rowsTotal += gap * (len(items) - 1)
	if rowsTotal <= h {
		t.Fatalf("测试前提：内容应高过视口，rowsTotal = %d", rowsTotal)
	}

	// ① 滚到最上（scrollPx=0、非尾随）：首行落在淡出带之下；pad 计入滚动内容。
	u.followTail = false
	u.shapes = u.shapes[:0]
	u.transcript(gtx, w, h)
	if u.scrollPx != 0 {
		t.Fatalf("顶部 scrollPx = %d, want 0", u.scrollPx)
	}
	if len(u.shapes) == 0 {
		t.Fatal("应登记行形状")
	}
	if y := u.shapes[0].outline.Min.Y; y < pad {
		t.Fatalf("首行 y = %d, want >= %d（D74：不入淡出带）", y, pad)
	}
	if u.contentH != rowsTotal+pad {
		t.Fatalf("contentH = %d, want 行高 %d + pad %d（D74：pad 计入滚动内容）",
			u.contentH, rowsTotal, pad)
	}

	// ② 尾随到底：底钉不因 pad 偏移——scrollPx = overflow、末行底 == 视口底。
	u.followTail = true
	u.shapes = u.shapes[:0]
	u.transcript(gtx, w, h)
	if u.scrollPx != u.contentH-h {
		t.Fatalf("底部 scrollPx = %d, want overflow %d", u.scrollPx, u.contentH-h)
	}
	if last := u.shapes[len(u.shapes)-1].outline; last.Max.Y != h {
		t.Fatalf("末行底 = %d, want %d（底钉不回归）", last.Max.Y, h)
	}

	// ③ 短内容：不产生滚动，末行仍贴底（pad 不把内容顶离输入栏）。
	u2 := newFrameUI()
	u2.m.add(blockAssistant, "短内容一条。")
	u2.transcript(gtx, w, h)
	if u2.scrollPx != 0 {
		t.Fatalf("短内容应无滚动: scrollPx = %d", u2.scrollPx)
	}
	if last := u2.shapes[len(u2.shapes)-1].outline; last.Max.Y != h {
		t.Fatalf("短内容末行底 = %d, want %d（贴底）", last.Max.Y, h)
	}
}

// TestWheelGesturePinStateMachine D71 状态机：转写区滚轮真有增量 → 手势挂上（锚点 =
// 当帧光标屏幕坐标）；光标不动保持、移位解除、收起解除；解除后重新滚动重新挂上。
func TestWheelGesturePinStateMachine(t *testing.T) {
	u := newFrameUI()
	for i := 0; i < 40; i++ {
		u.m.add(blockAssistant, fmt.Sprintf("第 %02d 条：内容高过视口，制造滚动余量。", i))
	}
	var q input.Router
	frame := func() {
		gtx, ops := frameGtx(q.Source())
		u.layout(gtx)
		q.Frame(ops) // 提交 hit 树与过滤器
	}

	frame() // 登记滚动手势区
	if u.wheelCap {
		t.Fatal("初始不应有滚动手势")
	}

	// ① 上滚一格（真有增量）：手势挂上，锚点 = 当帧光标。
	q.Queue(pointer.Event{Kind: pointer.Scroll, Position: f32.Pt(300, 200), Scroll: f32.Pt(0, -60)})
	frame()
	if !u.wheelCap {
		t.Fatal("滚轮真有增量后手势应挂上")
	}
	if cur := cursorPos(); u.wheelAnchor != cur {
		t.Fatalf("锚点 = %v, want 当帧光标 %v", u.wheelAnchor, cur)
	}

	// ② 光标不动：保持。
	frame()
	if !u.wheelCap {
		t.Fatal("光标未动应保持手势")
	}

	// ③ 光标移位（锚点失配）：解除。
	u.wheelAnchor = point{x: cursorPos().x + 99, y: cursorPos().y}
	frame()
	if u.wheelCap {
		t.Fatal("光标移位应解除手势")
	}

	// ④ 重新滚动：重新挂上；收起：解除。
	q.Queue(pointer.Event{Kind: pointer.Scroll, Position: f32.Pt(300, 200), Scroll: f32.Pt(0, -40)})
	frame()
	if !u.wheelCap {
		t.Fatal("解除后重新滚动应重新挂上")
	}
	u.collapsed = true
	frame()
	if u.wheelCap {
		t.Fatal("收起应解除手势")
	}
}

// TestFadePresentPinsCursorPixelDuringGesture D71 钉点落笔：手势期间提交位图的光标
// 所在像素 alpha 顶到 ≥1（分层窗逐像素命中保住 → 透明间隙不吞滚轮），只钉 A==0 像素、
// 解除后不再钉、光标在帧外不落笔。经 presentMain 槽捕获提交字节（钉点须在提交前）。
func TestFadePresentPinsCursorPixelDuringGesture(t *testing.T) {
	u := newFrameUI()
	u.hwnd = 1 // 非 0 即过门控（windowRectPx 无真句柄 → 回退 u.x/u.y）
	u.frameSize = image.Pt(400, 300)
	u.fadeBuf = make([]byte, 400*300*4) // 全透明间隙帧
	cur := cursorPos()
	u.x, u.y = cur.x-200, cur.y-150 // 窗口对齐光标 → 光标落帧中心 (200,150)
	oi := (150*400 + 200) * 4

	var presented []byte
	old := presentMain
	presentMain = func(x, y, w, h int32, bits []byte, alpha byte) bool {
		presented = append([]byte(nil), bits...)
		return true
	}
	defer func() { presentMain = old }()

	// ① 手势进行中：间隙像素钉到 alpha=1（提交字节里已含钉点）。
	u.wheelCap = true
	u.wheelAnchor = cur
	u.fadePresent(true)
	if presented == nil {
		t.Fatal("presentMain 未被调用")
	}
	if presented[oi+3] != 1 {
		t.Fatalf("钉点像素 alpha = %d, want 1（间隙像素应钉到 ≥1 保住命中）", presented[oi+3])
	}

	// ② 已有内容像素不改写（只钉 A==0）。
	u.fadeBuf[oi+3] = 40
	presented = nil
	u.fadePresent(true)
	if presented == nil {
		t.Fatal("presentMain 未被调用")
	}
	if presented[oi+3] != 40 {
		t.Fatalf("内容像素 alpha = %d, want 40（既有可见像素不应改写）", presented[oi+3])
	}

	// ③ 手势解除：不再钉（恢复 alpha=0 逐像素穿透）。
	u.wheelCap = false
	u.fadeBuf[oi+3] = 0
	presented = nil
	u.fadePresent(true)
	if presented == nil {
		t.Fatal("presentMain 未被调用")
	}
	if presented[oi+3] != 0 {
		t.Fatalf("解除后 alpha = %d, want 0（恢复穿透）", presented[oi+3])
	}

	// ④ 光标在帧外（窗移出光标下方）：不落笔。
	u.wheelCap = true
	u.x = cur.x + 1000
	presented = nil
	u.fadePresent(true)
	if presented == nil {
		t.Fatal("presentMain 未被调用")
	}
	if presented[oi+3] != 0 {
		t.Fatalf("帧外钉点 alpha = %d, want 0", presented[oi+3])
	}
}

// TestLogoRightClickMenuRequest D72 logo 右键菜单手势：区域内右键按下（Buttons 含
// secondary）武装、同指针原位抬起 = 请求菜单；移出热区（位置门控）/ 系统取消 / 左键
// 均不请求。收起态（球）与展开态（logo 钮，D49 与球同位）热区同源——ballRect 即两态
// 窗口系矩形。透明像素不产 OS 事件（D62 逐像素命中）由窗口层负责，这里只测几何过滤
// 后的状态机。
func TestLogoRightClickMenuRequest(t *testing.T) {
	type env struct {
		u     *UI
		q     *input.Router
		frame func()
		n     *int
	}
	setup := func() env {
		u := newFrameUI()
		n := 0
		u.logoMenuHook = func() { n++ }
		q := new(input.Router)
		e := env{u: u, q: q, n: &n}
		e.frame = func() {
			gtx, ops := frameGtx(q.Source())
			u.layout(gtx)
			q.Frame(ops)
		}
		return e
	}
	mid := func(u *UI) f32.Point {
		r := ballRect(u.frameSize, u.frameMetric.Dp)
		return f32.Pt(float32((r.Min.X+r.Max.X)/2), float32((r.Min.Y+r.Max.Y)/2))
	}
	press := func(e env, pos f32.Point, b pointer.Buttons, id pointer.ID) {
		e.q.Queue(pointer.Event{Kind: pointer.Press, Position: pos, Buttons: b, PointerID: id})
		e.frame()
	}
	release := func(e env, pos f32.Point, id pointer.ID) {
		e.q.Queue(pointer.Event{Kind: pointer.Release, Position: pos, PointerID: id})
		e.frame()
	}

	// ① 收起态：按下不请求（等原位抬起），抬起后请求 1 次。
	e := setup()
	e.u.collapsed = true
	e.frame()
	p := mid(e.u)
	press(e, p, pointer.ButtonSecondary, 1)
	if *e.n != 0 {
		t.Fatalf("按下即请求 = %d, want 0（应等原位抬起）", *e.n)
	}
	release(e, p, 1)
	if *e.n != 1 {
		t.Fatalf("原位抬起后请求 = %d, want 1", *e.n)
	}

	// ② 移出球矩形抬起 = 放弃（右键拖走不弹）：Gio Release 恒投按下方，须位置门控——
	// 窗内透明处（球外）与窗外两种落点都不得弹。
	for _, away := range []f32.Point{{X: 2, Y: 2}, {X: -20, Y: -20}} {
		e = setup()
		e.u.collapsed = true
		e.frame()
		p = mid(e.u)
		press(e, p, pointer.ButtonSecondary, 2)
		release(e, away, 2)
		if *e.n != 0 {
			t.Fatalf("移出后抬起（%v）请求 = %d, want 0", away, *e.n)
		}
	}

	// ③ 系统取消（窗口失焦等）后抬起不请求。
	e = setup()
	e.u.collapsed = true
	e.frame()
	p = mid(e.u)
	press(e, p, pointer.ButtonSecondary, 3)
	e.q.Queue(pointer.Event{Kind: pointer.Cancel, PointerID: 3})
	e.frame()
	release(e, p, 3)
	if *e.n != 0 {
		t.Fatalf("取消后抬起请求 = %d, want 0", *e.n)
	}

	// ④ 左键按下抬起不请求（不干扰左键互切/拖动语义）。
	e = setup()
	e.u.collapsed = true
	e.frame()
	p = mid(e.u)
	press(e, p, pointer.ButtonPrimary, 4)
	release(e, p, 4)
	if *e.n != 0 {
		t.Fatalf("左键请求 = %d, want 0", *e.n)
	}

	// ⑤ 展开态：logo 钮（D49 与球同位）右键原位抬起请求。
	e = setup()
	e.frame()
	p = mid(e.u)
	press(e, p, pointer.ButtonSecondary, 5)
	release(e, p, 5)
	if *e.n != 1 {
		t.Fatalf("展开态请求 = %d, want 1", *e.n)
	}
}
