package uigui

// 关系图布局基准（D124）：relSolve 现在求的是**确定性静息布局**（无物理迭代），
// 成本 ≈ O(n²)（斥力不参与，主要是扇区分配 + 树遍历）。物理逐帧成本见
// relmap_anim_bench_test.go（BenchmarkRelmapPhysFrame200 / DragFrame200）。
// 运行：go test ./internal/adapter/uigui/ -run '^$' -bench RelSolve -benchtime 1s

import "testing"

func benchRelSolve(b *testing.B, n int) {
	nodes := relRandTree(42, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sol, ok := relSolve(nodes)
		if !ok || !sol.Settled {
			b.Fatalf("未收敛: ok=%v sol=%+v", ok, sol)
		}
	}
}

func BenchmarkRelSolve50(b *testing.B)  { benchRelSolve(b, 50) }
func BenchmarkRelSolve200(b *testing.B) { benchRelSolve(b, 200) }
func BenchmarkRelSolve500(b *testing.B) { benchRelSolve(b, 500) }
