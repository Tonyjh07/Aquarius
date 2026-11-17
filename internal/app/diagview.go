package app

// diagview.go `/diag` 的取行与拼装（§7.1）：把一次请求的三份事实合成一份可外发的文本。
//
// 三部分是「发出去什么」「这一轮做了什么」「收回来什么」：
//   - 上下文分块账（`/context last` 那份，含每条消息的 token）
//   - 这一轮的审计行（时间窗内，含耗时与失败原因）
//   - 原始返回（`/raw` 那份，服务端写出的字节）
//
// 对齐靠**一个锚点**：上下文的交付时刻与原始返回的起点是同一次请求，审计行按这个
// 时间窗筛。三份各自带自己的时间，读者可以逐行核对，而不是凭「大概是刚才那次」相信。
//
// 这里也刻意**不做诊断结论**：包里全是事实，判断留给看包的人（第一个看的是用户自己，
// 第二个才是维护者）。

import (
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 审计行的取行窗口：请求起点之前 diagLead、收流结束之后 diagTail。
//
// 前面留一段是因为审计行在**动作发起时**记时间、而原始返回的起点在拿到响应头之后；
// 后面留一小段是为了接住「收流刚结束就写下的那一行」。窗口不放大到整轮：那会把下一轮
// 工具调用也卷进来，三部分对不上同一次请求就不再是可核对的事实。
const (
	diagLead = 5 * time.Second
	diagTail = 2 * time.Second
)

// diagPack 一份诊断包的料。字段都是原样取得的只读数据，拼装不改任何一项。
type diagPack struct {
	Version string    // 构建版本（dev = 开发构建）
	OS      string    // runtime.GOOS
	Arch    string    // runtime.GOARCH
	Go      string    // runtime.Version()
	Now     time.Time // 生成时刻（本地时区展示）
	Anchor  time.Time // 请求起点：上下文交付时刻与原始返回起点取其一
	Elapsed time.Duration
	Context string // 上下文分块账（已渲染）
	Audit   []auditRow
	Wire    port.WireTrace
}

// diagWindow 审计行的取行区间（含两端）。
func diagWindow(anchor time.Time, elapsed time.Duration) (time.Time, time.Time) {
	return anchor.Add(-diagLead), anchor.Add(elapsed + diagTail)
}

// pickDiagAudit 取时间窗内的审计行，保持文件顺序。
//
// 时间解析不出来的行**丢掉**而不是猜：猜错一侧就把别的请求混进来，而这份包的用处
// 正是「这行确实属于那次请求」。
func pickDiagAudit(rows []auditRow, from, to time.Time) []auditRow {
	out := make([]auditRow, 0, len(rows))
	for _, r := range rows {
		ts, err := time.Parse(time.RFC3339Nano, r.TS)
		if err != nil {
			continue
		}
		if ts.Before(from) || ts.After(to) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// renderDiag 拼一份诊断包。
//
// 头部三行一律以 `#` 打头：与 `/raw full` 同一约定——「去掉 # 开头的行」就是原件，
// 而这份文件里的原件正是第三节的原始返回。正文各节之间也用 `#` 分隔，免得读者把
// 上一节的尾巴当成下一节的开头。
func renderDiag(p diagPack) string {
	from, to := diagWindow(p.Anchor, p.Elapsed)
	stamp := func(t time.Time) string { return t.Local().Format("2006-01-02 15:04:05") }
	var b strings.Builder
	b.WriteString(diagLine(i18n.Tf("session.diag.head", "version", p.Version,
		"os", p.OS, "arch", p.Arch, "go", p.Go)))
	b.WriteByte('\n')
	b.WriteString(diagLine(i18n.Tf("session.diag.made", "when", stamp(p.Now))))
	b.WriteByte('\n')
	b.WriteString("#\n")
	b.WriteString(diagLine(i18n.T("session.diag.warn")))
	b.WriteByte('\n')
	b.WriteString(diagLine(i18n.Tf("session.diag.anchor", "at", stamp(p.Anchor),
		"elapsed", p.Elapsed.Round(100*time.Millisecond).String())))
	b.WriteByte('\n')
	b.WriteString("#\n")

	b.WriteString(diagLine(i18n.T("session.diag.sec_ctx")))
	b.WriteByte('\n')
	b.WriteString(strings.TrimRight(p.Context, "\n"))
	b.WriteByte('\n')
	b.WriteByte('\n')

	b.WriteString(diagLine(i18n.Tf("session.diag.sec_audit", "from", stamp(from), "to", stamp(to))))
	b.WriteByte('\n')
	if len(p.Audit) == 0 {
		b.WriteString(i18n.T("session.diag.audit_none"))
		b.WriteByte('\n')
	} else {
		for _, r := range p.Audit {
			b.WriteString(auditRowLine(r))
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')

	b.WriteString(diagLine(i18n.T("session.diag.sec_wire")))
	b.WriteByte('\n')
	b.WriteString(renderWireDump(p.Wire))
	return b.String()
}

// diagLine 头部注释行（统一补 `# `）。空行调用方自己写，这里不吞。
func diagLine(s string) string { return "# " + s }

// diagName 诊断包文件名里的会话尾巴：前 8 位 ID（与 `/context full`、`/raw full` 同规则）。
func diagName(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
