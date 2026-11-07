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
// `fs/read_text_file` → 五个 `terminal/*` → 最后是 `elicitation/*`。本版申报了 fs、
// 没申报 terminal 与 elicitation，所以期望是：
//
//   - 权限反问走确认器，fs 两个方法真的走通（写下去的字节与它发的一致）；
//   - `terminal/*` 五个一律 -32601（testy 记进它自己的报告，不影响本次转交）；
//   - 末尾 `elicitation/*` 因未申报被它自己判为 -32602，`session/prompt` 以这个错误收尾
//     ——**这是确定性的预期结果**，不是我们的 bug。
//
// 跑法：`AQUARIUS_TESTY=<testy 可执行文件> go test ./internal/adapter/acpgate -run TestTesty -v`
//
// 不进门禁的原因：要 cargo 构建、二进制在 temp 目录里会被清掉（见 plan.md「验证样本」）。
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

	// 权限反问走过确认器（callbacks 的第一步），并已按「允许」应答。
	if conf.calls != 1 {
		t.Fatalf("conf.calls = %d, want 1（session/request_permission）", conf.calls)
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
	t.Logf("stop=%s toolCalls=%d wrote=%v notes=%v", run.StopReason, run.ToolCalls, run.Wrote, run.Notes)
}
