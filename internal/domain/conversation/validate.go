package conversation

import (
	"errors"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// checkNodeShape 校验单个节点的形态约束（与树无关）：
// 空 ID、Root/非 Root 的 Parent 规则、思考/工具分片仅 assistant 可携带（D42/D95）、
// 工具分片 CallID 非空且节点内唯一、Result.CallID 与分片一致（D95）。
// D95 后无跨节点引用检查——调用与结果同片，不存在失联。
func checkNodeShape(m Message) error {
	if m.ID == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidNode)
	}
	if m.Role == RoleRoot {
		// Root 即会话（D19）：唯一空 Parent、空内容、无生成痕迹节点。
		if m.Parent != "" {
			return fmt.Errorf("node %q: %w: root must have empty parent", m.ID, ErrInvalidNode)
		}
		if len(m.Content) > 0 || m.Model != "" || m.Outcome != "" || m.Usage != (Usage{}) {
			return fmt.Errorf("node %q: %w: root must be an empty message", m.ID, ErrInvalidNode)
		}
		return nil
	}
	if m.Parent == "" {
		return fmt.Errorf("node %q: %w: non-root node requires a parent", m.ID, ErrInvalidNode)
	}
	if m.Role != RoleUser && m.Role != RoleAssistant && m.Role != RoleSystem {
		return fmt.Errorf("node %q: %w: unknown role %q", m.ID, ErrInvalidNode, m.Role)
	}
	// 思考分片（D42）与工具分片（D95）仅 assistant 可携带：入树入口统一把关。
	if m.Role != RoleAssistant {
		for _, p := range m.Content {
			if p.Kind == PartThinking {
				return fmt.Errorf("node %q: %w: thinking part only allowed on assistant message", m.ID, ErrInvalidNode)
			}
			if p.Kind == PartTool {
				return fmt.Errorf("node %q: %w: tool part only allowed on assistant message", m.ID, ErrInvalidNode)
			}
		}
	}
	// 工具分片：CallID 节点内唯一且非空、工具名非空、Result.CallID 与分片一致（D95）。
	seen := map[string]bool{}
	for _, p := range m.Content {
		if p.Kind != PartTool {
			continue
		}
		tp := p.Tool
		if tp == nil {
			return fmt.Errorf("node %q: %w: tool part missing payload", m.ID, ErrInvalidNode)
		}
		if tp.CallID == "" {
			return fmt.Errorf("node %q: %w: empty tool call id", m.ID, ErrInvalidNode)
		}
		if tp.Name == "" {
			return fmt.Errorf("node %q: %w: empty tool name", m.ID, ErrInvalidNode)
		}
		if seen[tp.CallID] {
			return fmt.Errorf("node %q: %w: duplicate tool call id %q", m.ID, ErrInvalidNode, tp.CallID)
		}
		seen[tp.CallID] = true
		if tp.Result != nil && tp.Result.CallID != tool.CallID(tp.CallID) {
			return fmt.Errorf("node %q: %w: tool result call id %q does not match part %q",
				m.ID, ErrInvalidNode, tp.Result.CallID, tp.CallID)
		}
	}
	return nil
}

// Validate 整树自检：不变量 1（树合法：唯一实根、双向一致、无环、Head 在树）必须恒成立，
// 另查结构元数据一致性（RevisedFrom 两端存在、Children 父节点存在）。
// 不变量 2（节点不可变）由操作 API 保证，性质测试以快照比对守护（DESIGN §11）。
// 旧不变量 2"存在性引用"随 D95 Tool as Part 取消：调用与结果同片，无跨节点引用。
func (c *Conversation) Validate() error {
	var errs []error

	kidsOf := map[MessageID]map[MessageID]bool{}
	for pid, kids := range c.Children {
		set := map[MessageID]bool{}
		for _, k := range kids {
			if set[k] {
				errs = append(errs, fmt.Errorf("invariant tree: duplicate child edge %q -> %q", pid, k))
			}
			set[k] = true
		}
		kidsOf[pid] = set
		if pid != "" {
			if _, ok := c.Nodes[pid]; !ok {
				errs = append(errs, fmt.Errorf("invariant tree: children key %q not in tree", pid))
			}
		}
	}

	// 不变量 1：唯一实根（D19）——Root 即会话：ID = 会话 ID、Role = root、Parent = ""。
	rootID := MessageID(c.ID)
	if root, ok := c.Nodes[rootID]; !ok {
		errs = append(errs, fmt.Errorf("invariant tree: root %q missing", rootID))
	} else if root.Role != RoleRoot {
		errs = append(errs, fmt.Errorf("invariant tree: node %q must have role root, got %q", rootID, root.Role))
	}
	for nid, m := range c.Nodes {
		if m.Role == RoleRoot && nid != rootID {
			errs = append(errs, fmt.Errorf("invariant tree: stray root-role node %q", nid))
		}
		if m.Parent == "" && nid != rootID {
			errs = append(errs, fmt.Errorf("invariant tree: node %q has empty parent but is not root", nid))
		}
	}
	if top := c.Children[""]; len(top) != 1 || top[0] != rootID {
		errs = append(errs, fmt.Errorf("invariant tree: children[\"\"] = %v, want exactly [%q]", top, rootID))
	}

	for nid, m := range c.Nodes {
		if m.ID != nid {
			errs = append(errs, fmt.Errorf("invariant immutable: node key %q holds message %q", nid, m.ID))
		}
		if err := checkNodeShape(m); err != nil {
			errs = append(errs, err)
			continue
		}

		// 不变量 1：Parent/Children 双向一致。
		if !kidsOf[m.Parent][nid] {
			errs = append(errs, fmt.Errorf("invariant tree: missing child edge %q -> %q", m.Parent, nid))
		}
		for k := range kidsOf[nid] {
			km, ok := c.Nodes[k]
			if !ok {
				errs = append(errs, fmt.Errorf("invariant tree: edge %q -> %q points outside tree", nid, k))
				continue
			}
			if km.Parent != nid {
				errs = append(errs, fmt.Errorf("invariant tree: edge %q -> %q but child parent is %q", nid, k, km.Parent))
			}
		}

		// 不变量 1：无环（沿 Parent 上溯）。
		seen := map[MessageID]bool{nid: true}
		for cur := m.Parent; cur != ""; {
			if seen[cur] {
				errs = append(errs, fmt.Errorf("invariant tree: cycle through node %q", nid))
				break
			}
			seen[cur] = true
			p, ok := c.Nodes[cur]
			if !ok {
				errs = append(errs, fmt.Errorf("invariant tree: node %q has dangling parent %q", nid, cur))
				break
			}
			cur = p.Parent
		}
	}

	// 不变量 1：Head 属于树（初始 = Root；空串即旧格式残留，D19 无空串特例）。
	if c.Head == "" {
		errs = append(errs, errors.New("invariant tree: empty head (legacy virtual root, D19)"))
	} else if _, ok := c.Nodes[c.Head]; !ok {
		errs = append(errs, fmt.Errorf("invariant tree: head %q not in tree", c.Head))
	}

	// 版本链元数据：两端都在树中。
	for newID, oldID := range c.RevisedFrom {
		if _, ok := c.Nodes[newID]; !ok {
			errs = append(errs, fmt.Errorf("metadata: revised_from key %q not in tree", newID))
		}
		if _, ok := c.Nodes[oldID]; !ok {
			errs = append(errs, fmt.Errorf("metadata: revised_from %q -> %q: old node not in tree", newID, oldID))
		}
	}

	return errors.Join(errs...)
}
