// Package uicommon 收拢「会话分片 → 展示文本」的共用实现。
//
// 终端、TUI、GUI 与系统通知此前各有一份复制粘贴的 sanitizeControl / partsText /
// appendPartText：转义剥离、控制字符剔除、分片拼接的规则完全一样，只有「图片怎么呈现」
// 与「换行保不保留」两处不同。这里保留一份实现，把差异收成显式参数——差异仍在，
// 但只在一处声明，不会再各改各的。
package uicommon

import (
	"strings"
	"unicode/utf8"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// LayoutPolicy 剥离控制字符后是否保留换行与制表。
type LayoutPolicy bool

const (
	// DropLayout 连换行与制表一并剥掉：面向单行场景（系统通知气泡）。
	DropLayout LayoutPolicy = false
	// KeepLayout 保留换行与制表：面向多行排版（终端、TUI、GUI 正文）。
	KeepLayout LayoutPolicy = true
)

// ImageMode 图片分片在纯文本里的占位策略。
type ImageMode int

const (
	// ImageAlways 总是写「〔图片〕」——终端文本流没有缩略图可用。
	ImageAlways ImageMode = iota
	// ImageWhenUnaddressable 只在没有可寻址引用时写占位——GUI 用真缩略图承载正文，
	// 再补一行文字占位会让纯图消息变成一条假正文。
	ImageWhenUnaddressable
	// ImageNamed 总是写占位并带上引用名——TUI 没有缩略图，但文件名可读。
	ImageNamed
)

// SanitizeControl 剔除控制序列与控制字符：先吞完整 ANSI 转义序列
// （CSI `ESC[…终止`、OSC `ESC]…BEL/ESC\`、其余两字符转义），再剔除残余 C0 控制符、
// DEL 与 C1 以及非法 UTF-8。
//
// 正文是不可信模型输出，把 ESC 剥净即瓦解终端/通知注入面（只渲染不执行）。
// 换行与制表是否保留由 layout 决定：单行场景剥离，多行排版保留。
func SanitizeControl(s string, layout LayoutPolicy) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == 0x1b && i+1 < len(rs) {
			switch rs[i+1] {
			case '[': // CSI：吞至终结字节（0x40–0x7E）
				j := i + 2
				for j < len(rs) && (rs[j] < 0x40 || rs[j] > 0x7e) {
					j++
				}
				if j < len(rs) {
					i = j
				} else {
					i = len(rs) - 1
				}
				continue
			case ']': // OSC：吞至 BEL 或 ESC\
				j := i + 2
				for j < len(rs) && rs[j] != 0x07 && !(rs[j] == 0x1b && j+1 < len(rs) && rs[j+1] == '\\') {
					j++
				}
				if j < len(rs) {
					if rs[j] == 0x1b {
						i = j + 1 // ST 的 ESC\ 两字符都要跳过
					} else {
						i = j
					}
				} else {
					i = len(rs) - 1
				}
				continue
			default: // 其余两字符转义（如 ESC M）
				i++
				continue
			}
		}
		keepWhitespace := layout == KeepLayout && (r == '\n' || r == '\t')
		if r < 0x20 && !keepWhitespace || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// PartsText 把分片序列拼成一段展示文本，最后整体过一遍控制字符剥离。
func PartsText(parts []conversation.Part, mode ImageMode) string {
	var b strings.Builder
	for _, p := range parts {
		AppendPartText(&b, p, mode)
	}
	return SanitizeControl(b.String(), KeepLayout)
}

// AppendPartText 追加单个分片的正文。思考与工具分片单独成行/成块，不进正文。
func AppendPartText(b *strings.Builder, p conversation.Part, mode ImageMode) {
	switch p.Kind {
	case conversation.PartText:
		b.WriteString(p.Text)
	case conversation.PartImage:
		appendImage(b, p, mode)
	case conversation.PartAudio:
		b.WriteString("〔音频转写〕")
	case conversation.PartDoc:
		b.WriteString(p.Text)
	case conversation.PartThinking, conversation.PartTool:
		// 单独成块（思考卡 / 工具 chip），不混正文。
	}
}

func appendImage(b *strings.Builder, p conversation.Part, mode ImageMode) {
	switch mode {
	case ImageWhenUnaddressable:
		// 有可寻址引用 = 界面已画出缩略图，不再补文字占位。
		if p.Ref != nil && p.Ref.Hash != "" {
			return
		}
		b.WriteString("〔图片〕")
	case ImageNamed:
		name := "图片"
		if p.Ref != nil {
			name = "图片：" + p.Ref.Name
		}
		b.WriteString("〔" + name + "〕")
	default:
		b.WriteString("〔图片〕")
	}
}
