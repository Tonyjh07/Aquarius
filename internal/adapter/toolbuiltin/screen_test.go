package toolbuiltin

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeCapture 屏幕捕获替身：按需返回固定窗口/图片，或指定错误。
type fakeCapture struct {
	wins       []port.WindowInfo
	winsErr    error
	screen     port.Image
	screenErr  error
	window     port.Image
	windowErr  error
	lastHandle uintptr
}

var _ port.Capture = (*fakeCapture)(nil)

func (f *fakeCapture) Windows(context.Context) ([]port.WindowInfo, error) {
	return f.wins, f.winsErr
}

func (f *fakeCapture) Screen(context.Context) (port.Image, error) { return f.screen, f.screenErr }

func (f *fakeCapture) Window(_ context.Context, h uintptr) (port.Image, error) {
	f.lastHandle = h
	return f.window, f.windowErr
}

// shotBlobs 记下写入的截图像节（screen_capture 只用到 Put）。
type shotBlobs struct {
	data []byte
	mime string
	name string
}

func (b *shotBlobs) Put(_ context.Context, r io.Reader, mime, name string) (conversation.BlobRef, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return conversation.BlobRef{}, err
	}
	b.data, b.mime, b.name = data, mime, name
	return conversation.BlobRef{Hash: strings.Repeat("b", 64), MIME: mime, Name: name, Size: int64(len(data))}, nil
}

func (b *shotBlobs) Get(context.Context, conversation.BlobRef) (io.ReadCloser, error) {
	return nil, os.ErrNotExist
}
func (b *shotBlobs) Stat(context.Context, conversation.BlobRef) (bool, error) { return false, nil }
func (b *shotBlobs) GC(context.Context, map[string]bool) error                { return nil }

// makePNG 构造一张可解码的渐变 PNG（缩放测试需要真实像素）。
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestWindowListFormat 清单按「句柄 / 程序 / 矩形 / 是否前台 / 标题」一行一个，
// 标题里的换行与控制字符不得破坏行结构（D178）。
func TestWindowListFormat(t *testing.T) {
	cap := &fakeCapture{wins: []port.WindowInfo{
		{Handle: 0x2a, Title: "main.go\n- Code\tEditor", App: "Code.exe",
			Rect: port.Rect{Left: 0, Top: 0, Right: 1280, Bottom: 800}, Foreground: true},
		{Handle: 0x1f, Title: "终端", App: "", Rect: port.Rect{Left: -1920, Top: 100, Right: 0, Bottom: 900}},
	}}
	wl := &windowList{capture: cap}
	res, err := wl.Execute(context.Background(), call(`{}`))
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	lines := strings.Split(res.Output, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), res.Output)
	}
	if !strings.HasPrefix(lines[0], `handle=0x2a app="Code.exe" rect=0,0,1280,800 foreground title="`) {
		t.Fatalf("line0 = %q", lines[0])
	}
	if !strings.Contains(lines[0], "main.go - Code Editor") || strings.Contains(lines[0], "\n") {
		t.Fatalf("标题未规约: %q", lines[0])
	}
	if !strings.Contains(lines[1], `handle=0x1f`) || !strings.Contains(lines[1], `rect=-1920,100,0,900`) ||
		strings.Contains(lines[1], "foreground") {
		t.Fatalf("line1 = %q", lines[1])
	}
}

func TestWindowListEmptyAndErrors(t *testing.T) {
	wl := &windowList{capture: &fakeCapture{}}
	res, err := wl.Execute(context.Background(), call(`{}`))
	if err != nil || res.Output != emptyEntries {
		t.Fatalf("empty = %+v, err = %v", res, err)
	}

	_, err = (&windowList{capture: &fakeCapture{winsErr: port.ErrCaptureUnsupported}}).Execute(context.Background(), call(`{}`))
	if err == nil || !strings.Contains(err.Error(), "not supported on this platform") {
		t.Fatalf("unsupported err = %v", err)
	}
	_, err = (&windowList{capture: &fakeCapture{winsErr: errors.New("boom")}}).Execute(context.Background(), call(`{}`))
	if err == nil || !strings.Contains(err.Error(), "window_list: boom") {
		t.Fatalf("wrapped err = %v", err)
	}
	if _, err := (&windowList{}).Execute(context.Background(), call(`{}`)); err == nil ||
		!strings.Contains(err.Error(), "not available in this build") {
		t.Fatalf("nil capture err = %v", err)
	}
}

// TestScreenCaptureScalesAndAttaches 大图按长边 1600 缩小后进附件库，回填说明原始尺寸（D178）。
func TestScreenCaptureScalesAndAttaches(t *testing.T) {
	blobs := &shotBlobs{}
	sc := &screenCapture{
		capture: &fakeCapture{screen: port.Image{Data: makePNG(t, 2000, 1000), Width: 2000, Height: 1000}},
		blobs:   blobs,
		vision:  true,
	}
	res, err := sc.Execute(context.Background(), call(`{"target":"screen"}`))
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if len(res.Images) != 1 || res.Images[0].MIME != "image/png" {
		t.Fatalf("images = %+v", res.Images)
	}
	if !strings.Contains(res.Output, "the whole desktop") || !strings.Contains(res.Output, "1600x800") ||
		!strings.Contains(res.Output, "scaled down from 2000x1000") {
		t.Fatalf("output = %q", res.Output)
	}
	if blobs.mime != "image/png" || blobs.name != "screenshot.png" || len(blobs.data) == 0 {
		t.Fatalf("blob = %s %s %d bytes", blobs.mime, blobs.name, len(blobs.data))
	}
	img, err := png.Decode(bytes.NewReader(blobs.data))
	if err != nil {
		t.Fatalf("stored bytes are not a PNG: %v", err)
	}
	if got := img.Bounds().Size(); got.X != 1600 || got.Y != 800 {
		t.Fatalf("stored size = %v, want 1600x800", got)
	}
}

// TestScreenCaptureWindowHandle 句柄走 Window，回填带句柄；未超限的图原样入附件库。
func TestScreenCaptureWindowHandle(t *testing.T) {
	blobs := &shotBlobs{}
	cap := &fakeCapture{window: port.Image{Data: makePNG(t, 320, 200), Width: 320, Height: 200}}
	sc := &screenCapture{capture: cap, blobs: blobs, vision: true}
	res, err := sc.Execute(context.Background(), call(`{"target":"0x2a"}`))
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if cap.lastHandle != 0x2a {
		t.Fatalf("handle = %#x, want 0x2a", cap.lastHandle)
	}
	if !strings.Contains(res.Output, "window 0x2a (320x200)") || strings.Contains(res.Output, "scaled") {
		t.Fatalf("output = %q", res.Output)
	}
}

func TestScreenCaptureErrors(t *testing.T) {
	okBlobs := func() *shotBlobs { return &shotBlobs{} }
	cases := []struct {
		name string
		tool *screenCapture
		args string
		want string
	}{
		{"missing target", &screenCapture{blobs: okBlobs(), vision: true}, `{}`, "missing target"},
		{"bad target", &screenCapture{capture: &fakeCapture{}, blobs: okBlobs(), vision: true}, `{"target":"nope"}`, "window handle"},
		{"no vision", &screenCapture{capture: &fakeCapture{}, blobs: okBlobs()}, `{"target":"screen"}`, "does not accept image input"},
		{"no blobs", &screenCapture{capture: &fakeCapture{}, vision: true}, `{"target":"screen"}`, "not available in this build"},
		{"no capture", &screenCapture{blobs: okBlobs(), vision: true}, `{"target":"screen"}`, "not available in this build"},
		{"unsupported", &screenCapture{capture: &fakeCapture{screenErr: port.ErrCaptureUnsupported}, blobs: okBlobs(), vision: true},
			`{"target":"screen"}`, "not supported on this platform"},
		{"window err", &screenCapture{capture: &fakeCapture{windowErr: errors.New("did not render")}, blobs: okBlobs(), vision: true},
			`{"target":"7"}`, "did not render"},
		{"bad png", &screenCapture{capture: &fakeCapture{screen: port.Image{Data: pngBytes(), Width: 1, Height: 1}}, blobs: okBlobs(), vision: true},
			`{"target":"screen"}`, "decode capture"},
	}
	for _, tc := range cases {
		_, err := tc.tool.Execute(context.Background(), call(tc.args))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want contains %q", tc.name, err, tc.want)
		}
	}
}

// TestCleanWindowTitle 控制字符压成空格、连续空白折叠、超长截断、双引号换单引号。
func TestCleanWindowTitle(t *testing.T) {
	if got := cleanWindowTitle("a\n\tb  c"); got != "a b c" {
		t.Fatalf("got %q", got)
	}
	if got := cleanWindowTitle(`say "hi"`); got != "say 'hi'" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("标", windowTitleChars+10)
	got := cleanWindowTitle(long)
	if n := len([]rune(got)); n != windowTitleChars+3 {
		t.Fatalf("rune count = %d, want %d (%q)", n, windowTitleChars+3, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("got %q, want ellipsis", got)
	}
}

// TestParseHandle 十六进制（window_list 的输出形状）与十进制都认。
func TestParseHandle(t *testing.T) {
	for in, want := range map[string]uintptr{"0x2a": 42, "42": 42} {
		got, err := parseHandle(in)
		if err != nil || got != want {
			t.Fatalf("parseHandle(%q) = %#x, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "0", "screen", "-1", "0x"} {
		if _, err := parseHandle(in); err == nil {
			t.Fatalf("parseHandle(%q) 应报错", in)
		}
	}
}

// 编译期护栏：截图工具实现 port.Tool。
var (
	_ port.Tool = (*windowList)(nil)
	_ port.Tool = (*screenCapture)(nil)
)
