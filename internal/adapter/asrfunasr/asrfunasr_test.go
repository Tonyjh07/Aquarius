package asrfunasr

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// newTestBlobs 临时目录附件库。
func newTestBlobs(t *testing.T) *blobfs.Store {
	t.Helper()
	b, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	return b
}

// putAudio 往附件库塞一段假音频。
func putAudio(t *testing.T, b *blobfs.Store) conversation.BlobRef {
	t.Helper()
	ref, err := b.Put(context.Background(), bytes.NewReader([]byte("RIFF....WAVE")), "audio/wav", "mic.wav")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	return ref
}

// TestTranscribeUsesRecognitionPath FunASR 原生形状：/recognition + audio 字段（不是 file）。
func TestTranscribeUsesRecognitionPath(t *testing.T) {
	blobs := newTestBlobs(t)
	ref := putAudio(t, blobs)

	var gotField, gotFile string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/recognition" {
			t.Errorf("请求 = %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("解析表单: %v", err)
		}
		// file 字段不该出现：FunASR 只认 audio。
		if _, _, err := r.FormFile("file"); err == nil {
			t.Error("不该发 file 字段")
		}
		f, hdr, err := r.FormFile("audio")
		if err != nil {
			t.Fatalf("缺 audio 字段: %v", err)
		}
		defer f.Close()
		body, _ := io.ReadAll(f)
		gotField, gotFile = "audio", hdr.Filename+":"+string(body)
		io.WriteString(w, `{"text":" 今天天气不错 ","code":0}`)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	text, err := c.Transcribe(context.Background(), ref, "zh")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "今天天气不错" {
		t.Fatalf("转写 = %q", text)
	}
	if gotField != "audio" || gotFile != "mic.wav:RIFF....WAVE" {
		t.Fatalf("表单字段 = %q / %q", gotField, gotFile)
	}
}

// TestTranscribeIgnoresLanguage lang 不参与请求：FunASR 的语种由模型决定，没有可传的参数。
func TestTranscribeIgnoresLanguage(t *testing.T) {
	blobs := newTestBlobs(t)
	ref := putAudio(t, blobs)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("解析表单: %v", err)
		}
		if len(r.MultipartForm.Value) != 0 {
			t.Errorf("不该发任何普通字段，实为 %v", r.MultipartForm.Value)
		}
		io.WriteString(w, `{"text":"ok","code":0}`)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Transcribe(context.Background(), ref, "en"); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
}

// TestTranscribeReportsServerCode code 非 0 = 服务端明确失败，要把它的说明带出来。
func TestTranscribeReportsServerCode(t *testing.T) {
	blobs := newTestBlobs(t)
	ref := putAudio(t, blobs)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"text":"","code":3,"msg":"模型未加载"}`)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Transcribe(context.Background(), ref, "zh")
	if err == nil || !strings.Contains(err.Error(), "模型未加载") || !strings.Contains(err.Error(), "3") {
		t.Fatalf("err = %v", err)
	}
}

// TestTranscribeHTTPError 非 2xx 带上响应体片段。
func TestTranscribeHTTPError(t *testing.T) {
	blobs := newTestBlobs(t)
	ref := putAudio(t, blobs)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "boom")
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Transcribe(context.Background(), ref, "zh")
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

// TestNewValidates 装配期拦住写错的配置。
func TestNewValidates(t *testing.T) {
	blobs := newTestBlobs(t)
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"空端点", Config{Blobs: blobs}, "base_url"},
		{"非法端点", Config{BaseURL: "x", Blobs: blobs}, "base_url"},
		{"缺附件库", Config{BaseURL: "https://x"}, "附件库"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(c.cfg); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want 含 %q", err, c.want)
			}
		})
	}
}
