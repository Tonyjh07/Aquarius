package acpgate

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// environ 父进程环境（测试可覆盖的口子留在装配根，不在这里）。
func environ() []string { return os.Environ() }

// process 一个正在跑的外部代理进程：stdin/stdout 接 ACP 连接，stderr 收进有界缓冲。
type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *tailBuffer

	mu   sync.Mutex
	done bool
}

// startProcess 起进程并接好三根管道（§5.18 进程卫生：环境剔除 AQUARIUS_*、
// CREATE_NO_WINDOW / 独立进程组由 setupProc 按平台设置）。
func startProcess(command string, args []string, wd string, env map[string]string) (*process, error) {
	cmd := exec.Command(command, args...)
	if wd != "" {
		cmd.Dir = wd
	}
	cmd.Env = childEnv(env)
	setupProc(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tb := newTailBuffer(stderrCap)
	cmd.Stderr = tb
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &process{cmd: cmd, stdin: stdin, stdout: stdout, stderr: tb}, nil
}

// kill 杀进程组（连带它自己拉起的子进程）。
func (p *process) kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return killTree(p.cmd)
}

// terminate 收尾：先温和地关 stdin（多数代理见到 EOF 会自己退出），再杀进程组，
// 最后有界地等它被回收——不无限期挂在 Wait 上。
func (p *process) terminate(grace time.Duration) {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.stdin.Close()
	done := make(chan struct{})
	go func() {
		_ = p.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(200 * time.Millisecond):
	}
	_ = p.kill()
	select {
	case <-done:
	case <-time.After(grace + time.Second):
	}
}

// stderrTail stderr 缓冲内容（报因用；已去掉尾部空白与空行）。
func (p *process) stderrTail() string {
	if p == nil || p.stderr == nil {
		return ""
	}
	return strings.TrimSpace(p.stderr.String())
}

// tailBuffer 有界环形缓冲（保留最后 max 字节）：代理崩溃时 stderr 可能刷很长，
// 我们只要末尾这段来说明原因。
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
