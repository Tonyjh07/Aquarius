package uigui

// 欢迎窗各页正文（接入 / 教程 / 完成）及其卡片、候选列表、连通探针与密钥输入框。

import (
	"errors"
	"image"
	"image/color"
	"strconv"
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/legal"
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

// introPage 第 1 页：品牌、界面语言与欢迎词（D278）。可滚：200% 字号下这段正文
// 折成七八行，压在页眉与页脚之间放不下。
//
// **语言选择排在正文之前**，两个理由：一是首次启动时界面语言可能正好是用户读不懂的
// 那种，第一个能操作的控件就该是它（正文可以读不懂，按钮上的「English」不会）；
// 二是 200% 字号下正文要占掉四行，排在后面的语言行会整个落到折叠线以下——一页里
// 最重要的一件事反而要滚动才看得见（离屏实测）。
//
// 语言三态用并排三钮而不是下拉：这是用户在这个程序里做的**第一个**选择，藏进下拉
// 等于告诉他「这个先别管」。选中项用主按钮配色，另外两项次按钮——同一组按钮里，
// 填色的那个就是当前值，不需要额外的勾选标记。
//
// 版式上整页居中（用户 11-04 反馈）：这一页只有品牌、一个选项和两句话，靠左上角
// 摊着会让 660×640 的窗空掉三分之二。居中用两条 Flexed 分摊留白——内容比视口矮时
// 上下留白相等；内容更高（200% 字号）时 Flexed 被压成 0，内容从顶部开始滚，不会把
// 上半截推到视口外。
func (f *welcomeForm) introPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	levels := welcomeLangLabels()
	// 视口按「正文行数」折算：整页内容大约要 12 行正文的高度（logo + 语言行 + 两颗
	// 按钮行 + 两行说明）。行数不够就换竖排 + 可滚的版式，中央对齐会把最后一段截掉。
	linePx := float32(paperBodySp(th)) * gtx.Metric.PxPerSp
	bigText := linePx > 0 && float32(gtx.Constraints.Max.Y) < linePx*introFitLines
	// 大字号下品牌区收到最小档，空隙也压紧：一屏本来就装不下全部内容，宁可把
	// 空间让给「名字 + 语言选择」，也别让 128dp 的圆钮占掉半屏。
	logoPx := introLogoPx(gtx, bigText)
	brandTop, langTop := unit.Dp(20), unit.Dp(40)
	if bigText {
		brandTop, langTop = unit.Dp(12), unit.Dp(24)
	}
	// 按钮组：常规字号横排三等分；大字号竖排——三等分的一颗钮放不下「跟随系统」，
	// 会折成「跟随系 / 统」两行，并且把选中项撑得比邻居高一截（离屏实测）。
	btns := make([]layout.FlexChild, 0, len(levels)*2)
	for i, label := range levels {
		i, label := i, label
		kind := paperBtnSecondary
		if f.lang == langSelections[i] {
			kind = paperBtnPrimary
		}
		btn := func(gtx layout.Context) layout.Dimensions {
			return paperButton(th, &f.langBtns[i], label, kind, true)(gtx)
		}
		if bigText {
			if i > 0 {
				btns = append(btns, layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout))
			}
			btns = append(btns, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X // 竖排时每颗钮铺满整列
				return btn(gtx)
			}))
			continue
		}
		if i > 0 {
			btns = append(btns, layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout))
		}
		btns = append(btns, layout.Flexed(1, btn))
	}
	gap := func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }
	column := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// logo 在本列里居中：列宽减去 logo 宽度，两边各留一半。
				side := logoPx
				pad := (gtx.Constraints.Max.X - side) / 2
				if pad < 0 {
					pad = 0
				}
				return layout.Inset{Left: pxToDp(gtx, pad)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					box := image.Rectangle{Max: image.Pt(side, side)}
					drawLogo(gtx, box)
					return layout.Dimensions{Size: box.Max}
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: brandTop}.Layout(gtx, welcomeBrand(th))
			}),
			// 语言选择与品牌行之间留出明显的一段：这一页只有三样东西（logo、名字、
			// 语言），把语言块压到名字下面会让整页挤成一团。
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperNoteSp(th), i18n.T("ui.page.general.lang_label"))
				l.Color = paperOf().text3
				return layout.Inset{Top: langTop}.Layout(gtx, l.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if bigText {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, btns...)
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx, btns...)
				})
			}),
		)
	}
	// 内容列夹在 introColumnDp 内并水平居中：左右内衬由可用宽度现算。只加内衬，
	// 不去改 Min——内衬是从 Max 里减的，先钉 Min = Max 会把列宽再减去两倍留白。
	boxed := func(gtx layout.Context) layout.Dimensions {
		w := gtx.Dp(unit.Dp(introColumnDp))
		pad := (gtx.Constraints.Max.X - w) / 2
		if pad < 0 {
			pad = 0
		}
		return layout.Inset{Left: pxToDp(gtx, pad), Right: pxToDp(gtx, pad)}.Layout(gtx, column)
	}
	if bigText {
		// 大字号下三钮竖排会把内容顶出视口，这时宁可顶对齐可滚，也不能把正文截掉。
		return (&f.introList).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			return boxed(gtx)
		})
	}
	// 常规字号：上下居中。两条 Flexed 分摊留白——**不能放进 layout.List**：列表给
	// 子项的 Max 是无界的，Flexed 会把留白算成一整屏高，内容被推到视口外（第一版
	// 就是这么变成白屏的）。所以居中这一支不用列表，靠字号判断内容装得下。
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, gap),
		layout.Rigid(boxed),
		layout.Flexed(1, gap),
	)
}

// pxToDp 像素 → dp（内衬只认 dp；宽度是现算的，得换回 dp）。
func pxToDp(gtx layout.Context, px int) unit.Dp {
	if gtx.Metric.PxPerDp <= 0 {
		return unit.Dp(px)
	}
	return unit.Dp(float32(px) / gtx.Metric.PxPerDp)
}

// introLogoDp 第 1 页 logo 的标称边长。比首版的 64dp 大一倍：这一页只有三样东西，
// 64dp 的圆钮在 620dp 宽的正文区里读起来像个漏画的小图标。
const introLogoDp = 128

// introLogoMinDp 视口再矮也不低于这个边长（再小就不像 logo 了，Layout 会自己滚）。
const introLogoMinDp = 72

// introLogoPx logo 边长：默认 introLogoDp，视口矮时按比例收（自适应版）——窗口可以被
// 拖到很小，128dp 的圆钮加下面两段文字会顶掉语言选择。tight = 已判决要走大字号可滚版式，
// 这时直接取最小档。
func introLogoPx(gtx layout.Context, tight bool) int {
	floor := gtx.Dp(unit.Dp(introLogoMinDp))
	if tight {
		return floor
	}
	side := gtx.Dp(unit.Dp(introLogoDp))
	if h := gtx.Constraints.Max.Y; h > 0 {
		if lim := h * 22 / 100; lim < side {
			side = lim
		}
	}
	if side < floor {
		side = floor
	}
	return side
}

// welcomeBrand 品牌行：名字 + 一句话定位，与「关于」页的身份区同源同字（`aboutTagline`）。
// 两处讲同一件事就该是同一个句子，否则用户在两个窗里看到两种自我描述。
func welcomeBrand(th *material.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		// 居中要整列宽：Label 只在自己拿到的约束内对齐。
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperTitleSp(th)*1.6, "Aquarius")
				l.Font.Weight = font.Bold
				l.Color = paperOf().text
				l.Alignment = text.Middle
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperBodySp(th), aboutTagline())
				l.Color = paperOf().text2
				l.Alignment = text.Middle
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, l.Layout)
			}),
		)
	}
}

// introFitLines 第 1 页内容大致要占的正文行数。视口折算出的行数低于它就改走
// 「顶对齐 + 可滚 + 三钮竖排」，而不是硬居中（居中时超出的那部分没有滚动区可去）。
const introFitLines = 14

// introColumnDp 第 1 页正文列的宽度上限：整页居中后内容不能被拉成整幅宽
// （三颗语言钮各占三分之一窗宽，读起来像三个互不相干的按钮）。
const introColumnDp = 420

// termsPage 第 2 页：用户协议与隐私政策正文 + 勾选闸门（D278）。
//
// 正文区占满页头与勾选框之间的高度并可滚，勾选框固定在底部——长正文滚到底才看得见闸门，
// 用户不会在没读到的情况下被推着往下点（正文二十来节，滚动区一定比窗矮）。
func (f *welcomeForm) termsPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	f.loadTerms()
	check := func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(10), Bottom: unit.Dp(2)}.Layout(gtx,
			paperCheckBox(th, &f.consent, i18n.T("ui.welcome.terms.accept")))
	}
	if f.termsErr != nil {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Flexed(1, welcomePara(th, f.termsErr.Error())),
			layout.Rigid(check),
		)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return (&f.termsList).Layout(gtx, len(f.terms), func(gtx layout.Context, i int) layout.Dimensions {
				return welcomeTermsRow(gtx, th, f.terms[i])
			})
		}),
		// 滚动区与闸门之间一条 hairline：正文是被滚动的，切口落在半行上；没有这条线，
		// 勾选框看着像压在了正文上。
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			h := gtx.Dp(1)
			w := gtx.Constraints.Max.X
			paint.FillShape(gtx.Ops, paperOf().hairline, clip.Rect(image.Rect(0, 0, w, h)).Op())
			return layout.Dimensions{Size: image.Pt(w, h)}
		}),
		layout.Rigid(check),
	)
}

// termsRow 滚动区里的一项：文档标题、节标题，或 legal 的一个正文块。
type termsRow struct {
	kind termsRowKind
	text string
	cols []string
	head bool
}

// termsRowKind 滚动项的类型。
type termsRowKind int

const (
	termsNote     termsRowKind = iota // 页首那句说明（在滚动区里，不占正文高度）
	termsDocTitle                     // 一份文档的大标题
	termsSection                      // 节标题
	termsPara
	termsTip
	termsTable
	termsBullet
)

// loadTerms 取当前语言的正文（进入第 2 页时调用；语言变了重取）。
//
// 两份文档拼成一条列表：勾选是一次覆盖两份（D278 决策 4），分成两页会让「同意」指代不明。
func (f *welcomeForm) loadTerms() {
	lang := i18n.Current()
	if f.termsLoaded && f.termsLang == lang {
		return
	}
	f.termsLoaded, f.termsLang, f.termsErr = true, lang, nil
	f.terms = f.terms[:0]
	// 页首那句说明也放进滚动区：200% 字号下它要折成三行，固定在顶上会把正文区挤到
	// 只剩一行高（离屏实测）。
	if f.env.consentOnly {
		f.terms = append(f.terms, termsRow{kind: termsNote,
			text: i18n.Tf("ui.welcome.consent.renew", "version", strconv.Itoa(legal.Version))})
	}
	f.terms = append(f.terms, termsRow{kind: termsNote, text: i18n.T("ui.welcome.terms.lead")})
	for _, d := range []legal.Doc{legal.Terms, legal.Privacy} {
		doc, err := legal.Load(d, lang)
		if err != nil {
			f.termsErr = errors.New(i18n.Tf("ui.welcome.err.terms_load", "err", err.Error()))
			return
		}
		f.terms = appendDocRows(f.terms, doc)
	}
}

// appendDocRows 一份文档 → 滚动项（文档标题、导言块、各节标题与块）。
func appendDocRows(rows []termsRow, doc legal.Document) []termsRow {
	if doc.Title != "" {
		rows = append(rows, termsRow{kind: termsDocTitle, text: doc.Title})
	}
	rows = appendBlocks(rows, doc.Lead)
	for _, sec := range doc.Sections {
		if sec.Title != "" {
			rows = append(rows, termsRow{kind: termsSection, text: sec.Title})
		}
		rows = appendBlocks(rows, sec.Blocks)
	}
	return rows
}

// appendBlocks legal 块 → 滚动项。
func appendBlocks(rows []termsRow, blocks []legal.Block) []termsRow {
	for _, b := range blocks {
		switch b.Kind {
		case legal.Tip:
			rows = append(rows, termsRow{kind: termsTip, text: b.Text})
		case legal.TableRow:
			rows = append(rows, termsRow{kind: termsTable, cols: b.Cols, head: b.Head})
		case legal.Bullet:
			rows = append(rows, termsRow{kind: termsBullet, text: b.Text})
		default:
			rows = append(rows, termsRow{kind: termsPara, text: b.Text})
		}
	}
	return rows
}

// welcomeTermsRow 画一项：文档标题 / 节标题 / 正文 / 提示段（加粗 + 左侧色条）/ 表行 / 列表项。
func welcomeTermsRow(gtx layout.Context, th *material.Theme, r termsRow) layout.Dimensions {
	pp := paperOf()
	switch r.kind {
	case termsNote:
		return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperNoteSp(th), r.text)
			l.Color = pp.text3
			l.LineHeight = unit.Sp(float32(paperNoteSp(th)) * legalLineLead)
			return l.Layout(gtx)
		})
	case termsDocTitle:
		return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Subtitle1(th, r.text)
			l.Color, l.Font.Weight = pp.text, font.Bold
			return l.Layout(gtx)
		})
	case termsSection:
		return layout.Inset{Top: unit.Dp(14), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Body1(th, r.text)
			l.Color, l.Font.Weight = pp.text, font.Bold
			return l.Layout(gtx)
		})
	case termsTip:
		// 提示段：左侧竖条 + 加粗正文。竖条用 Background 画——它拿到的 Min 就是内容的
		// 尺寸，所以竖条与段落的行数严格等高（用 Flex 量的话拿不到内容高度，只能猜）。
		return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Background{}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					size := gtx.Constraints.Min
					bar := gtx.Dp(3)
					paint.FillShape(gtx.Ops, pp.accent, clip.Rect(image.Rect(0, 0, bar, size.Y)).Op())
					return layout.Dimensions{Size: size}
				},
				func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := welcomeTermsText(th, r.text, pp.text)
						l.Font.Weight = font.Bold
						return l.Layout(gtx)
					})
				},
			)
		})
	case termsTable:
		// 表行：首列当行标题（约 1/3 宽、加粗），其余列折行排在后面。
		if len(r.cols) == 0 {
			return layout.Dimensions{}
		}
		col := func(text string, bold bool, first bool) layout.Widget {
			return func(gtx layout.Context) layout.Dimensions {
				l := welcomeTermsText(th, text, pp.text)
				if bold {
					l.Font.Weight = font.Bold
				} else if !first {
					l.Color = pp.text2
				}
				return l.Layout(gtx)
			}
		}
		return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			kids := make([]layout.FlexChild, 0, len(r.cols)*2)
			kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				w := gtx.Constraints.Max.X / 3
				gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
				return col(r.cols[0], true, true)(gtx)
			}))
			if len(r.cols) > 1 {
				kids = append(kids, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, col(strings.Join(r.cols[1:], " · "), r.head, false))
				}))
			}
			return layout.Flex{}.Layout(gtx, kids...)
		})
	case termsBullet:
		return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					w := gtx.Dp(unit.Dp(14))
					gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
					l := welcomeTermsText(th, "·", pp.text3)
					return l.Layout(gtx)
				}),
				layout.Flexed(1, welcomeTermsLine(th, r.text, pp.text)),
			)
		})
	default:
		return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, welcomeTermsLine(th, r.text, pp.text))
	}
}

// welcomeTermsLine 一段法务正文（widget 形态，便于直接放进 Flex）。
func welcomeTermsLine(th *material.Theme, text string, col color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := welcomeTermsText(th, text, col)
		return l.Layout(gtx)
	}
}

// welcomeTermsText 法务正文的文本样式：正文档字号 + 1.6 行高。
//
// 行高必须显式给：法务正文是**成段的长文本**，material 的默认行距（约 1.2）会让二十来节
// 糊成一块，读三行就找不到下一行（离屏实测）。这也是整个界面上唯一需要大行距的地方。
func welcomeTermsText(th *material.Theme, text string, col color.NRGBA) material.LabelStyle {
	l := material.Label(th, paperBodySp(th), text)
	l.Color = col
	l.LineHeight = unit.Sp(float32(paperBodySp(th)) * legalLineLead)
	return l
}

// legalLineLead 法务正文本的行高系数。
const legalLineLead = 1.6

// accessPage 接入模型页（D153，模拟稿）：一张四行分组卡 + 卡外「测试连通」与内联结果。
func (f *welcomeForm) accessPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, i18n.T("ui.welcome.label.provider"), editorBox(th, &f.name, i18n.T("ui.welcome.ph.provider")))
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
	if f.modelsHint.key != "" || len(f.cands) > 0 {
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
	}
	// 现有密钥已经能解析（环境变量 / 密钥库）：明说一句，免得用户以为非得再贴一遍。
	if f.env.snap.KeyReady {
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperNoteSp(th), i18n.T("ui.welcome.access.key_ready"))
			l.Color = paperOf().accent
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, l.Layout)
		}))
	}
	kids = append(kids,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return welcomeCard(gtx, th, rows)
			})
		}),
	)
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
	text := f.modelsHint.text()
	if text == "" {
		text = i18n.T("ui.welcome.cands_hint")
	}
	return paperChipFlow(gtx, th, text, f.cands, f.candClicks)
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
	if f.probe.key == "" {
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
			l := material.Label(th, paperNoteSp(th), f.probe.text())
			l.Color = pp.text2
			return layout.Inset{Left: unit.Dp(7)}.Layout(gtx, l.Layout)
		}),
	)
}

// tutTwoColSp 两栏版式的最小可用宽度（sp）。低于它就把说明折到用例下一行：左列按 sp
// 量宽（字号放大时跟着放大），剩下的宽度不够写一句说明时不值得硬分两栏。
const tutTwoColSp = 420

// tutCmdLeftSp 用例列的标称宽度（sp）。按 sp 而不是 dp：200% 字号下 190dp 只剩五六个字
// 的位置，用例全被折成三行，右栏反而空着。
const tutCmdLeftSp = 160

// tutorialPage 第 4 页：操作指引（滚动列表：基本操作 + 常用命令）。
//
// 行型与文案在 welcome_tutorial.go，这里只定版式。用 layout.List 而不是 material.List：
// 后者的滚动条常驻，静态说明页上一条灰色竖条看着像故障（与其余四页同口径）。
func (f *welcomeForm) tutorialPage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	rows := tutorialRows(f.hotkeyLabel())
	pp := paperOf()
	twoCol := gtx.Constraints.Max.X >= gtx.Sp(unit.Sp(tutTwoColSp))
	leftPx := gtx.Sp(unit.Sp(tutCmdLeftSp))
	if lim := gtx.Constraints.Max.X * 45 / 100; leftPx > lim {
		leftPx = lim // 列宽到 45% 封顶：再宽也不是「用例 / 说明」两栏，而是两段并排
	}
	line := func(size unit.Sp, text string, col color.NRGBA) material.LabelStyle {
		l := material.Label(th, size, text)
		l.Color = col
		return l
	}
	return (&f.tutList).Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		r := rows[i]
		top, bottom := unit.Dp(3), unit.Dp(3)
		if r.kind == tutHead && i > 0 {
			top = unit.Dp(16) // 分段：不留这段，第二条粗标题会贴在上一段的最后一行说明上
		}
		return layout.Inset{Top: top, Bottom: bottom}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			switch r.kind {
			case tutHead:
				l := material.Label(th, paperSectionSp(th), r.left)
				l.Color, l.Font.Weight = pp.text, font.Bold
				return l.Layout(gtx)
			case tutCmd:
				cmd := line(paperNoteSp(th), r.left, pp.text)
				cmd.Font.Weight = font.Medium
				desc := line(paperNoteSp(th), r.right, pp.text2)
				if !twoCol {
					// 窄窗 / 大字号：用例一行、说明一行。硬分两栏的话右栏只剩几个字的
					// 位置，两栏都读不成句（608dp + 200% 字号实测）。
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(cmd.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, desc.Layout)
						}),
					)
				}
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X, gtx.Constraints.Max.X = leftPx, leftPx
						return cmd.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, desc.Layout)
					}),
				)
			default:
				return line(paperNoteSp(th), r.left, pp.text2).Layout(gtx)
			}
		})
	})
}

// donePage 第 5 页：写盘摘要（密钥只报形态、不报值）。
//
// 第 3 页按过「稍后配置」时没有接入参数可列：这时只有两句话——空着四行摘要会让用户
// 以为保存失败了，而真正要说的是「现在能用、之后在哪补」（D278 决策 3）。
func (f *welcomeForm) donePage(gtx layout.Context, th *material.Theme) layout.Dimensions {
	p := f.patch()
	// 本轮不写 provider（第 3 页按过「稍后配置」，或本来就没配）：这里不该列一份「将要
	// 写入」的摘要——那会让人以为这些参数就是这么用的。说清「没改」+ 启动前检查就够了。
	if f.accessSkipped || strings.TrimSpace(p.Provider) == "" {
		head := i18n.T("ui.welcome.done.skipped")
		if p.Provider != "" {
			head = i18n.T("ui.welcome.done.kept")
		}
		hint := i18n.T("ui.welcome.done.skipped_hint")
		if p.Provider != "" {
			hint = i18n.T("ui.welcome.done.kept_hint")
		}
		note := func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(welcomePara(th, head)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperNoteSp(th), hint)
					l.Color = paperOf().text2
					return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, l.Layout)
				}),
			)
		}
		// 有检查结果时先报它：200% 字号下这一页本来就只放得下三四行（正文区可滚），
		// 「启动后起不来」比「这一页跳过了」更该在首屏。
		kids := []layout.FlexChild{layout.Rigid(welcomeDoneIssues(th, f.doneIssues))}
		if len(f.doneIssues) > 0 {
			kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(16)}.Layout(gtx, note)
			}))
		} else {
			kids = append(kids, layout.Rigid(note))
		}
		return (&f.doneList).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
		})
	}
	keyForm := f.keyForm(p.APIKey)
	row := func(label, value string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, label, func(gtx layout.Context) layout.Dimensions {
					l := material.Body1(th, value)
					l.Color = paperOf().text
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
			row(i18n.T("ui.welcome.label.provider"), p.Provider),
			row(i18n.T("ui.welcome.label.base_url"), p.BaseURL),
			row(i18n.T("ui.welcome.label.model"), model),
			row(i18n.T("ui.welcome.label.api_key"), keyForm),
		)
	})
}

// welcomeDoneIssues 末页「启动前检查」：仍缺的东西（进入本页时由 refreshDoneIssues 现探）。
//
// 措辞说的是**启动后**会怎样，不是「保存失败」——这些项不挡保存（可启动空态 / 缺密钥由
// 装配期报因），挡的是「模型能不能用」，用户据此决定是回去补还是先到此为止。
func welcomeDoneIssues(th *material.Theme, issues []string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if len(issues) == 0 {
			return layout.Dimensions{}
		}
		pp := paperOf()
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body1(th, i18n.Tf("ui.welcome.done.check",
					"issues", strings.Join(issues, i18n.T("ui.welcome.issues_sep"))))
				l.Color = pp.danger
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperNoteSp(th), i18n.T("ui.welcome.done.check_hint"))
				l.Color = pp.text2
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, l.Layout)
			}),
		)
	}
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
	snap.NeedKey = req.NeedKey
	snap.Welcomed = false // 装配前宿主：本次一定是要引导（不是「已看、想再看」）

	w := new(app.Window)
	outcome := SetupCancelled
	var once sync.Once
	env := welcomeEnv{mgr: req.Mgr, setup: true, snap: snap, consentOnly: req.ConsentOnly}
	env.done = func(out SetupOutcome) {
		once.Do(func() {
			outcome = out
			w.Perform(system.ActionClose) // 关窗 → DestroyEvent → 事件循环退出
		})
	}
	f := newWelcomeForm(env)
	f.env.inval = w.Invalidate
	// 第 1 页切语言后窗标题也要跟着换（标题不是 i18n 表的读者，得显式重设一次）。
	// in-app 宿主不给这个回调：那边语言只记录不热切（D278）。
	f.env.retitle = func() { w.Option(app.Title(winWelcome.title())) }

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
