package uigui

// 会话树关系图——UI 侧派生层（D120⑤，relation-map-design §6.2/§6.3）。
//
// 分工纪律（§6.1）：`port` 只给树事实（`TreeGraph`）；Level/Depth/InPath/权重/预算/标签/
// 扇区/坐标/配色**一律在 UI 侧派生**。本文件实现**确定性的纯函数**（可逐个单测）：
//
//	deriveLevels / deriveDepth / InPath / 子树规模 / 权重查表
//	budgetFilter（A1b/A6/A7）· paletteOf（A2）· shapeOf（类别→形状）
//	weightOf（A4：独立于 degree）· labelVisible（A5）· statementOf（§3.4）
//
// 坐标求解（不确定、只断言不变量）在 relmap_layout.go（第 4 步）。渲染符号
// （visibleNodes/visibleAssertions/…）与帧在 relmap_frame.go（第 7 步）。

import (
	"sort"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// relNode 派生展示节点（与 port.GraphNode 同序；UI 私有，不入 port）。
type relNode struct {
	id       conversation.MessageID
	role     conversation.Role
	snippet  string
	edgeKind port.EdgeKind
	sibIdx   int
	sibCount int
	parent   int // -1 = 结构根
	level    int // 到结构根跳数（根 = 0，几何 y 与颜色的唯一依据）
	depth    int // 到锚点跳数（锚点 = 0；只用于预算/权重，不用于几何）
	inPath   bool
	subtree  int
	weight   float64
}

// relModel 由整树快照派生的展示模型。
type relModel struct {
	nodes    []relNode
	index    map[conversation.MessageID]int
	children [][]int
	anchor   int // 锚点索引；-1 = 锚点不在图内
	total    int // A7「共 N」
}

// relWeight 档位（§2.2；必须写死并被测试钉住——A4：与 degree 无关）。
const (
	relW3 = 1.30 // 锚点（Head）
	relW2 = 1.00 // 主干（InPath）
	relW1 = 0.85 // 分叉位点（有兄弟版本）
	relW0 = 0.70 // 其余
)

// relPalettePeriod 深度色带的周期（Level mod 4，A2）。
const relPalettePeriod = 4

// relDefaultBudget 关系图默认呈现节点数（预算，A5/A7；「−/＋」调整，第 7 步接入按钮）。
const relDefaultBudget = 120

// relNewModel 从整树快照派生模型：结构校验（唯一根）通过才算成功。ok=false = 快照非法
// （空树 / 无根 / 多根 / 父缺失）——UI 侧安全回退为「解析中」态（§5.3）。
func relNewModel(g port.TreeGraph) (*relModel, bool) {
	n := len(g.Nodes)
	if n == 0 {
		return nil, false
	}
	m := &relModel{
		nodes:    make([]relNode, n),
		index:    make(map[conversation.MessageID]int, n),
		children: make([][]int, n),
		total:    g.Total,
		anchor:   -1,
	}
	for i := range g.Nodes {
		m.index[g.Nodes[i].ID] = i
	}
	root := -1
	for i, gn := range g.Nodes {
		nr := relNode{
			id:       gn.ID,
			role:     gn.Role,
			snippet:  gn.Snippet,
			edgeKind: gn.EdgeKind,
			sibIdx:   gn.SiblingIdx,
			sibCount: gn.SiblingCount,
			parent:   -1,
		}
		if gn.Parent != "" {
			p, ok := m.index[gn.Parent]
			if !ok || p == i { // 父缺失/自环 = 非法
				return nil, false
			}
			nr.parent = p
			m.children[p] = append(m.children[p], i)
		} else {
			if root >= 0 { // 多根 = 非法
				return nil, false
			}
			root = i
		}
		m.nodes[i] = nr
	}
	if root < 0 {
		return nil, false
	}

	// Level：自根 BFS（根 = 0）。
	order := make([]int, 0, n)
	queue := []int{root}
	m.nodes[root].level = 0
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		order = append(order, i)
		for _, c := range m.children[i] {
			m.nodes[c].level = m.nodes[i].level + 1
			queue = append(queue, c)
		}
	}
	if len(order) != n { // 环（结构上不可达）
		return nil, false
	}

	// 子树规模：逆 BFS 聚合。
	for i := range m.nodes {
		m.nodes[i].subtree = 1
	}
	for k := len(order) - 1; k >= 0; k-- {
		i := order[k]
		if p := m.nodes[i].parent; p >= 0 {
			m.nodes[p].subtree += m.nodes[i].subtree
		}
	}

	// 锚点。
	if g.Anchor != "" {
		if a, ok := m.index[g.Anchor]; ok {
			m.anchor = a
		}
	}

	m.deriveDepth()
	m.deriveInPath()
	m.deriveWeights()
	return m, true
}

// deriveDepth 无向 BFS：每节点到锚点的跳数（锚点 = 0）。锚点缺失时全部置 -1。
func (m *relModel) deriveDepth() {
	for i := range m.nodes {
		m.nodes[i].depth = -1
	}
	if m.anchor < 0 {
		return
	}
	m.nodes[m.anchor].depth = 0
	// 父子双向遍历（无向）：从锚点向外。
	queue := []int{m.anchor}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		next := m.nodes[i].depth + 1
		relVisit := func(j int) {
			if m.nodes[j].depth < 0 {
				m.nodes[j].depth = next
				queue = append(queue, j)
			}
		}
		if p := m.nodes[i].parent; p >= 0 {
			relVisit(p)
		}
		for _, c := range m.children[i] {
			relVisit(c)
		}
	}
}

// deriveInPath 根 → 锚点唯一路径（A11）：自锚点沿 parent 上溯至根。
func (m *relModel) deriveInPath() {
	if m.anchor < 0 {
		return
	}
	for i := m.anchor; i >= 0; i = m.nodes[i].parent {
		m.nodes[i].inPath = true
	}
}

// deriveWeights 权重查表（A4：只依赖 inPath/锚点/是否分叉，与 degree 无关）。
func (m *relModel) deriveWeights() {
	for i := range m.nodes {
		switch {
		case i == m.anchor:
			m.nodes[i].weight = relW3
		case m.nodes[i].inPath:
			m.nodes[i].weight = relW2
		case m.nodes[i].sibCount > 1:
			m.nodes[i].weight = relW1
		default:
			m.nodes[i].weight = relW0
		}
	}
}

// relPaletteIndex 深度色带下标：Level mod 4（A2：颜色是封闭可枚举量的纯投影）。
func relPaletteIndex(level int) int {
	p := level % relPalettePeriod
	if p < 0 {
		p += relPalettePeriod
	}
	return p
}

// relShape 类别 → 形状（封闭四值，A2 把类别移交形状通道）。
type relShape int

const (
	relShapeCircle  relShape = iota // assistant
	relShapeSquare                  // user
	relShapeDiamond                 // system（人格 / 压缩摘要）
	relShapeRing                    // root（空心圆 = 结构根）
)

// relShapeOf 由 Role 查形状（纯查表，无散落分支）。
func relShapeOf(role conversation.Role) relShape {
	switch role {
	case conversation.RoleRoot:
		return relShapeRing
	case conversation.RoleUser:
		return relShapeSquare
	case conversation.RoleSystem:
		return relShapeDiamond
	default:
		return relShapeCircle
	}
}

// 标签预算阈值（A5；全部为偶然实现档位，§7 可调）。
const (
	relZoomLabelMin  = 0.75 // 标签可见的 zoom 下限（低于即全隐）
	relZoomLabelRef  = 1.50 // 阈值参考 zoom（= 上限）
	relLabelWBase    = 0.70 // 参考 zoom 下的权重阈值（= w0：全显）
	relLabelWSlope   = 0.20 // zoom 每降 1.0 权重阈值上抬量
	relRolePrefixZ   = 1.00 // zoom 低于此值：标签补 Role 前缀（补偿形状退化，§3.2）
	relStatementNone = ""
)

// relLabelThreshold zoom → 权重阈值：zoom 越大阈值越低（标签更多），保证 A5 的单调性
// （zoom 单调下降 ⇒ 可见标签数单调不增）。
func relLabelThreshold(zoom float64) float64 {
	th := relLabelWBase + (relZoomLabelRef-zoom)*relLabelWSlope
	if th < 0 {
		th = 0
	}
	return th
}

// relLabelVisible 标签是否显形（A5：weight 预算 + zoom 下限）。
func relLabelVisible(weight, zoom float64) bool {
	if zoom < relZoomLabelMin {
		return false
	}
	return weight >= relLabelThreshold(zoom)
}

// relLabelText 标签文案：低 zoom 补 Role 前缀（§3.2 代价条目），否则纯摘要。
func relLabelText(m *relModel, i int, zoom float64) string {
	if !relLabelVisible(m.nodes[i].weight, zoom) {
		return ""
	}
	txt := m.nodes[i].snippet
	if zoom < relRolePrefixZ {
		txt = relRolePrefix(m.nodes[i].role) + txt
	}
	return txt
}

// relRolePrefix 低 zoom 下的 Role 前缀（形状退化时的补偿）。
func relRolePrefix(role conversation.Role) string {
	switch role {
	case conversation.RoleUser:
		return "你："
	case conversation.RoleSystem:
		return "系统："
	case conversation.RoleRoot:
		return "会话："
	default:
		return "AI："
	}
}

// relStatementOf 悬停一句话关系陈述（§3.4，必须自足）。convTitle 供会话根陈述。
func relStatementOf(m *relModel, i int, convTitle string) string {
	n := m.nodes[i]
	switch {
	case n.role == conversation.RoleRoot:
		return "会话：" + convTitle + "（第 1 层，共 " + itoa(m.total) + " 条消息）"
	case n.role == conversation.RoleSystem:
		if p := n.parent; p >= 0 && m.nodes[p].role == conversation.RoleRoot {
			return "系统：人格快照（会话首节点）"
		}
		return "系统：压缩摘要（水位节点）"
	case i == m.anchor:
		return relRoleName(n.role) + "：当前对话末端（锚点）"
	case n.inPath:
		return relRoleName(n.role) + "：当前对话主干上的一轮"
	case n.sibCount > 1:
		return relRoleName(n.role) + "：第 " + itoa(n.sibIdx+1) + "/" + itoa(n.sibCount) +
			" 个版本（修订而来）——未走的分支"
	default:
		return relRoleName(n.role) + "：未走的分支"
	}
}

// relRoleName 陈述用角色名。
func relRoleName(role conversation.Role) string {
	switch role {
	case conversation.RoleUser:
		return "你"
	case conversation.RoleSystem:
		return "系统"
	case conversation.RoleRoot:
		return "会话"
	default:
		return "AI"
	}
}

// itoa 小整数转字符串（避免 fmt 依赖，保持纯函数轻量）。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// relVisible 预算切片结果（A6/A7）：节点索引集 + 断言（两端均可见的父子边）。
type relVisible struct {
	nodes []int    // 可见节点索引（含锚点，A1b）
	edges [][2]int // 断言 [from, to]（父 → 子）
}

// relBudget 预算切片（A1b/A6/A7）：以**锚点为中心**的连通洪泛，按权重优先生长——
// 保证「主干完整 + 分叉位点优先 + 远端按层消失」，且结果连通（布局可单根）。
//
//   - 锚点恒在（maxCount ≥ 1）；maxCount=1 → 仅锚点（A1b）。
//   - 逆于「逐个按 weight 全局截断」：全局截断会切断父子链 → 悬空边（违反 A6）。
//   - maxCount 上限钳到 relHardCap（§5.4.3；由 spike 定档的呈现上限）。
//   - 边 = 两端都在可见集里的父子边（A6）。
func relBudget(m *relModel, maxCount int) relVisible {
	n := len(m.nodes)
	if maxCount > relHardCap {
		maxCount = relHardCap
	}
	if maxCount <= 0 {
		maxCount = 1
	}
	vis := make([]bool, n)
	out := relVisible{}
	if m.anchor < 0 { // 锚点缺失（异常数据）：退化为按权重取前 maxCount，仅作兜底
		order := m.byWeightDesc()
		for k := 0; k < maxCount && k < len(order); k++ {
			vis[order[k]] = true
		}
	} else if maxCount >= n {
		for i := range vis {
			vis[i] = true
		}
	} else {
		// 连通洪泛：从锚点出发，前沿按 (weight 降序, level 升序, ID 升序) 取。
		vis[m.anchor] = true
		cand := make([]bool, n)
		kept := 1
		addFrontier := func(i int) {
			if p := m.nodes[i].parent; p >= 0 && !vis[p] {
				cand[p] = true
			}
			for _, c := range m.children[i] {
				if !vis[c] {
					cand[c] = true
				}
			}
		}
		addFrontier(m.anchor)
		for kept < maxCount {
			best := -1
			for i := 0; i < n; i++ {
				if !cand[i] {
					continue
				}
				if best < 0 || m.better(i, best) {
					best = i
				}
			}
			if best < 0 {
				break
			}
			cand[best] = false
			vis[best] = true
			kept++
			addFrontier(best)
		}
	}
	for i := 0; i < n; i++ {
		if !vis[i] {
			continue
		}
		out.nodes = append(out.nodes, i)
		if p := m.nodes[i].parent; p >= 0 && vis[p] {
			out.edges = append(out.edges, [2]int{p, i})
		}
	}
	return out
}

// better 优先级：weight 降序，其次 level 升序（浅层优先），再 ID 升序（确定性）。
func (m *relModel) better(a, b int) bool {
	wa, wb := m.nodes[a].weight, m.nodes[b].weight
	if wa != wb {
		return wa > wb
	}
	if m.nodes[a].level != m.nodes[b].level {
		return m.nodes[a].level < m.nodes[b].level
	}
	return m.nodes[a].id < m.nodes[b].id
}

// byWeightDesc 全节点按 better 排序后的索引序（兜底路径用）。
func (m *relModel) byWeightDesc() []int {
	order := make([]int, len(m.nodes))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return m.better(order[a], order[b]) })
	return order
}

// relGeo 把可见节点映射为布局输入（紧凑索引）+ 锚点在其中的局部下标。
// 父不在可见集时该节点视为根（连通洪泛下仅锚点缺失兜底路径可能出现）。
func relGeo(m *relModel, vis relVisible) (nodes []relGeoNode, anchorLocal int) {
	local := make([]int, len(m.nodes))
	for i := range local {
		local[i] = -1
	}
	for k, gi := range vis.nodes {
		local[gi] = k
	}
	nodes = make([]relGeoNode, len(vis.nodes))
	anchorLocal = -1
	for k, gi := range vis.nodes {
		p := -1
		if gp := m.nodes[gi].parent; gp >= 0 {
			p = local[gp]
		}
		nodes[k] = relGeoNode{Parent: p, Level: m.nodes[gi].level, Weight: m.nodes[gi].weight}
		if gi == m.anchor {
			anchorLocal = k
		}
	}
	return nodes, anchorLocal
}
