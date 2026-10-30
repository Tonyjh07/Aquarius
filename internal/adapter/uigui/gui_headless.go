package uigui

// 无窗口事件循环（headless 测试路径）与单行输入的三个辅助函数。

import (
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uicommon"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// runHeadless 无窗口事件循环（§15.5：桥接层 headless 测试；无帧渲染）。
func (u *UI) runHeadless() {
	defer close(u.done)
	for {
		select {
		case msg := <-u.inbox:
			if !u.apply(msg) {
				return
			}
		}
	}
}

// parseInput 单行输入解析（斜杠开头 = 命令；与 repl.Next 同口径，实现见 uicommon）。
func parseInput(line string) port.UserInput {
	return uicommon.ParseInput(line)
}

// quoteInto 把引用文本组为单行引用前缀追加到 existing（D98）："> " + 文本，文本内
// 换行压为空格——输入框 SingleLine（D49），多行引用无法成块（Gio SetText/Insert 在
// SingleLine 下均把 \n 替换为空格），沿 D92 后果②「所见即所发」口径；2b-4 展开态
// 多行编辑区落地后可升级为整块引用。existing 非空以空格衔接。
func quoteInto(existing, quoted string) string {
	line := "> " + strings.ReplaceAll(quoted, "\n", " ")
	if existing == "" {
		return line
	}
	return existing + " " + line
}

// isYes y/yes（大小写不敏感）为同意——与 repl.Confirm 语义一致。
func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true
	}
	return false
}
