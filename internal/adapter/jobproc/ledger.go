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

// ledgerEntry 一条在跑任务的台账记录。
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

// dropLedger 任务进终态：抹掉它的台账条目。
func (m *Manager) dropLedger(id port.JobID) {
	if err := m.updateLedger(func(d *ledgerDoc) {
		out := d.Entries[:0]
		for _, e := range d.Entries {
			if e.ID != id {
				out = append(out, e)
			}
		}
		d.Entries = out
	}); err != nil {
		// 终态删不掉会留下一条假「未收尾」，下次启动多报一条——记进日志便于追。
		m.logLedgerFailure("", err)
	}
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
		item := port.UnfinishedJob{
			Job: port.Job{
				ID:        e.ID,
				Spec:      port.JobSpec{Command: e.Command, Args: e.Args},
				Status:    port.JobRunning,
				PID:       e.PID,
				StartedAt: e.StartedAt,
			},
			Session:  e.Session,
			LogPath:  e.LogPath,
			Deadline: e.Deadline,
			Alive:    alive(e.PID),
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
