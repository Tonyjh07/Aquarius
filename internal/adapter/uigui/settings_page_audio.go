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

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// audioPage 语音页模块。
type audioPage struct{}

// 语音协议位键值（与 config 里 audio.*.kind 的原文一致）。界面里只有本页按 kind 分支：
// **哪一档真正读哪些字段**由适配器决定，界面只照着摆，不问用户要已经不看的值。
const (
	asrKindWhisper = "whisper"
	asrKindFunASR  = "funasr"
	ttsKindVITS    = "vits"
	ttsKindGPTSo   = "gptsovits"
)

func (audioPage) Key() string { return "audio" }

func (audioPage) Rows(f *settingsForm) []rowDesc {
	asrKind := f.asrKind.value()
	ttsKind := f.ttsKind.value()
	rows := []rowDesc{sec(i18n.T("ui.page.audio.section.asr")), {kind: rowAudioASRKind}}
	// 每档只列它真正会读的字段：适配器不看的东西摆在这里，用户填了也不会生效，
	// 只会让人怀疑「是不是还要填点什么」。
	switch asrKind {
	case asrKindWhisper:
		// 本地引擎：只读模型文件；端点、密钥、服务、模型名一概不看。
		rows = append(rows, rowDesc{kind: rowAudioASRPath}, rowDesc{kind: rowAudioLang})
	case asrKindFunASR:
		// 模型由服务端启动参数决定；语种也没有可传的参数（见 asrfunasr）。
		rows = append(rows, rowDesc{kind: rowAudioASRProvider}, rowDesc{kind: rowAudioASREndpoint},
			rowDesc{kind: rowAudioASRKey})
	default:
		// 对话接口与 OpenAI 转写都用全套：服务、端点、密钥、模型名、语种。
		rows = append(rows, rowDesc{kind: rowAudioASRProvider}, rowDesc{kind: rowAudioASREndpoint},
			rowDesc{kind: rowAudioASRKey}, rowDesc{kind: rowAudioASRModel}, rowDesc{kind: rowAudioLang})
	}
	rows = append(rows, sec(i18n.T("ui.page.audio.section.tts")), rowDesc{kind: rowAudioTTSKind})
	switch ttsKind {
	case ttsKindVITS:
		// vits-simple-api：说话人 ID（音色）+ 风格标签，没有模型名、描述与风格指令。
		rows = append(rows, rowDesc{kind: rowAudioTTSProvider}, rowDesc{kind: rowAudioTTSEndpoint},
			rowDesc{kind: rowAudioTTSKey}, rowDesc{kind: rowAudioVoice}, rowDesc{kind: rowAudioTags})
	case ttsKindGPTSo:
		// GPT-SoVITS：音色 = 参考音频路径，描述 = 参考音频的文字；密钥由服务端
		// api_v2.py 决定（适配器不发鉴权头），风格指令没有对应参数。
		rows = append(rows, rowDesc{kind: rowAudioTTSProvider}, rowDesc{kind: rowAudioTTSEndpoint},
			rowDesc{kind: rowAudioVoice}, rowDesc{kind: rowAudioDesign}, rowDesc{kind: rowAudioTags})
	default:
		rows = append(rows, rowDesc{kind: rowAudioTTSProvider}, rowDesc{kind: rowAudioTTSEndpoint},
			rowDesc{kind: rowAudioTTSKey}, rowDesc{kind: rowAudioTTSModel}, rowDesc{kind: rowAudioVoice},
			rowDesc{kind: rowAudioDesign}, rowDesc{kind: rowAudioStyle}, rowDesc{kind: rowAudioTags})
	}
	return append(rows, rowDesc{kind: rowAudioPreview}, rowDesc{kind: rowAudioNote})
}

func (audioPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	switch rows[i].kind {
	case rowAudioASRKind:
		return fieldRow(gtx, th, i18n.T("ui.page.audio.asr_kind_label"), selectBtn(th, &f.asrKind)), true
	case rowAudioASRProvider:
		return f.audioServiceRow(gtx, th, &f.asrProv, &f.asrKeyUse), true
	case rowAudioASREndpoint:
		return fieldRow(gtx, th, i18n.T("ui.page.audio.endpoint_label"),
			editorBox(th, &f.asrBaseURL, i18n.T("ui.page.audio.endpoint_hint"))), true
	case rowAudioASRKey:
		return f.audioKeyRow(gtx, th, &f.asrKey, "asr"), true
	case rowAudioASRModel:
		return fieldRow(gtx, th, i18n.T("ui.page.audio.asr_model_label"),
			editorBox(th, &f.asrModel, i18n.T("ui.page.audio.asr_model_hint"))), true
	case rowAudioASRPath:
		return f.audioModelPathRow(gtx, th), true
	case rowAudioLang:
		return fieldRow(gtx, th, i18n.T("ui.page.audio.asr_lang_label"), selectBtn(th, &f.asrLang)), true
	case rowAudioTTSProvider:
		return f.audioServiceRow(gtx, th, &f.ttsProv, &f.ttsKeyUse), true
	case rowAudioTTSKind:
		return fieldRow(gtx, th, i18n.T("ui.page.audio.tts_kind_label"), selectBtn(th, &f.ttsKind)), true
	case rowAudioTTSEndpoint:
		return fieldRow(gtx, th, i18n.T("ui.page.audio.endpoint_label"),
			editorBox(th, &f.ttsBaseURL, i18n.T("ui.page.audio.endpoint_hint"))), true
	case rowAudioTTSKey:
		return f.audioKeyRow(gtx, th, &f.ttsKey, "tts"), true
	case rowAudioTTSModel:
		return fieldRow(gtx, th, i18n.T("ui.page.audio.tts_model_label"),
			editorBox(th, &f.ttsModel, i18n.T("ui.page.audio.tts_model_hint"))), true
	case rowAudioVoice:
		return fieldRow(gtx, th, i18n.T("ui.page.audio.voice_label"),
			editorBox(th, &f.ttsVoice, i18n.T("ui.page.audio.voice_hint"))), true
	case rowAudioDesign:
		return fieldRow(gtx, th, i18n.T("ui.page.audio.design_label"),
			editorBox(th, &f.ttsDesign, i18n.T("ui.page.audio.design_hint"))), true
	case rowAudioStyle:
		return fieldRow(gtx, th, i18n.T("ui.page.audio.style_label"),
			editorBox(th, &f.ttsStyle, i18n.T("ui.page.audio.style_hint"))), true
	case rowAudioTags:
		return fieldRow(gtx, th, i18n.T("ui.page.audio.tags_label"),
			editorBox(th, &f.ttsTags, i18n.T("ui.page.audio.tags_hint"))), true
	case rowAudioPreview:
		return f.audioPreviewRow(gtx, th), true
	case rowAudioNote:
		l := material.Body2(th, i18n.T("ui.page.audio.note"))
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	}
	return layout.Dimensions{}, false
}

// audioModelPathRow 本地识别模型文件路径一行 + 一句下载指引。
//
// 模型文件几十 MB 起，既不进仓库也不进二进制；用户自己下载后填绝对路径。
// 报错里也会带同样的指引（模型读不到时的那句话），这里提前说一遍。
func (f *settingsForm) audioModelPathRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, i18n.T("ui.page.audio.model_path_label"),
				editorBox(th, &f.asrPath, i18n.T("ui.page.audio.model_path_hint")))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), i18n.T("ui.page.audio.model_path_note"))
				l.Color = paperOf().text3
				return l.Layout(gtx)
			})
		}),
	)
}

// audioServiceRow 「服务」一行：从 model.providers 里选一个条目 + 「填入」按钮（D222）。
// 「未配置」= 不借任何条目（端点与密钥全靠自己填）。
func (f *settingsForm) audioServiceRow(gtx layout.Context, th *material.Theme, prov *selectField, use *widget.Clickable) layout.Dimensions {
	return fieldRow(gtx, th, i18n.T("ui.page.audio.service_label"), func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(selectBtn(th, prov)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, paperButton(th, use, i18n.T("ui.page.audio.fill"), paperBtnSecondary, true))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperNoteSp(th), i18n.T("ui.page.audio.fill_hint"))
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
			return fieldRow(gtx, th, i18n.T("ui.page.audio.key_label"),
				editorBox(th, ed, i18n.T("ui.page.audio.key_hint")))
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
	saved, plain, ref := false, false, ""
	asr := which == "asr"
	if asr {
		saved, plain, ref = f.snap.ASRKeySaved, f.snap.ASRKeyPlain, f.snap.ASRKeyRef
	} else {
		saved, plain, ref = f.snap.TTSKeySaved, f.snap.TTSKeyPlain, f.snap.TTSKeyRef
	}
	switch {
	case saved:
		if ref == "" {
			ref = "secret:…"
		}
		return i18n.Tf("ui.page.audio.keynote.saved", "ref", ref)
	case plain:
		return i18n.T("ui.page.audio.keynote.plain")
	case asr:
		return i18n.T("ui.page.audio.keynote.fallback_asr")
	default:
		return i18n.T("ui.page.audio.keynote.fallback_tts")
	}
}

// fillAudioFromService 「填入」：把所选服务的端点与密钥引用抄进该通道（D222）。
func (f *settingsForm) fillAudioFromService(prov *selectField, baseURL, key *widget.Editor) {
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
	f.openPicker(gtx, i18n.T("ui.page.audio.asr_kind_label"), &f.asrKind)
	f.openPicker(gtx, i18n.T("ui.page.audio.asr_service_picker"), &f.asrProv)
	f.openPicker(gtx, i18n.T("ui.page.audio.asr_lang_label"), &f.asrLang)
	f.openPicker(gtx, i18n.T("ui.page.audio.tts_kind_label"), &f.ttsKind)
	f.openPicker(gtx, i18n.T("ui.page.audio.tts_service_picker"), &f.ttsProv)
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
			f.voiceStatus = i18n.Tf("ui.page.audio.status.preview_failed", "err", err.Error())
		} else {
			f.voiceErr = false
			f.voiceStatus = i18n.T("ui.page.audio.status.playing")
		}
	default:
	}
}

func (audioPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.asrModel }

// audioPreviewRow 「试听」按钮 + 行内反馈（合成中禁用按钮，避免连点叠音）。
func (f *settingsForm) audioPreviewRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	label := i18n.T("ui.page.audio.preview_play")
	if f.voiceBusy {
		label = i18n.T("ui.page.audio.preview_busy")
	}
	return fieldRow(gtx, th, i18n.T("ui.page.audio.preview_label"), func(gtx layout.Context) layout.Dimensions {
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
		f.voiceStatus = i18n.T("ui.page.audio.status.unavailable")
		return
	}
	req := port.SpeechRequest{
		Text:   i18n.T("ui.page.audio.preview_text"),
		Voice:  strings.TrimSpace(f.ttsVoice.Text()),
		Design: strings.TrimSpace(f.ttsDesign.Text()),
		Style:  strings.TrimSpace(f.ttsStyle.Text()),
		Tags:   strings.TrimSpace(f.ttsTags.Text()),
	}
	f.voiceBusy = true
	f.voiceErr = false
	f.voiceStatus = i18n.T("ui.page.audio.status.synthesizing")
	win := f.win
	go func() {
		err := u.opts.PreviewVoice(req)
		f.voiceRes <- err
		if win != nil {
			win.Invalidate() // 唤醒设置窗事件循环取结果
		}
	}()
}
