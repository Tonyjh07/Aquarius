package uigui

// history_import_pick_test.go 导入多会话归档的勾选面板（D249）测试：
//   - 选完文件先经 ExchangeIndexer 要清单：一项（或取不到）直接投 /import，不弹面板；
//   - 多项弹面板、缺省全勾；
//   - 取消勾掉一个 → `/import --pick 1,3 <路径>`（一条 --pick 逗号拼）；
//   - 取消 / Esc 都不投命令；
//   - 面板在屏时算模态（背景不取事件），且「取消」不关卡以外的东西。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/key"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeIndexer 假清单源：按预设返回，或返回错误。
type fakeIndexer struct {
	entries []port.ExchangeEntry
	err     error
	paths   []string
}

func (f *fakeIndexer) Index(_ context.Context, path string) ([]port.ExchangeEntry, error) {
	f.paths = append(f.paths, path)
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

// pickEntries 三会话的清单（序号 1 起）。
func pickEntries() []port.ExchangeEntry {
	return []port.ExchangeEntry{
		{Index: 1, ID: "S1", Title: "甲备份", Nodes: 12},
		{Index: 2, ID: "S2", Title: "乙备份", Nodes: 3},
		{Index: 3, ID: "S3", Title: "丙备份", Nodes: 7},
	}
}

// pickDrainCmd 取一条已投出的命令（无则报错）。
func pickDrainCmd(t *testing.T, u *UI) *portCommand {
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

// newPickUI 装一个假清单源的历史窗夹具（ix == nil = 不装清单源）。
func newPickUI(t *testing.T, ix *fakeIndexer) (*UI, *historyState) {
	t.Helper()
	u, _, st := newExchangeUI(t, nil)
	if ix != nil {
		u.opts.Indexer = ix
	}
	return u, st
}

// TestImportSingleEntrySkipsPanel 只有一项（非 aqx 或单会话归档）不弹面板，直接投 /import。
func TestImportSingleEntrySkipsPanel(t *testing.T) {
	ix := &fakeIndexer{entries: []port.ExchangeEntry{{Index: 1, ID: "S1", Title: "甲", Nodes: 4}}}
	u, st := newPickUI(t, ix)
	u.filePicks <- filePick{purpose: platform.PurposeExchangeImport, path: `C:\a.aqx`}
	st.drainFilePicks(u)

	if st.pick != nil {
		t.Fatal("只有一项却弹了勾选面板（多一次点击）")
	}
	cmd := pickDrainCmd(t, u)
	if cmd.name != "import" || len(cmd.args) != 1 || cmd.args[0] != `C:\a.aqx` {
		t.Fatalf("命令 = %s %v, want import <路径>（不带 --pick）", cmd.name, cmd.args)
	}
}

// TestImportIndexerErrorFallsThrough 取清单失败（不是归档 / 文件坏了）也直接投：命令面会把
// 原因说清楚，界面不替它编一个说法（壳内不认格式）。
func TestImportIndexerErrorFallsThrough(t *testing.T) {
	ix := &fakeIndexer{err: errors.New("不是归档")}
	u, st := newPickUI(t, ix)
	u.filePicks <- filePick{purpose: platform.PurposeExchangeImport, path: `C:\坏.aqx`}
	st.drainFilePicks(u)

	if st.pick != nil {
		t.Fatal("取清单失败却弹了面板")
	}
	cmd := pickDrainCmd(t, u)
	if cmd.name != "import" || cmd.args[0] != `C:\坏.aqx` {
		t.Fatalf("命令 = %s %v, want import <路径>", cmd.name, cmd.args)
	}
}

// TestImportNilIndexerFallsThrough 没装清单源（测试瘦 UI / 旧装配）时退回单会话口径：
// 选完直接投 /import，不弹一个没有数据的空面板。
func TestImportNilIndexerFallsThrough(t *testing.T) {
	u, st := newPickUI(t, nil)
	u.filePicks <- filePick{purpose: platform.PurposeExchangeImport, path: `C:\a.aqx`}
	st.drainFilePicks(u)
	if st.pick != nil {
		t.Fatal("无清单源却弹了面板")
	}
	if cmd := pickDrainCmd(t, u); cmd.name != "import" {
		t.Fatalf("命令 = %s, want import", cmd.name)
	}
}

// TestImportMultiEntryOpensPanelAllChecked 多项：弹面板、缺省全勾（备份恢复的主路径就是全要）。
func TestImportMultiEntryOpensPanelAllChecked(t *testing.T) {
	ix := &fakeIndexer{entries: pickEntries()}
	u, st := newPickUI(t, ix)
	u.filePicks <- filePick{purpose: platform.PurposeExchangeImport, path: `C:\备份.aqx`}
	st.drainFilePicks(u)

	if st.pick == nil {
		t.Fatal("三项清单却没弹勾选面板")
	}
	if got := st.pick.count(); got != 3 {
		t.Fatalf("缺省勾了 %d 个, want 3（缺省全勾）", got)
	}
	if !st.modalActive(u) {
		t.Fatal("面板在屏却不算模态（背景会照常取事件）")
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("只弹面板、用户还没确认就投了命令: %+v", in.Command)
	default:
	}
	// 清单只问一次（一项一次调用）。
	if len(ix.paths) != 1 || ix.paths[0] != `C:\备份.aqx` {
		t.Fatalf("Index 调用 = %v, want [C:\\备份.aqx]", ix.paths)
	}
}

// TestImportPickSendsCommaSeparatedIndexes 取消勾一个 → `/import --pick 1,3 <路径>`：
// 一条 --pick 带全部序号（命令面认逗号分隔），序号取清单序号（1 起）。
func TestImportPickSendsCommaSeparatedIndexes(t *testing.T) {
	ix := &fakeIndexer{entries: pickEntries()}
	u, st := newPickUI(t, ix)
	u.filePicks <- filePick{purpose: platform.PurposeExchangeImport, path: `C:\备份.aqx`}
	st.drainFilePicks(u)

	st.pick.rows[1].Value = false // 不要第二个
	st.pick.ok.Click()
	histFrameFor(new(input.Router), u, st) // 借一帧把点击消费掉

	cmd := pickDrainCmd(t, u)
	if cmd.name != "import" {
		t.Fatalf("命令 = %s, want import", cmd.name)
	}
	want := []string{"--pick", "1,3", `C:\备份.aqx`}
	if strings.Join(cmd.args, "|") != strings.Join(want, "|") {
		t.Fatalf("Args = %v, want %v", cmd.args, want)
	}
	if st.pick != nil {
		t.Fatal("提交后面板应关闭")
	}
}

// TestImportPickCancelSendsNothing 取消 / Esc 都什么都不投。
func TestImportPickCancelSendsNothing(t *testing.T) {
	ix := &fakeIndexer{entries: pickEntries()}
	u, st := newPickUI(t, ix)
	q := new(input.Router)
	u.filePicks <- filePick{purpose: platform.PurposeExchangeImport, path: `C:\备份.aqx`}
	st.drainFilePicks(u)
	histFrameFor(q, u, st) // 焦点立起来（FocusCmd 在帧尾生效）

	st.pick.cancel.Click()
	histFrameFor(q, u, st)
	if st.pick != nil {
		t.Fatal("「取消」未关面板")
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("取消却投了命令: %+v", in.Command)
	default:
	}

	// 再来一次，走 Esc。
	u.filePicks <- filePick{purpose: platform.PurposeExchangeImport, path: `C:\备份.aqx`}
	st.drainFilePicks(u)
	histFrameFor(q, u, st)
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	histFrameFor(q, u, st)
	if st.pick != nil {
		t.Fatal("Esc 未关面板")
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("Esc 却投了命令: %+v", in.Command)
	default:
	}
}

// TestImportPickEmptySelectionKeepsPanel 全不勾时「导入」是禁用态：点击不投命令、也不关面板
// （用户正要重新勾）。
func TestImportPickEmptySelectionKeepsPanel(t *testing.T) {
	ix := &fakeIndexer{entries: pickEntries()}
	u, st := newPickUI(t, ix)
	u.filePicks <- filePick{purpose: platform.PurposeExchangeImport, path: `C:\备份.aqx`}
	st.drainFilePicks(u)

	for i := range st.pick.rows {
		st.pick.rows[i].Value = false
	}
	st.pick.ok.Click()
	histFrameFor(new(input.Router), u, st)

	if st.pick == nil {
		t.Fatal("全不勾时点了「导入」不该关面板")
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("全不勾却投了命令: %+v", in.Command)
	default:
	}
}

// TestImportPickBlocksBackground 面板在屏时左栏行不取事件（模态的纵深防御）。
func TestImportPickBlocksBackground(t *testing.T) {
	ix := &fakeIndexer{entries: pickEntries()}
	u, st := newPickUI(t, ix)
	q := new(input.Router)
	histFrameFor(q, u, st)
	u.filePicks <- filePick{purpose: platform.PurposeExchangeImport, path: `C:\备份.aqx`}
	st.drainFilePicks(u)
	histFrameFor(q, u, st)

	// 点左栏第一行（面板遮住整窗）：既不该投 /switch，也不该勾选。
	y := 60
	ovClick(q, 100, y)
	histFrameFor(q, u, st)
	select {
	case in := <-u.inCh:
		t.Fatalf("面板在屏时背景投出了命令: %+v", in.Command)
	default:
	}
	if st.selMode {
		t.Fatal("面板在屏时背景进入了选择态")
	}
}
