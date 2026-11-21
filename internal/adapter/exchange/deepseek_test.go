package exchange

// deepseek_test.go DeepSeek 整库导入（D321）的形状锁定：路径级识别（中央目录、不撞 aqx）、
// 清单倒序、版本组挂 Root、片段映射（THINK / TEMPLATE_RESPONSE / 搜索计数 / 横幅）、
// 附件入库与非图片丢弃、status → Outcome、空节点接最近祖先、Head 取 current_message_id。

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// dsFixtureZip 拼一份 deepseek 形状的包（条目名与真实导出一致）。
func dsFixtureZip(t *testing.T, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deepseek_data.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		if err := writeZipEntry(zw, name, func(w io.Writer) error {
			_, err := w.Write(body)
			return err
		}); err != nil {
			t.Fatalf("zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	return path
}

// dsIndexJSON / dsSessionJSON 测试数据的序列化捷径。
func dsIndexJSON(entries []dsIndexEntry) []byte {
	b, _ := json.Marshal(entries)
	return b
}

func dsSessionJSON(s dsSession) []byte {
	b, _ := json.Marshal(s)
	return b
}

func dsFrag(typ, content string) dsFragment { return dsFragment{Type: typ, Content: content} }

// TestDetectPathDeepSeek 路径级识别：manifest 版本号 + chat_sessions/ 同时在场才认；
// aqx 的包（manifest 是 format/version、条目是 conversations/）不认，仍走 Detect 的 aqx。
func TestDetectPathDeepSeek(t *testing.T) {
	ex := New(Options{})
	ds := dsFixtureZip(t, map[string][]byte{
		"manifest.json":           []byte(`{"export_format_version":3}`),
		"chat_sessions/aaaa.json": []byte(`{}`),
		"chat_session_index.json": []byte(`[]`),
	})
	f, ok := ex.DetectPath(ds)
	if !ok || f.Name != "deepseek" {
		t.Fatalf("DetectPath = %v, %v, want deepseek", f, ok)
	}
	aqxPath := dsFixtureZip(t, map[string][]byte{
		"manifest.json":             []byte(`{"format":"aqx","version":1,"conversations":[]}`),
		"conversations/1/tree.json": []byte(`{}`),
	})
	if f, ok := ex.DetectPath(aqxPath); ok {
		t.Fatalf("aqx 包被认成 %s", f.Name)
	}
	// 无 chat_sessions 条目的 deepseek 形状不认（光有版本号不够）。
	only := dsFixtureZip(t, map[string][]byte{"manifest.json": []byte(`{"export_format_version":3}`)})
	if _, ok := ex.DetectPath(only); ok {
		t.Fatal("缺 chat_sessions 条目不该认")
	}
}

// TestDeepSeekIndex 清单按 updated_at 倒序、序号 1 起重排。
func TestDeepSeekIndex(t *testing.T) {
	path := dsFixtureZip(t, map[string][]byte{
		"manifest.json": []byte(`{"export_format_version":3}`),
		"chat_session_index.json": dsIndexJSON([]dsIndexEntry{
			{ID: "old", Title: "早的", MessageCount: 2, UpdatedAt: "2026-11-10T00:00:00Z"},
			{ID: "new", Title: "晚的", MessageCount: 5, UpdatedAt: "2026-11-20T00:00:00Z"},
		}),
	})
	entries, err := New(Options{}).Index(context.Background(), path)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(entries) != 2 || entries[0].ID != "new" || entries[1].ID != "old" {
		t.Fatalf("entries = %+v, want new 在前", entries)
	}
	if entries[0].Index != 1 || entries[1].Index != 2 || entries[0].Nodes != 5 {
		t.Fatalf("序号/条数不对: %+v", entries)
	}
	if entries[0].UpdatedAt.IsZero() {
		t.Fatal("条目该带 UpdatedAt（重导去重按它判断）")
	}
}

// TestDeepSeekImportTree 主用例：版本组、THINK / TEMPLATE_RESPONSE、搜索计数、Outcome。
func TestDeepSeekImportTree(t *testing.T) {
	older := 1
	s := dsSession{
		ID: "aaaa", Title: "版本组", CurrentMessage: 3,
		InsertedAt: "2026-11-17T09:00:00Z", UpdatedAt: "2026-11-17T10:00:00Z",
		Messages: []dsMessage{
			{ID: 1, Role: "USER", Status: "FINISHED", Fragments: []dsFragment{dsFrag("REQUEST", "第一版")}},
			{ID: 2, Role: "USER", Status: "FINISHED", Fragments: []dsFragment{dsFrag("REQUEST", "第二版")}},
			{ID: 3, ParentID: &older, Role: "ASSISTANT", Status: "FINISHED",
				InsertedAt: "2026-11-17T09:59:59.997Z",
				Fragments: []dsFragment{
					{Type: "THINK", Content: "想想"},
					{Type: "CONTENT_RISK"},
					{Type: "TOOL_SEARCH"}, {Type: "TOOL_FIND"},
					{Type: "TOOL_OPEN"}, {Type: "READ_LINK"},
					{Type: "TEMPLATE_RESPONSE", Content: "固定话术"},
				}},
		},
	}
	path := dsFixtureZip(t, map[string][]byte{
		"manifest.json":           []byte(`{"export_format_version":3}`),
		"chat_session_index.json": dsIndexJSON([]dsIndexEntry{{ID: "aaaa", Title: "版本组", MessageCount: 3, UpdatedAt: s.UpdatedAt}}),
		"chat_sessions/aaaa.json": dsSessionJSON(s),
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	c, rep, err := importOne(context.Background(), New(Options{}), bytes.NewReader(data),
		port.ExchangeFormat{Name: "deepseek"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	snap := c.Snapshot()
	headMsg := dsFindByID(snap, snap.Head)
	if snap.Title != "版本组" || nodeCount(c) != 3 {
		t.Fatalf("title = %q, nodes = %d", snap.Title, nodeCount(c))
	}
	// 两个根都是 Root 的孩子（版本组做兄弟）；Head 是 current_message_id 对应的新节点。
	roots := 0
	for _, e := range snap.Edges {
		if e.Parent == snap.Nodes[0].ID { // Root 的 ID = 会话 ID
			roots += len(e.Childs)
		}
	}
	if roots != 2 {
		t.Fatalf("Root 孩子 = %d, want 2（版本组挂 Root）", roots)
	}
	if headMsg.Role != conversation.RoleAssistant {
		t.Fatalf("head role = %s, want assistant", headMsg.Role)
	}
	if headMsg.Outcome != conversation.OutcomeDone {
		t.Fatalf("outcome = %q, want done", headMsg.Outcome)
	}
	var hasThink, hasTemplate bool
	for _, p := range headMsg.Content {
		switch p.Kind {
		case conversation.PartThinking:
			hasThink = true
		case conversation.PartText:
			if p.Text == "固定话术" {
				hasTemplate = true
			}
		}
	}
	if !hasThink || !hasTemplate {
		t.Fatalf("分片缺失: think=%v template=%v", hasThink, hasTemplate)
	}
	// 搜索与读页计数进 Counts（分块导入靠它合并），横幅也计数。
	if rep.Counts == nil ||
		rep.Counts[i18n.T("exchange.io.drop.ds_searches")] != 2 ||
		rep.Counts[i18n.T("exchange.io.drop.ds_page_reads")] != 2 ||
		rep.Counts[i18n.T("exchange.io.drop.ds_risk_banner")] != 1 {
		t.Fatalf("counts = %+v", rep.Counts)
	}
	if !strings.Contains(strings.Join(rep.Dropped, "\n"), i18n.Tf("exchange.io.dropped.line",
		"kind", i18n.T("exchange.io.drop.ds_searches"), "count", 2)) {
		t.Fatalf("dropped 缺搜索计数行: %v", rep.Dropped)
	}
}

// TestDeepSeekStatusMapping status 四态映射（只看 assistant 节点）。
func TestDeepSeekStatusMapping(t *testing.T) {
	cases := map[string]conversation.Outcome{
		"FINISHED":       conversation.OutcomeDone,
		"INCOMPLETE":     conversation.OutcomeCancelled,
		"TIMEOUT":        conversation.OutcomeError,
		"CONTENT_FILTER": conversation.OutcomeError,
		"OTHER":          "",
	}
	for status, want := range cases {
		older := 1
		s := dsSession{
			ID: "aaaa", Title: "t", CurrentMessage: 2,
			Messages: []dsMessage{
				{ID: 1, Role: "USER", Status: "FINISHED", Fragments: []dsFragment{dsFrag("REQUEST", "q")}},
				{ID: 2, ParentID: &older, Role: "ASSISTANT", Status: status, Fragments: []dsFragment{dsFrag("RESPONSE", "a")}},
			},
		}
		path := dsFixtureZip(t, map[string][]byte{
			"manifest.json":           []byte(`{"export_format_version":3}`),
			"chat_session_index.json": dsIndexJSON([]dsIndexEntry{{ID: "aaaa", Title: "t", MessageCount: 2, UpdatedAt: "2026-11-17T10:00:00Z"}}),
			"chat_sessions/aaaa.json": dsSessionJSON(s),
		})
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		cs, _, err := New(Options{}).Import(context.Background(), f,
			port.ExchangeFormat{Name: "deepseek"}, nil, port.ImportOptions{})
		f.Close()
		if err != nil {
			t.Fatalf("%s: import: %v", status, err)
		}
		snap := cs[0].Snapshot()
		var got conversation.Outcome
		for _, m := range snap.Nodes {
			if m.Role == conversation.RoleAssistant {
				got = m.Outcome
			}
		}
		if got != want {
			t.Fatalf("%s: outcome = %q, want %q", status, got, want)
		}
	}
}

// TestDeepSeekFiles 图片附件入库（内容寻址），非图片计丢弃；--no-files 全丢。
func TestDeepSeekFiles(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfakepng")
	older := 1
	s := dsSession{
		ID: "aaaa", Title: "图", CurrentMessage: 2,
		Messages: []dsMessage{
			{ID: 1, Role: "USER", Status: "FINISHED", Fragments: []dsFragment{dsFrag("REQUEST", "看图")}},
			{ID: 2, ParentID: &older, Role: "ASSISTANT", Status: "FINISHED", Fragments: []dsFragment{
				{Type: "FILE", Files: []dsFile{
					{ID: "f1", FileName: "a.png", ExportPath: "chat_sessions/aaaa_files/f1_a.png", IsImage: true},
					{ID: "f2", FileName: "b.java", IsImage: false},
				}},
			}},
		},
	}
	files := map[string][]byte{
		"manifest.json":                     []byte(`{"export_format_version":3}`),
		"chat_session_index.json":           dsIndexJSON([]dsIndexEntry{{ID: "aaaa", Title: "图", MessageCount: 2, UpdatedAt: "2026-11-17T10:00:00Z"}}),
		"chat_sessions/aaaa.json":           dsSessionJSON(s),
		"chat_sessions/aaaa_files/f1_a.png": png,
	}
	blobs, err := blobfs.New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	im := New(Options{Blobs: blobs})
	path := dsFixtureZip(t, files)
	data, _ := os.ReadFile(path)
	c, rep, err := importOne(context.Background(), im, bytes.NewReader(data), port.ExchangeFormat{Name: "deepseek"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	snap := c.Snapshot()
	refs := 0
	for _, m := range snap.Nodes {
		for _, p := range m.Content {
			if p.Kind == conversation.PartImage && p.Ref != nil {
				refs++
				if p.Ref.MIME != "image/png" || p.Ref.Name != "a.png" {
					t.Fatalf("ref = %+v", p.Ref)
				}
			}
		}
	}
	if refs != 1 {
		t.Fatalf("图片分片 = %d, want 1", refs)
	}
	if rep.Counts[i18n.T("exchange.io.drop.ds_file_no_bytes")] != 1 {
		t.Fatalf("非图片没计丢弃: %+v", rep.Counts)
	}
	// --no-files：图片也不入库，两条都计丢弃。
	data, _ = os.ReadFile(path)
	cs, rep3, err := New(Options{Blobs: blobs}).Import(context.Background(), bytes.NewReader(data),
		port.ExchangeFormat{Name: "deepseek"}, nil, port.ImportOptions{OmitFiles: true})
	if len(cs) != 1 {
		t.Fatalf("cs = %d", len(cs))
	}
	if err != nil {
		t.Fatalf("no-files import: %v", err)
	}
	if rep3.Counts[i18n.T("exchange.io.drop.ds_file_no_bytes")] != 2 {
		t.Fatalf("--no-files 没全计丢弃: %+v", rep3.Counts)
	}
}

// TestDeepSeekEmptyNodeReparent 空节点（无分片）跳过，子节点顺父指针接最近祖先；Head
// 落在空节点上时回退到最近收下的祖先并写一行说明。
func TestDeepSeekEmptyNodeReparent(t *testing.T) {
	one, two := 1, 2
	s := dsSession{
		ID: "aaaa", Title: "空节点", CurrentMessage: 3,
		Messages: []dsMessage{
			{ID: 1, Role: "USER", Status: "FINISHED", Fragments: []dsFragment{dsFrag("REQUEST", "q")}},
			// 消息 2 无分片（空节点）：不建壳，消息 3 顺父接到消息 1。
			{ID: 2, ParentID: &one, Role: "ASSISTANT", Status: "FINISHED"},
			{ID: 3, ParentID: &two, Role: "ASSISTANT", Status: "FINISHED", Fragments: []dsFragment{dsFrag("RESPONSE", "a")}},
		},
	}
	files := map[string][]byte{
		"manifest.json":           []byte(`{"export_format_version":3}`),
		"chat_session_index.json": dsIndexJSON([]dsIndexEntry{{ID: "aaaa", Title: "空节点", MessageCount: 3, UpdatedAt: "2026-11-17T10:00:00Z"}}),
		"chat_sessions/aaaa.json": dsSessionJSON(s),
	}
	path := dsFixtureZip(t, files)
	data, _ := os.ReadFile(path)
	c, rep, err := importOne(context.Background(), New(Options{}), bytes.NewReader(data), port.ExchangeFormat{Name: "deepseek"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	snap := c.Snapshot()
	if nodeCount(c) != 2 {
		t.Fatalf("nodes = %d, want 2（空节点跳过）", nodeCount(c))
	}
	headMsg := dsFindByID(snap, snap.Head)
	if headMsg.Role != conversation.RoleAssistant || len(headMsg.Content) == 0 {
		t.Fatalf("head 落错: %+v", headMsg)
	}
	if len(rep.Dropped) != 0 {
		t.Fatalf("Head 直落收下节点，不该有说明: %v", rep.Dropped)
	}
	// Head 落在空节点上（current_message_id = 2）：回退到最近收下的祖先并写一行说明。
	s.CurrentMessage = 2
	files["chat_sessions/aaaa.json"] = dsSessionJSON(s)
	data2, _ := os.ReadFile(dsFixtureZip(t, files))
	c2, rep2, err := importOne(context.Background(), New(Options{}), bytes.NewReader(data2), port.ExchangeFormat{Name: "deepseek"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(rep2.Dropped) == 0 {
		t.Fatal("Head 回退该有一行说明")
	}
	if got := dsFindByID(c2.Snapshot(), c2.Snapshot().Head); got.Role == conversation.RoleAssistant {
		t.Fatalf("head 不该落在 assistant 上: %+v", got)
	}
}

// TestDeepSeekPickOutOfRange 越界报因并列清单。
func TestDeepSeekPickOutOfRange(t *testing.T) {
	path := dsFixtureZip(t, map[string][]byte{
		"manifest.json":           []byte(`{"export_format_version":3}`),
		"chat_session_index.json": dsIndexJSON([]dsIndexEntry{{ID: "a", Title: "唯一", MessageCount: 1, UpdatedAt: "2026-11-17T10:00:00Z"}}),
		"chat_sessions/a.json":    dsSessionJSON(dsSession{ID: "a", Title: "唯一", Messages: []dsMessage{{ID: 1, Role: "USER", Status: "FINISHED", Fragments: []dsFragment{dsFrag("REQUEST", "q")}}}}),
	})
	_, _, err := New(Options{}).Import(context.Background(), mustOpen(t, path),
		port.ExchangeFormat{Name: "deepseek"}, []int{5}, port.ImportOptions{})
	if err == nil || !strings.Contains(err.Error(), "5") {
		t.Fatalf("err = %v, want 越界报因", err)
	}
}

// TestDeepSeekNoIndex 没有 chat_session_index.json 不是 deepseek 的包。
func TestDeepSeekNoIndex(t *testing.T) {
	path := dsFixtureZip(t, map[string][]byte{
		"manifest.json":        []byte(`{"export_format_version":3}`),
		"chat_sessions/a.json": []byte(`{}`),
	})
	_, _, err := New(Options{}).Import(context.Background(), mustOpen(t, path),
		port.ExchangeFormat{Name: "deepseek"}, nil, port.ImportOptions{})
	if err == nil || !strings.Contains(err.Error(), trHead("exchange.err.ds.no_index")) {
		t.Fatalf("err = %v, want 缺清单报因", err)
	}
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// dsFindByID 按节点 ID 取消息（快照里 Nodes 无索引）。
func dsFindByID(snap conversation.Snapshot, id conversation.MessageID) conversation.Message {
	for _, m := range snap.Nodes {
		if m.ID == id {
			return m
		}
	}
	return conversation.Message{}
}
