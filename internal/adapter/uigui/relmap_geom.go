package uigui

// relmap_geom.go 右图几何与命中：卡片/标签盒、徽标盒、命中测试（含容差）。
// 盒定义与绘制共用同一份来源，避免「按 A 排、按 B 装、按 C 点」。

import (
	"image"
	"math"
)

// relCardHalf 布局输入里的卡片半宽/半高（dp）。视场适配与布局共用同一份占位，
// 不另算一套（否则「按 A 排、按 B 装」）。
func relCardHalf(gn relGeoNode) (float64, float64) {
	return gn.HalfW, gn.HalfH
}

// relBadgeBox 形状徽标盒（屏幕坐标）：节点直径 = weight × 基准（A4，与 degree 无关），
// 中心锚点即节点坐标——边连的也正是它。
func relBadgeBox(xs, ys, r float32) image.Rectangle {
	return image.Rect(int(xs-r), int(ys-r), int(xs+r), int(ys+r))
}

// labelBlock 标签文本块的**像素**几何（nodeCardBox 与 drawNodeCard 共用一份）：宽 = 估宽
// （封顶 relLabelMaxDp × z）、行数 = ⌈估宽 ÷ 盒宽⌉（钳 [1, relLabelLines]）、块高 = 行数 ×
// 行高。行高 = labelFont × spx × relLabelLineLead——**与 drawNodeCard 里显式设置的
// LineHeight 同值**：估的和画的必须一致，否则卡片装不下自己的文字。
func (g *relGraph) labelBlock(txt string) (w, h float32, lines int) {
	est := relEstTextW(txt, float64(g.labelFont())) * g.spx
	max := float32(relLabelMaxDp * g.z)
	w = float32(est)
	if w > max {
		w = max
	}
	lines = int(math.Ceil(float64(est) / float64(max)))
	if lines < 1 {
		lines = 1
	}
	if lines > relLabelLines {
		lines = relLabelLines
	}
	return w, float32(lines) * g.labelLineH(), lines
}

// labelLineH 一行标签的像素高 = 字号 × PxPerSp × 行高系数（与显式 LineHeight 同值）。
func (g *relGraph) labelLineH() float32 {
	return g.labelFont() * float32(g.spx) * relLabelLineLead
}

// nodeCardBox 节点卡盒（屏幕坐标；命中判定与绘制**共用**同一几何）。
// 标签可见（A5 预算 + zoom 下限）时卡 = [徽标 | 标签]（向右展开），否则退化为裸形状盒。
// 卡高 = max(徽标高, 标签块高) + 上下内边距（D126：多行摘要把卡片撑高，徽标仍居中）。
func (g *relGraph) nodeCardBox(gi int, xs, ys float32) image.Rectangle {
	m := g.model.nodes[gi]
	r := float32(g.nodeRadius(&m) * g.z)
	badge := relBadgeBox(xs, ys, r)
	txt := relLabelText(g.model, gi, g.zoom)
	if txt == "" {
		return badge
	}
	lw, lh, _ := g.labelBlock(txt)
	gap := int(relCardGapDp * g.z)
	pad := int(relCardPadDp * g.z)
	pv := int(relCardPadV * g.z)
	half := lh / 2
	if h := float32(badge.Dy()) / 2; h > half {
		half = h // 徽标比标签块高时以徽标为准（粗节点）
	}
	return image.Rect(badge.Min.X-pad, int(ys-half)-pv,
		badge.Max.X+gap+int(lw)+pad, int(ys+half)+pv)
}

// hitTest 屏幕点 → 命中的可见节点（本地索引；-1 无）。按节点卡盒 + relHitSlop 外扩判定
// （卡含标签，故点文字也算点中这个节点）。
func (g *relGraph) hitTest(sx, sy float32) int {
	return g.hitTestSlop(sx, sy, relHitSlop)
}

// hitTestSlop 同 hitTest，外扩量可指定（抓手判定用更宽的 relGrabSlop，见 update）。
func (g *relGraph) hitTestSlop(sx, sy float32, slopDp float64) int {
	if g.sol == nil {
		return -1
	}
	slop := int(slopDp * g.z)
	for li, gi := range g.vis.nodes {
		xs, ys := g.toScreen(g.sol.Pos[li])
		box := g.nodeCardBox(gi, xs, ys).Inset(-slop)
		if image.Pt(int(sx), int(sy)).In(box) {
			return li
		}
	}
	return -1
}
