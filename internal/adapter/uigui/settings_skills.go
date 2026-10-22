package uigui

// settings_skills.go 「技能」页数据面与异步动作（D202/D203）：消费方定义接口与快照
// 类型，装配根实现（`cmd/aquarius/skillsadmin.go`，落在 `skillfs.Installer` 上）。
//
// 与密钥库页的差别：安装/更新会 clone 仓库或下载压缩包（秒级到分钟级），故动作一律
// **异步**（与工具页同口径：旁路 goroutine + 结果通道 + Invalidate），页面只读快照。
//
// 边界（D203 口径三）：只认用户给的路径或链接，不做「技能市场 / 仓库浏览」——安装面
// 越像应用商店，越容易把「远端说了算」的指令包塞进上下文。

import (
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
)

// SkillsAdmin 技能管理面（装配根实现；线程安全——命令面与次窗 goroutine 共用）。
type SkillsAdmin interface {
	// Snapshot 原子快照（含 Revision，任何变更递增）。
	Snapshot() SkillsSnapshot
	// Install 安装一个来源（路径或 GitHub 链接）；返回用户面结果文案。
	Install(source string) (string, error)
	// Update 更新一个（name 空 = 全部可更新项）；返回用户面结果文案。
	Update(name string) (string, error)
	// Remove 卸载一个安装副本。
	Remove(name string) error
	// Rescan 重新扫描技能根。
	Rescan() error
	// AddDir 追加一个额外技能根（写回 config `skills.dirs`，热生效）。
	AddDir(dir string) error
	// RemoveDir 移除一个额外技能根（兜底根不可删）。
	RemoveDir(dir string) error
}

// SkillsSnapshot 「技能」页只读快照。
type SkillsSnapshot struct {
	// Revision 变更序号（任何装/卸/更新/目录增删/重扫都递增；界面据此重建行）。
	Revision int64
	// Skills 已发现技能（技能根序 + 根内目录名序，与工具描述同源）。
	Skills []SkillState
	// Dirs 生效技能根（按查找顺序；最后一项是兜底根）。
	Dirs []SkillDir
	// Warnings 未加载的条目与原因（用户面，不进模型上下文）。
	Warnings []string
	// ManagedRoot 安装副本根（`<profile>/skills`）；卸载只动这里。
	ManagedRoot string
}

// SkillState 一个已发现技能。
type SkillState struct {
	Name        string
	Description string
	Dir         string // 技能目录绝对路径
	Source      string // 命中的技能根（多根时说明哪一份生效）
	// Managed 目录是否在安装副本根下（决定「卸载」入口显不显示）。
	Managed bool
	// Kind 安装来源种类：git / zip / file / dir；空 = 手工放置或来自外部技能根。
	Kind string
	// Ref 来源（用户给的路径或链接原文）。
	Ref string
	// Updated 最近更新时间（用户面短格式；空 = 未更新过或手工放置）。
	Updated string
	// Updatable 是否有更新动作（git 与压缩包来源可更新；复制来源要重新导入）。
	Updatable bool
}

// SkillDir 一个生效的技能根。
type SkillDir struct {
	Path string
	// Fallback 兜底根（`<profile>/skills`，恒在最后，不可删）。
	Fallback bool
}

// skillActionResult 技能页异步动作结果（回灌状态行）。
type skillActionResult struct {
	status string
	err    error
}

// skillsAsync 技能页动作统一异步执行（旁路 goroutine；任何路径都不阻塞次窗）。
func (u *UI) skillsAsync(f *settingsForm, do func() (string, error)) {
	f.skillWait++
	go func() {
		status, err := do()
		f.skillRes <- skillActionResult{status: status, err: err}
		if f.win != nil {
			f.win.Invalidate()
		}
	}()
}

// drainSkillResults 取尽异步动作结果（帧内非阻塞）。
func (f *settingsForm) drainSkillResults() {
	for {
		select {
		case r := <-f.skillRes:
			if f.skillWait > 0 {
				f.skillWait--
			}
			f.skillsStatus, f.skillsErr = r.status, r.err != nil
			if r.err != nil {
				f.skillsStatus = "操作失败：" + r.err.Error()
			}
		default:
			return
		}
	}
}

// syncSkillsSnapshot 技能页原子快照同步（Revision 变了才重建行与按钮）。
func (f *settingsForm) syncSkillsSnapshot() {
	if f.skills == nil {
		return
	}
	cur := f.skills.Snapshot()
	if cur.Revision != f.skillsRev {
		f.skillsRev = cur.Revision
		f.skillsSnap = cur
	}
}

// skillPathPick 选择框回传的一笔结果（平台线程 → 设置窗 goroutine）。
type skillPathPick struct {
	kind platform.PickKind
	path string
}

// pushSkillPick 投递选择框结果（平台线程调用）：通道满即丢（用户重选一次即可），
// 随后对设置窗请求重绘让帧首取走。
func (u *UI) pushSkillPick(p skillPathPick) {
	select {
	case u.skillPicks <- p:
	default:
	}
	if u.wins != nil {
		u.wins.invalidateKind(winSettings)
	}
}

// drainSkillPicks 帧首取走选择框结果：**只填来源框**，由用户再点「安装」才落盘
// （D203 口径四：选择动作 ≠ 安装动作，避免误点即复制一整棵树）。
func (f *settingsForm) drainSkillPicks(u *UI) {
	if u == nil || u.skillPicks == nil {
		return
	}
	for {
		select {
		case p := <-u.skillPicks:
			f.skillSource.SetText(p.path)
			hint := "已选择文件夹"
			if p.kind == platform.PickSkillFile {
				hint = "已选择文件"
			}
			f.skillsStatus, f.skillsErr = hint+"："+p.path+"（点「安装」开始导入）", false
		default:
			return
		}
	}
}

// skillMetaLine 技能行第二行：来源种类 / 更新时间 / 生效目录（用户面短文案）。
func (st SkillState) metaLine() string {
	parts := []string{}
	switch st.Kind {
	case "git":
		parts = append(parts, "git 安装")
	case "zip":
		parts = append(parts, "压缩包安装")
	case "file":
		parts = append(parts, "单文件安装")
	case "dir":
		parts = append(parts, "文件夹安装")
	default:
		parts = append(parts, "手工放置")
	}
	if st.Updated != "" {
		parts = append(parts, "更新于 "+st.Updated)
	}
	parts = append(parts, st.Dir)
	return strings.Join(parts, " · ")
}

// skillDirLabel 技能根展示文案（兜底根注明「兜底」，不写内部字段名）。
func (d SkillDir) skillDirLabel() string {
	if d.Fallback {
		return d.Path + "（兜底，不可删）"
	}
	return d.Path
}
