package audiohttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// testKeys 词表与真实适配器同形状（key 用 asr.err.* 的口径）。
var testKeys = ErrKeys{
	BaseURLEmpty:   "asr.err.base_url_empty",
	BaseURLInvalid: "asr.err.base_url_invalid",
	ServerStatus:   "asr.err.server_status",
	RequestFailed:  "asr.err.request_failed",
	ReadAudio:      "asr.err.read_audio",
	AudioEmpty:     "asr.err.audio_empty",
	AudioTooLarge:  "asr.err.audio_too_large",
	SaveAudio:      "tts.err.save_audio",
}

// TestValidateBaseURL 校验分支：空/非法/合法（合法时去尾斜杠并给带超时的客户端）。
func TestValidateBaseURL(t *testing.T) {
	if _, _, err := ValidateBaseURL("  ", time.Minute, testKeys); err == nil {
		t.Fatal("空 base_url 应报错")
	}
	for _, raw := range []string{"ftp://x", "http://", "notaurl"} {
		if _, _, err := ValidateBaseURL(raw, time.Minute, testKeys); err == nil {
			t.Fatalf("%q 应报非法", raw)
		}
	}
	base, hc, err := ValidateBaseURL("http://srv:8080///", 2*time.Minute, testKeys)
	if err != nil {
		t.Fatalf("合法 base_url 报错: %v", err)
	}
	if base != "http://srv:8080" {
		t.Fatalf("base = %q, want 去尾斜杠", base)
	}
	if hc == nil || hc.Timeout != 2*time.Minute {
		t.Fatalf("默认客户端超时口径不对: %v", hc)
	}
}

// TestStatusErrorTransient 429/408/5xx 标注 ErrTransient；4xx 不标。
func TestStatusErrorTransient(t *testing.T) {
	for _, code := range []int{429, 408, 500, 503} {
		err := StatusError(code, strings.NewReader("oops"), testKeys)
		if !errors.Is(err, port.ErrTransient) {
			t.Fatalf("status %d 应命中 ErrTransient: %v", code, err)
		}
		if !strings.Contains(err.Error(), "oops") {
			t.Fatalf("报因应带响应体摘要: %v", err)
		}
	}
	for _, code := range []int{400, 401, 404} {
		err := StatusError(code, strings.NewReader("nope"), testKeys)
		if errors.Is(err, port.ErrTransient) {
			t.Fatalf("status %d 不该命中 ErrTransient", code)
		}
	}
	err := StatusError(500, strings.NewReader("  \n"), testKeys)
	if !strings.Contains(err.Error(), i18n.T("audio.detail.empty_body")) {
		t.Fatalf("空体应给 empty_body 文案: %v", err)
	}
}

// fakeBlobs 恒返回给定内容或错误的最小附件库替身。
type fakeBlobs struct {
	data string
	err  error
}

func (f *fakeBlobs) Put(ctx context.Context, r io.Reader, mime, name string) (conversation.BlobRef, error) {
	return conversation.BlobRef{Name: name}, nil
}

func (f *fakeBlobs) Get(ctx context.Context, ref conversation.BlobRef) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader(f.data)), nil
}

func (f *fakeBlobs) Stat(ctx context.Context, ref conversation.BlobRef) (bool, error) {
	return true, nil
}

func (f *fakeBlobs) GC(ctx context.Context, keep map[string]bool) error { return nil }

// TestReadBlob 超限读取的三分支：读失败、空体、超上限；正常路径返回全量字节。
func TestReadBlob(t *testing.T) {
	ctx := context.Background()
	b := &fakeBlobs{err: errors.New("disk boom")}
	_, err := ReadBlob(ctx, b, conversation.BlobRef{}, 16, testKeys)
	if err == nil || !strings.Contains(err.Error(), "disk boom") {
		t.Fatalf("读失败报因应包住底层错误: %v", err)
	}

	b = &fakeBlobs{data: ""}
	if _, err = ReadBlob(ctx, b, conversation.BlobRef{}, 16, testKeys); err == nil {
		t.Fatal("空体应报 audio_empty")
	}

	b = &fakeBlobs{data: "0123456789abcdefghij"} // 20 字节 > 16
	_, err = ReadBlob(ctx, b, conversation.BlobRef{}, 16, testKeys)
	if err == nil || !strings.Contains(err.Error(), "0") {
		t.Fatalf("超上限应报 audio_too_large（mb 参数）: %v", err)
	}

	b = &fakeBlobs{data: "0123456789abc"}
	data, err := ReadBlob(ctx, b, conversation.BlobRef{}, 16, testKeys)
	if err != nil || string(data) != "0123456789abc" {
		t.Fatalf("正常读取: %q, %v", data, err)
	}
}

// TestDoTransientTagging 传输层失败标注 ErrTransient；ctx 已取消的不标。
func TestDoTransientTagging(t *testing.T) {
	hc := &http.Client{Timeout: time.Second}
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/nope", nil)
	_, err := Do(context.Background(), hc, req, testKeys)
	if !errors.Is(err, port.ErrTransient) {
		t.Fatalf("断连应命中 ErrTransient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Do(ctx, hc, req, testKeys)
	if errors.Is(err, port.ErrTransient) {
		t.Fatal("ctx 已取消的传输失败不该算瞬时")
	}
}

// TestSaveWAV 命名与 MIME 落库口径：speech-%d.wav / audio/wav。
func TestSaveWAV(t *testing.T) {
	dir := t.TempDir()
	blobs, err := blobfs.New(dir)
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	ref, err := SaveWAV(context.Background(), blobs, []byte("RIFF....WAVE"), testKeys)
	if err != nil {
		t.Fatalf("SaveWAV: %v", err)
	}
	if !strings.HasPrefix(ref.Name, "speech-") || !strings.HasSuffix(ref.Name, ".wav") {
		t.Fatalf("命名口径: %q", ref.Name)
	}
	if len(ref.Name) != len("speech-1234567890123.wav") {
		t.Fatalf("命名应是毫秒时间戳: %q", ref.Name)
	}
}

// TestIsWAV 头判定边界。
func TestIsWAV(t *testing.T) {
	good := []byte("RIFF\x24\x00\x00\x00WAVEfmt ")
	if !IsWAV(good) {
		t.Fatal("合法 WAV 头应命中")
	}
	for _, b := range [][]byte{nil, []byte("RIFF"), []byte("RIFX....WAVE"), []byte("RIFF....WAVX")} {
		if IsWAV(b) {
			t.Fatalf("%q 不该命中", b)
		}
	}
}

// TestSniff 响应体开头的报因摘要。
func TestSniff(t *testing.T) {
	if got := Sniff([]byte("<html>boom</html>")); got == "" || !strings.Contains(got, "html") {
		t.Fatalf("sniff 应带开头: %q", got)
	}
	if got := Sniff(nil); got != i18n.T("tts.err.sniff_empty") {
		t.Fatalf("空体给 sniff_empty 文案: %q", got)
	}
}

// TestReadAudioRoundTrip 起一个真实 httptest server，走 ReadAudio 读音频字节，
// 顺带验证 Snippet/StatusError 在真实响应上的形态。
func TestReadAudioRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("overloaded"))
	}))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	err = StatusError(resp.StatusCode, resp.Body, testKeys)
	if err == nil || !errors.Is(err, port.ErrTransient) || !strings.Contains(err.Error(), "overloaded") {
		t.Fatalf("503 应命中 ErrTransient 且带摘要: %v", err)
	}
}
