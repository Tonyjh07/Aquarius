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

// 收集面的并发口径：session/update 跑在 SDK 的通知 goroutine 上，request_permission
// 每个请求各开一条 goroutine，收尾读取又在 run goroutine 上——三者可以重叠，所以每个
// 入口都走 r.cmu。

// appendText 攒 agent_message_chunk 的正文。超出上限后只留尾部窗口。
//
// 头段长度增量累加（曾经每个 chunk 都 String() 再数一遍 rune，长报告是 O(n²)）。
func (r *run) appendText(cb acp.ContentBlock) {
	if cb.Text == nil || cb.Text.Text == "" {
		return
	}
	r.cmu.Lock()
	defer r.cmu.Unlock()
	text := cb.Text.Text
	if r.headLen < reportHeadCap {
		if room := reportHeadCap - r.headLen; runeLen(text) > room {
			rs := []rune(text)
			r.report.WriteString(string(rs[:room]))
			r.headLen += room
			text = string(rs[room:])
		} else {
			r.report.WriteString(text)
			r.headLen += runeLen(text)
			return
		}
	}
	// 头段满了：只往尾部窗口里塞。窗口按两倍上限攒、满了整段前移，摊还下来每个字符
	// 只搬一次；真正丢多少在 reportText 里一次算清。
	r.truncated = true
	r.tailRunes = append(r.tailRunes, []rune(text)...)
	if len(r.tailRunes) > 2*reportTailCap {
		drop := len(r.tailRunes) - reportTailCap
		r.dropped += drop
		r.tailRunes = append(r.tailRunes[:0], r.tailRunes[drop:]...)
	}
}

// reportText 拼出最终正文（头 + 省略说明 + 尾）。
func (r *run) reportText() string {
	r.cmu.Lock()
	head := r.report.String()
	truncated, dropped := r.truncated, r.dropped
	tail := r.tailRunes
	if len(tail) > reportTailCap {
		extra := len(tail) - reportTailCap
		dropped += extra
		tail = tail[extra:]
	}
	tailStr := string(tail)
	r.cmu.Unlock()

	if !truncated {
		return head
	}
	var b strings.Builder
	b.WriteString(head)
	fmt.Fprintf(&b, "\n\n[... %d chars omitted from the middle ...]\n\n", dropped)
	b.WriteString(tailStr)
	return b.String()
}

// addToolCall 数一次工具调用（子代理自己发起的，不是我们的）。
func (r *run) addToolCall() {
	r.cmu.Lock()
	r.toolCalls++
	r.cmu.Unlock()
}

// addLocations 记它报过改动的文件（去重；超过上限只记数）。
func (r *run) addLocations(locs []acp.ToolCallLocation) {
	r.cmu.Lock()
	defer r.cmu.Unlock()
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
