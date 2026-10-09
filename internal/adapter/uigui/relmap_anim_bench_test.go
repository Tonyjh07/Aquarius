package uigui

// relmap_anim_bench_test.go 全程动画基准（D122 spike 固化）：200 节点下一帧全链路耗时，
// 对照 60fps 预算（16.67ms）。**spike 结论（Ryzen 7 8700F, 900×700, PxPerDp=1.25）**：
//
//	入场动画帧（lerp + 全量绘制） ≈ 0.52 ms/帧（预算的 ~3%，裕量 >30×；1s benchtime 稳定值）
//	拖拽帧（relax 32 步 + 绘制）  ≈ 0.49 ms/帧
//	一次性重排（relMaxIter）     ≈ 1.5 ms/次（每条消息一次，非逐帧）
//	纯 cascade lerp              ≈ <0.03 ms/帧
//
// ⇒ 200 节点（≤ hardCap 240）**全程动画 + 用户拖拽**稳定 60fps，无需任何近似/降采样。
// 注意：`-benchtime 3x` 这类小采样会被冷启动污染（曾现 5.7ms 假象），请用 ≥1s。
// 运行：go test ./internal/adapter/uigui/ -run '^$' -bench AnimFrame -benchtime 1s

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

// BenchmarkRelmapAnimFrame200 入场动画帧（cascade lerp + 全量绘制）。
func BenchmarkRelmapAnimFrame200(b *testing.B) {
	u, st, q, w := animBench(b)
	g := st.graph
	if !g.animOn {
		b.Fatal("应处于入场动画态")
	}
	// 预热（字形/GPU 缓存上手）
	for i := 0; i < 10; i++ {
		renderFrame(b, q, u, st, w)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		renderFrame(b, q, u, st, w)
	}
}

// BenchmarkRelmapDragFrame200 拖拽帧（relax(relFrameIter, pin) + 全量绘制）。
func BenchmarkRelmapDragFrame200(b *testing.B) {
	u, st, q, w := animBench(b)
	g := st.graph
	// 播完入场动画（基准测的是稳态拖拽帧）
	for i := 0; i < relEntryFrames+2 && g.animOn; i++ {
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
