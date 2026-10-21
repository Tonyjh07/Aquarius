package uigui

// 粘贴 / 拖拽 / 缩略图测试（D187）：Ctrl+V 两条路径（有图消费、无图让回编辑器）、
// 拖动路径解析、缩略图预算门、单图上限。

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/io/key"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeBlobs 内存附件库（D187 测试用）。
type fakeBlobs struct {
	put  [][]byte
	mime []string
	name []string
	err  error
}

func (f *fakeBlobs) Put(_ context.Context, r io.Reader, mime, name string) (conversation.BlobRef, error) {
	if f.err != nil {
		return conversation.BlobRef{}, f.err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return conversation.BlobRef{}, err
	}
	f.put = append(f.put, b)
	f.mime = append(f.mime, mime)
	f.name = append(f.name, name)
	return conversation.BlobRef{Hash: fmt.Sprintf("%064x", len(f.put)), MIME: mime, Name: name, Size: int64(len(b))}, nil
}

func (f *fakeBlobs) Get(context.Context, conversation.BlobRef) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeBlobs) Stat(context.Context, conversation.BlobRef) (bool, error) { return true, nil }
func (f *fakeBlobs) GC(context.Context, map[string]bool) error                { return nil }

// pngBytes 造一张 w×h 的合法 PNG。
func pngBytes(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// frameUIWithInbox 帧级 UI + 可用的投递通道（newFrameUI 的 done 是 nil，post 会卡在
// 第一个 select 上；后台 goroutine 回投类测试要补上它与带缓冲的 inbox）。
func frameUIWithInbox() *UI {
	u := newFrameUI()
	u.inbox = make(chan uiMsg, 8)
	u.done = make(chan struct{})
	return u
}

// TestPasteImageKeyConsumes 有图：Ctrl+V 被入口消费——暂存出 Blob 项、编辑器文本不变。
func TestPasteImageKeyConsumes(t *testing.T) {
	blobs := &fakeBlobs{}
	u := frameUIWithInbox()
	u.opts.Blobs = blobs
	u.editor.Submit = true
	u.editor.SingleLine = true
	u.focusPending = true

	fp := fakeOf(u)
	fp.ClipImage = pngBytes(t, 4, 4, color.NRGBA{R: 0xFF, A: 0xFF})

	q := new(input.Router)
	complFrame(q, u) // 首帧：执行 FocusCmd
	complFrame(q, u) // 次帧：FocusEvent 投递，编辑器真正持焦

	u.editor.SetText("前缀")
	q.Queue(key.Event{Name: "V", State: key.Press, Modifiers: key.ModShortcut})
	complFrame(q, u) // 本帧消费按键、起后台读图

	// 粘贴在后台 goroutine 里跑（newFrameUI 无事件循环，手工取 inbox 应用）。
	var got uiMsg
	select {
	case got = <-u.inbox:
	case <-time.After(3 * time.Second):
		t.Fatal("粘贴结果未回投")
	}
	u.apply(got)

	if len(u.m.stagedFiles) != 1 {
		t.Fatalf("暂存 = %+v, want 一项粘贴图片", u.m.stagedFiles)
	}
	att := u.m.stagedFiles[0]
	if att.blob == nil || att.blob.MIME != "image/png" {
		t.Fatalf("暂存项 = %+v, want Blob(image/png)", att)
	}
	if att.thumb == nil {
		t.Fatal("粘贴图片应带缩略图")
	}
	if len(blobs.put) != 1 || len(blobs.put[0]) == 0 {
		t.Fatalf("附件库 Put = %d 次", len(blobs.put))
	}
	if got := u.editor.Text(); got != "前缀" {
		t.Fatalf("编辑器文本 = %q，粘贴图片不应插入字符", got)
	}
}

// TestPasteImageKeyPassesThrough 没图：不注册过滤器，Ctrl+V 照常落到编辑器做文本粘贴。
func TestPasteImageKeyPassesThrough(t *testing.T) {
	u := newFrameUI()
	u.opts.Blobs = &fakeBlobs{}
	u.focusPending = true
	q := new(input.Router)
	complFrame(q, u)

	q.Queue(key.EditEvent{Text: "文字"}) // 模拟系统文本粘贴（Gio 的粘贴落成 EditEvent）
	complFrame(q, u)
	if len(u.m.stagedFiles) != 0 {
		t.Fatalf("无图不应暂存: %+v", u.m.stagedFiles)
	}
	if got := u.editor.Text(); got != "文字" {
		t.Fatalf("文本粘贴被劫持: 编辑器 = %q", got)
	}
}

// TestPasteTooLarge 超过单图上限：不暂存、明确报因（带大小与上限）。
func TestPasteTooLarge(t *testing.T) {
	u := newFrameUI()
	u.opts.Blobs = &fakeBlobs{}
	u.finishPaste(pasteMsg{tooLarge: port.MaxImageBytes + 1})
	if len(u.m.stagedFiles) != 0 {
		t.Fatalf("超限不应暂存: %+v", u.m.stagedFiles)
	}
	if n := len(u.m.blocks); n == 0 || u.m.blocks[n-1].kind != blockNotice {
		t.Fatalf("应提示超限: %+v", u.m.blocks)
	}
}

// TestPasteNoImageIsSilent 探测与读取之间剪贴板变了：不报错、不暂存。
func TestPasteNoImageIsSilent(t *testing.T) {
	u := newFrameUI()
	u.opts.Blobs = &fakeBlobs{}
	u.finishPaste(pasteMsg{err: platform.ErrNoClipboardImage})
	if len(u.m.blocks) != 0 || len(u.m.stagedFiles) != 0 {
		t.Fatalf("不应有输出: blocks=%+v staged=%+v", u.m.blocks, u.m.stagedFiles)
	}
}

// TestPasteFullList 暂存位已满：不落盘、明确提示上限。
func TestPasteFullList(t *testing.T) {
	blobs := &fakeBlobs{}
	u := newFrameUI()
	u.opts.Blobs = blobs
	u.finishPaste(pasteMsg{full: true})
	if len(blobs.put) != 0 {
		t.Fatalf("满员不应落盘: %d", len(blobs.put))
	}
	if n := len(u.m.blocks); n == 0 || u.m.blocks[n-1].kind != blockNotice {
		t.Fatalf("应提示满员: %+v", u.m.blocks)
	}
}

// TestStagePathDirectory 拖进文件夹：不暂存，明确提示「是文件夹」。
func TestStagePathDirectory(t *testing.T) {
	u := newFrameUI()
	dir := t.TempDir()
	u.stagePath(dir)
	if len(u.m.stagedFiles) != 0 {
		t.Fatalf("文件夹不应暂存: %+v", u.m.stagedFiles)
	}
	if n := len(u.m.blocks); n == 0 || u.m.blocks[n-1].kind != blockNotice {
		t.Fatalf("应提示: %+v", u.m.blocks)
	}
}

// TestStagePathImageThumb 拖入图片：暂存 + 后台解码缩略图（回投后挂上）。
func TestStagePathImageThumb(t *testing.T) {
	u := frameUIWithInbox()
	path := filepath.Join(t.TempDir(), "图.png")
	if err := os.WriteFile(path, pngBytes(t, 8, 8, color.NRGBA{G: 0xFF, A: 0xFF}), 0o600); err != nil {
		t.Fatal(err)
	}
	u.stagePath(path)
	if n := len(u.m.stagedFiles); n != 1 || u.m.stagedFiles[0].path != path {
		t.Fatalf("暂存 = %+v", u.m.stagedFiles)
	}
	waitThumb(t, u)
	if u.m.stagedFiles[0].thumb == nil {
		t.Fatal("图片附件应挂上缩略图")
	}
}

// TestStagePathImageTooLarge 拖入的图片超过单图上限：与贴图同口径，先拦下并报因。
func TestStagePathImageTooLarge(t *testing.T) {
	u := newFrameUI()
	path := filepath.Join(t.TempDir(), "大图.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(port.MaxImageBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	u.stagePath(path)
	if len(u.m.stagedFiles) != 0 {
		t.Fatalf("超限图片不应暂存: %+v", u.m.stagedFiles)
	}
	if n := len(u.m.blocks); n == 0 || u.m.blocks[n-1].kind != blockNotice {
		t.Fatalf("应提示超限: %+v", u.m.blocks)
	}
}

// waitThumb 等后台缩略图回投并应用（newFrameUI 无事件循环：手工取 inbox）。
func waitThumb(t *testing.T, u *UI) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for u.m.stagedFiles[0].thumb == nil {
		select {
		case m := <-u.inbox:
			u.apply(m)
		case <-deadline:
			return
		}
	}
}

// TestThumbBudget 缩略图预算门：像素数超预算就不解（回落回形针），不报错。
func TestThumbBudget(t *testing.T) {
	data := pngBytes(t, 20, 20, color.NRGBA{A: 0xFF})
	if th := loadThumbLimit(bytes.NewReader(data), 100); th != nil {
		t.Fatal("超预算不应解码（400 > 100）")
	}
	if th := loadThumbLimit(bytes.NewReader(data), 400); th == nil {
		t.Fatal("刚好在预算内应解码")
	}
	if th := loadThumbLimit(bytes.NewReader([]byte("not an image")), 0); th != nil {
		t.Fatal("非图片应返回 nil")
	}
}

// TestThumbSquare 缩略图等比居中留在方图里（不拉伸变形）。
func TestThumbSquare(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 40, 10))
	img := scaleToSquare(src, 40)
	if img.Bounds().Size() != image.Pt(40, 40) {
		t.Fatalf("尺寸 = %v, want 40x40", img.Bounds().Size())
	}
	// 40×10 缩到 40×10：上下各留 15px 透明边。
	if _, _, _, a := img.At(20, 2).RGBA(); a != 0 {
		t.Fatalf("上边应透明（a=%d）", a)
	}
}

// TestThumbChipRenders 带缩略图的 chip 能正常出帧，且比回形针 chip 宽（图标从 12dp 到
// 20dp）——量测与绘制同源，否则这里会露出宽度错位。
func TestThumbChipRenders(t *testing.T) {
	u := newFrameUI()
	q := new(input.Router)
	u.m.stageBlob(conversation.BlobRef{Hash: "h1", Name: "图.png", MIME: "image/png", Size: 9})
	u.m.stagedFiles[0].thumb = &thumbSrc{img: scaleToSquare(image.NewNRGBA(image.Rect(0, 0, 9, 9)), thumbPx)}
	u.setExpanded(true)
	complFrame(q, u) // 渲染路径冒烟（展开态 chip 排）

	gtx, _ := frameGtx(input.Source{})
	u.m.stagedFiles[0].thumb = nil
	plain, _ := u.attachChipMetrics(gtx, "图.png", chipIconDp(nil), gtx.Constraints.Max.X)
	u.m.stagedFiles[0].thumb = &thumbSrc{img: image.NewRGBA(image.Rect(0, 0, thumbPx, thumbPx))}
	thumb, _ := u.attachChipMetrics(gtx, "图.png", chipIconDp(u.m.stagedFiles[0].thumb), gtx.Constraints.Max.X)
	if want := gtx.Dp(attachThumbDp - attachIconDp); thumb != plain+want {
		t.Fatalf("缩略图 chip 宽 = %d，回形针 chip = %d，want +%d", thumb, plain, want)
	}
}
