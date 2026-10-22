package app

// skill_admin_test.go /skill 的安装面子命令（D202）：分派、确认、未接线报错与结果排版。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeSkillAdmin 安装面替身：记下调用参数，按脚本返回。
type fakeSkillAdmin struct {
	installed []string
	removed   []string
	updated   []string
	report    SkillInstallReport
	err       error
}

func (f *fakeSkillAdmin) Install(_ context.Context, source string) (SkillInstallReport, error) {
	f.installed = append(f.installed, source)
	return f.report, f.err
}

func (f *fakeSkillAdmin) Remove(_ context.Context, name string) error {
	f.removed = append(f.removed, name)
	return f.err
}

func (f *fakeSkillAdmin) Update(_ context.Context, name string) (SkillInstallReport, error) {
	f.updated = append(f.updated, name)
	return f.report, f.err
}

// adminSession 带安装面的会话（清单可空）。
func adminSession(t *testing.T, admin SkillAdmin) *Session {
	t.Helper()
	s, _, _ := skillSession(t, nil)
	s.skillAdmin = admin
	return s
}

// TestSkillInstallSubcommand 来源整段当路径（含空格），结果列出名字与提示。
func TestSkillInstallSubcommand(t *testing.T) {
	admin := &fakeSkillAdmin{report: SkillInstallReport{
		Skills:  []string{"pdf"},
		Notes:   []string{"未检测到 git，已改用压缩包下载（更新时重新下载）"},
		Skipped: []string{"bundle/bad 已跳过：缺少 frontmatter"},
	}}
	s := adminSession(t, admin)
	out, err := s.execSkill(context.Background(),
		[]string{"install", `C:\Users\me\My Skills\pdf`, "extra"})
	if err != nil {
		t.Fatal(err)
	}
	if len(admin.installed) != 1 || admin.installed[0] != `C:\Users\me\My Skills\pdf extra` {
		t.Fatalf("来源参数 = %q（应整段当一个来源）", admin.installed)
	}
	for _, want := range []string{"已安装 1 个技能", "- pdf", "未检测到 git", "未安装的条目（1 条）"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺 %q：\n%s", want, out)
		}
	}
}

// TestSkillInstallWithoutAdmin 未接线：明确报错（不静默）。
func TestSkillInstallWithoutAdmin(t *testing.T) {
	s := adminSession(t, nil)
	_, err := s.execSkill(context.Background(), []string{"install", "/tmp/x"})
	if err == nil || !strings.Contains(err.Error(), "未接入") {
		t.Fatalf("错误 = %v", err)
	}
}

// TestSkillRemoveConfirms 卸载先二次确认；拒绝则不动手。
func TestSkillRemoveConfirms(t *testing.T) {
	admin := &fakeSkillAdmin{}
	s := adminSession(t, admin)
	s.confirmer = denyConfirmer{}
	out, err := s.execSkill(context.Background(), []string{"remove", "pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "已取消") {
		t.Fatalf("输出 = %q", out)
	}
	if len(admin.removed) != 0 {
		t.Fatalf("拒绝后仍调了 Remove：%v", admin.removed)
	}
}

// TestSkillRemoveAllowed 允许则执行并回执。
func TestSkillRemoveAllowed(t *testing.T) {
	admin := &fakeSkillAdmin{}
	s := adminSession(t, admin)
	s.confirmer = allowConfirmer{}
	out, err := s.execSkill(context.Background(), []string{"remove", "pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if len(admin.removed) != 1 || admin.removed[0] != "pdf" {
		t.Fatalf("Remove 调用 = %v", admin.removed)
	}
	if !strings.Contains(out, "已卸载技能 pdf") {
		t.Fatalf("输出 = %q", out)
	}
}

// TestSkillUpdateAll `update all` = 全部可更新项（空名字）。
func TestSkillUpdateAll(t *testing.T) {
	admin := &fakeSkillAdmin{report: SkillInstallReport{Skills: []string{"a", "b"}}}
	s := adminSession(t, admin)
	out, err := s.execSkill(context.Background(), []string{"update", "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(admin.updated) != 1 || admin.updated[0] != "" {
		t.Fatalf("Update 参数 = %v", admin.updated)
	}
	if !strings.Contains(out, "已更新 2 个技能") {
		t.Fatalf("输出 = %q", out)
	}
}

// TestSkillAdminErrorsSurface 安装面错误原样上抛（命令面负责显示）。
func TestSkillAdminErrorsSurface(t *testing.T) {
	admin := &fakeSkillAdmin{err: errors.New("同名技能已存在")}
	s := adminSession(t, admin)
	if _, err := s.execSkill(context.Background(), []string{"install", "/tmp/x"}); err == nil {
		t.Fatal("应上抛错误")
	}
	if _, err := s.execSkill(context.Background(), []string{"update"}); err == nil {
		t.Fatal("更新错误也上抛")
	}
}

// denyConfirmer / allowConfirmer 固定答复的确认器。
type denyConfirmer struct{}

func (denyConfirmer) Confirm(context.Context, string) (port.ConfirmAnswer, error) {
	return port.ConfirmAnswer{Allow: false}, nil
}

type allowConfirmer struct{}

func (allowConfirmer) Confirm(context.Context, string) (port.ConfirmAnswer, error) {
	return port.ConfirmAnswer{Allow: true}, nil
}
