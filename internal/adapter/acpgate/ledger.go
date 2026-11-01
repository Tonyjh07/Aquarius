package acpgate

// 转交台账（§5.18/D269）：一次转交在跑期间的**唯一跨进程痕迹**。
//
// 为什么要有：转交是「派出去就不管」的形态——进程在跑、报告还没回来，这时候关掉程序，
// 除了一个被我们杀掉的进程组，什么都不会留下；下次启动不会有人知道「上次有一件活没有
// 下文」。任务那边（jobproc）已经用同一招解决过（D262/D264），这里照抄它的三段式：
//
//	Start 记一条 → 进终态只标状态、不删 → 报告确认送达才删（Settle）
//
// 删早了，那段「跑完了、报告还在路上」的窗口一退出就成了既没报告也没对账的静默丢失。
// 与任务台账的唯一差别：转交没有日志文件与 pid 探活（进程随退出被杀），条目里留下的是
// 「哪件活、谁去做的、什么时候开始的、停在哪一步」。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	ledgerVersion = 1
	// ledgerTaskRunes 台账里留的任务描述长度：够认出是哪件活，又不至于把台账写成正文
	// （原文在会话里，台账只做对账）。
	ledgerTaskRunes = 400
)

// delegateLedgerEntry 一条未收尾的转交记录。
type delegateLedgerEntry struct {
	ID        port.DelegateID `json:"id"`
	Agent     string          `json:"agent"`
	Session   conversation.ID `json:"session,omitempty"`
	Task      string          `json:"task,omitempty"`
	WorkDir   string          `json:"work_dir,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	PID       int             `json:"pid,omitempty"`
	// Stop / Error / EndedAt：进终态时补上但不删条目（D264 同口径），删的条件是报告
	// **落地并落盘**，不是这次转交结束。
	Stop    string    `json:"stop,omitempty"`
	Error   string    `json:"error,omitempty"`
	EndedAt time.Time `json:"ended_at,omitempty"`
}

// delegateLedgerDoc 台账文件形态（版本字段只为将来加字段时识别旧文件）。
type delegateLedgerDoc struct {
	Version int                   `json:"version"`
	Entries []delegateLedgerEntry `json:"entries"`
}

// ledgerEnabled 没有台账路径 = 不记（测试与嵌入式装配）。
func (m *Manager) ledgerEnabled() bool { return m != nil && m.opts.LedgerPath != "" }

// ledgerAdd 记一条在跑转交。写失败只记日志，不阻断派活——台账是事后对账用的，
// 不能反过来让活派不出去。
func (m *Manager) ledgerAdd(r *run) {
	if !m.ledgerEnabled() {
		return
	}
	task := []rune(r.spec.Task)
	if len(task) > ledgerTaskRunes {
		task = append(task[:ledgerTaskRunes], '…')
	}
	err := m.updateLedger(func(d *delegateLedgerDoc) {
		// 同 id 的旧条目先抹掉（id 只在进程内唯一，重启后从 d001 重来；跨进程不会撞上，
		// 但同一份文件被两次运行连着写时，留着旧条目会让「中断了几笔」数错）。
		d.Entries = dropEntry(d.Entries, r.id)
		d.Entries = append(d.Entries, delegateLedgerEntry{
			ID:        r.id,
			Agent:     r.agent.Name,
			Session:   r.session,
			Task:      string(task),
			WorkDir:   r.spec.WorkDir,
			StartedAt: r.started,
		})
	})
	if err != nil {
		m.opts.Logf("转交 %s 写台账失败: %v", r.id, err)
	}
}

// ledgerPID 记下子进程 pid（起进程之后才拿得到）：对账里能说出「当时那个进程是哪个」。
func (m *Manager) ledgerPID(id port.DelegateID, pid int) {
	if !m.ledgerEnabled() || pid <= 0 {
		return
	}
	if err := m.updateLedger(func(d *delegateLedgerDoc) {
		for i := range d.Entries {
			if d.Entries[i].ID == id {
				d.Entries[i].PID = pid
			}
		}
	}); err != nil {
		m.opts.Logf("转交 %s 写台账失败: %v", id, err)
	}
}

// ledgerFinish 进终态：记下停因与报错，但**不删**条目——删要等报告确认送到会话。
func (m *Manager) ledgerFinish(r *run, err error) {
	if !m.ledgerEnabled() {
		return
	}
	stop := stopName(r.stopKind())
	msg := ""
	if err != nil {
		msg = err.Error()
		// 报错可能很长（带 stderr 尾巴）：台账只留一行摘要，原文在报告正文里。
		if rs := []rune(msg); len(rs) > 200 {
			msg = string(rs[:200]) + "…"
		}
	}
	if uerr := m.updateLedger(func(d *delegateLedgerDoc) {
		for i := range d.Entries {
			if d.Entries[i].ID == r.id {
				d.Entries[i].Stop = stop
				d.Entries[i].Error = msg
				d.Entries[i].EndedAt = m.opts.Now()
			}
		}
	}); uerr != nil {
		m.opts.Logf("转交 %s 写台账失败: %v", r.id, uerr)
	}
}

// Settle 报告已送达会话：抹掉条目（下次启动不再提它）。
//
// 未知 id 静默：条目可能已被启动对账取走，也可能本来就没记上（台账写失败）。
func (m *Manager) Settle(ctx context.Context, id port.DelegateID) error {
	if !m.ledgerEnabled() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.updateLedger(func(d *delegateLedgerDoc) {
		d.Entries = dropEntry(d.Entries, id)
	}); err != nil {
		// 删不掉会留下一条假「未收尾」，下次启动多提一句——记日志便于追。
		m.opts.Logf("转交 %s 销账失败: %v", id, err)
		return err
	}
	return nil
}

// Unfinished 取走全部残留条目（读即清）：取走后不再重报。
//
// 清除失败不报错：宁可下次启动重报一遍，也不能把这一份事实丢掉。
func (m *Manager) Unfinished(ctx context.Context) ([]port.UnfinishedDelegate, error) {
	if !m.ledgerEnabled() {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.ledgerMu.Lock()
	d, err := m.readLedger()
	if err != nil {
		m.ledgerMu.Unlock()
		return nil, nil // 坏台账当作没有：对账不该因为一份读不动的文件挡住启动
	}
	if len(d.Entries) == 0 {
		m.ledgerMu.Unlock()
		return nil, nil
	}
	_ = m.writeLedger(delegateLedgerDoc{Version: ledgerVersion})
	m.ledgerMu.Unlock()

	out := make([]port.UnfinishedDelegate, 0, len(d.Entries))
	for _, e := range d.Entries {
		out = append(out, port.UnfinishedDelegate{
			ID: e.ID, Agent: e.Agent, Session: e.Session, Task: e.Task, WorkDir: e.WorkDir,
			StartedAt: e.StartedAt, Stop: e.Stop, Error: e.Error, EndedAt: e.EndedAt,
		})
	}
	return out, nil
}

// updateLedger 读-改-写整份台账（进程内串行；跨进程只有启动对账会读，且读即清）。
func (m *Manager) updateLedger(edit func(*delegateLedgerDoc)) error {
	m.ledgerMu.Lock()
	defer m.ledgerMu.Unlock()
	d, err := m.readLedger()
	if err != nil {
		d = delegateLedgerDoc{Version: ledgerVersion} // 坏文件：重建，不上一轮的残留挡住这一轮
	}
	d.Version = ledgerVersion
	edit(&d)
	return m.writeLedger(d)
}

// readLedger 读台账；不存在返回空文档。
func (m *Manager) readLedger() (delegateLedgerDoc, error) {
	var d delegateLedgerDoc
	data, err := os.ReadFile(m.opts.LedgerPath)
	if errors.Is(err, fs.ErrNotExist) {
		return delegateLedgerDoc{Version: ledgerVersion}, nil
	}
	if err != nil {
		return d, fmt.Errorf("read delegate ledger: %w", err)
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return d, fmt.Errorf("decode delegate ledger: %w", err)
	}
	return d, nil
}

// writeLedger 原子换入（并发读到的永远是完整的一份）。
func (m *Manager) writeLedger(d delegateLedgerDoc) error {
	if d.Entries == nil {
		d.Entries = []delegateLedgerEntry{} // 空台账写成 []，不写 null（读回来也是空数组，少一档歧义）
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("encode delegate ledger: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(m.opts.LedgerPath), 0o755); err != nil {
		return fmt.Errorf("create delegate ledger dir: %w", err)
	}
	if err := atomicfile.WriteFile(m.opts.LedgerPath, data, 0o600); err != nil {
		return fmt.Errorf("write delegate ledger: %w", err)
	}
	return nil
}

// dropEntry 去掉指定 id 的条目（保持其余次序）。
func dropEntry(entries []delegateLedgerEntry, id port.DelegateID) []delegateLedgerEntry {
	out := entries[:0]
	for _, e := range entries {
		if e.ID != id {
			out = append(out, e)
		}
	}
	return out
}

// stopName 停止原因进台账的字面（对账正文与日志共用一份词）。
func stopName(k stopKind) string {
	switch k {
	case stopKilled:
		return "killed"
	case stopTimeout:
		return "timeout"
	case stopShutdown:
		return "shutdown"
	default:
		return ""
	}
}
