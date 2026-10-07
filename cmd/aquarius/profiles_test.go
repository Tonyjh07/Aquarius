package main

// profile 管理面测试（D110③/修订④）：新建/复制/切换三动作的落盘行为、
// 同名拒建、切换存在性校验与指针原子写。

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newProfilesManager 落一套可运行布局（default profile）并返回管理面。
func newProfilesManager(t *testing.T) (*profilesManager, string) {
	t.Helper()
	dir := t.TempDir()
	prof := writeProfileLayout(t, dir, `{"model":{"providers":[{"name":"p","base_url":"http://x","api_key":"secret:K","models":["m"]}],"name":"m"},"ui":{"kind":"repl"}}`)
	return &profilesManager{dataDir: dir, current: "default", profileCfg: filepath.Join(prof, "config.json")}, dir
}

func readPointer(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("读指针: %v", err)
	}
	var p struct {
		Profile string `json:"profile"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("解析指针: %v", err)
	}
	return p.Profile
}

func TestProfilesManagerCreateCopySwitch(t *testing.T) {
	m, dir := newProfilesManager(t)

	// 新建：模板 config 落盘、进清单；同名拒建。
	if err := m.Create("fresh"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "fresh", "config.json")); err != nil {
		t.Fatalf("模板未落盘: %v", err)
	}
	if err := m.Create("fresh"); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("同名新建 err = %v, want 已存在", err)
	}
	tmp := &profilesManager{dataDir: dir}
	if got := tmp.Snapshot(); len(got.Items) != 2 || got.Items[0] != "default" || got.Items[1] != "fresh" {
		t.Fatalf("清单 = %+v, want [default fresh]", got.Items)
	}

	// 复制：仅配置——当前 profile 的 config.json 原文为底，无会话数据。
	if err := m.Copy("cloned"); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "profiles", "cloned", "config.json"))
	if err != nil || !strings.Contains(string(data), `"name":"p"`) {
		t.Fatalf("复制配置 = %q, %v", data, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "profiles", "cloned")); len(entries) != 1 {
		t.Fatalf("复制应仅含 config.json（不带会话数据）: %d 项", len(entries))
	}
	if err := m.Copy("cloned"); err == nil {
		t.Fatal("同名复制应拒")
	}

	// 切换：指针原子改写；不存在/当前名拒切。
	if err := m.Switch("fresh"); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if got := readPointer(t, dir); got != "fresh" {
		t.Fatalf("指针 = %q, want fresh", got)
	}
	if err := m.Switch("nope"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("切不存在 err = %v, want 不存在", err)
	}
	if err := m.Switch("default"); err == nil || !strings.Contains(err.Error(), "已运行") {
		t.Fatalf("切当前名 err = %v, want 已运行", err)
	}
	if got := readPointer(t, dir); got != "fresh" {
		t.Fatalf("拒切后指针应不变 = %q", got)
	}

	// 非法名字统一拒。
	for _, act := range []func(string) error{m.Create, m.Copy, m.Switch} {
		if err := act("a/b"); err == nil {
			t.Fatal("路径分隔名字应拒")
		}
	}
}

func TestProfilesManagerCopyMissingSource(t *testing.T) {
	m, _ := newProfilesManager(t)
	m.profileCfg = filepath.Join(m.dataDir, "profiles", "missing", "config.json")
	if err := m.Copy("x"); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want 源缺失报因", err)
	}
}
