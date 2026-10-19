package uigui

// 历史窗去噪（D159）测试：
//   - 状态栏项集契约：不再有「展开全部」；控件文案与序锁死（绘制只按这份项集排）；
//   - −/＋ 仍调预算（上限 relHardCap）；
//   - 左栏收起/展开：点收起钮 → 当帧换窄轨、关系图栏宽拿到让出的宽度；点展开钮 → 还原；
//     收起态下会话行不再取事件（行不在屏上）。

import (
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/pointer"
)

// TestHistStatusBarCtlItems 状态栏控件项集契约（D159：删掉「展开全部」）。
// 会话在默认预算内本来就是全量，「展开全部」点了没有任何变化。
func TestHistStatusBarCtlItems(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, fakeLister(nil))
	st := newHistoryState(u)
	items := st.histStatusCtlItems()
	want := []string{"−", "＋", "缩放 100%", "装下整树", "回到初始态", "聚焦当前历史", "聚焦当前节点"}
	if len(items) != len(want) {
		t.Fatalf("状态栏控件 = %d 项, want %d", len(items), len(want))
	}
	for i, w := range want {
		if items[i].label != w {
			t.Fatalf("控件[%d] = %q, want %q", i, items[i].label, w)
		}
	}
	for _, it := range items {
		if it.label == "展开全部" {
			t.Fatal("状态栏不该再有「展开全部」")
		}
	}
	// 只读项（缩放）没有点击件；其余都有。
	if items[2].click != nil {
		t.Fatal("缩放百分比应是只读文字（无点击件）")
	}
	for i, it := range items {
		if i == 2 {
			continue
		}
		if it.click == nil {
			t.Fatalf("控件 %q 缺点击件", it.label)
		}
	}
	// 缩放文案随帧 state 现取。
	st.graph.zoom = 0.42
	if got := st.histStatusCtlItems()[2].label; got != "缩放 42%" {
		t.Fatalf("缩放文案 = %q, want 缩放 42%%", got)
	}
}

// TestHistLeftColumnCollapse 左栏收起/展开（D159）：点收起钮当帧换态；收起态的关系图栏宽
// 拿到让出的宽度；点展开钮还原。判据用**关系图栏宽**（g.viewW，帧首按 Constraints 刷新），
// 不是左栏自身的宽度——用户要的是「把宽度让给关系图」。
func TestHistLeftColumnCollapse(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	histFrameFor(q, u, st)
	expandedW := st.graph.viewW
	if expandedW <= 0 {
		t.Fatalf("首帧关系图栏宽 = %.0f（应为正）", expandedW)
	}

	// 点收起钮：钮在左栏标题行右端——坐标经扫描得到（不硬编码排版常数）。
	x, y := histRailToggleXY(t, q, u, st)
	q.Queue(ovPointer(pointer.Move, x, y))
	histFrameFor(q, u, st)
	ovClick(q, x, y)
	histFrameFor(q, u, st)
	if !st.leftCollapsed {
		t.Fatal("点收起钮后左栏未收起")
	}
	collapsedW := st.graph.viewW
	wantGain := float64(histLeftColumnDp - histLeftRailDp) // @1x（frameGtxSize 的 Metric）
	if collapsedW-expandedW < wantGain-2 {
		t.Fatalf("收起后关系图栏宽 %0.f，想让出 ≈%.0f（收起前 %.0f）", collapsedW, wantGain, expandedW)
	}

	// 收起态：会话行不在屏上，点左栏空白不投任何命令。
	ovClick(q, 12, 400)
	histFrameFor(q, u, st)
	select {
	case in := <-u.inCh:
		t.Fatalf("收起态点左栏投出了命令：%+v", in)
	default:
	}

	// 点展开钮还原：钮在窄轨顶部居中。
	x2, y2 := histRailToggleXY(t, q, u, st)
	q.Queue(ovPointer(pointer.Move, x2, y2))
	histFrameFor(q, u, st)
	ovClick(q, x2, y2)
	histFrameFor(q, u, st)
	if st.leftCollapsed {
		t.Fatal("点展开钮后左栏未还原")
	}
	if got := st.graph.viewW; got > expandedW+2 {
		t.Fatalf("还原后关系图栏宽 %.0f，不应比收起前 %.0f 还宽", got, expandedW)
	}
}

// histRailToggleXY 扫出收起/展开钮在窗口里的坐标（判据 = 该点击件的 Hovered：真指针驱动，
// 不硬编码排版常数）。两态钮位置不同（展开态在左栏标题行右端、收起态在窄轨顶部），故扫描
// 覆盖窗口左上角一片区域。
func histRailToggleXY(t *testing.T, q *input.Router, u *UI, st *historyState) (int, int) {
	t.Helper()
	for y := 1; y < 60; y++ {
		for x := 1; x < 200; x++ {
			q.Queue(ovPointer(pointer.Move, x, y))
			histFrameFor(q, u, st)
			if st.btnLeft.Hovered() {
				return x, y
			}
		}
	}
	t.Fatalf("扫不到左栏收起/展开钮（收起态=%v）", st.leftCollapsed)
	return 0, 0
}
