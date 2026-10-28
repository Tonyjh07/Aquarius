//go:build windows

// 附件文件选择框（Windows，D104）：GetOpenFileNameW 模态呈现——请求投托盘线程
// （fileDlgMsg），模态泵不嵌 Gio 泵；选中路径经 Host.FilePicked 回传。
package platform

import (
	"fmt"
	"syscall"
	"unsafe"
)

var comdlg32 = syscall.NewLazyDLL("comdlg32.dll")

var (
	procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")     // D104 附件文件选择框
	procGetSaveFileNameW = comdlg32.NewProc("GetSaveFileNameW")     // 保存对话框（会话导出）
	procCommDlgExtErr    = comdlg32.NewProc("CommDlgExtendedError") // 0 = 用户取消，非 0 = 出错
)

// commDlgExtendedError 取上一次通用对话框的错误码（0 = 用户取消）。
func commDlgExtendedError() uint32 {
	r, _, _ := procCommDlgExtErr.Call()
	return uint32(r)
}

// 文件框常量。
const (
	fileDlgMsg  = wmApp + 5 // 托盘线程呈现文件选择框（D104：模态泵不嵌 Gio 泵）
	openFileMsg = wmApp + 7 // 托盘线程呈现通用单选文件框（会话导入）
	saveFileMsg = wmApp + 8 // 托盘线程呈现保存框（会话导出）
)

// RequestFileDialog 请求 shell 线程打开文件选择框（D104：对话框自带模态泵、不能嵌 Gio
// 泵，D72 菜单同理）。shell 未就绪（启动微窗/headless）= 静默放弃。
func (p *Plat) RequestFileDialog() {
	if h := shellHWND.Load(); h != 0 {
		procPostMessageW.Call(h, fileDlgMsg, 0, 0)
	}
}

// RequestOpenFile 请求通用单选文件框（会话导入）：请求体先入原子槽，再投消息让 shell
// 线程取走呈现——模态泵不能嵌 Gio 泵，与附件框同一口径。
//
// 已有一个框在等时直接放弃本次请求：对话框挂在消息窗上，模态期间主窗仍可点，连点两次会
// 覆盖同一份请求体（用途可能都变了），比「这次没反应」更坏。
func (p *Plat) RequestOpenFile(req OpenRequest) {
	h := shellHWND.Load()
	if h == 0 {
		return // 启动微窗/headless：无处呈现，静默放弃
	}
	if !p.dlgBusy.CompareAndSwap(false, true) {
		return
	}
	p.dlgReq.Store(&dlgRequest{purpose: req.Purpose, title: req.Title, filters: req.Filters})
	procPostMessageW.Call(h, openFileMsg, 0, 0)
}

// RequestSaveFile 请求保存框（会话导出）：同 RequestOpenFile。
func (p *Plat) RequestSaveFile(req SaveRequest) {
	h := shellHWND.Load()
	if h == 0 {
		return
	}
	if !p.dlgBusy.CompareAndSwap(false, true) {
		return
	}
	p.dlgReq.Store(&dlgRequest{
		save: true, purpose: req.Purpose, title: req.Title,
		name: req.Name, defExt: req.DefExt, filters: req.Filters,
		filterIndex: req.FilterIndex,
	})
	procPostMessageW.Call(h, saveFileMsg, 0, 0)
}

// showOpenFile 呈现单选文件框（shell 线程）：选中 → Host.FilePicked；取消/出错静默。
func (p *Plat) showOpenFile() {
	defer p.dlgBusy.Store(false) // 无论取消还是选中，框已关，下一次点击可以再问
	req := p.dlgReq.Swap(nil)
	if req == nil {
		return
	}
	path, _ := p.openOneFile(req.title, req.filters)
	if path == "" || p.host == nil {
		return
	}
	// 导入侧不看档位（认格式靠内容），-1 = 没有这回事。
	p.host.FilePicked(req.purpose, path, -1)
}

// showSaveFile 呈现保存框（shell 线程）：选定落点 → Host.FilePicked。
//
// 不设 OFN_OVERWRITEPROMPT：导出是「再存一份」，同名时命令面会加 -2 后缀并在回执里
// 写清真落到哪个文件——让用户在对话框里确认覆盖、转头又存成 -2，反而对不上。
func (p *Plat) showSaveFile() {
	defer p.dlgBusy.Store(false)
	req := p.dlgReq.Swap(nil)
	if req == nil {
		return
	}
	path, idx := p.saveOneFile(req.title, req.name, req.defExt, req.filters, req.filterIndex)
	if path == "" || p.host == nil {
		return
	}
	p.host.FilePicked(req.purpose, path, idx)
}

// openOneFile 单选文件（OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST，不带多选）。
func (p *Plat) openOneFile(title string, filters []FileFilter) (string, int) {
	const (
		ofnHideReadOnly  = 0x4
		ofnNoChangeDir   = 0x8
		ofnPathMustExist = 0x800
		ofnFileMustExist = 0x1000
		ofnExplorer      = 0x80000
	)
	return p.runFileDlg(procGetOpenFileNameW, title, "", "", filters, 0,
		ofnHideReadOnly|ofnNoChangeDir|ofnExplorer|ofnPathMustExist|ofnFileMustExist)
}

// saveOneFile 保存框（OFN_PATHMUSTEXIST，不要求目标已存在）。
func (p *Plat) saveOneFile(title, name, defExt string, filters []FileFilter, filterIndex int) (string, int) {
	const (
		ofnHideReadOnly  = 0x4
		ofnNoChangeDir   = 0x8
		ofnPathMustExist = 0x800
		ofnExplorer      = 0x80000
	)
	return p.runFileDlg(procGetSaveFileNameW, title, name, defExt, filters, filterIndex,
		ofnHideReadOnly|ofnNoChangeDir|ofnExplorer|ofnPathMustExist)
}

// runFileDlg 打开/保存框共用一次呈现：proc 决定是哪一个 proc，其余参数照 OPENFILENAMEW
// 填。返回空路径 = 取消或出错；第二个返回值是**用户选中的过滤器档位（0 起，-1 = 报不出）**。
//
// nFilterIndex 是 OPENFILENAME 里唯一的「入参兼出参」字段：进去指定预选哪一档，回来是
// 用户最终停在哪一档。这两件事无法从路径反推（用户可以把文件名写成任何扩展名，也可以
// 在「保存类型」里换了档却不改文件名），所以只能读它——`--format` 就靠这个值定。
func (p *Plat) runFileDlg(proc *syscall.LazyProc, title, name, defExt string, filters []FileFilter, filterIndex int, flags uint32) (string, int) {
	filter := utf16Filters(filters)
	t, err := syscall.UTF16FromString(title)
	if err != nil {
		return "", -1
	}
	buf := make([]uint16, 32768)
	if name != "" {
		// 预填文件名：调用方给的词干可能含中文，UTF16FromString 的终止 NUL 正好补齐。
		if w, err := syscall.UTF16FromString(name); err == nil && len(w) <= len(buf) {
			copy(buf, w)
		}
	}
	ofn := openFileNameW{
		lStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		lpstrFilter: &filter[0],
		lpstrFile:   &buf[0],
		nMaxFile:    uint32(len(buf)),
		lpstrTitle:  &t[0],
		flags:       flags,
	}
	if defExt != "" {
		if w, err := syscall.UTF16FromString(defExt); err == nil {
			ofn.lpstrDefExt = &w[0]
		}
	}
	// 预算档位：Win32 的 nFilterIndex 是 **1 起**（0 = 自定义过滤器）。越界不传，
	// 让对话框自己落到第一档（传一个不存在的序号会被当成自定义档，反而没有类型可选）。
	if filterIndex >= 0 && filterIndex < len(filters) {
		ofn.nFilterIndex = uint32(filterIndex) + 1
	}
	if h := shellHWND.Load(); h != 0 {
		ofn.hwndOwner = h
	}
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		// r == 0 既可能是「用户取消」，也可能是「出错」：后者要再查一次
		// CommDlgExtendedError 才分得清（Call 的第三个返回值不是可靠的 GetLastError 通道）。
		if code := commDlgExtendedError(); code != 0 {
			p.noticeOnce(&p.dlgNotice, fmt.Sprintf("文件对话框没能打开（错误码 %d），请重试或换个路径", code))
		}
		return "", -1
	}
	paths := parseMultiSelect(buf) // 单选也是这条解析（一个 token = 完整路径）
	if len(paths) == 0 {
		return "", -1
	}
	// 回读档位：1 起 → 0 起。0 或超出档数（对话框给了自定义过滤器）一律报 -1，
	// 让调用方走「按扩展名回落」而不是错认成第一档。
	idx := -1
	if n := int(ofn.nFilterIndex); n >= 1 && n <= len(filters) {
		idx = n - 1
	}
	return paths[0], idx
}

// utf16Filters 过滤器列表 → 对话框串（label/pattern 成对，末尾双 NUL）；空表给「所有文件」。
func utf16Filters(filters []FileFilter) []uint16 {
	if len(filters) == 0 {
		filters = []FileFilter{{Label: "所有文件 (*.*)", Pattern: "*.*"}}
	}
	segs := make([]string, 0, len(filters)*2)
	for _, f := range filters {
		segs = append(segs, f.Label, f.Pattern)
	}
	return utf16Pairs(segs...)
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

// attachFilter 附件选择框的过滤器（label、pattern 成对，末尾多一个 NUL）。
//
// 只列**真能转成文本**的格式：pptx/rtf/legacy 二进制 Office 没有解析支持，选中后只能
// 拿到“二进制、未提取”的占位，进去还要模型自己去读一个 zip——选得到却读不出内容，比
// 选不到更糟。确实要附这些格式的仍可走「所有文件」、拖拽或命令行附件参数。
var attachFilter = []string{
	"常见文档与图片",
	"*.pdf;*.docx;*.xlsx;*.html;*.htm;*.txt;*.md;*.csv;*.json;*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp",
	"文档",
	"*.pdf;*.docx;*.xlsx;*.html;*.htm;*.txt;*.md;*.csv;*.json",
	"图片",
	"*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp",
	"所有文件 (*.*)",
	"*.*",
}

// showFileDlg 文件选择框呈现（shell 线程，D104）：GetOpenFileNameW 自带模态泵，阻塞
// 本线程与 TrackPopupMenu 同语义（D72 先例）；选中 → Host.FilesPicked 回传，
// 取消/出错静默。
// D186 口径五：开 OFN_ALLOWMULTISELECT | OFN_EXPLORER，一次可选多个附件。
func (p *Plat) showFileDlg() {
	const (
		ofnHideReadOnly     = 0x4
		ofnNoChangeDir      = 0x8
		ofnAllowMultiSelect = 0x200
		ofnPathMustExist    = 0x800
		ofnFileMustExist    = 0x1000
		ofnExplorer         = 0x80000
	)
	filter := utf16Pairs(attachFilter...)
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
		flags: ofnHideReadOnly | ofnNoChangeDir | ofnAllowMultiSelect | ofnExplorer |
			ofnPathMustExist | ofnFileMustExist,
	}
	if h := shellHWND.Load(); h != 0 {
		ofn.hwndOwner = h
	}
	r, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return
	}
	if p.host == nil {
		return
	}
	paths := parseMultiSelect(buf)
	if len(paths) == 0 {
		return
	}
	p.host.FilesPicked(paths)
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
