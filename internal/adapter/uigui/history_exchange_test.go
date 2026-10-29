package uigui

// history_exchange_test.go 会话进出的 GUI 出口（§5.16）：两颗文字钮各自向平台要
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
// 派生的 .aqx 名、第一档预选），选完投 `/export --format aqx --path <路径>`——用户挑的
// 就是最终落点。
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
		if req.FilterIndex != 0 {
			t.Errorf("预选档位 = %d, want 0（缺省无损归档排第一）", req.FilterIndex)
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
		want := []string{"--format", "aqx", "--path", picked}
		if len(in.Command.Args) != len(want) {
			t.Fatalf("参数 = %v, want %v", in.Command.Args, want)
		}
		for i := range want {
			if in.Command.Args[i] != want[i] {
				t.Fatalf("参数 = %v, want %v", in.Command.Args, want)
			}
		}
	default:
		t.Fatal("选完落点后没投出 /export")
	}
}

// TestHistExportHonoursSelectedFormat 保存框里换了档位：`--format` 跟着换，落点扩展名也
// 跟着换——选了 SillyTavern 却存成 .aqx 就是格式与文件名对不上。
func TestHistExportHonoursSelectedFormat(t *testing.T) {
	u, fp, st := newExchangeUI(t, nil)
	idx := -1
	for i, f := range exportFormats() {
		if f.Name == "sillytavern" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("格式表里没有 sillytavern（下面的断言失去意义）")
	}
	fp.SaveFilterIndex = idx
	fp.OnSaveRequest = func(platform.SaveRequest) (string, bool) {
		// 用户只打了名字：Win32 的静态 defExt 会补 .aqx，这里模拟那个结果。
		return `D:\备份\甲会话.aqx`, true
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

	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "export" {
			t.Fatalf("投出 %+v, want /export", in.Command)
		}
		want := []string{"--format", "sillytavern", "--path", `D:\备份\甲会话.jsonl`}
		if len(in.Command.Args) != len(want) {
			t.Fatalf("参数 = %v, want %v", in.Command.Args, want)
		}
		for i := range want {
			if in.Command.Args[i] != want[i] {
				t.Fatalf("参数 = %v, want %v", in.Command.Args, want)
			}
		}
	default:
		t.Fatal("选完落点后没投出 /export")
	}
}

// TestHistExportFallsBackToExtension 平台报不出档位（非 Windows）时按扩展名反推：.jsonl
// → sillytavern，认不出的扩展名 → aqx（缺省且唯一无损的一档）。
func TestHistExportFallsBackToExtension(t *testing.T) {
	cases := []struct {
		picked string
		want   string
	}{
		{`D:\备份\甲会话.jsonl`, "sillytavern"},
		{`D:\备份\甲会话.json`, "chatgpt"},
		{`D:\备份\甲会话.aqx`, "aqx"},
		{`D:\备份\甲会话.txt`, "aqx"},
	}
	for _, c := range cases {
		u, _, st := newExchangeUI(t, nil)
		drainCh(u)
		// 直接投一笔结果：这一条测的是「档位未知时怎么定格式」，不掺按钮路径。
		u.filePicks <- filePick{
			purpose: platform.PurposeExchangeExport, path: c.picked, filterIndex: -1,
		}
		st.drainFilePicks(u)
		select {
		case in := <-u.inCh:
			if in.Command == nil || len(in.Command.Args) < 2 || in.Command.Args[1] != c.want {
				t.Fatalf("%s: 参数 = %v, want --format %s", c.picked, in.Command.Args, c.want)
			}
		default:
			t.Fatalf("%s: 没投出 /export", c.picked)
		}
	}
}

// TestWithFormatExt 落点扩展名与所选格式对齐：已知格式的扩展名换掉，其它一律不切。
func TestWithFormatExt(t *testing.T) {
	jsonl, ok := exportFormatAt(0)
	for _, f := range exportFormats() {
		if f.Name == "sillytavern" {
			jsonl, ok = f, true
		}
	}
	if !ok || jsonl.Name != "sillytavern" {
		t.Fatal("格式表里没有 sillytavern")
	}
	cases := []struct{ in, want string }{
		{`D:\x\甲.aqx`, `D:\x\甲.jsonl`},               // 已知格式扩展名 → 换成本档
		{`D:\x\甲.json`, `D:\x\甲.jsonl`},              // 同上（.json 属于 chatgpt/openai）
		{`D:\x\甲.jsonl`, `D:\x\甲.jsonl`},             // 已经对了，原样
		{`D:\x\甲`, `D:\x\甲.jsonl`},                   // 没有扩展名 → 补上
		{`D:\x\2026.10.28`, `D:\x\2026.10.28.jsonl`}, // 点名不是格式扩展名 → 保住不切
	}
	for _, c := range cases {
		if got := withFormatExt(c.in, jsonl); got != c.want {
			t.Fatalf("withFormatExt(%q) = %q, want %q", c.in, got, c.want)
		}
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
//   - 导出的「保存类型」下拉框 = 端口格式表里**全部可写格式**，一档一种、同序同数：
//     用户选中哪一档，界面就投那个 `--format`。这里锁住「档位 ↔ 格式」的对应关系，
//     序号错一位就是「选了 SillyTavern、拿到的却是 aqx 字节」；
//   - 只读格式（openwebui）不列：没有第二套同义出口，列出来就是空头承诺；
//   - 导入必须覆盖全部五档格式：认哪几种就得在框里给到哪几种，否则用户写了扩展名却
//     在框里找不到那一档。
func TestExchangeFiltersMatchWhatTheEntryWrites(t *testing.T) {
	writable := exportFormats()
	if len(writable) == 0 {
		t.Fatal("端口格式表里没有可写格式")
	}
	save := exchangeSaveFilters()
	if len(save) != len(writable) {
		t.Fatalf("导出过滤器 %d 档, want %d（可写格式数）", len(save), len(writable))
	}
	for i, f := range writable {
		if want := "*" + f.Ext; save[i].Pattern != want {
			t.Errorf("第 %d 档 pattern = %q, want %q（%s）", i, save[i].Pattern, want, f.Name)
		}
		if want := f.Label + " (*" + f.Ext + ")"; save[i].Label != want {
			t.Errorf("第 %d 档 label = %q, want %q", i, save[i].Label, want)
		}
		if got, ok := exportFormatAt(i); !ok || got.Name != f.Name {
			t.Errorf("档位 %d → %+v, want %s", i, got, f.Name)
		}
	}
	// 第一档必须是 aqx：预填名与缺省落点都照它算（无损、能原样导回）。
	if writable[0].Name != "aqx" {
		t.Errorf("第一档 = %s, want aqx（预填名与缺省落点照它算）", writable[0].Name)
	}
	// 只读格式一个都不许出现（openwebui 的扩展名恰好也是 .json，按名字与档数查）。
	if len(save) > 0 {
		for _, f := range port.ExchangeFormats() {
			if f.NoExport && f.Name == "openwebui" {
				for i, got := range writable {
					if got.Name == "openwebui" {
						t.Errorf("只读格式 openwebui 出现在第 %d 档", i)
					}
				}
			}
		}
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
