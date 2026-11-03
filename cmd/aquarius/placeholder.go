package main

// 输入框占位文字的归一与上限（D279/§7.8）。
//
// 这一条与 `ui.theme` / `ui.lang` 不同：它的值是**用户内容**，进了界面但不属于文案表——
// 切界面语言它不翻译，也不该被 i18n 的任何一把锁管到（锁扫的是源码里的字面量）。
// 归一只做三件事：把换行/连续空白折成一个空格、去掉首尾空白、按
// `port.SettingsPlaceholderMax` 封顶。前两件是「灰字必须是一行」的硬要求——多行占位会把
// 输入胶囊撑变形；第三件是「提示不能长成正文」的粗界。
//
// 两条路径对「超长」的处理不同，这是有意的：
//   - **保存路径**（设置窗、任何写 config 的调用）拒写并回报错。存得进去的值必须过得了
//     下次启动的同一条校验，否则用户会看到「保存成功、重启后变了样」。
//   - **启动路径**截断后使用。手改过 config 的超长值不该让程序起不来——它不是配置错误，
//     只是把输入栏撑高了一点；截断保住了这两件事。

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// normalizePlaceholder 折成单行并去首尾空白（空串保持不变 = 「用内置文案」）。
func normalizePlaceholder(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// checkPlaceholder 归一后按上限判定；返回归一值与「是否超限」。
func checkPlaceholder(s string) (string, bool) {
	s = normalizePlaceholder(s)
	return s, utf8.RuneCountInString(s) > port.SettingsPlaceholderMax
}

// placeholderForStartup 启动路径：超长则截断取用（理由见文件头注释）。
func placeholderForStartup(s string) string {
	s, over := checkPlaceholder(s)
	if !over {
		return s
	}
	return string([]rune(s)[:port.SettingsPlaceholderMax])
}

// placeholderForWrite 保存路径：超长返回错误，调用方不落盘（`cmd.config.err.placeholder_max`）。
func placeholderForWrite(s string) (string, error) {
	s, over := checkPlaceholder(s)
	if over {
		return "", errors.New(i18n.Tf("cmd.config.err.placeholder_max", "max", port.SettingsPlaceholderMax))
	}
	return s, nil
}
