package uigui

// settings_page_audio.go 「语音」页（D179 页面注册表）：识别服务 + 合成服务 + 音色与
// 风格 + 试听。本页是**字段页**：保存走全局底栏；服务/模型改动与「模型服务」页同口径
// （保存后重启生效），音色与风格对此后的播报立即生效，试听按当前草稿的音色/风格合成
// 一句（服务与模型仍来自启动装配）。

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// audioPage 语音页模块。
type audioPage struct{}

func (audioPage) Key() string { return "语音" }

func (audioPage) Rows(f *settingsForm) []rowDesc {
	return []rowDesc{
		sec("语音识别（说话转文字）"),
		{kind: rowAudioASRProvider},
		{kind: rowAudioASREndpoint},
		{kind: rowAudioASRKey},
		{kind: rowAudioASRModel},
		{kind: rowAudioLang},
		sec("语音合成（文字转语音）"),
		{kind: rowAudioTTSProvider},
		{kind: rowAudioTTSEndpoint},
		{kind: rowAudioTTSKey},
		{kind: rowAudioTTSModel},
		{kind: rowAudioVoice},
		{kind: rowAudioDesign},
		{kind: rowAudioStyle},
		{kind: rowAudioTags},
		{kind: rowAudioPreview},
		{kind: rowAudioNote},
	}
}

func (audioPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	switch rows[i].kind {
	case rowAudioASRProvider:
		return f.audioServiceRow(gtx, th, &f.asrProv, &f.asrKeyUse), true
	case rowAudioASREndpoint:
		return fieldRow(gtx, th, "端点", editorBox(th, &f.asrBaseURL, "如 https://api.example.com/v1；留空 = 用「服务」那条的")), true
	case rowAudioASRKey:
		return f.audioKeyRow(gtx, th, &f.asrKey, "asr"), true
	case rowAudioASRModel:
		return fieldRow(gtx, th, "识别模型", editorBox(th, &f.asrModel, "如 mimo-v2.5-asr")), true
	case rowAudioLang:
		return fieldRow(gtx, th, "录音语种", cycleBtn(th, &f.asrLang)), true
	case rowAudioTTSProvider:
		return f.audioServiceRow(gtx, th, &f.ttsProv, &f.ttsKeyUse), true
	case rowAudioTTSEndpoint:
		return fieldRow(gtx, th, "端点", editorBox(th, &f.ttsBaseURL, "如 https://api.example.com/v1；留空 = 用「服务」那条的")), true
	case rowAudioTTSKey:
		return f.audioKeyRow(gtx, th, &f.ttsKey, "tts"), true
	case rowAudioTTSModel:
		return fieldRow(gtx, th, "合成模型", editorBox(th, &f.ttsModel, "如 mimo-v2.5-tts / -voicedesign / -voiceclone")), true
	case rowAudioVoice:
		return fieldRow(gtx, th, "音色", editorBox(th, &f.ttsVoice, "预置音色名；克隆模型填样本文件绝对路径")), true
	case rowAudioDesign:
		return fieldRow(gtx, th, "音色描述", editorBox(th, &f.ttsDesign, "音色设计模型必填（如：温柔的女声）")), true
	case rowAudioStyle:
		return fieldRow(gtx, th, "风格指令", editorBox(th, &f.ttsStyle, "自然语言（如：语速稍快，语气轻松）")), true
	case rowAudioTags:
		return fieldRow(gtx, th, "风格标签", editorBox(th, &f.ttsTags, "如：温柔 语速稍快（会拼在正文前）")), true
	case rowAudioPreview:
		return f.audioPreviewRow(gtx, th), true
	case rowAudioNote:
		l := material.Body2(th, "识别与合成各自是一份完整配置：端点与密钥可以自己填，"+
			"也可以选一个「服务」再点「填入」——那会把该条目的端点与密钥引用抄过来，之后照样能改。"+
			"密钥只写不回显（留空 = 保持）。改服务、端点或模型需保存并重启。"+
			"音色、音色描述与风格保存后对此后的播报生效；试听按当前填写的内容合成一句。")
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	}
	return layout.Dimensions{}, false
}

// audioServiceRow 「服务」一行：循环选一个 model.providers 条目 + 「填入」按钮（D222）。
// 「未配置」= 不借任何条目（端点与密钥全靠自己填）。
func (f *settingsForm) audioServiceRow(gtx layout.Context, th *material.Theme, prov *cycleField, use *widget.Clickable) layout.Dimensions {
	return fieldRow(gtx, th, "服务", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(cycleBtn(th, prov)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, paperButton(th, use, "填入", paperBtnSecondary, true))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperNoteSp(th), "抄它的端点与密钥引用进本页，之后可改")
					l.Color = paperOf().text3
					return l.Layout(gtx)
				})
			}),
		)
	})
}

// audioKeyRow 密钥一行（只写不回显）+ 形态说明。
func (f *settingsForm) audioKeyRow(gtx layout.Context, th *material.Theme, ed *widget.Editor, which string) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "密钥", editorBox(th, ed, "secret:名 或明文；留空 = 保持 / 借用"))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), f.audioKeyNote(which))
				l.Color = paperOf().text3
				return l.Layout(gtx)
			})
		}),
	)
}

// audioKeyNote 该通道密钥框的形态说明（值永不回显）。
func (f *settingsForm) audioKeyNote(which string) string {
	saved, plain, ref, fallback := false, false, "", ""
	if which == "asr" {
		saved, plain, ref = f.snap.ASRKeySaved, f.snap.ASRKeyPlain, f.snap.ASRKeyRef
		fallback = "AQUARIUS_ASR_API_KEY"
	} else {
		saved, plain, ref = f.snap.TTSKeySaved, f.snap.TTSKeyPlain, f.snap.TTSKeyRef
		fallback = "AQUARIUS_TTS_API_KEY"
	}
	switch {
	case saved:
		if ref == "" {
			ref = "secret:…"
		}
		return "已存引用 " + ref + "；留空 = 保持不变"
	case plain:
		return "当前是明文（启动会警告）；填新值覆盖，留空 = 保持不变"
	default:
		return "留空 = 借用所选服务的密钥；未选服务则回落 secret:" + fallback
	}
}

// fillAudioFromService 「填入」：把所选服务的端点与密钥引用抄进该通道（D222）。
func (f *settingsForm) fillAudioFromService(prov *cycleField, baseURL, key *widget.Editor) {
	name := prov.value()
	if name == "" {
		return
	}
	for _, c := range f.chainChoices {
		if c.Name != name {
			continue
		}
		if c.BaseURL != "" {
			baseURL.SetText(c.BaseURL)
		}
		if c.KeyRef != "" {
			key.SetText(c.KeyRef)
		}
		return
	}
}

func (audioPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	f.asrProv.advance(gtx)
	f.ttsProv.advance(gtx)
	f.asrLang.advance(gtx)
	for f.asrKeyUse.Clicked(gtx) {
		f.fillAudioFromService(&f.asrProv, &f.asrBaseURL, &f.asrKey)
	}
	for f.ttsKeyUse.Clicked(gtx) {
		f.fillAudioFromService(&f.ttsProv, &f.ttsBaseURL, &f.ttsKey)
	}
	for f.voiceTry.Clicked(gtx) {
		u.previewVoice(f)
	}
	// 试听结果回灌（后台合成 → 本页状态行）。
	select {
	case err := <-f.voiceRes:
		f.voiceBusy = false
		if err != nil {
			f.voiceErr = true
			f.voiceStatus = "试听失败：" + err.Error()
		} else {
			f.voiceErr = false
			f.voiceStatus = "已开始播放"
		}
	default:
	}
}

func (audioPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.asrModel }

// audioPreviewRow 「试听」按钮 + 行内反馈（合成中禁用按钮，避免连点叠音）。
func (f *settingsForm) audioPreviewRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	label := "播放示例"
	if f.voiceBusy {
		label = "合成中…"
	}
	return fieldRow(gtx, th, "试听", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return paperButton(th, &f.voiceTry, label, paperBtnSecondary, !f.voiceBusy)(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if f.voiceStatus == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, f.voiceStatus)
					l.Color = paperOf().text3
					if f.voiceErr {
						l.Color = paperOf().danger
					}
					return l.Layout(gtx)
				})
			}),
		)
	})
}

// previewVoice 试听（D179）：草稿里的音色/描述/风格立刻参与合成；服务与模型来自启动
// 装配（保存并重启后生效，与「模型服务」页同口径）。合成在后台跑，结果投 voiceRes。
func (u *UI) previewVoice(f *settingsForm) {
	if f.voiceBusy {
		return
	}
	if u.opts.PreviewVoice == nil {
		f.voiceErr = true
		f.voiceStatus = "试听不可用：语音合成未配置"
		return
	}
	req := port.SpeechRequest{
		Text:   "你好，我是 Aquarius。这段声音听起来怎么样？",
		Voice:  strings.TrimSpace(f.ttsVoice.Text()),
		Design: strings.TrimSpace(f.ttsDesign.Text()),
		Style:  strings.TrimSpace(f.ttsStyle.Text()),
		Tags:   strings.TrimSpace(f.ttsTags.Text()),
	}
	f.voiceBusy = true
	f.voiceErr = false
	f.voiceStatus = "正在合成…"
	win := f.win
	go func() {
		err := u.opts.PreviewVoice(req)
		f.voiceRes <- err
		if win != nil {
			win.Invalidate() // 唤醒设置窗事件循环取结果
		}
	}()
}
