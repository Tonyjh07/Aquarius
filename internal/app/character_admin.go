package app

// character_admin.go /character 的导入与删除面（§5.17）。
//
// 导入 / 删除是**用户面动作**——模型侧没有角色卡工具：让模型能装卡等于把注入面交给
// 卡文件内容。命令面与管理面共用装配根实现的同一份导入器，接口由消费方定义（范式同
// SkillAdmin）；port.CharacterCatalog 保持只读两方法。

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// characterAdminUsage 管理面用法（用户面；子命令错误统一提示）。
const characterAdminUsage = "用法: /character list | /character show <角色> | /character import <卡文件> | /character start <角色> [开场白序号] | /character remove <角色>"

// CharacterAdmin 角色卡管理面（装配根实现；线程安全，命令面与设置窗共用）。
type CharacterAdmin interface {
	// Import 导入一个卡文件（PNG / JSON / .charx），返回解析后的卡（回执据此排版）。
	Import(ctx context.Context, source string) (port.CharacterCard, error)
	// Remove 删除一个已导入的角色目录；未命中返回包装了 ErrCharacterNotFound 的错误。
	Remove(ctx context.Context, slug string) error
}
