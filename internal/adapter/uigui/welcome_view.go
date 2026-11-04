package uigui

// 欢迎窗的帧骨架与外壳：页头、编号步骤条、内联问题/状态行、页脚与两类按钮。

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// welcomeFrame 欢迎窗单帧（常规窗形态：窗体面铺底，无形状位图/羽化，§15.7）。
// 事件先于渲染取尽（Clicked 自带 Update；点击触发重绘）。
//
// 版式（D153，模拟稿）：白面圆角面板 + 灰底 → 页头（标题 + 副标题 + 编号圆徽步骤条）
// → 正文（接入页 = 一张四行分组卡 + 卡外的「测试连通」与内联结果）→ 页脚（hairline + 三钮）。
func welcomeFrame(gtx layout.Context, th *material.Theme, f *welcomeForm) layout.Dimensions {
	f.drain()
	for f.next.Clicked(gtx) {
		f.onNext()
	}
	for f.prev.Clicked(gtx) {
		f.onPrev()
	}
	for f.save.Clicked(gtx) {
		f.onSave()
	}
	for f.skip.Clicked(gtx) {
		f.onSkip()
	}
	for f.testBtn.Clicked(gtx) {
		f.onTest()
	}
	for f.modelsBtn.Clicked(gtx) {
		f.onFetchModels()
	}
	for i := range f.candClicks {
		if !f.candClicks[i].Clicked(gtx) {
			continue
		}
		if i < len(f.cands) {
			f.models.SetText(f.cands[i]) // 点 chip = 替换成单选（但算「用户已改」）
			f.modelsOrig = f.models.Text()
		}
	}
	pp := paperOf()
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			// 灰底 + 白面圆角面板：面板本身是「纸」，窗体底只露一圈边（D146 面阶）。
			size := gtx.Constraints.Max
			paint.FillShape(gtx.Ops, pp.sunken, clip.Rect(image.Rectangle{Max: size}).Op())
			return layout.Dimensions{Size: size}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(14)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Background{}.Layout(gtx,
					func(gtx layout.Context) layout.Dimensions {
						r := image.Rectangle{Max: gtx.Constraints.Min}
						paperCard(gtx, pp, r, gtx.Dp(unit.Dp(pp.rWin)), pp.card)
						return layout.Dimensions{Size: r.Max}
					},
					func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return layout.Inset{
											Top: unit.Dp(22), Bottom: unit.Dp(16),
											Left: unit.Dp(22), Right: unit.Dp(22),
										}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
											return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
												layout.Rigid(welcomeHeader(th, f)),
												layout.Rigid(func(gtx layout.Context) layout.Dimensions {
													return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, welcomeIssues(th, f))
												}),
											)
										})
									}),
									// 页头与正文之间一条**贯通面板**的 hairline（模拟稿位置）。
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return welcomeHairline(gtx, 0)
									}),
								)
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{
									Left: unit.Dp(22), Right: unit.Dp(22),
									Top: unit.Dp(16), Bottom: unit.Dp(12),
								}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return f.pageBody(gtx, th)
								})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(22), Right: unit.Dp(22)}.Layout(gtx, welcomeStatusLine(th, f))
							}),
							layout.Rigid(welcomeFooter(th, f)),
						)
					},
				)
			})
		}),
	)
}

// welcomeHeader 标题 + 副标题 + 步进条（D278）。
func welcomeHeader(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return paperTitle(th, i18n.T("ui.welcome.title"))(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperNoteSp(th), i18n.T("ui.welcome.subtitle"))
				l.Color = paperOf().text2
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, l.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, welcomeSteps(th, f))
			}),
		)
	}
}

// welcomeSteps 步进条：五个圆点 + 当前页名 + n/N（D278）。
//
// 五项页名横排在 608dp 窗里中英都会挤（en 更长），且第 1 页切语言后整条要重排——所以只
// 说三件事：圆点给进度，页名说这一页是什么，n/N 给总量。页名走 Flexed：Gio 先排 Rigid
// 再分剩余，右侧计数不会被长页名挤掉，页名自己折行。
func welcomeSteps(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		kids := make([]layout.FlexChild, 0, welcomePageCount+2)
		for p := welcomePage(0); int(p) < welcomePageCount; p++ {
			kids = append(kids, layout.Rigid(welcomeDot(pp, p < f.page, p == f.page)))
		}
		kids = append(kids,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperNoteSp(th), welcomePageName(f.page))
				l.Color = pp.text
				l.Font.Weight = font.Bold
				return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, l.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), i18n.Tf("ui.welcome.step.progress",
					"n", strconv.Itoa(int(f.page)+1), "total", strconv.Itoa(welcomePageCount)))
				l.Color = pp.text3
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, l.Layout)
			}),
		)
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, kids...)
	}
}

// welcomeDot 一个进度圆点：已过 = 实心次级色，当前 = 实心强调色，未到 = 描边空心。
func welcomeDot(pp paperPalette, done, active bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		d := gtx.Dp(unit.Dp(7))
		if active {
			d = gtx.Dp(unit.Dp(9))
		}
		gtx.Constraints.Min, gtx.Constraints.Max = image.Pt(d, d), image.Pt(d, d)
		return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			circle := clip.Ellipse{Max: image.Pt(d, d)}
			switch {
			case active:
				paint.FillShape(gtx.Ops, pp.accent, circle.Op(gtx.Ops))
			case done:
				paint.FillShape(gtx.Ops, pp.text3, circle.Op(gtx.Ops))
			default:
				b := gtx.Dp(1)
				paint.FillShape(gtx.Ops, pp.hairline, circle.Op(gtx.Ops))
				paint.FillShape(gtx.Ops, pp.card,
					clip.Ellipse{Min: image.Pt(b, b), Max: image.Pt(d-b, d-b)}.Op(gtx.Ops))
			}
			return layout.Dimensions{Size: image.Pt(d, d)}
		})
	}
}

// welcomeIssues 问题清单提示（装配前宿主填；空 = 不占位）。
func welcomeIssues(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if len(f.env.snap.Issues) == 0 {
			return layout.Dimensions{}
		}
		l := material.Body2(th, i18n.Tf("ui.welcome.issues", "issues", strings.Join(f.env.snap.Issues, "；")))
		l.Color = paperOf().danger
		return l.Layout(gtx)
	}
}

// welcomeStatusLine 状态行（反馈 / 默认提示）。页脚之上、hairline 之下；空文本时不占高。
func welcomeStatusLine(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		text, col := f.status, paperOf().text2
		if text == "" {
			col = paperOf().text3
			// 接入页不放默认提示：模拟稿在卡下留白，密钥说明已经在输入框占位文字里；
			// 页脚之上只在真有反馈（校验/保存/应用失败）时出现一行。
			if f.page == welcomeDone {
				text = i18n.T("ui.welcome.hint.save_restart")
				if f.env.setup {
					text = i18n.T("ui.welcome.hint.save_continue")
				}
			}
		} else if f.err {
			col = paperOf().danger
		}
		if text == "" {
			return layout.Dimensions{} // 无反馈就不占高（页脚贴到正文内衬上）
		}
		l := material.Label(th, paperNoteSp(th), text)
		l.Color = col
		return l.Layout(gtx)
	}
}

// welcomeFooter 页脚：hairline 分隔 + 左「退出向导」/ 右「上一步」+「继续」（末页 = 完成）。
// 与正文同底色（Paper 里不出现第二块大底色），只用一条 hairline 分层。
//
// 页脚按页定（D278）：第 1 页只有「开始」（无上一步，占位禁用让按钮不跳）；中间页「上一步 /
// 继续」；末页「保存并继续」/「保存」。退出向导的灰钮只在首页与末页出现——中间页把它挤到
// 主次难分的位置，而窗标题栏的关闭本来就在。
func welcomeFooter(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		last := f.page == welcomeDone
		primary := i18n.T("ui.welcome.btn.next")
		if f.page == welcomeIntro {
			primary = i18n.T("ui.welcome.btn.start")
		}
		if last {
			primary = i18n.T("ui.common.save")
			if f.env.setup {
				primary = i18n.T("ui.welcome.btn.save_continue")
			}
		}
		skipLabel := i18n.T("ui.welcome.btn.later")
		if !f.env.setup {
			skipLabel = i18n.T("ui.common.close")
		}
		// 第 2 页的「继续」受勾选闸门约束：与 onNext 同一判据（canAdvance）。
		gate := f.canAdvance()
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				h := gtx.Dp(1)
				w := gtx.Constraints.Max.X
				paint.FillShape(gtx.Ops, pp.hairline, clip.Rect(image.Rect(0, 0, w, h)).Op())
				return layout.Dimensions{Size: image.Pt(w, h)}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(14), Left: unit.Dp(22), Right: unit.Dp(22)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					children := make([]layout.FlexChild, 0, 4)
					if f.page == welcomeIntro || last {
						children = append(children, layout.Rigid(welcomeGhostBtn(th, &f.skip, skipLabel)))
					}
					children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: gtx.Constraints.Min}
					}))
					if f.page > welcomeIntro {
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, welcomeBtn(th, &f.prev, i18n.T("ui.common.back"), false))
						}))
					} else {
						// 首屏也占位（禁用态）：位置固定，翻页时按钮不跳。
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, paperButton(th, &f.prev, i18n.T("ui.common.back"), paperBtnSecondary, false))
						}))
					}
					if last {
						children = append(children, layout.Rigid(welcomeBtn(th, &f.save, primary, true)))
					} else {
						children = append(children, layout.Rigid(welcomeBtnOn(th, &f.next, primary, true, gate)))
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
				})
			}),
		)
	}
}

// welcomeGhostBtn 无底按钮（页脚「跳过此项」）：透明底 + 次文字色，不打主操作配色。
func welcomeGhostBtn(th *material.Theme, c *widget.Clickable, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		b := material.Button(th, c, label)
		b.TextSize = paperNoteSp(th)
		b.CornerRadius = unit.Dp(pp.rCtl)
		b.Inset = layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(12), Right: unit.Dp(12)}
		b.Background, b.Color = color.NRGBA{}, pp.text2
		return b.Layout(gtx)
	}
}

// welcomeBtn 按钮（primary = 强调底 + 白字；次要 = 下沉面）。
func welcomeBtn(th *material.Theme, c *widget.Clickable, label string, primary bool) layout.Widget {
	return welcomeBtnOn(th, c, label, primary, true)
}

// welcomeBtnOn 同上，带可用态（第 2 页未勾选时「继续」禁用）。
func welcomeBtnOn(th *material.Theme, c *widget.Clickable, label string, primary, enabled bool) layout.Widget {
	kind := paperBtnSecondary
	if primary {
		// 强调色分两档：品牌色 #00AEEF 承载白字只有 2.53:1，按钮必须用深一档。
		kind = paperBtnPrimary
	}
	return paperButton(th, c, label, kind, enabled)
}
