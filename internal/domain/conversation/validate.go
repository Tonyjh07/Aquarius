package conversation

import (
	"errors"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// checkNodeShape 校验单个节点的形态约束（与树无关）：
// 空 ID、Root/非 Root 的 Parent 规则、角色与调用/结果的匹配关系、调用 ID 非空且节点内唯一。
func checkNodeShape(m Message) error {
	if m.ID == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidNode)
	}
	if m.Role == RoleRoot {
		// Root 即会话（D19）：唯一空 Parent、空内容、无生成痕迹节点。
		if m.Parent != "" {
			return fmt.Errorf("node %q: %w: root must have empty parent", m.ID, ErrInvalidNode)
		}
		if len(m.Content) > 0 || len(m.ToolCalls) > 0 || m.ToolResult != nil ||
			m.Model != "" || m.Outcome != "" || m.Usage != (Usage{}) {
			return fmt.Errorf("node %q: %w: root must be an empty message", m.ID, ErrInvalidNode)
		}
		return nil
	}
	if m.Parent == "" {
		return fmt.Errorf("node %q: %w: non-root node requires a parent", m.ID, ErrInvalidNode)
	}
	switch m.Role {
	case RoleUser:
		if len(m.ToolCalls) > 0 || m.ToolResult != nil {
			return fmt.Errorf("node %q: %w: user message cannot carry tool calls or results", m.ID, ErrInvalidNode)
		}
	case RoleSystem:
		// persona / 压缩摘要（D20/D21）：纯内容节点，不可携带工具调用或结果。
		if len(m.ToolCalls) > 0 || m.ToolResult != nil {
			return fmt.Errorf("node %q: %w: system message cannot carry tool calls or results", m.ID, ErrInvalidNode)
		}
	case RoleAssistant:
		if m.ToolResult != nil {
			return fmt.Errorf("node %q: %w: assistant message cannot carry tool result", m.ID, ErrInvalidNode)
		}
	case RoleTool:
		if len(m.ToolCalls) > 0 {
			return fmt.Errorf("node %q: %w: tool message cannot carry tool calls", m.ID, ErrInvalidNode)
		}
		if m.ToolResult == nil {
			return fmt.Errorf("node %q: %w: tool message requires tool result", m.ID, ErrInvalidNode)
		}
	default:
		return fmt.Errorf("node %q: %w: unknown role %q", m.ID, ErrInvalidNode, m.Role)
	}
	seen := map[tool.CallID]bool{}
	for _, call := range m.ToolCalls {
		if call.ID == "" {
			return fmt.Errorf("node %q: %w: empty tool call id", m.ID, ErrInvalidNode)
		}
		if seen[call.ID] {
			return fmt.Errorf("node %q: %w: duplicate tool call id %q", m.ID, ErrInvalidNode, call.ID)
		}
		seen[call.ID] = true
	}
	if m.ToolResult != nil && m.ToolResult.CallID == "" {
		return fmt.Errorf("node %q: %w: empty tool result call id", m.ID, ErrInvalidNode)
	}
	return nil
}

// Validate 整树自检：不变量 1（树合法：唯一实根、双向一致、无环、Head 在树）与
// 不变量 2（引用合法）必须恒成立，另查结构元数据一致性（RevisedFrom 两端存在、Children 父节点存在）。
// 不变量 3（节点不可变）由操作 API 保证，性质测试以快照比对守护（DESIGN §11）。
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

	owners := map[tool.CallID]MessageID{} // 调用 ID → 声明节点（唯一性）
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

		for _, call := range m.ToolCalls {
			if prev, dup := owners[call.ID]; dup {
				errs = append(errs, fmt.Errorf("invariant ref: call id %q declared by both %q and %q", call.ID, prev, nid))
			}
			owners[call.ID] = nid
		}
	}

	// 不变量 1：Head 属于树（初始 = Root；空串即旧格式残留，D19 无空串特例）。
	if c.Head == "" {
		errs = append(errs, errors.New("invariant tree: empty head (legacy virtual root, D19)"))
	} else if _, ok := c.Nodes[c.Head]; !ok {
		errs = append(errs, fmt.Errorf("invariant tree: head %q not in tree", c.Head))
	}

	// 不变量 2：存在性引用——tool 节点的 CallID 匹配树中存在的某 assistant 调用。
	for nid, m := range c.Nodes {
		if m.Role != RoleTool {
			continue
		}
		if _, ok := owners[m.ToolResult.CallID]; !ok {
			errs = append(errs, fmt.Errorf("invariant ref: tool node %q references unknown call id %q", nid, m.ToolResult.CallID))
		}
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
