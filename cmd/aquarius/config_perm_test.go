package main

// config_perm_test.go 配置文件权限：config.json 可以内联明文密钥（D35），一律 0600。

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestWriteConfigFileTightensPerm 新建与覆盖都落在 0600：早先按 0644 建出来的
// 那份也要收回来（atomicfile 的权限继承只保证不放宽，配置文件要的是相反方向）。
func TestWriteConfigFileTightensPerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位（ACL 决定可读性），这条只对类 unix 有意义")
	}
	dir := t.TempDir()

	fresh := filepath.Join(dir, "fresh.json")
	if err := writeConfigFile(fresh, []byte("{}")); err != nil {
		t.Fatalf("新建: %v", err)
	}
	assertPerm(t, fresh, 0o600)

	loose := filepath.Join(dir, "loose.json")
	if err := os.WriteFile(loose, []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := writeConfigFile(loose, []byte(`{"model":{"api_key":"sk-plain"}}`)); err != nil {
		t.Fatalf("覆盖: %v", err)
	}
	assertPerm(t, loose, 0o600)
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Fatalf("%s 权限 = %04o, want %04o", filepath.Base(path), got, want)
	}
}
