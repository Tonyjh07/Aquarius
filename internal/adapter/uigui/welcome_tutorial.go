package uigui

// 欢迎窗教程页的只读文案、菜单项与行计划（文案是契约，测试锁定）。

import (
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// TutorialTips 教程页「基本操作」段（只读；文案契约测试锁定）。呼出键单独动态生成
// （见 tutorialRows——用户可能改过 `ui.hotkey`）。两个菜单行以「、」逐项列出真实项集，
// 有对照测试（菜单改了教程不改即测试失败）。
func TutorialTips() []string {
	return []string{
		i18n.T("ui.welcome.tut.tip.enter"),
		i18n.T("ui.welcome.tut.tip.stop"),
		i18n.T("ui.welcome.tut.tip.completion"),
		i18n.T("ui.welcome.tut.tip.history"),
		i18n.T("ui.welcome.tut.tip.esc"),
		i18n.T("ui.welcome.tut.tip.bubble_menu"),
		i18n.T("ui.welcome.tut.tip.ball_menu"),
		i18n.T("ui.welcome.tut.tip.tray_menu"),
		i18n.T("ui.welcome.tut.tip.help"),
	}
}

// TutorialCommands 教程页「常用命令」段（只读；每项 = {用例, 一句话说明}）。命令名以
// `internal/app` 的 commandCatalog 为单一事实源——但**装配前宿主没有内核**（不能从
// port.CommandCatalog 取），故此处静态复刻；`cmd/aquarius` 有对照测试防止与真实清单脱节。
func TutorialCommands() [][2]string {
	return [][2]string{
		{"/new " + i18n.T("ui.welcome.usage.title"), i18n.T("ui.welcome.cmd.new")},
		{"/list", i18n.T("ui.welcome.cmd.list")},
		{"/switch " + i18n.T("ui.welcome.usage.id_prefix"), i18n.T("ui.welcome.cmd.switch")},
		{"/model [name]", i18n.T("ui.welcome.cmd.model")},
		{"/permission " + i18n.T("ui.welcome.usage.level"), i18n.T("ui.welcome.cmd.permission")},
		{"/edit " + i18n.T("ui.welcome.usage.edit"), i18n.T("ui.welcome.cmd.edit")},
		{"/memory " + i18n.T("ui.welcome.usage.memory"), i18n.T("ui.welcome.cmd.memory")},
		{"/usage", i18n.T("ui.welcome.cmd.usage")},
		{"/jobs", i18n.T("ui.welcome.cmd.jobs")},
		{"/compact", i18n.T("ui.welcome.cmd.compact")},
	}
}

const (
	tutHead = iota // 分组标题
	tutTip         // 提示行
	tutCmd         // 命令行（左 = 用例，右 = 说明）
)

// tutorialRow 教程页一行。
type tutorialRow struct {
	kind        int
	left, right string
}

// tutorialRows 教程页行计划（每帧现算；hotkey = 生效的呼出快捷键——用户可能改过
// `ui.hotkey`，空 = 默认 Alt+A，§15.1）。
func tutorialRows(hotkey string) []tutorialRow {
	rows := []tutorialRow{{kind: tutHead, left: i18n.T("ui.welcome.tut.head_basics")}}
	rows = append(rows, tutorialRow{kind: tutTip, left: i18n.Tf("ui.welcome.tut.hotkey", "hotkey", hotkey)})
	for _, t := range TutorialTips() {
		rows = append(rows, tutorialRow{kind: tutTip, left: t})
	}
	rows = append(rows, tutorialRow{kind: tutHead, left: i18n.T("ui.welcome.tut.head_commands")})
	for _, c := range TutorialCommands() {
		rows = append(rows, tutorialRow{kind: tutCmd, left: c[0], right: c[1]})
	}
	return rows
}

// hotkeyLabel 教程页展示的呼出快捷键（快照空 = 默认 Alt+A）。
func (f *welcomeForm) hotkeyLabel() string {
	if h := strings.TrimSpace(f.env.snap.Hotkey); h != "" {
		return h
	}
	return "Alt+A"
}
