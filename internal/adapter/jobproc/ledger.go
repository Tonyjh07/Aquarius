package jobproc

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

// 台账（D262）：任务在跑期间唯一的跨进程痕迹。
//
// 任务表与 watcher 都是内存态，退出即归零；而 Start 起的进程不随父进程退出（Windows
// 不挂 job object、unix 另起进程组），报告却再也不会产生。所以每次 Start 写一条，
// 进终态删一条，启动时读残留补一条事实注入。
//
// 一份文件而不是一条一文件：条目数等于并发任务数，量级是个位数；整份重写比散落一地的
// 小文件好对账，也不会随任务数增长出第二份文件。
const (
	ledgerName    = "ledger.json"
	ledgerVersion = 1
	// ledgerTailLines 对账报告里带的日志末几行：够看清它停在哪一步，又不会把上下文塞满。
	ledgerTailLines = 20
)

// ledgerEntry 一条在跑（或已结束而报告未送达）任务的台账记录。
type ledgerEntry struct {
	ID        port.JobID      `json:"id"`
	Session   conversation.ID `json:"session,omitempty"`
	Command   string          `json:"command"`
	Args      []string        `json:"args,omitempty"`
	WorkDir   string          `json:"work_dir,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	PID       int             `json:"pid"`
	LogPath   string          `json:"log_path"`
	Deadline  time.Time       `json:"deadline,omitempty"`
	// Status / ExitCode / EndedAt：进终态时补上但不删条目（D264）。删的条件是报告
	// **落地并落盘**，不是进程退出——中间这段窗口里退出程序，条目得留着让下次对账
	// 说「任务跑完了、退出码是这样、报告没送到」。
	Status   port.JobStatus `json:"status,omitempty"`
	ExitCode int            `json:"exit_code,omitempty"`
	EndedAt  time.Time      `json:"ended_at,omitempty"`
}

// ledgerDoc 台账文件形态（版本字段只为将来加字段时能识别旧文件）。
type ledgerDoc struct {
	Version int           `json:"version"`
	Entries []ledgerEntry `json:"entries"`
}

// addLedger 记一条在跑任务；写失败只写进任务日志，不阻断启动——
// 台账是事后对账用的，不能反过来让任务起不来。
func (m *Manager) addLedger(e ledgerEntry) {
	if err := m.updateLedger(func(d *ledgerDoc) {
		d.Entries = append(d.Entries, e)
	}); err != nil {
		m.logLedgerFailure(e.LogPath, err)
	}
}

// markFinished 任务进终态：记下状态与退出码，但**不删**条目（D264）——删要等报告
// 确认落到会话（Settle）。删早了，那段「已结束、报告还在路上」的窗口一退出就成了
// 既没有报告、也没有对账的静默丢失。
func (m *Manager) markFinished(id port.JobID, status port.JobStatus, code int, at time.Time) {
	if err := m.updateLedger(func(d *ledgerDoc) {
		for i := range d.Entries {
			if d.Entries[i].ID == id {
				d.Entries[i].Status = status
				d.Entries[i].ExitCode = code
				d.Entries[i].EndedAt = at
			}
		}
	}); err != nil {
		m.logLedgerFailure("", err)
	}
}

// Settle 报告已送达：抹掉条目（D264）。未知任务静默——报告送到时条目可能已经被
// 启动对账取走过（跨进程不存在），也可能本来就没记上（台账写失败）。
func (m *Manager) Settle(ctx context.Context, id port.JobID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.updateLedger(func(d *ledgerDoc) {
		out := d.Entries[:0]
		for _, e := range d.Entries {
			if e.ID != id {
				out = append(out, e)
			}
		}
		d.Entries = out
	}); err != nil {
		// 删不掉会留下一条假「未收尾」，下次启动多提一句——记进日志便于追。
		m.logLedgerFailure("", err)
		return err
	}
	return nil
}

// updateLedger 读-改-写整份台账（进程内串行；跨进程只有启动对账会读，且读即清）。
func (m *Manager) updateLedger(edit func(*ledgerDoc)) error {
	m.ledgerMu.Lock()
	defer m.ledgerMu.Unlock()
	d, err := m.readLedger()
	if err != nil {
		d = ledgerDoc{Version: ledgerVersion} // 坏文件：重建，不因上一轮的残留挡住这一轮
	}
	d.Version = ledgerVersion
	edit(&d)
	return m.writeLedger(d)
}

// readLedger 读台账；不存在返回空文档。
func (m *Manager) readLedger() (ledgerDoc, error) {
	var d ledgerDoc
	data, err := os.ReadFile(m.ledgerPath)
	if errors.Is(err, fs.ErrNotExist) {
		return ledgerDoc{Version: ledgerVersion}, nil
	}
	if err != nil {
		return d, fmt.Errorf("read ledger: %w", err)
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return d, fmt.Errorf("decode ledger: %w", err)
	}
	return d, nil
}

// writeLedger 原子换入（并发读到的永远是完整的一份）。
func (m *Manager) writeLedger(d ledgerDoc) error {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("encode ledger: %w", err)
	}
	if err := atomicfile.WriteFile(m.ledgerPath, data, 0o600); err != nil {
		return fmt.Errorf("write ledger: %w", err)
	}
	return nil
}

// Unfinished 取走全部残留条目（读即清，D262）：取走后不再重报。
//
// 清除失败不报错：宁可下次启动重报一遍，也不能把这一份事实丢掉。
func (m *Manager) Unfinished(ctx context.Context) ([]port.UnfinishedJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.ledgerMu.Lock()
	d, err := m.readLedger()
	if err != nil {
		m.ledgerMu.Unlock()
		// 坏台账当作没有：对账不该因为一份读不动的文件挡住启动。
		return nil, nil
	}
	if len(d.Entries) == 0 {
		m.ledgerMu.Unlock()
		return nil, nil
	}
	_ = m.writeLedger(ledgerDoc{Version: ledgerVersion})
	m.ledgerMu.Unlock()

	out := make([]port.UnfinishedJob, 0, len(d.Entries))
	for _, e := range d.Entries {
		status := e.Status
		if status == "" {
			status = port.JobRunning
		}
		item := port.UnfinishedJob{
			Job: port.Job{
				ID:        e.ID,
				Spec:      port.JobSpec{Command: e.Command, Args: e.Args},
				Status:    status,
				PID:       e.PID,
				ExitCode:  e.ExitCode,
				StartedAt: e.StartedAt,
				EndedAt:   e.EndedAt,
			},
			Session:  e.Session,
			LogPath:  e.LogPath,
			Deadline: e.Deadline,
			// 已进终态的条目不再探活：进程都没了还问 pid 只会撞上复用。
			Alive: status == port.JobRunning && alive(e.PID),
		}
		if tail, err := readLogTail(e.LogPath, ledgerTailLines); err == nil {
			item.LogTail = tail
		}
		out = append(out, item)
	}
	return out, nil
}

// logLedgerFailure 台账写失败记进任务日志（没日志路径时记 stderr 太吵，丢弃）。
func (m *Manager) logLedgerFailure(logPath string, err error) {
	if logPath == "" {
		return
	}
	f, oerr := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if oerr != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "[ledger update failed: %v]\n", err)
}

// readLogTail 读日志末尾 n 行（与 Logs 同一口径）；文件不存在返回空。
func readLogTail(path string, n int) (string, error) {
	text, truncated, err := readLogWindow(path)
	if err != nil || text == "" {
		return "", err
	}
	return tailLines(text, n, truncated), nil
}

// ledgerPathFor 台账文件位置（与日志同目录）。
func ledgerPathFor(logDir string) string { return filepath.Join(logDir, ledgerName) }
