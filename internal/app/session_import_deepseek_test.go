package app

// session_import_deepseek_test.go /import 的 deepseek 整库分支（D321）端到端：清单先行
// （不带 --all 只列不动库）、--all 全导并切到第一个、sidecar 去重（重导 0 新建）、
// updated_at 变了导成新会话、--no-files、--pick 越界。借用真 exchange 适配器 + 真 zip
// （端到端验收允许，生产仍走端口）。

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/exchange"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// dsAppFixture 拼一份两个会话的 deepseek 包（一个带图片附件），落进临时目录；
// s2Updated 控制问答会话在清单里的 updated_at（去重测试用它制造「源里变了」）。
func dsAppFixture(t *testing.T, s2Updated string) (path string, png []byte) {
	t.Helper()
	png = []byte("\x89PNG\r\n\x1a\nfakepng")
	type frag map[string]any
	s1 := map[string]any{
		"id": "aaaa", "title": "带图会话", "current_message_id": 3,
		"inserted_at": "2026-11-17T09:00:00Z", "updated_at": "2026-11-17T10:00:00Z",
		"messages": []any{
			map[string]any{"id": 1, "parent_id": nil, "role": "USER", "status": "FINISHED",
				"fragments": []any{frag{"type": "REQUEST", "content": "看图"}}},
			map[string]any{"id": 2, "parent_id": nil, "role": "ASSISTANT", "status": "FINISHED",
				"fragments": []any{frag{"type": "FILE", "files": []any{
					map[string]any{"id": "f1", "file_name": "a.png", "file_size": len(png),
						"export_path": "chat_sessions/aaaa_files/f1_a.png", "is_image": true},
				}}}},
			map[string]any{"id": 3, "parent_id": 2, "role": "ASSISTANT", "status": "FINISHED",
				"fragments": []any{frag{"type": "RESPONSE", "content": "好看"}}},
		},
	}
	s2 := map[string]any{
		"id": "bbbb", "title": "问答会话", "current_message_id": 2,
		"inserted_at": "2026-11-18T09:00:00Z", "updated_at": s2Updated,
		"messages": []any{
			map[string]any{"id": 1, "role": "USER", "status": "FINISHED",
				"fragments": []any{frag{"type": "REQUEST", "content": "你好"}}},
			map[string]any{"id": 2, "parent_id": 1, "role": "ASSISTANT", "status": "FINISHED",
				"fragments": []any{frag{"type": "RESPONSE", "content": "你好！"}}},
		},
	}
	mustJSON := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}
	idx := []map[string]any{
		{"id": "aaaa", "title": "带图会话", "message_count": 3, "updated_at": "2026-11-17T10:00:00Z"},
		{"id": "bbbb", "title": "问答会话", "message_count": 2, "updated_at": s2Updated},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string][]byte{
		"manifest.json":                     []byte(`{"export_format_version":3}`),
		"chat_session_index.json":           mustJSON(idx),
		"chat_sessions/aaaa.json":           mustJSON(s1),
		"chat_sessions/bbbb.json":           mustJSON(s2),
		"chat_sessions/aaaa_files/f1_a.png": png,
	} {
		if err := zwAdd(zw, name, body); err != nil {
			t.Fatalf("zip %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	path = filepath.Join(t.TempDir(), "deepseek_data.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path, png
}

func zwAdd(zw *zip.Writer, name string, body []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// dsAppSession 带真 exchange 适配器、blobfs 与 sidecar 目录的会话。
func dsAppSession(t *testing.T) (*Session, *memStore) {
	t.Helper()
	dir := t.TempDir()
	rec := &recorder{}
	llm := &scriptLLM{t: t}
	agent := newAgent(t, llm, rec, Deps{}, Config{})
	blobs, err := blobfs.New(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	s, err := NewSession(context.Background(), SessionDeps{
		Store:            newMemStore(),
		Agent:            agent,
		IDs:              &seqIDs{},
		Clock:            fixedClock{testTime},
		UI:               rec,
		Exporter:         exchange.New(exchange.Options{Blobs: blobs}),
		Importer:         exchange.New(exchange.Options{Blobs: blobs}),
		ImportSourcesDir: filepath.Join(dir, "import-sources"),
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, s.store.(*memStore)
}

// TestAppImportDeepSeekListing 不带 --all/--pick 只列清单，一个会话都不进库。
func TestAppImportDeepSeekListing(t *testing.T) {
	s, _ := dsAppSession(t)
	path, _ := dsAppFixture(t, "2026-11-18T09:30:00Z")
	out, err := handleCmd(s, "import", path)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "带图会话") || !strings.Contains(out, "问答会话") {
		t.Fatalf("out = %q, want 清单", out)
	}
	if n := len(dsList(t, s)); n != 1 { // 只有初始空会话
		t.Fatalf("库里会话 = %d, want 清单不动库", n)
	}
}

// TestAppImportDeepSeekAllAndDedup --all 全导并切换；同一份重导 0 新建全跳过；
// 源里 updated_at 变了的那份导成新会话。
func TestAppImportDeepSeekAllAndDedup(t *testing.T) {
	s, _ := dsAppSession(t)
	path, _ := dsAppFixture(t, "2026-11-18T09:30:00Z")
	out, err := handleCmd(s, "import", "--all", path)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, i18n.Tf("exchange.import.multi", "convs", 2, "nodes", 5)) {
		t.Fatalf("out = %q, want 多会话回执", out)
	}
	if n := len(dsList(t, s)); n != 3 { // 初始 + 2 导入
		t.Fatalf("库里会话 = %d, want 3", n)
	}
	if s.cur.Title != "问答会话" {
		t.Fatalf("cur = %q, want 切到清单第一个（updated_at 倒序，最新的在前）", s.cur.Title)
	}
	// 重导：全跳过，0 新建。
	out, err = handleCmd(s, "import", "--all", path)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if !strings.Contains(out, dsHead("exchange.import.all_skipped")) {
		t.Fatalf("out = %q, want 全跳过回执", out)
	}
	if len(dsList(t, s)) != 3 {
		t.Fatalf("重导新建了会话")
	}
	// sidecar 已写。
	if _, err := os.Stat(filepath.Join(s.importSourcesDir, "deepseek.json")); err != nil {
		t.Fatalf("sidecar 不在场: %v", err)
	}
	// 源里改一个 updated_at：那份导成新会话，另一份仍跳过。
	path2, _ := dsAppFixture(t, "2026-11-19T00:00:00Z")
	out, err = handleCmd(s, "import", "--all", path2)
	if err != nil {
		t.Fatalf("re-import changed: %v", err)
	}
	if !strings.Contains(out, dsHead("exchange.import.skipped")) {
		t.Fatalf("out = %q, want 跳过行", out)
	}
	if len(dsList(t, s)) != 4 {
		t.Fatalf("库里会话 = %d, want 4（变的导成新会话）", len(dsList(t, s)))
	}
}

// dsList 库里的会话数（快照 List）。
func dsList(t *testing.T, s *Session) []port.ConversationSummary {
	t.Helper()
	sums, err := s.store.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return sums
}

// TestAppImportDeepSeekNoFiles --no-files：图片不入库，回执计丢弃。
func TestAppImportDeepSeekNoFiles(t *testing.T) {
	s, _ := dsAppSession(t)
	path, _ := dsAppFixture(t, "2026-11-18T09:30:00Z")
	out, err := handleCmd(s, "import", "--all", "--no-files", path)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, i18n.T("exchange.io.drop.ds_file_no_bytes")) {
		t.Fatalf("out = %q, want 附件丢弃行", out)
	}
}

// TestAppImportDeepSeekFilter --filter 按标题/源 ID 过滤（D322）：只带 --filter 先列过滤
// 后的清单；--filter --all 只导过滤后的集合；--pick 点到被滤掉的序号报越界（清单序 1 = 问答会话，被「带图」滤掉）。
func TestAppImportDeepSeekFilter(t *testing.T) {
	s, _ := dsAppSession(t)
	path, _ := dsAppFixture(t, "2026-11-18T09:30:00Z")

	// 只带 --filter：先看清单，不导入（库里还是只有 persona 会话）。
	out, err := handleCmd(s, "import", "--filter", "带图", path)
	if err != nil {
		t.Fatalf("import --filter listing: %v", err)
	}
	if !strings.Contains(out, "带图会话") || strings.Contains(out, "问答会话") {
		t.Fatalf("listing = %q, want 只列带图会话", out)
	}
	if len(dsList(t, s)) != 1 {
		t.Fatalf("只列清单却导了 %d 个会话", len(dsList(t, s))-1)
	}

	// --filter --all：只导过滤后的集合。
	out, err = handleCmd(s, "import", "--filter", "带图", "--all", path)
	if err != nil {
		t.Fatalf("import --filter --all: %v", err)
	}
	if len(dsList(t, s)) != 2 {
		t.Fatalf("库里会话 = %d, want 2（只导带图那一个）", len(dsList(t, s)))
	}

	// --pick 点到被滤掉的序号：报越界（序号是原清单序号，1 = 问答会话，被「带图」滤掉）。
	if _, err := handleCmd(s, "import", "--filter", "带图", "--pick", "1", path); err == nil {
		t.Fatal("--pick 指到被滤掉的序号却不报错")
	}
}

// dsHead 文案里第一个占位符之前的那截（宽断言跟着 key 走）。
func dsHead(key string) string {
	t := i18n.T(key)
	if i := strings.Index(t, "{"); i >= 0 {
		return t[:i]
	}
	return t
}
