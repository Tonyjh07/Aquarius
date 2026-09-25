// Package notify 实现系统通知输出器（port.OutputAdapter，DESIGN §7.2 输出管线）。
//
// 扇出点在装配根的 Presenter 装饰器（D28/D14）：本包只负责"把一条已提交的
// 回答文本送出去"。平台发送经注入的 send 实现（装配根提供外部命令，如
// PowerShell 气泡 / notify-send / osascript），失败由装饰器记日志、不打断
// 主流程（§5.7 输出器语义）。
package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.OutputAdapter = (*Adapter)(nil)

// defaultPreview 通知正文预览长度（rune）：通知是摘要，完整回答在对话里。
const defaultPreview = 120

// Adapter 通知输出器。
type Adapter struct {
	send    func(title, body string) error
	preview int
}

// New 创建通知输出器（send 为平台发送实现，装配根注入；nil 时 Deliver 报错）。
func New(send func(title, body string) error) *Adapter {
	return &Adapter{send: send, preview: defaultPreview}
}

// Name 输出器名（装饰器日志与未来配置寻址用）。
func (a *Adapter) Name() string { return "notify" }

// Deliver 交付一条回答：抽取文本 → 截预览 → send。无文本内容不打扰（返回 nil）。
func (a *Adapter) Deliver(ctx context.Context, req port.OutputRequest) error {
	if a.send == nil {
		return errors.New("notify: 未配置发送器")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	text := Preview(MessageText(req), a.preview)
	if text == "" {
		return nil
	}
	return a.send("Aquarius", text)
}

// MessageText 抽取输出请求的文本（优先交付分片，回退消息内容），
// 换行压成空格——通知是单行摘要。
func MessageText(req port.OutputRequest) string {
	parts := req.Parts
	if len(parts) == 0 {
		parts = req.Message.Content
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == conversation.PartText && strings.TrimSpace(p.Text) != "" {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(strings.TrimSpace(p.Text))
		}
	}
	return collapseSpaces(b.String())
}

// Preview 截断到 max 个 rune（超出加省略号）；空文本返回空。
// 先剔除控制字符（§14 M3 遗留：ESC/BEL 等防通知/终端注入——正文是不可信模型输出）。
func Preview(text string, max int) string {
	text = sanitizeControl(collapseSpaces(text))
	if text == "" {
		return ""
	}
	if max <= 0 {
		return text
	}
	r := []rune(text)
	if len(r) <= max {
		return text
	}
	return string(r[:max]) + "…"
}

// sanitizeControl 剔除控制序列与控制字符（§14 M3 遗留）：
// 先吞完整 ANSI 转义序列（CSI `ESC[…终止`、OSC `ESC]…BEL/ESC\`、其余两字符转义），
// 再剔除残余 C0 控制符（保留空白已被 collapseSpaces 压平）、DEL 与 C1——
// 正文是不可信模型输出，ESC 剥净即瓦解注入面（§9：只渲染不执行）。
func sanitizeControl(s string) string {
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
					i = j
				} else {
					i = len(rs) - 1
				}
				continue
			default: // 其余两字符转义（如 ESC M）
				i++
				continue
			}
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// collapseSpaces 压平空白（含换行/制表）为空格。
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// String 便于日志展示。
func (a *Adapter) String() string { return fmt.Sprintf("notify(preview=%d)", a.preview) }
