// Package probe 实现观察器的探针端口（D190）：命令、文件、HTTP 三种取样方式。
//
// 边界很窄：它只负责**取到内容**，不判断命中（那是 app 层观察器的事），也不知道
// 观察器的节奏。取不到才报错；非零退出码、非 2xx 都是「取到了」，交给判据。
package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 取样上限：探针是给模型看的摘要，不是搬运文件的通道。超限按头尾裁剪。
const (
	maxOutputBytes = 64 << 10 // 单次取样原始字节上限
	maxFileBytes   = 1 << 20  // 文件读取上限：超过只读前 1MiB
	keepChars      = 4000     // 回填给观察器的字符上限（够判据用，也不至于塞爆上下文）
)

// Prober 探针实现。
type Prober struct {
	// HTTP 客户端（可注入；nil = 15s 超时的缺省客户端）。
	Client *http.Client
	// Logf 诊断日志（用户面中文）；nil = 丢弃。
	Logf func(format string, args ...any)
}

var _ port.WatchProber = (*Prober)(nil)

// New 建一个探针实现。
func New() *Prober { return &Prober{} }

// Probe 取样一次（D190）。命令走平台 shell（与 term_exec 同口径：整条命令行交给
// shell，引号/管道语义不在这里重新发明）。
func (p *Prober) Probe(ctx context.Context, spec port.WatchProbe) (port.WatchProbeResult, error) {
	switch spec.Kind {
	case port.WatchCommand:
		return p.command(ctx, spec)
	case port.WatchFile:
		return p.file(ctx, spec)
	case port.WatchHTTP:
		return p.http(ctx, spec)
	default:
		return port.WatchProbeResult{}, fmt.Errorf("unknown probe kind %q", spec.Kind)
	}
}

// command 跑一条命令取其输出（stdout+stderr）。退出码进 Status，不当错误。
func (p *Prober) command(ctx context.Context, spec port.WatchProbe) (port.WatchProbeResult, error) {
	cmd := shellCommand(ctx, spec.Target)
	if cmd == nil {
		return port.WatchProbeResult{}, errors.New("probe: empty command")
	}
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}
	out, err := cmd.CombinedOutput()
	res := port.WatchProbeResult{Output: clip(string(out))}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case errors.As(err, &ee):
		res.Status = ee.ExitCode() // 非零退出是「取到了」：判据自己决定要不要在意
		return res, nil
	case ctx.Err() != nil:
		return port.WatchProbeResult{}, ctx.Err()
	default:
		return port.WatchProbeResult{}, fmt.Errorf("probe: run command: %w", err)
	}
}

// file 读文件内容；不存在按空内容（观察「文件出现」时首轮就该是空的）。
func (p *Prober) file(ctx context.Context, spec port.WatchProbe) (port.WatchProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return port.WatchProbeResult{}, err
	}
	f, err := os.Open(spec.Target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return port.WatchProbeResult{Output: ""}, nil
		}
		return port.WatchProbeResult{}, fmt.Errorf("probe: read file: %w", err)
	}
	defer f.Close()
	buf := make([]byte, maxFileBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return port.WatchProbeResult{}, fmt.Errorf("probe: read file: %w", err)
	}
	return port.WatchProbeResult{Output: clip(string(buf[:n]))}, nil
}

// http GET 一个地址（只做 GET，D190 用户拍板）；状态码进 Status，2xx 与否不判错。
func (p *Prober) http(ctx context.Context, spec port.WatchProbe) (port.WatchProbeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.Target, nil)
	if err != nil {
		return port.WatchProbeResult{}, fmt.Errorf("probe: build request: %w", err)
	}
	cli := p.Client
	if cli == nil {
		cli = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := cli.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return port.WatchProbeResult{}, ctx.Err()
		}
		return port.WatchProbeResult{}, fmt.Errorf("probe: http get: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOutputBytes))
	if err != nil {
		if ctx.Err() != nil {
			return port.WatchProbeResult{}, ctx.Err()
		}
		return port.WatchProbeResult{}, fmt.Errorf("probe: read response: %w", err)
	}
	return port.WatchProbeResult{Status: resp.StatusCode, Output: clip(string(body))}, nil
}

// shellCommand 平台 shell 包装（与 term_exec 同口径）。
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/c", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}

// clip 取样内容按字符数保头尾裁剪：判据多半看开头或结尾，中段对模型也没用。
func clip(s string) string {
	s = strings.TrimRight(s, "\x00")
	r := []rune(s)
	if len(r) <= keepChars {
		return s
	}
	half := keepChars / 2
	return string(r[:half]) +
		fmt.Sprintf("\n...[probe output truncated: %d chars total]\n", len(r)) +
		string(r[len(r)-half:])
}
