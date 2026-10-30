package app

// session_exchange_test.go `/export` 与 `/import` 的命令面：路径挑选、防覆盖、半截文件
// 清理、导入落成新会话、回执口径，以及未接线时如实报「本构建没接」。

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeExchange 会话进出替身：导出写一段预设字节，导入交回一棵预设树。
type fakeExchange struct {
	formats   []port.ExchangeFormat
	payload   string
	writeErr  error
	imported  *conversation.Conversation
	importRep port.ExchangeReport
	importErr error
	detected  string
	exported  []string // 依次记下每次导出的格式名
	exportedN []int    // 每次导出时源会话的节点数
}

var (
	_ port.SessionExporter = (*fakeExchange)(nil)
	_ port.SessionImporter = (*fakeExchange)(nil)
)

func (f *fakeExchange) Formats() []port.ExchangeFormat {
	if f.formats != nil {
		return f.formats
	}
	return port.ExchangeFormats()
}

func (f *fakeExchange) Export(_ context.Context, cs []*conversation.Conversation, format string, w io.Writer) (port.ExchangeReport, error) {
	f.exported = append(f.exported, format)
	f.exportedN = append(f.exportedN, cs[0].NodeCount())
	if f.writeErr != nil {
		return port.ExchangeReport{Format: format}, f.writeErr
	}
	n, err := io.WriteString(w, f.payload)
	if err != nil {
		return port.ExchangeReport{Format: format}, err
	}
	return port.ExchangeReport{Format: format, Nodes: cs[0].NodeCount() - 1, Bytes: int64(n)}, nil
}

func (f *fakeExchange) Detect(name string, _ []byte) (port.ExchangeFormat, bool) {
	for _, fm := range f.Formats() {
		if fm.Name == f.detected {
			return fm, true
		}
	}
	return port.ExchangeFormat{}, false
}

func (f *fakeExchange) Import(context.Context, io.Reader, port.ExchangeFormat, []int) ([]*conversation.Conversation, port.ExchangeReport, error) {
	if f.importErr != nil {
		return nil, port.ExchangeReport{}, f.importErr
	}
	return []*conversation.Conversation{f.imported}, f.importRep, nil
}

// exchangeSession 造一个带导出/导入面与临时导出目录的会话。
func exchangeSession(t *testing.T, ex *fakeExchange) (*Session, string) {
	t.Helper()
	dir := t.TempDir()
	rec := &recorder{}
	llm := &scriptLLM{t: t}
	agent := newAgent(t, llm, rec, Deps{}, Config{})
	s, err := NewSession(context.Background(), SessionDeps{
		Store:      newMemStore(),
		Agent:      agent,
		IDs:        &seqIDs{},
		Clock:      fixedClock{testTime},
		UI:         rec,
		Exporter:   ex,
		Importer:   ex,
		ExportsDir: dir,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, dir
}

// exportSession 造一个带一条 user 消息的会话（节点数 3：Root + persona + 一条）。
func exportSession(t *testing.T, ex *fakeExchange) (*Session, string) {
	t.Helper()
	s, dir := exchangeSession(t, ex)
	if _, err := s.cur.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "hi"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	return s, dir
}

// TestExportDefaultsToAQXInExportsDir /export 无参：格式缺省 aqx，落在导出目录，
// 文件名由会话标题派生。
func TestExportDefaultsToAQXInExportsDir(t *testing.T) {
	ex := &fakeExchange{payload: "aqx-bytes"}
	s, dir := exportSession(t, ex)
	out, err := s.execExport(context.Background(), port.Command{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(ex.exported) != 1 || ex.exported[0] != "aqx" {
		t.Fatalf("导出格式 = %v, want [aqx]", ex.exported)
	}
	want := filepath.Join(dir, port.FileStem(s.cur.Title)+".aqx")
	if data, err := os.ReadFile(want); err != nil || string(data) != "aqx-bytes" {
		t.Fatalf("导出文件 %s 内容 = %q, err = %v", want, data, err)
	}
	for _, want := range []string{want, "aqx", "无损", "2 条消息"} {
		if !strings.Contains(out, want) {
			t.Fatalf("回执缺 %q:\n%s", want, out)
		}
	}
	// 无损格式不该出现「没有带过去」这一段。
	if strings.Contains(out, "没有带过去") {
		t.Fatalf("无损导出不该列丢东西:\n%s", out)
	}
}

// TestExportExplicitPathAndFormat --path 与 --format 都认；顺序无关。
func TestExportExplicitPathAndFormat(t *testing.T) {
	ex := &fakeExchange{payload: "chatgpt"}
	s, _ := exportSession(t, ex)
	target := filepath.Join(t.TempDir(), "conv.json")
	out, err := s.execExport(context.Background(), port.Command{
		Args: []string{"--format", "chatgpt", "--path", target},
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(ex.exported) != 1 || ex.exported[0] != "chatgpt" {
		t.Fatalf("导出格式 = %v, want [chatgpt]", ex.exported)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("目标文件没写出来: %v", err)
	}
	if !strings.Contains(out, "有损") {
		t.Fatalf("有损格式应写明丢什么:\n%s", out)
	}
}

// TestExportDoesNotOverwrite 同名文件已存在就加 -2 后缀——导出是「再存一份」。
func TestExportDoesNotOverwrite(t *testing.T) {
	ex := &fakeExchange{payload: "first"}
	s, dir := exportSession(t, ex)
	first, err := s.execExport(context.Background(), port.Command{})
	if err != nil {
		t.Fatalf("export#1: %v", err)
	}
	if _, err := s.execExport(context.Background(), port.Command{}); err != nil {
		t.Fatalf("export#2: %v", err)
	}
	stem := port.FileStem(s.cur.Title)
	base := filepath.Join(dir, stem+".aqx")
	if data, err := os.ReadFile(base); err != nil || string(data) != "first" {
		t.Fatalf("第一份被覆盖了: %q, err = %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, stem+"-2.aqx")); err != nil {
		t.Fatalf("第二份应是 -2 后缀: %v\n%s", err, first)
	}
}

// TestImportSaveFailureKeepsCurrent 落盘失败时不动当前会话：导入的树还没进库，
// 切过去就等于「会话在内存里但重启就没了」。
func TestImportSaveFailureKeepsCurrent(t *testing.T) {
	ex := &fakeExchange{detected: "aqx", imported: newConv(t)}
	s, _ := exchangeSession(t, ex)
	before := s.cur.ID
	s.store.(*memStore).err = errors.New("磁盘满了")
	source := filepath.Join(t.TempDir(), "in.aqx")
	if err := os.WriteFile(source, []byte("PK\x03\x04rest"), 0o644); err != nil {
		t.Fatalf("造源文件: %v", err)
	}
	_, err := s.execImport(context.Background(), port.Command{Args: []string{source}})
	if err == nil || !strings.Contains(err.Error(), "保存导入的会话") {
		t.Fatalf("err = %v, want 保存导入的会话", err)
	}
	if s.cur.ID != before {
		t.Fatal("保存失败不该切走当前会话")
	}
}

// TestExportThirdCopyGetsThree 第三份是 -3，不是再写一次 -2：后缀按「第一个空位」挑。
func TestExportThirdCopyGetsThree(t *testing.T) {
	ex := &fakeExchange{payload: "x"}
	s, dir := exportSession(t, ex)
	for i := 0; i < 3; i++ {
		if _, err := s.execExport(context.Background(), port.Command{}); err != nil {
			t.Fatalf("export#%d: %v", i+1, err)
		}
	}
	stem := port.FileStem(s.cur.Title)
	for _, name := range []string{stem + ".aqx", stem + "-2.aqx", stem + "-3.aqx"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("缺 %s: %v", name, err)
		}
	}
}

// TestExportToDirectoryPath --path 给的是目录时，落进去并用标题派生的文件名。
func TestExportToDirectoryPath(t *testing.T) {
	ex := &fakeExchange{payload: "x"}
	s, _ := exportSession(t, ex)
	dir := t.TempDir()
	out, err := s.execExport(context.Background(), port.Command{Args: []string{"--path", dir}})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	want := filepath.Join(dir, port.FileStem(s.cur.Title)+".aqx")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("应落在目录里的缺省文件名 %s: %v", want, err)
	}
	if !strings.Contains(out, want) {
		t.Fatalf("回执应写真正落点 %s:\n%s", want, out)
	}
}

// TestExportRemovesHalfWrittenFile 写一半失败要删掉半截文件（用户会以为备份成了）。
func TestExportRemovesHalfWrittenFile(t *testing.T) {
	ex := &fakeExchange{payload: "x", writeErr: errors.New("磁盘满了")}
	s, _ := exchangeSession(t, ex)
	target := filepath.Join(t.TempDir(), "half.aqx")
	if _, err := s.execExport(context.Background(), port.Command{Args: []string{"--path", target}}); err == nil ||
		!strings.Contains(err.Error(), "导出失败") {
		t.Fatalf("err = %v, want 导出失败", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("半截文件没删: %v", err)
	}
}

// TestExportRejectsReadOnlyAndUnknown 只读那档与不认识的名字各自报因，都列出可写清单。
func TestExportRejectsReadOnlyAndUnknown(t *testing.T) {
	ex := &fakeExchange{}
	s, _ := exportSession(t, ex)
	_, err := s.execExport(context.Background(), port.Command{Args: []string{"--format", "openwebui"}})
	if err == nil || !strings.Contains(err.Error(), "只认不写") {
		t.Fatalf("openwebui: err = %v", err)
	}
	_, err = s.execExport(context.Background(), port.Command{Args: []string{"--format", "不存在"}})
	if err == nil || !strings.Contains(err.Error(), "aqx") || !strings.Contains(err.Error(), "chatgpt") {
		t.Fatalf("未知格式应列出可写格式: %v", err)
	}
	// 都不该真去写文件。
	if len(ex.exported) != 0 {
		t.Fatalf("拒绝的格式不该走到导出: %v", ex.exported)
	}
}

// TestExportRejectsPositionalText /export 只吃 flag，位置参数按用法错误处理。
func TestExportRejectsPositionalText(t *testing.T) {
	s, _ := exportSession(t, &fakeExchange{})
	if _, err := s.execExport(context.Background(), port.Command{Args: []string{"随便"}}); err == nil ||
		!strings.Contains(err.Error(), "用法") {
		t.Fatalf("err = %v, want 用法", err)
	}
}

// TestImportLandsInNewConversation 导入落成**新**会话：ID 由这里分配、不覆盖既有会话、
// 切过去并清屏回放。
func TestImportLandsInNewConversation(t *testing.T) {
	ex := &fakeExchange{}
	s, _ := exchangeSession(t, ex)
	ex.detected = "aqx"
	ex.imported = newConv(t) // 端口交回的树（ID 是端口那边随手给的）
	ex.importRep = port.ExchangeReport{Nodes: 1, Dropped: []string{"附件 2 个（源文件不含字节）"}}
	source := filepath.Join(t.TempDir(), "in.aqx")
	if err := os.WriteFile(source, []byte("PK\x03\x04rest"), 0o644); err != nil {
		t.Fatalf("造源文件: %v", err)
	}
	oldID := s.cur.ID
	out, err := s.execImport(context.Background(), port.Command{Args: []string{source}})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if s.cur.ID == oldID {
		t.Fatal("导入应切到一个新会话，而不是复用当前会话")
	}
	if s.cur.ID == ex.imported.ID {
		t.Fatal("会话 ID 应由命令面分配，不该沿用端口给的")
	}
	if _, err := s.store.Load(context.Background(), oldID); err != nil {
		t.Fatalf("旧会话应仍在库里: %v", err)
	}
	if _, err := s.store.Load(context.Background(), s.cur.ID); err != nil {
		t.Fatalf("导入的会话应已落盘: %v", err)
	}
	for _, want := range []string{"已导入", "aqx", "1 条消息", "已落成新会话", "附件 2 个"} {
		if !strings.Contains(out, want) {
			t.Fatalf("回执缺 %q:\n%s", want, out)
		}
	}
}

// TestImportUnknownFormat 认不出时不猜：报因并列出能喂的格式。
func TestImportUnknownFormat(t *testing.T) {
	ex := &fakeExchange{detected: ""}
	s, _ := exchangeSession(t, ex)
	source := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(source, []byte("随便写点什么"), 0o644); err != nil {
		t.Fatalf("造源文件: %v", err)
	}
	_, err := s.execImport(context.Background(), port.Command{Args: []string{source}})
	if err == nil || !strings.Contains(err.Error(), "认不出") || !strings.Contains(err.Error(), "sillytavern") {
		t.Fatalf("err = %v, want 认不出并列出格式", err)
	}
}

// TestImportBadFile 文件打不开、导入失败都如实报因，且不改动当前会话。
func TestImportBadFile(t *testing.T) {
	ex := &fakeExchange{detected: "aqx", importErr: errors.New("树里有个悬空父节点")}
	s, _ := exchangeSession(t, ex)
	before := s.cur.ID
	source := filepath.Join(t.TempDir(), "bad.aqx")
	if err := os.WriteFile(source, []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatalf("造源文件: %v", err)
	}
	_, err := s.execImport(context.Background(), port.Command{Args: []string{source}})
	if err == nil || !strings.Contains(err.Error(), "悬空父节点") {
		t.Fatalf("err = %v, want 导入失败原因", err)
	}
	if s.cur.ID != before {
		t.Fatal("导入失败不该切走当前会话")
	}
	if _, err := s.execImport(context.Background(), port.Command{Args: []string{filepath.Join(t.TempDir(), "没有这个")}}); err == nil ||
		!strings.Contains(err.Error(), "打开文件") {
		t.Fatalf("缺文件 err = %v, want 打开文件", err)
	}
}

// TestImportUsage /import 参数个数不对时报用法。
func TestImportUsage(t *testing.T) {
	s, _ := exchangeSession(t, &fakeExchange{})
	for _, args := range [][]string{nil, {"a", "b"}} {
		if _, err := s.execImport(context.Background(), port.Command{Args: args}); err == nil ||
			!strings.Contains(err.Error(), "用法") {
			t.Fatalf("args = %v, err = %v", args, err)
		}
	}
}

// TestExchangeNotWired 未接线时两条命令如实说「本构建没接」，不假装成功。
func TestExchangeNotWired(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	if _, err := s.execExport(context.Background(), port.Command{}); err == nil ||
		!strings.Contains(err.Error(), "未接入导出面") {
		t.Fatalf("export err = %v", err)
	}
	if _, err := s.execImport(context.Background(), port.Command{Args: []string{"x"}}); err == nil ||
		!strings.Contains(err.Error(), "未接入导入面") {
		t.Fatalf("import err = %v", err)
	}
}

// TestExportWithoutDirRequiresPath 没配导出目录就得显式 --path，不往当前目录乱写。
func TestExportWithoutDirRequiresPath(t *testing.T) {
	ex := &fakeExchange{payload: "x"}
	s, _ := exchangeSession(t, ex)
	s.exportsDir = ""
	if _, err := s.execExport(context.Background(), port.Command{}); err == nil ||
		!strings.Contains(err.Error(), "--path") {
		t.Fatalf("err = %v, want 要求 --path", err)
	}
}

// TestImportPathWithSpaces 路径带空格也能导入：参数是界面切好的一个 token（引号由界面消化），
// 命令面不该再按空格猜。
func TestImportPathWithSpaces(t *testing.T) {
	ex := &fakeExchange{}
	s, _ := exchangeSession(t, ex)
	ex.detected = "aqx"
	ex.imported = newConv(t)
	source := filepath.Join(t.TempDir(), "我的 会话.aqx")
	if err := os.WriteFile(source, []byte("PK\x03\x04rest"), 0o644); err != nil {
		t.Fatalf("造源文件: %v", err)
	}
	if _, err := s.execImport(context.Background(), port.Command{Args: []string{source}}); err != nil {
		t.Fatalf("带空格的路径应能导入: %v", err)
	}
}

// TestImportSuggestsQuotesWhenSplit 参数被空格切成几段时说清怎么括，不只报一句「用法错误」。
//
// Windows 上导出目录带空格是常态，最常见的错法就是漏了引号——只说「用法错误」等于让用户
// 去猜是哪一步不对。
func TestImportSuggestsQuotesWhenSplit(t *testing.T) {
	s, _ := exchangeSession(t, &fakeExchange{})
	_, err := s.execImport(context.Background(), port.Command{Args: []string{"C:\\我的", "文档\\x.aqx"}})
	if err == nil || !strings.Contains(err.Error(), "引号") {
		t.Fatalf("err = %v, want 提示用引号", err)
	}
}

// TestFileStem 标题变成文件名：保留字符换掉、长度截断、空标题回退（规则在端口层，
// 命令面与 GUI 保存框共用同一份，所以断言直接打在 port.FileStem 上）。
func TestFileStem(t *testing.T) {
	cases := map[string]string{
		"正常标题":                   "正常标题",
		`a/b\c:d*e?f"g<h>i|j`:    `a-b-c-d-e-f-g-h-i-j`,
		"  两边空白  ":               "两边空白",
		"":                       "",
		"....":                   "",
		strings.Repeat("长", 100): strings.Repeat("长", 60),
	}
	for in, want := range cases {
		if got := port.FileStem(in); got != want {
			t.Errorf("port.FileStem(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHumanBytes 字节数的可读写法（回执用）。
func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:       "0 B",
		1023:    "1023 B",
		1024:    "1.0 KB",
		1536:    "1.5 KB",
		1 << 20: "1.0 MB",
		3 << 30: "3.0 GB",
		1 << 40: "1.0 TB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestExchangeReceiptLines 回执的两行口径：无损不列丢东西，有损列「没有带过去」逐项。
func TestExchangeReceiptLines(t *testing.T) {
	aqx, _ := formatOf(t, "aqx")
	out := exportReceipt(aqx, "/tmp/a.aqx", port.ExchangeReport{Nodes: 3, Bytes: 2048})
	if !strings.Contains(out, "无损") || strings.Contains(out, "没有带过去") {
		t.Fatalf("无损回执:\n%s", out)
	}
	gpt, _ := formatOf(t, "chatgpt")
	out = exportReceipt(gpt, "/tmp/a.json", port.ExchangeReport{
		Nodes: 3, Bytes: 100, Dropped: []string{"思考过程 2 个", "附件 1 个"},
	})
	for _, want := range []string{"有损", "没有带过去", "- 思考过程 2 个", "- 附件 1 个"} {
		if !strings.Contains(out, want) {
			t.Fatalf("有损回执缺 %q:\n%s", want, out)
		}
	}
}

// formatOf 从端口格式表里按名取一条（断言格式事实用）。
func formatOf(t *testing.T, name string) (port.ExchangeFormat, bool) {
	t.Helper()
	for _, f := range port.ExchangeFormats() {
		if f.Name == name {
			return f, true
		}
	}
	t.Fatalf("格式表里没有 %q", name)
	return port.ExchangeFormat{}, false
}

// TestReadHeadShortFile 短文件不算错：不足 512 字节照样能认出形状。
func TestReadHeadShortFile(t *testing.T) {
	head, err := readHead(bytes.NewReader([]byte(`{"messages":[]}`)))
	if err != nil {
		t.Fatalf("readHead: %v", err)
	}
	if string(head) != `{"messages":[]}` {
		t.Fatalf("head = %q", head)
	}
}
