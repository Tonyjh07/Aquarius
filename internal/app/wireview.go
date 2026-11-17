package app

import (
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// wireview.go 原始返回的可读视图（§7.1，`/raw`）：数据留存、帧切分与渲染。
//
// 与 `/context` 分工：那个看「发出去什么」，这个看「收回来什么」。两者都只读、都不写入会话树。
//
// 视图**只切不解析**——帧按 SSE 的空行分，正文原样给出。若在这里再解一遍协议，
// 就会和适配器的解码器形成第二份真相；而人要看这个视图，恰恰是因为不信第一份。

// wireKeep 留最近几次生成请求的原始响应。一轮 Turn 里的多轮「生成 + 工具」各算一次，
// 4 份够覆盖常规循环；再多就得考虑写入磁盘，而那该由 `/raw full` 显式决定。
const wireKeep = 4

// wireLog 最近几次留档，最旧在前。整体替换（不可变切片），读侧不必加锁。
type wireLog struct{ items []port.WireTrace }

// RecordWire 收下一次生成的原始响应。适配器在收流结束（正常/出错/中断）时回调一次，
// 执行在收流的那条 goroutine 上，这里只做一次切片替换，不阻塞。
func (a *Agent) RecordWire(t port.WireTrace) {
	for {
		old := a.wires.Load()
		next := &wireLog{}
		if old != nil {
			next.items = append(next.items, old.items...)
		}
		next.items = append(next.items, t)
		if len(next.items) > wireKeep {
			next.items = next.items[len(next.items)-wireKeep:]
		}
		if a.wires.CompareAndSwap(old, next) {
			return
		}
	}
}

// WireTrace 取倒数第 back 次（back 从 1 起）的留档。total 是当前留了几份。
func (a *Agent) WireTrace(back int) (tr port.WireTrace, total int, ok bool) {
	log := a.wires.Load()
	if log == nil {
		return port.WireTrace{}, 0, false
	}
	total = len(log.items)
	if back < 1 || back > total {
		return port.WireTrace{}, total, false
	}
	return log.items[total-back], total, true
}

// WireFrame 原始响应里的一帧：SSE 相邻空行之间的一段，或非 SSE 响应的整份正文。
type WireFrame struct {
	Index  int
	Kind   string // data / event / comment / body
	Text   string
	Bytes  int
	Head   string // 首行摘要（data 载荷优先）
	Marker bool   // 帧里带协议结束标记（[DONE] / response.completed / message_stop）
}

// splitWireFrames 把原始字节切成帧。
//
// 判据只有一条：出现 `data:` 行就按 SSE 切（空行分帧），否则整份算一帧——错误响应
// 常常是单个 JSON 对象或一页 HTML，按 SSE 切会把它切碎。
func splitWireFrames(raw []byte) []WireFrame {
	text := string(raw)
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	if !strings.Contains(text, "data:") {
		return []WireFrame{makeWireFrame(1, trimmed)}
	}
	var out []WireFrame
	var cur []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		body := strings.Join(cur, "\n")
		cur = nil
		if strings.TrimSpace(body) == "" {
			return // 连续空行 = 帧间空白，不是空帧
		}
		out = append(out, makeWireFrame(len(out)+1, body))
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

// makeWireFrame 归纳一帧：种类取首个可判别的行（data / event / comment），
// 摘要优先取 data 载荷——`event: ping` 这种帧的看点在它带没带 data。
func makeWireFrame(idx int, text string) WireFrame {
	f := WireFrame{Index: idx, Text: text, Bytes: len(text)}
	payload, first := "", ""
	for _, line := range strings.Split(text, "\n") {
		if first == "" {
			first = line
		}
		switch {
		case strings.HasPrefix(line, "data:"):
			if payload == "" {
				payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
			if f.Kind == "" {
				f.Kind = "data"
			}
		case strings.HasPrefix(line, "event:"):
			if f.Kind == "" {
				f.Kind = "event"
			}
		case strings.HasPrefix(line, ":"):
			if f.Kind == "" {
				f.Kind = "comment"
			}
		}
	}
	if f.Kind == "" {
		f.Kind = "body"
	}
	if payload == "" {
		payload = first
	}
	f.Head = clipRunes(payload, 60)
	f.Marker = wireMarker(text)
	return f
}

// wireMarker 帧里是否带协议结束标记。只做粗判，用途是概览里标一下「这一帧是收尾」，
// 不参与任何控制流——精确语义归各协议的解码器。
func wireMarker(text string) bool {
	return strings.Contains(text, "[DONE]") ||
		strings.Contains(text, "response.completed") ||
		strings.Contains(text, "message_stop")
}

// clipRunes 按字符截断（不切坏 UTF-8），末尾加省略号。
func clipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// wireEndText 结束原因的用户面说法。
func wireEndText(end string) string {
	switch end {
	case port.WireEndDone:
		return i18n.T("session.raw.end.done")
	case port.WireEndEOF:
		return i18n.T("session.raw.end.eof")
	case port.WireEndCancelled:
		return i18n.T("session.raw.end.cancelled")
	case port.WireEndStatus:
		return i18n.T("session.raw.end.status")
	default:
		return i18n.T("session.raw.end.error")
	}
}
