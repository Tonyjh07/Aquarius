package asrchat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

func newTestBlobs(t *testing.T) *blobfs.Store {
	t.Helper()
	b, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	return b
}

// TestTranscribeSendsInputAudioAndReadsContent 请求形状与响应解析（D179）。
func TestTranscribeSendsInputAudioAndReadsContent(t *testing.T) {
	blobs := newTestBlobs(t)
	raw := []byte("RIFF....WAVE")
	ref, err := blobs.Put(context.Background(), bytes.NewReader(raw), "audio/wav", "mic.wav")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("请求 = %s %s", r.Method, r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("Authorization = %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("解码请求: %v", err)
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":" 你好，世界 "}}]}`)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "mimo-v2.5-asr", Blobs: blobs, HTTP: srv.Client()})
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

	if got["model"] != "mimo-v2.5-asr" {
		t.Fatalf("model = %v", got["model"])
	}
	opts, _ := got["asr_options"].(map[string]any)
	if opts["language"] != "zh" {
		t.Fatalf("asr_options = %v", got["asr_options"])
	}
	msgs := got["messages"].([]any)
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Fatalf("messages[0].role = %v", msg["role"])
	}
	part := msg["content"].([]any)[0].(map[string]any)
	if part["type"] != "input_audio" {
		t.Fatalf("content[0].type = %v", part["type"])
	}
	audio := part["input_audio"].(map[string]any)
	want := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(raw)
	if audio["data"] != want {
		t.Fatalf("input_audio.data = %v", audio["data"])
	}
}

// TestTranscribeDefaultsLanguage 空语种按 auto 发。
func TestTranscribeDefaultsLanguage(t *testing.T) {
	blobs := newTestBlobs(t)
	ref, err := blobs.Put(context.Background(), strings.NewReader("x"), "", "mic.wav")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL, Model: "m", Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Transcribe(context.Background(), ref, ""); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	opts, _ := got["asr_options"].(map[string]any)
	if opts["language"] != "auto" {
		t.Fatalf("language = %v, want auto", opts["language"])
	}
}

// TestTranscribeReportsServerError 非 2xx 把状态码与响应体带出来，不吞因。
func TestTranscribeReportsServerError(t *testing.T) {
	blobs := newTestBlobs(t)
	ref, _ := blobs.Put(context.Background(), strings.NewReader("x"), "audio/wav", "mic.wav")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad key"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL, Model: "m", Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Transcribe(context.Background(), ref, "auto")
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("错误应含状态码与响应体，得到 %v", err)
	}
}

// TestTranscribeRejectsEmptyTranscript 服务端回空文本时明确报因。
func TestTranscribeRejectsEmptyTranscript(t *testing.T) {
	blobs := newTestBlobs(t)
	ref, _ := blobs.Put(context.Background(), strings.NewReader("x"), "audio/wav", "mic.wav")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"  "}}]}`)
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Model: "m", Blobs: blobs, HTTP: srv.Client()})
	if _, err := c.Transcribe(context.Background(), ref, "auto"); err == nil || !strings.Contains(err.Error(), "没有识别出文字") {
		t.Fatalf("空转写应报因，得到 %v", err)
	}
}

// TestTranscribeRejectsUnsupportedMIME 接口只收 wav / mp3，别把图片当音频发出去。
func TestTranscribeRejectsUnsupportedMIME(t *testing.T) {
	blobs := newTestBlobs(t)
	c, err := New(Config{BaseURL: "https://example.com", Model: "m", Blobs: blobs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Transcribe(context.Background(), conversation.BlobRef{MIME: "image/png", Hash: "h"}, "auto")
	if err == nil || !strings.Contains(err.Error(), "不支持的音频格式") {
		t.Fatalf("应拒绝图片附件，得到 %v", err)
	}
}

// TestNewValidatesConfig 装配期 fail-fast。
func TestNewValidatesConfig(t *testing.T) {
	blobs := newTestBlobs(t)
	cases := []Config{
		{Model: "m", Blobs: blobs},
		{BaseURL: "not a url", Model: "m", Blobs: blobs},
		{BaseURL: "https://example.com", Blobs: blobs},
		{BaseURL: "https://example.com", Model: "m"},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Fatalf("cases[%d] 应报错", i)
		}
	}
}
