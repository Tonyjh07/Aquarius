package conversation

import (
	"strings"
	"testing"
)

// TestValidateRejectsEditionCycle 版本链成环要被 Validate 查出（D255）：
// 链上两端都在树里、结构边也合法，只有链自己是坏的——不查就会「沿版本链找上游」永不终止。
// Revise 自己造不出环，这里手工接回起点来模拟读盘或被改父带坏的链。
func TestValidateRejectsEditionCycle(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")
	u2, err := c.Revise(u1.ID, textParts("q1'"), Fresh)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	u3, err := c.Revise(u2.ID, textParts("q1''"), Fresh)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("healthy chain should validate: %v", err)
	}

	// u1 → u3 把三个节点接成环：u2→u1→u3→u2。
	c.revisedFrom[u1.ID] = u3.ID
	err = c.Validate()
	if err == nil || !strings.Contains(err.Error(), "revised_from cycle") {
		t.Fatalf("validate = %v, want revised_from cycle", err)
	}
	// 同一个环只报一次——环上每个节点各报一遍的话，三方环会刷三条。
	if n := strings.Count(err.Error(), "revised_from cycle"); n != 1 {
		t.Fatalf("cycle reported %d times, want 1: %v", n, err)
	}
}

// TestValidateRejectsEditionSelfLoop 自指（A 修订自 A）也是环，且不能因为「没走一步」被漏掉。
func TestValidateRejectsEditionSelfLoop(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")

	c.revisedFrom[u1.ID] = u1.ID
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "revised_from cycle") {
		t.Fatalf("validate = %v, want revised_from cycle", err)
	}
}

// TestValidateAllowsEditionChainAcrossParents 版本链跨父之后仍然合法（D255 并存口径）：
// 旧节点无子、新版本挂在别的父下，结构边与版本链各自成立，Validate 不该拦。
func TestValidateAllowsEditionChainAcrossParents(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")
	a1 := mustAppend(t, c, RoleAssistant, "a1")
	// 另起一版 u1：与 u1 结构同级（同父 Root），下挂一条新回答。
	u1b, err := c.Revise(u1.ID, textParts("q1'"), Fresh)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if err := c.Checkout(u1b.ID); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	a1b := mustAppend(t, c, RoleAssistant, "a1'")
	// 版本链指向旧回答 a1，而两者父不同（u1 与 u1b）——这正是改父之后会出现的形状。
	c.revisedFrom[a1b.ID] = a1.ID

	if err := c.Validate(); err != nil {
		t.Fatalf("cross-parent edition chain should validate: %v", err)
	}
}
