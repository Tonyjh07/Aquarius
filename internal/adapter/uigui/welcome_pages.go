package uigui

// 欢迎窗各页正文（接入 / 教程 / 完成）及其卡片、候选列表、连通探针与密钥输入框。

import (
	"errors"
	"image"
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// pageBody 当前页正文。
func (f *welcomeForm) pageBody(gtx layout.Context, th *material.Theme) layout.Dimensions {
	switch f.page {
	case welcomeIntro:
		return f.introPage(gtx, th)
	case welcomeTerms:
		return f.termsPage(gtx, th)
	case welcomeAccess:
		return f.accessPage(gtx, th)
	case welcomeTutorial:
		return f.tutorialPage(gtx, th)
	default:
		return f.donePage(gtx, th)
	}
}

// introPage 第 1 页：品牌与「接下来确认哪几件事」（D278）。可滚：200% 字号下这段正文
// 折成七八行，压在页眉与页脚之间放不下。
func (f *welcomeForm) introPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return (&f.introList).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				d := gtx.Dp(unit.Dp(64))
				box := image.Rectangle{Max: image.Pt(d, d)}
				drawLogo(gtx, box)
				return layout.Dimensions{Size: box.Max}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(18)}.Layout(gtx, welcomePara(th, i18n.T("ui.welcome.intro.lead")))
			}),
		)
	})
}

// termsPage 第 2 页：用户协议与隐私政策 + 勾选闸门（D278）。
// 正文区占满页头与勾选框之间的高度（正文到位后可滚），勾选框固定在底部——
// 长正文滚到底才看得见闸门，用户不会在没读到的情况下被推着往下点。
func (f *welcomeForm) termsPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return (&f.termsList).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(welcomePara(th, i18n.T("ui.welcome.terms.lead"))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(10)}.Layout(gtx,
					paperCheckBox(th, &f.consent, i18n.T("ui.welcome.terms.accept")))
			}),
		)
	})
}

// accessPage 接入模型页（D153，模拟稿）：一张四行分组卡 + 卡外「测试连通」与内联结果。
func (f *welcomeForm) accessPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "Provider", editorBox(th, &f.name, i18n.T("ui.welcome.ph.provider")))
		},
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, i18n.T("ui.welcome.label.base_url"), editorBox(th, &f.baseURL, i18n.T("ui.welcome.ph.base_url")))
		},
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, i18n.T("ui.welcome.label.api_key"), editorBox(th, &f.apiKey, i18n.T("ui.welcome.ph.api_key")))
		},
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, i18n.T("ui.welcome.label.models"), func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, editorBox(th, &f.models, i18n.T("ui.welcome.ph.models"))),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx,
							paperButton(th, &f.modelsBtn, i18n.T("ui.welcome.btn.fetch_models"), paperBtnSecondary, true))
					}),
				)
			})
		},
	}
	// 候选挂在**卡内**模型行下方（模拟稿位置）：它是这一行的补充说明，不是卡外的另一
	// 个区块——放到卡外会被一条 card 边界与「模型名」割开。
	if f.modelsHint != "" || len(f.cands) > 0 {
		last := rows[len(rows)-1]
		rows[len(rows)-1] = func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(last),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return welcomeCands(gtx, th, f)
					})
				}),
			)
		}
	}
	kids := []layout.FlexChild{
		layout.Rigid(welcomePara(th, i18n.T("ui.welcome.access_intro"))),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return welcomeCard(gtx, th, rows)
			})
		}),
	}
	kids = append(kids,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(14), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(welcomeBtn(th, &f.testBtn, i18n.T("ui.welcome.btn.test"), false)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return welcomeProbe(gtx, th, f)
						})
					}),
				)
			})
		}),
	)
	// 内容比窗高时（候选 chip 会多占两三行）**必须可滚**：不可滚的话内容会压到页脚、
	// 甚至把「测试连通」整行盖掉（离屏实测：chip 行直接叠在按钮上）。这里用裸
	// layout.List 而不是 material.List——后者会常驻画一条滚动条，静态表单里像故障。
	return (&f.accessList).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
	})
}

// welcomeCard 分组卡：一张圆角卡包住全部行，行间 hairline 分隔（左内缩 14dp）。
// 行内容用 fieldRow（标签列 + 控件铺满），行内上下 14dp。
func welcomeCard(gtx layout.Context, th *material.Theme, rows []layout.Widget) layout.Dimensions {
	pp := paperOf()
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			r := image.Rectangle{Max: gtx.Constraints.Min}
			if m := gtx.Constraints.Max.X; m > r.Max.X {
				r.Max.X = m
			}
			paperCard(gtx, pp, r, gtx.Dp(unit.Dp(pp.rCard)), pp.card)
			return layout.Dimensions{Size: r.Max}
		},
		func(gtx layout.Context) layout.Dimensions {
			kids := make([]layout.FlexChild, 0, len(rows)*2)
			for i, row := range rows {
				i, row := i, row
				if i > 0 {
					kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						h := gtx.Dp(1)
						w := gtx.Constraints.Max.X
						paint.FillShape(gtx.Ops, pp.hairline,
							clip.Rect(image.Rect(gtx.Dp(14), 0, w-gtx.Dp(14), h)).Op())
						return layout.Dimensions{Size: image.Pt(w, h)}
					}))
				}
				kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top: unit.Dp(10), Bottom: unit.Dp(10), Left: unit.Dp(14), Right: unit.Dp(14),
					}.Layout(gtx, row)
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
		},
	)
}

// welcomeCands 候选行：说明文字与 chip 同排流式折行（模拟稿「已自动填入 2 个候选 ·
// 点选替换   gpt-5.2  gpt-5.2-mini」）。排布规则见 paperChipFlow。
func welcomeCands(gtx layout.Context, th *material.Theme, f *welcomeForm) layout.Dimensions {
	hint := f.modelsHint
	if hint == "" {
		hint = i18n.T("ui.welcome.cands_hint")
	}
	return paperChipFlow(gtx, th, hint, f.cands, f.candClicks)
}

// welcomeHairline 一条贯通卡面宽度的 hairline。pad = 当前左右内衬，线向两侧各伸出 pad，
// 于是分隔线能画到面板边缘（模拟稿的页头/页脚分隔线都是整幅贯通）。
func welcomeHairline(gtx layout.Context, pad unit.Dp) layout.Dimensions {
	h := gtx.Dp(1)
	w := gtx.Constraints.Max.X
	p := gtx.Dp(pad)
	paint.FillShape(gtx.Ops, paperOf().hairline, clip.Rect(image.Rect(-p, 0, w+p, h)).Op())
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// welcomeProbe 「测试连通」右侧的内联结果：绿点 + 成功时延 / 红字失败原因。
func welcomeProbe(gtx layout.Context, th *material.Theme, f *welcomeForm) layout.Dimensions {
	pp := paperOf()
	if f.probeBusy {
		l := material.Label(th, paperNoteSp(th), i18n.T("ui.welcome.testing"))
		l.Color = pp.text3
		return l.Layout(gtx)
	}
	if f.probe == "" {
		return layout.Dimensions{}
	}
	col := pp.ok
	if f.probeErr {
		col = pp.danger
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			d := gtx.Dp(unit.Dp(7))
			gtx.Constraints.Min, gtx.Constraints.Max = image.Pt(d, d), image.Pt(d, d)
			paint.FillShape(gtx.Ops, col, clip.Ellipse{Max: image.Pt(d, d)}.Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(d, d)}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperNoteSp(th), f.probe)
			l.Color = pp.text2
			return layout.Inset{Left: unit.Dp(7)}.Layout(gtx, l.Layout)
		}),
	)
}

// tutorialPage 教程页（滚动列表：操作提示 + 常用命令）。
func (f *welcomeForm) tutorialPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	rows := tutorialRows(f.hotkeyLabel())
	return material.List(th, &f.tutList).Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		r := rows[i]
		return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			switch r.kind {
			case tutHead:
				l := material.Subtitle1(th, r.left)
				l.Color = th.Fg
				return l.Layout(gtx)
			case tutCmd:
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						w := gtx.Dp(unit.Dp(190))
						gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
						l := material.Label(th, paperNoteSp(th), r.left)
						l.Color = paperOf().text
						return l.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, paperNoteSp(th), r.right)
						l.Color = paperOf().text2
						return l.Layout(gtx)
					}),
				)
			default:
				l := material.Label(th, paperNoteSp(th), r.left)
				l.Color = paperOf().text3
				return l.Layout(gtx)
			}
		})
	})
}

// donePage 完成页：写盘摘要（密钥只报形态、不报值）。
func (f *welcomeForm) donePage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	p := f.patch()
	keyForm := f.keyForm(p.APIKey)
	row := func(label, value string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, label, func(gtx layout.Context) layout.Dimensions {
					l := material.Body1(th, value)
					l.Color = th.Fg
					return l.Layout(gtx)
				})
			})
		})
	}
	model := p.Models
	if i := strings.Index(model, ","); i > 0 {
		model = i18n.Tf("ui.welcome.model_more", "model", model[:i])
	}
	// 可滚：四行摘要在 200% 字号下比正文区高（离屏实测只看得见前两行，后两行被页脚切掉）。
	return (&f.doneList).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(welcomePara(th, i18n.T("ui.welcome.done_intro"))),
			row("Provider", p.Provider),
			row(i18n.T("ui.welcome.label.base_url"), p.BaseURL),
			row(i18n.T("ui.welcome.label.model"), model),
			row(i18n.T("ui.welcome.label.api_key"), keyForm),
		)
	})
}

// keyForm 密钥形态描述（**不回显值**，D35）。
//
// 明文那一档说的必须是**保存后**的形态：向导填的明文由 `welcomeManager.Apply` 转入本机
// 密钥库，config 里只留引用（D161）。早先这里写「写入 config，启动会警告」，与真实行为
// 相反，用户据此以为密钥存成了明文（D210）。
func (f *welcomeForm) keyForm(apiKey string) string {
	switch {
	case strings.HasPrefix(apiKey, "secret:"):
		return i18n.Tf("ui.welcome.key_env_ref", "name", strings.TrimPrefix(apiKey, "secret:"))
	case apiKey != "":
		return i18n.T("ui.welcome.key_plain")
	default:
		return i18n.T("ui.welcome.key_keep")
	}
}

// welcomePara 段落文本。
func welcomePara(th *material.Theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := material.Body1(th, text)
		l.Color = th.Fg
		return l.Layout(gtx)
	}
}

// newUISideWelcome in-app 宿主表单（主窗宿主的 winWelcome 次窗）：完成/显式关闭 = 关窗
// （closeWin 由宿主给——次窗句柄的关闭路径），ProviderMgr 语义为「重启生效」。
func newUISideWelcome(mgr WelcomeManager, closeWin func()) *welcomeForm {
	env := welcomeEnv{mgr: mgr, done: func(SetupOutcome) { closeWin() }}
	if mgr != nil {
		env.snap = mgr.Snapshot()
	}
	return newWelcomeForm(env)
}

// RunSetupWindow 装配前的独立向导窗（阻塞至完成/关闭）。**宿主可注入**：cmd/aquarius 经
// 包级 var 持有它，测试换假实现（首跑测试不得真开窗）。
// 窗口形态与次窗一致（Decorated 常规窗；platform.New 惰性——不会提前装托盘/热键）。
func RunSetupWindow(req SetupRequest) (SetupOutcome, error) {
	if req.Mgr == nil {
		return SetupCancelled, errors.New(i18n.T("ui.welcome.err.wizard_unwired"))
	}
	plat := newPlatform(Options{Theme: req.Theme}, true)
	p := resolvePalette(req.Theme, plat.DarkMode())
	activeTokens.Store(&p) // 装配前：主窗 goroutine 尚未启动，写活动快照无并发（同 newUI 口径）
	applyPaper(resolvePaper(req.Theme, p.isDark()))
	th := newTheme(plat.SystemFontCandidates())
	th.Palette.Fg, th.Palette.Bg = p.fg, p.bg

	snap := req.Mgr.Snapshot()
	snap.Issues = req.Issues
	snap.Welcomed = false // 装配前宿主：本次一定是要引导（不是「已看、想再看」）

	w := new(app.Window)
	outcome := SetupCancelled
	var once sync.Once
	env := welcomeEnv{mgr: req.Mgr, setup: true, snap: snap}
	env.done = func(out SetupOutcome) {
		once.Do(func() {
			outcome = out
			w.Perform(system.ActionClose) // 关窗 → DestroyEvent → 事件循环退出
		})
	}
	f := newWelcomeForm(env)
	f.env.inval = w.Invalidate

	width, height, minW, minH := winWelcome.geometry()
	w.Option(
		app.Title(winWelcome.title()),
		app.Size(unit.Dp(width), unit.Dp(height)),
		app.MinSize(unit.Dp(minW), unit.Dp(minH)),
		app.Decorated(true), // 常规装饰窗（不接主窗形裁/ULW/置顶等机制，§15.7）
	)
	var ops op.Ops
	for {
		ev := w.Event()
		switch e := ev.(type) {
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			welcomeFrame(gtx, th, f)
			e.Frame(&ops)
		case app.DestroyEvent:
			return outcome, e.Err
		}
	}
}
