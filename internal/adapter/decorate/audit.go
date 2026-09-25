package decorate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// defaultAuditCap 审计文件大小上限（超出轮转一代到 <path>.1，§8）。
const defaultAuditCap = 4 << 20 // 4 MiB

// errAuditClosed 审计文件已关闭/不可写的内部错误（调用方静默忽略）。
var errAuditClosed = errors.New("audit: 文件已关闭")

// Audit 审计日志（DESIGN §8 audit.log）：JSONL 追加写、超限轮转一代、并发安全。
// 任何写失败静默——审计不得打断对话。
type Audit struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
	cap  int64
}

// auditLine 单条审计记录（一行 JSON）。
type auditLine struct {
	TS    string `json:"ts"`              // RFC3339Nano（UTC）
	Kind  string `json:"kind"`            // "llm" | "tool"
	Model string `json:"model,omitempty"` // kind=llm：目标模型
	Name  string `json:"name,omitempty"`  // kind=tool：工具名
	Msgs  int    `json:"msgs,omitempty"`  // kind=llm：请求消息条数
	DurMS int64  `json:"dur_ms"`
	OK    bool   `json:"ok"`
	Err   string `json:"err,omitempty"`
	In    int    `json:"in,omitempty"`  // usage.input_tokens（流末）
	Out   int    `json:"out,omitempty"` // usage.output_tokens（流末）
}

// NewAudit 打开（不存在则创建）审计文件；capBytes <=0 用默认上限。
func NewAudit(path string, capBytes int64) (*Audit, error) {
	if capBytes <= 0 {
		capBytes = defaultAuditCap
	}
	a := &Audit{path: path, cap: capBytes}
	if err := a.open(); err != nil {
		return nil, err
	}
	return a, nil
}

// open 打开（或重建）底层文件并校准已写字节数。
func (a *Audit) open() error {
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		a.f = nil
		return err
	}
	var size int64
	if st, err := f.Stat(); err == nil {
		size = st.Size()
	}
	a.f, a.size = f, size
	return nil
}

// Close 关闭底层文件（幂等）。
func (a *Audit) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	return err
}

// record 追加一行；失败静默返回错误供测试断言，调用方不处理。
func (a *Audit) record(line auditLine) error {
	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return errAuditClosed
	}
	if a.size+int64(len(data)) > a.cap {
		_ = a.rotate() // 轮转失败则回开原文件继续追加（不丢写）
		if a.f == nil {
			return errAuditClosed
		}
	}
	n, err := a.f.Write(data)
	a.size += int64(n)
	return err
}

// rotate 轮转一代：<path> → <path>.1（覆盖旧代），新开空文件继续。
func (a *Audit) rotate() error {
	_ = a.f.Close()
	a.f = nil
	backup := a.path + ".1"
	_ = os.Remove(backup) // Windows 的 rename 不覆盖既有目标
	_ = os.Rename(a.path, backup)
	return a.open() // rename 失败时 O_APPEND 回开原文件，等价于未轮转
}

// ---------------------------------------------------------------------------
// LLM 调用审计
// ---------------------------------------------------------------------------

// AuditLLM LLM 生成审计装饰器：每次 Generate 记一行（发起失败立即记；
// 成功流在 EOF/中断/错误时带 usage 记终态）。Models 透传不记。
func AuditLLM(inner port.LLM, a *Audit) port.LLM { return &auditLLM{inner: inner, a: a} }

type auditLLM struct {
	inner port.LLM
	a     *Audit
}

func (l *auditLLM) Generate(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	start := time.Now()
	stream, err := l.inner.Generate(ctx, req)
	if err != nil {
		_ = l.a.record(auditLine{
			TS: tsNow(), Kind: "llm", Model: req.Model, Msgs: len(req.Messages),
			DurMS: time.Since(start).Milliseconds(), OK: false, Err: err.Error(),
		})
		return nil, err
	}
	return &auditStream{
		inner: stream,
		done: func(u conversation.Usage, err error) {
			_ = l.a.record(auditLine{
				TS: tsNow(), Kind: "llm", Model: req.Model, Msgs: len(req.Messages),
				DurMS: time.Since(start).Milliseconds(), OK: err == nil, Err: errText(err),
				In: u.InputTokens, Out: u.OutputTokens,
			})
		},
	}, nil
}

func (l *auditLLM) Models(ctx context.Context) ([]port.ModelInfo, error) { return l.inner.Models(ctx) }

// auditStream 包装生成流：捕获流末 usage，EOF/错误/中断时记一行终态（只记一次）。
type auditStream struct {
	inner port.Stream
	done  func(conversation.Usage, error)

	mu    sync.Mutex
	usage conversation.Usage
	once  sync.Once
}

func (s *auditStream) Recv() (port.Delta, error) {
	d, err := s.inner.Recv()
	if d.Usage != nil {
		s.mu.Lock()
		s.usage = *d.Usage
		s.mu.Unlock()
	}
	switch {
	case err == nil:
	case errors.Is(err, io.EOF):
		s.finish(nil) // 正常结束：ok=true、err 留空
	default:
		s.finish(err)
	}
	return d, err
}

func (s *auditStream) Close() error {
	err := s.inner.Close()
	// 未见 EOF 的关闭 = 中断生成：仍记终态（err 视内层返回，nil 时标 closed）。
	if err == nil {
		err = errors.New("closed before eof")
	}
	s.finish(err)
	return err
}

// finish 幂等记账。
func (s *auditStream) finish(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		u := s.usage
		s.mu.Unlock()
		s.done(u, err)
	})
}

// ---------------------------------------------------------------------------
// 工具调用审计
// ---------------------------------------------------------------------------

// AuditTool 工具执行审计装饰器：Execute 每次记一行（工具名/耗时/结果状态）。
func AuditTool(inner port.ToolRunner, a *Audit) port.ToolRunner {
	return &auditTool{inner: inner, a: a}
}

type auditTool struct {
	inner port.ToolRunner
	a     *Audit
}

func (t *auditTool) Specs(ctx context.Context) ([]tool.Spec, error) { return t.inner.Specs(ctx) }

func (t *auditTool) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	start := time.Now()
	res, err := t.inner.Execute(ctx, call)
	errMsg := ""
	switch {
	case err != nil:
		errMsg = err.Error()
	case !res.OK:
		errMsg = res.Err
		if errMsg == "" {
			errMsg = "ok=false"
		}
	}
	_ = t.a.record(auditLine{
		TS: tsNow(), Kind: "tool", Name: call.Name,
		DurMS: time.Since(start).Milliseconds(), OK: err == nil && res.OK, Err: errMsg,
	})
	return res, err
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

// tsNow 审计时间戳（RFC3339Nano，UTC）。
func tsNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// errText 错误链转审计文本；nil 为空串。
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
