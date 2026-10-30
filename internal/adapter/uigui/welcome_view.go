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

// welcomeHeader 标题 + 副标题 + 步骤条（编号圆徽）。
func welcomeHeader(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return paperTitle(th, "欢迎使用 Aquarius")(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperNoteSp(th), "三步完成接入；密钥存入本机加密库，配置里只留引用。")
				l.Color = paperOf().text2
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, l.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						welcomeStep(th, 1, "接入模型", f.page == welcomeAccess),
						welcomeStep(th, 2, "教程", f.page == welcomeTutorial),
						welcomeStep(th, 3, "完成", f.page == welcomeDone),
					)
				})
			}),
		)
	}
}

// welcomeStep 单步指示：编号圆徽（当前步实心 accent + 白字；其余描边空心）+ 步骤名。
func welcomeStep(th *material.Theme, no int, label string, active bool) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		d := gtx.Dp(unit.Dp(22))
		return layout.Inset{Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min, gtx.Constraints.Max = image.Pt(d, d), image.Pt(d, d)
					return layout.Stack{Alignment: layout.Center}.Layout(gtx,
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							circle := clip.Ellipse{Max: image.Pt(d, d)}
							if active {
								paint.FillShape(gtx.Ops, pp.accent, circle.Op(gtx.Ops))
							} else {
								paint.FillShape(gtx.Ops, pp.hairline, circle.Op(gtx.Ops))
								b := gtx.Dp(1)
								paint.FillShape(gtx.Ops, pp.card,
									clip.Ellipse{Min: image.Pt(b, b), Max: image.Pt(d-b, d-b)}.Op(gtx.Ops))
							}
							return layout.Dimensions{Size: image.Pt(d, d)}
						}),
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							l := material.Label(th, paperMicroSp(th), strconv.Itoa(no))
							l.Color = pp.text3
							if active {
								l.Color = pp.accentFg
							}
							return l.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperNoteSp(th), label)
					l.Color = pp.text3
					if active {
						l.Color = pp.text
						l.Font.Weight = font.Bold
					}
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, l.Layout)
				}),
			)
		})
	})
}

// welcomeIssues 问题清单提示（装配前宿主填；空 = 不占位）。
func welcomeIssues(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if len(f.env.snap.Issues) == 0 {
			return layout.Dimensions{}
		}
		l := material.Body2(th, "配置尚未完成："+strings.Join(f.env.snap.Issues, "；"))
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
				text = "保存后写回配置；provider / 密钥类改动需重启进程生效。"
				if f.env.setup {
					text = "保存后立刻继续启动。"
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

// welcomeFooter 页脚：hairline 分隔 + 左「跳过此项」/ 右「上一步」+「下一步」。
// 与正文同底色（Paper 里不出现第二块大底色），只用一条 hairline 分层。
func welcomeFooter(th *material.Theme, f *welcomeForm) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		skipLabel := "跳过此项"
		if !f.env.setup {
			skipLabel = "关闭"
		}
		primary := "下一步"
		if f.page == welcomeDone {
			primary = "保存"
			if f.env.setup {
				primary = "保存并继续"
			}
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				h := gtx.Dp(1)
				w := gtx.Constraints.Max.X
				paint.FillShape(gtx.Ops, pp.hairline, clip.Rect(image.Rect(0, 0, w, h)).Op())
				return layout.Dimensions{Size: image.Pt(w, h)}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(14), Left: unit.Dp(22), Right: unit.Dp(22)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					children := []layout.FlexChild{
						layout.Rigid(welcomeGhostBtn(th, &f.skip, skipLabel)),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: gtx.Constraints.Min}
						}),
					}
					if f.page > welcomeAccess {
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, welcomeBtn(th, &f.prev, "上一步", false))
						}))
					} else {
						// 首屏也占位（禁用态）：位置固定，翻页时按钮不跳。
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, paperButton(th, &f.prev, "上一步", paperBtnSecondary, false))
						}))
					}
					if f.page == welcomeDone {
						children = append(children, layout.Rigid(welcomeBtn(th, &f.save, primary, true)))
					} else {
						children = append(children, layout.Rigid(welcomeBtn(th, &f.next, primary, true)))
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
	kind := paperBtnSecondary
	if primary {
		// 强调色分两档：品牌色 #00AEEF 承载白字只有 2.53:1，按钮必须用深一档。
		kind = paperBtnPrimary
	}
	return paperButton(th, c, label, kind, true)
}
