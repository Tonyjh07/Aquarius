package uigui

// imgthumb.go 转写区图片缩略图（D207）：气泡里的图片以固定方图呈现，点击进预览窗。
//
// 三条纪律（与暂存 chip 的缩略图同源，D187 口径四）：
//   - **解码前先看尺寸**（`image.DecodeConfig`）：像素数超预算就不解，格子回落占位；
//   - **解码不占 UI 线程**：读附件库/文件与解码全在后台 goroutine，结果经 inbox 回投；
//   - **同一张图只解一次**：缓存按内容寻址的键（附件库哈希），在途去重，失败留墓碑
//     ——解码不出来的图每帧重试一次会变成持续的卡帧源。
//
// 尺寸口径：格子边长固定（转写 120dp、工具 chip 展开体 96dp），解码成边长 240px 的
// 方图（2x 下采样，高清屏边缘干净）。方图 = 内部等比居中 + 透明留边，绘制时按格宽
// 直接铺，既不变形也不必在绘制路径里再算比例。

import (
	"context"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// 缩略图格子口径。
const (
	imgCellDp     unit.Dp = 120 // 转写气泡内的格子边长
	imgChipCellDp unit.Dp = 96  // 工具 chip 展开体的格子边长
	imgGapDp      unit.Dp = 6   // 格子间距
	imgCornerDp   unit.Dp = 6   // 格子圆角
	// imgDecodePx 解码边长（px）：120dp @2x 即 240——再大在格子里也看不出差别，
	// 却要按平方占用内存（240×240×4 ≈ 230 KB/张）。
	imgDecodePx = 240
	// imgCacheLimit 缓存条目上限（≈15 MB）：超限按插入序淘汰最早的那批。
	imgCacheLimit = 64
)

// imgRef 一张要在转写里显示的图：已入库项（blob）与本地路径项（path）二选一。
// 路径项只出现在实时回显（附件刚暂存、还没进附件库的那一刻）；提交后节点里的
// 分片一律是 BlobRef，回放与实时都按哈希命中同一份缓存。
type imgRef struct {
	blob *conversation.BlobRef
	path string
	// name 占位块上的显示名（解码失败/未就绪时至少让人知道这是哪张图）。
	name string
}

// key 缓存键：内容寻址优先（同一张图贴两次只解一次）。
func (r imgRef) key() string {
	if r.blob != nil {
		return "h:" + r.blob.Hash
	}
	return "p:" + r.path
}

// imgRefOfTool 工具产出的图片引用（D169）→ 转写区引用（D207）：工具侧类型不引
// conversation（依赖方向），这里换成内核的 BlobRef——字节仍按哈希从附件库取。
func imgRefOfTool(r tool.ImageRef) (imgRef, bool) {
	if r.Hash == "" {
		return imgRef{}, false
	}
	return imgRef{
		blob: &conversation.BlobRef{Hash: r.Hash, MIME: r.MIME, Name: r.Name, Size: r.Size},
		name: r.Name,
	}, true
}

// imgRefOfAtt 暂存附件 → 转写区引用（D207 实时回显）：只认图片（MIME 或扩展名）。
// 非图片附件仍走文字占位——文档的名字是有用信息，缩略图反而看不出内容。
func imgRefOfAtt(a stagedAtt) (imgRef, bool) {
	if a.blob != nil {
		if !strings.HasPrefix(strings.ToLower(a.blob.MIME), "image/") {
			return imgRef{}, false
		}
		return imgRef{blob: a.blob, name: a.displayName()}, true
	}
	if !imageExt[strings.ToLower(filepath.Ext(a.path))] {
		return imgRef{}, false
	}
	return imgRef{path: a.path, name: a.displayName()}, true
}

// imgRefOfChip 工具 chip 的图片列表 → 转写区引用。
func imgRefOfChip(images []tool.ImageRef) []imgRef {
	out := make([]imgRef, 0, len(images))
	for _, r := range images {
		if ref, ok := imgRefOfTool(r); ok {
			out = append(out, ref)
		}
	}
	return out
}

// imgMsg 缩略图就绪（后台 goroutine → 事件循环）：thumb 为 nil = 解码失败的墓碑。
type imgMsg struct {
	key   string
	thumb *thumbSrc
}

// ensureImg 取可绘制缩略图；未就绪则登记并在后台解码（返回 nil，本帧画占位）。
// 仅事件循环 goroutine 调用。
func (u *UI) ensureImg(r imgRef) *thumbSrc {
	key := r.key()
	if t, ok := u.imgLookup[key]; ok {
		return t
	}
	if u.imgLoading == nil {
		u.imgLoading = map[string]bool{}
	}
	if u.imgLoading[key] {
		return nil // 在途：不重复起 goroutine，本帧继续占位
	}
	u.imgLoading[key] = true
	go func() {
		thumb := u.decodeImg(r)
		u.post(imgMsg{key: key, thumb: thumb})
	}()
	return nil
}

// decodeImg 后台读字节 + 解码（后台 goroutine；只读不可变输入，不碰 UI 状态）。
func (u *UI) decodeImg(r imgRef) *thumbSrc {
	var (
		rc  io.ReadCloser
		err error
	)
	if r.blob != nil {
		if u.opts.Blobs == nil {
			return nil
		}
		rc, err = u.opts.Blobs.Get(context.Background(), *r.blob)
	} else {
		rc, err = os.Open(r.path)
	}
	if err != nil || rc == nil {
		return nil
	}
	defer rc.Close()
	return loadThumbSide(rc, imgDecodePx, thumbMaxPixels)
}

// applyImg 收下后台结果（事件循环）：入缓存并淘汰超额条目。
func (u *UI) applyImg(m imgMsg) {
	if u.imgLoading != nil {
		delete(u.imgLoading, m.key)
	}
	if _, ok := u.imgLookup[m.key]; ok {
		return // 竞态下的重复回投：首份为准
	}
	if u.imgLookup == nil {
		u.imgLookup = map[string]*thumbSrc{}
	}
	u.imgLookup[m.key] = m.thumb
	u.imgOrder = append(u.imgOrder, m.key)
	for len(u.imgOrder) > imgCacheLimit {
		old := u.imgOrder[0]
		u.imgOrder = u.imgOrder[1:]
		if _, ok := u.imgLookup[old]; ok && old != m.key {
			delete(u.imgLookup, old)
		}
	}
}

// imgNames 一组图片的显示名拼接（纯图气泡的复制口径：没有可选文本时给名字）。
func imgNames(imgs []imgRef) string {
	if len(imgs) == 0 {
		return ""
	}
	names := make([]string, 0, len(imgs))
	for _, r := range imgs {
		name := strings.TrimSpace(r.name)
		if name == "" {
			name = "图片"
		}
		names = append(names, name)
	}
	return strings.Join(names, "\n")
}

// imgClickKey 图片格点击件的缓存键（D207）：块序 + 格序。工具 chip 的图与气泡的图
// 不会同时出现在同一个块里，所以两者可以共用一套键空间。
type imgClickKey struct {
	bi, ii int
}

// imgClick 图片格点击件 get-or-create（照 chipClick 口径按块序/格序缓存）。
func (u *UI) imgClick(k imgClickKey) *widget.Clickable {
	if u.imgClicks == nil {
		u.imgClicks = map[imgClickKey]*widget.Clickable{}
	}
	if cl, ok := u.imgClicks[k]; ok {
		return cl
	}
	cl := new(widget.Clickable)
	u.imgClicks[k] = cl
	return cl
}

// openImage 点缩略图 → 预览次窗（D207）：单实例，已开 = 聚焦 + 原地换图。
// 内容经原子槽交付（跨 goroutine 只经快照），随后请求该窗重绘。
func (u *UI) openImage(r imgRef) {
	if r.blob != nil && r.blob.Hash == "" {
		return // 无寻址信息（防御）：没有可读的图
	}
	if r.blob == nil && r.path == "" {
		return
	}
	title := strings.TrimSpace(r.name)
	if title == "" {
		title = "图片"
	}
	u.imageView.Store(&imageContent{title: title, ref: r})
	if u.wins == nil {
		return // headless 无开窗能力：内容已入槽，真实窗装配后仍可读到
	}
	u.wins.openWin(winImage)
	u.wins.invalidateKind(winImage)
}

// imgFlow 图片格子流（固定方图、按可用宽自动换行）：量高与绘制同源。
// bi = 所属块序（点击件缓存键的一半；图片格点击开预览窗，见 openImage）。
func (u *UI) imgFlow(bi int, imgs []imgRef, cellDp unit.Dp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if len(imgs) == 0 {
			return layout.Dimensions{}
		}
		cell := gtx.Dp(cellDp)
		gap := gtx.Dp(imgGapDp)
		radius := gtx.Dp(imgCornerDp)
		per := 1
		if maxW := gtx.Constraints.Max.X; maxW > cell {
			per = (maxW + gap) / (cell + gap)
		}
		if per > len(imgs) {
			per = len(imgs)
		}
		rows := (len(imgs) + per - 1) / per
		for i, r := range imgs {
			x := (i % per) * (cell + gap)
			y := (i / per) * (cell + gap)
			off := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
			cl := u.imgClick(imgClickKey{bi: bi, ii: i})
			cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				u.imgCell(gtx, r, cell, radius)
				return layout.Dimensions{Size: image.Pt(cell, cell)}
			})
			off.Pop()
		}
		return layout.Dimensions{Size: image.Pt(
			per*cell+(per-1)*gap, rows*cell+(rows-1)*gap)}
	}
}

// imgCell 单格绘制：底板 + 缩略图（未就绪/解码失败 = 底板 + 名字占位）。
// 点击命中由点击件承载（imgClick），本函数只管画。
func (u *UI) imgCell(gtx layout.Context, r imgRef, cell, radius int) {
	box := image.Rect(0, 0, cell, cell)
	st := clip.UniformRRect(box, radius).Push(gtx.Ops)
	paint.Fill(gtx.Ops, cardTool)
	thumb := u.ensureImg(r)
	if op, ok := thumb.imageOp(); ok {
		drawFitted(gtx, box, op)
	}
	st.Pop()
	if thumb != nil {
		return
	}
	// 占位：名字一行（截断），居中——画不出来也要让人认出是哪张图。
	l := material.Caption(u.th, imgPlaceholder(r.name))
	l.Color = textDim
	l.MaxLines, l.Truncator = 1, "…"
	cs := gtx
	cs.Constraints = layout.Exact(box.Size())
	layout.Center.Layout(cs, l.Layout)
}

// imgPlaceholder 占位文字：尽量短（格子只有 120dp）。
func imgPlaceholder(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "图片"
	}
	if rs := []rune(name); len(rs) > 12 {
		return string(rs[:12]) + "…"
	}
	return name
}

// drawFitted 把方图等比铺进方框（源自带透明留边，等比关系已在解码期固定）。
func drawFitted(gtx layout.Context, box image.Rectangle, src paint.ImageOp) {
	sz := src.Size()
	if box.Dx() <= 0 || box.Dy() <= 0 || sz.X <= 0 || sz.Y <= 0 {
		return
	}
	defer op.Offset(box.Min).Push(gtx.Ops).Pop()
	s := float32(box.Dx()) / float32(sz.X)
	defer op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(s, s))).Push(gtx.Ops).Pop()
	src.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}
