package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBundleLandsNextToOutput 外部程序拷到产物旁边：插件靠「产物所在目录」找它，
// 这是唯一不需要配置、也不依赖运行目录的定位方式。
func TestBundleLandsNextToOutput(t *testing.T) {
	host, plugin := dir(t, "microhost"), dir(t, "microplugin")
	tmp := t.TempDir()
	out := filepath.Join(tmp, "micro"+exeSuffix())
	work := filepath.Join(tmp, "work")

	sidecar := filepath.Join(t.TempDir(), "sidecar"+exeSuffix())
	if err := os.WriteFile(sidecar, []byte("not really a program\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := run([]string{
		"-host", host, "-plugin", plugin,
		"-target", "cmd/micro/pluginext.go", "-pkg", "./cmd/micro",
		"-o", out, "-work", work, "-bundle", sidecar,
	})
	if err != nil {
		t.Fatalf("构建失败：%v", err)
	}

	got, err := os.ReadFile(filepath.Join(tmp, filepath.Base(sidecar)))
	if err != nil {
		t.Fatalf("产物旁边没有外部程序：%v", err)
	}
	if string(got) != "not really a program\n" {
		t.Errorf("外部程序内容不对：%q", got)
	}
	// 产物本身照旧能跑——拷外部程序不该动到构建结果。
	if printed := runBinary(t, out); !strings.Contains(printed, "factories=1") {
		t.Errorf("产物被影响了：%s", printed)
	}
}

// TestBundlePathIsRelativeToPlugin 相对路径按插件根解析，不按当前目录。
func TestBundlePathIsRelativeToPlugin(t *testing.T) {
	plugin := t.TempDir()
	if err := os.MkdirAll(filepath.Join(plugin, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(plugin, "bin", "sidecar.exe")
	if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := resolved{
		options:   options{bundles: []string{"bin/sidecar.exe"}},
		pluginDir: plugin,
		outFile:   filepath.Join(plugin, "host.exe"),
	}
	got, err := r.resolveBundles()
	if err != nil {
		t.Fatalf("相对路径解析失败：%v", err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("解析结果 = %v，想要 [%s]", got, want)
	}
}

// TestBundleRejectsBadSpec 三类错在构建前就挡掉：文件不在、给了目录、与产物同名。
// 拷到最后一步才失败会留下「产物有了、外部程序没有」的半套东西。
func TestBundleRejectsBadSpec(t *testing.T) {
	plugin := t.TempDir()
	if err := os.WriteFile(filepath.Join(plugin, "host.exe"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(plugin, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		bundles []string
		want    string
	}{
		{"文件不在", []string{"bin/nope.exe"}, "外部程序"},
		{"给的是目录", []string{"sub"}, "是目录"},
		{"与产物同名", []string{"host.exe"}, "同名"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := resolved{
				options:   options{bundles: c.bundles},
				pluginDir: plugin,
				outFile:   filepath.Join(plugin, "host.exe"),
			}
			if _, err := r.resolveBundles(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("应当报含「%s」的错，得到：%v", c.want, err)
			}
		})
	}
}

// TestBundleDuplicateNamesRejected 两个外部程序同名会互相覆盖，构建前就报。
func TestBundleDuplicateNamesRejected(t *testing.T) {
	plugin := t.TempDir()
	for _, sub := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(plugin, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(plugin, sub, "tool.exe"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := resolved{
		options:   options{bundles: []string{"a/tool.exe", "b/tool.exe"}},
		pluginDir: plugin,
		outFile:   filepath.Join(plugin, "host.exe"),
	}
	if _, err := r.resolveBundles(); err == nil || !strings.Contains(err.Error(), "同名") {
		t.Fatalf("同名应报错，得到：%v", err)
	}
}
