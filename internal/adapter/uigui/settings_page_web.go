package uigui

// settings_page_web.go 「联网」页（D162 页面注册表；D200）：搜索与抓取各自一组**有序**
// 提供商（顺序即优先级，失败换下一家），加本地抓取的网络与安全参数。
//
// 为什么单开一页而不是塞进「工具与 MCP」：这里的每一条都是「怎么连外部服务」的配置，
// 与工具启停是两件事；塞进工具页会让那张页同时承载启停开关与端点/密钥，越读越乱。
//
// 本页是**字段页**（保存走全局底栏）。提供商列表的改动与「模型服务」页同口径：
// 保存后重启生效（适配器在启动装配时构造，端口不可热换）。密钥只写不回显——编辑框
// 恒空，留空 = 保持原值（D35）。

import (
	"slices"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// webPage 联网页模块。
type webPage struct{}

func (webPage) Key() string { return "联网" }

// 联网提供商类型档（键值 = config 里的 kind 原文）。搜索与抓取支持的 kind 不同：
// brave 只做搜索、local 只做抓取——分两侧给档，界面就不会让用户选出「启动会拒」的组合。
var (
	webSearchKinds = []string{"deepseek", "brave"}
	webFetchKinds  = []string{"deepseek", "local"}
)

func (webPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{
		sec("搜索（按顺序尝试，失败换下一家）"),
	}
	for i := range f.webSearch {
		rows = append(rows, rowDesc{kind: rowWebSearchItem, field: i})
	}
	rows = append(rows, rowDesc{kind: rowWebSearchAdd}, rowDesc{kind: rowWebSearchCount})
	rows = append(rows, sec("网页抓取（按顺序尝试，失败换下一家）"))
	for i := range f.webFetch {
		rows = append(rows, rowDesc{kind: rowWebFetchItem, field: i})
	}
	rows = append(rows, rowDesc{kind: rowWebFetchAdd}, rowDesc{kind: rowWebFetchChars})
	rows = append(rows, sec("本地抓取（不经过第三方，直接取网页正文）"))
	rows = append(rows,
		rowDesc{kind: rowWebProxy}, rowDesc{kind: rowWebUA},
		rowDesc{kind: rowWebRobots}, rowDesc{kind: rowWebPrivate}, rowDesc{kind: rowWebTimeout},
		rowDesc{kind: rowWebNote},
	)
	return rows
}

func (webPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	switch rows[i].kind {
	case rowWebSearchItem:
		return f.webProviderRow(gtx, th, f.webSearch[rows[i].field], true), true
	case rowWebSearchAdd:
		return paperButton(th, &f.webSearchAdd, "＋ 添加搜索提供商", paperBtnSecondary, true)(gtx), true
	case rowWebSearchCount:
		return fieldRow(gtx, th, "结果条数", editorBox(th, &f.webCount, "缺省 8（1–20）；工具参数可逐次覆盖（空 = 保持）")), true
	case rowWebFetchItem:
		return f.webProviderRow(gtx, th, f.webFetch[rows[i].field], false), true
	case rowWebFetchAdd:
		return paperButton(th, &f.webFetchAdd, "＋ 添加抓取提供商", paperBtnSecondary, true)(gtx), true
	case rowWebFetchChars:
		return fieldRow(gtx, th, "正文上限", editorBox(th, &f.webMaxChars, "字符；缺省 16000（500–20000）（空 = 保持）")), true
	case rowWebProxy:
		return fieldRow(gtx, th, "代理", editorBox(th, &f.webProxy, "如 http://127.0.0.1:7897；空 = 读环境变量")), true
	case rowWebUA:
		return fieldRow(gtx, th, "User-Agent", editorBox(th, &f.webUA, "空 = 浏览器形态缺省值（很多站点拒绝非浏览器 UA）")), true
	case rowWebRobots:
		return paperCheckBox(th, &f.webRobots, "遵守 robots.txt（站点声明不许抓的路径就不抓）")(gtx), true
	case rowWebPrivate:
		return paperCheckBox(th, &f.webPrivate, "允许访问本机与内网地址（只用于调试本机服务）")(gtx), true
	case rowWebTimeout:
		return fieldRow(gtx, th, "超时", editorBox(th, &f.webTimeout, "秒；缺省 30（空 = 保持）")), true
	case rowWebNote:
		l := material.Body2(th, "两组列表都是有序的：先试第一家，失败换下一家。"+
			"端点与密钥可以自己填，也可以选「借用」再点「填入」——那会把「模型服务」里那条的端点与密钥引用抄过来，"+
			"之后照样能改。密钥只写不回显（留空 = 保持）。本地抓取不需要密钥，但会拒绝内网与回环地址。"+
			"提供商与安全参数的改动保存后重启生效。")
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	}
	return layout.Dimensions{}, false
}

func (webPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	for f.webSearchAdd.Clicked(gtx) {
		f.addWebProvider(true)
	}
	for f.webFetchAdd.Clicked(gtx) {
		f.addWebProvider(false)
	}
	for i, pf := range f.webSearch {
		f.webProviderEvents(gtx, pf, true, i)
	}
	for i, pf := range f.webFetch {
		f.webProviderEvents(gtx, pf, false, i)
	}
}

func (webPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.webCount }

// webKindHint 联网提供商类型的一句话说明（帮助用户理解该选哪个）。
func webKindHint(kind string) string {
	switch kind {
	case "deepseek":
		return "端点与密钥可自填，也可借「模型服务」"
	case "brave":
		return "需订阅令牌"
	case "local":
		return "本机抓取，无需密钥"
	default:
		return ""
	}
}

// webProviderForm 联网提供商一条的编辑控件组（D200；本窗 goroutine 独占）。
type webProviderForm struct {
	orig    WebProviderView  // 快照原值（保存提示与密钥形态判定的基线）
	kind    cycleField       // 类型（搜索/抓取各一组档）
	from    cycleField       // 借哪个 model.providers 条目（D222：候选来自「模型服务」）
	fromUse widget.Clickable // 「填入」：把 from 那条的端点与密钥引用抄进本条目
	key     widget.Editor    // 自己的密钥引用（只写不回显）
	baseURL widget.Editor    // 自己的端点
	country widget.Editor    // brave：结果国家
	lang    widget.Editor    // brave：结果语言
	count   widget.Editor    // 搜索：这一家的缺省条数
	up      widget.Clickable
	down    widget.Clickable
	del     widget.Clickable
}

// newWebProviderForm 由快照造一条编辑控件组；空快照 = 新增（kind 取该侧首档）。
// choices 是「借」的下拉候选（model.providers 的名字，D222）。
func newWebProviderForm(v WebProviderView, search bool, choices []ProviderChoice) *webProviderForm {
	pf := &webProviderForm{orig: v}
	if search {
		pf.kind = cycleField{displays: webSearchKinds, values: webSearchKinds}
	} else {
		pf.kind = cycleField{displays: webFetchKinds, values: webFetchKinds}
	}
	pf.kind.setValue(v.Kind) // "" 未命中 → 首档（deepseek）
	// 「借」的下拉：首项「不借」+ 各 provider 名；当前值不在候选里时补一项，
	// 免得开窗即把一条失联的引用悄悄改成「不借」。
	displays := []string{"不借"}
	values := []string{""}
	for _, c := range choices {
		if c.Name == "" || slices.Contains(values, c.Name) {
			continue
		}
		displays = append(displays, c.Name)
		values = append(values, c.Name)
	}
	if f := strings.TrimSpace(v.From); f != "" && !slices.Contains(values, f) {
		displays = append(displays, f)
		values = append(values, f)
	}
	pf.from = cycleField{displays: displays, values: values}
	pf.from.setValue(strings.TrimSpace(v.From))
	// 快照没写 from 的 deepseek（老配置 `{"kind":"deepseek"}`）实际是借同名条目：
	// 下拉就显示它，而不是「不借」——否则界面说的和启动做的事不一致。
	if strings.TrimSpace(v.From) == "" && pf.kind.value() == "deepseek" && slices.Contains(values, "deepseek") {
		pf.from.setValue("deepseek")
	}
	for _, ed := range []*widget.Editor{&pf.key, &pf.baseURL, &pf.country, &pf.lang, &pf.count} {
		ed.SingleLine = true
	}
	pf.key.Mask = '•'
	pf.baseURL.SetText(v.BaseURL)
	pf.country.SetText(v.Country)
	pf.lang.SetText(v.Lang)
	if v.Count > 0 {
		pf.count.SetText(strconv.Itoa(v.Count))
	}
	return pf
}

// keyNote 密钥编辑框下方的一行形态说明（值永不回显，只说明「现在是哪种形态」）。
func (pf *webProviderForm) keyNote() string {
	if pf.orig.KeySaved {
		ref := pf.orig.KeyRef
		if ref == "" {
			ref = "secret:…"
		}
		return "已存引用 " + ref + "；留空 = 保持不变"
	}
	if pf.orig.KeyPlain {
		return "当前是明文（启动会警告）；填新值覆盖，留空 = 保持不变"
	}
	return "留空 = 用「借用」那条的；都没有则按类型回落（brave 用 AQUARIUS_BRAVE_API_KEY，" +
		"deepseek 搜索用 AQUARIUS_DEEPSEEK_SEARCH_API_KEY、抓取用 _FETCH_）"
}

// webProviderRow 联网提供商一项：类型 / 借哪个服务 / 端点 / 密钥 / 国家语言 / 条数 +
// 上移 / 下移 / 删除。
//
// 端点与密钥（D222）：两者都可以自己填。填了就用自己那份；留空则按「借」抄过来的
// 值——所以「借」现在是省事的起点，而不是唯一形态。「填入」按钮把那一条的端点与密钥
// 引用抄进编辑框，之后照常可以改（只抄引用名，永不抄值）。
func (f *settingsForm) webProviderRow(gtx layout.Context, th *material.Theme, pf *webProviderForm, search bool) layout.Dimensions {
	kind := pf.kind.value()
	brave := kind == "brave"
	local := kind == "local"
	line := func(top int, w layout.Widget) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(top)}.Layout(gtx, w)
		}
	}
	items := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "类型", func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(cycleBtn(th, &pf.kind)),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := material.Label(th, paperNoteSp(th), webKindHint(kind))
							l.Color = paperOf().text3
							return l.Layout(gtx)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(paperButton(th, &pf.up, "↑", paperBtnSecondary, true)),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, paperButton(th, &pf.down, "↓", paperBtnSecondary, true))
								}),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, paperButton(th, &pf.del, "删除", paperBtnDanger, true))
								}),
							)
						})
					}),
				)
			})
		}),
	}
	if !local {
		items = append(items, layout.Rigid(line(6, func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "端点", editorBox(th, &pf.baseURL, "如 https://api.deepseek.com；留空 = 用「借用」那条的"))
		})))
		// 「借」只对需要鉴权的类型有意义（local 没有端点也没有密钥）。
		items = append(items, layout.Rigid(line(6, func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "借用", func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(cycleBtn(th, &pf.from)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, paperButton(th, &pf.fromUse, "填入", paperBtnSecondary, true))
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := material.Label(th, paperNoteSp(th), "把它的端点与密钥引用抄进本条目，之后可改")
							l.Color = paperOf().text3
							return l.Layout(gtx)
						})
					}),
				)
			})
		})))
	}
	if brave || kind == "deepseek" {
		items = append(items, layout.Rigid(line(6, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return fieldRow(gtx, th, "密钥", editorBox(th, &pf.key, "secret:名 或明文；留空 = 保持 / 借用"))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, paperMicroSp(th), pf.keyNote())
						l.Color = paperOf().text3
						return l.Layout(gtx)
					})
				}),
			)
		})))
	}
	if brave {
		items = append(items, layout.Rigid(line(6, func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "国家 / 语言", func(gtx layout.Context) layout.Dimensions {
				// 两个 Flexed 等分（Rigid 先量、editorBox 铺满 Max.X，会把 Flexed 挤成 0 宽）。
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, editorBox(th, &pf.country, "如 CN（空 = 服务端缺省）")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Flexed(1, editorBox(th, &pf.lang, "如 zh-hans（空 = 服务端缺省）")),
				)
			})
		})))
	}
	if search {
		items = append(items, layout.Rigid(line(6, func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, "本条条数", editorBox(th, &pf.count, "空 = 用上面的「结果条数」"))
		})))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, items...)
}

// webProviderEvents 一条提供商的事件消费（循环档 + 「填入」+ 上移/下移/删除）。
func (f *settingsForm) webProviderEvents(gtx layout.Context, pf *webProviderForm, search bool, i int) {
	pf.kind.advance(gtx)
	pf.from.advance(gtx)
	for pf.fromUse.Clicked(gtx) {
		f.fillWebProviderFrom(pf)
	}
	for pf.up.Clicked(gtx) {
		f.moveWebProvider(search, i, -1)
	}
	for pf.down.Clicked(gtx) {
		f.moveWebProvider(search, i, 1)
	}
	for pf.del.Clicked(gtx) {
		f.removeWebProvider(search, i)
	}
}

// fillWebProviderFrom 「填入」：把「借用」选中那条的端点与密钥引用抄进本条目（D222）。
//
// 只抄**引用名**，不抄值——密钥值只在密钥库里，编辑框永远不回显（D35）。抄过去之后
// 这条就变成独立配置，用户照常可以改端点、换密钥，与「借用」那条再无关系。
// 目标条目在「模型服务」里没有已入库的引用时，密钥框保持原样（不凭空造一个引用名）。
func (f *settingsForm) fillWebProviderFrom(pf *webProviderForm) {
	name := pf.from.value()
	if name == "" {
		return
	}
	for _, c := range f.chainChoices {
		if c.Name != name {
			continue
		}
		if c.BaseURL != "" {
			pf.baseURL.SetText(c.BaseURL)
		}
		if c.KeyRef != "" {
			pf.key.SetText(c.KeyRef)
		}
		return
	}
}

// webList 取一侧的提供商编辑列表（search = 搜索侧）。
func (f *settingsForm) webList(search bool) *[]*webProviderForm {
	if search {
		return &f.webSearch
	}
	return &f.webFetch
}

// addWebProvider 追加一条空白提供商（新增项缺省取该侧首档 = deepseek）。
func (f *settingsForm) addWebProvider(search bool) {
	list := f.webList(search)
	*list = append(*list, newWebProviderForm(WebProviderView{}, search, f.chainChoices))
}

// moveWebProvider 上移/下移一项（dir = -1 / +1；越界 no-op）。
func (f *settingsForm) moveWebProvider(search bool, i, dir int) {
	list := *f.webList(search)
	j := i + dir
	if i < 0 || i >= len(list) || j < 0 || j >= len(list) {
		return
	}
	list[i], list[j] = list[j], list[i]
}

// removeWebProvider 删除一项（越界 no-op）。
func (f *settingsForm) removeWebProvider(search bool, i int) {
	list := *f.webList(search)
	if i < 0 || i >= len(list) {
		return
	}
	*f.webList(search) = append(list[:i], list[i+1:]...)
}

// loadWeb 以快照重建联网页（开窗填表）。
func (f *settingsForm) loadWeb(s SettingsSnapshot) {
	f.webSearch = make([]*webProviderForm, 0, len(s.WebSearchProviders))
	for _, v := range s.WebSearchProviders {
		f.webSearch = append(f.webSearch, newWebProviderForm(v, true, f.chainChoices))
	}
	f.webFetch = make([]*webProviderForm, 0, len(s.WebFetchProviders))
	for _, v := range s.WebFetchProviders {
		f.webFetch = append(f.webFetch, newWebProviderForm(v, false, f.chainChoices))
	}
	if s.WebSearchCount > 0 {
		f.webCount.SetText(strconv.Itoa(s.WebSearchCount))
	}
	if s.WebFetchMaxChars > 0 {
		f.webMaxChars.SetText(strconv.Itoa(s.WebFetchMaxChars))
	}
	f.webProxy.SetText(s.WebFetchProxy)
	f.webUA.SetText(s.WebFetchUserAgent)
	f.webRobots.Value = s.WebFetchRobots
	f.webPrivate.Value = s.WebFetchPrivate
	if s.WebFetchTimeoutSec > 0 {
		f.webTimeout.SetText(strconv.Itoa(s.WebFetchTimeoutSec))
	}
}

// webProviderPatches 组装一侧的提供商提交（编辑区现值 → patch）。密钥框留空 = 不带值
// （cmd 侧按「保持原引用」处理），填了 `secret:` 前缀照写引用、否则按明文落盘。
func (f *settingsForm) webProviderPatches(search bool) []WebProviderPatch {
	list := *f.webList(search)
	out := make([]WebProviderPatch, 0, len(list))
	for _, pf := range list {
		out = append(out, WebProviderPatch{
			Kind:    strings.TrimSpace(pf.kind.value()),
			From:    strings.TrimSpace(pf.from.value()),
			Key:     strings.TrimSpace(pf.key.Text()),
			BaseURL: strings.TrimSpace(pf.baseURL.Text()),
			Country: strings.TrimSpace(pf.country.Text()),
			Lang:    strings.TrimSpace(pf.lang.Text()),
			Count:   atoiOr0(pf.count.Text()),
		})
	}
	return out
}

// atoiOr0 宽松整数解析：空串/非法一律 0（= 用上层缺省），负数夹到 0。
func atoiOr0(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
