package acpgate

import (
	"fmt"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 正文保头尾（§5.18 上限）：转交可能交付很长的一份东西，收下时留头留尾、中间省略并
// 注明——既不撑爆上下文，也不至于只剩开头一句。
const (
	reportHeadCap = port.MaxDelegateReportChars - 50000
	reportTailCap = 50000
)

// appendText 攒 agent_message_chunk 的正文。超出上限后只留尾部窗口。
func (r *run) appendText(cb acp.ContentBlock) {
	if cb.Text == nil || cb.Text.Text == "" {
		return
	}
	text := cb.Text.Text
	if head := runeLen(r.report.String()); head < reportHeadCap {
		if room := reportHeadCap - head; runeLen(text) > room {
			rs := []rune(text)
			r.report.WriteString(string(rs[:room]))
			text = string(rs[room:])
		} else {
			r.report.WriteString(text)
			return
		}
	}
	// 头段满了：只往尾部窗口里塞；窗口溢出时丢最旧的，记账省略了多少。
	r.truncated = true
	r.reportTail += text
	if t := []rune(r.reportTail); len(t) > reportTailCap {
		drop := len(t) - reportTailCap
		r.dropped += drop
		r.reportTail = string(t[drop:])
	}
}

// reportText 拼出最终正文（头 + 省略说明 + 尾）。
func (r *run) reportText() string {
	head := r.report.String()
	if !r.truncated {
		return head
	}
	var b strings.Builder
	b.WriteString(head)
	fmt.Fprintf(&b, "\n\n[... %d chars omitted from the middle ...]\n\n", r.dropped)
	b.WriteString(r.reportTail)
	return b.String()
}

// addToolCall 数一次工具调用（子代理自己发起的，不是我们的）。
func (r *run) addToolCall() { r.toolCalls++ }

// addLocations 记它报过改动的文件（去重；超过上限只报总数）。
func (r *run) addLocations(locs []acp.ToolCallLocation) {
	for _, l := range locs {
		p := strings.TrimSpace(l.Path)
		if p == "" || r.changedSeen[p] {
			continue
		}
		r.changedSeen[p] = true
		if len(r.changed) >= port.MaxDelegateChangedFiles {
			r.changedOver++
			continue
		}
		r.changed = append(r.changed, p)
	}
}

// runeLen 字符数（上限按字符算，不按字节）。
func runeLen(s string) int { return len([]rune(s)) }
