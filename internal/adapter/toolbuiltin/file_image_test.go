package toolbuiltin

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// fakeBlobs 只实现 file_read 需要的那一半：Put 记账并返回内容哈希。
type fakeBlobs struct {
	puts  []string
	fail  error
	mimes []string
}

func (f *fakeBlobs) Put(_ context.Context, r io.Reader, mime, name string) (conversation.BlobRef, error) {
	if f.fail != nil {
		return conversation.BlobRef{}, f.fail
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return conversation.BlobRef{}, err
	}
	f.puts = append(f.puts, name)
	f.mimes = append(f.mimes, mime)
	return conversation.BlobRef{Hash: strings.Repeat("a", 64), MIME: mime, Name: name, Size: int64(len(b))}, nil
}

func (f *fakeBlobs) Get(context.Context, conversation.BlobRef) (io.ReadCloser, error) {
	return nil, os.ErrNotExist
}
func (f *fakeBlobs) Stat(context.Context, conversation.BlobRef) (bool, error) { return false, nil }
func (f *fakeBlobs) GC(context.Context, map[string]bool) error                { return nil }

// pngBytes 最小可识别的 PNG 头（只需通过魔数嗅探）。
func pngBytes() []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, make([]byte, 32)...)
}

// TestFileReadImage 图片走附件库并把引用回填给装配层（D169）。
func TestFileReadImage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(p, pngBytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	blobs := &fakeBlobs{}
	r := pick(t, New(Deps{Blobs: blobs, Vision: true}), "file_read")

	res, err := r.Execute(context.Background(), call(`{"path":`+js(p)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || res.Images[0].MIME != "image/png" || res.Images[0].Name != "shot.png" {
		t.Fatalf("images = %+v", res.Images)
	}
	if res.Images[0].Size == 0 || len(blobs.puts) != 1 || blobs.mimes[0] != "image/png" {
		t.Fatalf("blobs = %+v", blobs)
	}
	// 回填文本必须说明图片随结果一起提供，且不能把字节塞进 JSON。
	if !strings.Contains(res.Output, "image") || len(res.Output) > 200 {
		t.Fatalf("output = %q", res.Output)
	}
}

// TestFileReadImageDegrades 模型不支持看图 / 附件库未接入 / 超出大小：都要**明确报因**，
// 不能静默把图片当文本塞进上下文（模型会读到一堆乱码还以为读到了内容）。
func TestFileReadImageDegrades(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(p, pngBytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		deps Deps
		want string
	}{
		{"no vision", Deps{Blobs: &fakeBlobs{}}, "does not accept image input"},
		{"no blobs", Deps{Vision: true}, "not available in this build"},
	}
	for _, tc := range cases {
		r := pick(t, New(tc.deps), "file_read")
		_, err := r.Execute(context.Background(), call(`{"path":`+js(p)+`}`))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}

	// 超出上限：报实际大小与上限，不截断成半张图。
	big := filepath.Join(dir, "big.png")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(pngBytes()); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxImageBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	r := pick(t, New(Deps{Blobs: &fakeBlobs{}, Vision: true}), "file_read")
	if _, err := r.Execute(context.Background(), call(`{"path":`+js(big)+`}`)); err == nil ||
		!strings.Contains(err.Error(), "image limit is") {
		t.Fatalf("oversize err = %v", err)
	}
}

// TestFileReadImageMIMEByContent 类型按内容判定，扩展名不作数。
func TestFileReadImageMIMEByContent(t *testing.T) {
	if got := imageMIME(pngBytes()); got != "image/png" {
		t.Fatalf("png = %q", got)
	}
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0}
	if got := imageMIME(jpeg); got != "image/jpeg" {
		t.Fatalf("jpeg = %q", got)
	}
	webp := append([]byte("RIFF"), append([]byte{0, 0, 0, 0}, []byte("WEBPVP8 ")...)...)
	if got := imageMIME(webp); got != "image/webp" {
		t.Fatalf("webp = %q", got)
	}
	if got := imageMIME([]byte("GIF89a")); got != "image/gif" {
		t.Fatalf("gif = %q", got)
	}
	if got := imageMIME([]byte("plain text")); got != "" {
		t.Fatalf("text = %q, want empty", got)
	}
}

// TestFileReadImageResultCarriesNoBytes 图片引用不带字节（JSON 里只应出现寻址事实）。
func TestFileReadImageResultCarriesNoBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(p, pngBytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	r := pick(t, New(Deps{Blobs: &fakeBlobs{}, Vision: true}), "file_read")
	res, err := r.Execute(context.Background(), call(`{"path":`+js(p)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"data"`) || len(raw) > 512 {
		t.Fatalf("result JSON = %s", raw)
	}
	var decoded tool.Result
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Images) != 1 {
		t.Fatalf("decoded = %+v", decoded)
	}
}
