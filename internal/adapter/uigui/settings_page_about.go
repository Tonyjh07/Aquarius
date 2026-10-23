package uigui

// settings_page_about.go 「关于」页（D208，页面注册表见 settings_page.go）：图标、名字与
// 一句话定位、构建版本、运行环境、项目主页、版权与许可。
//
// 本页**没有配置项**——不进快照、不进 patch，「保存并应用」对它无意义（按下只报没有
// 改动）。因此 FirstEditor 返回 nil：开窗不把焦点抢到本页（本页没有可手输的字段）。
//
// 版本号来自装配根注入的 `-ldflags -X main.version`（Options.Version），空或 "dev" =
// 开发构建；平台与 Go 版本取 runtime，不必再由装配处传一遍。

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"runtime"
	"strings"
	"sync"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/assets"
)

// aboutProjectURL 项目主页（GitHub）。
const aboutProjectURL = "https://github.com/Tonyjh07/Aquarius"

// aboutTagline 一句话定位。
const aboutTagline = "你的极简 AI 助手"

// aboutCopyright 版权行。年份与作者是**声明的口径**，写死而不从构建期推导——它不该随
// 谁在哪台机器上编译而变。
const aboutCopyright = "© 2026 Tonyjh07"

// aboutLicense 许可证（仓库根 LICENSE = MPL-2.0 全文）。
const aboutLicense = "Mozilla Public License 2.0"

// aboutPrivacy 数据边界（与 §9 口径一致）：只说「不收集」会被读成「连模型服务都不发」，
// 所以两句都说——不收集什么，以及哪些服务会收到内容。
const aboutPrivacy = "不收集使用数据，也不做联网上报：会话、附件与配置都留在本机；" +
	"除了你自己配置的模型服务、语音服务与 MCP server 之外，本程序不与其它服务通信。"

// aboutIconDp 图标边长（dp，经 gtx.Dp 消费）。
const aboutIconDp unit.Dp = 72

// aboutPage 关于页模块。
type aboutPage struct{}

func (aboutPage) Key() string { return "关于" }

func (aboutPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{
		{kind: rowAboutHero},
		sec("构建"),
		{kind: rowAboutVersion},
		{kind: rowAboutRuntime},
		sec("项目"),
		{kind: rowAboutProject},
		sec("版权与许可"),
		{kind: rowAboutCopyright},
		{kind: rowAboutLicense},
		{kind: rowAboutNote},
	}
	if f.aboutStatus != "" {
		// 打开 / 复制的反馈排在末尾；空串不占行（免得页尾留一块空白）。
		rows = append(rows, rowDesc{kind: rowAboutStatus})
	}
	return rows
}

func (aboutPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	switch rows[i].kind {
	case rowAboutHero:
		return aboutHeroRow(gtx, th, f), true
	case rowAboutVersion:
		return fieldRow(gtx, th, "构建版本", func(gtx layout.Context) layout.Dimensions {
			return aboutValue(gtx, th, aboutVersionText(f.aboutVersion))
		}), true
	case rowAboutRuntime:
		return fieldRow(gtx, th, "运行环境", func(gtx layout.Context) layout.Dimensions {
			return aboutValue(gtx, th, aboutRuntimeText())
		}), true
	case rowAboutProject:
		return aboutProjectRow(gtx, th, f), true
	case rowAboutCopyright:
		return fieldRow(gtx, th, "版权", func(gtx layout.Context) layout.Dimensions {
			return aboutValue(gtx, th, aboutCopyright)
		}), true
	case rowAboutLicense:
		return fieldRow(gtx, th, "许可证", func(gtx layout.Context) layout.Dimensions {
			return aboutValue(gtx, th, aboutLicense)
		}), true
	case rowAboutNote:
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(paperNote(th, aboutPrivacy)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, paperNote(th,
					"图标与产品名归作者所有；许可证全文见程序目录与项目主页。"))
			}),
		), true
	case rowAboutStatus:
		return aboutStatusRow(gtx, th, f), true
	}
	return layout.Dimensions{}, false
}

func (aboutPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	for f.aboutOpen.Clicked(gtx) {
		f.aboutStatus, f.aboutErr = "", false
		if u.plat == nil {
			f.aboutErr = true
			f.aboutStatus = "打不开浏览器：平台未接线；可以复制链接后手动打开"
			continue
		}
		if err := u.plat.OpenURL(aboutProjectURL); err != nil {
			f.aboutErr = true
			f.aboutStatus = "打不开浏览器：" + err.Error() + "；可以复制链接后手动打开"
			continue
		}
		f.aboutStatus = "已在浏览器中打开"
	}
	for f.aboutCopy.Clicked(gtx) {
		f.aboutStatus, f.aboutErr = "", false
		gtx.Execute(clipboard.WriteCmd{
			Type: "application/text",
			Data: io.NopCloser(strings.NewReader(aboutProjectURL)),
		})
		f.aboutStatus = "链接已复制"
	}
}

// FirstEditor 本页无编辑档（没有可手输的字段，开窗不抢焦点）。
func (aboutPage) FirstEditor(*settingsForm) *widget.Editor { return nil }

// aboutVersionText 版本行文案：发布构建给版本号，开发构建明说「不是发布版」。
func aboutVersionText(version string) string {
	v := strings.TrimSpace(version)
	if v == "" || v == "dev" {
		return "开发构建（未注入版本号）"
	}
	return v
}

// aboutRuntimeText 运行环境：平台与 Go 版本（与 `-version` 输出后半段同源同序）。
func aboutRuntimeText() string {
	return runtime.GOOS + "/" + runtime.GOARCH + " · " + runtime.Version()
}

// aboutValue 只读值文字：正文色 + 与编辑框首行齐平的顶端内衬（读起来仍是同一条表，
// 只是右侧不可改）。
func aboutValue(gtx layout.Context, th *material.Theme, text string) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.Body1(th, text).Layout(gtx)
	})
}

// aboutProjectRow 项目主页：链接全文 + 「打开项目主页」+「复制链接」。
//
// 链接同时显示全文：浏览器打不开（无默认浏览器 / 被策略拦住）时，用户还能抄走。
func aboutProjectRow(gtx layout.Context, th *material.Theme, f *settingsForm) layout.Dimensions {
	return fieldRow(gtx, th, "项目主页", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body1(th, aboutProjectURL)
				l.Color = paperOf().accent
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, l.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(paperButton(th, &f.aboutOpen, "打开项目主页", paperBtnPrimary, true)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(8)}.Layout(gtx,
								paperButton(th, &f.aboutCopy, "复制链接", paperBtnSecondary, true))
						}),
					)
				})
			}),
		)
	})
}

// aboutStatusRow 打开 / 复制的就地反馈（失败红字，成功次要色）。
func aboutStatusRow(gtx layout.Context, th *material.Theme, f *settingsForm) layout.Dimensions {
	l := material.Body2(th, f.aboutStatus)
	l.Color = paperOf().text3
	if f.aboutErr {
		l.Color = paperOf().danger
	}
	return l.Layout(gtx)
}

// aboutHeroRow 图标 + 名字 + 一句话（不落卡：它是页面的身份区，不是一行表单）。
func aboutHeroRow(gtx layout.Context, th *material.Theme, f *settingsForm) layout.Dimensions {
	side := gtx.Dp(aboutIconDp)
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return aboutIcon(gtx, &f.aboutIcon, side)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperTitleSp(th)*1.6, "Aquarius")
					l.Font.Weight = font.Bold
					l.Color = paperOf().text
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, paperBody(th, aboutTagline))
				}),
			)
		}),
	)
}

// 图标：内嵌 PNG 只解一次（sync.Once 之后源图只读，跨窗口安全）。
var (
	aboutIconOnce sync.Once
	aboutIconSrc  image.Image
)

// aboutIconCache 按目标像素边长缓存一档预缩放图（表单字段，本窗 goroutine 独占）。
type aboutIconCache struct {
	px  int
	op  paint.ImageOp
	has bool
}

// aboutIconOp 取 side 像素见方的图标（解码失败 → ok=false，退回纯色圆）。
//
// **不复用主窗的 logoImageOp**：那边是「单档缓存 + 包级变量」，主窗与设置窗各在自己的
// 事件循环里请求不同尺寸时会互相顶掉缓存，并构成数据竞争。这里缓存挂在表单上，源图
// 各自解一次（128×128，代价可忽略）。
func aboutIconOp(c *aboutIconCache, side int) (paint.ImageOp, bool) {
	if side <= 0 || c == nil {
		return paint.ImageOp{}, false
	}
	aboutIconOnce.Do(func() {
		if len(assets.LogoPNG) == 0 {
			return
		}
		img, err := png.Decode(bytes.NewReader(assets.LogoPNG))
		if err != nil {
			return
		}
		aboutIconSrc = img
	})
	if aboutIconSrc == nil {
		return paint.ImageOp{}, false
	}
	if c.has && c.px == side {
		return c.op, true
	}
	c.px, c.op, c.has = side, paint.NewImageOp(scaleBox(aboutIconSrc, image.Pt(side, side))), true
	return c.op, true
}

// aboutIcon 品牌色圆底 + 内嵌图标（图标透明角露出品牌色，与主窗悬浮球同款观感）。
func aboutIcon(gtx layout.Context, c *aboutIconCache, side int) layout.Dimensions {
	if side <= 0 {
		return layout.Dimensions{}
	}
	box := image.Rect(0, 0, side, side)
	st := clip.UniformRRect(box, side/2).Push(gtx.Ops)
	paint.Fill(gtx.Ops, paperOf().brand)
	if im, ok := aboutIconOp(c, side); ok {
		im.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops) // ImageOp 只设材质，必须紧跟 PaintOp 才出图
	}
	st.Pop()
	return layout.Dimensions{Size: image.Pt(side, side)}
}
