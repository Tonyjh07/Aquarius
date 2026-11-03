package uigui

// relmap_edit.go 树编辑模式下的**拖动改挂**（D257/§5.6.3）。
//
// 分工：UI 只做**预演**——算「离哪个节点最近」、画虚线、画环，决定「这一次松手算不算改挂」
// 好看不好看；结构**一律交内核**（投 `/reparent <id> <新父>`，命令面在会话内只改内存）。
// UI 侧对合法性的判断（非自身 / 非自身后代 / 非根）是**双保险**里的第一道：让虚线只出现在
// 真能挂上去的地方，用户不必「试了才知道不行」。内核再判一次，两边不一致时以内核为准。
//
// 手势沿用 D121：拖节点跟手、邻居被弹簧带着走、拖动中不改视场。本文件只在「已经握住一个
// 节点」之后叠加编辑语义——没开编辑模式时一切照旧（拖节点 = 摆位置）。

import (
	"image"
	"math"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 拖动改挂的档位：落点半径、淡出与呼吸时长、环宽、虚线步长（都是可调的呈现常数，
// 不影响任何语义——把它们当成「手感旋钮」，改这些不需要改契约）。
const (
	relDropReachDp  = 48.0 // 落点判定半径（dp，屏幕口径——手势容差跟手不跟 zoom）
	relDropFadeMs   = 120  // 旧父边淡出时长（拖起）
	relDropPulseMs  = 600  // 目标环呼吸周期
	relDropFlashMs  = 400  // 改挂成功后目标环的高亮时长
	relDropRingDp   = 2.0  // 目标环 / 提环线宽
	relDropDashOn   = 4.0  // 预览虚线段长（dp）
	relDropDashOff  = 4.0  // 预览虚线间隔（dp）
	relDropPulseMin = 0.6  // 目标环呼吸的最低 alpha
)

// dropFlash 改挂成功后目标环的高亮（400ms 淡出）：让「挂到了哪儿」有一处可见的回执——
// 结构一变，物理重排就把画面搅动了，没有这圈高亮，用户分不清是挂上了还是滑走了。
type dropFlash struct {
	id    conversation.MessageID
	start time.Time
	until time.Time
}

// nodeID 本地索引 → 消息 ID（越界/模型未就绪 = 空串）。
func (g *relGraph) nodeID(li int) conversation.MessageID {
	if g.model == nil || li < 0 || li >= len(g.vis.nodes) {
		return ""
	}
	gi := g.vis.nodes[li]
	if gi < 0 || gi >= len(g.model.nodes) {
		return ""
	}
	return g.model.nodes[gi].id
}

// localOf 消息 ID → 本地索引（不在可见集 = -1）。
func (g *relGraph) localOf(id conversation.MessageID) int {
	if g.model == nil || id == "" {
		return -1
	}
	gi, ok := g.model.index[id]
	if !ok {
		return -1
	}
	for li, v := range g.vis.nodes {
		if v == gi {
			return li
		}
	}
	return -1
}

// isStrictAncestor anc 是 node 的严格祖先（沿结构父上溯；跨父之后版本链不参与归属）。
func (g *relGraph) isStrictAncestor(anc, node int) bool {
	if g.model == nil || anc < 0 || node < 0 {
		return false
	}
	ai, ni := g.vis.nodes[anc], g.vis.nodes[node]
	seen := 0
	for p := g.model.nodes[ni].parent; p >= 0 && seen <= len(g.model.nodes); p = g.model.nodes[p].parent {
		if p == ai {
			return true
		}
		seen++
	}
	return false
}

// relBoxGap 两个矩形之间的最近距离（相交/相贴记 0）。
//
// 落点判定按**卡片到卡片**算，不按指针到卡片：卡片是长条形（徽标 + 标签），指针在卡片
// 一角时「离得挺近」，而真正要判的是「这两张卡挨上了没有」——设计稿 §5.6.3 的口径。
func relBoxGap(a, b image.Rectangle) float32 {
	dx := float32(0)
	if a.Max.X < b.Min.X {
		dx = float32(b.Min.X - a.Max.X)
	} else if b.Max.X < a.Min.X {
		dx = float32(a.Min.X - b.Max.X)
	}
	dy := float32(0)
	if a.Max.Y < b.Min.Y {
		dy = float32(b.Min.Y - a.Max.Y)
	} else if b.Max.Y < a.Min.Y {
		dy = float32(a.Min.Y - b.Max.Y)
	}
	return float32(math.Hypot(float64(dx), float64(dy)))
}

// dropCandidate 落点预演（§5.6.3）：在可见节点里找**最近的唯一一个**合法目标。
//
// 合法 = 非自身、非自身严格后代、非结构根；判据 = 卡片矩形最近边距离 ≤ 48dp。返回
// (目标, 被拦下的后代)：后者非 -1 表示「手指压在自己的后代上」——松手时给一句解释，
// 其余情况（空白、太远、根上）一律不解释，回弹即走「只改位置」那条路。
func (g *relGraph) dropCandidate() (int, int) {
	if g.model == nil || g.dragNode < 0 || g.sol == nil {
		return -1, -1
	}
	if g.dragNode >= len(g.sol.Pos) {
		return -1, -1
	}
	dx, dy := g.toScreen(g.sol.Pos[g.dragNode])
	mine := g.nodeCardBox(g.vis.nodes[g.dragNode], dx, dy)
	reach := float32(relDropReachDp * g.dpx)
	best, bestD := -1, float32(math.MaxFloat32)
	blocked := -1
	for li := range g.vis.nodes {
		if li == g.dragNode || li >= len(g.sol.Pos) {
			continue
		}
		if g.isRoot(li) {
			continue // 根不能当父（改父会造出第二棵树）
		}
		xs, ys := g.toScreen(g.sol.Pos[li])
		d := relBoxGap(mine, g.nodeCardBox(g.vis.nodes[li], xs, ys))
		if g.isStrictAncestor(g.dragNode, li) {
			if d <= reach && blocked < 0 {
				blocked = li // 自己的后代：留一句解释，但绝不作为落点
			}
			continue
		}
		if d <= reach && d < bestD {
			best, bestD = li, d
		}
	}
	return best, blocked
}

// beginDropFade 拖起：旧父边 120ms 淡出（§5.6.3）。淡的是「这条边走不通了」这件事，不是
// 节点——节点自己还在手上，它的子树照旧被弹簧带着。
func (g *relGraph) beginDropFade(now time.Time) {
	g.dragFade.retarget(now, 0, relDropFadeMs, relEaseOutCubic)
}

// resetDrop 松手/取消：撤掉预演状态，旧父边恢复（改挂成功时新形状由内核快照重排接上）。
func (g *relGraph) resetDrop() {
	g.dropTarget = -1
	g.dragFade.cancel()
	g.dragFade.from, g.dragFade.to, g.dragFade.v = 1, 1, 1
}

// endNodeDrag 松手收尾（编辑态）：有合法目标 → 投 `/reparent`；否则 = 只改位置（既有槽位）。
// 返回 true 表示这次松手被当成「改挂」处理了（调用方不再走普通固化槽位那条路）。
func (g *relGraph) endNodeDrag(u *UI, st *historyState, now time.Time) bool {
	if g.dragNode < 0 || !st.teOn {
		return false
	}
	id := g.nodeID(g.dragNode)
	target, blocked := g.dropCandidate()
	if id == "" {
		g.resetDrop()
		return false
	}
	if target < 0 {
		// 压在自己后代上才解释一句（§5.6.3）；其它情况静默——「拖了但没靠近谁」就是换位置。
		if blocked >= 0 {
			st.toast.show(i18n.T("ui.relmap.toast.not_descendant"), now)
		}
		g.resetDrop()
		return false
	}
	tid := g.nodeID(target)
	if tid == "" {
		g.resetDrop()
		return false
	}
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{
		Name: "reparent", Args: []string{string(id), string(tid)},
	}}:
	default:
		// 通道满：这次改挂丢掉，但要留回执——否则用户以为挂上了，树却没动。
		st.toast.show(i18n.T("ui.relmap.toast.channel_busy"), now)
		g.resetDrop()
		return false
	}
	g.flash.start = now
	g.flash.until = now.Add(relDropFlashMs * time.Millisecond)
	g.flash.id = tid
	g.resetDrop()
	g.dragNode = -1 // 撤 pin：结构要变了，位置交给重排（不做槽位固化——新父下按新形状收敛）
	return true
}

// drawEditOverlay 编辑态的叠层：图区胶囊（§5.6.1）+ 拖动叠层（§5.6.3：被拖卡片的提环 +
// 目标环（呼吸）+ 一条虚线预览）+ 改挂成功后的 400ms 目标环。
func (g *relGraph) drawEditOverlay(gtx layout.Context, th *material.Theme) {
	now := gtx.Now
	g.drawEditCapsule(gtx, th)
	if g.dropFlashActive(now) {
		if li := g.localOf(g.flash.id); li >= 0 {
			p := g.flashProgress(now)
			g.dropRing(gtx, li, 0.35+0.65*p) // 400ms 内由亮到灭
		}
	}
	if g.dragNode < 0 {
		return
	}
	// 被拖卡片提环：告诉用户「这一刻手里拿着的是它」，线宽 2dp、恒定亮度。
	g.dropRing(gtx, g.dragNode, 1)
	if g.dropTarget < 0 {
		return
	}
	// 目标环：0.6 ↔ 1.0 呼吸（600ms 周期）——静态环会被读成「选中」，「会呼吸」才读成
	// 「松手就挂这儿」。
	period := relDropPulseMs * time.Millisecond
	phase := float64(now.UnixNano()%int64(period)) / float64(period)
	pulse := relDropPulseMin + (1-relDropPulseMin)*(0.5+0.5*math.Sin(2*math.Pi*phase))
	g.dropRing(gtx, g.dropTarget, pulse)
	g.dropDash(gtx)
}

// drawEditCapsule 图区左上角的模式胶囊（§5.6.1）：「编辑树 · 拖动节点改挂 · 已改 N 处」。
//
// 光晕只说明「模式开了」，胶囊说明「拖了会发生什么」——两者不可互相替代，所以这条不随
// 拖动状态出现或消失（那会让说明在最需要的时候缺席）。
func (g *relGraph) drawEditCapsule(gtx layout.Context, th *material.Theme) {
	text := relEditCapsuleText(g.editCount)
	sp := float64(paperMicroSp(th))
	padX, h := gtx.Dp(unit.Dp(10)), gtx.Dp(unit.Dp(22))
	w := int(relEstTextW(text, sp)) + 2*padX
	// 标签的实际排版宽由字体决定，估宽偏小会让文字贴边——两端各留 2dp 余量。
	w += gtx.Dp(unit.Dp(4))
	off := op.Offset(image.Pt(gtx.Dp(unit.Dp(12)), gtx.Dp(unit.Dp(12)))).Push(gtx.Ops)
	defer off.Pop()
	box := image.Rect(0, 0, w, h)
	pp := paperOf()
	paint.FillShape(gtx.Ops, pp.card, clip.UniformRRect(box, h/2).Op(gtx.Ops))
	edge := pp.accent
	edge.A = 0x80
	paint.FillShape(gtx.Ops, edge,
		clip.Stroke{Path: clip.UniformRRect(box, h/2).Path(gtx.Ops), Width: float32(gtx.Dp(unit.Dp(1)))}.Op())
	st := op.Offset(image.Pt(padX, h/2-int(sp*float64(gtx.Metric.PxPerSp))/2)).Push(gtx.Ops)
	gtx.Constraints = layout.Exact(image.Pt(w, h))
	l := material.Label(th, unit.Sp(float32(sp)), text)
	l.Color = pp.accent
	l.MaxLines = 1
	l.Layout(gtx)
	st.Pop()
}

// relEditCapsuleText 胶囊文案（纯函数，可测）：没改过时不提处数——「已改 0 处」是噪音。
func relEditCapsuleText(edits int) string {
	if edits <= 0 {
		return i18n.T("ui.relmap.edit_capsule")
	}
	return i18n.Tf("ui.relmap.edit_capsule_count", "n", edits)
}

// dropRing 围绕某节点的卡片盒画一圈 2dp 环（alpha 由调用方给）。
func (g *relGraph) dropRing(gtx layout.Context, li int, alpha float64) {
	if li < 0 || li >= len(g.vis.nodes) || li >= len(g.sol.Pos) {
		return
	}
	xs, ys := g.toScreen(g.sol.Pos[li])
	box := g.nodeCardBox(g.vis.nodes[li], xs, ys)
	box = box.Inset(-gtx.Dp(unit.Dp(relDropRingDp)))
	c := paperOf().accent
	c.A = uint8(math.Max(0, math.Min(1, alpha))*255 + 0.5)
	w := float32(gtx.Dp(unit.Dp(relDropRingDp)))
	r := g.nodeRadius(&g.model.nodes[g.vis.nodes[li]]) * g.z
	ring := clip.UniformRRect(box, int(relCardRoundP*r)).Path(gtx.Ops)
	paint.FillShape(gtx.Ops, c, clip.Stroke{Path: ring, Width: w}.Op())
}

// dropDash 被拖卡片 → 目标卡片的一条 4/4dp 虚线（只此一条：候选一多，用户要在几条线之间
// 比较，反而更慢——§5.6.3 明确不做多目标候选）。
func (g *relGraph) dropDash(gtx layout.Context) {
	if g.dragNode < 0 || g.dropTarget < 0 {
		return
	}
	if g.dragNode >= len(g.sol.Pos) || g.dropTarget >= len(g.sol.Pos) {
		return
	}
	ax, ay := g.toScreen(g.sol.Pos[g.dragNode])
	bx, by := g.toScreen(g.sol.Pos[g.dropTarget])
	// 从两张卡片的边缘起止：线画进卡片里会盖住文字。
	ab := g.nodeCardBox(g.vis.nodes[g.dragNode], ax, ay).Inset(-gtx.Dp(unit.Dp(relDropRingDp)))
	bb := g.nodeCardBox(g.vis.nodes[g.dropTarget], bx, by).Inset(-gtx.Dp(unit.Dp(relDropRingDp)))
	ax, ay = relBoxEdgePoint(ab, bx, by)
	bx, by = relBoxEdgePoint(bb, ax, ay)
	step := float32(gtx.Dp(unit.Dp(relDropDashOn + relDropDashOff)))
	seg := float32(gtx.Dp(unit.Dp(relDropDashOn)))
	dist := float32(math.Hypot(float64(bx-ax), float64(by-ay)))
	if dist < 1 {
		return
	}
	ux, uy := (bx-ax)/dist, (by-ay)/dist
	c := paperOf().accent
	c.A = 0xE6
	w := float32(gtx.Dp(unit.Dp(relDropRingDp)))
	for s := float32(0); s < dist; s += step {
		e := s + seg
		if e > dist {
			e = dist
		}
		g.strokeLineW(gtx, ax+ux*s, ay+uy*s, ax+ux*e, ay+uy*e, c, w)
	}
}

// relBoxEdgePoint 从矩形中心朝 (x,y) 方向射出，落到矩形边上的点（虚线不压进卡片）。
// 点在盒内时退回盒心（两端重叠的退化情形交给调用方的最小长度判断）。
func relBoxEdgePoint(box image.Rectangle, x, y float32) (float32, float32) {
	cx := float32(box.Min.X+box.Max.X) / 2
	cy := float32(box.Min.Y+box.Max.Y) / 2
	dx, dy := x-cx, y-cy
	if dx == 0 && dy == 0 {
		return cx, cy
	}
	hw := float32(box.Dx()) / 2
	hh := float32(box.Dy()) / 2
	t := float32(math.MaxFloat32)
	if dx != 0 {
		t = float32(math.Min(float64(t), math.Abs(float64(hw/dx))))
	}
	if dy != 0 {
		t = float32(math.Min(float64(t), math.Abs(float64(hh/dy))))
	}
	return cx + dx*t, cy + dy*t
}

// flash 改挂成功后的目标高亮（见 dropFlash）。
func (f *dropFlash) active(now time.Time) bool { return f.id != "" && now.Before(f.until) }

func (g *relGraph) dropFlashActive(now time.Time) bool { return g.flash.active(now) }

// flashProgress 高亮进度：0 = 刚挂上（最亮）、1 = 到点（灭）。
func (g *relGraph) flashProgress(now time.Time) float64 {
	p := float64(now.Sub(g.flash.start)) / float64(relDropFlashMs*time.Millisecond)
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// fadeEdgeAlpha 旧父边的当前 alpha 倍数（未拖 / 非编辑态 = 1）。
func (g *relGraph) fadeEdgeAlpha(now time.Time) float64 {
	if !g.editOn || g.dragNode < 0 {
		return 1
	}
	return g.dragFade.at(now)
}
