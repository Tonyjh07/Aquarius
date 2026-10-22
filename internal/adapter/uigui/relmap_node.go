package uigui

// relmap_node.go 右图节点动作：查看/编辑遮罩、复制 ID、转到、右键菜单上下文。
// 菜单与主界面气泡菜单同一呈现、同一分发（D132）。

import (
	"image"
	"io"
	"strings"

	"gioui.org/io/clipboard"
	"gioui.org/layout"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// inspectNode 点节点 → 开消息查看/编辑遮罩（D139：取代原单击的 /goto/A10；正文经
// port.TreeView.NodeText 在遮罩内取，快照未发布/取不到正文时开只读空卡）。
func (g *relGraph) inspectNode(u *UI, st *historyState, li int) {
	if g.model == nil || li < 0 || li >= len(g.vis.nodes) {
		return
	}
	st.openOverlay(u, g.model.nodes[g.vis.nodes[li]].id, conversation.Fresh, overlayPageRendered)
}

// isRoot 本地索引 li 是否为会话根节点（Role == root）。快照未就绪/越界一律 false。
func (g *relGraph) isRoot(li int) bool {
	return g.nodeRole(li) == conversation.RoleRoot
}

// nodeRole 本地索引 li 的节点角色；模型未就绪/越界 = ""（零值，不等于任何角色）。
func (g *relGraph) nodeRole(li int) conversation.Role {
	if g.model == nil || li < 0 || li >= len(g.vis.nodes) {
		return ""
	}
	return g.model.nodes[g.vis.nodes[li]].role
}

// activateNode 原位左键抬起的落点（2026-10-25 用户批注）：会话根 = 复制会话 ID（轻提示，
// 不弹窗）；其余节点 = 开消息查看/编辑遮罩（D139）。根没有消息内容可查看，复制它的 ID
// 才有意义（根节点 ID 即会话 ID，D19）。
func (g *relGraph) activateNode(gtx layout.Context, u *UI, st *historyState, li int) {
	if g.isRoot(li) {
		g.copyNodeID(gtx, st, li, "已复制会话 ID")
		return
	}
	g.inspectNode(u, st, li)
}

// copyNodeID 把本地索引 li 的节点 ID 写进剪贴板并弹轻提示（剪贴板写入必须在 Gio 帧内）。
// hint 是提示文案（会话根与普通节点措辞不同）。
func (g *relGraph) copyNodeID(gtx layout.Context, st *historyState, li int, hint string) {
	if g.model == nil || li < 0 || li >= len(g.vis.nodes) {
		return
	}
	id := string(g.model.nodes[g.vis.nodes[li]].id)
	if id == "" {
		return
	}
	writeClipboard(gtx, id)
	st.toast.show(hint, gtx.Now)
}

// writeClipboard 帧内写剪贴板文本（gtx.Execute(clipboard.WriteCmd)；Gio 帧内唯一可写点）。
func writeClipboard(gtx layout.Context, text string) {
	gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
}

// gotoNode 投 /goto <id>（A10；图侧右键菜单「转到」经 nodeActGoto 走同路径）。
func (g *relGraph) gotoNode(u *UI, st *historyState, li int) {
	if g.model == nil || li < 0 || li >= len(g.vis.nodes) {
		return
	}
	id := g.model.nodes[g.vis.nodes[li]].id
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "goto", Args: []string{string(id)}}}:
	default:
	}
}

// openNodeMenu 图节点右键开菜单（D132）：与主界面气泡菜单**同一呈现、同一分发**——组好
// 上下文投 shell（requestBubbleMenu），项集/命令见 menu.go 的 nodeMenuItems。
// 会话根不弹菜单（2026-10-25 用户批注：根整份菜单删掉；武装阶段已拦，这里是纵深防御——
// 将来命中链/抬起判定有变动也不会复发）。
func (g *relGraph) openNodeMenu(u *UI, li int) {
	if g.isRoot(li) {
		return
	}
	ctx := g.nodeMenuCtx(u, li)
	if ctx == nil {
		return
	}
	u.requestBubbleMenu(ctx)
}

// nodeMenuCtx 组装图节点菜单上下文（D132）：
//   - id/角色 → 档位（user/assistant 走气泡七项；root/system 只给复制/查看原文）；
//   - 正文经 `port.TreeView.NodeText` 取**全文**（GraphNode.Snippet 只有 40 字摘要，拿它
//     预填编辑框会在提交时把消息截断成摘要——D132 的要点）；取不到（节点无正文/快照
//     未发布）→ edit/copy 空，项集随之收敛为图侧三项（nodeMenuItems 的 hasText 分支）；
//   - part = -1（缺省合段，/edit 语义：全部正文分片并为一条；与 NodeText 的拼接口径互逆）；
//   - raw = 正文原文（与主界面「原文 · <id>」同款标题）。
func (g *relGraph) nodeMenuCtx(u *UI, li int) *bubbleMenuCtx {
	if li < 0 || li >= len(g.vis.nodes) || g.model == nil {
		return nil
	}
	n := g.model.nodes[g.vis.nodes[li]]
	if n.id == "" {
		return nil
	}
	ctx := &bubbleMenuCtx{id: n.id, kind: relNodeKind(n.role), part: -1, graph: true}
	// D152：会话根与会话首节点人格之外都可以插摘要（父是结构根的 system = persona）。
	ctx.canSummary = n.role != conversation.RoleRoot &&
		!(n.parent >= 0 && n.parent < len(g.model.nodes) &&
			g.model.nodes[n.parent].role == conversation.RoleRoot)
	body := ""
	if u.opts.Tree != nil {
		if t, ok := u.opts.Tree.NodeText(n.id); ok {
			body = sanitizeControl(t) // 与气泡块同口径：控制序列不进菜单文本（§9）
		}
	}
	ctx.edit, ctx.copy = body, body
	ctx.raw = rawContent{title: "原文 · " + shortID(string(n.id)), text: body}
	return ctx
}

// relNodeKind 节点角色 → 气泡菜单档（D132）：user/assistant 与主界面气泡同档；root/system
// 不可编辑（blockPlain 分支只给复制/查看原文）。
func relNodeKind(r conversation.Role) blockKind {
	switch r {
	case conversation.RoleUser:
		return blockUser
	case conversation.RoleAssistant:
		return blockAssistant
	}
	return blockPlain
}

// relInBox 屏幕点是否在视口盒内（float32 版）。
func relInBox(box image.Rectangle, x, y float32) bool {
	return x >= float32(box.Min.X) && x <= float32(box.Max.X) &&
		y >= float32(box.Min.Y) && y <= float32(box.Max.Y)
}
