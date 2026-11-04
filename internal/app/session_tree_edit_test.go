package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// runCmd 执行一条命令（测试便利）：失败直接终止。
func runCmd(t *testing.T, s *Session, name string, args ...string) string {
	t.Helper()
	out, err := s.execCommand(context.Background(), port.Command{Name: name, Args: args})
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return out
}

// TestTreeEditDefersWritesUntilDone 树编辑同一出口：会话内连着改几次，盘上一次都不动；
// --done 时只写一次，写出去的是最终形状（D258）。
func TestTreeEditDefersWritesUntilDone(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	persona, q1, a1 := reparentFixture(t, s)
	if err := store.Save(context.Background(), s.cur); err != nil { // 进入编辑前先让盘上有完整基线
		t.Fatalf("seed save: %v", err)
	}
	baselineSaves := store.saves

	runCmd(t, s, "tree-edit")
	if active, _ := s.TreeEditState(); !active {
		t.Fatal("tree-edit did not activate")
	}
	// 改三次（末尾回到「挂在 persona 下」）。
	runCmd(t, s, "reparent", string(a1.ID), string(persona.ID))
	runCmd(t, s, "reparent", string(a1.ID), string(q1.ID))
	runCmd(t, s, "reparent", string(a1.ID), string(persona.ID))

	if store.saves != baselineSaves {
		t.Fatalf("saves during edit = %d, want unchanged %d", store.saves, baselineSaves)
	}
	if m, _ := store.convs[s.Current().ID].Find(a1.ID); m.Parent != q1.ID {
		t.Fatalf("saved parent during edit = %q, want untouched %q", m.Parent, q1.ID)
	}
	if active, edits := s.TreeEditState(); !active || edits != 3 {
		t.Fatalf("edit state = %v/%d, want active with 3 edits", active, edits)
	}

	out := runCmd(t, s, "tree-edit", "--done")
	if !strings.Contains(out, i18n.Tf("tree.edit.saved", "n", 3)) {
		t.Fatalf("done output = %q, want 已保存并退出 + 3 处", out)
	}
	if store.saves != baselineSaves+1 {
		t.Fatalf("saves after done = %d, want exactly %d（一次落盘）", store.saves, baselineSaves+1)
	}
	if m, _ := store.convs[s.Current().ID].Find(a1.ID); m.Parent != persona.ID {
		t.Fatalf("saved parent after done = %q, want %q", m.Parent, persona.ID)
	}
	if active, _ := s.TreeEditState(); active {
		t.Fatal("still in tree edit after --done")
	}
}

// TestTreeEditCancelRestoresTree 放弃：内存回到进入时的样子、盘上一次没写，
// 记录与结构一起回滚——不留下半截改动。
func TestTreeEditCancelRestoresTree(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	persona, q1, a1 := reparentFixture(t, s)
	if err := store.Save(context.Background(), s.cur); err != nil { // 进入编辑前先让盘上有完整基线
		t.Fatalf("seed save: %v", err)
	}
	baselineSaves := store.saves

	runCmd(t, s, "tree-edit")
	runCmd(t, s, "reparent", string(a1.ID), string(persona.ID))
	runCmd(t, s, "tree-edit", "--cancel")

	if m, _ := s.Current().Find(a1.ID); m.Parent != q1.ID {
		t.Fatalf("parent after cancel = %q, want %q", m.Parent, q1.ID)
	}
	if n := len(s.Current().Detachments()); n != 0 {
		t.Fatalf("detachments after cancel = %d, want 0", n)
	}
	if store.saves != baselineSaves {
		t.Fatalf("saves after cancel = %d, want unchanged %d", store.saves, baselineSaves)
	}
	if active, _ := s.TreeEditState(); active {
		t.Fatal("still in tree edit after --cancel")
	}
}

// TestTreeEditCancelAfterMessageSaved 会话期间来过新消息（照常落盘），放弃仍要回滚：
// 内存与文件都回到进入编辑时的样子，不能「盘上留着、下次复活」。
func TestTreeEditCancelAfterMessageSaved(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	persona, q1, a1 := reparentFixture(t, s)
	// 先存一次基线，模拟「进入编辑前这份会话已经在盘上」。
	if err := s.store.Save(context.Background(), s.cur); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	before := s.Current().NodeCount()

	runCmd(t, s, "tree-edit")
	runCmd(t, s, "reparent", string(a1.ID), string(persona.ID))
	// 模拟编辑期间来了新消息并落盘。
	if _, err := s.Current().Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "插一句"}}); err != nil {
		t.Fatalf("append during edit: %v", err)
	}
	if err := s.persist(context.Background()); err != nil {
		t.Fatalf("persist during edit: %v", err)
	}
	if got := store.convs[s.Current().ID].NodeCount(); got != before+1 {
		t.Fatalf("saved nodes during edit = %d, want %d", got, before+1)
	}

	runCmd(t, s, "tree-edit", "--cancel")
	if got := s.Current().NodeCount(); got != before {
		t.Fatalf("nodes after cancel = %d, want %d（新消息也回滚）", got, before)
	}
	if m, _ := s.Current().Find(a1.ID); m.Parent != q1.ID {
		t.Fatalf("parent after cancel = %q, want %q", m.Parent, q1.ID)
	}
	if got := store.convs[s.Current().ID].NodeCount(); got != before {
		t.Fatalf("saved nodes after cancel = %d, want %d（文件一并回滚）", got, before)
	}
}

// TestTreeEditShrinkAsksBeforeSaving 上下文条数变少要先问：拒绝就不保存、留在模式里；
// 同意后回执带前后条数。
func TestTreeEditShrinkAsksBeforeSaving(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	c := s.Current()
	persona := c.Path()[1]
	// persona → q1 → a1 → 摘要(system) → q2：把 q2 挪到 persona 下会跨过摘要之上，
	// q2 之上没有摘要了，水位消失 ⇒ 上下文条数变少。
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "q1"}}); err != nil {
		t.Fatalf("append q1: %v", err)
	}
	a1, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "a1"}})
	if err != nil {
		t.Fatalf("append a1: %v", err)
	}
	if err := c.AppendCommitted(conversation.Message{
		ID: conversation.NewMessageID(), Parent: a1.ID, Role: conversation.RoleSystem,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "摘要"}}, CreatedAt: c.UpdatedAt,
	}); err != nil {
		t.Fatalf("append summary: %v", err)
	}
	q2, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "q2"}})
	if err != nil {
		t.Fatalf("append q2: %v", err)
	}
	before := contextCount(c)
	if err := store.Save(context.Background(), s.cur); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	baselineSaves := store.saves

	s.confirmer = &scriptConfirmer{t: t, answers: []bool{false, true}}

	runCmd(t, s, "tree-edit")
	runCmd(t, s, "reparent", string(q2.ID), string(persona.ID))
	if got := contextCount(s.Current()); got >= before {
		t.Fatalf("fixture should shrink context: %d -> %d", before, got)
	}

	// 第一次拒绝保存：留在模式里，盘上没写。
	out := runCmd(t, s, "tree-edit", "--done")
	if !strings.Contains(out, i18n.Tf("tree.edit.save_canceled", "n", 1)) {
		t.Fatalf("declined output = %q, want 已取消保存", out)
	}
	if active, _ := s.TreeEditState(); !active {
		t.Fatal("declining save should keep the edit session open")
	}
	if store.saves != baselineSaves {
		t.Fatalf("declined save wrote to disk: saves %d -> %d", baselineSaves, store.saves)
	}
	// 第二次同意：保存并带前后条数。
	out = runCmd(t, s, "tree-edit", "--done")
	if want := i18n.Tf("tree.edit.saved", "n", 1) +
		i18n.Tf("tree.edit.saved_context", "before", before, "after", contextCount(s.Current())); !strings.Contains(out, want) {
		t.Fatalf("accepted output = %q, want 上下文前后条数", out)
	}
	if store.saves != baselineSaves+1 {
		t.Fatalf("accepted save = %d saves, want %d", store.saves, baselineSaves+1)
	}
}

// TestTreeEditSwitchAsksSaveDiscardCancel 切会话走同一个出口：先问保存、拒绝再问放弃，
// 再拒绝就是取消（不切换、仍在模式里）。
func TestTreeEditSwitchAsksSaveDiscardCancel(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	persona, _, a1 := reparentFixture(t, s)

	conf := &scriptConfirmer{t: t, answers: []bool{false, false}}
	s.confirmer = conf
	runCmd(t, s, "tree-edit")
	runCmd(t, s, "reparent", string(a1.ID), string(persona.ID))

	out := runCmd(t, s, "new", "另一个会话")
	if !strings.Contains(out, i18n.Tf("tree.edit.canceled", "n", 1)) {
		t.Fatalf("new output = %q, want 已取消（两次都拒绝）", out)
	}
	if active, _ := s.TreeEditState(); !active {
		t.Fatal("cancelled switch should keep the edit session")
	}

	// 这次放弃：切走，改动不落盘。
	s.confirmer = &scriptConfirmer{t: t, answers: []bool{false, true}}
	out = runCmd(t, s, "new", "另一个会话")
	if !strings.Contains(out, i18n.Tf("tree.new.done", "id", s.Current().ID)) {
		t.Fatalf("new output = %q, want 已新建会话", out)
	}
	if active, _ := s.TreeEditState(); active {
		t.Fatal("switching should leave the edit session")
	}
}

// TestTreeEditExitAsksLikeSwitch 关开关（--exit）与切会话走同一个三选一：先问保存、
// 拒绝再问放弃、再拒绝就是取消（开关仍是开的）。没改过就直接退，不弹问。
func TestTreeEditExitAsksLikeSwitch(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	persona, q1, a1 := reparentFixture(t, s)

	// 没改过：不问，直接退。
	conf := &scriptConfirmer{t: t, answers: []bool{}}
	s.confirmer = conf
	runCmd(t, s, "tree-edit")
	out := runCmd(t, s, "tree-edit", "--exit")
	if !strings.Contains(out, i18n.T("tree.edit.aborted_clean")) {
		t.Fatalf("exit without edits = %q, want 已退出树编辑", out)
	}
	if active, _ := s.TreeEditState(); active {
		t.Fatal("exit without edits should leave the mode")
	}
	if len(conf.asked) != 0 {
		t.Fatalf("exit without edits asked %d times, want 0", len(conf.asked))
	}

	// 改过：两问都拒绝 = 取消，仍留在模式里。
	conf = &scriptConfirmer{t: t, answers: []bool{false, false}}
	s.confirmer = conf
	runCmd(t, s, "tree-edit")
	runCmd(t, s, "reparent", string(a1.ID), string(persona.ID))
	out = runCmd(t, s, "tree-edit", "--exit")
	if !strings.Contains(out, i18n.Tf("tree.edit.canceled", "n", 1)) {
		t.Fatalf("cancel exit = %q, want 已取消", out)
	}
	if active, edits := s.TreeEditState(); !active || edits != 1 {
		t.Fatalf("state after cancelled exit = %v/%d, want active with 1 edit", active, edits)
	}

	// 拒绝保存、同意放弃：退出且改动不落盘。
	conf = &scriptConfirmer{t: t, answers: []bool{false, true}}
	s.confirmer = conf
	out = runCmd(t, s, "tree-edit", "--exit")
	if !strings.Contains(out, i18n.Tf("tree.edit.aborted", "n", 1)) {
		t.Fatalf("discard exit = %q, want 已放弃", out)
	}
	if active, _ := s.TreeEditState(); active {
		t.Fatal("discarding should leave the mode")
	}
	if m, _ := s.Current().Find(a1.ID); m.Parent != q1.ID {
		t.Fatalf("parent after discard = %q, want %q", m.Parent, q1.ID)
	}
}

// TestTreeEditExitOutsideMode 不在模式里 --exit 不是错误：它要的是「确保在模式外」，
// 已经在外面就是成功（返回一句说明，不抛错）。
func TestTreeEditExitOutsideMode(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	out := runCmd(t, s, "tree-edit", "--exit")
	if !strings.Contains(out, i18n.T("tree.edit.not_active_exit")) {
		t.Fatalf("--exit outside the mode = %q, want 不在树编辑模式", out)
	}
}

func TestTreeEditUsage(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)

	if out := runCmd(t, s, "tree-edit"); !strings.Contains(out, i18n.T("tree.edit.entered")) {
		t.Fatalf("enter = %q", out)
	}
	if out := runCmd(t, s, "tree-edit"); !strings.Contains(out, i18n.T("tree.edit.status_idle")) {
		t.Fatalf("second enter should report status, got %q", out)
	}
	if _, err := s.execCommand(context.Background(), port.Command{Name: "tree-edit", Args: []string{"--nope"}}); err == nil ||
		err.Error() != treeEditUsage() {
		t.Fatalf("unknown flag = %v, want 用法", err)
	}
	runCmd(t, s, "tree-edit", "--done")
	if _, err := s.execCommand(context.Background(), port.Command{Name: "tree-edit", Args: []string{"--done"}}); err == nil {
		t.Fatal("--done outside the mode should fail")
	}
}
