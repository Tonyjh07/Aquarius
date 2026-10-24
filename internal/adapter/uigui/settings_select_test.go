package uigui

// settings_select_test.go 枚举选择器的回归。
//
// 判据取**用户可见事实**：按钮上显示的是不是当前项、点一项之后值有没有真的回写
// ——而不是断言某个内部字段。

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestSelectFieldChoose 选择器的取值语义：按键值定位、越界夹取、选中即改值。
func TestSelectFieldChoose(t *testing.T) {
	c := selectField{
		displays: []string{"自动识别", "中文", "英文"},
		values:   []string{"auto", "zh", "en"},
	}
	if got := c.value(); got != "auto" {
		t.Fatalf("缺省值 = %q, want auto", got)
	}
	c.setValue("en")
	if got := c.value(); got != "en" || got != c.values[c.idx] {
		t.Fatalf("按值定位后 = %q", got)
	}
	c.setValue("没有这一档") // 未命中：保持不动，不越界
	if got := c.value(); got != "en" {
		t.Fatalf("未命中该保持原值，实为 %q", got)
	}
	c.choose(1)
	if got := c.value(); got != "zh" {
		t.Fatalf("选中第二项后 = %q", got)
	}
	c.choose(99) // 越界 no-op
	if got := c.value(); got != "zh" {
		t.Fatalf("越界选择应为 no-op，实为 %q", got)
	}
	// 展示名按下标一一对应。
	if got := c.display(); got != "中文" {
		t.Fatalf("展示名 = %q", got)
	}
}

// pickerGtx 选择器单帧用的布局上下文（离屏：只要 Ops 与约束即可）。
func pickerGtx(w, h int) layout.Context {
	var ops op.Ops
	return layout.Context{
		Ops:         &ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Constraints{Max: image.Pt(w, h)},
	}
}

// TestPickerOpenAndCancel 弹层开合：点按钮打开、「取消」收起、不改选项。
func TestPickerOpenAndCancel(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return audioSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "openai"}}},
	})
	f := newSettingsForm(u)
	th := testTheme()
	if f.pickerOptions() != nil {
		t.Fatal("未点按钮时不该有弹层")
	}
	// Clickable.Click 把点击登记为「下一帧生效」，之后 Clicked 即为真。
	f.asrLang.click.Click()
	f.openPicker(pickerGtx(720, 560), "录音语种", &f.asrLang)
	if opts := f.pickerOptions(); len(opts) != len(langLevels) {
		t.Fatalf("弹层选项 = %v, want %v", opts, langLevels)
	}
	before := f.asrLang.value()
	// 画一帧把选项按钮就位，再点「取消」，下一帧应收起。
	f.pickerFrame(pickerGtx(720, 560), th)
	f.pick.cancel.Click()
	f.pickerFrame(pickerGtx(720, 560), th)
	if f.pickerOptions() != nil {
		t.Fatal("取消后弹层应收起")
	}
	if f.asrLang.value() != before {
		t.Fatalf("取消不该改选项：%q → %q", before, f.asrLang.value())
	}
}

// TestPickerChooseWritesValue 弹层里点一项即回写该选择器的值（并收起）。
func TestPickerChooseWritesValue(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return audioSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "openai"}}},
	})
	f := newSettingsForm(u)
	th := testTheme()
	f.ttsKind.click.Click()
	f.openPicker(pickerGtx(720, 560), "合成方式", &f.ttsKind)
	if f.pickerOptions() == nil {
		t.Fatal("弹层没打开")
	}
	// 先画一帧让选项按钮就位，再点第 3 项（GPT-SoVITS）。
	f.pickerFrame(pickerGtx(720, 560), th)
	f.pick.buttons[2].Click()
	f.pickerFrame(pickerGtx(720, 560), th)
	if f.pickerOptions() != nil {
		t.Fatal("选中后弹层应收起")
	}
	if got := f.ttsKind.value(); got != "gptsovits" {
		t.Fatalf("选中后 = %q, want gptsovits", got)
	}
}

// TestPickerOnlyOneField 弹层是全窗唯一模态：后打开的选择器接管，前一个不受影响。
func TestPickerOnlyOneField(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return audioSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "openai"}}},
	})
	f := newSettingsForm(u)
	f.asrLang.click.Click()
	f.openPicker(pickerGtx(720, 560), "录音语种", &f.asrLang)
	f.ttsKind.click.Click()
	f.openPicker(pickerGtx(720, 560), "合成方式", &f.ttsKind)
	if got := f.pick.title; got != "合成方式" {
		t.Fatalf("弹层应被后者接管，标题 = %q", got)
	}
	if opts := f.pickerOptions(); len(opts) != len(ttsKindLevels) {
		t.Fatalf("选项应换成合成档，实为 %v", opts)
	}
}

// TestSelectBtnShowsCurrent "▾" 指示存在且展示当前项（读起来像可选值而不是动作按钮）。
func TestSelectBtnShowsCurrent(t *testing.T) {
	c := selectField{
		displays: []string{"自动识别", "中文", "英文"},
		values:   []string{"auto", "zh", "en"},
	}
	c.setValue("zh")
	if got := c.display(); got != "中文" {
		t.Fatalf("展示名 = %q", got)
	}
}
