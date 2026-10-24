package vitstts

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// wavBody 一段最小的 WAV 头（播放器与适配器都只看头）。
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

// TestSynthesizeSendsQueryParams VITS 走 GET /voice/vits，正文、语种、说话人都在查询串里，
// 返回的 WAV 落附件库。
func TestSynthesizeSendsQueryParams(t *testing.T) {
	blobs := newTestBlobs(t)
	var gotQuery, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("方法 = %s", r.Method)
		}
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(wavBody)
	}))
	defer srv.Close()

	c, err := New(Config{
		BaseURL: srv.URL, APIKey: "k", Speaker: "3", Language: "zh",
		Blobs: blobs, HTTP: srv.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref, err := c.Synthesize(context.Background(), port.SpeechRequest{
		Text: "你好，世界", Voice: "8", Tags: "温柔",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if gotPath != "/voice/vits" {
		t.Fatalf("路径 = %q", gotPath)
	}
	q := gotQuery
	for _, want := range []string{"format=wav", "lang=zh", "id=8", "api_key=k"} {
		if !strings.Contains(q, want) {
			t.Errorf("查询串缺 %q: %s", want, q)
		}
	}
	// 风格标签拼在正文前（与其它合成器同口径）。
	if !strings.Contains(q, "text=%28%E6%B8%A9%E6%9F%94%29") {
		t.Errorf("标签未拼进正文: %s", q)
	}
	if ref.MIME != "audio/wav" || ref.Size != int64(len(wavBody)) {
		t.Fatalf("附件引用 = %+v", ref)
	}
	rc, err := blobs.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if string(body) != string(wavBody) {
		t.Fatalf("落库音频不一致: %d 字节", len(body))
	}
}

// TestSynthesizeUsesFallbackSpeaker 请求里没给音色时用适配器的缺省说话人；都没有就不发 id。
func TestSynthesizeUsesFallbackSpeaker(t *testing.T) {
	blobs := newTestBlobs(t)
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write(wavBody)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, Speaker: "5", Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "a"}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if !strings.Contains(gotQuery, "id=5") {
		t.Fatalf("缺省说话人未生效: %s", gotQuery)
	}

	// 没有说话人时不发 id（服务端用自己的缺省音色）。
	c2, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c2.Synthesize(context.Background(), port.SpeechRequest{Text: "a"}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if strings.Contains(gotQuery, "id=") {
		t.Fatalf("没有说话人时不该发 id: %s", gotQuery)
	}
}

// TestSynthesizeRejectsNonWAV 服务端出错时也可能回 200 + 一段 JSON，不能当音频存下去。
func TestSynthesizeRejectsNonWAV(t *testing.T) {
	blobs := newTestBlobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"detail":"model not loaded"}`)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Synthesize(context.Background(), port.SpeechRequest{Text: "a"})
	if err == nil || !strings.Contains(err.Error(), "不是 WAV") || !strings.Contains(err.Error(), "model not loaded") {
		t.Fatalf("err = %v", err)
	}
}

// TestSynthesizeErrors 空正文、非 2xx、超长文本、空音频都报可读原因。
func TestSynthesizeErrors(t *testing.T) {
	blobs := newTestBlobs(t)

	t.Run("空正文", func(t *testing.T) {
		c, err := New(Config{BaseURL: "https://x", Blobs: blobs})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "  "}); err == nil {
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
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "boom")
		}))
		defer srv.Close()
		c, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = c.Synthesize(context.Background(), port.SpeechRequest{Text: "a"})
		if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("空音频", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer srv.Close()
		c, err := New(Config{BaseURL: srv.URL, Blobs: blobs, HTTP: srv.Client()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "a"}); err == nil {
			t.Fatal("空音频应报错")
		}
	})
}

// TestSynthesizeContextCancel 取消后立刻返回，不发请求。
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
	if _, err := c.Synthesize(ctx, port.SpeechRequest{Text: "a"}); !errors.Is(err, context.Canceled) {
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
