// Package platform 提供 GUI（`uigui`）所需的平台能力（D111/S4b）。
//
// 分层口径（D111 修订）：
//   - 本包**不 import gio**，也不 import `uigui`——因此三平台都能单独交叉编译与单测
//     （`uigui` 因 Gio 只能在 WSL/Linux 侧验，见 D119）；需要回传 UI 语义时只经注入的
//     `Host` 回调（菜单命令 ID、显隐/退出/文件选中之类的动作枚举），业务语义留在 `uigui`。
//   - `uigui`（消费方）声明所需接口（见 `internal/adapter/uigui/platform_api.go`），本包
//     提供各平台**具体实现** `*Plat`（Go 隐式接口，就地断言）。
//   - 各平台实现按构建标签分文件（`*_windows.go` / `*_other.go`），方法名与签名必须一致，
//     否则 `*Plat` 无法满足消费方接口——这正是编译期保证「降级实现不缺席」的机制。
//
// 能力范围：窗口句柄与帧事件挂钩、显隐/置顶/移动/尺寸、像素提交（ULW 或降级）、托盘、
// 全局快捷键、文件对话框、右键菜单、系统深浅色、DPI、系统字体候选。
package platform
