package uigui

import (
	"encoding/json"
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// chipClick 工具 chip 头部点击控件 get-or-create（D67：按块序缓存，块只增不减）。
func (u *UI) chipClick(i int) *widget.Clickable {
	for len(u.chipClicks) <= i {
		u.chipClicks = append(u.chipClicks, new(widget.Clickable))
	}
	return u.chipClicks[i]
}

// chipIsOpen chip 展开态（D67）：用户开合按块序缓存；待确认 chip 强制展开
// （权限问句不可折叠隐藏），应答后回落用户选择。
func (u *UI) chipIsOpen(idx int, c *toolChip) bool {
	if c != nil && c.confirmQ != "" && c.confirmA == "" && u.m.confirm != nil {
		return true
	}
	return u.chipOpen[idx]
}

// chipHeaderText chip 头部行（D67）：▸/▾ 开合指示 + 🔧 名称 + 参数首行预览 + 状态
// （… 运行中 / 待确认 / ✓ / ✗）。
func chipHeaderText(c *toolChip, open bool) string {
	arrow, status := "▸", "…"
	if open {
		arrow = "▾"
	}
	if c.done {
		status = "✓"
		if !c.ok {
			status = "✗"
		}
	} else if c.confirmQ != "" {
		status = "待确认"
	}
	args := strings.TrimSpace(c.args)
	if i := strings.IndexByte(args, '\n'); i >= 0 {
		args = args[:i]
	}
	if r := []rune(args); len(r) > 40 {
		args = string(r[:40]) + "…"
	}
	line := "🔧 " + c.name
	if args != "" {
		line += " · " + args
	}
	return arrow + " " + line + " · " + status
}

// chipCopyText chip 的复制文本（D100④）：工具名 + 参数 + 结果——与展开体同源数据，
// 剔除箭头/状态/问答着色等 UI 修饰。
func chipCopyText(c *toolChip) string {
	var b strings.Builder
	b.WriteString(c.name)
	if s := strings.TrimSpace(c.args); s != "" {
		b.WriteString("\n")
		b.WriteString(s)
	}
	if c.done && c.result != "" {
		mark := "结果 ✓"
		if !c.ok {
			mark = "结果 ✗"
		}
		b.WriteString("\n")
		b.WriteString(mark)
		b.WriteString("\n")
		b.WriteString(c.result)
	}
	return b.String()
}

// chipRaw chip 的查看原文内容（D100④）：调用/结果组为 JSON（参数为合法 JSON 时内嵌
// 原值，否则降级为字符串字段）。
func chipRaw(c *toolChip) rawContent {
	payload := struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
		ArgsText  string          `json:"arguments_text,omitempty"`
		Done      bool            `json:"done"`
		OK        bool            `json:"ok,omitempty"`
		Result    string          `json:"result,omitempty"`
	}{Tool: c.name, Done: c.done, OK: c.ok, Result: c.result}
	if json.Valid([]byte(c.args)) {
		payload.Arguments = json.RawMessage(c.args)
	} else {
		payload.ArgsText = c.args
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		b = []byte(c.name + "\n" + c.args + "\n" + c.result)
	}
	return rawContent{title: "工具调用 · " + c.name, text: string(b)}
}

// toolChipRow 工具合并 chip（D67）：头部行可点击折叠/展开；展开体 = 参数（等宽全文）+
// 权限问答（暗色）+ 结果全文。头部为点击热区不挂行选；参数/结果可选
// （键位固定 2 = selCount，开合不漂移后续行序号）。手排纵列（D91 ②：量期知块偏移；
// 折叠时参数/结果不铺开 → note 不落，指纹随开合变化即清选）。
func (u *UI) toolChipRow(it blockView, rs *rowSel, cardR int) (layout.Widget, color.NRGBA, int, bool, bool) {
	c := it.chip
	cl := u.chipClick(it.chipIdx)
	expanded := u.chipIsOpen(it.chipIdx, c)
	header := chipHeaderText(c, expanded)
	return func(gtx layout.Context) layout.Dimensions {
		vs := beginVStack(gtx)
		vs.add(func(gtx layout.Context) layout.Dimensions {
			return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(u.th, header)
				l.Color = textMuted
				return l.Layout(gtx)
			})
		})
		if expanded {
			add := func(k int, w layout.Widget) {
				vs.gap(mdBlockGapDp)
				off := image.Pt(0, vs.y)
				vs.add(func(gtx layout.Context) layout.Dimensions {
					return rs.layout(gtx, w, k, off, image.Point{})
				})
			}
			if strings.TrimSpace(c.args) != "" {
				add(0, func(gtx layout.Context) layout.Dimensions {
					s := material.Body2(u.th, c.args)
					s.Font = monoFace
					s.TextSize = u.th.TextSize * 13.0 / 16.0
					s.State = rs.sel(0)
					return s.Layout(gtx)
				})
			}
			if c.confirmQ != "" {
				qa := c.confirmQ
				if c.confirmA != "" {
					qa += " → " + c.confirmA
				}
				vs.gap(mdBlockGapDp)
				vs.add(func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(u.th, qa)
					l.Color = textDim
					return l.Layout(gtx)
				})
			}
			switch {
			case c.done:
				mark := "结果 ✓"
				if !c.ok {
					mark = "结果 ✗"
				}
				vs.gap(mdBlockGapDp)
				vs.add(func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(u.th, mark)
					l.Color = textDim
					return l.Layout(gtx)
				})
				add(1, func(gtx layout.Context) layout.Dimensions {
					s := material.Body2(u.th, c.result)
					s.Color = textMuted
					s.State = rs.sel(1)
					return s.Layout(gtx)
				})
			case c.confirmQ == "": // 运行中且无待确认（展开查看时）
				vs.gap(mdBlockGapDp)
				vs.add(func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(u.th, "运行中…")
					l.Color = textDim
					return l.Layout(gtx)
				})
			}
		}
		return vs.dims()
	}, cardTool, cardR, false, false
}

// statusChip 状态行 chip（§15.1：仅生成时显示"思考中/生成中 · 模型 · 权限档"），
// 右对齐小胶囊（自带底板 → 位图可见 + 可作拖拽把手）。
func (u *UI) statusChip(gtx layout.Context, w, h, absY int) {
	txt := u.statusText()
	if txt == "" {
		return
	}
	label := func(gtx layout.Context) layout.Dimensions {
		s := material.Caption(u.th, txt)
		s.Color = textMuted
		return s.Layout(gtx)
	}
	m := op.Record(gtx.Ops)
	dims := label(gtx)
	txtOp := m.Stop()
	padX := gtx.Dp(statusPadXDp)
	chipH := gtx.Dp(statusChipDp)
	x := w - gtx.Dp(sideMarginDp) - dims.Size.X - 2*padX
	y := (h - chipH) / 2
	bgRect := image.Rectangle{Min: image.Pt(x, y), Max: image.Pt(x+dims.Size.X+2*padX, y+chipH)}
	st := clip.UniformRRect(bgRect, chipH/2).Push(gtx.Ops)
	paint.Fill(gtx.Ops, pillBg)
	inner := op.Offset(image.Pt(bgRect.Min.X+padX, bgRect.Min.Y+(chipH-dims.Size.Y)/2)).Push(gtx.Ops)
	txtOp.Add(gtx.Ops)
	inner.Pop()
	st.Pop()
	u.record(bgRect.Add(image.Pt(0, absY)), chipH/2, pillBg, image.Rectangle{Max: u.frameSize})
}
