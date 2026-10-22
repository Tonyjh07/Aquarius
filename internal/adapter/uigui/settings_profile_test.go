package uigui

// Profile 区测试（D110③）：快照填表、动作分派、未接线反馈。

import (
	"errors"
	"strings"
	"testing"
)

// fakeProfiles ProfilesManager 假实现（动作分派与反馈断言用）。
type fakeProfiles struct {
	snap     ProfilesSnapshot
	created  []string
	copied   []string
	switched []string
	errOn    map[string]error // 动作名 → 注入错误
}

func (f *fakeProfiles) Snapshot() ProfilesSnapshot { return f.snap }
func (f *fakeProfiles) Create(name string) error {
	f.created = append(f.created, name)
	return f.errOn["create"]
}
func (f *fakeProfiles) Copy(name string) error {
	f.copied = append(f.copied, name)
	return f.errOn["copy"]
}
func (f *fakeProfiles) Switch(name string) error {
	f.switched = append(f.switched, name)
	return f.errOn["switch"]
}

// TestProfileActionsHeadless profile 三动作：分派经 ProfilesManager、快照刷新、
// 成功/失败反馈文案、空名拦截、未接线报错。
func TestProfileActionsHeadless(t *testing.T) {
	mgr := &fakeProfiles{snap: ProfilesSnapshot{Current: "default", Items: []string{"default"}}}
	u := newHeadless(t, Options{Profiles: mgr})
	f := newSettingsForm(u)
	if f.profiles.Current != "default" || len(f.profiles.Items) != 1 {
		t.Fatalf("开窗快照 = %+v", f.profiles)
	}

	// 空名拦截：三动作都不触达。
	for _, act := range []string{"create", "copy", "switch"} {
		u.profileAction(f, act)
		if !f.err || !strings.Contains(f.status, "填写 profile 名") {
			t.Fatalf("%s 空名: (%v, %q)", act, f.err, f.status)
		}
	}
	if len(mgr.created)+len(mgr.copied)+len(mgr.switched) != 0 {
		t.Fatal("空名不应触达管理面")
	}

	// 新建成功：分派 + 清单刷新 + 反馈带重启提示。
	f.profName.SetText("work")
	u.profileAction(f, "create")
	if f.err || len(mgr.created) != 1 || mgr.created[0] != "work" {
		t.Fatalf("create 分派 = %+v (%v, %q)", mgr.created, f.err, f.status)
	}
	if !strings.Contains(f.status, "重启") {
		t.Fatalf("create 反馈 = %q, want 重启提示", f.status)
	}
	if !strings.Contains(f.status, "切换") {
		t.Fatalf("create 反馈 = %q, want 引导切换", f.status)
	}

	// 复制成功：分派 + 「仅配置」语义提示。
	mgr.snap = ProfilesSnapshot{Current: "default", Items: []string{"default", "work"}}
	u.profileAction(f, "copy")
	if f.err || len(mgr.copied) != 1 || mgr.copied[0] != "work" {
		t.Fatalf("copy 分派 = %+v", mgr.copied)
	}
	if !strings.Contains(f.status, "仅配置") {
		t.Fatalf("copy 反馈 = %q, want 仅配置语义", f.status)
	}

	// 切换成功：分派 + 重启生效提示。
	u.profileAction(f, "switch")
	if f.err || len(mgr.switched) != 1 || mgr.switched[0] != "work" {
		t.Fatalf("switch 分派 = %+v", mgr.switched)
	}
	if !strings.Contains(f.status, "已切换 profile → work") || !strings.Contains(f.status, "重启") {
		t.Fatalf("switch 反馈 = %q", f.status)
	}

	// 失败路径：错误红字带动作名与报因。
	mgr.errOn = map[string]error{"switch": errors.New("profile %q 不存在")}
	f.profName.SetText("nope")
	u.profileAction(f, "switch")
	if !f.err || !strings.Contains(f.status, "切换失败") || !strings.Contains(f.status, "不存在") {
		t.Fatalf("switch 失败反馈 = (%v, %q)", f.err, f.status)
	}
}

// TestProfileActionsUnwired 未接线（TUI/repl 或老装配）时 profile 动作报错不触达。
func TestProfileActionsUnwired(t *testing.T) {
	u := newHeadless(t, Options{})
	f := newSettingsForm(u)
	f.profName.SetText("x")
	u.profileAction(f, "create")
	if !f.err || !strings.Contains(f.status, "未接线") {
		t.Fatalf("未接线反馈 = (%v, %q)", f.err, f.status)
	}
}
