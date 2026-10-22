package uigui

// 「工具与 MCP」页测试（S6/D143）：原子快照同步、异步动作回灌、未接线占位。

import (
	"image"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeToolsAdmin ToolsAdmin 测试替身（可变快照 + 动作记录）。
type fakeToolsAdmin struct {
	mu       sync.Mutex
	snap     ToolsSnapshot
	actions  []string
	grantErr error
}

func (a *fakeToolsAdmin) Snapshot() ToolsSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snap
}

func (a *fakeToolsAdmin) SetBuiltinEnabled(name string, enabled bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.snap.Builtins {
		if a.snap.Builtins[i].Name == name {
			a.snap.Builtins[i].Enabled = enabled
		}
	}
	a.snap.Revision++
	a.actions = append(a.actions, "builtin:"+name)
	return nil
}

func (a *fakeToolsAdmin) SetPluginEnabled(name string, enabled bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.snap.Plugins {
		if a.snap.Plugins[i].Name == name {
			a.snap.Plugins[i].Enabled = enabled
		}
	}
	a.snap.Revision++
	a.actions = append(a.actions, "plugin:"+name)
	return nil
}

// GrantCap 手动预授权：真的把能力加回 Granted（供「再点 = 撤销」的分支判定）。
func (a *fakeToolsAdmin) GrantCap(name string, cap string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.snap.Plugins {
		if a.snap.Plugins[i].Name == name && !hasStr(a.snap.Plugins[i].Granted, cap) {
			a.snap.Plugins[i].Granted = append(a.snap.Plugins[i].Granted, cap)
		}
	}
	a.snap.Revision++
	a.actions = append(a.actions, "grant:"+name+":"+cap)
	return a.grantErr
}

// RevokeGrant 撤销能力：真实语义 = 同时停用插件 + 清该授权（再启用重走确认）。
func (a *fakeToolsAdmin) RevokeGrant(name string, cap string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.snap.Plugins {
		if a.snap.Plugins[i].Name == name {
			a.snap.Plugins[i].Enabled = false
			a.snap.Plugins[i].Granted = delStr(a.snap.Plugins[i].Granted, cap)
		}
	}
	a.snap.Revision++
	a.actions = append(a.actions, "revoke:"+name+":"+cap)
	return nil
}

// RevokeTrust 撤销启动信任：清 Trusted + 状态转 revoked（撤销入口随之消失）。
func (a *fakeToolsAdmin) RevokeTrust(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.snap.Plugins {
		if a.snap.Plugins[i].Name == name {
			a.snap.Plugins[i].Trusted = false
			a.snap.Plugins[i].Status = "revoked"
		}
	}
	a.snap.Revision++
	a.actions = append(a.actions, "trust:"+name)
	return nil
}

func (a *fakeToolsAdmin) Logs(name string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, "logs:"+name)
	return "日志：" + name, nil
}

// actionCount / actionList 加锁读动作记录（动作 goroutine 写，测试 goroutine 读）。
func (a *fakeToolsAdmin) actionCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.actions)
}

func (a *fakeToolsAdmin) actionList() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.actions...)
}

// hasStr / delStr 小清单辅助。
func hasStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func delStr(list []string, s string) []string {
	out := list[:0:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// toolsPageUI 搭一个带假 ToolsAdmin 的 headless 设置窗。
func toolsPageUI(t *testing.T, snap ToolsSnapshot) (*UI, *settingsForm, *fakeToolsAdmin) {
	t.Helper()
	ta := &fakeToolsAdmin{snap: snap}
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return fillSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
		Tools:         ta,
	})
	f := newSettingsForm(u)
	return u, f, ta
}

func toolsFrame(f *settingsForm, u *UI) {
	var ops op.Ops
	settingsFrame(layout.Context{
		Ops:         &ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(720, 560)),
	}, testTheme(), u, f)
}

// clickToolAction 工具页动作的确定性驱动：清状态行 → 点击按钮 → 轮询帧直到「假实现已
// 记录下一个动作」且「状态行已回灌」。帧内顺序 = 分发 → 排空 → 快照同步，故回灌当帧快照
// 即已更新（连续动作的分支判定据此可证）。
func clickToolAction(t *testing.T, u *UI, f *settingsForm, ta *fakeToolsAdmin, key, wantStatus string) {
	t.Helper()
	btn := f.toolBtns[key]
	if btn == nil {
		t.Fatalf("按钮 %q 未渲染（已渲染 %d 个）", key, len(f.toolBtns))
	}
	f.toolsStatus, f.toolsErr = "", false
	wantN := ta.actionCount() + 1
	btn.Click()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		toolsFrame(f, u)
		if ta.actionCount() >= wantN && f.toolsStatus != "" {
			if f.toolsErr {
				t.Fatalf("%s 失败：%s", key, f.toolsStatus)
			}
			if !strings.Contains(f.toolsStatus, wantStatus) {
				t.Fatalf("%s 状态行 = %q, want 含 %q", key, f.toolsStatus, wantStatus)
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%s 超时：actions=%v status=%q", key, ta.actionList(), f.toolsStatus)
}

// TestToolsPageSnapshotSync 工具页动作全链路：点击 → 异步执行 → 状态行回灌 + 快照重建
// （内置启用 / 插件启停 / 逐能力撤销与预授权 / 撤销信任 / 日志）。假 ToolsAdmin 带真实
// 状态语义（撤销清授权 + 停用、预授权加回），故「同一按钮再点翻转分支」可证。
func TestToolsPageSnapshotSync(t *testing.T) {
	restoreLight(t)
	snap := ToolsSnapshot{Revision: 1,
		Builtins: []BuiltinToolState{{Name: "think", Label: "思考草稿", Enabled: false}},
		Plugins: []PluginToolState{
			{Name: "web", Source: "config", Transport: "stdio", Status: "ready", Enabled: true,
				Capabilities: []string{"network"}, Granted: []string{"network"}},
			{Name: "secure", Source: "plugins", Transport: "stdio", Status: "ready", Enabled: true, Trusted: true},
		},
	}
	u, f, ta := toolsPageUI(t, snap)
	f.navBtns[pageTools].Click()
	toolsFrame(f, u)
	if f.page != pageTools {
		t.Fatalf("page = %d, want 工具与 MCP", f.page)
	}
	// 首帧即同步快照（Revision 1 ≠ 初值 0）。
	if len(f.toolsSnap.Builtins) != 1 || len(f.toolsSnap.Plugins) != 2 {
		t.Fatalf("快照未同步: %+v", f.toolsSnap)
	}

	// ① 内置启用（think 当前禁用 → 点击 = 启用）。
	clickToolAction(t, u, f, ta, "b:think", "内置工具已更新")
	if !f.toolsSnap.Builtins[0].Enabled {
		t.Fatalf("启用后快照未更新: %+v", f.toolsSnap.Builtins[0])
	}

	// ② 插件停用（web 当前启用 → 点击 = 停用）。
	clickToolAction(t, u, f, ta, "p:web:toggle", "插件状态已更新")
	if f.toolsSnap.Plugins[0].Enabled {
		t.Fatalf("停用后快照未更新: %+v", f.toolsSnap.Plugins[0])
	}

	// ③ 逐能力撤销（network 已授权 → 撤销）：假实现同时清授权 + 停用。
	clickToolAction(t, u, f, ta, "p:web:cap:network", "已撤销能力")
	if hasStr(f.toolsSnap.Plugins[0].Granted, "network") || f.toolsSnap.Plugins[0].Enabled {
		t.Fatalf("撤销后快照未更新: %+v", f.toolsSnap.Plugins[0])
	}

	// ④ 同一按钮再点 = 未授权 → 手动预授权（分支随快照翻转）。
	clickToolAction(t, u, f, ta, "p:web:cap:network", "已预授权")
	if !hasStr(f.toolsSnap.Plugins[0].Granted, "network") {
		t.Fatalf("预授权后快照未更新: %+v", f.toolsSnap.Plugins[0])
	}

	// ⑤ 打开日志。
	clickToolAction(t, u, f, ta, "p:web:logs", "日志：web")

	// ⑥ 撤销启动信任（入口仅 source=plugins 且 Trusted 时渲染）。
	clickToolAction(t, u, f, ta, "p:secure:trust", "信任已撤销")
	if f.toolsSnap.Plugins[1].Trusted {
		t.Fatalf("撤销信任后快照未更新: %+v", f.toolsSnap.Plugins[1])
	}

	want := []string{
		"builtin:think", "plugin:web", "revoke:web:network",
		"grant:web:network", "logs:web", "trust:secure",
	}
	if got := ta.actionList(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("动作序列 = %v, want %v", got, want)
	}
}

// TestToolsPageNotWired 未接线（Tools = nil）：工具页只读占位，不空白。
func TestToolsPageNotWired(t *testing.T) {
	restoreLight(t)
	u := newHeadless(t, Options{Settings: func() SettingsSnapshot { return fillSnapshot() }})
	f := newSettingsForm(u)
	th := testTheme()
	f.navBtns[pageTools].Click()
	var ops op.Ops
	settingsFrame(layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}, th, u, f)
	if f.page != pageTools {
		t.Fatalf("page = %d", f.page)
	}
	rows := f.pageRows(pageTools)
	if len(rows) < 2 || rows[0].kind != rowTitle || rows[1].kind != rowToolsEmpty {
		t.Fatalf("未接线页行计划 = %+v, want 标题 + 占位", rows)
	}
}
