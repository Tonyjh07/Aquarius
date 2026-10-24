package uigui

// settings_select_test.go 枚举选择器与「按协议位收起无关字段」的回归。
//
// 这两件事的判据都取**用户可见事实**：行计划里有没有这一行（隐藏的字段不该出现，
// 切到对应档才出现）、选择器的值是否真的回写到 patch——而不是断言某个内部字段。

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// audioRowSet 语音页当前行计划的行型集合（每型计数）。
func audioRowSet(f *settingsForm) map[rowKind]int {
	count := map[rowKind]int{}
	for _, r := range f.pageRows(pageAudio) {
		count[r.kind]++
	}
	return count
}

// TestAudioRowsFollowKind 语音页按协议位收字段（S4e）：每一档只列它真正会读的行。
//
// 为什么按适配器源码而不是 config 结构判：结构体里字段恒有，用不用由适配器决定。
// 把不看的值摆出来，用户会以为「这里空着所以没生效」——那是最难自查的一类困惑。
func TestAudioRowsFollowKind(t *testing.T) {
	cases := []struct {
		name    string
		asrKind string
		ttsKind string
		hidden  []rowKind // 本档不该出现的行
		shown   []rowKind // 本档必须出现的行
	}{
		{
			name: "本地 whisper", asrKind: asrKindWhisper,
			hidden: []rowKind{rowAudioASRProvider, rowAudioASREndpoint, rowAudioASRKey, rowAudioASRModel},
			shown:  []rowKind{rowAudioASRKind, rowAudioASRPath, rowAudioLang},
		},
		{
			name: "FunASR 原生", asrKind: asrKindFunASR,
			hidden: []rowKind{rowAudioASRModel, rowAudioASRPath, rowAudioLang},
			shown:  []rowKind{rowAudioASRProvider, rowAudioASREndpoint, rowAudioASRKey},
		},
		{
			name: "OpenAI 转写", asrKind: "openai",
			hidden: []rowKind{rowAudioASRPath},
			shown: []rowKind{rowAudioASRProvider, rowAudioASREndpoint, rowAudioASRKey,
				rowAudioASRModel, rowAudioLang},
		},
		{
			name: "VITS", ttsKind: ttsKindVITS,
			hidden: []rowKind{rowAudioTTSModel, rowAudioDesign, rowAudioStyle},
			shown:  []rowKind{rowAudioVoice, rowAudioTags, rowAudioTTSKey},
		},
		{
			name: "GPT-SoVITS", ttsKind: ttsKindGPTSo,
			hidden: []rowKind{rowAudioTTSModel, rowAudioStyle, rowAudioTTSKey},
			shown:  []rowKind{rowAudioVoice, rowAudioDesign, rowAudioTags},
		},
		{
			name: "缺省对话接口", asrKind: "chat-audio", ttsKind: "chat-audio",
			hidden: []rowKind{rowAudioASRPath},
			shown: []rowKind{rowAudioASRModel, rowAudioLang, rowAudioTTSModel,
				rowAudioDesign, rowAudioStyle, rowAudioTags, rowAudioTTSKey},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := audioSnapshot()
			if c.asrKind != "" {
				s.ASRKind = c.asrKind
			}
			if c.ttsKind != "" {
				s.TTSKind = c.ttsKind
			}
			u := newHeadless(t, Options{
				Settings:      func() SettingsSnapshot { return s },
				ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
				ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "openai"}}},
			})
			f := newSettingsForm(u)
			count := audioRowSet(f)
			for _, k := range c.hidden {
				if count[k] != 0 {
					t.Errorf("该档不该出现行型 %v（出现 %d 次）", k, count[k])
				}
			}
			for _, k := range c.shown {
				if count[k] != 1 {
					t.Errorf("该档应出现行型 %v（出现 %d 次）", k, count[k])
				}
			}
		})
	}
}

// TestAudioKindSwitchRevealsRows 切档后行计划立刻跟着变（每帧现算，不需要保存或重开）。
func TestAudioKindSwitchRevealsRows(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return audioSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "openai"}}},
	})
	f := newSettingsForm(u)
	if n := audioRowSet(f)[rowAudioASRModel]; n != 1 {
		t.Fatalf("缺省档应显示识别模型行，实为 %d", n)
	}
	f.asrKind.setValue(asrKindWhisper)
	if n := audioRowSet(f)[rowAudioASRModel]; n != 0 {
		t.Errorf("切到本地 whisper 后识别模型行应消失，实为 %d", n)
	}
	if n := audioRowSet(f)[rowAudioASRPath]; n != 1 {
		t.Errorf("切到本地 whisper 后模型文件行应出现，实为 %d", n)
	}
	// 切回去：字段跟着回来（隐藏只是不画，控件状态一直还在）。
	f.asrKind.setValue("chat-audio")
	if n := audioRowSet(f)[rowAudioASRModel]; n != 1 {
		t.Errorf("切回对话接口后识别模型行应恢复，实为 %d", n)
	}
}

// TestAudioHiddenFieldsStayInPatch 隐藏不等于丢弃：切到只看少数几档后保存，其它字段的
// 草稿值仍然随 patch 落盘——否则用户来回切一次档就把之前配好的 TTS 参数弄没了。
func TestAudioHiddenFieldsStayInPatch(t *testing.T) {
	var got SettingsPatch
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return audioSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { got = p; return nil, nil },
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "openai"}}},
	})
	f := newSettingsForm(u)
	f.asrKind.setValue(asrKindWhisper) // 识别模型行此刻不在页上
	u.saveSettings(f)
	if got.ASRModel != "mimo-v2.5-asr" {
		t.Errorf("隐藏字段的草稿值应照旧落盘，实为 %q", got.ASRModel)
	}
	if got.TTSStyle != "语速稍快" || got.TTSDesign != "温柔女声" {
		t.Errorf("合成侧草稿值应照旧落盘，实为 %+v", got)
	}
}

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
	if got := f.ttsKind.value(); got != ttsKindGPTSo {
		t.Fatalf("选中后 = %q, want %q", got, ttsKindGPTSo)
	}
	// 该档的行计划随之变化（风格指令行消失）。
	if n := audioRowSet(f)[rowAudioStyle]; n != 0 {
		t.Errorf("GPT-SoVITS 不该出现风格指令行，实为 %d", n)
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
