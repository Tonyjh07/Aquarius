package conversation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// useFixedClock 用递增的固定时钟替换 nowFunc：CreatedAt 确定，且与创建顺序一致。
func useFixedClock(t *testing.T) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var tick int64
	nowFunc = func() time.Time {
		return base.Add(time.Duration(atomic.AddInt64(&tick, 1)) * time.Second)
	}
	t.Cleanup(func() { nowFunc = time.Now })
}

func textParts(s string) []Part { return []Part{{Kind: PartText, Text: s}} }

func ids(ms []Message) []MessageID {
	out := make([]MessageID, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

// sameContent 比较节点内容是否字节级一致；Parent 是结构边指针，不参与比较（D16）。
func sameContent(a, b Message) bool {
	x, y := a.Clone(), b.Clone()
	x.Parent, y.Parent = "", ""
	return reflect.DeepEqual(x, y)
}

// propDriver 随机操作序列驱动：覆盖 Append / AppendCommitted / Revise(Fresh|Carry) / Prune / Checkout，
// 并混入必然失败的操作（违规工具分片形态，D95），验证失败路径同样不破坏不变量。
type propDriver struct {
	rng     *rand.Rand
	callSeq int
}

// pickNode 从池中挑节点：includeRoot=false 排除 Root（Root 不可 Revise/Prune，D19）。
func (d *propDriver) pickNode(c *Conversation, includeRoot bool) MessageID {
	var pool []MessageID
	for nid, m := range c.Nodes {
		if m.Role == RoleRoot && !includeRoot {
			continue
		}
		pool = append(pool, nid)
	}
	if len(pool) == 0 {
		return ""
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i] < pool[j] })
	return pool[d.rng.Intn(len(pool))]
}

func (d *propDriver) parent(c *Conversation) MessageID {
	if d.rng.Intn(4) == 0 { // 偶尔挂到任意节点（含 Root=顶层），制造分支
		if nid := d.pickNode(c, true); nid != "" && d.rng.Intn(2) == 0 {
			return nid
		}
		if nid := d.pickNode(c, false); nid != "" {
			return nid
		}
	}
	return c.Head
}

// toolParts 生成 n 个工具分片（调用+结果同片，D95）：CallID 全局递增（节点内唯一即可），
// 结果按概率填充（含 nil = 未执行）。
func (d *propDriver) toolParts(n int) []Part {
	parts := make([]Part, 0, n)
	for i := 0; i < n; i++ {
		d.callSeq++
		tp := &ToolPart{
			CallID: fmt.Sprintf("call_%d", d.callSeq),
			Name:   "think",
			Args:   json.RawMessage(`{}`),
		}
		if d.rng.Intn(2) == 0 {
			tp.Result = &tool.Result{CallID: tool.CallID(tp.CallID), OK: true, Output: "ok"}
		}
		parts = append(parts, Part{Kind: PartTool, Tool: tp})
	}
	return parts
}

// step 执行一个随机操作并返回操作序号（8 = Prune；-1 = 跳过）。
func (d *propDriver) step(t *testing.T, c *Conversation) int {
	t.Helper()
	op := d.rng.Intn(12)
	switch op {
	case 0:
		if _, err := c.Append(RoleUser, textParts(fmt.Sprintf("u%d", d.rng.Int63()))); err != nil {
			t.Fatalf("append user: %v", err)
		}
	case 1:
		// D42：思考分片随 assistant 随机入树——形态校验与三不变量在同一批随机序列中受证。
		content := textParts(fmt.Sprintf("a%d", d.rng.Int63()))
		if d.rng.Intn(2) == 0 {
			content = append([]Part{{Kind: PartThinking, Text: fmt.Sprintf("t%d", d.rng.Int63())}}, content...)
		}
		if _, err := c.Append(RoleAssistant, content); err != nil {
			t.Fatalf("append assistant: %v", err)
		}
	case 2:
		// D95：工具分片（调用+结果同片）随 assistant 随机入树——偶为纯工具轮。
		var content []Part
		if d.rng.Intn(2) == 0 {
			content = textParts(fmt.Sprintf("a%d", d.rng.Int63()))
		}
		content = append(content, d.toolParts(d.rng.Intn(2)+1)...)
		err := c.AppendCommitted(Message{
			ID: NewMessageID(), Parent: d.parent(c), Role: RoleAssistant,
			Content: content, Outcome: OutcomeDone, CreatedAt: nowFunc(),
		})
		if err != nil {
			t.Fatalf("commit assistant with tool parts: %v", err)
		}
	case 3: // 跨节点复用 CallID（D95 后仅节点内唯一）：必须被接受
		var callIDs []string
		for _, m := range c.Nodes {
			for _, p := range m.Content {
				if p.Kind == PartTool && p.Tool != nil {
					callIDs = append(callIDs, p.Tool.CallID)
				}
			}
		}
		if len(callIDs) == 0 {
			return -1
		}
		sort.Strings(callIDs)
		reuse := callIDs[d.rng.Intn(len(callIDs))]
		err := c.AppendCommitted(Message{
			ID: NewMessageID(), Parent: d.parent(c), Role: RoleAssistant,
			Content: []Part{{Kind: PartTool, Tool: &ToolPart{
				CallID: reuse, Name: "think", Args: json.RawMessage(`{}`),
				Result: &tool.Result{CallID: tool.CallID(reuse), OK: true, Output: "ok"},
			}}},
			Outcome: OutcomeDone, CreatedAt: nowFunc(),
		})
		if err != nil {
			t.Fatalf("commit assistant reusing call id: %v", err)
		}
	case 4: // 工具分片形态违规：必须被拒绝（user/system 携带、节点内撞车、Result.CallID 不一致）
		bad := []Message{
			{ID: NewMessageID(), Parent: c.Head, Role: RoleUser,
				Content: []Part{{Kind: PartTool, Tool: &ToolPart{CallID: "bad", Name: "x"}}}, CreatedAt: nowFunc()},
			{ID: NewMessageID(), Parent: c.Head, Role: RoleAssistant,
				Content: []Part{
					{Kind: PartTool, Tool: &ToolPart{CallID: "dup", Name: "x"}},
					{Kind: PartTool, Tool: &ToolPart{CallID: "dup", Name: "x"}},
				}, CreatedAt: nowFunc()},
			{ID: NewMessageID(), Parent: c.Head, Role: RoleAssistant,
				Content: []Part{{Kind: PartTool, Tool: &ToolPart{CallID: "m1", Name: "x",
					Result: &tool.Result{CallID: "m2", OK: true}}}}, CreatedAt: nowFunc()},
		}
		for _, m := range bad {
			if err := c.AppendCommitted(m); err == nil {
				t.Fatalf("invalid tool part accepted: %+v", m)
			}
		}
	case 5, 6:
		target := d.pickNode(c, false)
		if target == "" {
			return -1
		}
		mode := Fresh
		if op == 6 {
			mode = Carry
		}
		if _, err := c.Revise(target, textParts("revised"), mode); err != nil {
			t.Fatalf("revise(%s) %s: %v", mode, target, err)
		}
	case 7: // 空 CallID / 空工具名必须被拒
		tp := &ToolPart{CallID: "", Name: "x"}
		if d.rng.Intn(2) == 0 {
			tp = &ToolPart{CallID: "g1", Name: ""}
		}
		err := c.AppendCommitted(Message{
			ID: NewMessageID(), Parent: c.Head, Role: RoleAssistant,
			Content: []Part{{Kind: PartTool, Tool: tp}}, CreatedAt: nowFunc(),
		})
		if err == nil {
			t.Fatalf("invalid tool part accepted")
		}
	case 8:
		target := d.pickNode(c, false)
		if target == "" {
			return -1
		}
		if err := c.Prune(target); err != nil {
			t.Fatalf("prune %s: %v", target, err)
		}
	case 9:
		id := MessageID(c.ID) // 回 Root（无空串特例，D19）
		if d.rng.Intn(2) == 0 {
			if id = d.pickNode(c, true); id == "" {
				return -1
			}
		}
		if err := c.Checkout(id); err != nil {
			t.Fatalf("checkout %q: %v", id, err)
		}
	case 10: // system 节点（persona / 摘要形态）经 AppendCommitted 入树
		err := c.AppendCommitted(Message{
			ID: NewMessageID(), Parent: d.parent(c), Role: RoleSystem,
			Content: textParts(fmt.Sprintf("s%d", d.rng.Int63())), CreatedAt: nowFunc(),
		})
		if err != nil {
			t.Fatalf("commit system: %v", err)
		}
	case 11: // D19/D18 必然失败路径：root 不可 Append/Commit/Revise/Prune，空串特例已移除
		if _, err := c.Append(RoleSystem, textParts("x")); !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("append(system) = %v, want ErrInvalidNode", err)
		}
		if _, err := c.Append(RoleRoot, nil); !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("append(root) = %v, want ErrInvalidNode", err)
		}
		if err := c.AppendCommitted(Message{ID: NewMessageID(), Role: RoleRoot, CreatedAt: nowFunc()}); !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("commit root = %v, want ErrInvalidNode", err)
		}
		if err := c.AppendCommitted(Message{ID: NewMessageID(), Role: RoleUser, CreatedAt: nowFunc()}); !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("commit parentless user = %v, want ErrInvalidNode", err)
		}
		root := MessageID(c.ID)
		if _, err := c.Revise(root, textParts("x"), Fresh); !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("revise(root) = %v, want ErrInvalidNode", err)
		}
		if err := c.Prune(root); !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("prune(root) = %v, want ErrInvalidNode", err)
		}
		if err := c.Checkout(""); !errors.Is(err, ErrNotFound) {
			t.Fatalf("checkout empty = %v, want ErrNotFound", err)
		}
	}
	return op
}

// checkInvariants 不变量 1–2（DESIGN §4.1）：树合法由 Validate 证明；
// 节点不可变以快照比对证明——非 Prune 步骤节点只增不减，存留节点内容字节级不变。
func checkInvariants(t *testing.T, c *Conversation, snap map[MessageID]Message, allowRemoval bool) {
	t.Helper()
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !allowRemoval {
		for nid := range snap {
			if _, ok := c.Nodes[nid]; !ok {
				t.Fatalf("node %s disappeared without Prune", nid)
			}
		}
	}
	for nid, m := range c.Nodes {
		if want, ok := snap[nid]; ok && !sameContent(want, m) {
			t.Fatalf("node %s content mutated in place", nid)
		}
	}
	for nid, m := range c.Nodes {
		if _, ok := snap[nid]; !ok {
			snap[nid] = m.Clone()
		}
	}
	for nid := range snap {
		if _, ok := c.Nodes[nid]; !ok {
			delete(snap, nid)
		}
	}
}

// TestPropertyInvariants 随机操作序列下两条不变量恒成立（含失败路径）。
func TestPropertyInvariants(t *testing.T) {
	useFixedClock(t)
	for seed := int64(1); seed <= 40; seed++ {
		d := &propDriver{rng: rand.New(rand.NewSource(seed))}
		c := New(NewID(), "invariants")
		snap := map[MessageID]Message{}
		for step := 0; step < 30; step++ {
			op := d.step(t, c)
			checkInvariants(t, c, snap, op == 8)
		}
	}
}

// TestPropertyPathChain Path 恒为 Root→Head 的合法链；Branches 恒为同级分叉的完备划分。
func TestPropertyPathChain(t *testing.T) {
	useFixedClock(t)
	for seed := int64(1); seed <= 40; seed++ {
		d := &propDriver{rng: rand.New(rand.NewSource(seed))}
		c := New(NewID(), "path")
		for step := 0; step < 30; step++ {
			d.step(t, c)

			path := c.Path()
			if len(path) == 0 || path[len(path)-1].ID != c.Head {
				t.Fatalf("seed %d: path %v does not end at head %s", seed, ids(path), c.Head)
			}
			seen := map[MessageID]bool{}
			for i, m := range path {
				if seen[m.ID] {
					t.Fatalf("seed %d: duplicate node %s in path", seed, m.ID)
				}
				seen[m.ID] = true
				if i == 0 {
					if m.ID != MessageID(string(c.ID)) || m.Role != RoleRoot || m.Parent != "" {
						t.Fatalf("seed %d: path does not start at real root: %+v", seed, m)
					}
					continue
				}
				if m.Parent != path[i-1].ID {
					t.Fatalf("seed %d: broken path link %s -> %s", seed, path[i-1].ID, m.ID)
				}
			}

			for _, nid := range nodeIDs(c) {
				self, ok := c.Find(nid)
				if !ok {
					continue
				}
				got := map[MessageID]bool{}
				for _, m := range c.Branches(nid) {
					got[m.ID] = true
				}
				if got[nid] {
					t.Fatalf("seed %d: branches(%s) contains self", seed, nid)
				}
				sibParent := self.Parent
				if self.Role == RoleRoot {
					sibParent = nid // Root 的分叉 = 顶层消息
				}
				for _, sib := range c.Children[sibParent] {
					if sib == nid {
						continue
					}
					if !got[sib] {
						t.Fatalf("seed %d: branches(%s) misses sibling %s", seed, nid, sib)
					}
				}
			}
		}
	}
}

func nodeIDs(c *Conversation) []MessageID {
	out := make([]MessageID, 0, len(c.Nodes))
	for nid := range c.Nodes {
		out = append(out, nid)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TestPropertyCarryEdgeTransfer Carry 边转移：被转移子树零拷贝、身份与内容不变，
// 仅直接孩子的 Parent 边指针改写；孙代及更深字节级不动（D2/D16）。
func TestPropertyCarryEdgeTransfer(t *testing.T) {
	useFixedClock(t)
	for seed := int64(1); seed <= 30; seed++ {
		d := &propDriver{rng: rand.New(rand.NewSource(seed))}
		c := New(NewID(), "carry")
		for i := d.rng.Intn(6) + 3; i > 0; i-- {
			d.step(t, c)
		}
		target := d.pickNode(c, false)
		if target == "" {
			continue
		}

		before := map[MessageID]Message{}
		for nid, m := range c.Nodes {
			before[nid] = m.Clone()
		}
		direct := append([]MessageID{}, c.Children[target]...)
		directSet := map[MessageID]bool{}
		moved := map[MessageID]bool{}
		for _, ch := range direct {
			directSet[ch] = true
			c.collectSubtree(ch, moved)
		}

		m, err := c.Revise(target, textParts("revised"), Carry)
		if err != nil {
			t.Fatalf("seed %d: revise carry: %v", seed, err)
		}

		// 零拷贝、身份不变：节点只多出新版本这一个。
		if len(c.Nodes) != len(before)+1 {
			t.Fatalf("seed %d: nodes = %d, want %d (subtree copied or lost)", seed, len(c.Nodes), len(before)+1)
		}
		for nid := range moved {
			got, ok := c.Nodes[nid]
			if !ok {
				t.Fatalf("seed %d: moved node %s disappeared", seed, nid)
			}
			if !sameContent(before[nid], got) {
				t.Fatalf("seed %d: moved node %s content changed", seed, nid)
			}
			if directSet[nid] {
				if got.Parent != m.ID {
					t.Fatalf("seed %d: direct child %s parent = %s, want %s", seed, nid, got.Parent, m.ID)
				}
			} else if got.Parent != before[nid].Parent {
				t.Fatalf("seed %d: deep node %s re-parented", seed, nid)
			}
		}
		// 修订目标本身字节级不变（含 Parent）：成为旧版本叶子。
		if !reflect.DeepEqual(c.Nodes[target], before[target]) {
			t.Fatalf("seed %d: revised target %s mutated", seed, target)
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("seed %d: validate: %v", seed, err)
		}
	}
}
