package acpgate

// 执行控制类文件：写这些位置要单独问一次。
//
// 协议点名的是三类——版本控制钩子、代理与编辑器设置、MCP 配置。共同点是**它们不是
// 项目内容，而是「之后谁会跑起来」的开关**：往 .git/hooks 放一个脚本，下次 commit 就
// 按它的意思跑；改 .mcp.json，下次启动就多一个外部服务。
//
// 清单是代码常量：不进配置、界面上不可调。能调就等于把这条防线交给「当时想图省事的人」
// （与 D280 的硬名单同一口径）。

import "github.com/Tonyjh07/Aquarius/internal/domain/perm"

// executionControl 命中的规则名（空 = 不是执行控制类文件）。
// 规则表在 perm 包里：策略表的硬名单与这里的确认走同一份清单，两处各写一份迟早漂移
// （D280 第 6 条）。
func executionControl(path string) string { return perm.HardLabel(path) }
