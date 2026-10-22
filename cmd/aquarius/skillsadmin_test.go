package main

// 技能管理面装配根侧测试（D202/D203）：用真实 skillfs.Catalog + Installer（临时目录）跑，
// 锁「安装即重扫并递增 Revision、快照给出可更新/可卸载、目录增删写回 config 并热生效、
// 相对路径与兜底根被拒」；不联网、不起 git。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/skillfs"
)

// newSkillsWiring 造一份最小 wiring：profileDir + 可写的 config.json + 真实清单/安装器。
func newSkillsWiring(t *testing.T, extraDirs ...string) *wiring {
	t.Helper()
	base := t.TempDir()
	profileDir := filepath.Join(base, "profile")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(profileDir, "config.json")
	body := `{"model":{"name":"x"}}`
	if len(extraDirs) > 0 {
		quoted := make([]string, 0, len(extraDirs))
		for _, d := range extraDirs {
			quoted = append(quoted, `"`+strings.ReplaceAll(d, `\`, `\\`)+`"`)
		}
		body = `{"model":{"name":"x"},"skills":{"dirs":[` + strings.Join(quoted, ",") + `]}}`
	}
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &wiring{profileDir: profileDir, cfgPath: cfgPath}
	w.skills = skillfs.New(skillfs.Options{
		Dirs: append(append([]string{}, extraDirs...), filepath.Join(profileDir, "skills")),
	})
	inst, err := skillfs.NewInstaller(skillfs.InstallOptions{
		Root:       filepath.Join(profileDir, "skills"),
		CacheDir:   filepath.Join(profileDir, "skill-cache"),
		LedgerPath: filepath.Join(profileDir, "skill-installs.json"),
		Catalog:    w.skills,
	})
	if err != nil {
		t.Fatalf("NewInstaller: %v", err)
	}
	w.skillInstall = inst
	return w
}

// writeSkill 在 dir 下写一份合规 SKILL.md。
func writeSkill(t *testing.T, dir, name, desc string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "SKILL.md")
	doc := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSkillsAdminInstallAndSnapshot 安装 → 快照出现该技能（managed + 来源种类 + 可更新标记）。
func TestSkillsAdminInstallAndSnapshot(t *testing.T) {
	w := newSkillsWiring(t)
	sa := &skillsAdmin{w: w}
	src := writeSkill(t, filepath.Join(t.TempDir(), "pdf"), "pdf", "Fill PDF forms.")

	if before := sa.Snapshot(); before.Revision != 0 || len(before.Skills) != 0 {
		t.Fatalf("初始快照 = %+v", before)
	}
	msg, err := sa.Install(src)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !strings.Contains(msg, "已安装 1 个技能") || !strings.Contains(msg, "pdf") {
		t.Fatalf("安装文案 = %q", msg)
	}
	if w.skillsRev.Load() != 1 {
		t.Fatalf("Revision = %d, want 1", w.skillsRev.Load())
	}
	snap := sa.Snapshot()
	if len(snap.Skills) != 1 {
		t.Fatalf("快照技能 = %+v", snap.Skills)
	}
	st := snap.Skills[0]
	if st.Name != "pdf" || !st.Managed || st.Kind != skillfs.KindFile || st.Updatable {
		t.Fatalf("技能快照 = %+v（单文件来源不可更新）", st)
	}
	if st.Updated == "" || len(st.Updated) != 10 {
		t.Fatalf("更新时间 = %q（应是日期短格式）", st.Updated)
	}
	if !strings.Contains(st.Description, "Fill PDF") {
		t.Fatalf("说明 = %q", st.Description)
	}
	// 目录表：额外根在前、兜底根垫底且标记 Fallback。
	if len(snap.Dirs) != 1 || !snap.Dirs[0].Fallback {
		t.Fatalf("目录表 = %+v", snap.Dirs)
	}
	if !strings.HasSuffix(snap.ManagedRoot, "skills") {
		t.Fatalf("安装根 = %q", snap.ManagedRoot)
	}

	// 卸载：副本消失、台账清空、Revision 递增。
	if err := sa.Remove("pdf"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if w.skillsRev.Load() != 2 {
		t.Fatalf("卸载后 Revision = %d, want 2", w.skillsRev.Load())
	}
	if got := sa.Snapshot(); len(got.Skills) != 0 {
		t.Fatalf("卸载后仍有技能 = %+v", got.Skills)
	}
}

// TestSkillsAdminExternalAndManaged 外部技能根里的技能不算 managed，也不显示卸载。
func TestSkillsAdminExternalAndManaged(t *testing.T) {
	extra := t.TempDir()
	writeSkill(t, filepath.Join(extra, "lint"), "lint", "Check style.")
	w := newSkillsWiring(t, extra)
	sa := &skillsAdmin{w: w}

	snap := sa.Snapshot()
	if len(snap.Skills) != 1 || snap.Skills[0].Name != "lint" {
		t.Fatalf("快照技能 = %+v", snap.Skills)
	}
	if snap.Skills[0].Managed {
		t.Fatal("外部根里的技能不该标 managed（否则界面会给卸载按钮）")
	}
	if len(snap.Dirs) != 2 || snap.Dirs[0].Fallback || !snap.Dirs[1].Fallback {
		t.Fatalf("目录表顺序/兜底标记 = %+v", snap.Dirs)
	}
	// 同名技能的安装要被拒（外部根已生效）。
	src := writeSkill(t, filepath.Join(t.TempDir(), "lint"), "lint", "Another lint.")
	if _, err := sa.Install(src); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("同名安装应被拒：%v", err)
	}
}

// TestSkillsAdminDirsPersistAndApply 增删额外目录：写回 config 且立刻热生效；兜底根不可加不可删。
func TestSkillsAdminDirsPersistAndApply(t *testing.T) {
	extra := t.TempDir()
	w := newSkillsWiring(t)
	sa := &skillsAdmin{w: w}
	fallback := filepath.Join(w.profileDir, "skills")

	// 相对路径 / 不存在的目录 / 兜底根：都拒。
	if err := sa.AddDir("skills"); err == nil || !strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("相对路径应被拒：%v", err)
	}
	if err := sa.AddDir(filepath.Join(extra, "nope")); err == nil {
		t.Fatal("不存在的目录应被拒")
	}
	if err := sa.AddDir(fallback); err == nil || !strings.Contains(err.Error(), "兜底") {
		t.Fatalf("兜底根应被拒：%v", err)
	}

	if err := sa.AddDir(extra); err != nil {
		t.Fatalf("AddDir: %v", err)
	}
	if w.skillsRev.Load() != 1 {
		t.Fatalf("Revision = %d, want 1", w.skillsRev.Load())
	}
	dirs := w.skills.Dirs()
	if len(dirs) != 2 || !samePath(dirs[0], extra) || !samePath(dirs[1], fallback) {
		t.Fatalf("热生效目录 = %v", dirs)
	}
	// 落盘：重新读 config 应能看到额外目录。
	cfg, err := loadConfig(w.cfgPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.Skills.Dirs) != 1 || !samePath(cfg.Skills.Dirs[0], extra) {
		t.Fatalf("config skills.dirs = %v", cfg.Skills.Dirs)
	}
	// 重复添加被拒。
	if err := sa.AddDir(extra); err == nil {
		t.Fatal("重复目录应被拒")
	}
	// 删兜底根被拒；删额外根成功并清空配置键。
	if err := sa.RemoveDir(fallback); err == nil || !strings.Contains(err.Error(), "不可移除") {
		t.Fatalf("删兜底根应被拒：%v", err)
	}
	if err := sa.RemoveDir(extra); err != nil {
		t.Fatalf("RemoveDir: %v", err)
	}
	if dirs := w.skills.Dirs(); len(dirs) != 1 || !samePath(dirs[0], fallback) {
		t.Fatalf("移除后目录 = %v", dirs)
	}
	cfg, err = loadConfig(w.cfgPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.Skills.Dirs) != 0 {
		t.Fatalf("config 仍留着目录 = %v", cfg.Skills.Dirs)
	}
	// 再删一次：明确报「已不在」。
	if err := sa.RemoveDir(extra); err == nil {
		t.Fatal("重复移除应报错")
	}
}

// TestSkillsAdminUnwired 未接线：动作报错、快照不 panic（页面显示只读占位）。
func TestSkillsAdminUnwired(t *testing.T) {
	sa := &skillsAdmin{w: &wiring{}}
	if snap := sa.Snapshot(); snap.Revision != 0 || len(snap.Skills) != 0 {
		t.Fatalf("未接线快照 = %+v", snap)
	}
	if _, err := sa.Install("/tmp/x"); err == nil || !strings.Contains(err.Error(), "未接线") {
		t.Fatalf("未接线安装错误 = %v", err)
	}
	if err := sa.Rescan(); err == nil {
		t.Fatal("未接线重扫应报错")
	}
}

// TestSkillsAdminAppAdapter 命令面适配器：结构化结果（名字 + 备注 + 跳过），不含内部字段。
func TestSkillsAdminAppAdapter(t *testing.T) {
	w := newSkillsWiring(t)
	app := skillsAdminApp{w: w}
	dir := t.TempDir()
	writeSkill(t, filepath.Join(dir, "alpha"), "alpha", "A.")
	// 一个坏条目：name 不合规范 → 跳过但不阻断。
	if err := os.MkdirAll(filepath.Join(dir, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad", "SKILL.md"),
		[]byte("---\nname: Bad Name\ndescription: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := app.Install(context.Background(), dir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(rep.Skills) != 1 || rep.Skills[0] != "alpha" {
		t.Fatalf("结果技能 = %v", rep.Skills)
	}
	if len(rep.Skipped) != 1 || !strings.Contains(rep.Skipped[0], "bad") {
		t.Fatalf("跳过条目 = %v", rep.Skipped)
	}
	// 复制来源没有更新动作：错误原样上抛（命令层负责显示）。
	if _, err := app.Update(context.Background(), "alpha"); err == nil {
		t.Fatal("复制来源更新应报错")
	}
	if err := app.Remove(context.Background(), "alpha"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := app.Remove(context.Background(), "alpha"); err == nil {
		t.Fatal("重复卸载应报错")
	}
}
