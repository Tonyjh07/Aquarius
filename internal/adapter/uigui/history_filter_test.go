package uigui

// history_filter_test.go 左栏过滤框与导入勾选面板过滤框（D322）：
//   - 左栏：过滤框对快照做客户端过滤（标题或 ID 子串，不分大小写），view 随内容收窄；
//     空查询 = 全量；过滤下行点击仍投对会话；
//   - 勾选面板：过滤收窄可见行，勾选动作只作用于可见行（被滤掉的行视同未勾）。

import (
	"strings"
	"testing"
)

// TestLeftFilterNarrowsView 过滤框收窄左栏：命中标题的行留下，恢复空查询回到全量。
func TestLeftFilterNarrowsView(t *testing.T) {
	t.Parallel()
	_, st, _ := delFixture(t)
	if len(st.view) != 2 {
		t.Fatalf("初始 view = %d 行, want 2", len(st.view))
	}

	st.filt.SetText("乙")
	st.applyFilter()
	if len(st.view) != 1 || st.view[0].Title != "乙" {
		t.Fatalf("过滤「乙」后 view = %+v, want 只剩乙", st.view)
	}

	// 大小写不敏感 + ID 命中。
	st.filt.SetText("C1")
	st.applyFilter()
	if len(st.view) != 1 || st.view[0].ID != "C1" {
		t.Fatalf("过滤「C1」后 view = %+v, want 只剩 C1", st.view)
	}

	st.filt.SetText("")
	st.applyFilter()
	if len(st.view) != 2 {
		t.Fatalf("清空过滤后 view = %d 行, want 2", len(st.view))
	}
}

// TestLeftFilterRowClickHit 过滤态下点行投的是**可见行**的会话（行序是 view 的行序，
// 不是快照的行序）。
func TestLeftFilterRowClickHit(t *testing.T) {
	t.Parallel()
	u, st, q := delFixture(t)
	st.filt.SetText("乙")
	st.applyFilter()
	if len(st.view) != 1 || st.view[0].ID != "C2" {
		t.Fatalf("过滤后 view = %+v, want 只剩乙(C2)", st.view)
	}
	st.rows[0].Click()
	histFrameFor(q, u, st)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "switch" ||
			strings.Join(in.Command.Args, "") != "C2" {
			t.Fatalf("过滤态点首行投了 %+v, want switch C2", in.Command)
		}
	default:
		t.Fatal("过滤态点首行没投命令")
	}
}

// TestPickFilterHidesRows 勾选面板过滤：可见行收窄；被滤掉的行视同未勾——
// 「导入」只导可见且勾着的。
func TestPickFilterHidesRows(t *testing.T) {
	t.Parallel()
	ix := &fakeIndexer{entries: pickEntries()}
	_, st := newPickUI(t, ix)
	st.openImportPick(`C:\备份.aqx`, pickEntries())
	d := st.pick
	if d.count() != 3 {
		t.Fatalf("初始勾了 %d 个, want 3", d.count())
	}

	d.filt.SetText("乙")
	d.applyFilter()
	if len(d.viewIdx) != 1 {
		t.Fatalf("过滤「乙」后可见 %d 行, want 1", len(d.viewIdx))
	}
	if n := d.count(); n != 1 {
		t.Fatalf("过滤后可勾 %d 个, want 1（被滤掉的视同未勾）", n)
	}

	// 清掉过滤：三行回到可见，勾选态原样保留（过滤不重置勾选）。
	d.filt.SetText("")
	d.applyFilter()
	if d.count() != 3 {
		t.Fatalf("清空过滤后勾了 %d 个, want 3", d.count())
	}
}
