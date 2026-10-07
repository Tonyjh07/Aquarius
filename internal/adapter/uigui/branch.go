package uigui

import (
	"fmt"
	"image/color"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// blockView 渲染期块视图（live = 本帧实时追加的思考/草稿，非定稿块；
// md = 助手定稿块的 markdown 结构块（D66 复合行）；chip/chipIdx = 工具合并 chip
// 及其块序（D67），text 为空；branch = 分叉条（D81）；bi/id = 所源 model 块序与
// 节点 ID（D92 右键菜单数据键——与 block.id 同源，live 条目为空）；keyBase = 行选
// 键线性基址（transcript 量高期填，整条复制取渲染文本用））。
type blockView struct {
	kind    blockKind
	text    string
	secs    int
	live    bool
	md      []mdBlock
	chip    *toolChip
	chipIdx int
	branch  *branchStrip
	bi      int
	id      conversation.MessageID
	keyBase int
}

// menuable 该条目是否响应气泡右键（D92/D100）：user/assistant 需带节点 ID（编辑/
// 重生成目标）；thinking 定稿块需已盖章（D100②）；工具 chip 态自足（无节点 ID 亦可，
// 复制/查看原文不需要）。live 草稿/notice/分叉条不进菜单。
func (it blockView) menuable() bool {
	switch it.kind {
	case blockUser, blockAssistant, blockThinking:
		return it.id != ""
	case blockTool:
		return it.chip != nil
	}
	return false
}

// selCount 本条目占用的行选键数（D66 双键）：助手复合行 = 块数；工具 chip = 恒 2
// （参数/结果——开合切换不漂移后续行序号，D67）；**分叉条 = 0**（D81：无文本可选，且
// 不占键 → 分叉条的增删不漂移其它行的选择序号）；其余行恒 1。
func (it blockView) selCount() int {
	switch {
	case it.kind == blockBranch:
		return 0
	case it.kind == blockAssistant && len(it.md) > 0:
		return len(it.md)
	case it.kind == blockTool && it.chip != nil:
		return 2
	}
	return 1
}

// branchStrip 分叉条（D81）：气泡正下方的 `◀ i/n ▶`。数据面 = port.TreeView 快照（D80）。
type branchStrip struct {
	blockIdx int                      // 所属块序（m.blocks 下标；渲染期按此对齐插入）
	id       conversation.MessageID   // 所源节点
	ids      []conversation.MessageID // 同级全部节点（创建序，含自身）
	index    int                      // 自身在 ids 中的下标
	right    bool                     // 与气泡同向对齐（user 气泡右对齐 → 分叉条也右对齐）
	slot     int                      // 点击件缓存槽 = 分叉条序（与 ids 无关，只增不改）
}

// branchStrips 本次渲染的分叉条（D81）：按块序为「带节点 ID 且同级 ≥2」的 user/assistant
// 正文块各生成一条；同级只有 1 条（无分叉）不生成。仅事件循环 goroutine 调用。
func (u *UI) branchStrips() []branchStrip {
	if u.opts.Tree == nil {
		return nil
	}
	var out []branchStrip
	for bi, b := range u.m.blocks {
		if b.id == "" {
			continue
		}
		// D89：纯工具轮的锚点在 chip 块上（blockTool），分叉条随之渲染于 chip 下方。
		if b.kind != blockUser && b.kind != blockAssistant && b.kind != blockTool {
			continue
		}
		bi2, ok := u.opts.Tree.Branches(b.id)
		if !ok || len(bi2.IDs) < 2 {
			continue
		}
		out = append(out, branchStrip{
			blockIdx: bi, id: b.id, ids: bi2.IDs, index: bi2.Index,
			right: b.kind == blockUser, slot: len(out),
		})
	}
	return out
}

// branchClick 分叉条左右点击件 get-or-create（D81；照 chipClick 口径按序缓存）。
func (u *UI) branchClick(slot int, next bool) *widget.Clickable {
	if next {
		for len(u.branchNext) <= slot {
			u.branchNext = append(u.branchNext, new(widget.Clickable))
		}
		return u.branchNext[slot]
	}
	for len(u.branchPrev) <= slot {
		u.branchPrev = append(u.branchPrev, new(widget.Clickable))
	}
	return u.branchPrev[slot]
}

// branchArrow 分叉条上的方向键；enabled=false（已在边界）时暗色——真正的不响应在
// updateClicks 的边界夹取里，这里只表达"不可用"。
func (u *UI) branchArrow(gtx layout.Context, glyph string, enabled bool) layout.Dimensions {
	c := textMuted
	if !enabled {
		c = textDim
	}
	l := material.Caption(u.th, glyph)
	l.Color = c
	return l.Layout(gtx)
}

// branchRow 分叉条渲染（D81）：`◀ i/n ▶` 横排，左右各一个点击件。底板 cardTool
// **不透明** → 整条像素可命中（D62 逐像素命中：透明间隙会穿透到下层窗，箭头字形
// 之间的空隙吞点击）。
func (u *UI) branchRow(s *branchStrip, cardR int) (layout.Widget, color.NRGBA, int, bool, bool) {
	n := len(s.ids)
	prev, next := u.branchClick(s.slot, false), u.branchClick(s.slot, true)
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return prev.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return u.branchArrow(gtx, "◀", s.index > 0)
				})
			}),
			layout.Rigid(layout.Spacer{Width: branchGapDp}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(u.th, fmt.Sprintf("%d/%d", s.index+1, n))
				l.Color = textMuted
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: branchGapDp}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return next.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return u.branchArrow(gtx, "▶", s.index < n-1)
				})
			}),
		)
	}, cardTool, cardR, s.right, false
}

// branchGapDp 分叉条内 `◀`/计数/`▶` 的横向间距。
const branchGapDp unit.Dp = 6

// gotoBranch 投递一次分叉切换（D81）：delta = -1 左（更旧版本）/ +1 右（更新版本）。
// 越界不环绕；输入缓冲满时给提示而不是静默丢弃（同 model.submit 口径）。
// D94：落点 = 目标版本的对话末端（port.TreeView.Tail）——/goto 停在消息节点上时其
// 回答不在 Head 路径上，切用户消息版本会只剩半截；端口不可用回退兄弟 id。
func (u *UI) gotoBranch(s branchStrip, delta int) {
	to := s.index + delta
	if to < 0 || to >= len(s.ids) {
		return
	}
	id := s.ids[to]
	if u.opts.Tree != nil {
		if t, ok := u.opts.Tree.Tail(id); ok {
			id = t
		}
	}
	if !u.m.submitCommand("/goto " + string(id)) {
		u.m.add(blockNotice, "[notice] 输入缓冲已满，分叉切换未执行")
	}
}
