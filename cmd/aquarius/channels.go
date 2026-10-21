package main

// 确认 / 提问通道的装配（D185 口径二/三/六）：同一份前端交互面派生出两套端口——
// 一套给「用户亲手发起」的路径（`/rm`、`/rmconv`、插件能力授权），一套给模型发起的
// 工具链（`toolrun` 与 `ask_user`，只有它套无人值守装饰器）。
//
// 拆成独立函数是为了可测：`-yes` 的优先级、装饰器装没装、两边各自指向谁，都不该
// 埋在 run() 的装配段里靠「跑一次真程序」验证。

import (
	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// toolChannels 两套通道：*Tool* 是模型链（可能被无人值守罩住），其余是用户面。
type toolChannels struct {
	// confirmer / asker 用户亲手输入的路径（命令二次确认、插件授权；-yes 时同一套）。
	confirmer port.Confirmer
	asker     port.Asker
	// toolConfirmer / toolAsker 模型发起的那条链（toolrun + ask_user）。
	toolConfirmer port.Confirmer
	toolAsker     port.Asker
}

// toolChannelsFor 由前端交互面造两条链：
//   - autoYes：两条链都换成「自动允许 / 用户不在」，且**不装**无人值守装饰器——命令行
//     显式要求放行优先于配置里的自动拒绝（口径三）。
//   - 否则：用户面原样透传前端，模型链套 decorate.Unattended / UnattendedAsker，
//     开关每次调用现取（热生效）。
//
// ui 为 nil（测试/无交互装配）时用户面留空：模型链在关闭态下 fail-closed 报错，
// 而不是静默放行。
func toolChannelsFor(ui uiFrontend, autoYes bool, unattendedOn func() bool) toolChannels {
	if autoYes {
		return toolChannels{
			confirmer:     yesConfirmer{},
			asker:         askNone{},
			toolConfirmer: yesConfirmer{},
			toolAsker:     askNone{},
		}
	}
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
