package uigui

// history_import_pick.go 导入多会话归档时的**勾选面板**（D249，§5.16）。
//
// 为什么不能选完文件直接投 `/import`：aqx 能装一组会话（D249），用户不知道自己刚挑的是
// 哪份备份里的哪些会话；整份灌进来之后要挑出不要的那几个，只能再逐个删——那是先制造问题
// 再解决它。所以拿到路径后先向内核要清单（`ExchangeIndexer.Index`，只读 `manifest.json`，
// 不解析树），把「里面有什么」摊开，让用户勾完再导。
//
// 三条口径：
//   - 复选框**缺省全勾**：备份恢复的主路径就是全要，缺省不该逼用户再点一次「全选」；
//   - **只有一项时不弹面板**：非 aqx（只有一条会话链）或只装了一个会话的 aqx，弹一行勾选框
//     只是多一次点击——直接投（由 drainFilePicks 分流，本文件只管多会话那一路）；
//   - 确认投 `/import --pick <序号>… <路径>`，取消什么都不投；序号取清单序号（1 起），
//     壳里不解析容器（铁律 5）。
//
// 模态机制与遮罩 / 确认弹窗 / 摘要小卡同源（overlay.go 头注）：整窗输入区排在命中链顶，
// 背景侧（historyState.dispatch 与 relGraph.update）再按 modalActive 显式不取事件。

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 勾选面板几何（dp）：定宽 + 视口高比例；条目多时在卡内滚。
const (
	pickCardWdp  unit.Dp = 420
	pickCardHFac         = 0.52
)

// importPickDialog 导入勾选面板的帧状态（**仅历史窗 goroutine 读写**；零值不可用，
// 经 openImportPick 构造）。
type importPickDialog struct {
	// tag 整窗输入区的路由锚点（Gio 要求地址稳定）。
	tag  struct{}
	path string      // 要导入的文件（原样回传到命令面）
	list widget.List // 条目区滚动（会话多时在卡内滚）
	// rows 每行的复选框，按下标与 entries 对齐（打开时一次建好：清单在面板存续期间不变）。
	rows   []widget.Bool
	ok     widget.Clickable
	cancel widget.Clickable
	focus  bool
	// entries 清单快照（序号 / 标题 / 条数）；只供呈现与拼 --pick。
	entries []port.ExchangeEntry
}

// openImportPick 开勾选面板（entries 至少两项；不足两项时调用方应直接投导入）。
func (st *historyState) openImportPick(path string, entries []port.ExchangeEntry) {
	if len(entries) == 0 {
		return
	}
	d := &importPickDialog{path: path, entries: entries, focus: true}
	d.list.List.Axis = layout.Vertical
	d.rows = make([]widget.Bool, len(entries))
	for i := range d.rows {
		d.rows[i].Value = true // 缺省全勾：备份恢复的主路径就是全要
	}
	st.pick = d
}

// closeImportPick 关勾选面板（Esc / 取消 / 已提交）。
func (st *historyState) closeImportPick() { st.pick = nil }

// picked 当前勾中的清单序号（升序：清单顺序即导入顺序，勾选顺序不参与语义）。
func (d *importPickDialog) picked() []int {
	out := make([]int, 0, len(d.rows))
	for i := range d.rows {
		if d.rows[i].Value {
			out = append(out, d.entries[i].Index)
		}
	}
	return out
}

// count 已勾几个（动作条与按钮文案用）。
func (d *importPickDialog) count() int { return len(d.picked()) }

// importPickFrame 勾选面板单帧：未开 = 零尺寸空操作；开 = scrim + 居中卡片。
func (st *historyState) importPickFrame(gtx layout.Context, th *material.Theme, u *UI) layout.Dimensions {
	d := st.pick
	if d == nil {
		return layout.Dimensions{}
	}
	// 整窗命中区 + 路由锚点：隔离的主机制（同遮罩 / 确认弹窗 / 摘要小卡）。
	stack := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	defer stack.Pop()
	gtx.Event(key.FocusFilter{Target: &d.tag})
	event.Op(gtx.Ops, &d.tag)
	// 键盘焦点夺到面板：Gio 只把键事件投给当前获焦的 handler，不夺的话 Esc 会落到
	// 背景的编辑器上（历史窗本身没有编辑器，但焦点可能仍在别的窗口里）。
	if d.focus {
		d.focus = false
		gtx.Execute(key.FocusCmd{Tag: &d.tag})
	}

	// Esc 关闭（不投命令）。
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &d.tag, Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
			st.closeImportPick()
			return layout.Dimensions{}
		}
	}

	// 先收动作再画（顺序即语义：一次点击只算一件事）。
	if d.cancel.Clicked(gtx) {
		st.closeImportPick()
		return layout.Dimensions{}
	}
	if d.ok.Clicked(gtx) {
		// 一个都没勾时「导入」呈禁用态，点击不投命令、也不关卡（用户正要重新勾）。
		if picks := d.picked(); len(picks) > 0 {
			st.sendImportPick(u, d.path, picks)
			st.closeImportPick()
			return layout.Dimensions{}
		}
	}

	card := importPickCardBox(gtx)
	paint.FillShape(gtx.Ops, color.NRGBA{A: overlayScrimAlpha},
		clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
	cardRad := gtx.Dp(unit.Dp(paperOf().rCard))
	paperShadow(gtx, card, cardRad, 14, 8, 0x38)
	// 显式定尺定位到卡片盒（同遮罩 / 确认弹窗：layout.Stacked 会清 Min，走 Center 会错位）。
	gtx.Constraints.Min, gtx.Constraints.Max = card.Size(), card.Size()
	trans := op.Offset(card.Min).Push(gtx.Ops)
	dims := importPickCard(gtx, th, d)
	trans.Pop()
	dims.Size = card.Size()
	return dims
}

// sendImportPick 投 `/import --pick <序号>… <路径>`（序号按清单顺序、一个 --pick 带全部）。
//
// 序号用**清单序号**而不是标题：标题可能重名或为空，序号是适配器与界面之间唯一的稳定
// 锚点（`resolvePick` 认它）。
func (st *historyState) sendImportPick(u *UI, path string, picks []int) {
	args := make([]string, 0, len(picks)+2)
	for i, n := range picks {
		if i == 0 {
			args = append(args, "--pick", fmt.Sprint(n))
			continue
		}
		args[1] += "," + fmt.Sprint(n) // 一条 --pick 带全部：逗号分隔（命令面认）
	}
	args = append(args, path)
	st.sendCmd(u, "import", args...)
}

// importPickCardBox 卡片盒（居中、定宽、按视口高夹取）。
func importPickCardBox(gtx layout.Context) image.Rectangle {
	margin := gtx.Dp(overlayCardMargindp)
	avail := gtx.Constraints.Max
	w := gtx.Dp(pickCardWdp)
	if lim := avail.X - 2*margin; w > lim {
		w = lim
	}
	if w < 1 {
		w = avail.X
	}
	h := int(float64(avail.Y) * pickCardHFac)
	if lim := avail.Y - 2*margin; h > lim {
		h = lim
	}
	if h < 1 {
		h = avail.Y
	}
	card := image.Rect(0, 0, w, h)
	return card.Add(image.Pt((avail.X-card.Dx())/2, (avail.Y-card.Dy())/2))
}

// importPickCard 卡片内容：标题 + 一行说明 + 条目列表（可滚）+ 底行「取消 / 导入 N 个」。
func importPickCard(gtx layout.Context, th *material.Theme, d *importPickDialog) layout.Dimensions {
	pp := paperOf()
	paperPanel(gtx, image.Rectangle{Max: gtx.Constraints.Max},
		gtx.Dp(unit.Dp(pp.rCard)), gtx.Dp(1), pp.card, pp.hairline)
	n := d.count()
	btn := fmt.Sprintf("导入 %d 个", n)
	return layout.UniformInset(overlayCardPadDp).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperNoteSp(th), "选择要导入的会话")
				l.Font.Weight = font.Bold
				l.Color = pp.text
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th),
					fmt.Sprintf("这份备份里有 %d 个会话，默认全要；不要的取消勾选。导进来的都是新会话，不动现有的。", len(d.entries)))
				l.Color = pp.text3
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return d.list.Layout(gtx, len(d.entries), func(gtx layout.Context, i int) layout.Dimensions {
					return importPickRow(gtx, th, &d.rows[i], d.entries[i])
				})
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, paperMicroSp(th), fmt.Sprintf("已选 %d / %d", n, len(d.entries)))
						l.Color = pp.text3
						return l.Layout(gtx)
					}),
					layout.Rigid(overlayBtn(&d.cancel, th, "取消", false, true)),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(overlayBtn(&d.ok, th, btn, true, n > 0)),
				)
			}),
		)
	})
}

// importPickRow 一行条目：复选框（不勾时也占位，勾选不引起重排）+ 标题 + 条数。
func importPickRow(gtx layout.Context, th *material.Theme, box *widget.Bool, e port.ExchangeEntry) layout.Dimensions {
	pp := paperOf()
	title := e.Title
	if title == "" {
		title = "（无标题）"
	}
	return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// 复选框自带 26dp 见方（material 按 4/3 放宽），这里不再加内衬。
				return paperCheckBox(th, box, "")(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperNoteSp(th), title)
				l.Color = pp.text
				l.MaxLines = 1
				return l.Layout(gtx)
			}),
			layout.Flexed(1, layout.Spacer{}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), fmt.Sprintf("%d 条", e.Nodes))
				l.Color = pp.text3
				return l.Layout(gtx)
			}),
		)
	})
}
