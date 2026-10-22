package platform

// 平台层 DTO（中性定义：不含任何平台类型，Windows/非 Windows 实现共用）。
//
// `uigui` 内部另有 point/rect（小写字段、自绘几何用），两侧在门面处转换——DTO 用导出
// 字段是跨包可见性的硬要求。

// Handle 原生窗口句柄（HLD）。非 Windows 恒 0（无句柄概念；Gio 不暴露原生窗口）。
type Handle uintptr

// Point 屏幕坐标（物理 px）。
type Point struct{ X, Y int32 }

// Rect 屏幕矩形（物理 px；Min/Max 语义同 image.Rectangle：含 Min 不含 Max）。
type Rect struct{ Left, Top, Right, Bottom int32 }

// Width 矩形宽（<=0 表示空矩形）。
func (r Rect) Width() int32 { return r.Right - r.Left }

// Height 矩形高（<=0 表示空矩形）。
func (r Rect) Height() int32 { return r.Bottom - r.Top }

// RunFunc 把修改性调用送到指定窗口线程执行并等待完成（§15.6 铁律 1；实现 = Gio
// `Window.Run`）。签名中性：平台层只当它是一个「在窗口线程跑 f」的能力。
type RunFunc func(f func())

// MenuKind 菜单出处（项集由 `uigui` 按 kind 现组，平台只负责呈现与回传命令 ID）。
type MenuKind int

const (
	// MenuTray 托盘图标右键菜单（§15.1）。
	MenuTray MenuKind = iota
	// MenuLogo 悬浮球 logo 右键菜单（D72/D73）。
	MenuLogo
	// MenuBubble 气泡右键菜单（D92/D97-D99）。
	MenuBubble
)

// MenuItem 菜单项（中性）：ID 0 = 分隔线；Sub 非空 = 二级菜单（父项不用 ID）。
type MenuItem struct {
	ID      uintptr
	Label   string
	Checked bool
	Sub     []MenuItem
}

// PickKind 专用选择框的用途（技能导入，D203 口径四）：决定过滤器与是否选目录。
// 附件槽用固定的「多选附件」语义（RequestFileDialog + FilesPicked），不复用本枚举。
type PickKind int

const (
	// PickSkillFile 单个文件：`SKILL.md` 或 `.zip`。
	PickSkillFile PickKind = iota
	// PickSkillDir 文件夹（技能目录，递归复制）。
	PickSkillDir
)

// Config 构造选项（装配根/uigui 注入）。
type Config struct {
	// TrayIcon 托盘图标图像字节（.ico 里的某一个条目；档位由 uigui 定，默认 32px——
	// shell 在 >100% DPI 下按这张单张位图缩放到托盘尺寸，档位越高越少插值）。
	// 空 = 平台用系统默认图标。
	TrayIcon []byte
	// Hotkey 初始全局快捷键（如 "Alt+A"）；空 = 默认。运行时改绑经 Host.HotkeySetting。
	Hotkey string
}

// Host 平台回调宿主（由 uigui 实现）：平台层只回传**动作枚举**，不解析业务语义。
type Host interface {
	// MenuItems 现取某处菜单的项集（勾选态/权限档随时间变，故呈现前回调而非预置）。
	MenuItems(kind MenuKind) []MenuItem
	// MenuChosen 菜单项被选中（ID 与 MenuItems 回传的一致；0 = 取消/未选）。
	MenuChosen(kind MenuKind, id uintptr)
	// ToggleShow 切换主窗显隐（托盘左键）。
	ToggleShow()
	// HotkeyPressed 全局快捷键触发（隐藏 → 呼出；可见 → 展开↔收起）。
	HotkeyPressed()
	// ToggleTopMost 切换主窗置顶（含持久化）。
	ToggleTopMost()
	// Exit 经菜单退出（收编次窗、中断进行中轮次、走 EOF 收尾）。
	Exit()
	// FilesPicked 文件选择框选中路径（可多选；空 = 取消）。
	FilesPicked(paths []string)
	// SystemThemeChanged 系统深浅色变化（仅 system 档需重解析）。
	SystemThemeChanged()
	// HotkeySetting 当前快捷键配置（改绑时现取）。
	HotkeySetting() string
	// Notice 降级提示（缺失能力告知用户；平台层不直接碰 UI）。
	Notice(msg string)
	// SkillPathPicked 技能导入的专用选择框结果（kind 区分文件/文件夹；空路径 = 取消）。
	// 与 FilesPicked 分开：附件槽是「多选 + 直投附件」，用途与语义都不同（D203 口径四）。
	SkillPathPicked(kind PickKind, path string)
}
