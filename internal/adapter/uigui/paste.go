package uigui

// 图片附件入口（D187）：Ctrl+V 贴图。
//
// 关键在前置判断：剪贴板里**有图**才消费这个键。没图时不注册过滤器，按键原样落到
// 编辑器做文本粘贴——普通粘贴一个字都不会被劫持。
//
// 读剪贴板与转 PNG 都在后台 goroutine 里做（大图转换能到几十毫秒），结果经 inbox
// 回投；UI 线程只负责把字节塞进附件库、挂上缩略图。

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// pasteName 粘贴图片在附件库里的名字（会话树里显示这个；重名不影响去重——按内容哈希）。
const pasteName = "粘贴的图片.png"

// imageExt 按扩展名判定是否值得解缩略图（真正的判据仍是解码成功与否，这里只是
// 避免对每个 .pdf/.docx 都起一次读文件的 goroutine）。
var imageExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true,
}

// pasteImageKey 有图才抢 Ctrl+V（D187 口径一）：命中即消费，编辑器不会再收到这个键
// （也就不会去读文本剪贴板）。
func (u *UI) pasteImageKey(gtx layout.Context) {
	if u.opts.Blobs == nil {
		return
	}
	// 编辑器不持焦时这个键根本不会派给我们；先判掉，省下每帧的剪贴板探测
	// （淡出源恒无焦点，也一并跳过）。
	if !gtx.Focused(&u.editor) {
		return
	}
	if !u.plat.ClipboardHasImage() {
		return // 没有图：不注册过滤器，按键留给编辑器
	}
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &u.editor, Name: "V", Required: key.ModShortcut})
		if !ok {
			return
		}
		ke, isKey := ev.(key.Event)
		if !isKey || ke.State != key.Press {
			continue // Release 不重复触发
		}
		// 上一张还没落库时再按：吞掉这个键、不排队——否则同一张图会被贴出好几份。
		if u.pasting.Load() {
			continue
		}
		u.startPaste()
		return
	}
}

// startPaste 后台读剪贴板 → 转 PNG → 入附件库 → 回投。整条链都在 goroutine 里
// （大图转 PNG 与 20MB 落盘都能到几十上百毫秒），UI 线程只做「挂到暂存列表」这一件事。
//
// 上限判定在入附件库**之前**：先拿暂存位数量快照（跨 goroutine 读 u.m 是数据竞争），
// 超限就不落盘，免得留下没人引用的字节。
func (u *UI) startPaste() {
	u.pasting.Store(true)
	room := len(u.m.stagedFiles) < port.MaxAttachments
	go func() {
		defer u.pasting.Store(false)
		pngBytes, err := u.plat.ClipboardImage()
		if err != nil {
			u.post(pasteMsg{err: err})
			return
		}
		if int64(len(pngBytes)) > port.MaxImageBytes {
			u.post(pasteMsg{tooLarge: int64(len(pngBytes))})
			return
		}
		if !room {
			u.post(pasteMsg{full: true})
			return
		}
		ref, err := u.opts.Blobs.Put(context.Background(), bytes.NewReader(pngBytes),
			"image/png", pasteName)
		if err != nil {
			u.post(pasteMsg{err: err})
			return
		}
		u.post(pasteMsg{ref: &ref, thumb: loadThumb(bytes.NewReader(pngBytes))})
	}()
}

// finishPaste 收尾：把后台结果挂到暂存列表（失败与超限各报一次因）。
func (u *UI) finishPaste(m pasteMsg) {
	switch {
	case m.err != nil:
		if platform.NoClipboardImage(m.err) {
			return // 探测与读取之间剪贴板变了：静默，不算错误
		}
		u.m.add(blockNotice, "[notice] 读取剪贴板图片失败："+m.err.Error())
	case m.tooLarge > 0:
		u.m.add(blockNotice, fmt.Sprintf("[notice] 图片过大（%.1f MB，上限 %s），未添加",
			float64(m.tooLarge)/(1<<20), mbLabel(port.MaxImageBytes)))
	case m.full:
		u.m.add(blockNotice, fmt.Sprintf("[notice] 一条消息最多 %d 个附件，这张图未添加",
			port.MaxAttachments))
	case m.ref != nil:
		u.m.stageAtt(stagedAtt{blob: m.ref, thumb: m.thumb})
		u.focusPending = true
	}
}

// mbLabel 字节数 → 「20 MB」这样的整数标签（只用于提示文案）。
func mbLabel(n int64) string {
	return fmt.Sprintf("%d MB", n/(1<<20))
}

// stagePath 暂存一项路径附件（文件选择框 / 拖拽共用）：目录要当场说清——拖进来一个
// 文件夹时，与其让用户在摄取报错里猜，不如立刻告诉他「这里只收文件」。
func (u *UI) stagePath(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	isImage := imageExt[strings.ToLower(filepath.Ext(path))]
	if fi, err := os.Stat(path); err == nil {
		if fi.IsDir() {
			u.m.add(blockNotice, fmt.Sprintf("[notice] %s 是文件夹，请拖入或选择具体文件",
				filepath.Base(path)))
			return
		}
		// 图片上限与贴图同口径（D187 口径六）：这里先拦，免得摄取阶段才报错。
		if isImage && fi.Size() > port.MaxImageBytes {
			u.m.add(blockNotice, fmt.Sprintf("[notice] 图片过大（%.1f MB，上限 %s），未添加：%s",
				float64(fi.Size())/(1<<20), mbLabel(port.MaxImageBytes), filepath.Base(path)))
			return
		}
	}
	u.m.stageAttach(path)
	if isImage {
		u.startThumbLoad(path)
	}
}

// startThumbLoad 后台读图片文件做缩略图（D187 口径四）：不阻塞帧循环，结果回投后
// 按附件去重键找到那条暂存项挂上。
//
// 一次读完再解（os.ReadFile 内部即关句柄）：拖进来的图可能是用户马上要删的临时文件，
// 长时间握着句柄会挡住删除/清理。
func (u *UI) startThumbLoad(path string) {
	if fi, err := os.Stat(path); err != nil || fi.Size() <= 0 || fi.Size() > thumbReadBytes {
		return
	}
	key := stagedAtt{path: path}.key()
	go func() {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		if th := loadThumb(bytes.NewReader(data)); th != nil {
			u.post(thumbMsg{key: key, thumb: th})
		}
	}()
}
