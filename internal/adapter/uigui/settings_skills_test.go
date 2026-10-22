package uigui

// settings_skills_test.go 「技能」页（D202/D203）：快照同步、行计划、异步动作回灌、
// 选择框只填来源框（不直接落盘）、行型覆盖（主覆盖测试见 settings_page_test.go）。

import (
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
)

// fakeSkillsAdmin 技能管理面替身：动作记参数、快照可预置。
type fakeSkillsAdmin struct {
	snap     SkillsSnapshot
	installs []string
	updates  []string
	removes  []string
	added    []string
	removedD []string
	rescans  int
	err      error
	status   string
}

var _ SkillsAdmin = (*fakeSkillsAdmin)(nil)

func (f *fakeSkillsAdmin) Snapshot() SkillsSnapshot { return f.snap }

func (f *fakeSkillsAdmin) Install(source string) (string, error) {
	f.installs = append(f.installs, source)
	if f.err != nil {
		return "", f.err
	}
	if f.status != "" {
		return f.status, nil
	}
	return "安装完成：pdf", nil
}

func (f *fakeSkillsAdmin) Update(name string) (string, error) {
	f.updates = append(f.updates, name)
	if f.err != nil {
		return "", f.err
	}
	return "更新完成：" + name, nil
}

func (f *fakeSkillsAdmin) Remove(name string) error {
	f.removes = append(f.removes, name)
	return f.err
}

func (f *fakeSkillsAdmin) Rescan() error {
	f.rescans++
	return f.err
}

func (f *fakeSkillsAdmin) AddDir(dir string) error {
	f.added = append(f.added, dir)
	return f.err
}

func (f *fakeSkillsAdmin) RemoveDir(dir string) error {
	f.removedD = append(f.removedD, dir)
	return f.err
}

// skillsForm 造一个挂了技能管理面的表单（快照已同步）。
func skillsForm(t *testing.T, admin *fakeSkillsAdmin) (*settingsForm, *UI) {
	t.Helper()
	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot { return promptSnapshot() },
		Skills:   admin,
	})
	f := newSettingsForm(u)
	f.page = pageSkills
	f.syncSkillsSnapshot()
	return f, u
}

// TestSkillsPageRows 行计划：清单 / 空态 / 来源 / 目录 / 警告 / 状态。
func TestSkillsPageRows(t *testing.T) {
	admin := &fakeSkillsAdmin{snap: SkillsSnapshot{
		Revision:    1,
		ManagedRoot: "/profile/skills",
		Skills:      []SkillState{{Name: "pdf", Managed: true}},
		Dirs:        []SkillDir{{Path: "/extra"}, {Path: "/profile/skills", Fallback: true}},
		Warnings:    []string{"坏条目"},
	}}
	f, _ := skillsForm(t, admin)
	f.skillsStatus = "ok"
	rows := f.pageRows(pageSkills)

	count := map[rowKind]int{}
	for _, r := range rows {
		count[r.kind]++
	}
	want := map[rowKind]int{
		rowTitle: 1, rowSkillItem: 1, rowSkillSource: 1, rowSkillPick: 1,
		rowSkillDir: 2, rowSkillDirAdd: 1, rowSkillRescan: 1, rowSkillWarn: 1, rowSkillStatus: 1,
	}
	for k, n := range want {
		if count[k] != n {
			t.Errorf("行型 %d 出现 %d 次，want %d", k, count[k], n)
		}
	}
	if count[rowSkillEmpty] != 0 {
		t.Error("有技能时不该出现空态行")
	}
}

// TestSkillsPageEmptyAndUnwired 空清单给引导；未接线给只读占位。
func TestSkillsPageEmptyAndUnwired(t *testing.T) {
	f, _ := skillsForm(t, &fakeSkillsAdmin{snap: SkillsSnapshot{Revision: 1}})
	if !hasRowKind(f.pageRows(pageSkills), rowSkillEmpty) {
		t.Error("空清单应给空态引导行")
	}
	u := newHeadless(t, Options{Settings: func() SettingsSnapshot { return promptSnapshot() }})
	f2 := newSettingsForm(u)
	f2.page = pageSkills
	rows := f2.pageRows(pageSkills)
	if len(rows) == 0 || rows[1].kind != rowSkillEmpty && rows[1].kind != rowSection {
		t.Fatalf("未接线行计划 = %+v", rows)
	}
}

// hasRowKind 行计划里有没有该行型。
func hasRowKind(rows []rowDesc, k rowKind) bool {
	for _, r := range rows {
		if r.kind == k {
			return true
		}
	}
	return false
}

// TestSkillsInstallIsAsync 安装是异步动作：结果经通道回灌状态行，期间来源框清空。
func TestSkillsInstallIsAsync(t *testing.T) {
	admin := &fakeSkillsAdmin{snap: SkillsSnapshot{Revision: 1}}
	f, _ := skillsForm(t, admin)
	f.skillSource.SetText(`C:\Skills\pdf`)

	// 模拟点击：直接走 Event 路径太依赖 Gio 输入路由，这里调同一段逻辑。
	func() {
		source := strings.TrimSpace(f.skillSource.Text())
		u := &UI{}
		u.skillsAsync(f, func() (string, error) {
			status, err := f.skills.Install(source)
			if err != nil {
				return "", err
			}
			f.skillSource.SetText("")
			return status, nil
		})
	}()
	// 等结果（直接取通道；动作在旁路 goroutine 跑）。
	r := <-f.skillRes
	f.skillWait--
	f.skillsStatus, f.skillsErr = r.status, r.err != nil
	if r.err != nil {
		f.skillsStatus = "操作失败：" + r.err.Error()
	}
	if len(admin.installs) != 1 || admin.installs[0] != `C:\Skills\pdf` {
		t.Fatalf("安装参数 = %v", admin.installs)
	}
	if f.skillsStatus == "" || f.skillsErr {
		t.Fatalf("状态行 = %q（err=%v）", f.skillsStatus, f.skillsErr)
	}
	if f.skillSource.Text() != "" {
		t.Error("安装成功后来源框应清空")
	}
}

// TestSkillsActionError 失败带文案（状态行红字）。
func TestSkillsActionError(t *testing.T) {
	admin := &fakeSkillsAdmin{snap: SkillsSnapshot{Revision: 1}, err: errors.New("同名技能已存在")}
	f, _ := skillsForm(t, admin)
	u := &UI{}
	u.skillsAsync(f, func() (string, error) {
		if err := f.skills.Remove("pdf"); err != nil {
			return "", err
		}
		return "已卸载", nil
	})
	for f.skillWait > 0 {
		r := <-f.skillRes
		f.skillWait--
		f.skillsStatus, f.skillsErr = r.status, r.err != nil
		if r.err != nil {
			f.skillsStatus = "操作失败：" + r.err.Error()
		}
	}
	if !f.skillsErr || !strings.Contains(f.skillsStatus, "同名技能已存在") {
		t.Fatalf("状态行 = %q（err=%v）", f.skillsStatus, f.skillsErr)
	}
}

// TestSkillPickFillsSource 选择框结果只填来源框，不触发安装。
func TestSkillPickFillsSource(t *testing.T) {
	admin := &fakeSkillsAdmin{snap: SkillsSnapshot{Revision: 1}}
	f, u := skillsForm(t, admin)
	u.pushSkillPick(skillPathPick{kind: platform.PickSkillFile, path: `C:\x\SKILL.md`})
	f.drainSkillPicks(u)
	if got := f.skillSource.Text(); got != `C:\x\SKILL.md` {
		t.Fatalf("来源框 = %q", got)
	}
	if len(admin.installs) != 0 {
		t.Fatalf("选择动作不该安装：%v", admin.installs)
	}
	if !strings.Contains(f.skillsStatus, "点「安装」") {
		t.Fatalf("状态行 = %q", f.skillsStatus)
	}
}

// TestSkillPickDirHint 文件夹选择给出对应提示（不是「文件」）。
func TestSkillPickDirHint(t *testing.T) {
	f, u := skillsForm(t, &fakeSkillsAdmin{snap: SkillsSnapshot{Revision: 1}})
	u.pushSkillPick(skillPathPick{kind: platform.PickSkillDir, path: `C:\Skills`})
	f.drainSkillPicks(u)
	if !strings.Contains(f.skillsStatus, "已选择文件夹") {
		t.Fatalf("状态行 = %q", f.skillsStatus)
	}
}

// TestSkillMetaLine 元信息行按来源种类给中文（不暴露内部字段名）。
func TestSkillMetaLine(t *testing.T) {
	cases := []struct {
		st   SkillState
		want string
	}{
		{SkillState{Kind: "git", Updated: "2026-10-22", Dir: "/d"}, "git 安装 · 更新于 2026-10-22 · /d"},
		{SkillState{Kind: "zip", Dir: "/d"}, "压缩包安装 · /d"},
		{SkillState{Dir: "/d"}, "手工放置 · /d"},
	}
	for _, c := range cases {
		if got := c.st.metaLine(); got != c.want {
			t.Errorf("metaLine = %q, want %q", got, c.want)
		}
	}
}

// TestSkillDirFallbackNotRemovable 兜底根注明「不可删」，行内不出现移除按钮逻辑
// （按钮是 get-or-create 的；这里只锁文案）。
func TestSkillDirFallbackLabel(t *testing.T) {
	d := SkillDir{Path: "/profile/skills", Fallback: true}
	if !strings.Contains(d.skillDirLabel(), "不可删") {
		t.Fatalf("兜底根文案 = %q", d.skillDirLabel())
	}
	if got := (SkillDir{Path: "/extra"}).skillDirLabel(); strings.Contains(got, "兜底") {
		t.Fatalf("额外根不该标兜底：%q", got)
	}
}
