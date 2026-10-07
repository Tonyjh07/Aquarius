package uigui

import (
	"image"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// fieldH 文本档高度下限（dp，经 gtx.Dp 消费）：单行编辑器 + 上下留白（material 无
// 内建输入框边框）。实际高由 editorBox 自适应（≥ 编辑器行高 + 留白，D113 验收补充）。
const fieldH = 40

// fieldLabelW 标签列宽（dp，经 gtx.Dp 消费）：对齐各行控件起点。
const fieldLabelW = 92

// rowKind 表单行类型（动态行计划：provider 编辑区行数随 providers 数量变化，D110②）。
type rowKind int

const (
	rowTitle rowKind = iota
	rowProfHead
	rowProfCurrent
	rowProfName
	rowProfBtns
	rowSecModel
	rowProvField
	rowProvBtns
	rowProvAdd
	rowProvApply
	rowSecCore
	rowPerm
	rowThink
	rowEffort
	rowSecLook
	rowHotkey
	rowTheme
	rowScale
	rowFontSize
	rowWinSize
	rowSecOut
	rowNotify
	rowTTS
	rowSecLimits
	rowMaxTurns
	rowMaxCtx
	rowCompact
	rowToolChars
	rowToolTo
	rowSecPrompt
	rowSysPrompt
	rowSecMCP
	rowMCPList
	rowSecAdv
	rowRaw
	rowActions
)

// provider 字段槽位（rowDesc.field）。
const (
	provNameFld = iota
	provURLFld
	provKeyFld
	provModelsFld
	provTokFld
	provUnsupFld
	provFldN
)

// provFieldMeta provider 字段行的标签与占位提示（下标对齐字段槽位）。
var provFieldMeta = [provFldN]struct{ label, hint string }{
	{"名称", "provider 唯一标识（primary/fallback 按名引用）"},
	{"接口地址", "如 https://api.openai.com/v1"},
	{"API 密钥", "只写不回显；留空保持不变（D35）"},
	{"模型列表", "逗号分隔；首个 = 缺省请求模型"},
	{"Tokenizer", "本地 tokenizer.json 路径；空 = 估算"},
	{"不认参数", "逗号分隔；服务端不认的请求参数（D34）"},
}

// rowDesc 一行的类型与定位。
type rowDesc struct {
	kind  rowKind
	prov  int // rowProvField / rowProvBtns：provider 下标
	field int // rowProvField：字段槽位
}

// rowPlan 本帧行计划（每帧现算：providers 数量在新增/删除后即变）。
func (f *settingsForm) rowPlan() []rowDesc {
	rows := []rowDesc{
		{kind: rowTitle}, {kind: rowProfHead}, {kind: rowProfCurrent},
		{kind: rowProfName}, {kind: rowProfBtns}, {kind: rowSecModel},
	}
	for i := range f.provs {
		for fld := 0; fld < provFldN; fld++ {
			rows = append(rows, rowDesc{kind: rowProvField, prov: i, field: fld})
		}
		rows = append(rows, rowDesc{kind: rowProvBtns, prov: i})
	}
	return append(rows,
		rowDesc{kind: rowProvAdd}, rowDesc{kind: rowProvApply},
		rowDesc{kind: rowSecCore}, rowDesc{kind: rowPerm}, rowDesc{kind: rowThink}, rowDesc{kind: rowEffort},
		rowDesc{kind: rowSecLook}, rowDesc{kind: rowHotkey}, rowDesc{kind: rowTheme}, rowDesc{kind: rowScale},
		rowDesc{kind: rowFontSize}, rowDesc{kind: rowWinSize},
		rowDesc{kind: rowSecOut}, rowDesc{kind: rowNotify}, rowDesc{kind: rowTTS},
		rowDesc{kind: rowSecLimits}, rowDesc{kind: rowMaxTurns}, rowDesc{kind: rowMaxCtx},
		rowDesc{kind: rowCompact}, rowDesc{kind: rowToolChars}, rowDesc{kind: rowToolTo},
		rowDesc{kind: rowSecPrompt}, rowDesc{kind: rowSysPrompt},
		rowDesc{kind: rowSecMCP}, rowDesc{kind: rowMCPList},
		rowDesc{kind: rowSecAdv}, rowDesc{kind: rowRaw},
		rowDesc{kind: rowActions})
}

// settingsFrame 设置窗单帧：滚动表单（§15.7/D60 核心档数据面 + D110②③ 动态区）。
// 事件先于渲染取尽（Clicked 自带 Update；点击触发重绘，新值随后续帧呈现）。常规窗
// 不做形状位图/羽化（§15.7 形态）：windowBg 铺底，与主窗内容面同底。
func settingsFrame(gtx layout.Context, th *material.Theme, u *UI, f *settingsForm) layout.Dimensions {
	for f.save.Clicked(gtx) {
		u.saveSettings(f)
	}
	for f.profCreate.Clicked(gtx) {
		u.profileAction(f, "create")
	}
	for f.profCopy.Clicked(gtx) {
		u.profileAction(f, "copy")
	}
	for f.profSwitch.Clicked(gtx) {
		u.profileAction(f, "switch")
	}
	for f.provAdd.Clicked(gtx) {
		f.addProviderForm()
	}
	for f.provApply.Clicked(gtx) {
		u.applyProviderChanges(f)
	}
	for i, pf := range f.provs {
		for pf.test.Clicked(gtx) {
			u.runProviderTest(f, pf)
		}
		for pf.setPrimary.Clicked(gtx) {
			f.primaryIdx = i
		}
		for pf.del.Clicked(gtx) {
			if len(f.provs) > 1 { // 至少保留一条（providers 不可为空，启动校验同口径）
				f.provs = append(f.provs[:i], f.provs[i+1:]...)
				switch {
				case f.primaryIdx == i:
					f.primaryIdx = 0
				case f.primaryIdx > i:
					f.primaryIdx--
				}
			}
		}
	}
	f.drainTestResults() // 连通性测试异步结果（最新一条覆盖展示）
	f.perm.advance(gtx)
	f.effort.advance(gtx)
	f.theme.advance(gtx)
	f.scale.advance(gtx)
	if !f.focused {
		f.focused = true
		gtx.Execute(key.FocusCmd{Tag: &f.model}) // 开窗焦点入模型名档；点击切换（editor.go 自聚焦）
	}
	rows := f.rowPlan()
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, windowBg,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return material.List(th, &f.list).Layout(gtx, len(rows), f.row(th, rows))
		}),
	)
}

// row 第 i 行渲染（material.List 回调；行内容按 rowPlan 的类型分派）。
func (f *settingsForm) row(th *material.Theme, rows []rowDesc) func(layout.Context, int) layout.Dimensions {
	return func(gtx layout.Context, i int) layout.Dimensions {
		return layout.UniformInset(unit.Dp(14)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			r := rows[i]
			switch r.kind {
			case rowTitle:
				return material.H6(th, "设置").Layout(gtx)
			case rowProfHead: // D110③：profile 区（当前 + 名字输入 + 三动作）
				return material.Subtitle1(th, "Profile").Layout(gtx)
			case rowProfCurrent:
				return fieldRow(gtx, th, "当前", func(gtx layout.Context) layout.Dimensions {
					txt := f.profiles.Current
					if len(f.profiles.Items) > 0 {
						txt += "（可用：" + strings.Join(f.profiles.Items, ", ") + "）"
					}
					l := material.Body1(th, txt)
					l.Color = textDim
					return l.Layout(gtx)
				})
			case rowProfName:
				return fieldRow(gtx, th, "Profile 名", editorBox(th, &f.profName, "新建 / 复制 / 切换的目标名"))
			case rowProfBtns:
				return f.profileButtonsRow(gtx, th)
			case rowSecModel: // D110②：provider 编辑区（动态行，重启生效）
				return material.Subtitle1(th, "模型服务（provider 列表）").Layout(gtx)
			case rowProvField:
				pf := f.provs[r.prov]
				eds := []*widget.Editor{&pf.name, &pf.baseURL, &pf.apiKey, &pf.models, &pf.tokenizer, &pf.unsupported}
				meta := provFieldMeta[r.field]
				return fieldRow(gtx, th, meta.label, editorBox(th, eds[r.field], meta.hint))
			case rowProvBtns:
				return f.providerButtonsRow(gtx, th, r.prov)
			case rowProvAdd:
				return f.providerAddRow(gtx, th)
			case rowProvApply:
				return f.providerApplyRow(gtx, th)
			case rowSecCore:
				return material.Subtitle1(th, "权限与思考").Layout(gtx)
			case rowPerm:
				return fieldRow(gtx, th, "权限等级", cycleBtn(th, &f.perm))
			case rowThink:
				return material.CheckBox(th, &f.think, "原生思考（/think 总开关，关时忽略推理档位）").Layout(gtx)
			case rowEffort:
				return fieldRow(gtx, th, "推理档位", cycleBtn(th, &f.effort))
			case rowSecLook:
				return material.Subtitle1(th, "外观与呼出").Layout(gtx)
			case rowHotkey:
				return fieldRow(gtx, th, "全局快捷键", editorBox(th, &f.hotkey, "如 Alt+A（空 = 默认）"))
			case rowTheme:
				return fieldRow(gtx, th, "主题", cycleBtn(th, &f.theme))
			case rowScale:
				return fieldRow(gtx, th, "元素缩放", cycleBtn(th, &f.scale))
			case rowFontSize:
				return fieldRow(gtx, th, "字号", editorBox(th, &f.fontSize, "正文字号 10–28（最终 = 字号 × 缩放）"))
			case rowWinSize:
				return fieldRow(gtx, th, "窗口尺寸", sizeRow(gtx, th, &f.winW, &f.winH))
			case rowSecOut: // S4/D110③：输出分组
				return material.Subtitle1(th, "输出").Layout(gtx)
			case rowNotify:
				return material.CheckBox(th, &f.notify, "桌面通知（回答完成时，M3）").Layout(gtx)
			case rowTTS:
				return material.CheckBox(th, &f.tts, "语音播报（D27 解析，尚未启用）").Layout(gtx)
			case rowSecLimits:
				return material.Subtitle1(th, "限额").Layout(gtx)
			case rowMaxTurns:
				return fieldRow(gtx, th, "最大轮数", editorBox(th, &f.maxTurns, "单次提问的工具调用轮数上限（空 = 保持）"))
			case rowMaxCtx:
				return fieldRow(gtx, th, "上下文上限", editorBox(th, &f.maxCtx, "tokens；自动压缩与截断的预算分母（空 = 保持）"))
			case rowCompact:
				return fieldRow(gtx, th, "压缩阈值", editorBox(th, &f.compact, "0–1；占用超阈值触发自动压缩（空 = 保持）"))
			case rowToolChars:
				return fieldRow(gtx, th, "工具输出上限", editorBox(th, &f.toolChars, "字符；超出截断（空 = 保持）"))
			case rowToolTo:
				return fieldRow(gtx, th, "工具超时", editorBox(th, &f.toolTo, "秒；缺省单次调用超时（空 = 保持）"))
			case rowSecPrompt:
				return material.Subtitle1(th, "提示词").Layout(gtx)
			case rowSysPrompt:
				return editorBoxH(th, &f.sysPrompt, "人格（system prompt；空 = 内置默认。清空保存即恢复默认）", 80)(gtx)
			case rowSecMCP:
				return material.Subtitle1(th, "MCP 与插件（只读）").Layout(gtx)
			case rowMCPList:
				return mcpListRow(gtx, th, f)
			case rowSecAdv:
				return material.Subtitle1(th, "高级（config.json 原文）").Layout(gtx)
			case rowRaw:
				return editorBoxH(th, &f.rawCfg, "整文件编辑；保存前 JSON 校验（解析失败原文件不动，Q10）", 220)(gtx)
			default:
				return f.actionsRow(gtx, th)
			}
		})
	}
}

// providerButtonsRow 单个 provider 的动作按钮：连通测试 / 设为主 / 删除（D110②）。
func (f *settingsForm) providerButtonsRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	pf := f.provs[i]
	btn := func(c *widget.Clickable, label string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			b := material.Button(th, c, label)
			b.TextSize = unit.Sp(12)
			return b.Layout(gtx)
		}
	}
	primary := "设为主"
	col := th.Fg
	if f.primaryIdx == i {
		primary, col = "★ 主", brandColor
	}
	pb := material.Button(th, &pf.setPrimary, primary)
	pb.TextSize = unit.Sp(12)
	pb.Color = col
	del := material.Button(th, &pf.del, "删除")
	del.TextSize = unit.Sp(12)
	if len(f.provs) <= 1 {
		del.Color = textDim // 仅剩一条不可删（providers 不可为空）
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(btn(&pf.test, "测试连通")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, pb.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, del.Layout)
		}),
	)
}

// providerAddRow 新增 provider 入口（编辑区尾部；Apply 校验名字等必填项）。
func (f *settingsForm) providerAddRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	b := material.Button(th, &f.provAdd, "＋ 新增 provider")
	b.TextSize = unit.Sp(13)
	return b.Layout(gtx)
}

// providerApplyRow provider 区应用按钮 + 反馈文案（成功绿/错误红 = 转写区语义色）。
func (f *settingsForm) providerApplyRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline, Spacing: layout.SpaceStart}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			b := material.Button(th, &f.provApply, "应用 provider 变更")
			b.Background = brandColor
			b.Color = whiteText
			b.TextSize = unit.Sp(13)
			return b.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				text := f.provStatus
				col := textSystem
				switch {
				case text == "":
					text, col = "变更全量写回 config，重启生效；「测试连通」发真实最小请求。", textDim
				case f.provErr:
					col = textError
				}
				l := material.Body2(th, text)
				l.Color = col
				return l.Layout(gtx)
			})
		}),
	)
}

// profileButtonsRow profile 区三动作按钮（D110③）：新建 / 复制当前 / 切换（重启生效）。
func (f *settingsForm) profileButtonsRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	btn := func(c *widget.Clickable, label string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			b := material.Button(th, c, label)
			b.TextSize = unit.Sp(13)
			return b.Layout(gtx)
		}
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(btn(&f.profCreate, "新建")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, btn(&f.profCopy, "复制当前"))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, btn(&f.profSwitch, "切换（重启生效）"))
		}),
	)
}

// fieldRow 一行：定宽标签列 + 控件（基线对齐，各行控件起点一致）。标签列宽随
// 度量缩放（Dp）——裸 px 常量在高 DPI 下实际变窄，「接口地址」等四字标签折行。
func fieldRow(gtx layout.Context, th *material.Theme, label string, control layout.Widget) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			labelW := gtx.Dp(fieldLabelW)
			gtx.Constraints.Min.X = labelW
			gtx.Constraints.Max.X = labelW
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Body1(th, label).Layout(gtx)
			})
		}),
		layout.Flexed(1, control),
	)
}

// cycleBtn 循环档按钮（当前展示值；点击进一档——material v0.10 无下拉框）。
func cycleBtn(th *material.Theme, c *cycleField) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		label := ""
		if len(c.displays) > 0 {
			label = c.displays[c.idx%len(c.displays)]
		}
		b := material.Button(th, &c.click, label)
		b.TextSize = unit.Sp(13)
		return b.Layout(gtx)
	}
}

// editorBox 文本档输入区（layout.Widget，供 fieldRow 装配）：满宽底衬，高度自适应
// ——max(Dp(fieldH) 下限, 编辑器实测行高 + 2×留白)。固定裸 px 定高会在任何 DPI 裁掉
// 文字底部（CJK 字面行高 > 内容区，D113 验收补充；实测行高 38px @1.0 / 48px @1.25 vs
// 旧内容区 24px/20px），自适应后框随文字度量走。
func editorBox(th *material.Theme, ed *widget.Editor, hint string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{Alignment: layout.NW}.Layout(gtx,
			// 底衬（Expanded 拿 Stacked 最大尺寸；垫底项保证下限，编辑器项决定实际高）。
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				paint.FillShape(gtx.Ops, th.Bg, clip.Rect(image.Rectangle{Max: gtx.Constraints.Min}).Op())
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: image.Point{Y: gtx.Dp(fieldH)}}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X // 铺满行宽：点击聚焦区 = 整框
				return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return material.Editor(th, ed, hint).Layout(gtx)
				})
			}),
		)
	}
}

// editorBoxH 定高多行编辑区（编辑框的加高变体）：提示词与高级 raw JSON 用。高度
// 随度量缩放（Dp）——多行内容超高走编辑器内部滚动，但首行行高必须容得下。
func editorBoxH(th *material.Theme, ed *widget.Editor, hint string, h int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		size := image.Point{X: gtx.Constraints.Max.X, Y: gtx.Dp(unit.Dp(h))}
		paint.FillShape(gtx.Ops, th.Bg, clip.Rect(image.Rectangle{Max: size}).Op())
		gtx.Constraints = layout.Exact(size)
		layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return material.Editor(th, ed, hint).Layout(gtx)
		})
		return layout.Dimensions{Size: size}
	}
}

// mcpListRow MCP 与插件只读清单（S4/D110③）：声明名展示 + raw JSON 编辑指引。
func mcpListRow(gtx layout.Context, th *material.Theme, f *settingsForm) layout.Dimensions {
	txt := "（无声明——MCP server 经 config mcpServers 声明，插件经 plugins 目录发现）"
	var parts []string
	if len(f.snap.MCPServers) > 0 {
		parts = append(parts, "MCP: "+strings.Join(f.snap.MCPServers, ", "))
	}
	if len(f.snap.Plugins) > 0 {
		parts = append(parts, "插件: "+strings.Join(f.snap.Plugins, ", "))
	}
	if len(parts) > 0 {
		txt = strings.Join(parts, "；") + "。"
	}
	l := material.Body2(th, txt+"　声明编辑走「高级」页 raw JSON（transport/args/env 等结构化字段不设表单）。")
	l.Color = textDim
	return l.Layout(gtx)
}

// sizeRow 窗口尺寸双数值档（D90）：宽 × 高（px），并排等分；空 = 保持缺省。
func sizeRow(gtx layout.Context, th *material.Theme, w, h *widget.Editor) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, editorBox(th, w, "宽 px")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return material.Body1(th, "×").Layout(gtx)
				})
			}),
			layout.Flexed(1, editorBox(th, h, "高 px")),
		)
	}
}

// actionsRow 保存键 + 反馈文案（成功绿/错误红 = 转写区语义色；空 = 默认提示）。
func (f *settingsForm) actionsRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline, Spacing: layout.SpaceStart}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			b := material.Button(th, &f.save, "保存并应用")
			b.Background = brandColor
			b.Color = whiteText
			b.TextSize = unit.Sp(14)
			return b.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				text := f.status
				col := textSystem
				switch {
				case text == "":
					text, col = "保存后立即生效；provider 列表在「模型服务」区应用（重启生效）。", textDim
				case f.err:
					col = textError
				}
				l := material.Body2(th, text)
				l.Color = col
				return l.Layout(gtx)
			})
		}),
	)
}
