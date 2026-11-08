package acpgate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestTestyCallbacks 手工验证（**不进门禁**）：把官方 Rust SDK 的测试代理
// （`agent-client-protocol-test`，俗称 testy）当外部代理跑一遍 `callbacks` 场景。
//
// 它按固定顺序发反向请求：`session/request_permission` → `fs/write_text_file` →
// `fs/read_text_file` → 五个 `terminal/*` → 最后是 `elicitation/*`。本版申报了 fs 与
// terminal、没申报 elicitation，所以期望是：
//
//   - 权限反问走确认器，fs 两个方法真的走通（写下去的字节与它发的一致）；
//   - `terminal/*` 五个都走通：命令真的跑起来、能取到输出与退出码、`kill` 之后仍能取、
//     `release` 之后 id 失效（A3）；
//   - 末尾 `elicitation/*` 因未申报被它自己判为 -32602，`session/prompt` 以这个错误收尾
//     ——**这是确定性的预期结果**，不是我们的 bug。
//
// 跑法：`AQUARIUS_TESTY=<testy 可执行文件> go test ./internal/adapter/acpgate -run TestTesty -v`
//
// 不进门禁的原因与跑法见 development.md「外部代理的协议验证（testy）」。
func TestTestyCallbacks(t *testing.T) {
	bin := os.Getenv("AQUARIUS_TESTY")
	if bin == "" {
		t.Skip("set AQUARIUS_TESTY to the agent-client-protocol-test binary")
	}
	work := t.TempDir()
	conf := &fakeConfirmer{allow: true}
	m := New(Options{
		Agents:    []Agent{{Name: "testy", Command: bin, Enabled: true}},
		Home:      work,
		Confirmer: conf,
		Grace:     2 * time.Second,
		Handshake: 30 * time.Second,
		Now:       time.Now,
	})
	t.Cleanup(m.Close)

	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "callbacks"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err == nil {
		t.Fatal("这一版不申报 elicitation，testy 的 callbacks 末尾必以 -32602 收尾；没有错误说明行为变了")
	}
	if !strings.Contains(err.Error(), "elicitation") {
		t.Fatalf("失败原因应是未申报 elicitation，got: %v", err)
	}

	// 权限反问走过确认器（callbacks 的第一步），并已按「允许」应答。之后每条命令各问一次，
	// 所以次数 = 1 + 建过的终端数。
	if want := 1 + len(run.Commands); conf.calls != want {
		t.Fatalf("conf.calls = %d, want %d（1 次权限反问 + %d 条命令）", conf.calls, want, len(run.Commands))
	}
	// 写经我们过手：落地在 cwd 下，字节与它发的一模一样（不做换行转换），并记进 Wrote。
	want := filepath.Join(mustResolvePath(t, work), "testy-write.txt")
	if len(run.Wrote) != 1 || !strings.EqualFold(run.Wrote[0], want) {
		t.Fatalf("wrote = %v, want [%s]", run.Wrote, want)
	}
	b, readErr := os.ReadFile(want)
	if readErr != nil {
		t.Fatalf("读回写下的文件: %v", readErr)
	}
	if string(b) != "written by testy\n" {
		t.Fatalf("落盘内容 = %q, want %q", string(b), "written by testy\n")
	}

	// 五个 terminal/* 都走通了：testy 在 callbacks 里至少建一条命令，且每条都留下了结局。
	// 不按它的脚本文本断言具体命令——那是它的实现细节，我们只保证协议面成立。
	if len(run.Commands) == 0 {
		t.Fatalf("terminal/* 没有一条命令走通（notes = %v）", run.Notes)
	}
	for _, cmd := range run.Commands {
		if cmd.ExitCode == nil && cmd.Signal == "" {
			t.Fatalf("命令没有结局（%q）：output 与 wait_for_exit 至少一条没走通", cmd.Line)
		}
	}
	if !strings.Contains(port.DelegateReportText(task.ID, run), "Commands run through this session: ") {
		t.Fatalf("报告缺少命令清单：\n%s", port.DelegateReportText(task.ID, run))
	}
	t.Logf("stop=%s toolCalls=%d wrote=%v commands=%+v notes=%v",
		run.StopReason, run.ToolCalls, run.Wrote, run.Commands, run.Notes)
}
