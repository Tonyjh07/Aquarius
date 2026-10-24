package asropenai

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

// newTestBlobs 临时目录附件库（契约测试只用真实现，不打桩）。
func newTestBlobs(t *testing.T) *blobfs.Store {
	t.Helper()
	b, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	return b
}

// putAudio 往附件库塞一段假音频，返回引用。
func putAudio(t *testing.T, b *blobfs.Store, mime string) conversation.BlobRef {
	t.Helper()
	ref, err := b.Put(context.Background(), bytes.NewReader([]byte("RIFF....WAVE")), mime, "mic.wav")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	return ref
}

// TestTranscribeSendsMultipart OpenAI 形状：/audio/transcriptions + file/model/language，
// 读回 {"text"}。
func TestTranscribeSendsMultipart(t *testing.T) {
	blobs := newTestBlobs(t)
	ref := putAudio(t, blobs, "audio/wav")

	var gotModel, gotLang, gotFile, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("请求 = %s %s", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("解析表单: %v", err)
		}
		gotModel = r.FormValue("model")
		gotLang = r.FormValue("language")
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("缺 file 字段: %v", err)
		}
		defer f.Close()
		body, _ := io.ReadAll(f)
		gotFile = hdr.Filename + ":" + string(body)
		io.WriteString(w, `{"text":" 你好，世界 "}`)
	}))
	defer srv.Close()

	c, err := New(Config{
		BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "whisper-1",
		Blobs: blobs, HTTP: srv.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	text, err := c.Transcribe(context.Background(), ref, "zh")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "你好，世界" {
		t.Fatalf("转写 = %q", text)
	}
	if gotModel != "whisper-1" || gotLang != "zh" {
		t.Fatalf("model/language = %q / %q", gotModel, gotLang)
	}
	if gotFile != "mic.wav:RIFF....WAVE" {
		t.Fatalf("file = %q", gotFile)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

// TestTranscribeOmitsLanguageOnAuto auto 是本地约定的「自己判断」，不发给服务端。
func TestTranscribeOmitsLanguageOnAuto(t *testing.T) {
	blobs := newTestBlobs(t)
	ref := putAudio(t, blobs, "audio/wav")

	sent := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("解析表单: %v", err)
		}
		_, ok := r.MultipartForm.Value["language"]
		sent = ok
		io.WriteString(w, `{"text":"hello"}`)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, Model: "m", Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, lang := range []string{"", "auto", " AUTO "} {
		if _, err := c.Transcribe(context.Background(), ref, lang); err != nil {
			t.Fatalf("Transcribe(%q): %v", lang, err)
		}
		if sent {
			t.Fatalf("lang=%q 时不该发 language 字段", lang)
		}
	}
}

// TestTranscribeErrors 非 2xx、空文本、空音频都给出可读报因。
func TestTranscribeErrors(t *testing.T) {
	blobs := newTestBlobs(t)
	ref := putAudio(t, blobs, "audio/wav")

	t.Run("服务端报错", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"message":"bad key"}}`)
		}))
		defer srv.Close()
		c, err := New(Config{BaseURL: srv.URL, Model: "m", Blobs: blobs, HTTP: srv.Client()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = c.Transcribe(context.Background(), ref, "zh")
		if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad key") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("空文本", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"text":"   "}`)
		}))
		defer srv.Close()
		c, err := New(Config{BaseURL: srv.URL, Model: "m", Blobs: blobs, HTTP: srv.Client()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := c.Transcribe(context.Background(), ref, "zh"); err == nil {
			t.Fatal("空文本应报错")
		}
	})

	t.Run("空音频", func(t *testing.T) {
		empty, err := blobs.Put(context.Background(), bytes.NewReader(nil), "audio/wav", "e.wav")
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("空音频不该发请求")
		}))
		defer srv.Close()
		c, err := New(Config{BaseURL: srv.URL, Model: "m", Blobs: blobs, HTTP: srv.Client()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := c.Transcribe(context.Background(), empty, "zh"); err == nil {
			t.Fatal("空音频应报错")
		}
	})
}

// TestNewValidates 装配期就拦住写错的配置（fail-fast，报因面向用户）。
func TestNewValidates(t *testing.T) {
	blobs := newTestBlobs(t)
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"空端点", Config{Model: "m", Blobs: blobs}, "base_url"},
		{"非法端点", Config{BaseURL: "ftp://x", Model: "m", Blobs: blobs}, "base_url"},
		{"空模型", Config{BaseURL: "https://x/v1", Blobs: blobs}, "模型名"},
		{"缺附件库", Config{BaseURL: "https://x/v1", Model: "m"}, "附件库"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(c.cfg); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want 含 %q", err, c.want)
			}
		})
	}
}

// TestExtensionByMIME 上传文件名按 MIME 配扩展名（服务端常按扩展名判断容器）。
func TestExtensionByMIME(t *testing.T) {
	cases := map[string]string{
		"audio/wav":                ".wav",
		"audio/mpeg":               ".mp3",
		"audio/ogg; codecs=opus":   ".ogg",
		"audio/x-m4a":              ".m4a",
		"":                         ".wav",
		"application/octet-stream": ".wav",
	}
	for mime, want := range cases {
		if got := extension(mime); got != want {
			t.Errorf("extension(%q) = %q, want %q", mime, got, want)
		}
	}
}
