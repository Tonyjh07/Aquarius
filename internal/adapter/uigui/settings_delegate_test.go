package uigui

// 「外部代理」页测试（D270）：行计划、命令行编辑档、三个动作的写回语义、未接线占位。

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// errFakeWrite 写回失败的替身错误（状态行要把它说出来）。
var errFakeWrite = errors.New("写不进 config")

// fakeDelegateAdmin DelegateAdmin 测试替身（快照可变 + 动作记录）。
type fakeDelegateAdmin struct {
	mu        sync.Mutex
	snap      DelegateSnapshot
	actions   []string
	err       error
	probeInfo DelegateProbe
	probeErr  error
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

// agent 锁内的条目查找（替身只在这一处按名字取，其余动作共用）。
func (a *fakeDelegateAdmin) agent(name string) *DelegateAgentState {
	for i := range a.snap.Agents {
		if a.snap.Agents[i].Name == name {
			return &a.snap.Agents[i]
		}
	}
	return nil
}

func (a *fakeDelegateAdmin) Add(name, commandLine string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	command, args, _ := strings.Cut(commandLine, " ")
	a.snap.Agents = append(a.snap.Agents, DelegateAgentState{
		Name: name, CommandLine: commandLine, Command: command, Args: strings.Fields(args),
		Available: true, Enabled: true,
	})
	a.actions = append(a.actions, "add:"+name+"="+commandLine)
	return nil
}

func (a *fakeDelegateAdmin) Remove(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	out := a.snap.Agents[:0]
	for _, st := range a.snap.Agents {
		if st.Name != name {
			out = append(out, st)
		}
	}
	a.snap.Agents = out
	a.actions = append(a.actions, "del:"+name)
	return nil
}

func (a *fakeDelegateAdmin) Rename(oldName, newName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	if st := a.agent(oldName); st != nil {
		st.Name = newName
	}
	a.actions = append(a.actions, "rename:"+oldName+"="+newName)
	return nil
}

func (a *fakeDelegateAdmin) SetCommand(name, command string) error {
	return a.editField(name, "cmd", func(st *DelegateAgentState) { st.Command = command })
}

func (a *fakeDelegateAdmin) SetArgs(name string, args []string) error {
	return a.editField(name, "args", func(st *DelegateAgentState) { st.Args = args })
}

func (a *fakeDelegateAdmin) SetEnv(name string, env map[string]string) error {
	return a.editField(name, "env", func(st *DelegateAgentState) { st.Env = env })
}

func (a *fakeDelegateAdmin) SetWorkdir(name, dir string) error {
	return a.editField(name, "workdir", func(st *DelegateAgentState) { st.WorkDir = dir })
}

// Probe 探测替身：回执与失败都可预设（测试不真起进程）。
func (a *fakeDelegateAdmin) Probe(name string) (DelegateProbe, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, "probe:"+name)
	return a.probeInfo, a.probeErr
}

// editField 字段级动作共用：记账 + 改快照里的那一条。
func (a *fakeDelegateAdmin) editField(name, action string, apply func(*DelegateAgentState)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	if st := a.agent(name); st != nil {
		apply(st)
	}
	a.actions = append(a.actions, action+":"+name)
	return nil
}

// delegateFixture 两个代理：一个可用且缺省，一个不可用。
func delegateFixture() *fakeDelegateAdmin {
	return &fakeDelegateAdmin{snap: DelegateSnapshot{Agents: []DelegateAgentState{
		{Name: "opencode", CommandLine: "opencode acp", Command: "opencode", Args: []string{"acp"},
			Available: true, Enabled: true, Default: true, Title: "opencode", Version: "2.0.15"},
		{Name: "ghost", CommandLine: "ghost-agent", Command: "ghost-agent",
			Available: false, Enabled: false, Note: "命令不在 PATH 上：ghost-agent"},
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

// TestDelegatePageRows 接线时：一条代理一块 + 新增区 + 「重新检测」；收起态不铺展开字段。
func TestDelegatePageRows(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	fake := delegateFixture()
	_, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})
	rows := f.pageRows(pageDelegates)
	items, refresh, add, expanded := 0, 0, 0, 0
	for _, r := range rows {
		switch r.kind {
		case rowDlgItem:
			items++
		case rowDlgAdd:
			add++
		case rowDlgRefresh:
			refresh++
		case rowDlgCmd, rowDlgArgs, rowDlgEnv, rowDlgWorkdir, rowDlgSave:
			expanded++
		}
	}
	if items != 2 || refresh != 1 || add != 1 || expanded != 0 {
		t.Fatalf("行计划 = %+v（want 2 条代理 + 新增 + 重新检测，且全部收起）", rows)
	}
	// 标题/版本/不可用原因各在自己的位置（第二行脚注）。
	if line := fake.snap.Agents[0].metaLine(); !strings.Contains(line, "opencode 2.0.15") {
		t.Fatalf("metaLine = %q", line)
	}
	if line := fake.snap.Agents[1].metaLine(); !strings.Contains(line, "不在 PATH 上") {
		t.Fatalf("metaLine = %q", line)
	}
}

// TestDelegateExpand 展开一条：六个字段行 + 保存行 + 探测行都在，草稿按快照预填。
func TestDelegateExpand(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	fake := delegateFixture()
	fake.snap.Agents[0].Command = "opencode"
	fake.snap.Agents[0].Args = []string{"acp", "--stdio"}
	fake.snap.Agents[0].Env = map[string]string{"KEY": "secret:openai"}
	fake.snap.Agents[0].WorkDir = "/tmp/work"
	u, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})

	u.dispatchDelegateAction(f, "open:opencode")
	if f.delegateOpen != "opencode" {
		t.Fatalf("展开态 = %q", f.delegateOpen)
	}
	counts := map[rowKind]int{}
	for _, r := range f.pageRows(pageDelegates) {
		counts[r.kind]++
	}
	for _, k := range []rowKind{rowDlgCmd, rowDlgArgs, rowDlgEnv, rowDlgWorkdir, rowDlgSave, rowDlgProbe} {
		if counts[k] != 1 {
			t.Fatalf("展开后 %v 行数 = %d, want 1（行计划 = %+v）", k, counts[k], f.pageRows(pageDelegates))
		}
	}

	df := f.delegateFormFor("opencode")
	if got := df.cmd.Text(); got != "opencode" {
		t.Fatalf("命令档 = %q", got)
	}
	if got := df.args.Text(); got != "acp\n--stdio" {
		t.Fatalf("参数档 = %q", got)
	}
	if got := df.env.Text(); got != "KEY=secret:openai" {
		t.Fatalf("环境档 = %q", got)
	}
	if got := df.workdir.Text(); got != "/tmp/work" {
		t.Fatalf("工作目录档 = %q", got)
	}

	// 收起后展开字段全部消失。
	u.dispatchDelegateAction(f, "close:opencode")
	for _, r := range f.pageRows(pageDelegates) {
		if r.kind >= rowDlgCmd && r.kind <= rowDlgProbe {
			t.Fatalf("收起后不该再有展开行: %+v", r)
		}
	}
}

// TestDelegateSaveFields 保存整块字段：逐项落到替身，状态行说清重启生效。
func TestDelegateSaveFields(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	fake := delegateFixture()
	u, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})
	u.dispatchDelegateAction(f, "open:opencode")
	df := f.delegateFormFor("opencode")
	df.cmd.SetText("  /opt/oc  ")
	df.args.SetText("acp\n\n --stdio ")
	df.env.SetText("# 注释\nKEY=secret:openai\n")
	df.workdir.SetText("/tmp/work")

	u.dispatchDelegateAction(f, "save:opencode")
	if f.delegateErr {
		t.Fatalf("保存失败: %s", f.delegateStat)
	}
	want := []string{"cmd:opencode", "args:opencode", "env:opencode", "workdir:opencode"}
	got := fake.actions
	// 展开与保存不记账，替身只记字段动作：前五项应与 want 一致（忽略顺序之外的东西）。
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("动作序列 = %v, want %v", got, want)
	}
	st := fake.snap.Agents[0]
	if st.Command != "/opt/oc" {
		t.Fatalf("命令 = %q（应去空白）", st.Command)
	}
	if len(st.Args) != 2 || st.Args[0] != "acp" || st.Args[1] != "--stdio" {
		t.Fatalf("参数 = %v（应去空行）", st.Args)
	}
	if st.Env["KEY"] != "secret:openai" || len(st.Env) != 1 {
		t.Fatalf("环境 = %v（注释与空行应忽略）", st.Env)
	}
	if want := i18n.Tf("ui.settings.dlg.saved", "name", "opencode"); f.delegateStat != want {
		t.Fatalf("状态行 = %q, want %q", f.delegateStat, want)
	}
}

// TestDelegateSaveValidation 保存时的本地校验：空命令、坏的环境行都拦在写之前。
func TestDelegateSaveValidation(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	fake := delegateFixture()
	u, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})
	u.dispatchDelegateAction(f, "open:opencode")
	df := f.delegateFormFor("opencode")

	df.cmd.SetText("   ")
	u.dispatchDelegateAction(f, "save:opencode")
	if !f.delegateErr || !strings.Contains(f.delegateStat, i18n.T("cmd.delegate.err.command_required")) {
		t.Fatalf("空命令应拦下: %v %q", f.delegateErr, f.delegateStat)
	}

	df.cmd.SetText("opencode")
	df.env.SetText("NOT_A_PAIR")
	u.dispatchDelegateAction(f, "save:opencode")
	if !f.delegateErr || !strings.Contains(f.delegateStat, "KEY=VALUE") {
		t.Fatalf("坏的环境行应拦下: %v %q", f.delegateErr, f.delegateStat)
	}
	if len(fake.actions) != 0 {
		t.Fatalf("校验失败不该写盘: %v", fake.actions)
	}
}

// TestDelegateRename 改名：先改名，其余字段按新名字写。
func TestDelegateRename(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	fake := delegateFixture()
	u, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})
	u.dispatchDelegateAction(f, "open:opencode")
	df := f.delegateFormFor("opencode")
	df.name.SetText("oc2")
	u.dispatchDelegateAction(f, "save:opencode")
	if f.delegateErr {
		t.Fatalf("改名保存失败: %s", f.delegateStat)
	}
	if f.delegateOpen != "oc2" {
		t.Fatalf("展开态应跟到新名字: %q", f.delegateOpen)
	}
	if _, ok := f.delegateForms["opencode"]; ok {
		t.Fatal("旧名字的草稿档该被丢掉")
	}
	if f.delegateForms["oc2"] == nil {
		t.Fatal("新名字该有草稿档")
	}
	if fake.actions[0] != "rename:opencode=oc2" {
		t.Fatalf("动作序列 = %v（改名应排第一）", fake.actions)
	}
}

// TestDelegateAddAndRemove 新增与删除：删除要过一道行内确认，确认后才真删。
func TestDelegateAddAndRemove(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	fake := delegateFixture()
	u, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})

	// 新增：名字与命令行都必须有。
	u.dispatchDelegateAction(f, "add:")
	if !f.delegateErr {
		t.Fatal("空名字应拦下")
	}
	f.delegateAddN.SetText("newbie")
	u.dispatchDelegateAction(f, "add:")
	if !f.delegateErr {
		t.Fatal("空命令行应拦下")
	}
	f.delegateAddC.SetText("newbie acp")
	u.dispatchDelegateAction(f, "add:")
	if f.delegateErr {
		t.Fatalf("新增失败: %s", f.delegateStat)
	}
	if f.delegateAddN.Text() != "" || f.delegateAddC.Text() != "" {
		t.Fatal("新增成功后输入档应清空")
	}

	// 删除：第一次点只进确认态，不写盘。
	u.dispatchDelegateAction(f, "del:ghost")
	if f.delegateConfr != "ghost" {
		t.Fatalf("确认态 = %q", f.delegateConfr)
	}
	if strings.Contains(strings.Join(fake.actions, "|"), "del:") {
		t.Fatalf("进确认态就删了: %v", fake.actions)
	}
	u.dispatchDelegateAction(f, "delok:ghost")
	if f.delegateErr {
		t.Fatalf("删除失败: %s", f.delegateStat)
	}
	if f.delegateConfr != "" {
		t.Fatal("删除后确认态应清掉")
	}
	if !strings.Contains(strings.Join(fake.actions, "|"), "del:ghost") {
		t.Fatalf("动作序列 = %v", fake.actions)
	}
}

// TestDelegateProbe 测试连接：走旁路 goroutine，结果回到状态行与回执行；在途时不重复起进程。
func TestDelegateProbe(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	fake := delegateFixture()
	fake.probeInfo = DelegateProbe{
		ProtocolVersion: 1, Title: "opencode", Version: "2.0.15",
		AuthMethods: []string{"Log in first"}, LoadSession: true,
	}
	u, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})
	u.dispatchDelegateAction(f, "open:opencode")
	u.dispatchDelegateAction(f, "probe:opencode")
	if !f.delegateBusy["opencode"] {
		t.Fatal("探测在途应标记 busy")
	}

	// 等结果回灌（替身立即返回，只需让出一两帧）。
	deadline := time.Now().Add(2 * time.Second)
	for f.delegateBusy["opencode"] && time.Now().Before(deadline) {
		f.consumeDelegateProbes()
		time.Sleep(5 * time.Millisecond)
	}
	f.consumeDelegateProbes()
	if f.delegateBusy["opencode"] {
		t.Fatal("结果没回灌")
	}
	out, ok := f.delegateProbe["opencode"]
	if !ok || out.err != nil {
		t.Fatalf("回执行 = %+v", out)
	}
	if !strings.Contains(f.delegateStat, "opencode") || !strings.Contains(f.delegateStat, "v1") {
		t.Fatalf("状态行 = %q", f.delegateStat)
	}
	// 能力行只列申报为 true 的项。
	line := delegateCapLine(out.info)
	if !strings.Contains(line, i18n.T("ui.settings.dlg.cap_load")) ||
		strings.Contains(line, i18n.T("ui.settings.dlg.cap_image")) {
		t.Fatalf("能力行 = %q", line)
	}
	if !strings.Contains(line, "Log in first") {
		t.Fatalf("登录方式应出现在能力行: %q", line)
	}
}

// TestDelegateProbeFailure 探测失败：状态行与回执行都报因，不静默。
func TestDelegateProbeFailure(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	fake := delegateFixture()
	fake.probeErr = errors.New(i18n.T("acp.probe.err.handshake") + ": EOF")
	u, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})
	u.dispatchDelegateAction(f, "probe:ghost")
	deadline := time.Now().Add(2 * time.Second)
	for f.delegateBusy["ghost"] && time.Now().Before(deadline) {
		f.consumeDelegateProbes()
		time.Sleep(5 * time.Millisecond)
	}
	f.consumeDelegateProbes()
	if !f.delegateErr || !strings.Contains(f.delegateStat, i18n.T("acp.probe.err.handshake")) {
		t.Fatalf("失败应报到状态行: %v %q", f.delegateErr, f.delegateStat)
	}
}

// TestDelegatePageNotWired 未接线：只读占位，不空白也不崩。
func TestDelegatePageNotWired(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	_, f := openDelegatePage(t, Options{Settings: func() SettingsSnapshot { return fillSnapshot() }})
	rows := f.pageRows(pageDelegates)
	if len(rows) < 2 || rows[0].kind != rowTitle || rows[1].kind != rowDlgEmpty {
		t.Fatalf("未接线页行计划 = %+v, want 标题 + 占位", rows)
	}
}

// TestDelegateActionError 写盘失败：状态行报因，不静默。
func TestDelegateActionError(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	fake := delegateFixture()
	fake.err = errFakeWrite
	u, f := openDelegatePage(t, Options{
		Settings:  func() SettingsSnapshot { return fillSnapshot() },
		Delegates: fake,
	})
	u.dispatchDelegateAction(f, "def:ghost")
	if !f.delegateErr || !strings.Contains(f.delegateStat, "写不进") {
		t.Fatalf("失败应报到状态行: %v %q", f.delegateErr, f.delegateStat)
	}
}
