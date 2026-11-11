// Package assets 内嵌随包资源：图标（iconify 生成物）与落进数据目录的说明正文。
// 单二进制交付：运行时（托盘图标、数据目录 README 等）从这里取字节，不依赖盘上文件。
package assets

import _ "embed"

// TrayICO 托盘图标（icon/aquarius.ico，多尺寸；uigui 取 16px 档，§15.1）。
//
//go:embed icon/aquarius.ico
var TrayICO []byte

// LogoPNG 品牌图标（icon/icon-128.png，透明底）——悬浮球圆钮内嵌的 logo（§15.2）。
//
//go:embed icon/icon-128.png
var LogoPNG []byte

// DataReadmeZH 数据目录说明的中文正文（D291）。两份 md 由装配根拼成一份文件，
// 与 internal/legal 同一形态：正文是给人读的 Markdown，改动要能在 diff 里看见，
// 所以不拼进 Go 字符串字面量、也不进 i18n 表。
//
//go:embed data-readme/zh.md
var DataReadmeZH []byte

// DataReadmeEN 数据目录说明的英文正文（D291），与 DataReadmeZH 同结构、同条目名。
//
//go:embed data-readme/en.md
var DataReadmeEN []byte

// DataReadmeHeader 生成文件首行的模板（D291），`{version}` 换成构建版本号。单独成一个
// 资源是为了让装配根里一个中文字都不留——落盘内容不是界面文案，两种语言同时出现，进
// i18n 表只会取到其中一种。
//
//go:embed data-readme/header.txt
var DataReadmeHeader []byte
