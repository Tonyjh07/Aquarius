//go:build !windows

package main

// 非 Windows 无「自建控制台」语义（D108/D114 仅适用 Windows console 子系统）：恒不
// 脱离，stderr 恒可见，无消息框兜底。

func hideSpawnedConsole() bool { return false }

func restoreConsole() bool { return false }

func notifyFatal(string) {}
