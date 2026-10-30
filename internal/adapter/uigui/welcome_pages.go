package uigui

// 欢迎窗各页正文（接入 / 教程 / 完成）及其卡片、候选列表、连通探针与密钥输入框。

import (
	"fmt"
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
)

// pageBody 当前页正文。
func (f *welcomeForm) pageBody(gtx layout.Context, th *material.Theme) layout.Dimensions {
	switch f.page {
	case welcomeAccess:
		return f.accessPage(gtx, th)
	case welcomeTutorial:
		return f.tutorialPage(gtx, th)
	default:
		return f.donePage(gtx, th)
	}
}

// accessPage 接入模型页（D153，模拟稿）：一张四行分组卡 + 卡外「测试连通」与内联结果。
func (f *welcomeForm) accessPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "Provider", editorBox(th, &f.name, "provider 名，如 openai"))
		},
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "接口地址", editorBox(th, &f.baseURL, "如 https://api.openai.com/v1"))
		},
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "API 密钥", editorBox(th, &f.apiKey, "明文（存入本机加密库）或 secret:名 引用；只写不回显"))
		},
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "模型名", func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, editorBox(th, &f.models, "逗号可多条；首个 = 缺省请求模型")),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx,
							paperButton(th, &f.modelsBtn, "获取模型", paperBtnSecondary, true))
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
		layout.Rigid(welcomePara(th, "填写 OpenAI 兼容端点的接入参数；保存后立即用于对话。")),
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
					layout.Rigid(welcomeBtn(th, &f.testBtn, "测试连通", false)),
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
		hint = "点选替换模型名"
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
		l := material.Label(th, paperNoteSp(th), "正在测试…")
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
		model = model[:i] + " 等"
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(welcomePara(th, "将要写入 config 的接入参数（确认无误后保存）：")),
		row("Provider", p.Provider),
		row("接口地址", p.BaseURL),
		row("模型", model),
		row("API 密钥", keyForm),
	)
}

// keyForm 密钥形态描述（**不回显值**，D35）。
//
// 明文那一档说的必须是**保存后**的形态：向导填的明文由 `welcomeManager.Apply` 转入本机
// 密钥库，config 里只留引用（D161）。早先这里写「写入 config，启动会警告」，与真实行为
// 相反，用户据此以为密钥存成了明文（D210）。
func (f *welcomeForm) keyForm(apiKey string) string {
	switch {
	case strings.HasPrefix(apiKey, "secret:"):
		return "环境变量引用 secret:" + strings.TrimPrefix(apiKey, "secret:") + "（密钥不落盘）"
	case apiKey != "":
		return "明文（保存时转入本机加密密钥库，config 只留引用）"
	default:
		return "保持原值（未改）"
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
		return SetupCancelled, fmt.Errorf("向导窗：数据面未接线")
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
