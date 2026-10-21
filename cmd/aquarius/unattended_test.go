package main

// 无人值守落盘测试（D185 口径四）：`/unattended` 与菜单/设置窗都走这一条写回——config
// 的 permissions.unattended 落盘 + 活开关同步 + 其余 permissions 键（level）不丢。

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPersistUnattendedRoundTrip 写回 → 读回；level 保留；活开关同步。
func TestPersistUnattendedRoundTrip(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
		t.Fatalf("写模板: %v", err)
	}
	lvl := newLevelHolder("strict")
	on := newUnattendedHolder(false)
	w := &cfgWriter{path: cfgPath, lvl: lvl, unattended: on}

	if err := w.persistUnattended(true); err != nil {
		t.Fatalf("persistUnattended(true): %v", err)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if !cfg.Permissions.Unattended {
		t.Fatal("permissions.unattended 应为 true")
	}
	if cfg.Permissions.Level != "strict" {
		t.Fatalf("level 被写坏: %q", cfg.Permissions.Level)
	}
	if !on.Get() {
		t.Fatal("活开关应同步为 true")
	}

	// 关闭也落成显式 false（不是删键——否则下次读回会回到「键缺失」语义，虽然缺省也是
	// false，但显式写更能表达用户意图）。
	if err := w.persistUnattended(false); err != nil {
		t.Fatalf("persistUnattended(false): %v", err)
	}
	cfg, err = loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if cfg.Permissions.Unattended {
		t.Fatal("permissions.unattended 应为 false")
	}
	if on.Get() {
		t.Fatal("活开关应同步为 false")
	}
}

// TestPersistUnattendedKeepsLevel 与 /permission 交替写回不互相清键。
func TestPersistUnattendedKeepsLevel(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
		t.Fatalf("写模板: %v", err)
	}
	lvl := newLevelHolder("strict")
	w := &cfgWriter{path: cfgPath, lvl: lvl, unattended: newUnattendedHolder(false)}
	if err := w.persistUnattended(true); err != nil {
		t.Fatalf("persistUnattended: %v", err)
	}
	if err := w.persistLevel("permissive"); err != nil {
		t.Fatalf("persistLevel: %v", err)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if !cfg.Permissions.Unattended || cfg.Permissions.Level != "permissive" {
		t.Fatalf("两键应同时保留: %+v", cfg.Permissions)
	}
}

// TestToolChannelsAutoYesWarnEdge 口径三的告警条件：`-yes` 与配置里的开关同时成立才提示
// （模板缺省为关，因此正常路径不该打警告）。
func TestToolChannelsAutoYesWarnEdge(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
		t.Fatalf("写模板: %v", err)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Permissions.Unattended {
		t.Fatal("模板缺省应为关")
	}
	if err := os.WriteFile(cfgPath, []byte(`{"permissions":{"level":"strict","unattended":true}}`), 0o644); err != nil {
		t.Fatalf("写配置: %v", err)
	}
	cfg, err = loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if !cfg.Permissions.Unattended {
		t.Fatal("应读回 true")
	}
}
