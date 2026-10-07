package main

// profile 管理面（D110③/修订④）：设置窗 profile 区的装配根实现。
// 快照 = 本次启动生效的 profile（--profile 或指针决定）+ profiles/ 可用清单；
// 新建 = 模板 config；复制 = 以当前 profile 配置为底（仅配置，不带会话/记忆/附件
// 数据，D110 修订④）；切换 = 原子写根指针——重启生效（Q1：profile 进程级，
// 运行态不热切，进程内只动指针文件，不触碰本次运行已加载的任何状态）。

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// profilesManager uigui.ProfilesManager 的装配根实现（D110③）。
type profilesManager struct {
	dataDir    string // <data> 根（指针与 profiles/ 所在）
	current    string // 本次启动生效的 profile 名（含 --profile 覆盖）
	profileCfg string // 当前 profile 的 config.json 路径（复制源）
}

// Snapshot profile 区数据面（设置窗开窗与动作后现取）。
func (m *profilesManager) Snapshot() uigui.ProfilesSnapshot {
	return uigui.ProfilesSnapshot{Current: m.current, Items: listProfiles(m.dataDir)}
}

// Create 以模板 config 新建 profile（同名拒建防覆盖）。
func (m *profilesManager) Create(name string) error {
	if err := validProfileName(name); err != nil {
		return err
	}
	dir := filepath.Join(m.dataDir, "profiles", name)
	cfg := filepath.Join(dir, "config.json")
	if _, err := os.Stat(cfg); err == nil {
		return fmt.Errorf("profile %q 已存在", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 profile 目录: %w", err)
	}
	return writeDefaultConfig(cfg)
}

// Copy 以当前 profile 的 config.json 为底新建（D110 修订④：仅复制配置——会话/
// 记忆/附件等数据不随行，新 profile 从同套配置起步）。
func (m *profilesManager) Copy(name string) error {
	if err := validProfileName(name); err != nil {
		return err
	}
	dir := filepath.Join(m.dataDir, "profiles", name)
	cfg := filepath.Join(dir, "config.json")
	if _, err := os.Stat(cfg); err == nil {
		return fmt.Errorf("profile %q 已存在", name)
	}
	data, err := os.ReadFile(m.profileCfg)
	if err != nil {
		return fmt.Errorf("读取当前 profile 配置: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 profile 目录: %w", err)
	}
	return os.WriteFile(cfg, data, 0o644)
}

// Switch 原子写根指针（重启生效）；目标须已存在——新建/复制后立即可切，显式
// 指向不存在的名字一律拒写（同 --profile 口径，D110 修订②）。
func (m *profilesManager) Switch(name string) error {
	if err := validProfileName(name); err != nil {
		return err
	}
	if name == m.current {
		return fmt.Errorf("已运行在 profile %q", name)
	}
	if !slices.Contains(listProfiles(m.dataDir), name) {
		return fmt.Errorf("profile %q 不存在（可先「新建」或「复制当前」）", name)
	}
	return atomicfile.WriteFile(filepath.Join(m.dataDir, "config.json"),
		[]byte(pointerContent(name)), 0o644)
}
