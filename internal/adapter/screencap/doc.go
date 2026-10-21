// Package screencap 是 port.Capture 的 Windows 实现（DESIGN §5.11 / D178）：
// 窗口清单走 EnumWindows，抓窗口先 PrintWindow（PW_RENDERFULLCONTENT，不依赖是否被遮挡），
// 失败且窗口可见时回退到屏幕区域的 BitBlt；整屏 = 整个虚拟桌面。
//
// 适配器只负责「抓得到」：输出 PNG 原始像素尺寸，不做 DPI 缩放、不落盘、不进上下文。
package screencap
