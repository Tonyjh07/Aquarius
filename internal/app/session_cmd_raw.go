package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// session_cmd_raw.go `/raw` 命令（§7.1）：看模型 API 的原始返回。
//
// 四种用法，都只读：
//   - `/raw`           最近一次请求的概览（元信息 + 帧目录）
//   - `/raw -k`        倒数第 k 次的概览（-1 最新，缺省即 -1）
//   - `/raw <n>`       概览里第 n 帧的原文
//   - `/raw full [-k]` 整份原始返回写进导出目录，回执给路径
//
// 帧不给「解析后的含义」，只给原文与字节数——语义归适配器的解码器，这里再解一遍
// 就成了第二份真相，而人要看原始返回恰恰是因为不信第一份。
func (s *Session) execRaw(ctx context.Context, cmd port.Command) (string, error) {
	back, frame, full, seenBack := 1, 0, false, false
	for _, a := range cmd.Args {
		switch {
		case a == "full":
			if full {
				return "", errors.New(i18n.T("usage.raw"))
			}
			full = true
		case strings.HasPrefix(a, "-"):
			// 负数 = 往回数第几次；正数 = 帧号。两类编号不混，所以各自只许出现一次。
			n, err := strconv.Atoi(strings.TrimPrefix(a, "-"))
			if err != nil || n < 1 || seenBack {
				return "", errors.New(i18n.T("usage.raw"))
			}
			seenBack = true
			back = n
		default:
			n, err := strconv.Atoi(a)
			if err != nil || n < 1 || frame != 0 {
				return "", errors.New(i18n.T("usage.raw"))
			}
			frame = n
		}
	}
	if full && frame > 0 {
		return "", errors.New(i18n.T("usage.raw"))
	}

	tr, total, ok := s.agent.WireTrace(back)
	if !ok {
		if total == 0 {
			return "", errors.New(i18n.T("session.raw.no_trace"))
		}
		return "", errors.New(i18n.Tf("session.raw.no_back", "keep", total))
	}
	frames := splitWireFrames(tr.Raw)
	if frame > 0 {
		if frame > len(frames) {
			return "", errors.New(i18n.Tf("session.raw.no_frame", "n", frame, "max", len(frames)))
		}
		return frames[frame-1].Text, nil
	}
	if full {
		return s.dumpRaw(ctx, tr)
	}
	return renderWireOverview(tr, frames, back, total), nil
}

// dumpRaw 整份原始返回落盘，顺手铺进只读次窗。
//
// 用 `.txt` 不用 `.md`：正文里常有 `___`、`**`、`#` 这些 markdown 记号，渲染一遍就不是
// 原文了；而这份文件的用途就是逐字对照。
func (s *Session) dumpRaw(ctx context.Context, tr port.WireTrace) (string, error) {
	if s.exportsDir == "" {
		return "", errors.New(i18n.T("exchange.no_exports_dir"))
	}
	if err := os.MkdirAll(s.exportsDir, 0o755); err != nil {
		return "", wrapSession("err.mkdir_export", err)
	}
	stem := "raw"
	if s.cur != nil {
		// 会话 ID 只取前 8 位：导出目录里一眼要能扫完（与 `/context full` 同口径）。
		id := string(s.cur.ID)
		if len(id) > 8 {
			id = id[:8]
		}
		stem += "-" + id
	}
	name := fmt.Sprintf("%s-%s.txt", stem, s.clock.Now().Format("20060102-150405"))
	path := filepath.Join(s.exportsDir, name)
	body := renderWireDump(tr)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", wrapSession("err.create_export", err)
	}
	if s.ui != nil {
		_ = s.ui.Emit(ctx, port.TextViewEvent{
			Title: i18n.Tf("session.raw.view_title", "proto", tr.Meta.Proto, "status", tr.Meta.Status),
			Text:  body,
		})
	}
	return i18n.Tf("session.raw.dumped", "path", path), nil
}
