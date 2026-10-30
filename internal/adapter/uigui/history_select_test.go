package uigui

// history_select_test.go 左栏批量选择态（D249）测试：
//   - 「选择」钮进选择态，缺省一个都不勾；
//   - 点行 = 勾选切换（不投 /switch）；再点一次取消；
//   - 「全选」= 全勾，再点 = 全不选（一颗钮管两头）；
//   - 未勾时「导出」「删除」点击无反应；勾了之后各投一条命令：
//     删除 = `/rmconv <id>…`（ID 按左栏顺序）、导出 = 保存框 → `/export --ids a,b --path <落点>`；
//   - Esc 退出选择态；「取消」也退出，且都不投命令；
//   - 勾选按会话 ID 记：快照重排后勾的仍是同一个会话。

import (
	"strconv"
	"strings"
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newBatchSelUI 三会话的夹具（左栏顺序 C1 / C2 / C3，C1 当前）。
func newBatchSelUI(t *testing.T) (*UI, *fakePlat, *historyState, *input.Router) {
	t.Helper()
	sum := func(id, title string, cur bool) port.SessionSummary {
		return port.SessionSummary{ID: conversation.ID(id), Title: title, Messages: 3, Current: cur}
	}
	u, fp, st := newExchangeUI(t, fakeLister{
		sum("C1", "甲会话", true),
		sum("C2", "乙会话", false),
		sum("C3", "丙会话", false),
	})
	q := new(input.Router)
	histFrameFor(q, u, st)
	drainCh(u)
	return u, fp, st, q
}

// histFrameHdp 历史窗测试帧高（histFrameFor 用 720×560）。
const histFrameHdp = 560

// batchHit 一次扫描得到的控件坐标。
type batchHit struct{ x, y int }

// batchLocate 扫左栏整列，定位一个点击件（判据 = 该件自己的 Hovered：真指针驱动，不硬编码
// 排版常数）。左栏从标题行扫到底部动作条——「选择」在**列表底下**，只扫标题行那一带会扫不到。
//
// 步长 6：最小的点击件也比 6dp 宽（行尾的「✕」槽 20dp），格点必落在它的矩形里；逐像素扫
// 只是把帧数翻九倍，换不来更准的定位（整包在 `-race` 下会因此超时）。
func batchLocate(t *testing.T, q *input.Router, u *UI, st *historyState, c *widget.Clickable, what string) batchHit {
	t.Helper()
	// 先空跑一帧把排版铺开（首帧的 Hovered 恒 false：命中集上一帧才建立）。
	histFrameFor(q, u, st)
	const step = 6
	for y := 1; y < histFrameHdp; y += step {
		for x := 1; x < int(histLeftColumnDp); x += step {
			q.Queue(ovPointer(pointer.Move, x, y))
			histFrameFor(q, u, st)
			if c.Hovered() {
				return batchHit{x: x, y: y}
			}
		}
	}
	t.Fatalf("扫不到%s钮", what)
	return batchHit{}
}

// batchClickAt 点一个坐标（先悬浮一帧再点：点击件要先进入窗口）。点完再空跑一帧：
// 文件框结果经平台宿主同步回传时，命令是在**下一帧帧首**由 drainFilePicks 投出的。
func batchClickAt(t *testing.T, q *input.Router, u *UI, st *historyState, p batchHit) {
	t.Helper()
	q.Queue(ovPointer(pointer.Move, p.x, p.y))
	histFrameFor(q, u, st)
	ovClick(q, p.x, p.y)
	histFrameFor(q, u, st)
	histFrameFor(q, u, st)
}

// batchClickBtn 点一颗钮。
func batchClickBtn(t *testing.T, q *input.Router, u *UI, st *historyState, c *widget.Clickable, what string) {
	t.Helper()
	batchClickAt(t, q, u, st, batchLocate(t, q, u, st, c, what))
}

// batchClickRow 点第 i 行（整行都是命中区；行中点用行点击件的命中盒扫出，不靠 ✕ 的显隐——
// 选择态里那颗粒子根本不画）。
func batchClickRow(t *testing.T, q *input.Router, u *UI, st *historyState, i int) {
	t.Helper()
	c := st.rowClick(i)
	p := batchLocate(t, q, u, st, c, "第"+strconv.Itoa(i)+"行")
	// 行命中盒是整个 200dp 宽的行带；扫到的是最左命中点，往右挪一截避开左侧复选框槽。
	p.x += 60
	if p.x > 180 {
		p.x = 180
	}
	batchClickAt(t, q, u, st, p)
}

// batchDrainCmd 取一条命令（无则报错）。
func batchDrainCmd(t *testing.T, u *UI) *portCommand {
	t.Helper()
	select {
	case in := <-u.inCh:
		if in.Command == nil {
			t.Fatalf("投递的不是命令: %+v", in)
		}
		return &portCommand{name: in.Command.Name, args: in.Command.Args}
	default:
		t.Fatal("未投出任何命令")
	}
	return nil
}

// TestSelectModeEnterAndDeselect 「选择」进选择态：缺省全不勾（勾是用户的动作），
// 且此时点行不再投 /switch。
func TestSelectModeEnterAndDeselect(t *testing.T) {
	u, _, st, q := newBatchSelUI(t)
	if st.selMode {
		t.Fatal("初始就在选择态")
	}
	batchClickBtn(t, q, u, st, &st.btnSelect, "选择")
	if !st.selMode {
		t.Fatal("点「选择」后未进选择态")
	}
	if n := st.selCount(); n != 0 {
		t.Fatalf("进选择态缺省勾了 %d 个, want 0", n)
	}

	batchClickRow(t, q, u, st, 1)
	if n := st.selCount(); n != 1 {
		t.Fatalf("点一行后勾了 %d 个, want 1", n)
	}
	if ids := st.selIDs(); len(ids) != 1 || ids[0] != "C2" {
		t.Fatalf("勾中的 ID = %v, want [C2]", ids)
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("选择态点行不该投命令: %+v", in.Command)
	default:
	}

	// 再点同一行 = 取消勾选。
	batchClickRow(t, q, u, st, 1)
	if n := st.selCount(); n != 0 {
		t.Fatalf("再点一行后勾了 %d 个, want 0", n)
	}
}

// TestSelectAllTogglesBothWays 「全选」全勾，再点变「全不选」——一颗钮管两头，
// 「全选」不该是没有回退方向的死路。
func TestSelectAllTogglesBothWays(t *testing.T) {
	u, _, st, q := newBatchSelUI(t)
	batchClickBtn(t, q, u, st, &st.btnSelect, "选择")

	batchClickBtn(t, q, u, st, &st.btnSelAll, "全选")
	if n := st.selCount(); n != len(st.sessions) {
		t.Fatalf("全选后勾了 %d 个, want %d", n, len(st.sessions))
	}
	batchClickBtn(t, q, u, st, &st.btnSelAll, "全不选")
	if n := st.selCount(); n != 0 {
		t.Fatalf("再点全选后勾了 %d 个, want 0", n)
	}
}

// TestSelectBatchDeleteSendsAllIDsInListOrder 勾三个里的两个（先勾第 3 行、再勾第 1 行，
// 顺序与左栏相反）→ `/rmconv` 的参数仍是**左栏顺序**：命令里的写序即归档清单顺序，
// 勾选顺序不参与语义。
func TestSelectBatchDeleteSendsAllIDsInListOrder(t *testing.T) {
	u, _, st, q := newBatchSelUI(t)
	batchClickBtn(t, q, u, st, &st.btnSelect, "选择")
	batchClickRow(t, q, u, st, 2)
	batchClickRow(t, q, u, st, 0)
	if n := st.selCount(); n != 2 {
		t.Fatalf("勾了 %d 个, want 2", n)
	}

	batchClickBtn(t, q, u, st, &st.btnSelDel, "删除")
	cmd := batchDrainCmd(t, u)
	if cmd.name != "rmconv" {
		t.Fatalf("命令 = %s, want rmconv", cmd.name)
	}
	want := []string{"C1", "C3"}
	if strings.Join(cmd.args, ",") != strings.Join(want, ",") {
		t.Fatalf("Args = %v, want %v（左栏顺序）", cmd.args, want)
	}
	if st.selMode {
		t.Fatal("投完删除后应退出选择态")
	}
}

// TestSelectBatchExportSendsIDsAndPath 批量导出：保存框只一档 aqx、预填 aquarius-backup.aqx；
// 落点回来后投 `/export --ids a,c --path <落点>`，并退出选择态。
func TestSelectBatchExportSendsIDsAndPath(t *testing.T) {
	u, fp, st, q := newBatchSelUI(t)
	const picked = `D:\备份\aquarius-backup.aqx`
	fp.OnSaveRequest = func(req platform.SaveRequest) (string, bool) {
		if req.Purpose != platform.PurposeExchangeBatchExport {
			t.Errorf("保存框用途 = %v, want 批量导出", req.Purpose)
		}
		if req.Name != port.MultiExportStem+".aqx" {
			t.Errorf("预填名 = %q, want %q", req.Name, port.MultiExportStem+".aqx")
		}
		if len(req.Filters) != 1 || req.Filters[0].Pattern != "*.aqx" {
			t.Errorf("过滤器 = %+v, want 只有一档 aqx（其余格式装不下多会话）", req.Filters)
		}
		return picked, true
	}

	batchClickBtn(t, q, u, st, &st.btnSelect, "选择")
	batchClickRow(t, q, u, st, 0)
	batchClickRow(t, q, u, st, 2)
	batchClickBtn(t, q, u, st, &st.btnSelExport, "导出")

	if len(fp.SaveRequests) != 1 {
		t.Fatalf("保存框请求 %d 次, want 1", len(fp.SaveRequests))
	}
	cmd := batchDrainCmd(t, u)
	if cmd.name != "export" {
		t.Fatalf("命令 = %s, want export", cmd.name)
	}
	want := []string{"--ids", "C1,C3", "--path", picked}
	if strings.Join(cmd.args, "|") != strings.Join(want, "|") {
		t.Fatalf("Args = %v, want %v", cmd.args, want)
	}
	if st.selMode {
		t.Fatal("投完导出后应退出选择态")
	}
	if len(st.selPending) != 0 {
		t.Fatalf("selPending 未清: %v", st.selPending)
	}
}

// TestSelectExportUsesSnapshotWhenModeExits 保存框异步：落点回来前用户可能已退出选择态
// （Esc），勾选已清空——投出的仍是按下「导出」那刻的快照，不是一条空命令。
func TestSelectExportUsesSnapshotWhenModeExits(t *testing.T) {
	u, fp, st, q := newBatchSelUI(t)
	var pending string
	fp.OnSaveRequest = func(platform.SaveRequest) (string, bool) {
		// 模拟「对话框开着的时候用户退出选择态」：先把结果扣住，退出后再回传。
		pending = `D:\x.aqx`
		return "", true
	}
	batchClickBtn(t, q, u, st, &st.btnSelect, "选择")
	batchClickRow(t, q, u, st, 0)
	batchClickBtn(t, q, u, st, &st.btnSelExport, "导出")
	st.exitSelMode() // 对话框期间的 Esc

	u.filePicks <- filePick{purpose: platform.PurposeExchangeBatchExport, path: pending}
	st.drainFilePicks(u)

	cmd := batchDrainCmd(t, u)
	if cmd.name != "export" || cmd.args[0] != "--ids" || cmd.args[1] != "C1" {
		t.Fatalf("命令 = %s %v, want export --ids C1 --path …", cmd.name, cmd.args)
	}
}

// TestSelectActionsDisabledWithNothingChecked 一个都没勾时「导出」「删除」点击不投命令，
// 也不弹保存框（禁用态 = 真不响应，不只是颜色浅一点）。
func TestSelectActionsDisabledWithNothingChecked(t *testing.T) {
	u, fp, st, q := newBatchSelUI(t)
	batchClickBtn(t, q, u, st, &st.btnSelect, "选择")

	batchClickBtn(t, q, u, st, &st.btnSelDel, "删除")
	batchClickBtn(t, q, u, st, &st.btnSelExport, "导出")
	select {
	case in := <-u.inCh:
		t.Fatalf("未勾任何项却投出命令: %+v", in.Command)
	default:
	}
	if len(fp.SaveRequests) != 0 {
		t.Fatalf("未勾任何项却弹了保存框: %+v", fp.SaveRequests)
	}
}

// TestSelectEscAndCancelExit Esc 与「取消」都退出选择态、都不投命令。
func TestSelectEscAndCancelExit(t *testing.T) {
	u, _, st, q := newBatchSelUI(t)
	batchClickBtn(t, q, u, st, &st.btnSelect, "选择")
	batchClickRow(t, q, u, st, 1)
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	histFrameFor(q, u, st)
	if st.selMode {
		t.Fatal("Esc 未退出选择态")
	}
	if n := st.selCount(); n != 0 {
		t.Fatalf("退出后仍留着 %d 个勾选", n)
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("Esc 退出了却投命令: %+v", in.Command)
	default:
	}

	batchClickBtn(t, q, u, st, &st.btnSelect, "选择")
	batchClickRow(t, q, u, st, 1)
	batchClickBtn(t, q, u, st, &st.btnSelCancel, "取消")
	if st.selMode {
		t.Fatal("「取消」未退出选择态")
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("「取消」了却投命令: %+v", in.Command)
	default:
	}
}

// TestSelectKeepsChoicesAcrossResort 勾选按**会话 ID** 记：快照重排（换个顺序）后勾的仍是
// 同一个会话，不会因为行号变了就跳到别人身上。
func TestSelectKeepsChoicesAcrossResort(t *testing.T) {
	u, _, st, q := newBatchSelUI(t)
	batchClickBtn(t, q, u, st, &st.btnSelect, "选择")
	batchClickRow(t, q, u, st, 2) // C3
	if ids := st.selIDs(); len(ids) != 1 || ids[0] != "C3" {
		t.Fatalf("勾中的 ID = %v, want [C3]", ids)
	}

	// 列表重排（内核另一次快照：C3 挪到最前）。
	u.opts.Lister = fakeLister{
		{ID: "C3", Title: "丙会话", Messages: 3},
		{ID: "C1", Title: "甲会话", Messages: 3, Current: true},
		{ID: "C2", Title: "乙会话", Messages: 3},
	}
	histFrameFor(q, u, st)
	if ids := st.selIDs(); len(ids) != 1 || ids[0] != "C3" {
		t.Fatalf("重排后勾中的 ID = %v, want 仍为 [C3]", ids)
	}
	if !st.sel[conversation.ID("C3")] {
		t.Fatal("重排后 C3 的勾选丢了")
	}
}
