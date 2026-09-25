// Package jobproc 实现 port.JobManager（DESIGN §5.8）：
//   - 后台任务 = 独立进程，stdout/stderr 落盘 <logDir>/<id>.log；
//   - 任务表 v1 内存态（D8），进程内互斥更新（watcher goroutine 写、/jobs 读，过 -race）；
//   - 任务不随会话取消（§10：Start 不绑定调用方 ctx），job_kill 显式终止
//     （Windows 杀进程树 / unix 杀进程组）；Timeout 到点自动终止。
//
// 同步执行 Run（term_exec 路）响应 ctx：取消即终止进程并上抛（Runner 归类超时/取消）。
package jobproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.JobManager = (*Manager)(nil)

// logWindow Logs 的读取窗口上限（超大日志只读末尾这一窗口，防爆内存/上下文）。
const logWindow = 1 << 20 // 1MB

// Manager port.JobManager 实现。
type Manager struct {
	logDir string
	mu     sync.Mutex
	byID   map[port.JobID]*jobRec
	seq    int // 任务号序列（日志文件 O_EXCL 占位保证跨进程不撞号）
}

// jobRec 单个任务的运行时记录（除日志句柄外，字段均在 Manager.mu 下更新）。
type jobRec struct {
	job      port.Job
	logPath  string
	cmd      *exec.Cmd
	logFile  *os.File
	killed   bool // 显式 Kill（区分自然退出）
	timedOut bool // Timeout 到点终止
}

// New 创建任务管理器并确保日志目录存在。
func New(logDir string) (*Manager, error) {
	if strings.TrimSpace(logDir) == "" {
		return nil, errors.New("jobproc: 日志目录为空")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("jobproc: 创建日志目录 %s: %w", logDir, err)
	}
	return &Manager{logDir: logDir, byID: map[port.JobID]*jobRec{}}, nil
}

// Start 后台启动任务：独立进程 + 日志落盘，返回即刻快照（Status=running）。
// 不绑定调用方 ctx（§10：会话取消不影响后台任务）；spec.Timeout>0 到点自动终止。
func (m *Manager) Start(ctx context.Context, spec port.JobSpec) (port.Job, error) {
	if err := ctx.Err(); err != nil {
		return port.Job{}, err
	}
	if strings.TrimSpace(spec.Command) == "" {
		return port.Job{}, errors.New("jobproc: 缺少 command")
	}
	id, logPath, logFile, err := m.allocLog()
	if err != nil {
		return port.Job{}, err
	}
	cmd := exec.Command(spec.Command, spec.Args...)
	applySpec(cmd, spec)
	setupProc(cmd) // unix 独立进程组（killTree 连子进程终止）；windows 空操作
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		_ = os.Remove(logPath)
		return port.Job{}, fmt.Errorf("jobproc: 启动 %s: %w", spec.Command, err)
	}
	_, _ = fmt.Fprintf(logFile, "$ %s\n", commandLine(spec))
	rec := &jobRec{
		job:     port.Job{ID: id, Spec: spec, Status: port.JobRunning, PID: cmd.Process.Pid, StartedAt: time.Now()},
		logPath: logPath,
		cmd:     cmd,
		logFile: logFile,
	}
	m.mu.Lock()
	m.byID[id] = rec
	m.mu.Unlock()
	if spec.Timeout > 0 {
		time.AfterFunc(spec.Timeout, func() { m.expire(rec) })
	}
	go m.wait(rec)
	return m.snap(rec), nil
}

// Run 同步执行（term_exec 路）：合并 stdout/stderr 返回，ctx 取消即终止进程。
// 退出码非 0 / 启动失败 → Result{OK:false}（§10 交模型自行纠正）；
// error 只留给 ctx 取消（装配级，Runner/Agent 据此归类）。
func (m *Manager) Run(ctx context.Context, spec port.JobSpec) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if strings.TrimSpace(spec.Command) == "" {
		return tool.Result{OK: false, Err: "缺少 command"}, nil
	}
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	applySpec(cmd, spec)
	setupProc(cmd)
	// 注意：取消只终止直接子进程（如 cmd/sh），其孙进程可能残留——v1 接受，
	// 后台任务的整树终止由 Start + killTree 承担。
	out, err := cmd.CombinedOutput()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return tool.Result{}, ctxErr
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return tool.Result{
				OK:     false,
				Output: string(out),
				Err:    fmt.Sprintf("退出码 %d", ee.ExitCode()),
			}, nil
		}
		return tool.Result{OK: false, Output: string(out), Err: "启动: " + err.Error()}, nil
	}
	return tool.Result{OK: true, Output: string(out)}, nil
}

// List 按启动时间升序返回全部任务快照。
func (m *Manager) List(ctx context.Context) ([]port.Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	out := make([]port.Job, 0, len(m.byID))
	for _, r := range m.byID {
		out = append(out, r.job)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.Before(out[j].StartedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Status 返回任务快照；未知任务报错。
func (m *Manager) Status(ctx context.Context, id port.JobID) (port.Job, error) {
	if err := ctx.Err(); err != nil {
		return port.Job{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.byID[id]
	if !ok {
		return port.Job{}, fmt.Errorf("jobproc: 没有任务 %s", id)
	}
	return r.job, nil
}

// Logs 返回任务日志：tail>0 取末尾 N 行；tail<=0 取末尾 logWindow 字节窗口。
// 任务在表内但日志尚未写入（极小窗口）返回空串。
func (m *Manager) Logs(ctx context.Context, id port.JobID, tail int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	r, ok := m.byID[id]
	path := ""
	if ok {
		path = r.logPath
	}
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("jobproc: 没有任务 %s", id)
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("jobproc: 读取 %s 日志: %w", id, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("jobproc: 读取 %s 日志: %w", id, err)
	}
	size := fi.Size()
	if size == 0 {
		return "", nil
	}
	var start int64
	if size > logWindow {
		start = size - logWindow
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("jobproc: 读取 %s 日志: %w", id, err)
	}
	text := string(buf)
	if tail > 0 {
		// 先去掉行尾换行，避免 split 出空尾行挤占 tail 名额。
		lines := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
		if start > 0 && len(lines) > 0 {
			lines = lines[1:] // 窗口起点截断的半行
		}
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		text = strings.Join(lines, "\n")
	}
	if start > 0 {
		text = "…[日志超出窗口，仅读末尾]\n" + text
	}
	return text, nil
}

// Kill 显式终止任务（杀进程树/组）；未知任务或已结束报错。
func (m *Manager) Kill(ctx context.Context, id port.JobID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	r, ok := m.byID[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("jobproc: 没有任务 %s", id)
	}
	if r.job.Status != port.JobRunning {
		m.mu.Unlock()
		return fmt.Errorf("jobproc: 任务 %s 已结束（%s）", id, r.job.Status)
	}
	r.killed = true
	m.mu.Unlock()
	if err := killTree(r.cmd); err != nil {
		return fmt.Errorf("jobproc: 终止 %s: %w", id, err)
	}
	return nil
}

// allocLog 分配任务号并以 O_EXCL 原子占位日志文件：
// 序号跨进程重启从 1 重来时遇到旧日志自动跳号，且并发进程互不撞号。
func (m *Manager) allocLog() (port.JobID, string, *os.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		m.seq++
		id := port.JobID(fmt.Sprintf("j%03d", m.seq))
		p := filepath.Join(m.logDir, string(id)+".log")
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue // 上一进程留下的同号日志：跳号
		}
		if err != nil {
			return "", "", nil, fmt.Errorf("jobproc: 占位日志 %s: %w", p, err)
		}
		return id, p, f, nil
	}
}

// expire Timeout 到点终止（终态记 failed；已结束则不动作）。
func (m *Manager) expire(r *jobRec) {
	m.mu.Lock()
	if r.job.Status != port.JobRunning {
		m.mu.Unlock()
		return
	}
	r.timedOut = true
	m.mu.Unlock()
	_ = killTree(r.cmd)
}

// wait 收割子进程：置终态与退出码、写退出标记行、关闭日志句柄。
// 终态优先级：超时(failed) > 显式 Kill(killed) > 退出码（0=done，非 0=failed）。
func (m *Manager) wait(r *jobRec) {
	err := r.cmd.Wait()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	status := port.JobDone
	if code != 0 {
		status = port.JobFailed
	}
	m.mu.Lock()
	switch {
	case r.timedOut:
		status = port.JobFailed
	case r.killed:
		status = port.JobKilled
	}
	r.job.Status = status
	r.job.ExitCode = code
	r.job.EndedAt = time.Now()
	m.mu.Unlock()
	_, _ = fmt.Fprintf(r.logFile, "[exit code=%d status=%s]\n", code, status)
	_ = r.logFile.Close()
}

// snap 在锁内取任务快照。
func (m *Manager) snap(r *jobRec) port.Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return r.job
}

// applySpec 应用 JobSpec：env 追加（键排序保证确定性）、cwd 缺省家目录
// （DESIGN §5.8：进程 cwd，不是"工作区"）。
func applySpec(cmd *exec.Cmd, spec port.JobSpec) {
	if len(spec.Env) > 0 {
		keys := make([]string, 0, len(spec.Env))
		for k := range spec.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		env := os.Environ()
		for _, k := range keys {
			env = append(env, k+"="+spec.Env[k])
		}
		cmd.Env = env
	}
	dir := spec.WorkDir
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		}
	}
	cmd.Dir = dir
}

// commandLine 日志头的命令行展示（含参数引用）。
func commandLine(spec port.JobSpec) string {
	parts := make([]string, 0, 1+len(spec.Args))
	parts = append(parts, spec.Command)
	for _, a := range spec.Args {
		if strings.ContainsAny(a, " \t\"") {
			parts = append(parts, strconv.Quote(a))
		} else {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}
