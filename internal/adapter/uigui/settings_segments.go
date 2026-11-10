package uigui

// settings_segments.go 行内分段控件（D280/D282）。
//
// 为什么不用现成的 selectField：它是**居中模态**——点开要铺满整窗遮罩、再点一次才收。
// 设置窗里一行一个枚举那样做还行，几十个工具逐行改档位就是几十次遮罩往返，批量调整
// 完全没法用。分段控件没有弹层：一次点击直接落值，扫一眼也能看出每行现在是哪一档。
//
// 也不想做锚定下拉：Gio 不暴露控件绝对坐标（只有上一帧画过的矩形），表单又是虚拟化
// 列表，行内浮层会被视口裁掉——settings_select.go 头注记过这件事。

import (
	"image"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// segmented 一组互斥的短标签按钮（横排，当前项用品牌淡底）。
//
// 无状态：选中项由调用方按快照现算传入，控件自己只持有可点区域——这样写回失败时
// 下一帧自动退回快照值，不需要额外的回滚逻辑。
type segmented struct {
	btns []widget.Clickable
}

// clicked 消费本帧点击，返回被点中的下标（-1 = 无）。渲染它的页面在 Events 里调一次。
func (s *segmented) clicked(gtx layout.Context) int {
	for i := range s.btns {
		if s.btns[i].Clicked(gtx) {
			return i
		}
	}
	return -1
}

// layout 画一组分段按钮。selected 越界或为负 = 全不选中（档位是未知值时的兜底）。
func (s *segmented) layout(gtx layout.Context, th *material.Theme, labels []string, selected int) layout.Dimensions {
	if len(s.btns) < len(labels) {
		s.btns = append(s.btns, make([]widget.Clickable, len(labels)-len(s.btns))...)
	}
	children := make([]layout.FlexChild, 0, len(labels))
	for i := range labels {
		i := i
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var gap unit.Dp
			if i > 0 {
				gap = 2
			}
			return layout.Inset{Left: gap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return s.btn(gtx, th, &s.btns[i], labels[i], i == selected)
			})
		}))
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
}

// btn 单个分段：先按内容量出尺寸，再在它底下铺底色，最后画字（录制成宏保证顺序）。
func (s *segmented) btn(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string, on bool) layout.Dimensions {
	pp := paperOf()
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		m := op.Record(gtx.Ops)
		d := layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10), Top: unit.Dp(5), Bottom: unit.Dp(5)}.
			Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperNoteSp(th), label)
				l.Color = pp.text3
				if on {
					l.Font.Weight = font.Bold
					l.Color = pp.accent
				}
				return l.Layout(gtx)
			})
		call := m.Stop()
		bg := pp.sunken
		if on {
			bg = pp.brandSoft
		}
		rad := gtx.Dp(unit.Dp(pp.rChip))
		paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: d.Size}, rad).Op(gtx.Ops))
		call.Add(gtx.Ops)
		return d
	})
}
