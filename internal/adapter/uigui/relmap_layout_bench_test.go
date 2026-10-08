package uigui

// 关系图布局求解器基准（D120④，relation-map-design §5.4.4）：50 / 200 / 500 节点实测，
// 用于定档 maxIter / hardCap / 是否需要斥力近似。基准为偶然实现层测量，不参与设计语言。
//
// 实测（Ryzen 7 8700F）：50 ≈ 0.034ms、200 ≈ 1.09ms、500 ≈ 178ms → hardCap 定 240。
// 运行：go test ./internal/adapter/uigui/ -run '^$' -bench RelSolve -benchtime 3x

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
