package uigui

// 「外部代理」页测试（D270）：行计划、命令行编辑档、三个动作的写回语义、未接线占位。

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// errFakeWrite 写回失败的替身错误（状态行要把它说出来）。
var errFakeWrite = errors.New("写不进 config")

// fakeDelegateAdmin DelegateAdmin 测试替身（快照可变 + 动作记录）。
type fakeDelegateAdmin struct {
	mu      sync.Mutex
	snap    DelegateSnapshot
	actions []string
	err     error
}

func (a *fakeDelegateAdmin) Snapshot() DelegateSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snap
}

func (a *fakeDelegateAdmin) SetCommandLine(name, commandLine string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	for i := range a.snap.Agents {
		if a.snap.Agents[i].Name == name {
			a.snap.Agents[i].CommandLine = commandLine
			a.snap.Agents[i].Available = true
		}
	}
	a.actions = append(a.actions, "save:"+name+"="+commandLine)
	return nil
}

func (a *fakeDelegateAdmin) SetEnabled(name string, enabled bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	for i := range a.snap.Agents {
		if a.snap.Agents[i].Name == name {
			a.snap.Agents[i].Enabled = enabled
		}
	}
	a.actions = append(a.actions, "on:"+name)
	return nil
}

func (a *fakeDelegateAdmin) SetDefault(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	for i := range a.snap.Agents {
		a.snap.Agents[i].Default = a.snap.Agents[i].Name == name
	}
	a.actions = append(a.actions, "def:"+name)
	return nil
}

// delegateFixture 两个代理：一个可用且缺省，一个不可用。
func delegateFixture() *fakeDelegateAdmin {
	return &fakeDelegateAdmin{snap: DelegateSnapshot{Agents: []DelegateAgentState{
		{Name: "opencode", CommandLine: "opencode acp", Available: true, Enabled: true, Default: true,
			Title: "opencode", Version: "2.0.15"},
		{Name: "ghost", CommandLine: "ghost-agent", Available: false, Enabled: false, Note: "命令不在 PATH 上：ghost-agent"},
	}}}
}

// openDelegatePage 打开设置窗并切到「外部代理」页（跑一帧；行计划与编辑档这时才铺好）。
func openDelegatePage(t *testing.T, opts Options) (*UI, *settingsForm) {
	t.Helper()
	u := newHeadless(t, opts)
	f := newSettingsForm(u)
	f.navBtns[pageDelegates].Click()
	var ops op.Ops
	settingsFrame(layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}, testTheme(), u, f)
	if f.page != pageDelegates {
		t.Fatalf("page = %d, want %d", f.page, pageDelegates)
	}
	return u, f
}

// TestDelegatePageRows 接线时：一条代理一块 + 「重新检测」，命令行编辑档预填配置里的值。
func TestDelegatePageRows(t *testing.T) {
	restoreLight(t)
	fake := delegateFixture()
	_, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})
	rows := f.pageRows(pageDelegates)
	items, refresh := 0, 0
	for _, r := range rows {
		switch r.kind {
		case rowDlgItem:
			items++
		case rowDlgRefresh:
			refresh++
		}
	}
	if items != 2 || refresh != 1 {
		t.Fatalf("行计划 = %+v（want 2 条代理 + 1 个重新检测）", rows)
	}
	if got := f.delegateEditor("opencode").Text(); got != "opencode acp" {
		t.Fatalf("命令行编辑档 = %q, want 配置里的值", got)
	}
	// 标题/版本/不可用原因各在自己的位置（第二行脚注）。
	if line := fake.snap.Agents[0].metaLine(); !strings.Contains(line, "opencode 2.0.15") {
		t.Fatalf("metaLine = %q", line)
	}
	if line := fake.snap.Agents[1].metaLine(); !strings.Contains(line, "不在 PATH 上") {
		t.Fatalf("metaLine = %q", line)
	}
}

// TestDelegatePageNotWired 未接线：只读占位，不空白也不崩。
func TestDelegatePageNotWired(t *testing.T) {
	restoreLight(t)
	_, f := openDelegatePage(t, Options{Settings: func() SettingsSnapshot { return fillSnapshot() }})
	rows := f.pageRows(pageDelegates)
	if len(rows) < 2 || rows[0].kind != rowTitle || rows[1].kind != rowDlgEmpty {
		t.Fatalf("未接线页行计划 = %+v, want 标题 + 占位", rows)
	}
}

// TestDelegateActions 三个动作：保存命令行 / 启停 / 设缺省，反馈都带「重启生效」。
func TestDelegateActions(t *testing.T) {
	restoreLight(t)
	fake := delegateFixture()
	u, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})

	// 保存：编辑器里的字就是新命令行；成功后编辑档对齐回配置值。
	f.delegateEditor("opencode").SetText(`"C:\tools\oc.exe" acp --stdio`)
	u.dispatchDelegateAction(f, "save:opencode")
	if f.delegateErr {
		t.Fatalf("保存失败: %s", f.delegateStatus)
	}
	if got := f.delegateEditor("opencode").Text(); got != `"C:\tools\oc.exe" acp --stdio` {
		t.Fatalf("保存后编辑档 = %q（应与配置值一致）", got)
	}

	// 空命令行：拦下，不写回。
	f.delegateEditor("ghost").SetText("   ")
	u.dispatchDelegateAction(f, "save:ghost")
	if !f.delegateErr || f.delegateStatus != i18n.T("ui.settings.dlg.cmd_empty") {
		t.Fatalf("空命令行应拦下: %v %q", f.delegateErr, f.delegateStatus)
	}

	// 启停与设缺省。
	u.dispatchDelegateAction(f, "on:ghost")
	u.dispatchDelegateAction(f, "def:ghost")
	want := []string{
		`save:opencode="C:\tools\oc.exe" acp --stdio`,
		"on:ghost",
		"def:ghost",
	}
	if strings.Join(fake.actions, "|") != strings.Join(want, "|") {
		t.Fatalf("动作序列 = %v, want %v", fake.actions, want)
	}
	for _, a := range fake.snap.Agents {
		if a.Name == "ghost" && (!a.Default || !a.Enabled) {
			t.Fatalf("ghost 应已缺省且启用: %+v", a)
		}
	}
	if want := i18n.Tf("ui.settings.dlg.set_default_done", "name", "ghost"); f.delegateStatus != want {
		t.Fatalf("状态行应说清重启生效: %q, want %q", f.delegateStatus, want)
	}
}

// TestDelegateActionError 写盘失败：状态行报因，不静默。
func TestDelegateActionError(t *testing.T) {
	restoreLight(t)
	fake := delegateFixture()
	fake.err = errFakeWrite
	u, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})
	u.dispatchDelegateAction(f, "def:ghost")
	if !f.delegateErr || !strings.Contains(f.delegateStatus, "写不进") {
		t.Fatalf("失败应报到状态行: %v %q", f.delegateErr, f.delegateStatus)
	}
}
