package acpgate

// 转交台账（D269）：派活记一条、终态标状态、报告落地才销账、读即清。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ledgerManager 带台账的管理器（台账落在测试自己的临时目录）。
func ledgerManager(t *testing.T, mode string) (*Manager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs", "delegates.json")
	m := newManager(t, mode, func(o *Options) {
		o.Home = t.TempDir()
		o.LedgerPath = path
	})
	return m, path
}

// waitDone 等一次转交进终态（Start 是异步的）。
func waitDone(t *testing.T, m *Manager, id port.DelegateID) {
	t.Helper()
	if _, err := m.Wait(context.Background(), id); err != nil {
		// 报错也算终态：失败报告照样要写台账。
		t.Logf("wait: %v", err)
	}
}

// TestLedgerWritesAndSettles 正常跑完：条目先有、终态有停因、Settle 之后才消失。
func TestLedgerWritesAndSettles(t *testing.T) {
	m, path := ledgerManager(t, "ok")
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{
		Task: "do the thing", WorkDir: t.TempDir(), Roots: []string{"/srv/x"}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Start 返回时条目已在盘上（先落台账再起进程）。
	d := readDoc(t, path)
	if len(d.Entries) != 1 || d.Entries[0].ID != task.ID {
		t.Fatalf("start 后台账 = %+v, want 一条 %s", d.Entries, task.ID)
	}
	if d.Entries[0].Agent != "fake" || d.Entries[0].Session != "c1" {
		t.Fatalf("条目缺 agent/session: %+v", d.Entries[0])
	}
	if !strings.Contains(d.Entries[0].Task, "do the thing") {
		t.Fatalf("条目缺任务描述: %q", d.Entries[0].Task)
	}
	// 范围要留痕：读台账的人看得出这活当时被允许碰哪些额外位置。
	if len(d.Entries[0].Roots) != 1 || d.Entries[0].Roots[0] != "/srv/x" {
		t.Fatalf("条目缺根集: %+v", d.Entries[0])
	}

	waitDone(t, m, task.ID)
	d = readDoc(t, path)
	if len(d.Entries) != 1 {
		t.Fatalf("终态后条目应仍在（报告还没落地），得到 %+v", d.Entries)
	}
	if d.Entries[0].EndedAt.IsZero() || d.Entries[0].PID == 0 {
		t.Fatalf("终态条目缺 ended_at/pid: %+v", d.Entries[0])
	}

	// 报告落地 → 销账。
	if err := m.Settle(context.Background(), task.ID); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if d = readDoc(t, path); len(d.Entries) != 0 {
		t.Fatalf("销账后条目 = %+v, want 空", d.Entries)
	}
}

// TestLedgerUnfinishedReadsAndClears 上次没销账的条目：读一次拿到、再读为空（不重报）。
func TestLedgerUnfinishedReadsAndClears(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "delegates.json")
	writeFixture(t, path, `{
  "version": 1,
  "entries": [
    {"id":"d001","agent":"fake","session":"c1","task":"half done","started_at":"2026-11-01T10:00:00Z","pid":4242},
    {"id":"d002","agent":"fake","session":"c2","task":"finished late","started_at":"2026-11-01T10:05:00Z","stop":"end_turn","ended_at":"2026-11-01T10:06:00Z"}
  ]
}`)
	m := New(Options{LedgerPath: path})
	items, err := m.Unfinished(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("unfinished = %+v, err = %v", items, err)
	}
	if items[0].ID != "d001" || items[0].Session != "c1" || items[0].Task != "half done" {
		t.Fatalf("第一条 = %+v", items[0])
	}
	if !items[0].EndedAt.IsZero() {
		t.Fatalf("在跑的那条不该有 ended_at: %+v", items[0])
	}
	if items[1].Stop != "end_turn" || items[1].EndedAt.IsZero() {
		t.Fatalf("跑完但没送报告的那条应带停因: %+v", items[1])
	}
	if again, err := m.Unfinished(context.Background()); err != nil || len(again) != 0 {
		t.Fatalf("读即清：第二次 = %+v, err = %v", again, err)
	}
}

// TestLedgerKilledKeepsStop 叫停之后条目带 stop=killed（销账由调用方做：命令已交回结果）。
func TestLedgerKilledKeepsStop(t *testing.T) {
	m, path := ledgerManager(t, "slow")
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "long one"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := m.Kill(context.Background(), task.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	d := readDoc(t, path)
	if len(d.Entries) != 1 || d.Entries[0].Stop != "killed" {
		t.Fatalf("叫停后台账 = %+v, want stop=killed", d.Entries)
	}
}

// TestLedgerBadFileDoesNotBlock 坏台账不挡派活，也不挡对账。
func TestLedgerBadFileDoesNotBlock(t *testing.T) {
	m, path := ledgerManager(t, "ok")
	writeFixture(t, path, "{not json")
	if _, err := m.Unfinished(context.Background()); err != nil {
		t.Fatalf("坏台账应静默当空: %v", err)
	}
	task, err := m.Start(context.Background(), port.DelegateSpec{Task: "x"})
	if err != nil {
		t.Fatalf("坏台账不该挡派活: %v", err)
	}
	waitDone(t, m, task.ID)
	if d := readDoc(t, path); len(d.Entries) != 1 || d.Entries[0].ID != task.ID {
		t.Fatalf("重建后的台账 = %+v", d.Entries)
	}
}

// TestLedgerDisabledWithoutPath 没给路径 = 不落盘（测试与嵌入式装配的缺省）。
func TestLedgerDisabledWithoutPath(t *testing.T) {
	m := newManager(t, "ok", func(o *Options) { o.Home = t.TempDir() })
	task, err := m.Start(context.Background(), port.DelegateSpec{Task: "x"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitDone(t, m, task.ID)
	if items, err := m.Unfinished(context.Background()); err != nil || len(items) != 0 {
		t.Fatalf("未配台账却在读数里出现: %+v, err = %v", items, err)
	}
}

func readDoc(t *testing.T, path string) delegateLedgerDoc {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return delegateLedgerDoc{}
		}
		t.Fatalf("读台账: %v", err)
	}
	m := New(Options{LedgerPath: path})
	d, err := m.readLedger()
	if err != nil {
		t.Fatalf("解析台账 %s: %v", data, err)
	}
	return d
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写夹具: %v", err)
	}
}

// TestStartRejectsRelativeRoot 根集在入口归一：相对路径直接拒（它会让越界判定失去参照物）。
func TestStartRejectsRelativeRoot(t *testing.T) {
	m, _ := ledgerManager(t, "ok")
	if _, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "x", Roots: []string{"rel"}}); err == nil {
		t.Fatal("相对根应报错")
	}
	// 归一后重复项进台账只剩一条（去空白、去重是入口的事，不是调用方的事）。
	if _, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{
		Task: "x", Roots: []string{" /srv/y ", "/srv/y"}}); err != nil {
		t.Fatalf("start: %v", err)
	}
}
