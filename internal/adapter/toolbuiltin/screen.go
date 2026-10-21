package toolbuiltin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strconv"
	"strings"

	xdraw "golang.org/x/image/draw"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// maxCaptureEdge 截图长边上限（D178）：超过就按比例缩小再编码，
// 避免一张 4K 截图把上下文吃掉小半（口径同 file_read 的图片上限）。
const maxCaptureEdge = 1600

// windowTitleChars 窗口标题在清单里的字符上限：标题是用户数据，可能很长，
// 清单只用于「认出是哪个窗口」，不需要全文。
const windowTitleChars = 120

// windowList 列出可抓取的顶层窗口（Safe）：纯元数据，没有副作用，不该被确认打断。
type windowList struct{ capture port.Capture }

func (t *windowList) Spec() tool.Spec {
	return tool.Spec{
		Name: "window_list",
		Description: "List the visible top-level windows on this computer, topmost first: handle, program, screen rectangle " +
			"(physical pixels, left,top,right,bottom) and whether the window is in the foreground. " +
			"Pass a handle to screen_capture to capture that window. Listing never needs confirmation.",
		Schema: tool.NoArgsSchema,
		Risk:   tool.Safe,
	}
}

func (t *windowList) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if t.capture == nil {
		return tool.Result{}, errors.New("window_list: screen capture is not available in this build")
	}
	wins, err := t.capture.Windows(ctx)
	if err != nil {
		return tool.Result{}, captureError("window_list", err)
	}
	if len(wins) == 0 {
		return okResult(emptyEntries), nil
	}
	var b strings.Builder
	for i, w := range wins {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "handle=0x%x app=%s rect=%d,%d,%d,%d%s title=\"%s\"",
			w.Handle, strconv.Quote(w.App), w.Rect.Left, w.Rect.Top, w.Rect.Right, w.Rect.Bottom,
			foregroundTag(w.Foreground), cleanWindowTitle(w.Title))
	}
	return okResult(b.String()), nil
}

func foregroundTag(fg bool) string {
	if fg {
		return " foreground"
	}
	return ""
}

// screenCapture 截屏给模型看（Confirm）：图片进附件库并随结果内联（D169 通道）。
type screenCapture struct {
	capture port.Capture
	blobs   port.AttachmentStore
	vision  bool
}

func (t *screenCapture) Spec() tool.Spec {
	return tool.Spec{
		Name: "screen_capture",
		Description: fmt.Sprintf("Capture the screen and attach the image to the conversation for you to look at. "+
			"target \"screen\" captures the whole desktop (all monitors); a handle from window_list captures that window's "+
			"client area, including when it is covered or in the background, plus any open menu or dropdown owned by it. "+
			"A minimized window is captured through the desktop compositor's own preview, so it comes back as the whole "+
			"window including its title bar; the window is not restored and does not take focus. "+
			"Some GPU-only windows cannot be rendered and report an error instead. "+
			"Long edges are scaled down to %d pixels. Every capture needs confirmation.", maxCaptureEdge),
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"target": {"type": "string", "description": "\"screen\" for the whole desktop, or a window handle from window_list"}
			},
			"required": ["target"]
		}`),
		Risk: tool.Confirm,
	}
}

func (t *screenCapture) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	var a struct {
		Target string `json:"target"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	target := strings.TrimSpace(a.Target)
	if target == "" {
		return tool.Result{}, errors.New(`missing target; use "screen" or a handle from window_list`)
	}
	if t.capture == nil {
		return tool.Result{}, errors.New("screen_capture: screen capture is not available in this build")
	}
	if !t.vision {
		return tool.Result{}, errors.New("screen_capture: the current model does not accept image input")
	}
	if t.blobs == nil {
		return tool.Result{}, errors.New("screen_capture: image input is not available in this build")
	}

	var (
		img  port.Image
		what string
		err  error
	)
	switch {
	case strings.EqualFold(target, "screen"):
		img, err = t.capture.Screen(ctx)
		what = "the whole desktop"
	default:
		h, perr := parseHandle(target)
		if perr != nil {
			return tool.Result{}, perr
		}
		img, err = t.capture.Window(ctx, h)
		what = fmt.Sprintf("window 0x%x", h)
	}
	if err != nil {
		return tool.Result{}, captureError("screen_capture", err)
	}

	data, w, h, scaled, err := fitPNG(img)
	if err != nil {
		return tool.Result{}, err
	}
	ref, err := t.blobs.Put(ctx, bytes.NewReader(data), "image/png", "screenshot.png")
	if err != nil {
		return tool.Result{}, fmt.Errorf("store screenshot: %w", err)
	}
	out := fmt.Sprintf("captured %s (%dx%d", what, w, h)
	if scaled {
		out += fmt.Sprintf(", scaled down from %dx%d", img.Width, img.Height)
	}
	out += "); the image itself is provided with this result."
	return tool.Result{
		OK:     true,
		Output: out,
		Images: []tool.ImageRef{{Hash: ref.Hash, MIME: ref.MIME, Name: ref.Name, Size: ref.Size}},
	}, nil
}

// parseHandle 解析 window_list 给回的句柄（十六进制 0x... 或十进制）。
func parseHandle(s string) (uintptr, error) {
	v, err := strconv.ParseUint(s, 0, 64)
	if err != nil || v == 0 {
		return 0, fmt.Errorf("target must be \"screen\" or a window handle from window_list, got %q", s)
	}
	return uintptr(v), nil
}

// captureError 把平台哨兵错误翻成给模型的可行动提示，其余原样包装。
func captureError(toolName string, err error) error {
	if errors.Is(err, port.ErrCaptureUnsupported) {
		return fmt.Errorf("%s: screen capture is not supported on this platform", toolName)
	}
	return fmt.Errorf("%s: %w", toolName, err)
}

// fitPNG 解码抓屏结果，长边超过 maxCaptureEdge 时按比例缩小，再编码 PNG。
// 返回值里的 w/h 是最终尺寸，scaled 表示是否缩过（回填里说明原始尺寸）。
func fitPNG(img port.Image) (data []byte, w, h int, scaled bool, err error) {
	src, err := png.Decode(bytes.NewReader(img.Data))
	if err != nil {
		return nil, 0, 0, false, fmt.Errorf("screen_capture: decode capture: %w", err)
	}
	b := src.Bounds()
	w, h = b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, 0, 0, false, fmt.Errorf("screen_capture: the capture is empty (%dx%d)", w, h)
	}
	if longest := max(w, h); longest > maxCaptureEdge {
		scale := float64(maxCaptureEdge) / float64(longest)
		nw := max(1, int(float64(w)*scale+0.5))
		nh := max(1, int(float64(h)*scale+0.5))
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
		src, w, h, scaled = dst, nw, nh, true
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		return nil, 0, 0, false, fmt.Errorf("screen_capture: encode png: %w", err)
	}
	return buf.Bytes(), w, h, scaled, nil
}

// cleanWindowTitle 把标题里的换行/控制字符压成空格，并按字符数截断
// （窗口标题是用户数据，可能带引号与换行，不能直接拼进清单）。
func cleanWindowTitle(s string) string {
	var b strings.Builder
	n := 0
	prevSpace := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			r = ' '
		} else if r == '"' {
			r = '\'' // 清单里用双引号包标题，标题自己的双引号换成单引号
		}
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		if n >= windowTitleChars {
			b.WriteString("...")
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
