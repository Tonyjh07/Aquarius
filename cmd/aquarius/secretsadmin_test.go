package main

// 密钥库管理面装配根侧测试（S9-6b/D161）：uigui.SecretsAdmin 的列出 / 新增覆盖 /
// 删除——经真实 secretbin.Store（临时目录）跑，锁「写库即递增 Revision、掩码只出
// 尾 4 位、删不存在报错、未接线报错」。

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
)

// TestSecretsAdminRoundTrip 列出 / 覆盖 / 删除往返 + Revision 递增。
func TestSecretsAdminRoundTrip(t *testing.T) {
	dir := t.TempDir()
	bin, err := secretbin.New(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	w := &wiring{secretsBin: bin}
	sa := &secretsAdmin{w: w}

	// 空库：无条目、有路径、Revision 0。
	if s := sa.Snapshot(); len(s.Entries) != 0 || s.Path != bin.Path() {
		t.Fatalf("空库快照 = %+v", s)
	}

	if err := sa.Put("AQUARIUS_OPENAI_API_KEY", "sk-abcdefgh-9876"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if w.secretsRev.Load() != 1 {
		t.Fatalf("Put 后 Revision = %d, want 1", w.secretsRev.Load())
	}
	s := sa.Snapshot()
	if len(s.Entries) != 1 || s.Entries[0].Name != "AQUARIUS_OPENAI_API_KEY" ||
		s.Entries[0].Mask != "••••••9876" {
		t.Fatalf("快照 = %+v", s.Entries)
	}
	if strings.Contains(s.Entries[0].Mask, "abcdefgh") {
		t.Fatalf("掩码泄漏中段值：%q", s.Entries[0].Mask)
	}

	// 同名覆盖：条目数不变，Revision 递增。
	if err := sa.Put("AQUARIUS_OPENAI_API_KEY", "sk-zzzzzz-4321"); err != nil {
		t.Fatalf("覆盖: %v", err)
	}
	s = sa.Snapshot()
	if len(s.Entries) != 1 || s.Entries[0].Mask != "••••••4321" {
		t.Fatalf("覆盖后快照 = %+v", s.Entries)
	}
	if w.secretsRev.Load() != 2 {
		t.Fatalf("覆盖后 Revision = %d, want 2", w.secretsRev.Load())
	}

	// 删除：条目消失；不存在再删报错（不静默成功）。
	if err := sa.Delete("AQUARIUS_OPENAI_API_KEY"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if s = sa.Snapshot(); len(s.Entries) != 0 {
		t.Fatalf("删除后快照 = %+v", s.Entries)
	}
	if err := sa.Delete("AQUARIUS_OPENAI_API_KEY"); err == nil {
		t.Fatal("删不存在应报错")
	}
}

// TestSecretsAdminUnwired 未接线（secretsBin = nil）：写/删报错、快照为空不 panic。
func TestSecretsAdminUnwired(t *testing.T) {
	sa := &secretsAdmin{w: &wiring{}}
	if s := sa.Snapshot(); len(s.Entries) != 0 || s.Path != "" {
		t.Fatalf("未接线快照 = %+v", s)
	}
	if err := sa.Put("K", "v"); err == nil {
		t.Fatal("未接线 Put 应报错")
	}
	if err := sa.Delete("K"); err == nil {
		t.Fatal("未接线 Delete 应报错")
	}
}

// TestSecretsAdminPutSurfacesStoreErrors 库自有校验（空值）透传：空值 → 报错、Revision 不动。
func TestSecretsAdminPutSurfacesStoreErrors(t *testing.T) {
	dir := t.TempDir()
	bin, err := secretbin.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	w := &wiring{secretsBin: bin}
	sa := &secretsAdmin{w: w}
	if err := sa.Put("K", "   "); err == nil {
		t.Fatal("空值应报错（清一条请用删除）")
	}
	if w.secretsRev.Load() != 0 {
		t.Fatalf("失败不应递增 Revision，实为 %d", w.secretsRev.Load())
	}
}

// TestSecretsAdminDeleteNotFoundWrapsErrNotFound 删不存在时报错，文案含名字（不静默成功）。
func TestSecretsAdminDeleteNotFoundWrapsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	bin, err := secretbin.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	sa := &secretsAdmin{w: &wiring{secretsBin: bin}}
	if err := sa.Delete("NOPE"); err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("删不存在 = %v, want 含名字", err)
	}
}
