package uigui

// D279/§7.8：输入框占位文字的三态。
//
// 这一条与 `ui.lang` 的对比是这组测试的重点：语言要重启才换（文案在构造期散进了各视图），
// 而占位文字**每帧现取**——设置窗保存完，下一帧输入栏就该是新的一句。所以这里断言的是
// 「换槽之后立刻读得到新值」，不涉及任何重建。

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestPlaceholderThreeStates 内置（随语言）→ 自定义（两处共用）→ 清空回落。
func TestPlaceholderThreeStates(t *testing.T) {
	u := newHeadless(t, Options{})

	// ① 内置：没自定义时取表里的两条，各自不同（主输入框一句、拒绝原因框一句）。
	builtinInput := i18n.T("ui.input.placeholder")
	if got := u.inputPlaceholder(); got != builtinInput {
		t.Fatalf("内置占位 = %q, want %q", got, builtinInput)
	}
	if u.reasonPlaceholder() != i18n.T("ui.confirm.reason_placeholder") {
		t.Errorf("拒绝原因框内置占位 = %q", u.reasonPlaceholder())
	}

	// ② 自定义：一个字面量同时作用于两处——用户改的是「输入框里那行灰字」，不是某一个框。
	u.SetPlaceholder("随手写点什么")
	if got := u.inputPlaceholder(); got != "随手写点什么" {
		t.Fatalf("自定义占位 = %q", got)
	}
	if got := u.reasonPlaceholder(); got != "随手写点什么" {
		t.Errorf("拒绝原因框应共用同一份自定义值，got %q", got)
	}

	// ③ 清空：回到内置文案（不是空灰字）。只有空白也按清空算，否则用户敲了个空格就再也
	// 看不到提示，且看不出是自己弄的。
	for _, blank := range []string{"", "   ", "\t\n"} {
		u.SetPlaceholder("随手写点什么")
		u.SetPlaceholder(blank)
		if got := u.inputPlaceholder(); got != builtinInput {
			t.Errorf("清空（%q）后占位 = %q, want 内置 %q", blank, got, builtinInput)
		}
	}
}

// TestPlaceholderFollowsLang 内置文案随界面语言变，自定义值不随之变（D279：用户内容不翻译）。
func TestPlaceholderFollowsLang(t *testing.T) {
	u := newHeadless(t, Options{})
	defer func() { i18n.Use(i18n.SelectionZh) }()

	// 从 zh 起算：进程级语言可能已被 `AQUARIUS_UI_LANG=en` 的全量冒烟置成 en，
	// 那样下面的「切到 en 后变没变」前后同值，测的就不是这件事了。
	i18n.Use(i18n.SelectionZh)
	zhInput := u.inputPlaceholder()
	i18n.Use(i18n.SelectionEn)
	enInput := u.inputPlaceholder()
	if enInput == zhInput {
		t.Fatalf("切到 en 后内置占位没变：%q", enInput)
	}
	// 自定义值在 en 下原样保留——它没有译文，也不该被翻译。
	u.SetPlaceholder("随手写点什么")
	i18n.Use(i18n.SelectionEn)
	if got := u.inputPlaceholder(); got != "随手写点什么" {
		t.Errorf("en 下自定义占位 = %q, want 原样", got)
	}
	// 内置那一侧是真英文（表里 en 侧的值），不是中文残留。
	i18n.Use(i18n.SelectionEn)
	u.SetPlaceholder("")
	if strings.ContainsFunc(u.inputPlaceholder(), func(r rune) bool { return r > 0x2E80 }) {
		t.Errorf("en 下内置占位仍是中文：%q", u.inputPlaceholder())
	}
}

// TestSettingsFormPlaceholder 设置窗那一行的两头：开窗从快照装载、保存时采集并就地拦下超长。
func TestSettingsFormPlaceholder(t *testing.T) {
	u := newHeadless(t, Options{Settings: func() SettingsSnapshot {
		return SettingsSnapshot{Placeholder: "先看 AGENTS.md"}
	}})
	f := newSettingsForm(u)
	if got := f.placeholder.Text(); got != "先看 AGENTS.md" {
		t.Fatalf("表单装载 = %q", got)
	}
	p, err := f.patch()
	if err != nil {
		t.Fatalf("采集 patch: %v", err)
	}
	if p.Placeholder != "先看 AGENTS.md" {
		t.Fatalf("patch.Placeholder = %q", p.Placeholder)
	}
	// 超长在采集阶段就拦（不触达写回调），报错里带上实际长度与上限。
	f.placeholder.SetText(strings.Repeat("x", port.SettingsPlaceholderMax+1))
	if _, err := f.patch(); err == nil {
		t.Fatal("超长占位应在采集阶段被拦下")
	}
}
