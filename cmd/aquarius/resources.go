package main

// Windows 资源嵌入：程序图标与版本信息。
//
// go-winres 依 winres/winres.json 生成 rsrc_windows_<arch>.syso；链接器按文件名的
// GOOS/GOARCH 构建约束只在 Windows 目标下纳入这些对象文件，linux/darwin 目标不受影响。
// 换图标（cmd/iconify 先重生成 assets/icon）或改版本（winres.json 的 fixed/info）后，
// 重新生成并提交产物：
//
//	go generate ./cmd/aquarius
//
//go:generate go run github.com/tc-hib/go-winres@v0.3.3 make --in winres/winres.json --arch amd64,386,arm64
