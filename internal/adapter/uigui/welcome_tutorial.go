package uigui

// 欢迎窗教程页的只读文案、菜单项与行计划（文案是契约，测试锁定）。

import (
	"strings"
)

// TutorialTips 教程页「基本操作」段（只读；文案契约测试锁定）。呼出键单独动态生成
// （见 tutorialRows——用户可能改过 `ui.hotkey`）。两个菜单行以「、」逐项列出真实项集，
// 有对照测试（菜单改了教程不改即测试失败）。
func TutorialTips() []string {
	return []string{
		"发送消息：Enter；换行：先点展开成多行，再 Shift+Enter",
		"停止生成：点右侧方块；复制选中文本：Ctrl+C",
		"输入 / 开补全浮层：Tab 换候选（会填进输入框）、Shift+Tab 往回退、Enter 发送；命令名后加空格看参数提示",
		"收起态单行输入时 ↑ ↓ 翻输入历史；展开成多行后方向键是移动光标",
		"Esc：关补全浮层 / 取消编辑 / 收起展开态",
		"消息右键：编辑、编辑并转移历史、编辑并复制历史、重新生成、复制、引用、查看原文",
		"球右键：新对话、权限、无人值守、消息历史、设置、置顶、隐藏、退出",
		"托盘右键：显示 / 隐藏输入窗、窗口置顶、无人值守、设置、会话历史、退出",
		"完整命令清单见 /help；接入参数与主题、快捷键都在「设置」窗（本引导可从设置窗重开）。",
	}
}

// TutorialCommands 教程页「常用命令」段（只读；每项 = {用例, 一句话说明}）。命令名以
// `internal/app` 的 commandCatalog 为单一事实源——但**装配前宿主没有内核**（不能从
// port.CommandCatalog 取），故此处静态复刻；`cmd/aquarius` 有对照测试防止与真实清单脱节。
func TutorialCommands() [][2]string {
	return [][2]string{
		{"/new [标题]", "新建会话"},
		{"/list", "列出会话"},
		{"/switch <id前缀>", "切换到既有会话"},
		{"/model [name]", "查看可用模型 / 切换并写回 config"},
		{"/permission [等级]", "查看 / 切换权限等级"},
		{"/edit <id> [--keep|--copy] <文本>", "修订某条消息（开新分支）"},
		{"/memory [会话id]", "用系统编辑器打开记忆文件"},
		{"/usage", "查看 token 用量"},
		{"/jobs", "后台任务管理"},
		{"/compact", "触发上下文压缩"},
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
	rows := []tutorialRow{{kind: tutHead, left: "基本操作"}}
	rows = append(rows, tutorialRow{kind: tutTip, left: "呼出 / 收起输入栏：" + hotkey + "（可在设置窗改 ui.hotkey）"})
	for _, t := range TutorialTips() {
		rows = append(rows, tutorialRow{kind: tutTip, left: t})
	}
	rows = append(rows, tutorialRow{kind: tutHead, left: "常用命令"})
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
