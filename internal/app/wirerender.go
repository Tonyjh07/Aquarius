package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// wirerender.go `/raw` 的输出形状（§7.1）：概览、单帧、整份。
//
// 概览只给账与摘要：帧数、字节数、每帧的头几十个字符。看全文用编号或 full——
// 一帧正文动辄上千字符，铺进转写区就得逐行滚着找，不如按编号取。

// wireFrameRowsLimit 概览里最多列多少帧。超长的流（每 token 一帧）列全没有意义，
// 编号仍然可用，全文走 `/raw full`。
const wireFrameRowsLimit = 200

// renderWireOverview 概览：元信息 + 帧目录 + 下一步提示。
func renderWireOverview(tr port.WireTrace, frames []WireFrame, back, total int) string {
	m := tr.Meta
	when := i18n.T("session.raw.when_now")
	if back > 1 {
		when = i18n.Tf("session.raw.when_back", "k", back)
	}
	var b strings.Builder
	b.WriteString(i18n.Tf("session.raw.head", "when", when, "proto", m.Proto, "status", m.Status))
	b.WriteByte('\n')
	b.WriteString(i18n.Tf("session.raw.where", "url", m.URL, "model", m.Model))
	b.WriteByte('\n')
	b.WriteString(wireTiming(m))
	for _, h := range m.Headers {
		b.WriteByte('\n')
		b.WriteString(h)
	}
	for _, n := range m.Notes {
		b.WriteByte('\n')
		b.WriteString(i18n.Tf("session.raw.note", "text", n))
	}
	if m.End != "" {
		b.WriteByte('\n')
		b.WriteString(wireEndLine(m, frames))
	}

	if len(frames) > 0 {
		b.WriteString("\n\n")
		shown := frames
		if len(shown) > wireFrameRowsLimit {
			shown = shown[:wireFrameRowsLimit]
		}
		for _, f := range shown {
			b.WriteString(i18n.Tf("session.raw.frame",
				"idx", fmt.Sprintf("%4d", f.Index),
				"size", fmt.Sprintf("%8s", humanBytes(int64(f.Bytes))),
				"head", f.Head))
			b.WriteByte('\n')
		}
		if len(frames) > len(shown) {
			b.WriteString(i18n.Tf("session.raw.more_frames", "n", len(frames)-len(shown)))
			b.WriteByte('\n')
		}
		b.WriteString(i18n.T("session.raw.hint"))
	}

	if total > 1 {
		b.WriteString("\n")
		b.WriteString(i18n.Tf("session.raw.older", "n", total-1))
	}
	return b.String()
}

// wireTiming 收流耗时、首字节与实收字节。
func wireTiming(m port.WireMeta) string {
	size := humanBytes(int64(m.Bytes))
	if m.Truncated {
		size += i18n.T("session.raw.truncated")
	}
	elapsed := m.Elapsed.Round(100 * time.Millisecond).String()
	if m.FirstByte <= 0 {
		return i18n.Tf("session.raw.timing", "elapsed", elapsed, "size", size)
	}
	return i18n.Tf("session.raw.timing_first", "elapsed", elapsed,
		"first", m.FirstByte.Round(100*time.Millisecond).String(), "size", size)
}

// wireEndLine 结束原因 + 帧数构成。
func wireEndLine(m port.WireMeta, frames []WireFrame) string {
	if len(frames) == 0 {
		return i18n.Tf("session.raw.end_plain", "end", wireEndText(m.End))
	}
	data, comment := 0, 0
	for _, f := range frames {
		switch f.Kind {
		case "data":
			data++
		case "comment":
			comment++
		}
	}
	return i18n.Tf("session.raw.end", "end", wireEndText(m.End),
		"n", len(frames), "data", data, "comment", comment)
}

// renderWireDump 整份写入磁盘/次窗的内容：一段标注 + 原始字节。
//
// 头部每行以 `#` 打头，所以「去掉 # 开头的行」就是服务端原样返回的响应体——
// 既让人看得懂这份文件是什么，又不至于把原件改成一份加工品。
func renderWireDump(tr port.WireTrace) string {
	m := tr.Meta
	var b strings.Builder
	b.WriteString(i18n.Tf("session.raw.dump_head",
		"proto", m.Proto, "status", m.Status, "size", humanBytes(int64(m.Bytes))))
	b.WriteByte('\n')
	if m.URL != "" {
		b.WriteString(i18n.Tf("session.raw.dump_url", "url", m.URL))
		b.WriteByte('\n')
	}
	if m.End != "" {
		b.WriteString(i18n.Tf("session.raw.dump_end", "end", wireEndText(m.End)))
		b.WriteByte('\n')
	}
	for _, h := range m.Headers {
		b.WriteString(i18n.Tf("session.raw.dump_line", "text", h))
		b.WriteByte('\n')
	}
	for _, n := range m.Notes {
		b.WriteString(i18n.Tf("session.raw.dump_line", "text", n))
		b.WriteByte('\n')
	}
	b.WriteString(i18n.T("session.raw.dump_note"))
	b.WriteString("\n\n")
	b.Write(tr.Raw)
	if len(tr.Raw) == 0 || tr.Raw[len(tr.Raw)-1] != '\n' {
		b.WriteByte('\n')
	}
	return b.String()
}
