package main

// characteradmin.go 角色卡管理面装配根侧（§5.17）。
//
// 与技能安装面不同，这里不需要中间适配：characterfs.Store 的 Import / Remove 签名与
// app.CharacterAdmin 完全对齐，读侧的 List / Load 又满足 port.CharacterCatalog。同一份
// Store 同时接两个消费方接口——命令面与管理面只有一条导入路径。断言把这条约定钉在编译期。

import (
	"github.com/Tonyjh07/Aquarius/internal/adapter/characterfs"
	"github.com/Tonyjh07/Aquarius/internal/app"
)

var _ app.CharacterAdmin = (*characterfs.Store)(nil)
