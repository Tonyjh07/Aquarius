//go:build !windows

package main

// 非 Windows 无「自建控制台」语义（D108 仅适用 Windows console 子系统）：恒不隐藏。

func hideSpawnedConsole() bool { return false }

func restoreConsole() {}
