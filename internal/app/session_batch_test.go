package app

// session_batch_test.go 批量选择落到命令面的两件事（§5.16 / D249）：
// `/rmconv` 收多个 ID（先全部解析、再一次确认、最后才动库）、`/export --all|--ids`。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newBatchSession 造一个有若干个会话的会话（每个会话带一条正文，便于核对条数）。
func newBatchSession(t *testing.T, titles ...string) (*Session, *memStore, *scriptConfirmer) {
	t.Helper()
	store := newMemStore()
	conf := &scriptConfirmer{t: t, answers: allowAll(t)}
	rec := &recorder{}
	s, err := NewSession(context.Background(), SessionDeps{
		Store: store, Agent: newAgent(t, &scriptLLM{t: t}, rec, Deps{}, Config{}),
		IDs: &seqIDs{}, Clock: fixedClock{testTime}, UI: rec, Confirmer: conf,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	// 第一个会话是构造时建的（默认标题），其余用 /new 逐个建。
	for i, title := range titles {
		if i > 0 {
			cmd := port.Command{Name: "new"}
			if title != "" {
				cmd.Args = []string{title}
			}
			if _, err := s.Handle(context.Background(), port.UserInput{Command: &cmd}); err != nil {
				t.Fatalf("/new %q: %v", title, err)
			}
		}
		if title != "" {
			s.Current().Title = title
		}
		if _, err := s.Current().Append(conversation.RoleUser, []conversation.Part{
			{Kind: conversation.PartText, Text: "hi " + title},
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := store.Save(context.Background(), s.Current()); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	return s, store, conf
}

// allowAll 满足任意次确认（确认次数由调用方按需覆盖）。
func allowAll(t *testing.T) []bool {
	t.Helper()
	const n = 8
	out := make([]bool, n)
	for i := range out {
		out[i] = true
	}
	return out
}

// TestRmconvDeletesSeveralInOneConfirm 批量删除：确认只问一次、两个都删、当前会话不动。
func TestRmconvDeletesSeveralInOneConfirm(t *testing.T) {
	s, store, conf := newBatchSession(t, "甲", "乙", "丙")
	ids := make([]string, 0, 3)
	for id := range store.convs {
		ids = append(ids, string(id))
	}
	// 当前会话是「丙」：删另外两个。
	cur := s.Current().ID
	var del []string
	for _, id := range ids {
		if conversation.ID(id) != cur {
			del = append(del, id)
		}
	}
	out, err := handleCmd(s, "rmconv", del[0], del[1])
	if err != nil {
		t.Fatalf("rmconv: %v", err)
	}
	if len(conf.asked) != 1 {
		t.Fatalf("确认次数 = %d, want 1（一次确认删一批）", len(conf.asked))
	}
	if !strings.Contains(conf.asked[0].prompt, "2 个会话") {
		t.Fatalf("确认句应写清删几个: %s", conf.asked[0].prompt)
	}
	for _, id := range del {
		if _, ok := store.convs[conversation.ID(id)]; ok {
			t.Fatalf("会话 %s 应已删除", id)
		}
	}
	if s.Current().ID != cur {
		t.Fatalf("当前会话 = %s, want %s（删的不是当前会话）", s.Current().ID, cur)
	}
	if !strings.Contains(out, "已删除 2 个会话") {
		t.Fatalf("回执 = %q, want 已删除 2 个会话", out)
	}
}

// TestRmconvBadIDDeletesNothing 有一个 ID 解析不出 → 一个都不删、不弹确认（半截删除比不删更坏）。
func TestRmconvBadIDDeletesNothing(t *testing.T) {
	s, store, conf := newBatchSession(t, "甲", "乙")
	before := len(store.convs)
	good := s.Current().ID
	if _, err := handleCmd(s, "rmconv", string(good), "ZZZ没有这个"); err == nil ||
		!strings.Contains(err.Error(), "没有会话") {
		t.Fatalf("err = %v, want 没有会话", err)
	}
	if len(store.convs) != before {
		t.Fatalf("库 = %d 个会话, want %d（一个都不该删）", len(store.convs), before)
	}
	if len(conf.asked) != 0 {
		t.Fatalf("解析失败时不该弹确认，实际 %d 次", len(conf.asked))
	}
}

// TestRmconvCurrentWithBatchSkipsDeleted 批量删到当前会话时，继任者必须是**不在本批**里的那个——
// 继任到一个刚删掉的 ID 会让下一次保存把它写回来（用户看到「删了又回来」）。
func TestRmconvCurrentWithBatchSkipsDeleted(t *testing.T) {
	s, store, _ := newBatchSession(t, "甲", "乙", "丙")
	ctx := context.Background()
	// 库里三个会话：把「甲」当当前会话，本批删「甲」和「乙」，继任者只能是「丙」。
	var ids []conversation.ID
	for id := range store.convs {
		ids = append(ids, id)
	}
	var cur, keep conversation.ID
	for _, id := range ids {
		switch store.convs[id].Title {
		case "甲":
			cur = id
		case "乙":
			// 本批也删
		case "丙":
			keep = id
		}
	}
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{
		Name: "switch", Args: []string{string(cur)},
	}}); err != nil {
		t.Fatalf("/switch: %v", err)
	}
	var del []string
	for _, id := range ids {
		if id != keep {
			del = append(del, string(id))
		}
	}
	out, err := handleCmd(s, "rmconv", del[0], del[1])
	if err != nil {
		t.Fatalf("rmconv: %v", err)
	}
	if s.Current().ID != keep {
		t.Fatalf("继任 = %s, want %s（不得继任到本批删掉的会话）", s.Current().ID, keep)
	}
	if _, ok := store.convs[keep]; !ok {
		t.Fatal("继任会话不该被删掉")
	}
	if !strings.Contains(out, "已切换到") {
		t.Fatalf("回执应写明已切换: %q", out)
	}
}

// TestRmconvSingleKeepsOldWording 单个 ID 的用法与回执与今天逐字相同。
func TestRmconvSingleKeepsOldWording(t *testing.T) {
	s, store, conf := newBatchSession(t, "甲")
	other := conversation.ID("conv-other")
	if err := store.Save(context.Background(), conversation.New(other, "别的")); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := handleCmd(s, "rmconv", string(other))
	if err != nil {
		t.Fatalf("rmconv: %v", err)
	}
	if !strings.Contains(out, "已删除会话 "+string(other)) {
		t.Fatalf("回执 = %q, want 单个 ID 的老说法", out)
	}
	if len(conf.asked) != 1 || !strings.Contains(conf.asked[0].prompt, "确认删除会话") {
		t.Fatalf("确认句 = %+v, want 单个会话的老说法", conf.asked)
	}
}

// TestExportAllWritesWholeLibrary --all 写出整库（List 序），落点用中性缺省名。
func TestExportAllWritesWholeLibrary(t *testing.T) {
	ex := &fakeExchange{payload: "backup"}
	s, dir := exportSession(t, ex)
	ctx := context.Background()
	for _, title := range []string{"乙", "丙"} {
		if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "new", Args: []string{title}}}); err != nil {
			t.Fatalf("/new: %v", err)
		}
		if err := s.store.Save(ctx, s.Current()); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	out, err := s.execExport(ctx, port.Command{Args: []string{"--all"}})
	if err != nil {
		t.Fatalf("export --all: %v", err)
	}
	sums, err := s.store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ex.exportIDs) != len(sums) {
		t.Fatalf("导出 %d 个会话, want %d（整库）", len(ex.exportIDs), len(sums))
	}
	for i, sm := range sums {
		if ex.exportIDs[i] != sm.ID {
			t.Fatalf("第 %d 个 = %s, want %s（List 序 = 左栏顺序）", i, ex.exportIDs[i], sm.ID)
		}
	}
	want := filepath.Join(dir, multiExportStem+".aqx")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("应落在 %s: %v", want, err)
	}
	if !strings.Contains(out, "个会话") || !strings.Contains(out, want) {
		t.Fatalf("多会话回执应写数量与落点:\n%s", out)
	}
}

// TestExportIDsOnlySelected --ids 只写列出的几个，顺序照用户给的（写序即清单顺序）。
func TestExportIDsOnlySelected(t *testing.T) {
	ex := &fakeExchange{payload: "x"}
	s, _ := exportSession(t, ex)
	ctx := context.Background()
	base := s.Current().ID
	for _, title := range []string{"乙", "丙"} {
		if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "new", Args: []string{title}}}); err != nil {
			t.Fatalf("/new: %v", err)
		}
		if err := s.store.Save(ctx, s.Current()); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	sums, err := s.store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var others []conversation.ID
	for _, sm := range sums {
		if sm.ID != s.Current().ID && sm.ID != base {
			others = append(others, sm.ID)
		}
	}
	if len(others) < 1 {
		t.Fatal("测试需要至少一个非当前会话")
	}
	// 给「当前 + 另一个」：写序照参数顺序，与左栏顺序无关。
	if _, err := s.execExport(ctx, port.Command{
		Args: []string{"--ids", string(s.Current().ID) + "," + string(base)},
	}); err != nil {
		t.Fatalf("export --ids: %v", err)
	}
	if len(ex.exportIDs) != 2 {
		t.Fatalf("导出 %d 个会话, want 2", len(ex.exportIDs))
	}
	if ex.exportIDs[0] != s.Current().ID || ex.exportIDs[1] != base {
		t.Fatalf("写序 = %v, want 参数顺序", ex.exportIDs)
	}
}

// TestExportAllAndIDsConflict --all 与 --ids 同时给就报错，不替用户挑一个。
func TestExportAllAndIDsConflict(t *testing.T) {
	s, _ := exportSession(t, &fakeExchange{})
	_, err := s.execExport(context.Background(), port.Command{Args: []string{"--all", "--ids", "x"}})
	if err == nil || !strings.Contains(err.Error(), "只能给一个") {
		t.Fatalf("err = %v, want 互斥报因", err)
	}
}

// TestExportAllRejectsLossyFormat 非 aqx 与 --all 一起用就报错（一份文件装不下多条链）。
func TestExportAllRejectsLossyFormat(t *testing.T) {
	ex := &fakeExchange{}
	s, _ := exportSession(t, ex)
	_, err := s.execExport(context.Background(), port.Command{
		Args: []string{"--all", "--format", "chatgpt"},
	})
	if err == nil || !strings.Contains(err.Error(), "一次只能写一个会话") {
		t.Fatalf("err = %v, want 一次只能写一个会话", err)
	}
	if len(ex.exported) != 0 {
		t.Fatalf("报错时不该走到导出: %v", ex.exported)
	}
}

// TestImportPickPassedThrough --pick 解析后原样交给端口（1 起、逗号或重复都给）。
func TestImportPickPassedThrough(t *testing.T) {
	ex := &fakeExchange{detected: "aqx", imported: newConv(t)}
	s, _ := exchangeSession(t, ex)
	source := filepath.Join(t.TempDir(), "in.aqx")
	if err := os.WriteFile(source, []byte("PK\x03\x04rest"), 0o644); err != nil {
		t.Fatalf("造源文件: %v", err)
	}
	if _, err := s.execImport(context.Background(), port.Command{
		Args: []string{"--pick", "1,3", "--pick", "5", source},
	}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(ex.picks) != 3 || ex.picks[0] != 1 || ex.picks[1] != 3 || ex.picks[2] != 5 {
		t.Fatalf("picks = %v, want [1 3 5]", ex.picks)
	}
}

// TestImportPickRejectsZero --pick 只接受 1 起的序号（0 起写错就导错项）。
func TestImportPickRejectsZero(t *testing.T) {
	s, _ := exchangeSession(t, &fakeExchange{detected: "aqx"})
	source := filepath.Join(t.TempDir(), "in.aqx")
	if err := os.WriteFile(source, []byte("PK\x03\x04rest"), 0o644); err != nil {
		t.Fatalf("造源文件: %v", err)
	}
	for _, raw := range []string{"0", "abc", "-1"} {
		if _, err := s.execImport(context.Background(), port.Command{
			Args: []string{"--pick", raw, source},
		}); err == nil || !strings.Contains(err.Error(), "--pick") {
			t.Fatalf("--pick %q: err = %v, want 报因", raw, err)
		}
	}
}

// TestImportMultiLandsAll 多会话导入：全部落盘、切到第一个、回执逐条列标题与条数。
func TestImportMultiLandsAll(t *testing.T) {
	ex := &fakeExchange{detected: "aqx"}
	ex.importedM = []*conversation.Conversation{newConv(t), newConv(t), newConv(t)}
	ex.importRep = port.ExchangeReport{
		Nodes: 6,
		Entries: []port.ExchangeEntry{
			{Index: 1, Title: "第一个", Nodes: 2},
			{Index: 2, Title: "第二个", Nodes: 3},
			{Index: 3, Title: "第三个", Nodes: 1},
		},
	}
	s, _ := exchangeSession(t, ex)
	source := filepath.Join(t.TempDir(), "in.aqx")
	if err := os.WriteFile(source, []byte("PK\x03\x04rest"), 0o644); err != nil {
		t.Fatalf("造源文件: %v", err)
	}
	before := s.cur.ID
	beforeN := len(s.store.(*memStore).convs)
	out, err := s.execImport(context.Background(), port.Command{Args: []string{source}})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(s.store.(*memStore).convs) != beforeN+3 {
		t.Fatalf("库 = %d 个会话, want %d（原有 + 导入的三个）",
			len(s.store.(*memStore).convs), beforeN+3)
	}
	if s.Current().ID == before {
		t.Fatal("应切到第一个导入的会话")
	}
	for _, want := range []string{"已导入 3 个会话", "第一个 · 2 条", "第二个 · 3 条", "第三个 · 1 条"} {
		if !strings.Contains(out, want) {
			t.Fatalf("回执缺 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "已落成新会话") {
		t.Fatalf("多会话回执不该用单会话的说法:\n%s", out)
	}
}

// TestImportMultiRollsBackOnFailure 第 3 个落盘失败 → 前两个也撤掉，库里一个都不多。
func TestImportMultiRollsBackOnFailure(t *testing.T) {
	ex := &fakeExchange{detected: "aqx"}
	ex.importedM = []*conversation.Conversation{newConv(t), newConv(t), newConv(t)}
	s, _ := exchangeSession(t, ex)
	store := s.store.(*memStore)
	store.saveLimit = 2 // 第三次保存失败
	source := filepath.Join(t.TempDir(), "in.aqx")
	if err := os.WriteFile(source, []byte("PK\x03\x04rest"), 0o644); err != nil {
		t.Fatalf("造源文件: %v", err)
	}
	before := len(store.convs)
	if _, err := s.execImport(context.Background(), port.Command{Args: []string{source}}); err == nil ||
		!strings.Contains(err.Error(), "保存导入的会话") {
		t.Fatalf("err = %v, want 保存导入的会话", err)
	}
	if len(store.convs) != before {
		t.Fatalf("库 = %d 个会话, want %d（半截导入应回滚）", len(store.convs), before)
	}
}
