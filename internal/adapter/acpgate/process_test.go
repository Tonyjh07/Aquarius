package acpgate

import (
	"io"
	"strings"
	"testing"
	"time"
)

// TestWaitStderrDrainsBeforeReport exec 把 cmd.Stderr 的管道内容交给独立 goroutine 拷贝，
// stdout 到 EOF 不代表这截 stderr 已经写进 tailBuffer。abort 在读尾巴前要等 cmd.Wait，
// 否则满载下会报出不带 stderr 的空报因（TestDelegateCrash 的偶发失败）。
func TestWaitStderrDrainsBeforeReport(t *testing.T) {
	t.Parallel()
	proc, err := startProcess(fakeAgent, nil, "", map[string]string{"FAKE_AGENT_MODE": "crash"})
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}
	defer proc.terminate(300 * time.Millisecond)

	_, _ = io.WriteString(proc.stdin,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`+"\n")
	_, _ = io.ReadAll(proc.stdout) // 对端退出后 stdout 到 EOF，等价于 conn.Done()

	proc.waitStderr(2 * time.Second)
	select {
	case <-proc.wait():
	default:
		t.Fatal("waitStderr returned before cmd.Wait drained the stderr copy")
	}
	if tail := proc.stderrTail(); !strings.Contains(tail, "boom on purpose") {
		t.Fatalf("stderr tail = %q, want the crash marker", tail)
	}
}

// TestProcessExitedSeesTheCrash 崩溃报因不再只依赖 watchdog 的标志：进程是否已经结束本身
// 就是「该等 stderr」的实锤。abort 两条都看，这里锁住 exited() 的行为——刚起来是 false，
// 对端崩溃后转为 true。watchdog 与 Prompt 抢同一个 Done() 时，这一条决定报因有没有线索。
func TestProcessExitedSeesTheCrash(t *testing.T) {
	t.Parallel()
	proc, err := startProcess(fakeAgent, nil, "", map[string]string{"FAKE_AGENT_MODE": "crash"})
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}
	defer proc.terminate(300 * time.Millisecond)
	if proc.exited() {
		t.Fatal("刚启动的进程不该已经退出")
	}
	_, _ = io.WriteString(proc.stdin,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`+"\n")
	_, _ = io.ReadAll(proc.stdout) // 崩溃后 stdout 到 EOF
	deadline := time.Now().Add(2 * time.Second)
	for !proc.exited() {
		if time.Now().After(deadline) {
			t.Fatal("崩溃后 2 秒内 exited() 仍未转为 true")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
