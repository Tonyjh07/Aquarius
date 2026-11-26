package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	stdout = io.Discard // 摘要只给人看，测试里别混进日志
	os.Exit(m.Run())
}

// dir 取 testdata 下的绝对路径：构建子进程的工作目录是宿主根，相对路径会对不上。
func dir(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("取 testdata 路径：%v", err)
	}
	return abs
}

func TestBuildInjectsPlugin(t *testing.T) {
	host, plugin := dir(t, "microhost"), dir(t, "microplugin")
	tmp := t.TempDir()
	out := filepath.Join(tmp, "micro"+exeSuffix())
	work := filepath.Join(tmp, "work")
	before := snapshot(t, host)

	err := run([]string{
		"-host", host, "-plugin", plugin,
		"-target", "cmd/micro/pluginext.go", "-pkg", "./cmd/micro",
		"-o", out, "-work", work,
	})
	if err != nil {
		t.Fatalf("构建失败：%v", err)
	}

	got := runBinary(t, out)
	if !strings.Contains(got, "dep=localdep") {
		t.Errorf("本地依赖没参与构建：%s", got)
	}
	if !strings.Contains(got, "factories=1") || !strings.Contains(got, "names=[demo]") {
		t.Errorf("插件没被注入：%s", got)
	}

	// 中间文件落在工作区，宿主一个字节都不该变。
	for _, name := range []string{"build.mod", "overlay.json"} {
		if _, err := os.Stat(filepath.Join(work, name)); err != nil {
			t.Errorf("工作区缺 %s：%v", name, err)
		}
	}
	after := snapshot(t, host)
	if diff := diffSnapshot(before, after); diff != "" {
		t.Errorf("宿主目录被改动：\n%s", diff)
	}

	// 相对 replace 必须被改写成绝对路径，否则 modfile 搬到工作区后解析不到。
	mod, err := os.ReadFile(filepath.Join(work, "build.mod"))
	if err != nil {
		t.Fatalf("读 build.mod：%v", err)
	}
	want := "example.com/localdep => " + filepath.ToSlash(filepath.Join(host, "localdep"))
	if !strings.Contains(string(mod), want) {
		t.Errorf("相对 replace 没改写成绝对路径，build.mod 里找不到：%s\n---\n%s", want, mod)
	}
	if !strings.Contains(string(mod), "example.com/microplugin") {
		t.Errorf("build.mod 里没有插件模块：\n%s", mod)
	}
}

func TestPlainBuildHasNoPlugin(t *testing.T) {
	host := dir(t, "microhost")
	out := filepath.Join(t.TempDir(), "micro-plain"+exeSuffix())
	cmd := exec.Command("go", "build", "-o", out, "./cmd/micro")
	cmd.Dir = host
	cmd.Env = cleanEnv()
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("不带注入的构建失败：%v\n%s", err, msg)
	}
	got := runBinary(t, out)
	if !strings.Contains(got, "factories=0") {
		t.Errorf("默认构建里冒出了插件：%s", got)
	}
}

func TestMissingHookFails(t *testing.T) {
	host := dir(t, "microhost")
	plugin := t.TempDir()
	if err := os.WriteFile(filepath.Join(plugin, "go.mod"),
		[]byte("module example.com/empty\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"-host", host, "-plugin", plugin, "-o", filepath.Join(t.TempDir(), "x"), "-work", t.TempDir()})
	if err == nil {
		t.Fatal("插件目录没有注入文件，构建应当失败")
	}
	if !strings.Contains(err.Error(), "注入实现文件") {
		t.Errorf("报因没指向注入文件：%v", err)
	}
}

func TestMissingTargetFails(t *testing.T) {
	err := run([]string{
		"-host", dir(t, "microhost"), "-plugin", dir(t, "microplugin"),
		"-target", "cmd/micro/nope.go",
		"-o", filepath.Join(t.TempDir(), "x"), "-work", t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "接入口") {
		t.Fatalf("接入口不存在时应当失败并指出来，得到：%v", err)
	}
}

func TestDefaultOutputLandsInPlugin(t *testing.T) {
	r, err := options{plugin: dir(t, "microplugin"), host: dir(t, "microhost")}.resolve()
	if err != nil {
		t.Fatalf("解析缺省值：%v", err)
	}
	want := filepath.Join(dir(t, "microplugin"), "microhost-with-plugin"+exeSuffix())
	if r.outFile != want {
		t.Errorf("缺省产物路径：想要 %s，得到 %s", want, r.outFile)
	}
	if !strings.Contains(r.workDir, "aqbuild") {
		t.Errorf("缺省工作区应在缓存目录下：%s", r.workDir)
	}
}

func runBinary(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command(path).CombinedOutput()
	if err != nil {
		t.Fatalf("跑产物 %s：%v\n%s", path, err, out)
	}
	return string(out)
}

// snapshot 记下宿主目录里每个文件的内容，用来断言构建没有留下痕迹。
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("快照 %s：%v", root, err)
	}
	return files
}

func diffSnapshot(before, after map[string]string) string {
	var b strings.Builder
	for name, old := range before {
		cur, ok := after[name]
		if !ok {
			b.WriteString("  删除：" + name + "\n")
			continue
		}
		if cur != old {
			b.WriteString("  修改：" + name + "\n")
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			b.WriteString("  新增：" + name + "\n")
		}
	}
	return b.String()
}
