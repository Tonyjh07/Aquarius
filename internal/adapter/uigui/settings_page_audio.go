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

// 语音协议位键值（与 config 里 audio.*.kind 的原文一致）。界面里只有本页按 kind 分支：
// **哪一档真正读哪些字段**由适配器决定，界面只照着摆，不问用户要已经不看的值。
const (
	asrKindWhisper = "whisper"
	asrKindFunASR  = "funasr"
	ttsKindVITS    = "vits"
	ttsKindGPTSo   = "gptsovits"
)

func (audioPage) Key() string { return "语音" }

func (audioPage) Rows(f *settingsForm) []rowDesc {
	asrKind := f.asrKind.value()
	ttsKind := f.ttsKind.value()
	rows := []rowDesc{sec("语音识别（说话转文字）"), {kind: rowAudioASRKind}}
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
	rows = append(rows, sec("语音合成（文字转语音）"), rowDesc{kind: rowAudioTTSKind})
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
		return fieldRow(gtx, th, "识别方式", selectBtn(th, &f.asrKind)), true
	case rowAudioASRProvider:
		return f.audioServiceRow(gtx, th, &f.asrProv, &f.asrKeyUse), true
	case rowAudioASREndpoint:
		return fieldRow(gtx, th, "端点", editorBox(th, &f.asrBaseURL, "如 https://api.example.com/v1；留空 = 用「服务」那条的")), true
	case rowAudioASRKey:
		return f.audioKeyRow(gtx, th, &f.asrKey, "asr"), true
	case rowAudioASRModel:
		return fieldRow(gtx, th, "识别模型", editorBox(th, &f.asrModel, "如 mimo-v2.5-asr / whisper-1")), true
	case rowAudioASRPath:
		return f.audioModelPathRow(gtx, th), true
	case rowAudioLang:
		return fieldRow(gtx, th, "录音语种", selectBtn(th, &f.asrLang)), true
	case rowAudioTTSProvider:
		return f.audioServiceRow(gtx, th, &f.ttsProv, &f.ttsKeyUse), true
	case rowAudioTTSKind:
		return fieldRow(gtx, th, "合成方式", selectBtn(th, &f.ttsKind)), true
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
		l := material.Body2(th, "识别与合成各自是一份完整配置：先选方式（协议），再填本页给出的字段——"+
			"本地 whisper 只有模型文件，其余方式才有服务与端点。"+
			"需要端点或密钥的那几档，可以自己填，也可以选一个「服务」再点「填入」，"+
			"那会把该条目的端点与密钥引用抄过来，之后照样能改。"+
			"密钥只写不回显（留空 = 保持）。改方式、服务、端点或模型需保存并重启。"+
			"音色、音色描述与风格保存后对此后的播报生效；试听按当前填写的内容合成一句。")
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
			return fieldRow(gtx, th, "模型文件", editorBox(th, &f.asrPath, "本地 whisper 的 ggml 模型绝对路径（如 …\\ggml-base-q5_1.bin）"))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th),
					"模型不在仓库里，需要自己下载：到 huggingface.co/ggerganov/whisper.cpp "+
						"取 ggml-base-q5_1.bin（约 60MB，中文效果与速度的平衡点）。"+
						"只在「本地 whisper」这一档用到。")
				l.Color = paperOf().text3
				return l.Layout(gtx)
			})
		}),
	)
}

// audioServiceRow 「服务」一行：从 model.providers 里选一个条目 + 「填入」按钮（D222）。
// 「未配置」= 不借任何条目（端点与密钥全靠自己填）。
func (f *settingsForm) audioServiceRow(gtx layout.Context, th *material.Theme, prov *selectField, use *widget.Clickable) layout.Dimensions {
	return fieldRow(gtx, th, "服务", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(selectBtn(th, prov)),
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
	f.openPicker(gtx, "识别方式", &f.asrKind)
	f.openPicker(gtx, "语音识别服务", &f.asrProv)
	f.openPicker(gtx, "录音语种", &f.asrLang)
	f.openPicker(gtx, "合成方式", &f.ttsKind)
	f.openPicker(gtx, "语音合成服务", &f.ttsProv)
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
