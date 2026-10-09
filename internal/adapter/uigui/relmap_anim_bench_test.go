package uigui

// relmap_anim_bench_test.go 全程物理基准（D124）：200 节点下一帧全链路耗时（物理步进 +
// 全量绘制），对照 60fps 预算（16.67ms）。**实测（Ryzen 7 8700F, 900×700, PxPerDp=1.25）**：
//
//	物理仿真帧（6 子步 + 全量绘制） ≈ 0.88 ms/帧（预算 ~5%，裕量 ~19×）
//	拖拽帧（带 pin 同上）          ≈ 0.88 ms/帧
//
// ⇒ 200 节点（≤ hardCap 240）**全程物理 + 用户拖拽**稳定 60fps。注意：`-benchtime 3x`
// 这类小采样会被冷启动污染，请用 ≥1s。
// 运行：go test ./internal/adapter/uigui/ -run '^$' -bench 'PhysFrame|DragFrame|PhysStep' -benchtime 1s

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const animBenchN = 200

// animBenchGraph 200 节点链（snippet 24 字符级，贴近真实摘要成本）。
func animBenchGraph(n int) port.TreeGraph {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := make([]port.GraphNode, n)
	for i := 0; i < n; i++ {
		role := conversation.RoleAssistant
		if i%2 == 1 {
			role = conversation.RoleUser
		}
		if i == 0 {
			role = conversation.RoleRoot
		}
		parent := ""
		if i > 0 {
			parent = "n" + itoa(i-1)
		}
		nodes[i] = port.GraphNode{
			ID: conversation.MessageID("n" + itoa(i)), Parent: conversation.MessageID(parent),
			Role: role, Snippet: "这条消息的摘要文字大约二十四个字符左右",
			CreatedAt: base.Add(time.Duration(i) * time.Second), EdgeKind: port.EdgeSeq,
		}
	}
	return port.TreeGraph{Anchor: conversation.MessageID("n" + itoa(n-1)), Nodes: nodes, Total: n}
}

// animBench 基准夹具：200 节点、预算全开、离屏窗 900×700。
func animBench(b *testing.B) (*UI, *historyState, *input.Router, *headless.Window) {
	b.Helper()
	applyPalette(lightPalette())
	u := newHistUI(fakeRelTree{g: animBenchGraph(animBenchN)}, fakeLister(nil))
	st := newHistoryState(u)
	st.expandAll() // 预算 = 200（≤ hardCap 240）
	q := new(input.Router)
	w, err := headless.NewWindow(900, 700)
	if err != nil {
		b.Skipf("离屏渲染不可用: %v", err)
	}
	b.Cleanup(w.Release)
	return u, st, q, w
}

// renderFrame 一帧：frame() + router 提交 + 离屏渲染（真链路的 GPU 光栅化成本在内）。
func renderFrame(b *testing.B, q *input.Router, u *UI, st *historyState, w *headless.Window) {
	b.Helper()
	var ops op.Ops
	gtx := layout.Context{
		Source: q.Source(), Ops: &ops,
		Metric:      unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25},
		Constraints: layout.Exact(image.Pt(900, 700)),
	}
	st.graph.frame(gtx, u.th, u, st)
	q.Frame(&ops)
	if err := w.Frame(&ops); err != nil {
		b.Fatalf("渲染失败: %v", err)
	}
}

// BenchmarkRelmapPhysFrame200 物理仿真帧（200 节点：物理步进 + 全量绘制）。每轮注入
// 一点速度使仿真保持运动（模拟持续扰动/展开），测一帧全链路成本。
func BenchmarkRelmapPhysFrame200(b *testing.B) {
	u, st, q, w := animBench(b)
	g := st.graph
	// 先跑到静止（基准测稳态物理帧）。
	for i := 0; i < relSettleFrames*4 && g.lay != nil && !g.lay.settled; i++ {
		renderFrame(b, q, u, st, w)
	}
	for i := 0; i < 10; i++ { // 预热（字形/GPU 缓存上手）
		renderFrame(b, q, u, st, w)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if g.lay != nil { // 注入扰动，保持物理在跑
			for k := range g.lay.vx {
				g.lay.vx[k], g.lay.vy[k] = 3, 1
			}
			g.lay.settled = false
			g.relaxLeft = relSettleFrames
		}
		renderFrame(b, q, u, st, w)
	}
}

// BenchmarkRelmapDragFrame200 拖拽帧（带 pin 的物理步进 + 全量绘制）。
func BenchmarkRelmapDragFrame200(b *testing.B) {
	u, st, q, w := animBench(b)
	g := st.graph
	// 跑完展开（基准测稳态拖拽帧）
	for i := 0; i < relSettleFrames*4 && g.lay != nil && !g.lay.settled; i++ {
		renderFrame(b, q, u, st, w)
	}
	sx, li := f32.Pt(0, 0), g.anchor
	if g.sol == nil || li < 0 || li >= len(g.sol.Pos) {
		b.Fatal("布局未就绪")
	}
	x, y := g.toScreen(g.sol.Pos[li])
	sx = f32.Pt(x, y)
	q.Queue(relPointer(pointer.Press, sx, true))
	renderFrame(b, q, u, st, w)
	to := f32.Pt(sx.X+60, sx.Y+30)
	q.Queue(relPointer(pointer.Move, to, true))
	renderFrame(b, q, u, st, w)
	if g.dragNode != li {
		b.Fatalf("未进入拖拽: %d", g.dragNode)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		renderFrame(b, q, u, st, w)
	}
}

// BenchmarkRelmapPhysStep200 纯物理步进（200 节点链，6 子步，不含绘制）：测物理核成本。
func BenchmarkRelmapPhysStep200(b *testing.B) {
	lay, ok := newRelLayout(animBenchGeo(animBenchN))
	if !ok {
		b.Fatal("布局构建失败")
	}
	lay.seedCollapsed()
	// 跑到静止（测稳态物理步进的成本）。
	for f := 0; f < relSettleFrames*4 && !lay.settled; f++ {
		lay.step(relPin{})
	}
	pin := relPin{Valid: true, Index: 7, Pos: relPoint{X: 40, Y: 30}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lay.step(pin)
	}
}

// animBenchGeo 200 节点链的布局投影（质点，HalfW/HalfH = 0）。
func animBenchGeo(n int) []relGeoNode {
	ns := make([]relGeoNode, n)
	for i := range ns {
		ns[i] = relGeoNode{Parent: i - 1, Level: i, Weight: 1}
	}
	return ns
}
