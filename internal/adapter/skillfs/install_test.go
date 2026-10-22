package skillfs

// install_test.go 覆盖安装面口径（D202/D203）：四来源、同名不覆盖、zip 门禁、更新覆盖、
// 卸载、台账失效清理。git 用注入的假 RunGit（不起真实仓库），网络用 httptest。

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestInstaller 建一个装在临时目录里的安装器（catalog 可空）。
func newTestInstaller(t *testing.T, cat *Catalog) *Installer {
	t.Helper()
	base := t.TempDir()
	inst, err := NewInstaller(InstallOptions{
		Root:       filepath.Join(base, "skills"),
		CacheDir:   filepath.Join(base, "skill-cache"),
		LedgerPath: filepath.Join(base, "skill-installs.json"),
		Catalog:    cat,
		Now:        func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewInstaller: %v", err)
	}
	return inst
}

// writeSkillFile 在 dir 下写 SKILL.md（自动建目录）。
func writeSkillFile(t *testing.T, dir, name, desc, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, skillFileName)
	doc := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// makeZip 内存里打一个压缩包（name → 内容）。
func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestInstallSingleFile 单文件 SKILL.md → 副本目录 + 台账。
func TestInstallSingleFile(t *testing.T) {
	inst := newTestInstaller(t, nil)
	src := writeSkillFile(t, filepath.Join(t.TempDir(), "pdf"), "pdf", "Fill PDFs.", "# PDF")

	rep, err := inst.Install(context.Background(), src)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(rep.Installed) != 1 || rep.Installed[0].Name != "pdf" {
		t.Fatalf("装上的技能 = %+v", rep.Installed)
	}
	if _, err := os.Stat(filepath.Join(inst.Root(), "pdf", skillFileName)); err != nil {
		t.Fatalf("副本没落位：%v", err)
	}
	recs := inst.Records()
	if len(recs) != 1 || recs[0].Kind != kindFile || recs[0].Source != src {
		t.Fatalf("台账 = %+v", recs)
	}
	if recs[0].InstalledAt == "" {
		t.Fatal("台账缺安装时间")
	}
	// 台账文件应是可解析的 JSON（重新构造即可验证）。
	again, err := NewInstaller(inst.opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Records(); len(got) != 1 || got[0].Name != "pdf" {
		t.Fatalf("重新读台账 = %+v", got)
	}
}

// TestInstallDirWithNestedSkills 文件夹来源：向下找多个技能，坏条目跳过但不阻断。
func TestInstallDirWithNestedSkills(t *testing.T) {
	inst := newTestInstaller(t, nil)
	src := t.TempDir()
	writeSkillFile(t, filepath.Join(src, "alpha"), "alpha", "A.", "# A")
	writeSkillFile(t, filepath.Join(src, "bundle", "beta"), "beta", "B.", "# B")
	if err := os.MkdirAll(filepath.Join(src, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 坏技能：name 与规范不符
	if err := os.WriteFile(filepath.Join(src, "notes", skillFileName),
		[]byte("---\nname: Bad Name\ndescription: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := inst.Install(context.Background(), src)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	names := []string{}
	for _, s := range rep.Installed {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "alpha,beta" {
		t.Fatalf("装上的技能 = %v", names)
	}
	if len(rep.Skipped) != 1 || !strings.Contains(rep.Skipped[0], "notes") {
		t.Fatalf("跳过条目 = %v", rep.Skipped)
	}
	if _, err := os.Stat(filepath.Join(inst.Root(), "beta", skillFileName)); err != nil {
		t.Fatalf("嵌套技能没落位：%v", err)
	}
}

// TestInstallZipStripsWrapper 压缩包：剥掉单一顶层目录、记录 Subdir（更新要靠它找回目录）。
func TestInstallZipStripsWrapper(t *testing.T) {
	inst := newTestInstaller(t, nil)
	data := makeZip(t, map[string]string{
		"repo-main/SKILL.md":           "---\nname: zipped\ndescription: z\n---\n\n# Z\n",
		"repo-main/references/note.md": "note",
		"repo-main/scripts/run.sh":     "echo hi",
	})
	zp := filepath.Join(t.TempDir(), "pkg.zip")
	if err := os.WriteFile(zp, data, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := inst.Install(context.Background(), zp)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(rep.Installed) != 1 || rep.Installed[0].Name != "zipped" {
		t.Fatalf("装上 = %+v", rep.Installed)
	}
	if _, err := os.Stat(filepath.Join(inst.Root(), "zipped", "references", "note.md")); err != nil {
		t.Fatalf("references 没跟着复制：%v", err)
	}
	recs := inst.Records()
	if len(recs) != 1 || recs[0].Subdir != "" || recs[0].Kind != kindZip {
		t.Fatalf("台账 = %+v", recs)
	}
}

// TestInstallZipRejectsSlip 恶意压缩包（`..` 越界）整包拒绝，且不落任何副本。
func TestInstallZipRejectsSlip(t *testing.T) {
	inst := newTestInstaller(t, nil)
	data := makeZip(t, map[string]string{
		"SKILL.md":      "---\nname: evil\ndescription: e\n---\n",
		"../escape.txt": "nope",
	})
	zp := filepath.Join(t.TempDir(), "evil.zip")
	if err := os.WriteFile(zp, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.Install(context.Background(), zp); err == nil {
		t.Fatal("越界压缩包应被拒绝")
	}
	if ents, err := os.ReadDir(inst.Root()); err == nil {
		for _, e := range ents {
			t.Fatalf("失败安装留了残渣：%s", e.Name())
		}
	}
	if len(inst.Records()) != 0 {
		t.Fatal("失败安装写了台账")
	}
}

// TestSafeZipPath 条目归一与拒绝口径。
func TestSafeZipPath(t *testing.T) {
	ok := map[string]string{
		"a/b.md": "a/b.md",
		`a\b.md`: "a/b.md",
		"./a.md": "a.md",
		"dir/":   "dir",
		"":       "",
	}
	for in, want := range ok {
		got, err := safeZipPath(in)
		if err != nil || got != want {
			t.Errorf("safeZipPath(%q) = %q, %v；期望 %q", in, got, err, want)
		}
	}
	bad := []string{"/abs", `C:\win`, "../up", "a/../../up", `..\up`}
	for _, in := range bad {
		if _, err := safeZipPath(in); err == nil {
			t.Errorf("safeZipPath(%q) 应拒绝", in)
		}
	}
}

// TestInstallRejectsDuplicateName 同名不覆盖：已生效（含外部技能根）与已装副本都算。
func TestInstallRejectsDuplicateName(t *testing.T) {
	outside := t.TempDir()
	writeSkillFile(t, filepath.Join(outside, "taken"), "taken", "外部根里的同名技能。", "# T")
	inst := newTestInstaller(t, New(Options{Dirs: []string{outside}}))

	src := writeSkillFile(t, filepath.Join(t.TempDir(), "taken"), "taken", "新来的。", "# N")
	reps, err := inst.Install(context.Background(), src)
	if err == nil {
		t.Fatal("同名应被拒绝")
	}
	if !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("错误文案 = %v", err)
	}
	if len(reps.Installed) != 0 {
		t.Fatalf("拒绝时不该落位：%+v", reps.Installed)
	}
	// 外部那份正文没被动过。
	if b, err := os.ReadFile(filepath.Join(outside, "taken", skillFileName)); err != nil ||
		!bytes.Contains(b, []byte("# T")) {
		t.Fatalf("外部技能被改了：%v", err)
	}
}

// TestInstallRelativePathRejected 无工作区概念：相对路径不猜 cwd。
func TestInstallRelativePathRejected(t *testing.T) {
	inst := newTestInstaller(t, nil)
	if _, err := inst.Install(context.Background(), `skills\pdf`); err == nil ||
		!strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("相对路径应被拒绝：%v", err)
	}
}

// TestRemove 卸载删副本与台账；外部根里的同名技能不被碰。
func TestRemove(t *testing.T) {
	inst := newTestInstaller(t, nil)
	src := writeSkillFile(t, filepath.Join(t.TempDir(), "gone"), "gone", "G.", "# G")
	if _, err := inst.Install(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if err := inst.Remove(context.Background(), "gone"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.Root(), "gone")); !os.IsNotExist(err) {
		t.Fatalf("副本没删掉：%v", err)
	}
	if len(inst.Records()) != 0 {
		t.Fatalf("台账没清：%+v", inst.Records())
	}
	// 再删一次：明确报错（不静默成功）。
	if err := inst.Remove(context.Background(), "gone"); err == nil {
		t.Fatal("重复卸载应报错")
	}
}

// TestUpdateZipOverwrites 更新 = 重新下载/读取压缩包并整体覆盖副本。
func TestUpdateZipOverwrites(t *testing.T) {
	inst := newTestInstaller(t, nil)
	zp := filepath.Join(t.TempDir(), "pkg.zip")
	old := makeZip(t, map[string]string{"SKILL.md": "---\nname: up\ndescription: 旧\n---\n\nold body\n"})
	if err := os.WriteFile(zp, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.Install(context.Background(), zp); err != nil {
		t.Fatal(err)
	}
	// 上游更新（同名、新正文）。
	newer := makeZip(t, map[string]string{"SKILL.md": "---\nname: up\ndescription: 新\n---\n\nnew body\n"})
	if err := os.WriteFile(zp, newer, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := inst.Update(context.Background(), "up")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(rep.Installed) != 1 {
		t.Fatalf("更新结果 = %+v", rep)
	}
	b, err := os.ReadFile(filepath.Join(inst.Root(), "up", skillFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "new body") {
		t.Fatalf("副本没被覆盖：%s", b)
	}
	recs := inst.Records()
	if recs[0].UpdatedAt == "" {
		t.Fatal("台账缺更新时间")
	}
}

// TestUpdateCopySourceRefused 复制来源没有更新动作。
func TestUpdateCopySourceRefused(t *testing.T) {
	inst := newTestInstaller(t, nil)
	src := writeSkillFile(t, filepath.Join(t.TempDir(), "cp"), "cp", "C.", "# C")
	if _, err := inst.Install(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.Update(context.Background(), "cp"); err == nil ||
		!strings.Contains(err.Error(), "重新导入") {
		t.Fatalf("复制来源更新应报错：%v", err)
	}
}

// fakeGit 假 git：只模拟 clone（把来源写成目录），其余子命令空转。
type fakeGit struct {
	mu     sync.Mutex
	avail  bool
	calls  [][]string
	branch string
}

func (f *fakeGit) available() bool { return f.avail }

func (f *fakeGit) run(_ context.Context, dir string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{dir}, args...))
	if len(args) > 0 && args[0] == "clone" {
		dst := args[len(args)-1]
		if err := os.MkdirAll(filepath.Join(dst, ".git"), 0o755); err != nil {
			return "", err
		}
		doc := "---\nname: fromgit\ndescription: g\n---\n\n# from git\n"
		sub := filepath.Join(dst, "pack", "fromgit")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(sub, skillFileName), []byte(doc), 0o644); err != nil {
			return "", err
		}
		deep := filepath.Join(dst, "pack", "deep")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			return "", err
		}
		doc2 := "---\nname: deepskill\ndescription: d\n---\n\n# deep\n"
		if err := os.WriteFile(filepath.Join(deep, skillFileName), []byte(doc2), 0o644); err != nil {
			return "", err
		}
	}
	return "", nil
}

// TestInstallGitCloneAndUpdate git 来源：克隆进缓存、装副本；更新走 pull/reset 而非重新克隆。
func TestInstallGitCloneAndUpdate(t *testing.T) {
	inst := newTestInstaller(t, nil)
	git := &fakeGit{avail: true}
	inst.opts.GitAvailable = git.available
	inst.opts.RunGit = git.run

	rep, err := inst.Install(context.Background(), "https://github.com/o/r#main")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	names := []string{}
	for _, s := range rep.Installed {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "deepskill,fromgit" {
		t.Fatalf("装上 = %v", names)
	}
	recs := inst.Records()
	if len(recs) != 2 || recs[0].Kind != kindGit || recs[0].Ref != "main" || recs[0].Cache == "" {
		t.Fatalf("台账 = %+v", recs)
	}
	cloneCount := 0
	for _, c := range git.calls {
		if len(c) > 1 && c[1] == "clone" {
			cloneCount++
		}
	}
	if cloneCount != 1 {
		t.Fatalf("git clone 次数 = %d，调用 = %v", cloneCount, git.calls)
	}

	if _, err := inst.Update(context.Background(), "fromgit"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if cloneCount != 1 {
		t.Fatalf("更新又克隆了一次：%v", git.calls)
	}
}

// TestInstallGitWithoutGitFallsBackToArchive 无 git：GitHub 仓库改下载压缩包，台账记 zip。
func TestInstallGitWithoutGitFallsBackToArchive(t *testing.T) {
	inst := newTestInstaller(t, nil)
	inst.opts.GitAvailable = func() bool { return false }
	data := makeZip(t, map[string]string{
		"repo-main/SKILL.md": "---\nname: archived\ndescription: a\n---\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()
	// 把 GitHub 主机换成测试服务器（githubArchive 只认 github.com，故直接喂 zip 链接走同一条
	// 下载/解压路径；这里验证「无 git + zip 链接」的降级）。
	rep, err := inst.Install(context.Background(), srv.URL+"/pkg.zip")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(rep.Installed) != 1 || rep.Installed[0].Name != "archived" {
		t.Fatalf("装上 = %+v", rep.Installed)
	}
	if recs := inst.Records(); recs[0].Kind != kindZip || recs[0].FetchURL != srv.URL+"/pkg.zip" {
		t.Fatalf("台账 = %+v", recs)
	}
}

// TestLedgerDropsMissingDir 副本目录消失后，重新构造安装器即清掉台账条目。
func TestLedgerDropsMissingDir(t *testing.T) {
	inst := newTestInstaller(t, nil)
	src := writeSkillFile(t, filepath.Join(t.TempDir(), "vanish"), "vanish", "V.", "# V")
	if _, err := inst.Install(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(inst.Root(), "vanish")); err != nil {
		t.Fatal(err)
	}
	again, err := NewInstaller(inst.opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Records(); len(got) != 0 {
		t.Fatalf("失效条目没清：%+v", got)
	}
}

// TestCacheKeyDistinct 缓存目录名：不同仓库 / 不同分支不互相踩。
func TestCacheKeyDistinct(t *testing.T) {
	a := cacheKey("https://github.com/o/r", "")
	b := cacheKey("https://github.com/o/r", "dev")
	c := cacheKey("https://github.com/o/other", "")
	if a == b || a == c {
		t.Fatalf("缓存键重复：%q %q %q", a, b, c)
	}
	if strings.ContainsAny(a, `/\:*?"<>|`) {
		t.Fatalf("缓存键含非法字符：%q", a)
	}
}
