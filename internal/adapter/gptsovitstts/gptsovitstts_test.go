package gptsovitstts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// wavBody 一段带 WAV 头的字节（适配器只校验头）。
var wavBody = append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 32)...)

// newTestBlobs 临时目录附件库。
func newTestBlobs(t *testing.T) *blobfs.Store {
	t.Helper()
	b, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	return b
}

// TestSynthesizePostsJSON GPT-SoVITS 走 POST /tts，音色 = 参考音频路径 + 参考文字。
func TestSynthesizePostsJSON(t *testing.T) {
	blobs := newTestBlobs(t)
	var got request
	var gotPath, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("方法 = %s", r.Method)
		}
		gotPath, gotCT = r.URL.Path, r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("解码请求: %v", err)
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(wavBody)
	}))
	defer srv.Close()

	c, err := New(Config{
		BaseURL: srv.URL, RefAudio: "/refs/a.wav", PromptText: "参考文字",
		Blobs: blobs, HTTP: srv.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref, err := c.Synthesize(context.Background(), port.SpeechRequest{
		Text: "你好，世界", Voice: "/refs/b.wav", Design: "另一段参考文字", Tags: "温柔",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if gotPath != "/tts" || gotCT != "application/json" {
		t.Fatalf("路径/类型 = %q / %q", gotPath, gotCT)
	}
	// 请求里的音色与参考文字优先于适配器缺省值。
	if got.RefAudioPath != "/refs/b.wav" || got.PromptText != "另一段参考文字" {
		t.Fatalf("参考音频/文字 = %q / %q", got.RefAudioPath, got.PromptText)
	}
	if got.Text != "(温柔)你好，世界" {
		t.Fatalf("正文 = %q", got.Text)
	}
	if got.TextSplit != "cut5" || got.MediaType != "wav" || got.Streaming {
		t.Fatalf("切分/格式/流式 = %q / %q / %v", got.TextSplit, got.MediaType, got.Streaming)
	}
	if got.TextLang != "auto" || got.PromptLang != "auto" {
		t.Fatalf("语种 = %q / %q", got.TextLang, got.PromptLang)
	}
	if ref.MIME != "audio/wav" || ref.Size != int64(len(wavBody)) {
		t.Fatalf("附件引用 = %+v", ref)
	}
}

// TestSynthesizeRequiresRefAudio 音色就是那段录音：没有参考音频就合成不了，报因要说清楚。
func TestSynthesizeRequiresRefAudio(t *testing.T) {
	blobs := newTestBlobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("缺参考音频不该发请求")
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Synthesize(context.Background(), port.SpeechRequest{Text: "a"})
	if err == nil || !strings.Contains(err.Error(), "参考音频") {
		t.Fatalf("err = %v", err)
	}
}

// TestSynthesizePromptLangFollowsTextLang 没单独给参考文字语种时跟随正文语种。
func TestSynthesizePromptLangFollowsTextLang(t *testing.T) {
	blobs := newTestBlobs(t)
	var got request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write(wavBody)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, TextLang: "JA", Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "a", Voice: "/r.wav"}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if got.TextLang != "ja" || got.PromptLang != "ja" {
		t.Fatalf("语种 = %q / %q", got.TextLang, got.PromptLang)
	}
}

// TestSynthesizeRejectsNonWAV 服务端 200 但回了错误 JSON 时不能当音频存。
func TestSynthesizeRejectsNonWAV(t *testing.T) {
	blobs := newTestBlobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":400,"message":"ref audio not found"}`)
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Synthesize(context.Background(), port.SpeechRequest{Text: "a", Voice: "/r.wav"})
	if err == nil || !strings.Contains(err.Error(), "不是 WAV") || !strings.Contains(err.Error(), "ref audio not found") {
		t.Fatalf("err = %v", err)
	}
}

// TestSynthesizeErrors 空正文、非 2xx、超长文本都报可读原因。
func TestSynthesizeErrors(t *testing.T) {
	blobs := newTestBlobs(t)

	t.Run("空正文", func(t *testing.T) {
		c, err := New(Config{BaseURL: "https://x", Blobs: blobs})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: " "}); err == nil {
			t.Fatal("空正文应报错")
		}
	})

	t.Run("超长文本", func(t *testing.T) {
		c, err := New(Config{BaseURL: "https://x", Blobs: blobs})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		long := strings.Repeat("字", maxTextBytes)
		if _, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: long}); err == nil ||
			!strings.Contains(err.Error(), "最多") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("非2xx", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, "upstream down")
		}))
		defer srv.Close()
		c, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = c.Synthesize(context.Background(), port.SpeechRequest{Text: "a", Voice: "/r.wav"})
		if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "upstream down") {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestSynthesizeContextCancel 取消后立刻返回。
func TestSynthesizeContextCancel(t *testing.T) {
	blobs := newTestBlobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("取消后不该发请求")
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Synthesize(ctx, port.SpeechRequest{Text: "a", Voice: "/r.wav"}); !errors.Is(err, context.Canceled) {
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
