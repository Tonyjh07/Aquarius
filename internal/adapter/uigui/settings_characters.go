package uigui

// settings_characters.go 「角色」页数据面与动作（§5.17）。
//
// 与「技能」页的差别：本页**不直连管理面**——导入 / 删除 / 开始对话全部经 `/character`
// 命令通道投给内核（回执进主窗转写区），界面只呈现与转达。清单是一条只读快照（装配根
// 现取并做节流缓存），头像**异步解码**（后台 goroutine + 结果通道 + Invalidate），与主窗
// 缩略图同款纪律：解码不占 UI 线程。
//
// 卡片是用户挑选的外部内容，本页只读导入（不编辑卡、不导出卡）；「删除」删的是导入副本。

import (
	"image"
	"strconv"
	"strings"
	"unicode"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 头像口径：设置页行内方图。
const (
	charAvatarDp unit.Dp = 44 // 行内头像边长（dp）
	charAvatarPx         = 88 // 解码边长（2x：高 DPI 下仍是下采样，边缘干净）
)

// CharactersSnapshot 「角色」页只读快照（装配根现取；逐帧问，由装配根侧节流读盘）。
type CharactersSnapshot struct {
	Items []CharacterState
	// Err 读角色卡目录失败的原因（用户面）；空 = 正常。
	Err string
}

// CharacterState 一张已导入角色卡（页面展示；不进模型上下文——Warnings/Unsupported 是给
// 用户的来路说明，不是要遵循的指令）。
type CharacterState struct {
	Slug        string
	Display     string
	Source      string // png | json | charx
	Spec        string // v2 | v3 | v3+
	BookEntries int
	Greetings   int
	Assets      int
	HasAvatar   bool
	AvatarPath  string // 头像文件绝对路径（空 = 无头像）
	ImportedAt  string // 用户面短日期（空 = 未知）；由装配根格式化
	Warnings    []string
	Unsupported []string
}

// sendCharacterCmd 投一条 `/character` 命令（经 u.inCh，与键入同路径串行执行；缓冲满丢弃，
// 用户重点一次即可）。
func (u *UI) sendCharacterCmd(args ...string) {
	if u == nil || u.inCh == nil {
		return
	}
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "character", Args: args}}:
	default:
	}
}

// charPathPick 角色卡选择框回传（平台线程 → 设置窗 goroutine）。
type charPathPick struct{ path string }

// pushCharPick 投递角色卡选择框结果（平台线程调用）：通道满即丢（用户重选一次即可），
// 随后催设置窗重绘让帧首取走。
func (u *UI) pushCharPick(p charPathPick) {
	select {
	case u.charPicks <- p:
	default:
	}
	if u.wins != nil {
		u.wins.invalidateKind(winSettings)
	}
}

// drainCharPicks 帧首取走选择框结果：**只填来源框**，由用户再点「导入」才落盘（选择动作
// ≠ 导入动作，与技能进口同口径）。
func (f *settingsForm) drainCharPicks(u *UI) {
	if u == nil || u.charPicks == nil {
		return
	}
	for {
		select {
		case p := <-u.charPicks:
			f.charSource.SetText(p.path)
			f.charStatus, f.charErr = "已选择文件："+p.path+"（点「导入」开始）", false
		default:
			return
		}
	}
}

// syncCharactersSnapshot 角色页快照同步（仅在本页时现取，别页不空读盘）+ 头像结果排空。
func (f *settingsForm) syncCharactersSnapshot(u *UI) {
	if u == nil {
		return
	}
	if f.page == pageCharacters && u.opts.Characters != nil {
		f.charactersSnap = u.opts.Characters()
	}
	f.charAvatars.drain()
}

// charMetaLine 角色行第二行：来源 / 世界书条目 / 资产 / 导入时间（用户面短文案）。
func (st CharacterState) metaLine() string {
	parts := []string{}
	switch st.Source {
	case "png":
		parts = append(parts, "PNG 卡")
	case "charx":
		parts = append(parts, "角色卡包")
	default:
		parts = append(parts, "JSON 卡")
	}
	if st.Spec != "" {
		parts = append(parts, st.Spec)
	}
	if st.BookEntries > 0 {
		parts = append(parts, strconv.Itoa(st.BookEntries)+" 条世界书")
	}
	if st.Assets > 0 {
		parts = append(parts, strconv.Itoa(st.Assets)+" 个资产")
	}
	if st.ImportedAt != "" {
		parts = append(parts, "导入于 "+st.ImportedAt)
	}
	if len(st.Unsupported) > 0 {
		parts = append(parts, "含未支持的机制（见导入回执）")
	}
	return strings.Join(parts, " · ")
}

// charAvatar 角色卡头像：真图（异步解码；未就绪/失败画兜底）或兜底首字方图。
func (f *settingsForm) charAvatar(gtx layout.Context, th *material.Theme, st CharacterState) layout.Dimensions {
	side := gtx.Dp(charAvatarDp)
	if src := f.charAvatars.get(st.AvatarPath); src != nil {
		if op, ok := src.imageOp(); ok {
			drawThumb(gtx, image.Rect(0, 0, side, side), op)
			return layout.Dimensions{Size: image.Pt(side, side)}
		}
	}
	return charAvatarFallback(gtx, th, st.Display, side)
}

// charAvatarFallback 无头像 / 解码中 / 解码失败时的兜底：下沉面圆角方块 + 显示名首字。
// name 为空（非角色会话）时只画纯色方块，不画字。
func charAvatarFallback(gtx layout.Context, th *material.Theme, name string, side int) layout.Dimensions {
	if side <= 0 {
		return layout.Dimensions{}
	}
	pp := paperOf()
	gtx.Constraints = layout.Exact(image.Pt(side, side))
	children := []layout.StackChild{
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, pp.sunken,
				clip.UniformRRect(image.Rect(0, 0, side, side), side/4).Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(side, side)}
		}),
	}
	if strings.TrimSpace(name) != "" {
		children = append(children, layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, unit.Sp(float32(side)*0.42), charInitial(name))
				l.Color = pp.text2
				return l.Layout(gtx)
			})
		}))
	}
	return layout.Stack{}.Layout(gtx, children...)
}

// charInitial 显示名首字（去空白；空名回落「?」）。
func charInitial(name string) string {
	for _, r := range name {
		if !unicode.IsSpace(r) {
			return string(r)
		}
	}
	return "?"
}
