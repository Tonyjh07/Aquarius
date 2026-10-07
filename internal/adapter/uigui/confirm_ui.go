package uigui

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// factsCard logo 悬停事实卡内容（D82/S1-1g，§15.1）：profile · 会话（标题+ID 前缀）·
// 模型（权限档/effort）· 上下文占用（Q6：精确优先 est 兜底，分母 max_context_tokens）·
// 用量（Path 累计 + 有实测时上轮）。快照未就绪（零值）→ nil（调用方回退启动提示）。
// 纯逻辑（读 Status 回调后不触 GUI 状态），可测。
func (u *UI) factsCard() []string {
	var st Status
	if u.opts.Status != nil {
		st = u.opts.Status()
	}
	f := st.Facts
	if f.Title == "" && f.CtxMax == 0 { // 尚未发布（构造早期/计数一直失败）
		return nil
	}
	profile := st.Profile
	if profile == "" { // S4/Q1 前装配根留空 → 占位
		profile = "default"
	}
	title := f.Title
	if title == "" {
		title = "（未就绪）"
	}
	l1 := fmt.Sprintf("%s · %s（%s）", profile, title, shortID(f.ConvID))
	if st.Model != "" {
		l1 += " · " + st.Model
		if st.Level != "" {
			l1 += "（" + st.Level
			if st.Effort != "" {
				l1 += "，" + st.Effort
			}
			l1 += "）"
		}
	}
	var l2 string
	if f.CtxMax > 0 {
		mode := "估算"
		if f.CtxExact {
			mode = "精确"
		}
		l2 = fmt.Sprintf("上下文 %d/%d tokens（%.1f%%，%s）",
			f.CtxTokens, f.CtxMax, 100*float64(f.CtxTokens)/float64(f.CtxMax), mode)
	}
	l3 := fmt.Sprintf("累计 in %d / out %d tokens", f.SumIn, f.SumOut)
	out := []string{l1}
	if l2 != "" {
		out = append(out, l2)
	}
	out = append(out, l3)
	if f.LastIn > 0 || f.LastOut > 0 {
		out = append(out, fmt.Sprintf("上轮 in %d / out %d tokens", f.LastIn, f.LastOut))
	}
	return out
}

// shortID 会话 ID 展示前缀（D82）：前 8 位，短 ID 原样。
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// pillContent 胶囊内横排（坐标原点 = 胶囊左上，约束 = 胶囊尺寸；D49/§15.2）：
// [pad16][附件槽20][12][文字 grow][12][展开槽20][12][动作区][pad16]。图标槽是 canvas 的
// 20dp 灰占位（附件/展开未实现、不可点，实现时启用）；动作区仅确认态有内容（[允许/拒绝]）——
// 生成中停止在右圆、胶囊内不占位，idle 时胶囊内只有占位文字。
func (u *UI) pillContent(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(inputPadDp), Right: unit.Dp(inputPadDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// D106 修订⑵：展开态 = 竖排 composer——附件左上/展开右上工具行 + 顶对齐多行
		// 编辑区；确认态仍走单行布局（原因编辑器，灰槽本就隐藏）。
		if u.expanded && u.m.confirm == nil {
			return u.pillContentExpanded(gtx)
		}
		return layout.Flex{
			Axis:      layout.Horizontal,
			Alignment: layout.Middle,
		}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.m.confirm != nil {
					return layout.Dimensions{} // D86：确认态灰槽隐藏（本就不可点占位）
				}
				// D104 附件槽实装：无暂存 = 20dp 可点图标槽；有暂存 = chip（点取消）。
				if u.m.stagedFile != "" {
					return u.attachChip(gtx)
				}
				return u.attachSlot(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				inset := layout.Inset{Left: unit.Dp(inputGapDp), Right: unit.Dp(inputGapDp)}
				return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					// 约束 Min.Y 被 Exact 拉满会把编辑器/提示行顶到盒顶（Flex Middle 对满高盒
					// 无效 → 文本视觉偏上）：放开 Min 让其返回自然行高，由 Flex 垂直居中（§15.2）。
					gtx.Constraints.Min.Y = 0
					if u.m.confirm != nil {
						// D86：原因编辑器（拒绝原因，可留空；Enter = 拒绝附原因）。
						re := material.Editor(u.th, &u.reasonEd, "reason……")
						re.TextSize = unit.Sp(15)
						dims := re.Layout(gtx)
						if u.inFadePass && u.caretFocused {
							u.drawReasonCaret(gtx, dims) // 同 D62：fade pass caret 自绘
						}
						return dims
					}
					ed := material.Editor(u.th, &u.editor, "Ask anything or type a command...")
					ed.TextSize = unit.Sp(15)
					dims := ed.Layout(gtx)
					if u.inFadePass && u.caretFocused {
						u.drawCaret(gtx, dims) // D62：fade pass 无 Focused 态，caret 自绘
					}
					return dims
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.m.confirm != nil {
					return layout.Dimensions{} // D86：确认态灰槽隐藏（本就不可点占位）
				}
				return u.expandSlot(gtx) // D106：点击切换展开/收起，图标随态
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.m.confirm == nil {
					return layout.Dimensions{}
				}
				// D86 确认动作区：左→右 [✗ 拒绝][✓ 允许][🔑 提升权限]（⌀36、间距 8；
				// 右内边距 12 = 胶囊 padding 16 − 4 圆形光学校正）。
				return layout.Inset{Left: unit.Dp(inputGapDp), Right: unit.Dp(-confirmBtnOptDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					kids := []layout.FlexChild{
						layout.Rigid(confirmBtn(gtx, &u.denyBtn, actionDeny, glyphDeny)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(confirmBtnGapDp)}.Layout(gtx,
								confirmBtn(gtx, &u.allowBtn, actionAllow, glyphAllow))
						}),
					}
					if u.confirmElevatable() {
						kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(confirmBtnGapDp)}.Layout(gtx,
								confirmBtn(gtx, &u.elevateBtn, actionElevate, glyphKey))
						}))
					}
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, kids...)
				})
			}),
		)
	})
}

// expandSlot 展开槽（D106）：点击切换展开/收起，图标随态（四角括号外扩 ↔ 内收）。
func (u *UI) expandSlot(gtx layout.Context) layout.Dimensions {
	d := gtx.Dp(inputIconDp)
	draw := drawMaximize
	if u.expanded {
		draw = drawMinimize
	}
	return u.expandBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		box := image.Rectangle{Max: image.Pt(d, d)}
		draw(gtx, box)
		return layout.Dimensions{Size: box.Size()}
	})
}

// pillContentExpanded 展开态胶囊内竖排（D106 修订⑵⑸）：[工具行 20 + 顶距 10][编辑区
// 自然高]——附件左上、展开右上（顶距避开 24dp 圆角曲线），编辑器**顶对齐**（Vertical
// Flex 默认 Start，不居中）。
func (u *UI) pillContentExpanded(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(pillToolTopDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						// 附件左上（D104：无暂存 = 图标槽、有暂存 = chip）。
						if u.m.stagedFile != "" {
							return u.attachChip(gtx)
						}
						return u.attachSlot(gtx)
					}),
					layout.Flexed(1, layout.Spacer{}.Layout),
					layout.Rigid(u.expandSlot), // 展开右上
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = 0 // 自然高顶对齐（原 Flex Middle 居中——首行悬半空）
			ed := material.Editor(u.th, &u.editor, "Ask anything or type a command...")
			ed.TextSize = unit.Sp(15)
			dims := ed.Layout(gtx)
			if u.inFadePass && u.caretFocused {
				u.drawCaret(gtx, dims) // D62：fade pass 无 Focused 态，caret 自绘
			}
			return dims
		}),
	)
}

// confirmBtn 确认态动作圆钮（D86：⌀36 实色圆 + 白色字形，点击热区即整圆）。
func confirmBtn(gtx layout.Context, cl *widget.Clickable, fill color.NRGBA, glyph func(gtx layout.Context, d int, fill color.NRGBA)) func(gtx layout.Context) layout.Dimensions {
	return func(gtx layout.Context) layout.Dimensions {
		d := gtx.Dp(confirmBtnDp)
		draw := func(gtx layout.Context) layout.Dimensions {
			box := image.Rectangle{Max: image.Pt(d, d)}
			paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, d/2).Op(gtx.Ops))
			glyph(gtx, d, fill)
			return layout.Dimensions{Size: image.Pt(d, d)}
		}
		return cl.Layout(gtx, draw)
	}
}

// glyphDeny ✗（两对角白杆）。
func glyphDeny(gtx layout.Context, d int, _ color.NRGBA) {
	a := float32(d) * 0.16
	cx, cy := float32(d)/2, float32(d)/2
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(cx-a, cy-a))
	p.LineTo(f32.Pt(cx+a, cy+a))
	p.MoveTo(f32.Pt(cx+a, cy-a))
	p.LineTo(f32.Pt(cx-a, cy+a))
	paint.FillShape(gtx.Ops, whiteText, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(3))}.Op())
}

// glyphAllow ✓（短杆下探 + 长臂上挑）。
func glyphAllow(gtx layout.Context, d int, _ color.NRGBA) {
	a := float32(d) * 0.20
	cx, cy := float32(d)/2, float32(d)/2
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(cx-a, cy))
	p.LineTo(f32.Pt(cx-a/3, cy+a*0.7))
	p.LineTo(f32.Pt(cx+a, cy-a*0.7))
	paint.FillShape(gtx.Ops, whiteText, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(3))}.Op())
}

// glyphKey 🔑（环头 + 杆 + 两齿；环 = 白实心圆挖钮底色孔——实色钮上与描边等效，
// 本版 gio 无 clip.Circle，方盒 UniformRRect 即内切圆）。
func glyphKey(gtx layout.Context, d int, fill color.NRGBA) {
	a := float32(d) * 0.20
	cx, cy := float32(d)/2, float32(d)/2
	hx := cx - a*0.9
	headR := a * 0.62
	headBox := image.Rect(int(hx-headR), int(cy-headR), int(hx+headR), int(cy+headR))
	paint.FillShape(gtx.Ops, whiteText,
		clip.UniformRRect(headBox, headBox.Dx()/2).Op(gtx.Ops))
	if inner := headBox.Inset(2); !inner.Empty() {
		paint.FillShape(gtx.Ops, fill,
			clip.UniformRRect(inner, inner.Dx()/2).Op(gtx.Ops))
	}
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(hx+headR, cy)) // 杆：环右缘 → 右端
	p.LineTo(f32.Pt(cx+a, cy))
	p.MoveTo(f32.Pt(cx+a*0.45, cy)) // 齿 1
	p.LineTo(f32.Pt(cx+a*0.45, cy+a*0.55))
	p.MoveTo(f32.Pt(cx+a, cy)) // 齿 2
	p.LineTo(f32.Pt(cx+a, cy+a*0.75))
	paint.FillShape(gtx.Ops, whiteText, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
}

// iconSlot 胶囊内 20dp 图标槽（D49/§15.2：canvas 20×20 灰占位、不可点——附件/展开实现时启用）。
func iconSlot(gtx layout.Context, draw func(gtx layout.Context, box image.Rectangle)) layout.Dimensions {
	d := gtx.Dp(inputIconDp)
	box := image.Rectangle{Max: image.Pt(d, d)}
	draw(gtx, box)
	return layout.Dimensions{Size: box.Size()}
}

// nextPermLevel 权限档序列的下一档（D86）：无下一档（已 full-access / 档名未知）
// 返回 false。
func nextPermLevel(level string) (string, bool) {
	for i, lv := range permLevels {
		if lv == level && i+1 < len(permLevels) {
			return permLevels[i+1], true
		}
	}
	return "", false
}

// confirmElevatable 🔑 提升钮可见性（D86）：仅工具确认（问句并入 chip，confirmChip ≥ 0）
// 且当前档有下一档时显示；/rm 等非工具确认与 full-access 档隐藏。
func (u *UI) confirmElevatable() bool {
	if u.m.confirm == nil || u.m.confirmChip < 0 {
		return false
	}
	if u.opts.Status == nil {
		return false
	}
	_, ok := nextPermLevel(u.opts.Status().Level)
	return ok
}

// confirmElevate 一键提档放行（D86）：经输入通道投 /permission <下一档>（托盘 D73
// 同路径，PersistLevel 写回 config；转写回显由 /permission 报告承载）+ 放行本次——
// 放行是显式点击授权，档位只影响后续判定，二者无时序依赖。投递失败（缓冲满）只
// 降级为放行，不阻塞应答。
func (u *UI) confirmElevate() {
	if next, ok := nextPermLevel(u.opts.Status().Level); ok {
		u.m.submitCommand("/permission " + next)
	}
	u.m.replyConfirm(port.ConfirmAnswer{Allow: true})
}

// confirmTipAt 确认态三钮 tips 判定（D86）：窗口系矩形直采 × OS 命中直证（D85）。
// 返回 (tooltip 文本, 是否命中)；按钮在胶囊右段，tip 右对齐。
func (u *UI) confirmTipAt(cur point) (string, bool) {
	if u.frameSize.X <= 0 || u.frameMetric.PxPerDp <= 0 {
		return "", false
	}
	elevate := u.confirmElevatable()
	rects := confirmBtnRects(u.frameSize, u.frameMetric.Dp, elevate)
	texts := make([]string, len(rects))
	texts[0] = "拒绝"
	texts[1] = "允许"
	if elevate {
		next, _ := nextPermLevel(u.opts.Status().Level)
		texts[2] = "提升权限 → " + next
	}
	for i, r := range rects {
		if u.cursorHitsRect(r, cur) {
			return texts[i], true
		}
	}
	return "", false
}
