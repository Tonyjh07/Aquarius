package main

// 确认 / 提问通道的装配（D185 口径二/六）：同一份前端交互面派生出两套端口——
// 一套给「用户亲手发起」的路径（`/rm`、`/rmconv`），一套给模型发起的工具链
// （`toolrun` 与 `ask_user`，只有它套无人值守装饰器）。插件授权不在这一层：
// 它由宿主自己判无人值守（见 `newPluginHost`）。
//
// 拆成独立函数是为了可测：装饰器装没装、两边各自指向谁，都不该埋在 run() 的
// 装配段里靠「跑一次真程序」验证。

import (
	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// toolChannels 两套通道：*Tool* 是模型链（可能被无人值守罩住），其余是用户面。
type toolChannels struct {
	// confirmer / asker 用户亲手输入的路径（命令二次确认）。
	confirmer port.Confirmer
	asker     port.Asker
	// toolConfirmer / toolAsker 模型发起的那条链（toolrun + ask_user）。
	toolConfirmer port.Confirmer
	toolAsker     port.Asker
}

// toolChannelsFor 由前端交互面造两条链：用户面原样透传前端，模型链套
// decorate.Unattended / UnattendedAsker，开关每次调用现取（热生效）。
//
// ui 为 nil（测试/无交互装配）时用户面留空：模型链在关闭态下 fail-closed 报错，
// 而不是静默放行。
func toolChannelsFor(ui uiFrontend, unattendedOn func() bool) toolChannels {
	var confirmer port.Confirmer
	var asker port.Asker
	if ui != nil {
		confirmer, asker = ui, ui
	}
	return toolChannels{
		confirmer:     confirmer,
		asker:         asker,
		toolConfirmer: decorate.Unattended(confirmer, unattendedOn),
		toolAsker:     decorate.UnattendedAsker(asker, unattendedOn),
	}
}
