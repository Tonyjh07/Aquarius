package uitui

// pending.go TUI 侧的待发清单（D272）：生成中键入的 Enter 不再「等这一轮结束」，
// 而是进清单，由内核在下一个请求边界或轮末取走。
//
// 终端与 GUI 的口径差：这里没有「输入框 / 待发条 / 引导」三个可视位置的来回搬（一条
// 行式输入框搬不动），所以只保留一件事——**键入的行按档位进清单**，状态行报出还有
// 几条在等，Ctrl+U 在空输入时撤销最后一条。要看内容就 `/pending`？不做：那一行状态
// 已经够回答「我是不是还有话没说出来」，列全文会把屏幕吃掉。

import (
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uicommon"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// defaultTUIKeys 终端的缺省三键：Enter 发送、Alt+Enter 引导、Ctrl+J 换行（管道行终止）。
// Shift+Enter 在多数终端里与 Enter 不可分，所以引导的缺省换成 Alt+Enter——配置里写
// Shift+Enter 的用户在 TUI 里仍按 Alt+Enter 唤起同一条语义（TUI 的按键面窄，这是代价）。
func defaultTUIKeys() uicommon.KeyBindings {
	k := uicommon.DefaultKeyBindings()
	k.Steer = uicommon.Binding{Mods: uicommon.ModAlt, Key: "Enter"}
	return k
}

// pending 取当前清单（nil = 本构建没接清单）。
func (u *UI) pending() port.Pending {
	if u.opts.Pending == nil {
		return nil
	}
	return u.opts.Pending()
}

// pendingCount 当前待发条数（状态行显示用）。
func (u *UI) pendingCount() int {
	p := u.pending()
	if p == nil {
		return 0
	}
	return len(p.List())
}

// addPending 把一行按档位放进清单；收不下就返回一句提示（不静默丢弃）。
func (u *UI) addPending(text string, d port.Delivery) (bool, string) {
	p := u.pending()
	if p == nil {
		return false, ""
	}
	if _, err := p.Add(text, d); err != nil {
		return false, fmt.Sprintf("[notice] %v", err)
	}
	return true, ""
}

// undoPending 撤销最后一条待发（Ctrl+U，输入为空时）：返回被撤销的正文与是否命中。
func (u *UI) undoPending() (string, bool) {
	p := u.pending()
	if p == nil {
		return "", false
	}
	items := p.List()
	if len(items) == 0 {
		return "", false
	}
	last := items[len(items)-1]
	p.Remove(last.ID)
	return last.Text, true
}
