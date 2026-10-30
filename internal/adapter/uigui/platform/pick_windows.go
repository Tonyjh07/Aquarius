//go:build windows

// 技能导入的专用选择框（D203 口径四）：与附件槽（多选 + 直投附件）分开——用途不同，
// 语义也不同（选文件夹没有「多选」可言）。仍然投托盘线程呈现（文件框自带模态泵、
// 不能嵌 Gio 泵，D104/D72 先例）。
package platform

import (
	"syscall"
	"unsafe"
)

var (
	procSHBrowseForFolderW  = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree       = ole32.NewProc("CoTaskMemFree")
)

// ole32 COM 内存释放（SHBrowseForFolderW 返回的 PIDL 由调用方释放）。
var ole32 = syscall.NewLazyDLL("ole32.dll")

// skillPickMsg 托盘线程呈现技能选择框（wParam = PickKind）。
const skillPickMsg = wmApp + 6

// RequestPathPick 请求呈现技能导入选择框（kind 决定选文件还是选文件夹）。
func (p *Plat) RequestPathPick(kind PickKind) {
	h := shellHWND.Load()
	if h == 0 {
		return // 启动微窗/headless：选择框无处呈现，静默放弃（界面回落到手填路径）
	}
	procGoToSkillPick := procPostMessageW
	procGoToSkillPick.Call(h, skillPickMsg, uintptr(kind), 0)
}

// showSkillPick 呈现选择框（shell 线程）：选中 → Host.SkillPathPicked；取消/出错静默。
func (p *Plat) showSkillPick(kind PickKind) {
	var path string
	switch kind {
	case PickSkillDir:
		path = browseForFolder("选择技能文件夹")
	case PickCharacterCard:
		path = openCharacterCard()
	default:
		path = openSkillFile()
	}
	if path == "" || p.host == nil {
		return
	}
	p.host.SkillPathPicked(kind, path)
}

// openSkillFile 单选技能文件（SKILL.md / .zip）。
func openSkillFile() string {
	filter := utf16Pairs(
		"技能文件 (SKILL.md, *.zip)",
		"SKILL.md;*.zip",
		"Markdown (*.md)",
		"*.md",
		"压缩包 (*.zip)",
		"*.zip",
		"所有文件 (*.*)",
		"*.*",
	)
	return openSingleFile(filter, "选择技能文件")
}

// openCharacterCard 单选角色卡文件（PNG / JSON / .charx）。
func openCharacterCard() string {
	filter := utf16Pairs(
		"角色卡 (PNG / JSON / .charx)",
		"*.png;*.json;*.charx",
		"PNG 卡 (*.png)",
		"*.png",
		"JSON 卡 (*.json)",
		"*.json",
		"角色卡包 (*.charx)",
		"*.charx",
		"所有文件 (*.*)",
		"*.*",
	)
	return openSingleFile(filter, "选择角色卡文件")
}

// openSingleFile 单选文件框（OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST）：filter 为
// utf16Pairs 组好的过滤器，titleText 为标题。选中即返回路径，取消/出错返回空串。
func openSingleFile(filter []uint16, titleText string) string {
	const (
		ofnHideReadOnly  = 0x4
		ofnNoChangeDir   = 0x8
		ofnPathMustExist = 0x800
		ofnFileMustExist = 0x1000
		ofnExplorer      = 0x80000
	)
	title, err := syscall.UTF16FromString(titleText)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 32768)
	ofn := openFileNameW{
		lStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		lpstrFilter: &filter[0],
		lpstrFile:   &buf[0],
		nMaxFile:    uint32(len(buf)),
		lpstrTitle:  &title[0],
		flags:       ofnHideReadOnly | ofnNoChangeDir | ofnExplorer | ofnPathMustExist | ofnFileMustExist,
	}
	if h := shellHWND.Load(); h != 0 {
		ofn.hwndOwner = h
	}
	r, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return ""
	}
	paths := parseMultiSelect(buf) // 单选也是这条解析（一个 token = 完整路径）
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// browseInfoW BROWSEINFOW（Win64 布局；SHBrowseForFolderW 参数）。
type browseInfoW struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

// browseForFolder 文件夹选择框（SHBrowseForFolderW）：BIF_RETURNONLYFSDIRS |
// BIF_NEWDIALOGSTYLE（可新建/可输入路径）| BIF_EDITBOX。
func browseForFolder(title string) string {
	const (
		bifReturnOnlyFSDirs = 0x0001
		bifEditBox          = 0x0010
		bifNewDialogStyle   = 0x0040
	)
	t, err := syscall.UTF16FromString(title)
	if err != nil {
		return ""
	}
	display := make([]uint16, 260)
	bi := browseInfoW{
		pszDisplayName: &display[0],
		lpszTitle:      &t[0],
		ulFlags:        bifReturnOnlyFSDirs | bifEditBox | bifNewDialogStyle,
	}
	if h := shellHWND.Load(); h != 0 {
		bi.hwndOwner = h
	}
	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer procCoTaskMemFree.Call(pidl)
	pathBuf := make([]uint16, 32768)
	ok, _, _ := procSHGetPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&pathBuf[0])))
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(pathBuf)
}
