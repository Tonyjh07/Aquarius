package uigui

import (
	"image"
	"math"
	"testing"
	"time"
)

// ---- 缓动纯函数（D54 拍板的三条曲线） ----

// TestCssEase 消息区曲线 = CSS ease = cubic-bezier(.25,.1,.25,1)（D54 拍板）：端点精确、
// 区间内单调不越界、先快后慢（中点已过 ~0.8）。
func TestCssEase(t *testing.T) {
	if cssEase(0) != 0 || cssEase(1) != 1 {
		t.Fatalf("端点: cssEase(0)=%v cssEase(1)=%v, want 0/1", cssEase(0), cssEase(1))
	}
	if cssEase(-0.5) != 0 || cssEase(1.5) != 1 {
		t.Fatalf("区间外应夹取端点: cssEase(-0.5)=%v cssEase(1.5)=%v",
			cssEase(-0.5), cssEase(1.5))
	}
	prev := -1.0
	for i := 0; i <= 200; i++ {
		x := float64(i) / 200
		v := cssEase(x)
		if v < prev-1e-9 {
			t.Fatalf("非单调: cssEase(%v)=%v < 上一点 %v", x, v, prev)
		}
		if v < -1e-9 || v > 1+1e-9 {
			t.Fatalf("越界: cssEase(%v)=%v", x, v)
		}
		prev = v
	}
	// 参考值：浏览器对 cubic-bezier(.25,.1,.25,1) 在 t=0.5 给出 ≈0.802。
	if mid := cssEase(0.5); math.Abs(mid-0.802) > 0.01 {
		t.Fatalf("cssEase(0.5)=%.4f, want ≈0.802", mid)
	}
	// 可微（牛顿迭代收敛足够）：导数在 (0,1) 内恒正 → 采样斜率不为负。
	for i := 1; i < 100; i++ {
		a, b := cssEase(float64(i)/100), cssEase(float64(i+1)/100)
		if b < a-1e-9 {
			t.Fatalf("相邻采样下降: %v → %v", a, b)
		}
	}
}

// TestBackOut 输入栏轻回弹（D54 拍板：easeOutBack c1=1.2 → 峰值过冲约 5.3% ≈ 6%）。
func TestBackOut(t *testing.T) {
	if backOut(0, backOutC1) != 0 || backOut(1, backOutC1) != 1 {
		t.Fatalf("端点: backOut(0)=%v backOut(1)=%v, want 0/1",
			backOut(0, backOutC1), backOut(1, backOutC1))
	}
	if backOut(-1, backOutC1) != 0 || backOut(2, backOutC1) != 1 {
		t.Fatalf("区间外应夹取端点")
	}
	peak, min := 0.0, 2.0
	for i := 0; i <= 1000; i++ {
		v := backOut(float64(i)/1000, backOutC1)
		if v > peak {
			peak = v
		}
		if v < min {
			min = v
		}
	}
	if peak <= 1.0 || peak > 1.07 {
		t.Fatalf("过冲峰值 = %.4f, want ∈ (1.0, 1.07]（≈6%% 轻回弹）", peak)
	}
	if min < 0 {
		t.Fatalf("最小值 = %.4f, 不得为负（几何不得反向越出 logo）", min)
	}
	// 无回弹系数 → 纯 easeOut（对照：c1=0 即 cubic-out 形态，不越界）。
	if v := backOut(0.5, 0); v > 1 {
		t.Fatalf("c1=0 不应回弹: %v", v)
	}
}

// TestInSine 收起输入栏曲线 = easeInSine：慢起、到球时最快，端点精确且单调。
func TestInSine(t *testing.T) {
	if inSine(0) != 0 || inSine(1) != 1 {
		t.Fatalf("端点: inSine(0)=%v inSine(1)=%v, want 0/1", inSine(0), inSine(1))
	}
	if inSine(-1) != 0 || inSine(2) != 1 {
		t.Fatalf("区间外应夹取端点")
	}
	prev := -1.0
	for i := 0; i <= 100; i++ {
		v := inSine(float64(i) / 100)
		if v < prev-1e-9 {
			t.Fatalf("非单调: inSine(%v)=%v < %v", float64(i)/100, v, prev)
		}
		prev = v
	}
	// 慢起：起步 1/10 行程远小于 t（与 easeOutBack 的快起对照）。
	if v := inSine(0.1); v > 0.05 {
		t.Fatalf("easeInSine 起步应慢: inSine(0.1)=%.4f", v)
	}
}

// ---- 揭示带与几何插值（纯逻辑） ----

// TestRevealBand 消息揭示带（D54）：静息 = §15.3 顶带；全隐 = 空带且带顶 = 转写区底；
// 中途带顶单调上移、带高单调生长，且**带底永远夹在转写区内**（不压状态行/输入行）。
func TestRevealBand(t *testing.T) {
	const h, band = 400, 56
	if top, bottom := revealBand(1, h, band); top != 0 || bottom != band {
		t.Fatalf("静息应为 (0,%d), 得 (%d,%d)", band, top, bottom)
	}
	if top, bottom := revealBand(0, h, band); top != h || bottom != h {
		t.Fatalf("全隐应为空带 (%d,%d), 得 (%d,%d)", h, h, top, bottom)
	}
	prevTop, prevH := h, -1
	for i := 1; i < 100; i++ {
		p := float64(i) / 100
		top, bottom := revealBand(p, h, band)
		if top < 0 || bottom > h || bottom < top {
			t.Fatalf("p=%v → (%d,%d) 越界", p, top, bottom)
		}
		if bh := bottom - top; bh > band {
			t.Fatalf("p=%v 带高 %d > %d", p, bh, band)
		}
		if top > prevTop {
			t.Fatalf("带顶应随 p 单调上移: p=%v 时 %d > %d", p, top, prevTop)
		}
		if bh := bottom - top; bh < prevH {
			t.Fatalf("带高应随 p 单调生长: p=%v 时 %d < %d", p, bh, prevH)
		}
		prevTop, prevH = top, bottom-top
	}
	// 转写区极矮时带底不得溢出（状态行在 h 之下）。
	if _, bottom := revealBand(0.5, 20, band); bottom > 20 {
		t.Fatalf("矮转写区带底溢出: %d > 20", bottom)
	}
}

// TestRowRectsFromSend 输入栏几何按 send 行程分段导出（D76，修订 D54 单标量插值）：
// 里程碑（0 → logo 圆、split → 胶囊成圆、1 → D49 终位）、split 处连续、缩/长段双间隙
// 恒 gap（含过冲）、间距单调、两段区间不重叠（缩/长段只改宽、平移段只移位）。
func TestRowRectsFromSend(t *testing.T) {
	const w, top, rowH, gap, margin = 608, 8, 48, 12, 16
	logo, pillEnd, sendEnd := inputRowRects(w, top, rowH, gap, margin)
	circle := image.Rect(pillEnd.Min.X, pillEnd.Min.Y, pillEnd.Min.X+rowH, pillEnd.Min.Y+rowH)
	// split = send.Min 抵达成圆处（pillEnd.Min+rowH+gap）的行程进度（D76 分界点）。
	split := float64(pillEnd.Min.X+rowH+gap-logo.Min.X) / float64(sendEnd.Min.X-logo.Min.X)

	// 里程碑：0 → 双双退化为 logo 圆（logo 最后绘制盖住）；split → 胶囊成圆贴 send；1 → D49 终位。
	p0, s0 := rowRectsFromSend(logo, pillEnd, sendEnd, 0, w)
	if p0 != logo || s0 != logo {
		t.Fatalf("sendP=0 应双双退化为 logo 圆: pill=%v send=%v", p0, s0)
	}
	ps, ss := rowRectsFromSend(logo, pillEnd, sendEnd, split, w)
	if ps != circle {
		t.Fatalf("split 处胶囊应成圆 %v, 得 %v", circle, ps)
	}
	if ss.Min.X != circle.Max.X+gap || ss.Dy() != rowH {
		t.Fatalf("split 处 send 应贴圆间隙 %d 且行高不变: %v", gap, ss)
	}
	p1, s1 := rowRectsFromSend(logo, pillEnd, sendEnd, 1, w)
	if p1 != pillEnd || s1 != sendEnd {
		t.Fatalf("sendP=1 应取终位: pill=%v send=%v, want %v/%v", p1, s1, pillEnd, sendEnd)
	}

	// split 连续：分支切换不跳变（中途反向由此天然连续）。
	if below, _ := rowRectsFromSend(logo, pillEnd, sendEnd, split-1e-9, w); below != ps {
		t.Fatalf("split 处应连续: 下侧 %v vs 上侧 %v", below, ps)
	}

	// 全程扫描（0 → 1.6 含过冲）：行高不变、在窗内、间距与几何单调不减。
	prevLogoGap, prevJoinGap := logo.Min.X-logo.Max.X, logo.Min.X-logo.Max.X
	prevPillMax, prevSendMin := logo.Max.X, logo.Min.X
	for i := 0; i <= 160; i++ {
		p := float64(i) / 100
		pr, sr := rowRectsFromSend(logo, pillEnd, sendEnd, p, w)
		if pr.Dy() != rowH || sr.Dy() != rowH {
			t.Fatalf("行高不随动画变: pill.h=%d send.h=%d", pr.Dy(), sr.Dy())
		}
		if pr.Min.X < 0 || pr.Max.X > w || sr.Min.X < 0 || sr.Max.X > w {
			t.Fatalf("p=%v 越窗: pill=%v send=%v (w=%d)", p, pr, sr, w)
		}
		logoGap, joinGap := pr.Min.X-logo.Max.X, sr.Min.X-pr.Max.X
		if logoGap < prevLogoGap || joinGap < prevJoinGap {
			t.Fatalf("p=%v 间距应单调不减: logoGap %d→%d joinGap %d→%d", p,
				prevLogoGap, logoGap, prevJoinGap, joinGap)
		}
		if pr.Max.X < prevPillMax || sr.Min.X < prevSendMin {
			t.Fatalf("p=%v 几何应单调不缩回: pill.Max %d→%d send.Min %d→%d", p,
				prevPillMax, pr.Max.X, prevSendMin, sr.Min.X)
		}
		prevLogoGap, prevJoinGap = logoGap, joinGap
		prevPillMax, prevSendMin = pr.Max.X, sr.Min.X
	}

	// 缩/长段（sendP ≥ split）：双间隙恒 gap（含过冲），左缘钉终位 → 只改宽不移位。
	for i := int(split*100) + 1; i <= 140; i++ {
		p := float64(i) / 100
		pr, sr := rowRectsFromSend(logo, pillEnd, sendEnd, p, w)
		if pr.Min.X != pillEnd.Min.X {
			t.Fatalf("sendP=%v 缩/长段左缘应钉终位: %d != %d", p, pr.Min.X, pillEnd.Min.X)
		}
		if lg, jg := pr.Min.X-logo.Max.X, sr.Min.X-pr.Max.X; lg != gap || jg != gap {
			t.Fatalf("sendP=%v 缩/长段双间隙应恒 %d: logoGap=%d joinGap=%d (pill=%v send=%v)",
				p, gap, lg, jg, pr, sr)
		}
	}

	// 平移段（sendP < split）：宽恒 = 行高 → 只移位不改宽（与缩/长段区间不重叠）。
	for i := 0; i <= int(split*100); i++ {
		p := float64(i) / 100
		pr, _ := rowRectsFromSend(logo, pillEnd, sendEnd, p, w)
		if pr.Dx() != rowH {
			t.Fatalf("sendP=%v 平移段胶囊应恒成圆宽 %d, 得 %d", p, rowH, pr.Dx())
		}
	}

	// 过冲回弹（D54 手感 × D76 双间隙）：越出终位、收界在窗、胶囊随夹后 send 同步。
	po, so := rowRectsFromSend(logo, pillEnd, sendEnd, 1.053, w)
	if po.Max.X <= pillEnd.Max.X || so.Min.X <= sendEnd.Min.X {
		t.Fatalf("过冲应越出终位: pill=%v send=%v (终位 %v/%v)", po, so, pillEnd, sendEnd)
	}
	if so.Max.X > w {
		t.Fatalf("过冲收界后仍越窗: %v (w=%d)", so, w)
	}
	if lg, jg := po.Min.X-logo.Max.X, so.Min.X-po.Max.X; lg != gap || jg != gap {
		t.Fatalf("过冲收界后双间隙应恒 %d: logoGap=%d joinGap=%d", gap, lg, jg)
	}

	// p<0 防御（缓动理论上不产生负值，仍须夹住不反向缩过 logo）。
	if pn, sn := rowRectsFromSend(logo, pillEnd, sendEnd, -1, w); pn != logo || sn != logo {
		t.Fatalf("p<0 应夹为 logo 圆: %v/%v", pn, sn)
	}
	// w≤0 跳过收界（纯几何口径）：过冲原始外推仍在。
	if _, sRaw := rowRectsFromSend(logo, pillEnd, sendEnd, 1.053, 0); sRaw.Max.X <= w {
		t.Fatalf("w=0 不应收界: %v", sRaw)
	}
}

// TestClampRowX 过冲几何夹回窗口边界（D57；D76 夹取先 send 后胶囊）：easeOutBack 峰值处
// 右钮右缘越过窗宽会被窗边切平（实测 620 > 608）、同帧形裁/overlay 越界；收界**只平移保尺寸**，
// 窗内 16px 行边距仍留给回弹（D54「越出终位再回落」不落空）；胶囊由夹后 send 导出 → 双间隙仍恒 gap。
func TestClampRowX(t *testing.T) {
	const w, top, rowH, gap, margin = 608, 8, 48, 12, 16
	logo, pillEnd, sendEnd := inputRowRects(w, top, rowH, gap, margin)

	// 全进度扫描（含过冲与负值防御段）：收界后恒在窗内；send 只平移不改宽。
	for i := -20; i <= 180; i++ {
		p := float64(i) / 100
		pr, sr := rowRectsFromSend(logo, pillEnd, sendEnd, p, w)
		if pr.Min.X < 0 || pr.Max.X > w || sr.Min.X < 0 || sr.Max.X > w {
			t.Fatalf("p=%v 收界后越窗: pill=%v send=%v (w=%d)", p, pr, sr, w)
		}
		if sr.Dx() != rowH {
			t.Fatalf("p=%v send 收界只该平移不该改宽: %d", p, sr.Dx())
		}
	}

	// 峰值处原始外推确实越窗（复现缺陷的口径），收界后贴窗但仍在终位右侧（回弹保留）。
	rawS := lerpRect(logo, sendEnd, 1.053)
	if rawS.Max.X <= w {
		t.Fatalf("峰值外推应越窗（否则收界无意义）: send=%v w=%d", rawS, w)
	}
	cS := clampRowX(rawS, w)
	if cS.Max.X != w || cS.Dx() != sendEnd.Dx() {
		t.Fatalf("收界后应贴窗且保尺寸: got %v want Max.X=%d Dx=%d", cS, w, sendEnd.Dx())
	}
	if cS.Min.X <= sendEnd.Min.X {
		t.Fatalf("收界不应吃掉回弹：右钮仍应越出终位: %d <= %d", cS.Min.X, sendEnd.Min.X)
	}

	// D76 夹取顺序：峰值贴边收界后，胶囊随夹后 send 导出——send 与单独收界一致、
	// 双间隙仍恒 gap、胶囊回弹仍可见。
	po, so := rowRectsFromSend(logo, pillEnd, sendEnd, 1.053, w)
	if so != cS {
		t.Fatalf("几何导出的 send 应与单独收界一致: %v vs %v", so, cS)
	}
	if lg, jg := po.Min.X-logo.Max.X, so.Min.X-po.Max.X; lg != gap || jg != gap {
		t.Fatalf("收界后双间隙应恒 %d: logoGap=%d joinGap=%d (pill=%v)", gap, lg, jg, po)
	}
	if po.Max.X <= pillEnd.Max.X {
		t.Fatalf("胶囊回弹收界后仍应越出终位: %d <= %d", po.Max.X, pillEnd.Max.X)
	}

	// 极窄窗兜底：尺寸也保不住时退化为压缩到窗内（仍不越界）。
	narrow := clampRowX(rawS, 20)
	if narrow.Min.X < 0 || narrow.Max.X > 20 || narrow.Dx() > 20 {
		t.Fatalf("窄窗兜底越界: %v", narrow)
	}
}

// ---- 时间线与状态机 ----

// startAt 启动动画并返回基准时刻（headless：u.w == nil，不起唤帧 ticker，D54）。
func startAt(u *UI, expand bool) time.Time {
	u.startExpandAnim(expand)
	return u.expandAn.start
}

// TestExpandTimeline 展开 700ms 严格先后（D54）：输入栏 260ms 先跑完，消息区 440ms 才起。
func TestExpandTimeline(t *testing.T) {
	u := &UI{collapsed: true}
	t0 := startAt(u, true)
	if u.expandAn.stop != nil {
		t.Fatal("headless 不应起唤帧 ticker（D54）")
	}
	u.expandAn.advance(t0)
	if u.expandAn.barP != 0 || u.expandAn.msgP != 0 {
		t.Fatalf("起点进度应为 (0,0), 得 (%v,%v)", u.expandAn.barP, u.expandAn.msgP)
	}
	u.expandAn.advance(t0.Add(65 * time.Millisecond))
	if u.expandAn.barP <= 0 || u.expandAn.barP >= 1 {
		t.Fatalf("输入栏阶段前段 barP=%v, want ∈ (0,1)", u.expandAn.barP)
	}
	if u.expandAn.msgP != 0 {
		t.Fatalf("严格先后：消息阶段未开始 msgP 应为 0, 得 %v", u.expandAn.msgP)
	}
	// easeOutBack 回弹过冲：峰值处越出终位（D54 拍板的 ≈6% 轻回弹）。
	u.expandAn.advance(t0.Add(165 * time.Millisecond))
	if u.expandAn.barP <= 1 {
		t.Fatalf("回弹阶段 barP=%v, 应过冲 >1", u.expandAn.barP)
	}
	if u.expandAn.msgP != 0 {
		t.Fatalf("严格先后：消息阶段未开始 msgP 应为 0, 得 %v", u.expandAn.msgP)
	}
	u.expandAn.advance(t0.Add(expandBarMs * time.Millisecond))
	if u.expandAn.barP != 1 || u.expandAn.msgP != 0 || !u.expandAn.phase2 {
		t.Fatalf("260ms 处 barP=%v msgP=%v phase2=%v, want 1/0/true",
			u.expandAn.barP, u.expandAn.msgP, u.expandAn.phase2)
	}
	u.expandAn.advance(t0.Add((expandBarMs + expandMsgMs/2) * time.Millisecond))
	if u.expandAn.msgP <= 0 || u.expandAn.msgP >= 1 {
		t.Fatalf("消息阶段中段 msgP=%v, want ∈ (0,1)", u.expandAn.msgP)
	}
	u.expandAn.advance(t0.Add((expandBarMs + expandMsgMs) * time.Millisecond))
	if u.expandAn.active {
		t.Fatal("700ms 应已收尾")
	}
	if u.expandAn.barP != 1 || u.expandAn.msgP != 1 {
		t.Fatalf("收尾进度应为 (1,1), 得 (%v,%v)", u.expandAn.barP, u.expandAn.msgP)
	}
}

// TestCollapseTimeline 收起 540ms 严格先后（D54）：消息区 320ms 先退，输入栏 220ms 后缩。
func TestCollapseTimeline(t *testing.T) {
	u := &UI{collapsed: false}
	t0 := startAt(u, false)
	u.expandAn.advance(t0)
	if u.expandAn.barP != 1 || u.expandAn.msgP != 1 {
		t.Fatalf("起点进度应为 (1,1), 得 (%v,%v)", u.expandAn.barP, u.expandAn.msgP)
	}
	u.expandAn.advance(t0.Add(80 * time.Millisecond))
	if u.expandAn.msgP >= 1 || u.expandAn.msgP <= 0 {
		t.Fatalf("消息阶段中段 msgP=%v, want ∈ (0,1)", u.expandAn.msgP)
	}
	if u.expandAn.barP != 1 {
		t.Fatalf("严格先后：输入栏未开始 barP 应为 1, 得 %v", u.expandAn.barP)
	}
	u.expandAn.advance(t0.Add(collapseMsgMs * time.Millisecond))
	if u.expandAn.msgP != 0 || u.expandAn.barP != 1 || !u.expandAn.phase2 {
		t.Fatalf("320ms 处 msgP=%v barP=%v phase2=%v, want 0/1/true",
			u.expandAn.msgP, u.expandAn.barP, u.expandAn.phase2)
	}
	u.expandAn.advance(t0.Add((collapseMsgMs + collapseBarMs) * time.Millisecond))
	if u.expandAn.active {
		t.Fatal("540ms 应已收尾")
	}
	if u.expandAn.barP != 0 || u.expandAn.msgP != 0 {
		t.Fatalf("收尾进度应为 (0,0), 得 (%v,%v)", u.expandAn.barP, u.expandAn.msgP)
	}
}

// TestExpandReverseNoJump 中途反向（D54）：从当前进度续跑，进度连续不跳变；
// 无距离的阶段瞬时跳过（反向后不空转吃时长）。
func TestExpandReverseNoJump(t *testing.T) {
	u := &UI{collapsed: true}
	t0 := startAt(u, true)
	u.expandAn.advance(t0.Add(65 * time.Millisecond))
	mid := u.expandAn.barP
	if mid <= 0 || mid >= 1 {
		t.Fatalf("反向起点应取中间进度, 得 %v", mid)
	}

	// 展开到输入栏半程 → 反向收起：进度原样带过去（不跳变）。
	t1 := startAt(u, false)
	if u.expandAn.barP != mid {
		t.Fatalf("反向瞬间 barP 跳变: %v → %v", mid, u.expandAn.barP)
	}
	if !u.expandAn.active || u.expandAn.expand {
		t.Fatal("反向后应为收起方向的进行中动画")
	}
	// 消息阶段本来就没起步（msgP=0）→ 收起第一阶段无距离，瞬时跳进输入栏阶段。
	u.expandAn.advance(t1.Add(time.Millisecond))
	if !u.expandAn.phase2 {
		t.Fatal("消息阶段无距离应瞬时跳过")
	}
	if u.expandAn.barP != mid {
		t.Fatalf("跳过空阶段不应改输入栏进度: %v != %v", u.expandAn.barP, mid)
	}
	u.expandAn.advance(t1.Add(50 * time.Millisecond))
	if u.expandAn.barP >= mid {
		t.Fatalf("反向后 barP 应下降: %v >= %v", u.expandAn.barP, mid)
	}
	u.expandAn.advance(t1.Add((collapseMsgMs + collapseBarMs) * time.Millisecond))
	if u.expandAn.active || u.expandAn.barP != 0 || u.expandAn.msgP != 0 {
		t.Fatalf("反向收起应完成: active=%v (%v,%v)",
			u.expandAn.active, u.expandAn.barP, u.expandAn.msgP)
	}

	// 另一半：展开到消息半程 → 反向收起（barP 已是 1，输入栏阶段无距离）。
	u2 := &UI{collapsed: true}
	s0 := startAt(u2, true)
	u2.expandAn.advance(s0.Add((expandBarMs + 110) * time.Millisecond))
	if u2.expandAn.barP != 1 || u2.expandAn.msgP <= 0 || u2.expandAn.msgP >= 1 {
		t.Fatalf("前置态应为 barP=1、消息半程, 得 (%v,%v)",
			u2.expandAn.barP, u2.expandAn.msgP)
	}
	msgMid := u2.expandAn.msgP
	t2 := startAt(u2, false)
	if u2.expandAn.barP != 1 || u2.expandAn.msgP != msgMid {
		t.Fatalf("反向瞬间进度跳变: (%v,%v), want (1,%v)",
			u2.expandAn.barP, u2.expandAn.msgP, msgMid)
	}
	u2.expandAn.advance(t2.Add(collapseMsgMs * time.Millisecond))
	if u2.expandAn.msgP != 0 || u2.expandAn.barP != 1 || !u2.expandAn.phase2 {
		t.Fatalf("320ms 处消息应退尽: msgP=%v barP=%v phase2=%v",
			u2.expandAn.msgP, u2.expandAn.barP, u2.expandAn.phase2)
	}
	u2.expandAn.advance(t2.Add((collapseMsgMs + collapseBarMs) * time.Millisecond))
	if u2.expandAn.active || u2.expandAn.barP != 0 {
		t.Fatalf("反向收起应完成: active=%v barP=%v", u2.expandAn.active, u2.expandAn.barP)
	}
}

// TestExpandProgressRest 静止态进度由 collapsed 推导（直接翻 collapsed 的调用方不必
// 手工同步 barP/msgP）；动画中取现算值。
func TestExpandProgressRest(t *testing.T) {
	u := &UI{}
	if b, m := u.expandProgress(); b != 1 || m != 1 {
		t.Fatalf("展开静止态应为 (1,1), 得 (%v,%v)", b, m)
	}
	u.collapsed = true
	if b, m := u.expandProgress(); b != 0 || m != 0 {
		t.Fatalf("收起静止态应为 (0,0), 得 (%v,%v)", b, m)
	}
	t0 := startAt(u, true)
	u.expandAn.advance(t0.Add(40 * time.Millisecond))
	b, _ := u.expandProgress()
	if b <= 0 || b >= 1 {
		t.Fatalf("动画中应取现算进度, 得 %v", b)
	}
}

// TestBeginExpandCollapseFlips 静止态启停（headless 起落 + 逻辑态翻转）：
// 起点进度必须在翻 collapsed **之前**读到（否则被读成终态、动画零距离瞬时完成）。
func TestBeginExpandCollapseFlips(t *testing.T) {
	u := &UI{collapsed: true}
	u.beginExpand()
	if u.collapsed {
		t.Fatal("beginExpand 应展开")
	}
	if !u.expandAn.active || !u.expandAn.expand {
		t.Fatalf("应起展开动画: active=%v expand=%v", u.expandAn.active, u.expandAn.expand)
	}
	if u.expandAn.barP != 0 || u.expandAn.msgP != 0 {
		t.Fatalf("起点进度应为 (0,0), 得 (%v,%v)", u.expandAn.barP, u.expandAn.msgP)
	}
	if u.expandAn.stop != nil {
		t.Fatal("headless 不应起 ticker")
	}
	if !u.focusPending {
		t.Fatal("展开应置焦点入栏")
	}
	// 跑完 → 静止展开态。
	u.stepExpand(u.expandAn.start.Add((expandBarMs + expandMsgMs) * time.Millisecond))
	if u.expandAn.active {
		t.Fatal("stepExpand 收尾应结束动画")
	}
	if b, m := u.expandProgress(); b != 1 || m != 1 {
		t.Fatalf("静止展开态应为 (1,1), 得 (%v,%v)", b, m)
	}

	u.focusPending = false
	u.beginCollapse()
	if !u.collapsed {
		t.Fatal("beginCollapse 应收起")
	}
	if !u.expandAn.active || u.expandAn.expand {
		t.Fatalf("应起收起动画: active=%v expand=%v", u.expandAn.active, u.expandAn.expand)
	}
	if u.expandAn.barP != 1 || u.expandAn.msgP != 1 {
		t.Fatalf("起点进度应为 (1,1), 得 (%v,%v)", u.expandAn.barP, u.expandAn.msgP)
	}
	if u.focusPending {
		t.Fatal("收起不应置焦点")
	}

	// 已收起 → 再收起是空操作（不误起动画）。
	u.expandAn.advance(u.expandAn.start.Add((collapseMsgMs + collapseBarMs) * time.Millisecond))
	u.beginCollapse()
	if u.expandAn.active {
		t.Fatal("静止收起态调 beginCollapse 不应起动画")
	}
}

// TestToggleExpandReverses 单击 logo = 互切；动画中则反向（D54）。
func TestToggleExpandReverses(t *testing.T) {
	u := &UI{collapsed: true}
	u.toggleExpand() // 静止收起 → 展开
	if u.collapsed || !u.expandAn.active || !u.expandAn.expand {
		t.Fatalf("静止态 toggle 应展开: collapsed=%v active=%v expand=%v",
			u.collapsed, u.expandAn.active, u.expandAn.expand)
	}
	u.toggleExpand() // 展开中 → 反向收起
	if !u.collapsed || !u.expandAn.active || u.expandAn.expand {
		t.Fatalf("动画中 toggle 应反向收起: collapsed=%v active=%v expand=%v",
			u.collapsed, u.expandAn.active, u.expandAn.expand)
	}
	u.toggleExpand() // 收起中 → 反向展开
	if u.collapsed || !u.expandAn.active || !u.expandAn.expand {
		t.Fatalf("动画中 toggle 应反向展开: collapsed=%v active=%v expand=%v",
			u.collapsed, u.expandAn.active, u.expandAn.expand)
	}
}

// TestExpandMsgsDriveAnim 呼出/互切消息（§15.1/D54）驱动动画：collapsed 仍即时翻转
// （逻辑态），几何由 expandAn 从当前进度续跑；headless 不起 ticker。
// drainSync 给 happens-before 后事件循环空转，测试可直接推进度再投下一条消息。
func TestExpandMsgsDriveAnim(t *testing.T) {
	u := newHeadless(t, Options{})

	u.collapsed = true
	u.post(showExpandMsg{})
	drainSync(t, u)
	if u.collapsed || !u.focusPending {
		t.Fatalf("showExpand 应展开并置焦点: collapsed=%v focusPending=%v",
			u.collapsed, u.focusPending)
	}
	if !u.expandAn.active || !u.expandAn.expand {
		t.Fatalf("showExpand 应起展开动画: active=%v expand=%v",
			u.expandAn.active, u.expandAn.expand)
	}
	if u.expandAn.stop != nil {
		t.Fatal("headless 不应起唤帧 ticker（D54）")
	}

	// 推到输入栏半程，再互切 → 反向收起且进度原样带过去（不跳变）。
	u.stepExpand(u.expandAn.start.Add(100 * time.Millisecond))
	mid := u.expandAn.barP
	if mid <= 0 || mid >= 1 {
		t.Fatalf("推进后的 barP 应在 (0,1), 得 %v", mid)
	}
	u.post(toggleExpandMsg{})
	drainSync(t, u)
	if !u.collapsed || !u.expandAn.active || u.expandAn.expand {
		t.Fatalf("展开中 toggle 应反向收起: collapsed=%v active=%v expand=%v",
			u.collapsed, u.expandAn.active, u.expandAn.expand)
	}
	if u.expandAn.barP != mid {
		t.Fatalf("反向瞬间 barP 跳变: %v → %v", mid, u.expandAn.barP)
	}

	// 跑完收起 → 静止收起态（collapsed 与进度一致）。消息阶段无距离（还没起步）先被
	// 瞬时跳过，再按计划时长跑完输入栏阶段。
	u.stepExpand(u.expandAn.start.Add(time.Millisecond))
	if !u.expandAn.phase2 {
		t.Fatal("消息阶段无距离应瞬时跳过")
	}
	u.stepExpand(u.expandAn.start.Add((collapseMsgMs + collapseBarMs) * time.Millisecond))
	if u.expandAn.active {
		t.Fatal("收起动画应跑完")
	}
	if b, m := u.expandProgress(); b != 0 || m != 0 || !u.collapsed {
		t.Fatalf("静止收起态应为 collapsed + (0,0): collapsed=%v (%v,%v)",
			u.collapsed, b, m)
	}

	// 静止收起态再 toggle → 起展开动画、置焦点。
	u.focusPending = false
	u.post(toggleExpandMsg{})
	drainSync(t, u)
	if u.collapsed || !u.focusPending || !u.expandAn.active || !u.expandAn.expand {
		t.Fatalf("收起态 toggle 应展开并置焦点: collapsed=%v focusPending=%v active=%v expand=%v",
			u.collapsed, u.focusPending, u.expandAn.active, u.expandAn.expand)
	}
}

// TestClampedBand 淡出带夹取（D54）：带范围夹进当前窗口，绝不越窗/倒置。
func TestClampedBand(t *testing.T) {
	u := &UI{frameSize: image.Pt(100, 300)}
	u.bandTop, u.bandBottom = 10, 66
	if top, bottom := u.clampedBand(); top != 10 || bottom != 66 {
		t.Fatalf("正常带应原样: (%d,%d), want (10,66)", top, bottom)
	}
	u.bandTop, u.bandBottom = -5, 400
	if top, bottom := u.clampedBand(); top != 0 || bottom != 300 {
		t.Fatalf("越界带应夹到窗口: (%d,%d), want (0,300)", top, bottom)
	}
	u.bandTop, u.bandBottom = 120, 60 // 倒置
	if top, bottom := u.clampedBand(); top != 120 || bottom != 120 {
		t.Fatalf("倒置带应夹平: (%d,%d), want (120,120)", top, bottom)
	}
}

// TestStepExpandStopsTicker 动画收尾关停唤帧循环（channel 关闭、句柄清空、幂等）。
func TestStepExpandStopsTicker(t *testing.T) {
	u := &UI{collapsed: true}
	startAt(u, true)
	ch := make(chan struct{})
	u.expandAn.stop = ch
	u.stepExpand(u.expandAn.start.Add((expandBarMs + expandMsgMs) * time.Millisecond))
	if u.expandAn.active {
		t.Fatal("应已收尾")
	}
	select {
	case <-ch:
	default:
		t.Fatal("收尾应关闭唤帧 channel")
	}
	if u.expandAn.stop != nil {
		t.Fatal("收尾应清空句柄")
	}
	u.stepExpand(time.Now()) // 幂等：无动画直接返回
}

// TestRecordDynamicBand record 不随带底剔除（D62 修订 D54 口径）：带渐变改为全帧合成的
// 每像素因子 g(y)（fadeFrame），取代旧「带内整行单绘 + record 剔除带内元素」——带内/
// 跨带元素一律登记，带底动画位不再影响登记。
func TestRecordDynamicBand(t *testing.T) {
	u := newShapeUI(1)
	viewport := image.Rect(0, 0, 100, 300)
	u.bandBottom = 200 // 动画中：带底已下移到 200
	u.record(image.Rect(10, 0, 90, 199), 12, brandColor, viewport)
	if len(u.shapes) != 1 {
		t.Fatalf("带内矩形应登记（D62：带渐变按像素作用）: %+v", u.shapes)
	}
	u.record(image.Rect(10, 0, 90, 201), 12, brandColor, viewport)
	if len(u.shapes) != 2 {
		t.Fatalf("跨带底矩形应登记: %+v", u.shapes)
	}
	// 带底 0 = 静息顶带口径，登记行为一致。
	u.shapes = u.shapes[:0]
	u.bandBottom = 0
	u.record(image.Rect(10, 0, 90, 50), 12, brandColor, viewport)
	if len(u.shapes) != 1 {
		t.Fatalf("静息带内元素应登记: %+v", u.shapes)
	}
}
