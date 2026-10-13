module github.com/Tonyjh07/Aquarius/spike/giospike

// D137：与主模块口径对齐（主模块 go.mod 的 go 指针 = 1.26）。原 1.27.0 高于主模块，
// 照文档跑 spike 的人会白装一个更高的工具链。
go 1.26.0

require gioui.org v0.10.2

require (
	gioui.org/shader v1.0.9 // indirect
	github.com/go-text/typesetting v0.3.4 // indirect
	golang.org/x/exp/shiny v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/image v0.26.0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/text v0.32.0 // indirect
)
