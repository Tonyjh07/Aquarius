// Package assets 内嵌随包图标资产（iconify 生成物，源与产物均入库）。
// 单二进制交付：运行时（托盘图标等）从这里取字节，不依赖盘上文件。
package assets

import _ "embed"

// TrayICO 托盘图标（icon/aquarius.ico，多尺寸；uigui 取 16px 档，§15.1）。
//
//go:embed icon/aquarius.ico
var TrayICO []byte
