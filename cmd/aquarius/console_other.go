//go:build !windows

package main

// 非 Windows 无「自建控制台」语义（D108/D114 仅适用 Windows console 子系统）：恒不
// 脱离，stderr 恒可见，无消息框兜底。

func hideSpawnedConsole() bool { return false }

func restoreConsole() bool { return false }

// consoleVisible 非 Windows 没有「自建控制台」这一层：从终端启动是常态，回显照开。
func consoleVisible() bool { return true }

func notifyFatal(string) {}
