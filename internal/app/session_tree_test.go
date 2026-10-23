package app

// session_tree_test.go 树结构命令：/branch、/rm、跨会话 /title 与 /rmconv。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestSessionBranchCommand /branch：缺省取 Head、显式 id、Head/修订标记、无分叉与错误路径。
func TestSessionBranchCommand(t *testing.T) {
	store := newMemStore()
	// 一条流：u1 的编辑是用户消息（Fresh）→ 即重新生成（D93），Head 落在回答上。
	s, _, _ := newTestSession(t, store, textStream("新回答"))
	c := buildTree(t, s)

	if _, err := handleCmd(s, "edit", "u1", "改"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	n := revisedID(c, "u1")
	if n == "" || nodeOf(t, c, c.Head).Parent != n {
		t.Fatalf("编辑后 Head 应在新分支回答上: head=%s n=%s", c.Head, n)
	}

	// 显式 id：自身 + 同级（新旧版本对比）+ 下级，带修订标记；新分支已有回答，
	// 同级行不再标 Head（D93）。
	out, err := handleCmd(s, "branch", "u1")
	if err != nil {
		t.Fatalf("branch u1: %v", err)
	}
	for _, want := range []string{
		"当前: u1 [user] 问题",
		"同级分叉（1 条，不含自身）:",
		string(n) + " [user] 改",
		"（修订自 u1）",
		"下级（1 条）:",
		"a1 [assistant] 回复",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("branch 缺 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, string(n)+" [user] 改 ← Head") {
		t.Fatalf("新分支已有回答，同级行不应标 Head:\n%s", out)
	}

	// 缺省 = Head（编辑生成的回答节点，标 Head）。
	out, err = handleCmd(s, "branch")
	if err != nil || !strings.Contains(out, "当前: "+string(c.Head)) ||
		!strings.Contains(out, "[assistant] 新回答 ← Head") {
		t.Fatalf("branch 缺省 = %q, %v", out, err)
	}

	// 无同级分叉 / 错误路径。
	out, err = handleCmd(s, "branch", "a1")
	if err != nil || !strings.Contains(out, "同级分叉: 无") || !strings.Contains(out, "下级（1 条）:\n  u2 [user] 追问") {
		t.Fatalf("无分叉 = %q, %v", out, err)
	}

	// Root：孩子即"顶层消息"，不重复列下级。
	out, err = handleCmd(s, "branch", "convT")
	if err != nil || !strings.Contains(out, "顶层消息（1 条") || strings.Contains(out, "下级") {
		t.Fatalf("root branch = %q, %v", out, err)
	}

	if _, err := handleCmd(s, "branch", "zz"); err == nil || !strings.Contains(err.Error(), "没有节点") {
		t.Fatalf("未知 id err = %v", err)
	}
	if _, err := handleCmd(s, "branch", "a", "b"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("多参数 err = %v", err)
	}
}

// TestSessionRmCommand /rm：二次确认三态（未配置/拒绝/同意）、root 先于确认拒绝、子树计数与 Head 回退。
func TestSessionRmCommand(t *testing.T) {
	t.Run("未配置确认器", func(t *testing.T) {
		store := newMemStore()
		agent := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
		s, err := NewSession(context.Background(), SessionDeps{
			Store: store, Agent: agent, IDs: &seqIDs{}, Clock: fixedClock{testTime},
		})
		if err != nil {
			t.Fatalf("new session: %v", err)
		}
		buildTree(t, s)
		if _, err := handleCmd(s, "rm", "u1"); err == nil || !strings.Contains(err.Error(), "未配置确认器") {
			t.Fatalf("err = %v, want 未配置确认器", err)
		}
	})

	t.Run("root 先于确认拒绝", func(t *testing.T) {
		store := newMemStore()
		s, conf := newConfirmedSession(t, store, nil) // 确认脚本为空：被调用即 Fatalf
		buildTree(t, s)
		if _, err := handleCmd(s, "rm", "convT"); err == nil || !strings.Contains(err.Error(), "会话根节点就是会话本身") {
			t.Fatalf("err = %v, want 会话根节点不可删除", err)
		}
		if len(conf.asked) != 0 {
			t.Fatalf("不应消费确认: %+v", conf.asked)
		}
	})

	t.Run("拒绝", func(t *testing.T) {
		store := newMemStore()
		s, _ := newConfirmedSession(t, store, []bool{false})
		c := buildTree(t, s)
		out, err := handleCmd(s, "rm", "u1")
		if err != nil || !strings.Contains(out, "已取消删除 u1") {
			t.Fatalf("deny = %q, %v", out, err)
		}
		if c.NodeCount() != 5 {
			t.Fatalf("nodes = %d, want 树未改动 5", c.NodeCount())
		}
	})

	t.Run("同意", func(t *testing.T) {
		store := newMemStore()
		s, _ := newConfirmedSession(t, store, []bool{true})
		c := buildTree(t, s)
		out, err := handleCmd(s, "rm", "u1")
		if err != nil {
			t.Fatalf("rm: %v", err)
		}
		// 子树 u1 = {u1,a1,u2} 共 3 条；Head(u2) 回退到最近存活祖先 p。
		if !strings.Contains(out, "共 3 条节点") && !strings.Contains(out, "3 条节点") {
			t.Fatalf("out = %q, want 子树计数 3", out)
		}
		if !strings.Contains(out, "Head → p") {
			t.Fatalf("out = %q, want Head 回退到 p", out)
		}
		for _, id := range []string{"u1", "a1", "u2"} {
			if _, ok := c.Find(conversation.MessageID(id)); ok {
				t.Fatalf("节点 %s 应已删除", id)
			}
		}
		if c.NodeCount() != 2 || c.Head != "p" {
			t.Fatalf("nodes/head = %d/%s, want 2/p", c.NodeCount(), c.Head)
		}
		saved := store.convs[c.ID]
		if saved == nil || saved.Head != "p" || saved.NodeCount() != 2 {
			t.Fatalf("saved = %+v, want 删除结果已落盘", saved)
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
	})

	t.Run("落盘失败回滚", func(t *testing.T) {
		store := newMemStore()
		s, _ := newConfirmedSession(t, store, []bool{true})
		c := buildTree(t, s)
		if err := store.Save(context.Background(), c); err != nil {
			t.Fatalf("预存: %v", err)
		}
		store.err = errors.New("disk full")
		_, err := handleCmd(s, "rm", "u1")
		if err == nil || !strings.Contains(err.Error(), "回滚") || !strings.Contains(err.Error(), "disk full") {
			t.Fatalf("err = %v, want 回滚且保留 disk full", err)
		}
		// 内存与已存副本都回到删除前状态：删除绝不"半生效"。
		if s.cur.NodeCount() != 5 {
			t.Fatalf("nodes = %d, want 回滚到 5", s.cur.NodeCount())
		}
		if saved := store.convs[c.ID]; saved == nil || saved.NodeCount() != 5 {
			t.Fatalf("saved = %v, want 5 节点副本未被改写", saved)
		}
	})

	t.Run("错误路径", func(t *testing.T) {
		store := newMemStore()
		s, _ := newConfirmedSession(t, store, nil)
		buildTree(t, s)
		if _, err := handleCmd(s, "rm"); err == nil || !strings.Contains(err.Error(), "用法") {
			t.Fatalf("用法 err = %v", err)
		}
		if _, err := handleCmd(s, "rm", "zz"); err == nil || !strings.Contains(err.Error(), "没有节点") {
			t.Fatalf("未知 id err = %v", err)
		}
	})
}

// TestSessionTitleTargetsOtherConversation /title --id 改非当前会话标题（D120⑥）：
// 目标落盘、当前会话与其标题不受影响；无标题回显目标标题；--id 缺省作用于当前（回归）；
// 非 --id 的 "--x" 仍按标题文本处理。
func TestSessionTitleTargetsOtherConversation(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	ctx := context.Background()

	idA := s.Current().ID
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "new", Args: []string{"乙调"}}}); err != nil {
		t.Fatalf("/new: %v", err)
	}
	idB := s.Current().ID

	out, err := handleCmd(s, "title", "--id", string(idA), "甲改")
	if err != nil || !strings.Contains(out, string(idA)) || !strings.Contains(out, "甲改") {
		t.Fatalf("title --id = %q, %v", out, err)
	}
	if got := store.convs[idA].Title; got != "甲改" {
		t.Fatalf("甲标题 = %q, want 甲改", got)
	}
	if s.Current().ID != idB || s.Current().Title != "乙调" {
		t.Fatalf("当前会话被误动: %s/%q", s.Current().ID, s.Current().Title)
	}
	if got := store.convs[idB].Title; got != "乙调" {
		t.Fatalf("乙标题 = %q, want 乙调", got)
	}

	// 无标题：回显目标标题（不写盘）。
	if out, err = handleCmd(s, "title", "--id", string(idA)); err != nil || !strings.Contains(out, "甲改") {
		t.Fatalf("title --id 查看 = %q, %v", out, err)
	}
	// 错误前缀：报错、不误写。
	if _, err := handleCmd(s, "title", "--id", "ZZZ", "x"); err == nil || !strings.Contains(err.Error(), "没有会话") {
		t.Fatalf("未知 --id err = %v", err)
	}
	// 缺 --id 参数：用法错误（不消费后续文本）。
	if _, err := handleCmd(s, "title", "--id"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("缺参 err = %v", err)
	}
	// --id 缺省：改当前标题（回归）。
	if _, err := handleCmd(s, "title", "乙改名"); err != nil {
		t.Fatalf("title 当前: %v", err)
	}
	if s.Current().Title != "乙改名" {
		t.Fatalf("当前标题 = %q, want 乙改名", s.Current().Title)
	}
	// "--" 开头但非 --id：按标题文本处理。
	if _, err := handleCmd(s, "title", "--你好", "世界"); err != nil {
		t.Fatalf("title 文本: %v", err)
	}
	if s.Current().Title != "--你好 世界" {
		t.Fatalf("标题 = %q, want --你好 世界", s.Current().Title)
	}
}

// TestSessionRmconvOtherConversation /rmconv 删非当前会话：目标从库中消失、当前会话不动、
// 恰好一次二次确认。
func TestSessionRmconvOtherConversation(t *testing.T) {
	store := newMemStore()
	s, conf := newConfirmedSession(t, store, []bool{true})
	ctx := context.Background()
	idA := s.Current().ID
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "new", Args: []string{"乙"}}}); err != nil {
		t.Fatalf("/new: %v", err)
	}
	idB := s.Current().ID

	out, err := handleCmd(s, "rmconv", string(idA))
	if err != nil || !strings.Contains(out, "已删除会话") {
		t.Fatalf("rmconv = %q, %v", out, err)
	}
	if _, ok := store.convs[idA]; ok {
		t.Fatal("甲会话应已从库中删除")
	}
	if s.Current().ID != idB {
		t.Fatalf("当前会话 = %s, want %s（删非当前不应动 Head）", s.Current().ID, idB)
	}
	if len(conf.asked) != 1 || !strings.Contains(conf.asked[0].prompt, string(idA)) {
		t.Fatalf("确认 = %+v, want 恰一次且提示含目标 id", conf.asked)
	}
}

// TestSessionRmconvCurrentSuccessor 删当前会话 → 继任到其余会话并清屏（D75/D81 口径）。
func TestSessionRmconvCurrentSuccessor(t *testing.T) {
	store := newMemStore()
	rec := &recorder{}
	conf := &scriptConfirmer{t: t, answers: []bool{true}}
	s, err := NewSession(context.Background(), SessionDeps{
		Store: store, Agent: newAgent(t, &scriptLLM{t: t}, rec, Deps{}, Config{}),
		IDs: &seqIDs{}, Clock: fixedClock{testTime}, UI: rec, Confirmer: conf,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	idA := s.Current().ID
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "new", Args: []string{"乙"}}}); err != nil {
		t.Fatalf("/new: %v", err)
	}
	idB := s.Current().ID

	// 切回甲，使「删除对象 = 当前会话」。
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "switch", Args: []string{string(idA)}}}); err != nil {
		t.Fatalf("/switch: %v", err)
	}
	rec.events = nil
	out, err := handleCmd(s, "rmconv", string(idA))
	if err != nil || !strings.Contains(out, "已删除当前会话") || !strings.Contains(out, string(idB)) {
		t.Fatalf("rmconv 当前 = %q, %v", out, err)
	}
	if _, ok := store.convs[idA]; ok {
		t.Fatal("当前会话应已删除")
	}
	if s.Current().ID != idB {
		t.Fatalf("继任 = %s, want %s", s.Current().ID, idB)
	}
	// 继任会话（仅 Root+persona）无历史：清屏、无 notice。
	if names := eventNames(rec.events); len(names) != 1 || names[0] != "clear" {
		t.Fatalf("events = %v, want [clear]", names)
	}
}

// TestSessionRmconvLastCreatesEmpty 删唯一会话 → 新建空会话（保持「总有当前会话」不变量）。
func TestSessionRmconvLastCreatesEmpty(t *testing.T) {
	store := newMemStore()
	s, _ := newConfirmedSession(t, store, []bool{true})
	idA := s.Current().ID

	out, err := handleCmd(s, "rmconv", string(idA))
	if err != nil || !strings.Contains(out, "已切换到") {
		t.Fatalf("rmconv 唯一 = %q, %v", out, err)
	}
	if _, ok := store.convs[idA]; ok {
		t.Fatal("旧会话应已删除")
	}
	cur := s.Current()
	if cur.ID == idA {
		t.Fatal("应新建继任会话（旧 id 不得复用）")
	}
	if cur.Title != defaultTitle {
		t.Fatalf("继任标题 = %q, want %q", cur.Title, defaultTitle)
	}
	if len(store.convs) != 1 {
		t.Fatalf("库 = %d 个会话, want 仅新会话", len(store.convs))
	}
	if saved := store.convs[cur.ID]; saved == nil || saved.ID != cur.ID {
		t.Fatalf("新会话未落盘: %+v", saved)
	}
	if err := cur.Validate(); err != nil {
		t.Fatalf("新会话不合法: %v", err)
	}
}

// TestSessionRmconvGuards /rmconv 的用法/确认器/拒绝三分支。
func TestSessionRmconvGuards(t *testing.T) {
	t.Run("用法与未知 id", func(t *testing.T) {
		s, _ := newConfirmedSession(t, newMemStore(), nil)
		if _, err := handleCmd(s, "rmconv"); err == nil || !strings.Contains(err.Error(), "用法") {
			t.Fatalf("用法 err = %v", err)
		}
		if _, err := handleCmd(s, "rmconv", "ZZZ"); err == nil || !strings.Contains(err.Error(), "没有会话") {
			t.Fatalf("未知 id err = %v", err)
		}
	})

	t.Run("未配置确认器", func(t *testing.T) {
		store := newMemStore()
		agent := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
		s, err := NewSession(context.Background(), SessionDeps{
			Store: store, Agent: agent, IDs: &seqIDs{}, Clock: fixedClock{testTime},
		})
		if err != nil {
			t.Fatalf("new session: %v", err)
		}
		id := s.Current().ID
		if _, err := handleCmd(s, "rmconv", string(id)); err == nil || !strings.Contains(err.Error(), "未配置确认器") {
			t.Fatalf("err = %v, want 未配置确认器", err)
		}
		if _, ok := store.convs[id]; !ok {
			t.Fatal("无确认器时不得删除")
		}
	})

	t.Run("拒绝", func(t *testing.T) {
		store := newMemStore()
		s, _ := newConfirmedSession(t, store, []bool{false})
		id := s.Current().ID
		out, err := handleCmd(s, "rmconv", string(id))
		if err != nil || !strings.Contains(out, "已取消删除会话") {
			t.Fatalf("deny = %q, %v", out, err)
		}
		if _, ok := store.convs[id]; !ok {
			t.Fatal("拒绝后会话不应删除")
		}
		if s.Current().ID != id {
			t.Fatal("拒绝后当前会话不应变")
		}
	})
}
