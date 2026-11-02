package uicommon

// keys.go 输入栏三键（§5.19 的界面侧）：发送 / 引导 / 换行。
//
// 三键只是**一条绑定表**，键盘事件在哪个框架里长什么样由各前端自己翻（gui 用 Gio 的
// key.Filter，tui 用 bubbletea 的按键字符串）。这里只做两件事：把 `ui.keys.*` 的写法
// 解析成一个规范化结构、给四个修饰键与可用的键名划清边界——非法值在启动时就报错，
// 而不是等用户按下那个键才发现没反应。

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// Mods 修饰键位集（可组合）。
type Mods uint8

const (
	ModCtrl Mods = 1 << iota
	ModShift
	ModAlt
	ModSuper
)

// Binding 一条按键绑定：修饰键 + 规范化键名（Enter/Tab/Escape/Space/Backspace 或单个
// 大写字符）。
type Binding struct {
	Mods Mods
	Key  string
}

// String 还原成配置里的写法（回执、错误信息与默认值展示用）。
func (b Binding) String() string {
	var mods []string
	if b.Mods&ModCtrl != 0 {
		mods = append(mods, "Ctrl")
	}
	if b.Mods&ModShift != 0 {
		mods = append(mods, "Shift")
	}
	if b.Mods&ModAlt != 0 {
		mods = append(mods, "Alt")
	}
	if b.Mods&ModSuper != 0 {
		mods = append(mods, "Super")
	}
	return strings.Join(append(mods, b.Key), "+")
}

// Has 是否带某个修饰键位。
func (b Binding) Has(m Mods) bool { return b.Mods&m != 0 }

// keyNameAliases 键名别名 → 规范名（大小写不敏感；单字符键另走字符分支）。
var keyNameAliases = map[string]string{
	"enter":     "Enter",
	"return":    "Enter",
	"tab":       "Tab",
	"escape":    "Escape",
	"esc":       "Escape",
	"space":     "Space",
	"backspace": "Backspace",
}

// ParseBinding 解析 `修饰键+键` 写法（与 ui.hotkey 同款）。空串 = 错误（缺省值由装配根
// 填，这里不猜）。
func ParseBinding(s string) (Binding, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Binding{}, errors.New(i18n.T("keys.err.empty"))
	}
	parts := strings.Split(raw, "+")
	var b Binding
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return Binding{}, errors.New(i18n.Tf("keys.err.empty_segment", "v", strconv.Quote(raw)))
		}
		if i < len(parts)-1 { // 修饰键段
			switch strings.ToLower(p) {
			case "ctrl", "control":
				b.Mods |= ModCtrl
			case "shift":
				b.Mods |= ModShift
			case "alt":
				b.Mods |= ModAlt
			case "super", "meta", "cmd", "win":
				b.Mods |= ModSuper
			default:
				return Binding{}, errors.New(i18n.Tf("keys.err.bad_modifier",
					"v", strconv.Quote(raw), "p", strconv.Quote(p)))
			}
			continue
		}
		if name, ok := keyNameAliases[strings.ToLower(p)]; ok {
			b.Key = name
			continue
		}
		if len([]rune(p)) == 1 && !strings.ContainsAny(p, "+") {
			b.Key = strings.ToUpper(p)
			continue
		}
		return Binding{}, errors.New(i18n.Tf("keys.err.bad_key",
			"v", strconv.Quote(raw), "p", strconv.Quote(p)))
	}
	if b.Key == "" {
		return Binding{}, errors.New(i18n.Tf("keys.err.only_modifiers", "v", strconv.Quote(raw)))
	}
	return b, nil
}

// KeyBindings 输入栏三键（gui 与 tui 共用一份；装配根从 `ui.keys` 解析后注入）。
type KeyBindings struct {
	Send    Binding // 发送：空闲起一轮；生成中按排队进清单
	Steer   Binding // 引导：只在生成中生效（空闲时与 Send 等价）
	Newline Binding // 换行：插入 \n 不发送
}

// DefaultKeyBindings 缺省三键（Enter / Shift+Enter / Ctrl+Enter）。
func DefaultKeyBindings() KeyBindings {
	return KeyBindings{
		Send:    Binding{Key: "Enter"},
		Steer:   Binding{Mods: ModShift, Key: "Enter"},
		Newline: Binding{Mods: ModCtrl, Key: "Enter"},
	}
}

// Validate 三键必须互不相同——同一个组合绑两个语义，谁先命中就看实现顺序，那是 bug。
func (k KeyBindings) Validate() error {
	seen := map[string]string{"send": "", "steer": "", "newline": ""}
	for name, b := range map[string]Binding{"send": k.Send, "steer": k.Steer, "newline": k.Newline} {
		if b.Key == "" {
			return errors.New(i18n.Tf("keys.err.missing", "name", name))
		}
		s := b.String()
		if other := seen[s]; other != "" {
			return errors.New(i18n.Tf("keys.err.collision",
				"name", name, "other", other, "binding", s))
		}
		seen[s] = name
	}
	return nil
}

// ParseKeyBindings 从三个配置值解析整张表；空值取缺省。
func ParseKeyBindings(send, steer, newline string) (KeyBindings, error) {
	k := DefaultKeyBindings()
	for _, f := range []struct {
		name string
		val  string
		dst  *Binding
	}{
		{"send", send, &k.Send},
		{"steer", steer, &k.Steer},
		{"newline", newline, &k.Newline},
	} {
		if strings.TrimSpace(f.val) == "" {
			continue
		}
		b, err := ParseBinding(f.val)
		if err != nil {
			return KeyBindings{}, fmt.Errorf("ui.keys.%s: %w", f.name, err)
		}
		*f.dst = b
	}
	if err := k.Validate(); err != nil {
		return KeyBindings{}, err
	}
	return k, nil
}
