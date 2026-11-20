package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// session_cmd_context.go ` /context` 命令（§7.1）：看装配好、即将发给模型的上下文。
//
// 四种用法，都只读：
//   - `/context`        下一轮将发送的概览（分块 token 账）
//   - `/context last`   上一轮**实际发出**的那份（含自动压缩后的形态）
//   - `/context <n>`    概览里编号为 n 的那条消息全文
//   - `/context full`   整份写进导出目录，回执给路径（`full last` 取上次实发）
//
// 全文不铺进转写区：一份上下文动辄十几万字符，铺开要过 markdown 解析与逐行选区，
// 而全文的实际用途是逐字比对、贴给别人、跨轮 diff——那三件事都更适合文件。
func (s *Session) execContext(ctx context.Context, cmd port.Command) (string, error) {
	args := cmd.Args
	wantLast := false
	switch {
	case len(args) == 0:
	case args[0] == "last":
		wantLast = true
		args = args[1:]
	case args[0] == "full":
		args = args[1:]
		if len(args) == 1 && args[0] == "last" {
			wantLast = true
			args = args[1:]
		}
		if len(args) > 0 {
			return "", errors.New(i18n.T("usage.context"))
		}
		return s.dumpContext(ctx, wantLast)
	default:
		if len(args) > 1 {
			return "", errors.New(i18n.T("usage.context"))
		}
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return "", errors.New(i18n.T("usage.context"))
		}
		return s.showContextMessage(ctx, n)
	}
	if len(args) > 0 {
		return "", errors.New(i18n.T("usage.context"))
	}
	rep, err := s.contextReport(ctx, wantLast)
	if err != nil {
		return "", err
	}
	return s.agent.renderContextSummary(rep), nil
}

// contextReport 取选定来源的报告：live = 下一轮将发送，last = 上一轮实发。
func (s *Session) contextReport(ctx context.Context, last bool) (ContextReport, error) {
	if last {
		rep, ok := s.agent.LastContextReport(ctx)
		if !ok {
			return ContextReport{}, errors.New(i18n.T("session.ctx.no_last"))
		}
		return rep, nil
	}
	rep, err := s.agent.ContextReport(ctx, s.cur)
	if err != nil {
		return ContextReport{}, wrapSession("err.estimate_usage", err)
	}
	return rep, nil
}

// showContextMessage 打印第 n 条消息的全文（n 从 1 起，与概览里的编号一致）。
func (s *Session) showContextMessage(ctx context.Context, n int) (string, error) {
	rep, err := s.agent.ContextReport(ctx, s.cur)
	if err != nil {
		return "", wrapSession("err.estimate_usage", err)
	}
	body, ok := sectionBody(rep, n)
	if !ok {
		return "", errors.New(i18n.Tf("session.ctx.no_message", "n", n, "max", len(rep.Sections)))
	}
	return body, nil
}

// dumpContext 整份上下文写入磁盘，回执给路径与大小。
//
// 文件名带时间戳：同一轮里连开两次不该互相覆盖，而「上次那份」正是要拿来比的。
func (s *Session) dumpContext(ctx context.Context, last bool) (string, error) {
	rep, err := s.contextReport(ctx, last)
	if err != nil {
		return "", err
	}
	if s.exportsDir == "" {
		return "", errors.New(i18n.T("exchange.no_exports_dir"))
	}
	if err := os.MkdirAll(s.exportsDir, 0o755); err != nil {
		return "", wrapSession("err.mkdir_export", err)
	}
	stem := "context"
	if s.cur != nil {
		// 会话 ID 只取前 8 位：导出目录里一眼要能扫完，完整 ID 在文件名里没有增量信息。
		id := string(s.cur.ID)
		if len(id) > 8 {
			id = id[:8]
		}
		stem += "-" + id
	}
	path := filepath.Join(s.exportsDir, fmt.Sprintf("%s-%s.md", stem, s.clock.Now().Format("20060102-150405")))
	body := s.agent.renderContextDump(rep)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", wrapSession("err.create_export", err)
	}
	// 界面有只读次窗就顺手铺开同一份；没有（行式界面）就忽略这条事件——文件已经写了，
	// 忽略不会丢信息。两者内容逐字相同，所以「看的」和「存的」不会各说一套。
	if s.ui != nil {
		_ = s.ui.Emit(ctx, port.TextViewEvent{
			Title: i18n.Tf("session.ctx.view_title", "model", rep.Model, "tok", rep.Total),
			Text:  body,
		})
	}
	return i18n.Tf("session.ctx.dumped", "path", path, "tok", rep.Total), nil
}
