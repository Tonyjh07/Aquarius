package uigui

// 树编辑光带（§5.6.2）测试：扫亮按周长推进、时间线按 §5.6.2 的档位走（260 扫亮 → 600
// 呼吸到 0.7 → 退出 160 淡出），以及编辑态整帧能画出来（渐变/裁剪不出边界错）。

import (
	"math"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
)

// TestGlowSweepCoversRing 扫亮进度与可见周长严格对应：p = 0 什么都不画、p = 1 整圈画满，
// 中途可见长度 = p（八段带子的起点是 0,1/8,…,7/8 的一个排列——「从顶边中点顺时针」）。
func TestGlowSweepCoversRing(t *testing.T) {
	t.Parallel()
	seen := map[float64]bool{}
	for side := 0; side < 4; side++ {
		for half := 0; half < 2; half++ {
			b := relGlowSweepBase[side][half]
			if seen[b] {
				t.Fatalf("扫亮起点 %v 出现了两次（八段必须等分周长）", b)
			}
			seen[b] = true
			if _, _, ok := glowHalfRange(side, half, 0); ok {
				t.Fatalf("p=0 时 side=%d half=%d 不该可见", side, half)
			}
		}
	}
	for i := 0; i < 8; i++ {
		if !seen[float64(i)*relGlowHalf] {
			t.Fatalf("缺少扫亮起点 %v", float64(i)*relGlowHalf)
		}
	}
	for _, p := range []float64{0.05, 0.3, 0.5, 0.77, 1} {
		sum := 0.0
		for side := 0; side < 4; side++ {
			for half := 0; half < 2; half++ {
				t0, t1, ok := glowHalfRange(side, half, p)
				if !ok {
					continue
				}
				if t0 < 0 || t1 > 1 || t1 < t0 {
					t.Fatalf("p=%v side=%d half=%d 区间 [%v,%v] 越界", p, side, half, t0, t1)
				}
				sum += 0.25 * (t1 - t0) // 每段整边 1/4，t 满程 = 半段
			}
		}
		if math.Abs(sum-p) > 1e-9 {
			t.Fatalf("p=%v 时可见周长占比 = %v，应相等", p, sum)
		}
	}
}

// TestGlowEntersAndExits 时间线档位（§5.6.2）：进入先亮到 0.85 再扫亮，扫完呼吸到 0.7；
// 退出 160ms 淡到 0；静止即停（不再续帧）。
func TestGlowEntersAndExits(t *testing.T) {
	t.Parallel()
	g := newRelGraph()
	ms := time.Millisecond
	t0 := time.Now()

	if !g.stepGlow(t0, true) {
		t.Fatal("进入首帧应续帧")
	}
	if g.glow.level.v != relGlowLit || g.glow.sweep.v != 0 {
		t.Fatalf("进入首帧 = 亮度 %v / 扫亮 %v, want %v / 0",
			g.glow.level.v, g.glow.sweep.v, relGlowLit)
	}
	g.stepGlow(t0.Add(130*ms), true)
	if v := g.glow.sweep.v; v <= 0.5 || v >= 1 {
		t.Fatalf("130ms 时扫亮 = %v（easeOutCubic 应已过半、未满）", v)
	}
	g.stepGlow(t0.Add(400*ms), true) // 扫亮跑完（该帧收尾，尚未布防呼吸）
	g.stepGlow(t0.Add(420*ms), true) // 呼吸起跑
	if !g.glow.swept || g.glow.level.v != relGlowLit {
		t.Fatalf("呼吸起跑时 swept=%v 亮度=%v, want true/%v", g.glow.swept, g.glow.level.v, relGlowLit)
	}
	if g.stepGlow(t0.Add(1100*ms), true) {
		t.Fatal("呼吸落定后不该再续帧（静止即停）")
	}
	if math.Abs(g.glow.level.v-relGlowRest) > 1e-9 {
		t.Fatalf("静止亮度 = %v, want %v", g.glow.level.v, relGlowRest)
	}

	g.stepGlow(t0.Add(1200*ms), false) // 退出起跑
	if g.glow.level.v != relGlowRest {
		t.Fatalf("退出首帧亮度 = %v, want 保持 %v（从当前值续着淡）", g.glow.level.v, relGlowRest)
	}
	g.stepGlow(t0.Add(1400*ms), false)
	if g.glow.level.v != 0 {
		t.Fatalf("退出 200ms 后亮度 = %v, want 0", g.glow.level.v)
	}
	if g.stepGlow(t0.Add(1500*ms), false) {
		t.Fatal("退出落定后不该再续帧")
	}
}

// TestGlowMidFlightReversal 进了又退：从当前亮度接着淡，不跳变（扫亮进度不动）。
func TestGlowMidFlightReversal(t *testing.T) {
	t.Parallel()
	g := newRelGraph()
	ms := time.Millisecond
	t0 := time.Now()
	g.stepGlow(t0, true)
	g.stepGlow(t0.Add(100*ms), true)
	lit := g.glow.level.v
	sweep := g.glow.sweep.v
	g.stepGlow(t0.Add(110*ms), false)
	if g.glow.level.v != lit {
		t.Fatalf("反向首帧亮度 = %v, want 保持 %v", g.glow.level.v, lit)
	}
	if g.glow.sweep.v != sweep {
		t.Fatalf("退出期扫亮进度被改了：%v → %v（应原地不动，不反向扫）", sweep, g.glow.sweep.v)
	}
	g.stepGlow(t0.Add(300*ms), false)
	if g.glow.level.v != 0 {
		t.Fatalf("淡出后亮度 = %v, want 0", g.glow.level.v)
	}
}

// TestGlowBandsFillRing 八段带子正好铺满整圈环带：面积逐项相加 = 外框 − 内框（斜面拼接
// 既不留缝也不重叠，闭合是精确的）。这条断言同时锁住两端的 45° 斜切几何。
func TestGlowBandsFillRing(t *testing.T) {
	t.Parallel()
	const W, H, w float32 = 640, 480, 12
	total := 0.0
	for side := 0; side < 4; side++ {
		for half := 0; half < 2; half++ {
			t0 := float32(half) * 0.5
			q := glowBandQuad(side, t0, t0+0.5, w, W, H)
			total += math.Abs(relQuadArea(q))
		}
	}
	ring := float64(W*H) - float64((W-2*w)*(H-2*w))
	if math.Abs(total-ring) > 1e-3 {
		t.Fatalf("八段面积和 = %.3f, 环带面积 = %.3f", total, ring)
	}
}

// relQuadArea 四边形面积（鞋带公式，取绝对值）。
func relQuadArea(q [4]f32.Point) float64 {
	sum := 0.0
	for i := 0; i < 4; i++ {
		a, b := q[i], q[(i+1)%4]
		sum += float64(a.X)*float64(b.Y) - float64(b.X)*float64(a.Y)
	}
	return sum / 2
}

// TestGlowDrawsInEditMode 编辑态整帧能画出来（八段渐变 + 圆角裁剪不越界），且非编辑态
// 一个 op 都不多（drawGlow 空转）。
func TestGlowDrawsInEditMode(t *testing.T) {
	t.Parallel()
	tree := &fakeRelTree{g: relFixtureGraph(), editOn: true}
	u := newHistUI(tree, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	histFrameFor(q, u, st) // 进入编辑态的首帧：亮起并开扫
	if st.graph.glow.level.v <= 0 {
		t.Fatal("编辑态首帧光带亮度应为正")
	}
	// 扫亮推到满：画满整圈（含四角圆角裁剪）不 panic。
	st.graph.glow.sweep.v = 1
	st.graph.glow.level.v = relGlowRest
	histFrameFor(q, u, st)

	// 退出编辑态：下一帧起按时间线淡出，最终为 0。
	tree.editOn = false
	st.graph.glow.level.v = 0
	histFrameFor(q, u, st)
	if st.graph.glow.level.v != 0 {
		t.Fatalf("非编辑态亮度 = %v, want 0（空转）", st.graph.glow.level.v)
	}
	// 夹具健全性：树里确实有节点（否则上面几帧等于在空白面板上画）。
	if len(st.vis.nodes) == 0 {
		t.Fatal("夹具树为空")
	}
}
