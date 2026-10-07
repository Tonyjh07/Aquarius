package uigui

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// 位置记忆（§15.1：拖拽 + 位置记忆，含多显示器工作区夹取；置顶态随存——
// §15.1 置顶开关）。posMu：拖动保存（帧循环）与菜单切换保存（托盘线程）互斥。
// Docked 停靠边（D50）：left/right = 停靠中（恢复按当前工作区重算停靠位、X/Y 忽略），
// 缺省/旧文件空键 = 未停靠。
type posRec struct {
	X, Y int32
	// TopMost 置顶态；nil = 旧文件/未设置 → 缺省置顶。
	TopMost *bool `json:"top_most,omitempty"`
	// Docked 停靠边；"" = 未停靠（omitted 保证旧文件兼容）。
	Docked string `json:"docked,omitempty"`
}

var posMu sync.Mutex

func loadPos(path string) (posRec, bool) {
	posMu.Lock()
	defer posMu.Unlock()
	src, err := os.ReadFile(path)
	if err != nil {
		return posRec{}, false
	}
	var p posRec
	if json.Unmarshal(src, &p) != nil {
		return posRec{}, false
	}
	return p, true
}

func savePos(path string, p posRec) {
	posMu.Lock()
	defer posMu.Unlock()
	src, _ := json.Marshal(p)
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	_ = os.WriteFile(path, src, 0o644)
}

// commitWinGeom 一拍提交本帧屏幕态（移窗，经 Window.Run，§15.6 铁律 1）。
// D55：帧内改动一律只记账，由这里统一 flush——分散发起会各占一拍。D62：形裁/alpha
// 通道退役（随位图同拍提交），仅剩移窗。
func (u *UI) commitWinGeom() {
	if u.movePending {
		u.movePending = false
		u.moveWindowTo(u.x, u.y)
	}
}

// applyConfiguredSize 启动期把配置像素尺寸落到 OS 窗口（D90/§15.8）：WindowWidth/
// Height 均非 0 才生效（0 = 缺省 dp 建窗，现行为）。夹取按挂接点实测 DPI × 当前缩放。
func (u *UI) applyConfiguredSize() {
	if u.opts.WindowWidth <= 0 || u.opts.WindowHeight <= 0 || u.hwnd == 0 {
		return
	}
	w, h := clampWindowPx(u.opts.WindowWidth, u.opts.WindowHeight,
		u.plat.WindowDPI(u.hwnd), u.zoomLoad().scale)
	u.resizeWindowTo(int32(w), int32(h))
}

// clampWindowPx 窗口像素夹取（D90）：粗界 [200,3840]×[200,2160]，再按布局地板
// （layoutFloorDp × DPI × scale）抬下限。纯函数；config 侧 DPI/缩放未知时传 1,1（仅粗界）。
func clampWindowPx(w, h int, dpi, scale float64) (int, int) {
	w = max(winPxMinW, min(w, winPxMaxW))
	h = max(winPxMinH, min(h, winPxMaxH))
	if floor := int(math.Round(layoutFloorDp * dpi * scale)); floor > 0 {
		w = max(w, floor)
		h = max(h, floor)
	}
	return w, h
}
