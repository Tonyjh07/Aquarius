package main

// 技能管理面装配根侧（D202/D203）：把 skillfs.Installer（安装内核）适配成两套**消费方
// 定义**的接口——uigui.SkillsAdmin（设置窗「技能」页，返回用户面文案）与 app.SkillAdmin
// （/skill install|remove|update 命令面，返回结构化结果）。
//
// 为什么不进 port：安装/卸载/更新是用户面动作，模型侧只该「列」与「载」（D202 口径一）
// ——让模型能拉远端指令包，等于把注入面交给远端内容。
//
// 两套接口的 Install 签名不同（UI 要现成的中文摘要，命令面要能自己排版），故拆成两个
// 薄适配器，共享同一份 `skillActions`（改库 → 重扫 → 递增 Revision）。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/skillfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/app"
)

// skillActions 三动作的共用实现（装配根）。
type skillActions struct{ w *wiring }

// installer 安装内核尚未装配（无 profile 目录 / 装配失败）时报错而不是 panic。
func (s skillActions) installer() (*skillfs.Installer, error) {
	if s.w.skillInstall == nil {
		return nil, errors.New("技能安装面未接线（数据目录未就绪）")
	}
	return s.w.skillInstall, nil
}

// catalog 技能清单（安装后重扫用；nil = 只读，不可装）。
func (s skillActions) catalog() (*skillfs.Catalog, error) {
	if s.w.skills == nil {
		return nil, errors.New("技能目录未接线")
	}
	return s.w.skills, nil
}

// install 安装一个来源，成功后重扫并递增 Revision。
func (s skillActions) install(source string) (skillfs.InstallReport, error) {
	inst, err := s.installer()
	if err != nil {
		return skillfs.InstallReport{}, err
	}
	cat, err := s.catalog()
	if err != nil {
		return skillfs.InstallReport{}, err
	}
	rep, err := inst.Install(context.Background(), source)
	if err != nil {
		return rep, err
	}
	cat.Rescan()
	s.w.skillsRev.Add(1)
	return rep, nil
}

// update 更新一个（name 空 = 全部），成功后重扫并递增 Revision。
func (s skillActions) update(name string) (skillfs.InstallReport, error) {
	inst, err := s.installer()
	if err != nil {
		return skillfs.InstallReport{}, err
	}
	cat, err := s.catalog()
	if err != nil {
		return skillfs.InstallReport{}, err
	}
	rep, err := inst.Update(context.Background(), name)
	if err != nil {
		return rep, err
	}
	cat.Rescan()
	s.w.skillsRev.Add(1)
	return rep, nil
}

// remove 卸载一个安装副本，成功后重扫并递增 Revision。
func (s skillActions) remove(name string) error {
	inst, err := s.installer()
	if err != nil {
		return err
	}
	cat, err := s.catalog()
	if err != nil {
		return err
	}
	if err := inst.Remove(context.Background(), name); err != nil {
		return err
	}
	cat.Rescan()
	s.w.skillsRev.Add(1)
	return nil
}

// rescan 重新扫描（不做文件监听：硬性规则 7 不引入监听器）。
func (s skillActions) rescan() error {
	cat, err := s.catalog()
	if err != nil {
		return err
	}
	cat.Rescan()
	s.w.skillsRev.Add(1)
	return nil
}

// extraDirs 当前额外技能根（生效目录里去掉兜底根）。
func (s skillActions) extraDirs() ([]string, string) {
	fallback := ""
	if s.w.profileDir != "" {
		fallback = filepath.Join(s.w.profileDir, "skills")
	}
	var out []string
	if s.w.skills != nil {
		for _, d := range s.w.skills.Dirs() {
			if fallback != "" && samePath(d, fallback) {
				continue
			}
			out = append(out, d)
		}
	}
	return out, fallback
}

// addDir 追加一个额外技能根：校验 → 写回 config → 热生效。
//
// 顺序上**先落盘再生效**：写盘失败时运行态不动，用户看到的仍是旧目录表——比「运行态
// 已改、重启后又没了」可解释得多。
func (s skillActions) addDir(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("请填写技能目录路径")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%q 不是绝对路径（本程序没有工作目录概念，技能目录必须写绝对路径）", dir)
	}
	dir = filepath.Clean(dir)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s 不存在或不是文件夹", dir)
	}
	extras, fallback := s.extraDirs()
	if fallback != "" && samePath(dir, fallback) {
		return errors.New("这一项是内置的兜底技能目录，不需要再添加")
	}
	for _, d := range extras {
		if samePath(d, dir) {
			return fmt.Errorf("技能目录 %s 已在清单里", dir)
		}
	}
	next := append(append([]string{}, extras...), dir)
	if err := s.w.persistSkillDirs(next); err != nil {
		return err
	}
	if s.w.skills != nil {
		s.w.skills.SetDirs(append(append([]string{}, next...), fallback))
	}
	s.w.skillsRev.Add(1)
	return nil
}

// removeDir 移除一个额外技能根（兜底根不可删：删掉就没有安装落点了）。
func (s skillActions) removeDir(dir string) error {
	dir = strings.TrimSpace(dir)
	extras, fallback := s.extraDirs()
	if fallback != "" && samePath(dir, fallback) {
		return errors.New("内置的兜底技能目录不可移除")
	}
	next := make([]string, 0, len(extras))
	found := false
	for _, d := range extras {
		if samePath(d, dir) {
			found = true
			continue
		}
		next = append(next, d)
	}
	if !found {
		return fmt.Errorf("技能目录 %s 不在清单里（可能已被移除）", dir)
	}
	if err := s.w.persistSkillDirs(next); err != nil {
		return err
	}
	if s.w.skills != nil {
		s.w.skills.SetDirs(append(append([]string{}, next...), fallback))
	}
	s.w.skillsRev.Add(1)
	return nil
}

// samePath 路径等价判定（Windows 大小写不敏感；其余平台按字面）。
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if os.PathSeparator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ---------------------------------------------------------------------------
// uigui.SkillsAdmin（设置窗「技能」页）
// ---------------------------------------------------------------------------

// skillsAdmin 实现 uigui.SkillsAdmin：动作返回用户面中文摘要（页面直接放进状态行）。
type skillsAdmin struct{ w *wiring }

var _ uigui.SkillsAdmin = (*skillsAdmin)(nil)

func (a *skillsAdmin) actions() skillActions { return skillActions{w: a.w} }

// Snapshot 原子快照：清单（含台账里的来源种类与更新时间）+ 生效目录 + 未加载条目。
func (a *skillsAdmin) Snapshot() uigui.SkillsSnapshot {
	s := uigui.SkillsSnapshot{Revision: a.w.skillsRev.Load()}
	if a.w.skillInstall != nil {
		s.ManagedRoot = a.w.skillInstall.Root()
	}
	if a.w.skills == nil {
		return s
	}
	ctx := context.Background()
	infos, _ := a.w.skills.List(ctx)
	records := map[string]skillfs.Record{}
	if a.w.skillInstall != nil {
		for _, r := range a.w.skillInstall.Records() {
			records[r.Name] = r
		}
	}
	managed := s.ManagedRoot
	for _, info := range infos {
		st := uigui.SkillState{
			Name:        info.Name,
			Description: info.Description,
			Dir:         info.Dir,
			Source:      info.Source,
			Kind:        "external",
		}
		if managed != "" && pathInside(info.Dir, managed) {
			st.Managed = true
			st.Kind = "" // 手工放进安装目录：无来源可更新
		}
		if r, ok := records[info.Name]; ok {
			st.Kind = r.Kind
			st.Ref = r.Source
			st.Updatable = r.Kind == skillfs.KindGit || r.Kind == skillfs.KindZip
			st.Updated = shortTime(r.UpdatedAt, r.InstalledAt)
		}
		s.Skills = append(s.Skills, st)
	}
	fallback := filepath.Join(a.w.profileDir, "skills")
	for _, d := range a.w.skills.Dirs() {
		s.Dirs = append(s.Dirs, uigui.SkillDir{Path: d, Fallback: samePath(d, fallback)})
	}
	s.Warnings = a.w.skills.Warnings(ctx)
	return s
}

func (a *skillsAdmin) Install(source string) (string, error) {
	rep, err := a.actions().install(source)
	if err != nil {
		return "", err
	}
	return summarizeSkillReport("已安装", rep), nil
}

func (a *skillsAdmin) Update(name string) (string, error) {
	rep, err := a.actions().update(name)
	if err != nil {
		return "", err
	}
	return summarizeSkillReport("已更新", rep), nil
}

func (a *skillsAdmin) Remove(name string) error { return a.actions().remove(name) }

func (a *skillsAdmin) Rescan() error { return a.actions().rescan() }

func (a *skillsAdmin) AddDir(dir string) error { return a.actions().addDir(dir) }

func (a *skillsAdmin) RemoveDir(dir string) error { return a.actions().removeDir(dir) }

// summarizeSkillReport 安装/更新结果的一行摘要（用户面；跳过条目另起一行）。
func summarizeSkillReport(verb string, rep skillfs.InstallReport) string {
	names := make([]string, 0, len(rep.Installed))
	for _, s := range rep.Installed {
		names = append(names, s.Name)
	}
	var b strings.Builder
	if len(names) == 0 {
		fmt.Fprintf(&b, "%s 0 个技能", verb)
	} else {
		fmt.Fprintf(&b, "%s %d 个技能：%s", verb, len(names), strings.Join(names, "、"))
	}
	for _, n := range rep.Notes {
		b.WriteString("\n" + n)
	}
	if len(rep.Skipped) > 0 {
		fmt.Fprintf(&b, "\n未安装的条目（%d 条）：", len(rep.Skipped))
		for _, sk := range rep.Skipped {
			b.WriteString("\n- " + sk)
		}
	}
	return b.String()
}

// pathInside dir 是否落在 root 之下（Windows 大小写不敏感）。
func pathInside(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return true
}

// shortTime 台账时间戳（RFC3339）→ 用户面短格式（只留日期；解析不了原样返回）。
func shortTime(updated, installed string) string {
	ts := updated
	if ts == "" {
		ts = installed
	}
	if len(ts) >= 10 { // 2026-10-22T... → 2026-10-22
		return ts[:10]
	}
	return ts
}

// ---------------------------------------------------------------------------
// app.SkillAdmin（/skill install|remove|update）
// ---------------------------------------------------------------------------

// skillsAdminApp 实现 app.SkillAdmin（命令面）：返回结构化结果，排版交给命令层。
type skillsAdminApp struct{ w *wiring }

var _ app.SkillAdmin = (*skillsAdminApp)(nil)

func (a skillsAdminApp) Install(_ context.Context, source string) (app.SkillInstallReport, error) {
	rep, err := skillActions{w: a.w}.install(source)
	return toAppReport(rep), err
}

func (a skillsAdminApp) Remove(_ context.Context, name string) error {
	return skillActions{w: a.w}.remove(name)
}

func (a skillsAdminApp) Update(_ context.Context, name string) (app.SkillInstallReport, error) {
	rep, err := skillActions{w: a.w}.update(name)
	return toAppReport(rep), err
}

// toAppReport 内核结果 → 命令面结果（只搬名字与说明，不泄漏内部字段）。
func toAppReport(rep skillfs.InstallReport) app.SkillInstallReport {
	out := app.SkillInstallReport{Notes: rep.Notes, Skipped: rep.Skipped}
	for _, s := range rep.Installed {
		out.Skills = append(out.Skills, s.Name)
	}
	return out
}
