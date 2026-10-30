package exchange

// aqx_multi_test.go 一个归档装一组会话的读写口径（§5.16 / D249）：
// 清单顺序、附件跨会话去重、导入的 --pick、旧形状拒绝、Index 只看清单。

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// multiConv 造一个带 n 条 user 消息的会话（根 + persona 之外只有正文，够比清单与条数）。
func multiConv(t *testing.T, id, title string, n int) *conversation.Conversation {
	t.Helper()
	c := conversation.New(conversation.ID(id), title)
	for i := 0; i < n; i++ {
		if _, err := c.Append(conversation.RoleUser, []conversation.Part{
			{Kind: conversation.PartText, Text: fmt.Sprintf("%s-%d", title, i)},
		}); err != nil {
			t.Fatalf("append %s#%d: %v", title, i, err)
		}
	}
	return c
}

// zipNames 归档里的条目名（按写入序）。
func zipNames(t *testing.T, data []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	out := make([]string, len(zr.File))
	for i, f := range zr.File {
		out[i] = f.Name
	}
	return out
}

// TestAQXMultiSessionLayout 三件事一次锁住：清单顺序 = 会话顺序、树按序号落条目、
// 附件跨会话只存一份（同一个附件被两个会话引用）。
func TestAQXMultiSessionLayout(t *testing.T) {
	blobs, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	ref, err := blobs.Put(context.Background(), strings.NewReader("共用图片"), "image/png", "shared.png")
	if err != nil {
		t.Fatalf("put blob: %v", err)
	}
	cs := []*conversation.Conversation{
		multiConv(t, "c1", "第一个", 2),
		multiConv(t, "c2", "第二个", 1),
		multiConv(t, "c3", "第三个", 3),
	}
	// 第 1、3 个会话引用同一份附件：归档里只该出现一次。
	for _, i := range []int{0, 2} {
		if _, err := cs[i].Append(conversation.RoleUser, []conversation.Part{
			{Kind: conversation.PartImage, Ref: &ref},
		}); err != nil {
			t.Fatalf("append image #%d: %v", i, err)
		}
	}

	var buf bytes.Buffer
	rep, err := New(Options{Blobs: blobs}).Export(context.Background(), cs, "aqx", &buf)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(rep.Entries) != 3 {
		t.Fatalf("回执清单条目 = %d, want 3", len(rep.Entries))
	}
	for i, e := range rep.Entries {
		if e.Index != i+1 || e.ID != string(cs[i].ID) || e.Title != cs[i].Title {
			t.Fatalf("清单第 %d 项 = %+v，want 序号 %d / %s / %s", i, e, i+1, cs[i].ID, cs[i].Title)
		}
	}
	if rep.Nodes != 2+1+3+2 { // 三个会话的正文条数之和（不含 Root），加两张图
		t.Fatalf("回执节点总数 = %d, want 8", rep.Nodes)
	}

	names := zipNames(t, buf.Bytes())
	want := []string{
		"manifest.json",
		"conversations/1/tree.json",
		"conversations/2/tree.json",
		"conversations/3/tree.json",
		"blobs/" + ref.Hash,
	}
	if len(names) != len(want) {
		t.Fatalf("条目 = %v，want %v（同一附件不该存两份）", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("条目[%d] = %q, want %q（写入顺序要确定）", i, names[i], want[i])
		}
	}
}

// TestAQXMultiSessionRoundtrip 三棵树导回来：数量、顺序、标题、节点数逐个对得上。
func TestAQXMultiSessionRoundtrip(t *testing.T) {
	blobs := mustBlobs(t)
	src := []*conversation.Conversation{
		multiConv(t, "c1", "第一个", 2),
		multiConv(t, "c2", "第二个", 1),
		multiConv(t, "c3", "第三个", 3),
	}
	var buf bytes.Buffer
	if _, err := New(Options{Blobs: blobs}).Export(context.Background(), src, "aqx", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	got, rep, err := New(Options{Blobs: blobs}).Import(context.Background(),
		bytes.NewReader(buf.Bytes()), port.ExchangeFormat{Name: "aqx"}, nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(got) != len(src) {
		t.Fatalf("导入 %d 个会话, want %d", len(got), len(src))
	}
	for i := range src {
		if got[i].ID != src[i].ID || got[i].Title != src[i].Title {
			t.Fatalf("第 %d 个 = %s/%s, want %s/%s", i, got[i].ID, got[i].Title, src[i].ID, src[i].Title)
		}
		assertSnapshotsEqual(t, src[i].Snapshot(), got[i].Snapshot())
	}
	if len(rep.Entries) != 3 || rep.Nodes != 6 {
		t.Fatalf("回执 = %d 条清单 / %d 条消息, want 3 / 6", len(rep.Entries), rep.Nodes)
	}
}

// TestAQXPickVariants `--pick` 四种写法：空 = 全部、单个、逗号、重复；顺序恒为清单顺序。
func TestAQXPickVariants(t *testing.T) {
	blobs := mustBlobs(t)
	src := []*conversation.Conversation{
		multiConv(t, "c1", "一", 1),
		multiConv(t, "c2", "二", 2),
		multiConv(t, "c3", "三", 3),
	}
	var buf bytes.Buffer
	if _, err := New(Options{Blobs: blobs}).Export(context.Background(), src, "aqx", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	aqx := port.ExchangeFormat{Name: "aqx"}
	cases := []struct {
		name string
		pick []int
		want []string
	}{
		{"空 = 全部", nil, []string{"c1", "c2", "c3"}},
		{"单个", []int{2}, []string{"c2"}},
		{"乱序也按清单顺序", []int{3, 1}, []string{"c1", "c3"}},
		{"重复只算一次", []int{2, 2, 1}, []string{"c1", "c2"}},
	}
	for _, tc := range cases {
		got, rep, err := New(Options{Blobs: blobs}).Import(context.Background(),
			bytes.NewReader(buf.Bytes()), aqx, tc.pick)
		if err != nil {
			t.Fatalf("%s: import: %v", tc.name, err)
		}
		var ids []string
		for _, c := range got {
			ids = append(ids, string(c.ID))
		}
		if strings.Join(ids, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("%s: 导入 = %v, want %v", tc.name, ids, tc.want)
		}
		// 回执列的是**这次导入的**那几个，序号仍是清单序号（越界报错时才列整份清单）。
		if len(rep.Entries) != len(tc.want) {
			t.Fatalf("%s: 回执清单 = %d 项, want %d", tc.name, len(rep.Entries), len(tc.want))
		}
	}
}

// TestAQXPickOutOfRange 越界与 0 都报因，并把清单列出来（只说「序号不对」用户还得自己数）。
func TestAQXPickOutOfRange(t *testing.T) {
	blobs := mustBlobs(t)
	src := []*conversation.Conversation{
		multiConv(t, "c1", "第一个", 1),
		multiConv(t, "c2", "第二个", 1),
	}
	var buf bytes.Buffer
	if _, err := New(Options{Blobs: blobs}).Export(context.Background(), src, "aqx", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	for _, pick := range [][]int{{3}, {0}} {
		_, _, err := New(Options{Blobs: blobs}).Import(context.Background(),
			bytes.NewReader(buf.Bytes()), port.ExchangeFormat{Name: "aqx"}, pick)
		if err == nil || !strings.Contains(err.Error(), "清单") ||
			!strings.Contains(err.Error(), "第一个") || !strings.Contains(err.Error(), "第二个") {
			t.Fatalf("pick %v: err = %v, want 报因并列出清单", pick, err)
		}
	}
}

// TestAQXMultiSessionOnly 多会话只对 aqx 成立：其余格式给多于一个就报错，不静默只写第一个。
func TestAQXMultiSessionOnly(t *testing.T) {
	cs := []*conversation.Conversation{
		multiConv(t, "c1", "一", 1),
		multiConv(t, "c2", "二", 1),
	}
	for _, name := range []string{"chatgpt", "openai", "sillytavern"} {
		var buf bytes.Buffer
		_, err := New(Options{}).Export(context.Background(), cs, name, &buf)
		if err == nil || !strings.Contains(err.Error(), "一次只能写一个会话") ||
			!strings.Contains(err.Error(), "aqx") {
			t.Fatalf("%s: err = %v, want 一次只能写一个会话 + 指路 aqx", name, err)
		}
		if buf.Len() != 0 {
			t.Fatalf("%s: 报错时不该写出任何字节（%d 字节）", name, buf.Len())
		}
	}
}

// TestAQXMultiSessionDeterministic 同一组会话导两次字节相同（附件排序、会话按清单顺序）。
func TestAQXMultiSessionDeterministic(t *testing.T) {
	blobs := mustBlobs(t)
	cs := []*conversation.Conversation{
		multiConv(t, "c1", "一", 2),
		multiConv(t, "c2", "二", 1),
	}
	var a, b bytes.Buffer
	if _, err := New(Options{Blobs: blobs}).Export(context.Background(), cs, "aqx", &a); err != nil {
		t.Fatalf("export#1: %v", err)
	}
	if _, err := New(Options{Blobs: blobs}).Export(context.Background(), cs, "aqx", &b); err != nil {
		t.Fatalf("export#2: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("同一组会话导两次字节不同")
	}
}

// TestIndexListsManifest Index 只读清单：序号、ID、标题、条数；不解树（坏树照样能列出清单）。
func TestIndexListsManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backup.aqx")
	data := zipBytes(t, map[string]string{
		"manifest.json": `{"format":"aqx","version":1,"conversations":[` +
			`{"id":"c1","title":"第一个","nodes":4},{"id":"c2","title":"第二个","nodes":0}]}`,
		// 树是坏的：Index 不解析它，所以照样能列出清单。
		"conversations/1/tree.json": "not json",
		"conversations/2/tree.json": "not json",
	})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写归档: %v", err)
	}
	entries, err := New(Options{}).Index(context.Background(), path)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("清单 = %d 项, want 2", len(entries))
	}
	if entries[0] != (port.ExchangeEntry{Index: 1, ID: "c1", Title: "第一个", Nodes: 4}) {
		t.Fatalf("第 1 项 = %+v", entries[0])
	}
	if entries[1].Index != 2 || entries[1].Title != "第二个" {
		t.Fatalf("第 2 项 = %+v", entries[1])
	}
}

// TestIndexRejects 不是归档、更高版本、缺清单都报因——界面据此退回普通导入。
func TestIndexRejects(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"不是 zip":     "这不是归档",
		"缺 manifest": "",
	}
	for name, body := range cases {
		path := filepath.Join(dir, name+".aqx")
		if body == "" {
			body = string(zipBytes(t, map[string]string{"conversations/1/tree.json": "{}"}))
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("%s: 写文件: %v", name, err)
		}
		if _, err := New(Options{}).Index(context.Background(), path); err == nil {
			t.Fatalf("%s: 应报错", name)
		}
	}
	higher := filepath.Join(dir, "higher.aqx")
	if err := os.WriteFile(higher, zipBytes(t, map[string]string{
		"manifest.json": `{"format":"aqx","version":99,"conversations":[{"id":"c1","title":"t","nodes":1}]}`,
	}), 0o644); err != nil {
		t.Fatalf("写高版本归档: %v", err)
	}
	if _, err := New(Options{}).Index(context.Background(), higher); err == nil ||
		!strings.Contains(err.Error(), "高于本程序支持") {
		t.Fatalf("高版本 err = %v", err)
	}
}

// TestImportPickIgnoresUnselectedTrees 没选中的树坏掉也不该挡住导入：它们本来就不会进库。
func TestImportPickIgnoresUnselectedTrees(t *testing.T) {
	blobs := mustBlobs(t)
	src := []*conversation.Conversation{
		multiConv(t, "c1", "好的", 1),
		multiConv(t, "c2", "坏的", 1),
	}
	var buf bytes.Buffer
	if _, err := New(Options{Blobs: blobs}).Export(context.Background(), src, "aqx", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	// 把第 2 个会话的树换成一段坏 JSON，只挑第 1 个。
	patched := rewriteZipEntry(t, buf.Bytes(), "conversations/2/tree.json", "not json")
	if _, _, err := New(Options{Blobs: blobs}).Import(context.Background(),
		bytes.NewReader(patched), port.ExchangeFormat{Name: "aqx"}, nil); err == nil {
		t.Fatal("全导时应因第 2 个会话的坏树而失败")
	}
	got, _, err := New(Options{Blobs: blobs}).Import(context.Background(),
		bytes.NewReader(patched), port.ExchangeFormat{Name: "aqx"}, []int{1})
	if err != nil {
		t.Fatalf("只导第 1 个应成功: %v", err)
	}
	if len(got) != 1 || got[0].Title != "好的" {
		t.Fatalf("导入 = %+v", got)
	}
}

// TestAQXManifestOrderIsPickOrder 清单顺序 = 导入顺序 = 回执顺序：清单与树条目错位时按清单走，
// 树按序号取，不会「按压缩包里的物理顺序」。
func TestAQXManifestOrderIsPickOrder(t *testing.T) {
	blobs := mustBlobs(t)
	a := multiConv(t, "ca", "甲", 1)
	b := multiConv(t, "cb", "乙", 1)
	var buf bytes.Buffer
	if _, err := New(Options{Blobs: blobs}).Export(context.Background(),
		[]*conversation.Conversation{b, a}, "aqx", &buf); err != nil { // 故意倒着给
		t.Fatalf("export: %v", err)
	}
	got, rep, err := New(Options{Blobs: blobs}).Import(context.Background(),
		bytes.NewReader(buf.Bytes()), port.ExchangeFormat{Name: "aqx"}, nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if got[0].ID != "cb" || got[1].ID != "ca" {
		t.Fatalf("导入顺序 = %s,%s，want cb,ca（清单顺序 = 导出给的顺序）", got[0].ID, got[1].ID)
	}
	if rep.Entries[0].ID != "cb" {
		t.Fatalf("回执清单首项 = %s, want cb", rep.Entries[0].ID)
	}
}

// rewriteZipEntry 复制一份归档，把其中一个条目的内容换掉（其余原样）。
func rewriteZipEntry(t *testing.T, data []byte, name, body string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		payload, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		if f.Name == name {
			payload = []byte(body)
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatalf("create %s: %v", f.Name, err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("write %s: %v", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return out.Bytes()
}
