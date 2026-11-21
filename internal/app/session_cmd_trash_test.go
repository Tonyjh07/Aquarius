package app

// session_cmd_trash_test.go 会话回收站的命令面行为（D311）：
// /rmconv 删除移进 trash/、/trash 列出还原、/trash empty 二次确认。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// newTrashSession 会话存储用真 storejson（回收站动作都是目录改名，内存替身做不出来）：
// 会话目录与回收站目录各取临时目录。会话按 titles 逐个建好写盘，最后一个为当前会话。
func newTrashSession(t *testing.T, titles ...string) (*Session, *storejson.Store, string, string, *scriptConfirmer) {
	t.Helper()
	convDir := t.TempDir()
	trashDir := filepath.Join(t.TempDir(), "trash")
	store, err := storejson.NewWithKeep(convDir, 1)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conf := &scriptConfirmer{t: t, answers: allowAll(t)}
	rec := &recorder{}
	s, err := NewSession(context.Background(), SessionDeps{
		Store: store, Agent: newAgent(t, &scriptLLM{t: t}, rec, Deps{}, Config{}),
		IDs: &seqIDs{}, Clock: fixedClock{testTime}, UI: rec, Confirmer: conf,
		TrashDir: trashDir,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	cur := s.Current()
	cur.Title = titles[len(titles)-1]
	if err := store.Save(context.Background(), cur); err != nil {
		t.Fatalf("save: %v", err)
	}
	for _, title := range titles[:len(titles)-1] {
		c := conversation.New(conversation.ID(title), title)
		if _, err := c.Append(conversation.RoleUser, []conversation.Part{
			{Kind: conversation.PartText, Text: "hi " + title},
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := store.Save(context.Background(), c); err != nil {
			t.Fatalf("save %q: %v", title, err)
		}
	}
	return s, store, convDir, trashDir, conf
}

// trashConvPath 会话主文件在会话目录 / 回收站目录里的路径。
func trashConvPath(dir string, id conversation.ID) string {
	return filepath.Join(dir, string(id)+".json")
}

// TestRmconvMovesToTrash 删除 = 移进回收站：主文件离开会话目录、出现在回收站，
// 回执说「移进回收站」。
func TestRmconvMovesToTrash(t *testing.T) {
	s, _, convDir, trash, _ := newTrashSession(t, "甲", "乙")
	other := conversation.ID("甲")
	if _, err := os.Stat(trashConvPath(convDir, other)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	out, err := handleCmd(s, "rmconv", "甲")
	if err != nil {
		t.Fatalf("rmconv: %v", err)
	}
	if _, err := os.Stat(trashConvPath(trash, other)); err != nil {
		t.Fatalf("main file not in trash: %v", err)
	}
	if _, err := os.Stat(trashConvPath(convDir, other)); !os.IsNotExist(err) {
		t.Fatalf("main file still in conversations: %v", err)
	}
	if want := i18n.Tf("tree.rmconv.deleted", "id", "甲", "title", "甲"); !strings.Contains(out, want) {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

// TestTrashListShowsAndRestores /trash 列出已删会话；restore 还原回原 ID，/list 再看得见。
func TestTrashListShowsAndRestores(t *testing.T) {
	s, store, _, _, _ := newTrashSession(t, "甲", "乙")
	if _, err := handleCmd(s, "rmconv", "甲"); err != nil {
		t.Fatalf("rmconv: %v", err)
	}
	out, err := handleCmd(s, "trash")
	if err != nil {
		t.Fatalf("trash: %v", err)
	}
	if want := i18n.Tf("tree.trash.header", "n", 1); !strings.Contains(out, want) {
		t.Fatalf("out = %q, want header %q", out, want)
	}
	if !strings.Contains(out, "甲") {
		t.Fatalf("out = %q, want the deleted id 甲", out)
	}
	// 唯一前缀还原。
	if _, err := handleCmd(s, "trash", "restore", "甲"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := store.Load(context.Background(), "甲"); err != nil {
		t.Fatalf("restored conversation not loadable: %v", err)
	}
	out, err = handleCmd(s, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "甲") {
		t.Fatalf("/list = %q, want 甲 back", out)
	}
	// 还原后回收站为空。
	out, err = handleCmd(s, "trash")
	if err != nil {
		t.Fatalf("trash: %v", err)
	}
	if want := i18n.T("tree.trash.empty"); !strings.Contains(out, want) {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

// TestTrashRestoreAmbiguousAndMissing 前缀要唯一；认不出报因。
func TestTrashRestoreAmbiguousAndMissing(t *testing.T) {
	s, store, _, _, _ := newTrashSession(t, "甲", "乙")
	ctx := context.Background()
	for _, id := range []conversation.ID{"ab-one", "ab-two"} {
		c := conversation.New(id, string(id))
		if err := store.Save(ctx, c); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	if _, err := handleCmd(s, "rmconv", "ab-one", "ab-two"); err != nil {
		t.Fatalf("rmconv: %v", err)
	}
	if _, err := handleCmd(s, "trash", "restore", "ab"); err == nil ||
		!strings.Contains(err.Error(), i18n.Tf("tree.trash.ambiguous", "raw", "ab")) {
		t.Fatalf("err = %v, want 前缀歧义", err)
	}
	if _, err := handleCmd(s, "trash", "restore", "zz"); err == nil ||
		!strings.Contains(err.Error(), i18n.Tf("tree.trash.not_found", "raw", "zz")) {
		t.Fatalf("err = %v, want 没有匹配条目", err)
	}
}

// TestTrashRestoreRejectsOccupiedID 还原撞上已占用的 ID：拒绝、不动回收站里的那份。
func TestTrashRestoreRejectsOccupiedID(t *testing.T) {
	s, store, _, trash, _ := newTrashSession(t, "甲", "乙")
	if _, err := handleCmd(s, "rmconv", "甲"); err != nil {
		t.Fatalf("rmconv: %v", err)
	}
	// 同 ID 的新会话占位（正常流程不会发生，真发生说明数据被动过）。
	c := conversation.New("甲", "占位")
	if err := store.Save(context.Background(), c); err != nil {
		t.Fatalf("save occupant: %v", err)
	}
	if _, err := handleCmd(s, "trash", "restore", "甲"); err == nil {
		t.Fatal("restore onto an occupied id should fail")
	}
	if _, err := os.Stat(trashConvPath(trash, "甲")); err != nil {
		t.Fatalf("trash entry must survive the failed restore: %v", err)
	}
}

// TestTrashEmptyNeedsConfirm 清空要二次确认：拒绝则原样，同意则清光。
func TestTrashEmptyNeedsConfirm(t *testing.T) {
	s, _, convDir, trash, conf := newTrashSession(t, "甲", "乙")
	// 会话记忆跟着走：删之前写一份。
	if err := os.WriteFile(filepath.Join(convDir, "甲.memory.md"), []byte("# 记忆"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := handleCmd(s, "rmconv", "甲"); err != nil {
		t.Fatalf("rmconv: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, "甲.memory.md")); err != nil {
		t.Fatalf("memory file must move with the conversation: %v", err)
	}
	conf.answers = []bool{false, true}
	conf.asked = nil // rmconv 的那次确认不算，只看 /trash empty 自己的
	if _, err := handleCmd(s, "trash", "empty"); err != nil {
		t.Fatalf("empty (declined): %v", err)
	}
	if len(conf.asked) != 1 {
		t.Fatalf("确认次数 = %d, want 1", len(conf.asked))
	}
	if want := i18n.Tf("tree.trash.empty_confirm", "n", 1); !strings.Contains(conf.asked[0].prompt, want) {
		t.Fatalf("确认句 = %q, want %q", conf.asked[0].prompt, want)
	}
	if _, err := os.Stat(trashConvPath(trash, "甲")); err != nil {
		t.Fatalf("declined empty must keep the entry: %v", err)
	}
	if _, err := handleCmd(s, "trash", "empty"); err != nil {
		t.Fatalf("empty (allowed): %v", err)
	}
	if _, err := os.Stat(trashConvPath(trash, "甲")); !os.IsNotExist(err) {
		t.Fatalf("allowed empty must remove the entry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, "甲.memory.md")); !os.IsNotExist(err) {
		t.Fatalf("memory file must go with the entry: %v", err)
	}
}

// TestTrashUnavailableReportsWire /trash 在未接入回收站时报未接入，/rmconv 退回硬删。
func TestTrashUnavailableReportsWire(t *testing.T) {
	s, store, _ := newBatchSession(t, "甲", "乙")
	if _, err := handleCmd(s, "trash"); err == nil ||
		!strings.Contains(err.Error(), i18n.T("tree.trash.unavailable")) {
		t.Fatalf("err = %v, want 未接入", err)
	}
	// 硬删退路仍在：删掉就真没了（内存替身没有回收站）。
	var gone conversation.ID
	for id, c := range store.convs {
		if c.Title == "甲" {
			gone = id
		}
	}
	if gone == "" {
		t.Fatal("setup: no conversation titled 甲")
	}
	if _, err := handleCmd(s, "rmconv", string(gone)); err != nil {
		t.Fatalf("rmconv: %v", err)
	}
	if _, ok := store.convs[gone]; ok {
		t.Fatal("fallback delete must hard-remove without the trash")
	}
}
