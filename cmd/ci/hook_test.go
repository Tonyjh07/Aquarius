package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newTestRepo 建一个够用的空仓库：只测 hook 落位，需要 .git 存在。
func newTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git，跳过")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Skipf("git init 失败：%v %s", err, out)
	}
	return root
}

func TestInstallPrePush(t *testing.T) {
	root := newTestRepo(t)
	if err := installPrePush(root); err != nil {
		t.Fatalf("安装失败：%v", err)
	}

	dir, err := hooksDir(root)
	if err != nil {
		t.Fatalf("定位 hooks 目录失败：%v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "pre-push"))
	if err != nil {
		t.Fatalf("hook 没写出来：%v", err)
	}
	body := string(b)
	if !strings.Contains(body, hookMarker) {
		t.Error("缺少认领标记，下次安装会把自己的 hook 当别人的备份")
	}
	if !strings.Contains(body, "--profile=fast") {
		t.Error("hook 应只跑 fast 档：pre-push 必须快到让人愿意等")
	}
	if strings.Contains(body, "\r\n") {
		t.Error("hook 必须是 LF 行尾：Windows 上 CRLF 的 sh 脚本会报错")
	}
}

func TestInstallPrePushBacksUpExisting(t *testing.T) {
	root := newTestRepo(t)
	dir, err := hooksDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pre-push")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 别人写的\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := installPrePush(root); err != nil {
		t.Fatalf("安装失败：%v", err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("原有 hook 应被备份而不是丢弃：%v", err)
	}
	if !strings.Contains(string(bak), "别人写的") {
		t.Error("备份内容不对")
	}
}
