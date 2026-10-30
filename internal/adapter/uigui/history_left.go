package uigui

// 历史窗左栏：展开/收起两态、标题行动作钮、会话行（含头像）与行内删除槽。

import (
	"fmt"
	"image"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// histLeftColumnDp 左栏展开态宽度（§5.4）。
const histLeftColumnDp unit.Dp = 200

// histLeftRailDp 左栏收起态宽度（D159）：一条窄轨，只放展开钮——左栏存在的意义是选会话，
// 选完就收起把宽度让给关系图（长会话横向本来就不够看）。
const histLeftRailDp unit.Dp = 30

// histLeftColumn 左栏：会话列表（标题 / 时间 / 消息数；当前会话高亮）+ 标题行右端的收起钮。
//
// D249 批量选择态（st.selMode）下同一列换三处：每行**左侧**出复选框、行尾「✕」收起
// （删除这时走底部动作条，两个删除入口同时在场只会点错）、列表底部换成动作条。
func histLeftColumn(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	rowH := gtx.Dp(44)
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, paperMicroSp(th), "会话")
						l.Font.Weight = font.Bold
						l.Color = paperOf().text3
						return l.Layout(gtx)
					}),
					layout.Flexed(1, layout.Spacer{}.Layout),
					// 会话进出的两个入口（§5.16 的 GUI 出口）：「导入」读外部文件成一个
					// 新会话、「导出」把**当前**会话写成一份归档。两者都只负责拿路径，选完投一条
					// 内核命令——搬运、防覆盖、回执全在命令面（铁律 5：壳内不复制内核逻辑）。
					layout.Rigid(histTextBtn(th, &st.btnImport, "导入")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(histTextBtn(th, &st.btnExport, "导出")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					// D160：标题行右端两颗钮——「＋」新建对话（左） + 「◀」收起左栏（右）。
					// 新建排在收起**左侧**：收起钮语义是「把这条栏收掉」，贴边最右更顺；
					// 且两颗钮都在这条标题行里，它们的位置语义与其它行操作一致。
					layout.Rigid(histPlusBtn(&st.btnNew)),
					// 两颗钮留 8dp 间隔：加号是细笔画、三角是实心块，贴在一起会读成
					// 一个「＋▶」连字（2026-10-19 取像 4x 复看确认需要分开）。
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(histGlyphBtn(th, &st.btnLeft, "◀")),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return st.list.Layout(gtx, len(st.sessions), func(gtx layout.Context, i int) layout.Dimensions {
					c := st.rowClick(i)
					return layout.Stack{}.Layout(gtx,
						// 行底板（纯绘制，不取事件）：整行面，当前会话用选中色。
						layout.Expanded(func(gtx layout.Context) layout.Dimensions {
							s := st.sessions[i]
							pp := paperOf()
							fill := pp.win
							if s.Current {
								fill = pp.sel
							}
							paint.FillShape(gtx.Ops, fill,
								clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Max},
									gtx.Dp(unit.Dp(pp.rChip))).Op(gtx.Ops))
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, rowH)}
						}),
						// 行命中区（点行 = 切会话；悬浮行 = 显示「✕」）：上报**整行**盒，
						// 文字按内衬排布、右侧让出 ✕ 槽。
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								// 选择态行尾没有「✕」（见下），就不必让出那个槽——否则列宽
								// 会平白少一截，标题更早被截断。
								right := unit.Dp(6)
								if !st.selMode {
									right += histRowDelSlotDp
								}
								layout.Inset{
									Top: unit.Dp(6), Bottom: unit.Dp(6),
									Left: unit.Dp(6), Right: right,
								}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									if st.selMode {
										return selRowByIndex(gtx, th, st, i)
									}
									return st.histSessionRow(gtx, th, st.sessions[i])
								})
								return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, rowH)}
							})
						}),
						// 「✕」：与行点击件**同级兄弟**，排在它之后（命中链更靠前）。
						//
						// **不能**把它嵌进行点击件的 widget 里（2026-10-19 探针实测）：嵌
						// 进去的控件收不到任何指针事件——✕ 永远不悬浮、点了也没反应（逐点
						// 扫描：整行 hover 正常、✕ 恒 false）。同级后 ✕ 正常命中，且它会把
						// 同一位置的行的命中遮掉（点 ✕ 只投 /rmconv，不会顺带切会话）。
						//
						// 选择态**不画**它（D249）：删除走底部动作条，两个删除入口同时在场
						// 只会点错。
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							if st.selMode {
								return layout.Dimensions{}
							}
							return st.rowDelSlot(gtx, th, c, i, rowH)
						}),
					)
				})
			}),
			// 底部：常态一颗「选择」（进批量选择态），选择态换成动作条（D249）。
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !st.selMode {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(histTextBtn(th, &st.btnSelect, "选择")),
						layout.Flexed(1, layout.Spacer{}.Layout),
					)
				}
				return layout.Stack{}.Layout(gtx,
					layout.Expanded(func(gtx layout.Context) layout.Dimensions {
						return selBarPaint(gtx)
					}),
					layout.Stacked(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(2), Right: unit.Dp(2)}.
							Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return st.selActionBar(gtx, th)
							})
					}),
				)
			}),
		)
	})
}

// histLeftRail 左栏收起态（D159）：整条铺 sunken 底色 + 顶部展开钮，钮外整条不取事件
// （收起态唯一的交互件就是那颗钮，点空白不该有反应）。u 不参与绘制，只为与 histLeftColumn
// 同签名（帧里按状态二选一）。
func histLeftRail(gtx layout.Context, th *material.Theme, _ *UI, st *historyState) layout.Dimensions {
	pp := paperOf()
	paint.FillShape(gtx.Ops, pp.sunken,
		clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
	return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, layout.Spacer{}.Layout),
					layout.Rigid(histGlyphBtn(th, &st.btnLeft, "▶")),
					layout.Flexed(1, layout.Spacer{}.Layout),
				)
			}),
		)
	})
}

// histChromeBtn 左栏标题行 / 收起窄轨上的小钮外壳：Clickable 自带 hover 反馈 + 左右 2dp 内衬。
// 内容由调用方给（文字字形或自绘图标）。
func histChromeBtn(c *widget.Clickable, content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(2), Right: unit.Dp(2)}.Layout(gtx, content)
		})
	}
}

// histTextBtn 文字小钮（「导入」「导出」）：与字形象钮同款外壳，只是内容是一段字。
//
// 用文字不用图标：这两个动作在别处（命令行、文档）都以这两个词出现，图形符号得靠猜；
// 左栏标题行只有这一处能放它们，字号与旁边的「会话」标签同档。
func histTextBtn(th *material.Theme, c *widget.Clickable, label string) layout.Widget {
	return histChromeBtn(c, func(gtx layout.Context) layout.Dimensions {
		l := material.Caption(th, label)
		l.Color = paperOf().text2
		return l.Layout(gtx)
	})
}

// histGlyphBtn 文字字形小钮（◀ 收起 / ▶ 展开）。
func histGlyphBtn(th *material.Theme, c *widget.Clickable, glyph string) layout.Widget {
	return histChromeBtn(c, func(gtx layout.Context) layout.Dimensions {
		l := material.Caption(th, glyph)
		l.Color = paperOf().text2
		return l.Layout(gtx)
	})
}

// histPlusBtn 「＋」新建对话钮。
func histPlusBtn(c *widget.Clickable) layout.Widget {
	return histChromeBtn(c, histPlusGlyph)
}

// histPlusGlyph 自绘加号：全角「＋」是细十字，同字号下比 ◀/▶ 的实心三角淡一档（1x 几乎糊在
// 底上，加粗也没有粗体面可用）。两笔描边按 dp 定粗细与臂长，视觉重量与三角拉平。
func histPlusGlyph(gtx layout.Context) layout.Dimensions {
	d := gtx.Dp(unit.Dp(11))
	cx, cy := float32(d)/2, float32(d)/2
	a := float32(d) / 2
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(cx-a, cy))
	p.LineTo(f32.Pt(cx+a, cy))
	p.MoveTo(f32.Pt(cx, cy-a))
	p.LineTo(f32.Pt(cx, cy+a))
	paint.FillShape(gtx.Ops, paperOf().text2,
		clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(unit.Dp(2)))}.Op())
	return layout.Dimensions{Size: image.Pt(d, d)}
}

// histAvatarDp / histAvatarPx 会话行左槽头像口径：边长 30dp（行高 44dp 内衬后约 32dp），
// 解码 60px（2x 下采样）。
const (
	histAvatarDp unit.Dp = 30
	histAvatarPx         = 60
)

// histSessionRow 单行内容：左槽角色头像 + 标题 + （时间 · N 条）；当前会话带「●」。
func (st *historyState) histSessionRow(gtx layout.Context, th *material.Theme, s port.SessionSummary) layout.Dimensions {
	title := s.Title
	if title == "" {
		title = "新会话"
	}
	title = truncRunes(title, 18)
	mark := " "
	if s.Current {
		mark = "●"
	}
	meta := fmt.Sprintf("%s · %d 条", s.UpdatedAt.Format("01-02 15:04"), s.Messages)
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return st.sessionAvatar(gtx, th, s)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperNoteSp(th), mark+" "+title)
					l.Color = paperOf().text
					l.Font.Weight = font.Bold
					// D157：行尾让出「✕」槽后可用宽度变窄。标题已按 rune 截断，这里再钉一行
					// ——否则超长标题会折成两行把行高撑开，左栏行高参差。
					l.MaxLines = 1
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperMicroSp(th), meta)
					l.Color = paperOf().text3
					l.MaxLines = 1
					return l.Layout(gtx)
				}),
			)
		}),
	)
}

// sessionAvatar 会话行左槽头像（§5.17）：角色卡真图（异步解码；未就绪/失败画兜底）；
// 非角色会话（无卡）画中性兜底方块。
func (st *historyState) sessionAvatar(gtx layout.Context, th *material.Theme, s port.SessionSummary) layout.Dimensions {
	side := gtx.Dp(histAvatarDp)
	if src := st.avatars.get(s.AvatarPath); src != nil {
		if op, ok := src.imageOp(); ok {
			drawThumb(gtx, image.Rect(0, 0, side, side), op)
			return layout.Dimensions{Size: image.Pt(side, side)}
		}
	}
	// 有卡无头像：显示角色首字；无卡：空名 → 纯色方块（charAvatarFallback 空名不画字）。
	return charAvatarFallback(gtx, th, s.Character, side)
}

// truncRunes 截断到最多 n 个 rune（超长标题防溢出）。
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// histRowDelSlotDp 「✕」槽边长（D157）。行内容右侧**恒**让出这么多（`histLeftColumn` 的
// 内衬），故 ✕ 的显隐不引起行内文字重排；✕ 自己的盒也按这个边长定尺。
const histRowDelSlotDp unit.Dp = 20

// rowDelSlot 行尾「✕」槽（D157）：**仅指针在该行上时**绘制并登记点击件；未悬浮时连事件
// op 都不发（不显形、不占命中）。
//
// 判据取「行 ∪ ✕」两件（`row.Hovered() || del.Hovered()`）。两件都要看，是因为**指针一旦
// 停在 ✕ 上，行的 hover 就没了**（实测：✕ 的命中区在链上更靠前，会把同一位置的行的命中
// 遮掉——见 `histLeftColumn` 里兄弟结构的记录）；此时只有 `del.Hovered()` 能把 ✕ 撑住。
// 反过来，指针从右侧直接进入 ✕ 槽时，行的 hover 先成立一帧把 ✕ 登记出来，之后才轮到 ✕
// 自己接管——两帧之间不会闪空（进入/离开事件同一批投递，判据不会同时为假）。
//
// 悬浮态翻转时补一帧（wakeFrame）：保证「登记 ✕ / 撤下 ✕」与绘制落在同一帧，光标移入后
// 的第一次点击不会因为差一帧而落空。
//
// rowH 由调用方给（Stack 的 Stacked 子项拿到的 Max.Y 是整栏剩余高度，不是行高），用于
// 纵向居中；横向靠 `op.Offset` 贴行右缘（Stacked 子项 Min 被清 0，不能靠 Center/Right）。
func (st *historyState) rowDelSlot(gtx layout.Context, th *material.Theme, row *widget.Clickable, i, rowH int) layout.Dimensions {
	side := gtx.Dp(histRowDelSlotDp)
	del := st.rowDelClick(i)
	show := row.Hovered() || del.Hovered()
	if st.setDelShown(i, show) {
		wakeFrame(gtx)
	}
	if !show {
		return layout.Dimensions{}
	}
	margin := gtx.Dp(6) // 与行内容右侧内衬同宽：✕ 右缘和文字右缘对齐
	// 纵向：槽内居中后再上抬 2px——「✕」(U+2715) 的墨迹在行盒里偏下（CJK 字体的 ascent 大），
	// 直接居中看着往下掉；这点偏移是按默认字族调的（字体换了也仍是个位数像素的偏差）。
	y := (rowH-side)/2 - gtx.Dp(2)
	off := op.Offset(image.Pt(gtx.Constraints.Max.X-margin-side, y)).Push(gtx.Ops)
	defer off.Pop()
	gtx.Constraints = layout.Exact(image.Pt(side, side))
	return del.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		l := material.Label(th, paperNoteSp(th), "✕")
		l.Color = pp.text2
		if del.Hovered() {
			// 压在 ✕ 上：字形转**危险色**（在任何行面上都看得出来——下沉底与选中面几乎同色，
			// 只靠底色读不出来），并垫一层浅底下沉面，明确「这一下点的是删除」。
			l.Color = pp.danger
			paint.FillShape(gtx.Ops, pp.sunken,
				clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Max}, gtx.Dp(unit.Dp(pp.rCtl))).Op(gtx.Ops))
		}
		return layout.Center.Layout(gtx, l.Layout)
	})
}

// setDelShown 记下第 i 行「✕」的显隐，返回是否**翻转**（true = 需要补一帧重绘）。
func (st *historyState) setDelShown(i int, v bool) bool {
	for len(st.delShown) <= i {
		st.delShown = append(st.delShown, false)
	}
	if st.delShown[i] == v {
		return false
	}
	st.delShown[i] = v
	return true
}
