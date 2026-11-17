package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// session_cmd_diag.go `/diag`（§7.1）：把一次请求的三份事实打成一份文本，写进导出目录。
//
// 三份事实按同一个锚点对齐：上下文取「上一轮实发」快照，原始返回取最近一次留档，审计行
// 取锚点时间窗内的那些。三者本来就在内存/日志里，`/diag` 只是把它们放到一处——
// 不再采一份样，也不在包里下结论。
//
// 无参数：一个包只讲一次请求，选哪次由「最近一次」决定。要看别的请求先 `/raw -k` 认清，
// 这类「选哪一次」的旋钮留到有人真的需要时再加，那时才有真实的判断依据。
func (s *Session) execDiag(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) > 0 {
		return "", errors.New(i18n.T("session.diag.usage_err"))
	}
	rep, ok := s.agent.LastContextReport(ctx)
	if !ok || rep.At.IsZero() {
		return "", errors.New(i18n.T("session.diag.no_context"))
	}
	tr, _, ok := s.agent.WireTrace(1)
	if !ok {
		return "", errors.New(i18n.T("session.diag.no_trace"))
	}
	// 审计行取不到不算失败：没有审计日志（未接线）时这一节写「窗口内没有」，其余两节照发。
	// 一份缺一节的包仍然有用——比「因为缺审计就什么都拿不到」有用。
	var rows []auditRow
	if s.auditPath != "" {
		all, _, err := readAudit(s.auditPath)
		if err != nil {
			return "", err
		}
		from, to := diagWindow(rep.At, tr.Meta.Elapsed)
		rows = pickDiagAudit(all, from, to)
	}
	return s.dumpDiag(ctx, diagPack{
		Version: s.version,
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Go:      runtime.Version(),
		Now:     s.clock.Now(),
		Anchor:  rep.At,
		Elapsed: tr.Meta.Elapsed,
		Context: s.agent.renderContextSummary(rep),
		Audit:   rows,
		Wire:    tr,
	})
}

// dumpDiag 落盘 + 铺进只读次窗，回执给路径与大小。
//
// 锚点取的是**上下文交付时刻**而不是原始返回的起点：两者是同一次请求的两端，取先发生
// 的那个，窗口才把这一段整个盖住。
func (s *Session) dumpDiag(ctx context.Context, p diagPack) (string, error) {
	if s.exportsDir == "" {
		return "", errors.New(i18n.T("exchange.no_exports_dir"))
	}
	if err := os.MkdirAll(s.exportsDir, 0o755); err != nil {
		return "", wrapSession("err.mkdir_export", err)
	}
	stem := "diag"
	if s.cur != nil {
		stem += "-" + diagName(string(s.cur.ID))
	}
	path := filepath.Join(s.exportsDir, stem+"-"+s.clock.Now().Format("20060102-150405")+".txt")
	body := renderDiag(p)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", wrapSession("err.create_export", err)
	}
	if s.ui != nil {
		_ = s.ui.Emit(ctx, port.TextViewEvent{
			Title: i18n.Tf("session.diag.view_title", "model", p.Wire.Meta.Model),
			Text:  body,
		})
	}
	return i18n.Tf("session.diag.dumped", "path", path, "size", humanBytes(int64(len(body)))), nil
}
