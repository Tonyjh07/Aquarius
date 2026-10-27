package exchange

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fixture 造一棵尽量「用得上全部字段」的会话：分叉、版本链、思考分片、工具分片（含签名字段
// 之外的一切）、注入节点带来源与信任位、附件引用。
//
// 无损往返的验收就在这些字段上——只比对节点数的往返测不出「哪个字段被静默丢了」。
func fixture(t *testing.T, blobs port.AttachmentStore) *conversation.Conversation {
	t.Helper()
	c := conversation.New(conversation.ID("conv-ex"), "往返测试")
	c.CreatedAt = c.CreatedAt.UTC()
	if err := c.AppendCommitted(conversation.Message{
		ID: "S1", Parent: conversation.MessageID(c.ID), Role: conversation.RoleSystem,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: "人格"}},
		CreatedAt: c.CreatedAt,
	}); err != nil {
		t.Fatalf("append system: %v", err)
	}
	u1, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "你好"}})
	if err != nil {
		t.Fatalf("append user: %v", err)
	}
	a1, err := c.Append(conversation.RoleAssistant, []conversation.Part{
		{Kind: conversation.PartThinking, Text: "想想", Signature: "anthropic:sig"},
		{Kind: conversation.PartText, Text: "在"},
		{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
			CallID: "c1", Name: "file_read", Args: []byte(`{"path":"a.txt"}`),
			Result: &tool.Result{CallID: "c1", OK: true, Output: "文件内容"},
		}},
	})
	if err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	// 附件分片：先往附件库存一份，再引用它。
	if blobs != nil {
		ref, err := blobs.Put(context.Background(), strings.NewReader("图片字节"), "image/png", "pic.png")
		if err != nil {
			t.Fatalf("put blob: %v", err)
		}
		if _, err := c.Append(conversation.RoleUser, []conversation.Part{
			{Kind: conversation.PartImage, Ref: &ref},
		}); err != nil {
			t.Fatalf("append image: %v", err)
		}
		if err := c.AppendCommitted(conversation.Message{
			ID: "J1", Parent: c.Head, Role: conversation.RoleInject,
			Content:   []conversation.Part{{Kind: conversation.PartText, Text: "任务完成"}},
			Source:    "job:j1",
			CreatedAt: c.CreatedAt,
		}); err != nil {
			t.Fatalf("append inject: %v", err)
		}
	}
	// 版本链：在 a1 上分叉后撤回，再在 u1 上重发（Revise Fresh）——树里因此既有旁支又有版本链。
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "旁支"}}); err != nil {
		t.Fatalf("append branch: %v", err)
	}
	if _, err := c.Revise(u1.ID,
		[]conversation.Part{{Kind: conversation.PartText, Text: "你好（改）"}}, conversation.Fresh); err != nil {
		t.Fatalf("revise: %v", err)
	}
	_ = a1
	return c
}

// TestAQXRoundtripEquivalent aqx 无损往返：节点、边、版本链、Head、时间戳、分片全部字段、
// 附件字节逐项相同（DESIGN §5.16 的验收口径）。
func TestAQXRoundtripEquivalent(t *testing.T) {
	blobsDir := t.TempDir()
	blobs, err := blobfs.New(blobsDir)
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	ex := New(Options{Blobs: blobs})
	src := fixture(t, blobs)

	var buf bytes.Buffer
	rep, err := ex.Export(context.Background(), src, "aqx", &buf)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if rep.Bytes <= 0 || buf.Len() != int(rep.Bytes) {
		t.Fatalf("回执字节数 = %d，实际写出 %d", rep.Bytes, buf.Len())
	}
	if got, want := rep.Nodes, src.NodeCount()-1; got != want {
		t.Fatalf("回执节点数 = %d, want %d（不含 Root）", got, want)
	}
	if len(rep.Dropped) != 0 {
		t.Fatalf("无损导出不该有丢弃项: %v", rep.Dropped)
	}

	// 换一份空附件库导入，才能证明附件字节真进了归档（而不是恰好还在原库里）。
	fresh, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs#2: %v", err)
	}
	importer := New(Options{Blobs: fresh})
	format, ok := importer.Detect("conv.aqx", buf.Bytes()[:min(64, buf.Len())])
	if !ok || format.Name != "aqx" {
		t.Fatalf("Detect = %+v, %v", format, ok)
	}
	got, irep, err := importer.Import(context.Background(), bytes.NewReader(buf.Bytes()), format)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(irep.Dropped) != 0 {
		t.Fatalf("无损导入不该有丢弃项: %v", irep.Dropped)
	}
	assertSnapshotsEqual(t, src.Snapshot(), got.Snapshot())

	// 附件字节确实回来了：新库里能按树里的引用取到同样内容。
	for _, m := range got.Snapshot().Nodes {
		for _, p := range m.Content {
			if p.Ref == nil {
				continue
			}
			rc, err := fresh.Get(context.Background(), *p.Ref)
			if err != nil {
				t.Fatalf("取回附件 %s: %v", p.Ref.Name, err)
			}
			var body bytes.Buffer
			if _, err := body.ReadFrom(rc); err != nil {
				t.Fatalf("读回附件: %v", err)
			}
			rc.Close()
			if body.String() != "图片字节" {
				t.Fatalf("附件内容 = %q，want 图片字节", body.String())
			}
		}
	}
}

// TestAQXDeterministic 同一棵树导两次字节相同（DESIGN §5.16 验收）。
func TestAQXDeterministic(t *testing.T) {
	blobs, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	ex := New(Options{Blobs: blobs})
	src := fixture(t, blobs)
	var a, b bytes.Buffer
	if _, err := ex.Export(context.Background(), src, "aqx", &a); err != nil {
		t.Fatalf("export#1: %v", err)
	}
	if _, err := ex.Export(context.Background(), src, "aqx", &b); err != nil {
		t.Fatalf("export#2: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("同一棵树导两次字节不同（归档里有非确定性的顺序或时间戳）")
	}
}

// TestDetect 认 aqx：ZIP 魔数优先，扩展名兜底；其他格式交回 false（本版只接 aqx）。
func TestDetect(t *testing.T) {
	ex := New(Options{})
	if f, ok := ex.Detect("x.aqx", []byte("PK\x03\x04rest")); !ok || f.Name != "aqx" {
		t.Fatalf("ZIP 魔数应认成 aqx: %+v %v", f, ok)
	}
	if f, ok := ex.Detect("X.AQX", []byte("not a zip")); !ok || f.Name != "aqx" {
		t.Fatalf("扩展名应认成 aqx: %+v %v", f, ok)
	}
	for _, name := range []string{"chat.json", "conv.jsonl", "note.txt", ""} {
		if f, ok := ex.Detect(name, []byte("{}")); ok {
			t.Fatalf("%q 不该被认成 %s", name, f.Name)
		}
	}
}

// TestImportRejectsUntrusted 外部文件是不可信输入（DESIGN §5.16）：坏树、高版本、混乱条目
// 一律报因，不「尽力修复」。
func TestImportRejectsUntrusted(t *testing.T) {
	ex := New(Options{})
	aqx := port.ExchangeFormats()[0] // aqx

	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "缺 manifest",
			files: map[string]string{"tree.json": "{}"},
			want:  "manifest",
		},
		{
			name: "更高版本",
			files: map[string]string{
				"manifest.json": `{"format":"aqx","version":99}`,
				"tree.json":     "{}",
			},
			want: "高于本程序支持",
		},
		{
			name: "tree 不是合法树",
			files: map[string]string{
				"manifest.json": `{"format":"aqx","version":1}`,
				"tree.json":     `{"id":"x","title":"t","head":"nope","nodes":{},"children":{}}`,
			},
			want: "根节点",
		},
		{
			name: "不认识的文件名混进归档",
			files: map[string]string{
				"manifest.json": `{"format":"aqx","version":1}`,
				"tree.json":     "{}",
				"other.txt":     "x",
			},
			want: "不认识的条目",
		},
	}
	for _, tc := range cases {
		data := zipBytes(t, tc.files)
		_, _, err := ex.Import(context.Background(), bytes.NewReader(data), aqx)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want 含 %q", tc.name, err, tc.want)
		}
	}
}

// TestImportRejectsBlobHashMismatch 归档里的附件字节与它声明的哈希对不上 = 归档损坏，
// 拒绝导入（按错误哈希入库会在装配时取到别的内容）。
func TestImportRejectsBlobHashMismatch(t *testing.T) {
	c := conversation.New(conversation.ID("c-hash"), "哈希测试")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "hi"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	tree, err := storejson.EncodeTree(c)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	data := zipBytes(t, map[string]string{
		"manifest.json":                    `{"format":"aqx","version":1}`,
		"tree.json":                        string(tree),
		"blobs/" + strings.Repeat("0", 64): "内容与名字对不上",
	})
	_, _, err = New(Options{Blobs: mustBlobs(t)}).Import(context.Background(),
		bytes.NewReader(data), port.ExchangeFormat{Name: "aqx"})
	if err == nil || !strings.Contains(err.Error(), "哈希不符") {
		t.Fatalf("err = %v, want 哈希不符", err)
	}
}

// TestSafeArchivePath 归档条目口径：绝对路径、盘符、`..` 拒绝；`.` 与空名跳过。
func TestSafeArchivePath(t *testing.T) {
	ok := map[string]string{
		"tree.json":    "tree.json",
		`blobs\abc`:    "blobs/abc",
		"./tree.json":  "tree.json",
		"blobs/":       "blobs",
		".":            "",
		"":             "",
		"a/./b/c.json": "a/b/c.json",
	}
	for in, want := range ok {
		got, err := safeArchivePath(in)
		if err != nil || got != want {
			t.Errorf("safeArchivePath(%q) = %q, %v；期望 %q", in, got, err, want)
		}
	}
	for _, in := range []string{"/abs", `C:\win`, "../up", "a/../../up", `..\up`} {
		if _, err := safeArchivePath(in); err == nil {
			t.Errorf("safeArchivePath(%q) 应拒绝", in)
		}
	}
}

// TestExportWithoutBlobStore 没配附件库时仍能导出树，但回执必须说明附件字节没带上——
// 「备份看起来完整」是最危险的那种静默。
func TestExportWithoutBlobStore(t *testing.T) {
	blobs, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	src := fixture(t, blobs) // 树里有附件引用，但导出侧没有附件库
	ex := New(Options{})
	var buf bytes.Buffer
	rep, err := ex.Export(context.Background(), src, "aqx", &buf)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(rep.Dropped) == 0 || !strings.Contains(rep.Dropped[0], "附件") {
		t.Fatalf("回执应说明附件字节没带上，实际 %v", rep.Dropped)
	}
	// 树本身仍然无损：导入后节点与边齐全，只是取不到附件字节。
	got, irep, err := New(Options{}).Import(context.Background(), bytes.NewReader(buf.Bytes()), port.ExchangeFormat{Name: "aqx"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if got.NodeCount() != src.NodeCount() {
		t.Fatalf("节点数 = %d, want %d", got.NodeCount(), src.NodeCount())
	}
	if len(irep.Dropped) == 0 || !strings.Contains(strings.Join(irep.Dropped, " "), "附件") {
		t.Fatalf("导入回执应说明附件字节没入库，实际 %v", irep.Dropped)
	}
}

// TestExportFormatsNotWired 本版只有 aqx 接入；其余格式与只读那档都要给明确的错因，
// 不能落到「未知格式」那种含糊说法。
func TestExportFormatsNotWired(t *testing.T) {
	ex := New(Options{})
	c := conversation.New("c1", "t")
	var buf bytes.Buffer
	if _, err := ex.Export(context.Background(), c, "openwebui", &buf); err == nil ||
		!strings.Contains(err.Error(), "只认不写") {
		t.Fatalf("openwebui: err = %v", err)
	}
	if _, err := ex.Export(context.Background(), c, "chatgpt", &buf); err == nil ||
		!strings.Contains(err.Error(), "未接入") {
		t.Fatalf("chatgpt: err = %v", err)
	}
	if _, err := ex.Export(context.Background(), c, "不存在", &buf); err == nil ||
		!strings.Contains(err.Error(), "可写") {
		t.Fatalf("未知格式应列出可写格式: %v", err)
	}
}

// assertSnapshotsEqual 逐字段比对两份快照（节点、边、版本链、Head、时间戳）。
func assertSnapshotsEqual(t *testing.T, want, got conversation.Snapshot) {
	t.Helper()
	if want.ID != got.ID || want.Title != got.Title || want.Head != got.Head {
		t.Fatalf("元信息不等: %+v vs %+v", want, got)
	}
	if !want.CreatedAt.Equal(got.CreatedAt) || !want.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatalf("时间戳不等: %v/%v vs %v/%v", want.CreatedAt, want.UpdatedAt, got.CreatedAt, got.UpdatedAt)
	}
	if len(want.Nodes) != len(got.Nodes) {
		t.Fatalf("节点数 = %d, want %d", len(got.Nodes), len(want.Nodes))
	}
	byID := map[conversation.MessageID]conversation.Message{}
	for _, m := range got.Nodes {
		byID[m.ID] = m
	}
	for _, w := range want.Nodes {
		g, ok := byID[w.ID]
		if !ok {
			t.Fatalf("节点 %s 丢失", w.ID)
		}
		// 内存里的时间带单调时钟读数、落盘后没有：比对前统一去掉（Round(0) 只保留墙钟）。
		w.CreatedAt, g.CreatedAt = w.CreatedAt.Round(0), g.CreatedAt.Round(0)
		wj, err := json.Marshal(w)
		if err != nil {
			t.Fatalf("序列化节点 %s: %v", w.ID, err)
		}
		gj, err := json.Marshal(g)
		if err != nil {
			t.Fatalf("序列化节点 %s: %v", w.ID, err)
		}
		if !bytes.Equal(wj, gj) {
			t.Fatalf("节点 %s 不等:\nwant %s\ngot  %s", w.ID, wj, gj)
		}
	}
	if !reflect.DeepEqual(want.Edges, got.Edges) {
		t.Fatalf("边不等:\nwant %+v\ngot  %+v", want.Edges, got.Edges)
	}
	if !reflect.DeepEqual(want.RevisedFrom, got.RevisedFrom) {
		t.Fatalf("版本链不等:\nwant %+v\ngot  %+v", want.RevisedFrom, got.RevisedFrom)
	}
}

// mustBlobs 造一个临时附件库（测试里多处要用）。
func mustBlobs(t *testing.T) port.AttachmentStore {
	t.Helper()
	b, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	return b
}

// zipBytes 在内存里造一个 ZIP（测试用；文件名即条目名）。
func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		if err := writeZipEntry(zw, name, func(w io.Writer) error {
			_, err := io.WriteString(w, body)
			return err
		}); err != nil {
			t.Fatalf("写条目 %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("收尾 ZIP: %v", err)
	}
	return buf.Bytes()
}
