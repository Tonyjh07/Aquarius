package uigui

import (
	"image"
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestPermLevelCount 档位列表的控件数组长度与 permLevels 同源（D282）：两边一改一漏，
// 列表就会少一行或多出几个空控件。序列本身是契约（logo 菜单下标按它算，D73）。
func TestPermLevelCount(t *testing.T) {
	t.Parallel()
	want := []string{"read-only", "strict", "permissive", "full-access"}
	if len(permLevels) != permLevelCount || len(permLevels) != len(want) {
		t.Fatalf("档数 = %d/%d, want %d", len(permLevels), permLevelCount, len(want))
	}
	for i, lv := range want {
		if permLevels[i] != lv {
			t.Fatalf("第 %d 档 = %q, want %q", i, permLevels[i], lv)
		}
	}
}

// TestLevelPickRects 档位列表几何（D282）：贴在胶囊上沿之上、右缘对齐胶囊，四行
// 互不重叠且都落在卡内；窗口太矮时整体下移，不出窗。
func TestLevelPickRects(t *testing.T) {
	t.Parallel()
	gtx, _ := frameGtxSize(input.Source{}, winWidthDp, winHeightDp)
	_, pill, _ := inputRowRects(winWidthDp, 400, 48, 12, 8)
	card, rows := levelPickRects(gtx, pill, -400) // 输入行在窗内 400dp 处：窗顶 = 局部 -400

	if len(rows) != permLevelCount {
		t.Fatalf("行数 = %d, want %d", len(rows), permLevelCount)
	}
	if card.Max.Y > pill.Min.Y {
		t.Fatalf("卡应在胶囊上沿之上：card=%v pill=%v", card, pill)
	}
	if card.Max.X != pill.Max.X {
		t.Fatalf("卡右缘应对齐胶囊右缘：card.Max.X=%d pill.Max.X=%d", card.Max.X, pill.Max.X)
	}
	if card.Min.Y < -400 || card.Max.Y > winHeightDp {
		t.Fatalf("卡出窗：%v（窗高 %d）", card, winHeightDp)
	}
	for i, r := range rows {
		if !r.In(card) {
			t.Fatalf("第 %d 行不在卡内：row=%v card=%v", i, r, card)
		}
		if i > 0 && r.Min.Y < rows[i-1].Max.Y {
			t.Fatalf("第 %d 行与上一行重叠：%v / %v", i, rows[i-1], r)
		}
	}
	if rows[len(rows)-1].Max.Y > card.Max.Y {
		t.Fatalf("末行伸出卡外：%v > %d", rows[len(rows)-1], card.Max.Y)
	}

	// 窗顶放不下：卡整体下移，第一行仍在卡内（不因夹取把行留在卡外）。
	card2, rows2 := levelPickRects(gtx, image.Rect(40, 20, 400, 68), -60)
	if card2.Min.Y < -60 {
		t.Fatalf("夹取后卡仍在窗外：%v", card2)
	}
	if !rows2[0].In(card2) {
		t.Fatalf("夹取后首行在卡外：row=%v card=%v", rows2[0], card2)
	}
}

// TestLevelPickFrame 展开列表后整帧布局不 panic，且只在工具确认态画。
func TestLevelPickFrame(t *testing.T) {
	t.Parallel()
	u, q := newTestUIForLevelPick(t)
	u.levelPickOpen = true
	gtx, ops := frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops) // 事件树提交后仍需一次布局才把行控件摆上去
	gtx, ops = frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)

	// 换成命令行确认（来源 command）：列表不该再画，但也必须不 panic。
	u.m.replyConfirm(port.ConfirmAnswer{})
	u.m.startConfirm(port.ConfirmRequest{Origin: port.OriginCommand, Prompt: "确认删除？"},
		make(chan port.ConfirmAnswer, 1))
	u.levelPickOpen = true
	gtx, ops = frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)
}

// newTestUIForLevelPick 帧测试 UI：工具确认态已开（chip 就绪，问句并入 chip）。
func newTestUIForLevelPick(t *testing.T) (*UI, *input.Router) {
	t.Helper()
	u := newFrameUI()
	u.opts.Status = func() Status { return Status{Level: "strict"} }
	u.m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "file_write"}})
	u.m.startConfirm(port.ConfirmRequest{Origin: port.OriginTool, Tool: "file_write",
		Prompt: "允许写 a.txt？"}, make(chan port.ConfirmAnswer, 1))
	q := new(input.Router)
	return u, q
}
