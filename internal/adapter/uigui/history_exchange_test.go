package uigui

// history_exchange_test.go 会话进出的 GUI 出口（DESIGN §5.16）：两颗文字钮各自向平台要
// 一个路径，拿到之后**只投一条内核命令**（与键入同路径）；取消则什么都不投。过滤器与
// 预填文件名是界面替用户填的默认值，这里锁住它们与端口格式表不脱节。

import (
	"strings"
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/pointer"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newExchangeUI 历史窗测试 UI：接上文件框结果通道与平台宿主（真实现的宿主也在这条路上）。
func newExchangeUI(t *testing.T, list port.SessionLister) (*UI, *fakePlat, *historyState) {
	t.Helper()
	if list == nil {
		list = fakeLister{{ID: "C1", Title: "甲会话", Messages: 3, Current: true}}
	}
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, list)
	u.filePicks = make(chan filePick, 4)
	fp := fakeOf(u)
	fp.SetHost(&platHost{u: u})
	return u, fp, newHistoryState(u)
}

// TestHistImportButtonRequestsOpenAndSendsCommand 点「导入」：向平台要一个文件（带导入
// 用途与过滤器），选完投 `/import <路径>`——不投别的、不自己读文件。
func TestHistImportButtonRequestsOpenAndSendsCommand(t *testing.T) {
	u, fp, st := newExchangeUI(t, nil)
	const picked = `C:\tmp\别人的会话.json`
	fp.OnOpenRequest = func(req platform.OpenRequest) (string, bool) {
		if req.Purpose != platform.PurposeExchangeImport {
			t.Errorf("打开框用途 = %v, want 导入", req.Purpose)
		}
		if req.Title == "" || len(req.Filters) == 0 {
			t.Errorf("打开框缺标题或过滤器: %+v", req)
		}
		return picked, true
	}
	q := new(input.Router)
	histFrameFor(q, u, st)
	drainCh(u)

	x, y := histBtnXY(t, q, u, st, &st.btnImport, "导入")
	q.Queue(ovPointer(pointer.Move, x, y))
	histFrameFor(q, u, st)
	ovClick(q, x, y)
	histFrameFor(q, u, st)
	// 结果经平台宿主回到通道，再由下一帧帧首投出命令。
	histFrameFor(q, u, st)

	if len(fp.OpenRequests) != 1 {
		t.Fatalf("打开框请求 %d 次, want 1", len(fp.OpenRequests))
	}
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "import" {
			t.Fatalf("投出 %+v, want /import", in.Command)
		}
		if len(in.Command.Args) != 1 || in.Command.Args[0] != picked {
			t.Fatalf("参数 = %v, want [%s]", in.Command.Args, picked)
		}
	default:
		t.Fatal("选完文件后没投出 /import")
	}
}

// TestHistExportButtonRequestsSaveSendsCommand 点「导出」：向平台要一个落点（预填由标题
// 派生的 .aqx 名），选完投 `/export --path <路径>`——用户挑的就是最终落点。
func TestHistExportButtonRequestsSaveSendsCommand(t *testing.T) {
	u, fp, st := newExchangeUI(t, nil)
	const picked = `D:\备份\甲会话.aqx`
	fp.OnSaveRequest = func(req platform.SaveRequest) (string, bool) {
		if req.Purpose != platform.PurposeExchangeExport {
			t.Errorf("保存框用途 = %v, want 导出", req.Purpose)
		}
		if req.Name != "甲会话.aqx" {
			t.Errorf("预填名 = %q, want 甲会话.aqx（按标题派生）", req.Name)
		}
		if req.DefExt != "aqx" {
			t.Errorf("缺省扩展名 = %q, want aqx（不带点：Win32 的 lpstrDefExt 只取前三个字符）", req.DefExt)
		}
		return picked, true
	}
	q := new(input.Router)
	histFrameFor(q, u, st)
	drainCh(u)

	x, y := histBtnXY(t, q, u, st, &st.btnExport, "导出")
	q.Queue(ovPointer(pointer.Move, x, y))
	histFrameFor(q, u, st)
	ovClick(q, x, y)
	histFrameFor(q, u, st)
	histFrameFor(q, u, st)

	if len(fp.SaveRequests) != 1 {
		t.Fatalf("保存框请求 %d 次, want 1", len(fp.SaveRequests))
	}
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "export" {
			t.Fatalf("投出 %+v, want /export", in.Command)
		}
		want := []string{"--path", picked}
		if len(in.Command.Args) != 2 || in.Command.Args[0] != want[0] || in.Command.Args[1] != want[1] {
			t.Fatalf("参数 = %v, want %v", in.Command.Args, want)
		}
	default:
		t.Fatal("选完落点后没投出 /export")
	}
}

// TestHistExchangeCancelSendsNothing 用户取消（平台回空）→ 一条命令都不投，也不报错。
func TestHistExchangeCancelSendsNothing(t *testing.T) {
	u, fp, st := newExchangeUI(t, nil)
	fp.OnOpenRequest = func(platform.OpenRequest) (string, bool) { return "", true }
	fp.OnSaveRequest = func(platform.SaveRequest) (string, bool) { return "", true }
	q := new(input.Router)
	histFrameFor(q, u, st)
	drainCh(u)

	x, y := histBtnXY(t, q, u, st, &st.btnImport, "导入")
	q.Queue(ovPointer(pointer.Move, x, y))
	histFrameFor(q, u, st)
	ovClick(q, x, y)
	histFrameFor(q, u, st)
	histFrameFor(q, u, st)

	x2, y2 := histBtnXY(t, q, u, st, &st.btnExport, "导出")
	q.Queue(ovPointer(pointer.Move, x2, y2))
	histFrameFor(q, u, st)
	ovClick(q, x2, y2)
	histFrameFor(q, u, st)
	histFrameFor(q, u, st)

	select {
	case in := <-u.inCh:
		t.Fatalf("取消后仍投出命令: %+v", in)
	default:
	}
}

// TestDrainFilePicksIgnoresUnknownPurpose 认不出用途的一笔（将来加新用途）直接丢掉，
// 不投任何命令——宁可没反应，也不猜用户想干什么。
func TestDrainFilePicksIgnoresUnknownPurpose(t *testing.T) {
	u, _, st := newExchangeUI(t, nil)
	drainCh(u)
	u.filePicks <- filePick{purpose: platform.FilePurpose(99), path: `C:\x.aqx`}
	st.drainFilePicks(u)
	select {
	case in := <-u.inCh:
		t.Fatalf("未知用途投出了命令: %+v", in.Command)
	default:
	}
}

// TestExchangeFiltersMatchWhatTheEntryWrites 两颗钮各自只承诺它会兑现的东西：
//
//   - 导出走界面时**固定**是无损归档（换格式是命令行的事），所以保存框只列 `.aqx`——
//     列了 `.json` / `.jsonl` 就等于许诺「选这份扩展名就能得到那个格式」，而那不会发生；
//   - 导入必须覆盖全部五档格式：认哪几种就得在框里给到哪几种，否则用户写了扩展名却
//     在框里找不到那一档。
func TestExchangeFiltersMatchWhatTheEntryWrites(t *testing.T) {
	aqxExt := ""
	for _, fm := range port.ExchangeFormats() {
		if !fm.NoExport && fm.Name == "aqx" {
			aqxExt = fm.Ext
		}
	}
	if aqxExt == "" {
		t.Fatal("格式表里找不到 aqx 的扩展名")
	}
	save := exchangeSaveFilters()
	if len(save) != 1 || save[0].Pattern != "*"+aqxExt {
		t.Fatalf("导出过滤器 = %+v, want 只有 *%s", save, aqxExt)
	}

	open := ""
	for _, f := range exchangeOpenFilters() {
		open += " " + f.Pattern
	}
	for _, fm := range port.ExchangeFormats() {
		if !strings.Contains(open, fm.Ext) {
			t.Errorf("导入过滤器缺 %s 的扩展名 %s（%s）", fm.Name, fm.Ext, open)
		}
	}
}
