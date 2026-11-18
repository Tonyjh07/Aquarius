package uigui

// 「日志」页测试（D305）：列文件 → 点一行看末尾 → 打开目录。数据面是 port.LogReader，
// 排序与「只读末尾」的规则在 internal/logging 的测试里（那一层才碰真文件）。

import (
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeLogs port.LogReader 的测试替身：文件名 → 内容，按给定顺序返回。
type fakeLogs struct {
	dir     string
	order   []string
	bodies  map[string]string
	listErr error
	tailErr error
	tails   []string // 被读过的文件名（顺序）
}

func (l *fakeLogs) Dir() string { return l.dir }

func (l *fakeLogs) List() ([]port.LogFile, error) {
	if l.listErr != nil {
		return nil, l.listErr
	}
	out := make([]port.LogFile, 0, len(l.order))
	for _, name := range l.order {
		out = append(out, port.LogFile{Name: name, Size: int64(len(l.bodies[name]))})
	}
	return out, nil
}

func (l *fakeLogs) Tail(name string, _ int) (string, error) {
	l.tails = append(l.tails, name)
	if l.tailErr != nil {
		return "", l.tailErr
	}
	return l.bodies[name], nil
}

// logsPageUI 造一个接好读侧的 headless 设置窗。
func logsPageUI(t *testing.T, logs port.LogReader) (*UI, *settingsForm) {
	t.Helper()
	u := newHeadless(t, Options{Logs: logs})
	f := newSettingsForm(u)
	f.page = pageLogs
	return u, f
}

// newFakeLogs 目录里有两份日志。
func newFakeLogs() *fakeLogs {
	return &fakeLogs{
		dir:   `C:\profile\logs`,
		order: []string{"aquarius-2026-11-18.log", "aquarius-2026-11-17.log"},
		bodies: map[string]string{
			"aquarius-2026-11-18.log": "first-record\nlast-record\n",
			"aquarius-2026-11-17.log": "older\n",
		},
	}
}

// TestLogsPageListsFiles 进入本页时现取清单：两份文件都在行计划里，且各有自己的点击件。
func TestLogsPageListsFiles(t *testing.T) {
	t.Parallel()
	logs := newFakeLogs()
	_, f := logsPageUI(t, logs)
	logsPage{}.OnEnter(nil, f)

	var got []string
	for _, r := range f.pageRows(pageLogs) {
		if r.kind == rowLogFile {
			got = append(got, f.logsFiles[r.field].Name)
		}
	}
	if strings.Join(got, ",") != strings.Join(logs.order, ",") {
		t.Fatalf("文件行 = %v, want %v", got, logs.order)
	}
	// 点击件按文件名 get-or-create：同一个名字两次拿到同一个件（不会每次重建、吞掉点击）。
	if a, b := f.logFileButton(got[0]), f.logFileButton(got[0]); a == nil || a != b {
		t.Fatalf("点击件不唯一：%p vs %p", a, b)
	}
}

// TestLogsPageEmptyDir 目录里还没有文件：给一行说明，不报错。
func TestLogsPageEmptyDir(t *testing.T) {
	t.Parallel()
	_, f := logsPageUI(t, &fakeLogs{dir: "d"})
	logsPage{}.OnEnter(nil, f)
	var empty bool
	for _, r := range f.pageRows(pageLogs) {
		if r.kind == rowLogFile {
			t.Fatalf("空目录不该有文件行：%v", r)
		}
		if r.kind == rowLogEmpty {
			empty = true
		}
	}
	if !empty {
		t.Fatal("空目录应给一行说明")
	}
}

// TestLogsPageUnwired 没接读侧：只读占位，不给文件行与动作行。
func TestLogsPageUnwired(t *testing.T) {
	t.Parallel()
	_, f := logsPageUI(t, nil)
	logsPage{}.OnEnter(nil, f)
	var empty bool
	for _, r := range f.pageRows(pageLogs) {
		if r.kind == rowLogEmpty {
			empty = true
		}
		if r.kind == rowLogFile || r.kind == rowLogAction {
			t.Fatalf("未接线不该有文件行或动作行：%v", r.kind)
		}
	}
	if !empty {
		t.Fatal("未接线应给一行说明")
	}
}

// TestLogsPageOpenTail 点一份：末尾内容铺进只读次窗，反馈里点名文件。
func TestLogsPageOpenTail(t *testing.T) {
	t.Parallel()
	logs := newFakeLogs()
	u, f := logsPageUI(t, logs)
	logsPage{}.OnEnter(nil, f)
	f.openLogTail(u, "aquarius-2026-11-18.log")

	if f.logErr {
		t.Fatalf("反馈是错误：%q", f.logStatus)
	}
	c := u.rawView.Load()
	if c == nil {
		t.Fatal("次窗没收到内容")
	}
	if !strings.Contains(c.text, "last-record") || !strings.Contains(c.text, "first-record") {
		t.Fatalf("次窗内容 = %q", c.text)
	}
	if !strings.Contains(c.title, "aquarius-2026-11-18.log") {
		t.Fatalf("次窗标题 = %q，应含文件名", c.title)
	}
	if !strings.Contains(f.logStatus, "aquarius-2026-11-18.log") {
		t.Fatalf("反馈 = %q，应点名文件", f.logStatus)
	}
}

// TestLogsPageOpenTailEmpty 空文件：反馈说明还没内容，不铺一个空窗口。
func TestLogsPageOpenTailEmpty(t *testing.T) {
	t.Parallel()
	logs := newFakeLogs()
	logs.bodies["aquarius-2026-11-18.log"] = ""
	u, f := logsPageUI(t, logs)
	logsPage{}.OnEnter(nil, f)
	f.openLogTail(u, "aquarius-2026-11-18.log")

	if !f.logErr || !strings.Contains(f.logStatus, "aquarius-2026-11-18.log") {
		t.Fatalf("反馈 = (%v, %q)", f.logErr, f.logStatus)
	}
	if u.rawView.Load() != nil {
		t.Fatal("空文件不该开次窗")
	}
}

// TestLogsPageOpenTailFails 读失败：报原因，不吞。
func TestLogsPageOpenTailFails(t *testing.T) {
	t.Parallel()
	logs := newFakeLogs()
	logs.tailErr = errors.New("access denied")
	u, f := logsPageUI(t, logs)
	logsPage{}.OnEnter(nil, f)
	f.openLogTail(u, "aquarius-2026-11-18.log")

	if !f.logErr || !strings.Contains(f.logStatus, "access denied") {
		t.Fatalf("反馈 = (%v, %q)", f.logErr, f.logStatus)
	}
}

// TestLogsPageOpenDir 「打开目录」走平台层，路径由读侧给；失败就地报原因。
func TestLogsPageOpenDir(t *testing.T) {
	t.Parallel()
	logs := newFakeLogs()
	u, f := logsPageUI(t, logs)
	f.openLogsDir(u)

	plat := u.plat.(*fakePlat)
	if len(plat.OpenPaths) != 1 || plat.OpenPaths[0] != logs.dir {
		t.Fatalf("打开的路径 = %v, want [%s]", plat.OpenPaths, logs.dir)
	}
	if f.logErr || f.logStatus != i18n.T("ui.page.logs.dir_opened") {
		t.Fatalf("反馈 = (%v, %q)", f.logErr, f.logStatus)
	}

	plat.OpenPathErr = errors.New("denied by policy")
	f.openLogsDir(u)
	if !f.logErr || !strings.Contains(f.logStatus, "denied by policy") {
		t.Fatalf("失败反馈 = (%v, %q)", f.logErr, f.logStatus)
	}
}

// TestLogsPageRefresh 刷新重取清单：这期间新写的一份出现在行计划里。
func TestLogsPageRefresh(t *testing.T) {
	t.Parallel()
	logs := &fakeLogs{dir: "d", bodies: map[string]string{}}
	_, f := logsPageUI(t, logs)
	logsPage{}.OnEnter(nil, f)
	if len(f.logsFiles) != 0 {
		t.Fatalf("起手 = %v", f.logsFiles)
	}
	logs.order = []string{"aquarius-2026-11-18.log"}
	logs.bodies["aquarius-2026-11-18.log"] = "x\n"
	f.loadLogFiles()
	if len(f.logsFiles) != 1 {
		t.Fatalf("刷新后 = %v, want 1 份", f.logsFiles)
	}
}

// TestLogsPageListFails 列目录失败：给一行失败原因，页面不空转。
func TestLogsPageListFails(t *testing.T) {
	t.Parallel()
	logs := &fakeLogs{dir: "d", listErr: errors.New("permission denied")}
	_, f := logsPageUI(t, logs)
	logsPage{}.OnEnter(nil, f)
	if !f.logErr || !strings.Contains(f.logStatus, "permission denied") {
		t.Fatalf("反馈 = (%v, %q)", f.logErr, f.logStatus)
	}
}
