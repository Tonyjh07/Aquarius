package uigui

// 树编辑模式的界面语汇（§5.6.1/§5.6.5）：图区胶囊文案、编辑期菜单置灰、光标档位。
// 菜单项集与分发是契约，测试锁定；胶囊文案是纯函数；光标无法在离屏帧里断言（op 不可读），
// 靠 frame 的编辑态分支覆盖。

import (
	"testing"

	"gioui.org/io/pointer"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestRelEditCapsuleText 胶囊文案：没改过不提处数（「已改 0 处」是噪音）。
func TestRelEditCapsuleText(t *testing.T) {
	if got := relEditCapsuleText(0); got != i18n.T("ui.relmap.edit_capsule") {
		t.Fatalf("未改文案 = %q", got)
	}
	if got := relEditCapsuleText(3); got != i18n.Tf("ui.relmap.edit_capsule_count", "n", 3) {
		t.Fatalf("已改文案 = %q", got)
	}
}

// TestNodeMenuFrozenGreysStructural 编辑期只有只读项可点（§5.6.5）：会改结构的全置灰。
func TestNodeMenuFrozenGreysStructural(t *testing.T) {
	frozenLabels := map[string]bool{
		"编辑": true, "编辑并转移历史": true, "编辑并复制历史": true,
		"重新生成": true, "生成响应": true, "插入摘要": true, "删除该节点及子树": true,
	}
	openLabels := map[string]bool{"复制": true, "查看原文": true, "复制 ID": true, "引用": true, "转到": true}

	items := nodeMenuItems(blockUser, true, true, true)
	seenGrey, seenOpen := 0, 0
	for _, it := range items {
		if it.id == 0 {
			continue // 分隔线
		}
		switch {
		case frozenLabels[it.label]:
			if !it.disabled {
				t.Fatalf("编辑期「%s」应置灰", it.label)
			}
			seenGrey++
		case openLabels[it.label]:
			if it.disabled {
				t.Fatalf("编辑期「%s」是只读项，不该置灰", it.label)
			}
			seenOpen++
		default:
			t.Fatalf("菜单里出现了未归类的项「%s」——置灰口径要跟着项集走", it.label)
		}
	}
	if seenGrey != len(frozenLabels) || seenOpen != len(openLabels) {
		t.Fatalf("项数 = 灰 %d / 亮 %d, want %d / %d",
			seenGrey, seenOpen, len(frozenLabels), len(openLabels))
	}

	// 非编辑期：一项都不灰。
	for _, it := range nodeMenuItems(blockUser, true, true, false) {
		if it.disabled {
			t.Fatalf("非编辑期「%s」不该置灰", it.label)
		}
	}
}

// TestFrozenMenuItemsReachPlatform 置灰位要一路走到平台 DTO（否则 Windows 侧永远不灰）。
func TestFrozenMenuItemsReachPlatform(t *testing.T) {
	u := newFrameUI()
	u.bubbleMenu.Store(&bubbleMenuCtx{id: "u1", kind: blockUser, graph: true, frozen: true})
	var greyed, lit int
	for _, it := range u.menuItems(platform.MenuBubble) {
		if it.ID == 0 {
			continue
		}
		if it.Disabled {
			greyed++
		} else {
			lit++
		}
	}
	if greyed == 0 || lit == 0 {
		t.Fatalf("置灰位未传到平台: 灰 %d / 亮 %d", greyed, lit)
	}
	for _, it := range u.menuItems(platform.MenuBubble) {
		if it.ID == cmdNodeRm && !it.Disabled {
			t.Fatal("「删除该节点及子树」在编辑期必须置灰")
		}
		if it.ID == cmdNodeCopyID && it.Disabled {
			t.Fatal("「复制 ID」是只读项，不该置灰")
		}
	}
}

// TestFrozenMenuDispatchIgnoresStructural 纵深防御：置灰是呈现约定，分发处再拦一道——
// 编辑期开出的菜单里点结构项一律不生效（防「菜单开着时进了编辑模式」这类时序）。
func TestFrozenMenuDispatchIgnoresStructural(t *testing.T) {
	u := newFrameUI()
	u.inCh = make(chan port.UserInput, 4)
	ctx := &bubbleMenuCtx{id: "u1", kind: blockUser, graph: true, frozen: true}
	menuDispatchBubble(u, ctx, cmdNodeRm)
	select {
	case in := <-u.inCh:
		t.Fatalf("编辑期点「删除」不应生效，却投出 %+v", in)
	default:
	}
	if u.nodeAction.Load() != nil {
		t.Fatal("编辑期点结构项不应留下动作")
	}
}

// TestEditCapsuleDrawnInEditMode 编辑态整帧能画出来（胶囊 + 环 + 虚线 + 光带同屏），
// 且帧态从内核状态取到（含「已改 N 处」）。
func TestEditCapsuleDrawnInEditMode(t *testing.T) {
	u, st, q, tree := relEditFixture(t)
	if !st.graph.editOn {
		t.Fatal("编辑态未同步到图帧态")
	}
	tree.editEdits = 2
	st.sync(u) // 帧首同步（relGFrame 只走图帧，不重跑 historyState.sync）
	relGFrame(q, u, st)
	if st.graph.editCount != 2 {
		t.Fatalf("胶囊处数 = %d, want 2", st.graph.editCount)
	}
	// 拖起来一帧（提环 / 虚线 / 目标环这条绘制路径也要能画）。
	from := relNodeScreen(t, st.graph, "u2")
	q.Queue(relPointer(pointer.Press, from, true))
	relGFrame(q, u, st)
	to := relNodeScreen(t, st.graph, "u1b")
	q.Queue(relPointer(pointer.Move, to, true))
	relGFrame(q, u, st)
	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st)
}
