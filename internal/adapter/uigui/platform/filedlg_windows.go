//go:build windows

// 附件文件选择框（Windows，D104）：GetOpenFileNameW 模态呈现——请求投托盘线程
// （fileDlgMsg），模态泵不嵌 Gio 泵；选中路径经 Host.FilePicked 回传。
package platform

import (
	"syscall"
	"unsafe"
)

var comdlg32 = syscall.NewLazyDLL("comdlg32.dll")

var procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW") // D104 附件文件选择框

// 文件框常量。
const fileDlgMsg = wmApp + 5 // 托盘线程呈现文件选择框（D104：模态泵不嵌 Gio 泵）

// RequestFileDialog 请求 shell 线程打开文件选择框（D104：对话框自带模态泵、不能嵌 Gio
// 泵，D72 菜单同理）。shell 未就绪（启动微窗/headless）= 静默放弃。
func (p *Plat) RequestFileDialog() {
	if h := shellHWND.Load(); h != 0 {
		procPostMessageW.Call(h, fileDlgMsg, 0, 0)
	}
}

// openFileNameW OPENFILENAMEW（Win64 布局，GetOpenFileNameW 参数，D104）：
// 下划线字段 = x64 对齐填充（指针 8 对齐），lStructSize 传 unsafe.Sizeof。
type openFileNameW struct {
	lStructSize       uint32
	_                 uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	_                 uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	_                 uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

// showFileDlg 文件选择框呈现（shell 线程，D104）：GetOpenFileNameW 自带模态泵，阻塞
// 本线程与 TrackPopupMenu 同语义（D72 先例）；选中 → Host.FilePicked 回传，
// 取消/出错静默。单选 v1（多选留 UserInput.Raw 口径扩展，D104④）。
func (p *Plat) showFileDlg() {
	const (
		ofnHideReadOnly  = 0x4
		ofnNoChangeDir   = 0x8
		ofnPathMustExist = 0x800
		ofnFileMustExist = 0x1000
	)
	filter := utf16Pairs(
		"常见文档与图片",
		"*.pdf;*.docx;*.xlsx;*.pptx;*.html;*.htm;*.txt;*.md;*.csv;*.json;*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp",
		"文档",
		"*.pdf;*.docx;*.xlsx;*.pptx;*.html;*.htm;*.txt;*.md;*.csv;*.json",
		"图片",
		"*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp",
		"所有文件 (*.*)",
		"*.*",
	)
	title, err := syscall.UTF16FromString("选择附件")
	if err != nil {
		return
	}
	buf := make([]uint16, 32768)
	ofn := openFileNameW{
		lStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		lpstrFilter: &filter[0],
		lpstrFile:   &buf[0],
		nMaxFile:    uint32(len(buf)),
		lpstrTitle:  &title[0],
		flags:       ofnHideReadOnly | ofnNoChangeDir | ofnPathMustExist | ofnFileMustExist,
	}
	if h := shellHWND.Load(); h != 0 {
		ofn.hwndOwner = h
	}
	r, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return
	}
	if p.host != nil {
		p.host.FilePicked(syscall.UTF16ToString(buf))
	}
}

// utf16Pairs 对话框过滤器串：段间单 NUL、末尾双 NUL 的 UTF-16（段 = 显示名/模式交替）。
func utf16Pairs(segs ...string) []uint16 {
	var b []uint16
	for _, s := range segs {
		w, err := syscall.UTF16FromString(s)
		if err != nil {
			continue
		}
		b = append(b, w...) // UTF16FromString 自带终止 NUL = 段间分隔
	}
	return append(b, 0) // 末段收双 NUL
}
