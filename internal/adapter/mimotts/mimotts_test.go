package mimotts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func newTestBlobs(t *testing.T) *blobfs.Store {
	t.Helper()
	b, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	return b
}

// ttsServer 记录请求体，回一段固定音频。
func ttsServer(t *testing.T, audio []byte, got *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("路径 = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Errorf("解码请求: %v", err)
		}
		body := map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(audio)},
				},
			}},
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
}

// TestSynthesizePresetUsesStyleAndTags 预置音色：风格进 user、标签拼正文前缀、音色名进 audio。
func TestSynthesizePresetUsesStyleAndTags(t *testing.T) {
	blobs := newTestBlobs(t)
	want := []byte("WAVE-AUDIO")
	var got map[string]any
	srv := ttsServer(t, want, &got)
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "mimo-v2.5-tts", Blobs: blobs, HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref, err := c.Synthesize(context.Background(), port.SpeechRequest{
		Text: "你好", Voice: "冰糖", Style: "语速稍快", Tags: "温柔",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if ref.MIME != "audio/wav" || ref.Size != int64(len(want)) {
		t.Fatalf("BlobRef = %+v", ref)
	}
	rc, err := blobs.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	back, _ := io.ReadAll(rc)
	if !bytes.Equal(back, want) {
		t.Fatalf("落库音频 = %q, want %q", back, want)
	}

	msgs := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", msgs)
	}
	if u := msgs[0].(map[string]any); u["role"] != "user" || u["content"] != "语速稍快" {
		t.Fatalf("user 消息 = %v", u)
	}
	if a := msgs[1].(map[string]any); a["role"] != "assistant" || a["content"] != "(温柔)你好" {
		t.Fatalf("assistant 消息 = %v", a)
	}
	audio := got["audio"].(map[string]any)
	if audio["format"] != "wav" || audio["voice"] != "冰糖" {
		t.Fatalf("audio = %v", audio)
	}
}

// TestSynthesizeWithoutStyle 风格为空时不发 user 消息（模型有默认）。
func TestSynthesizeWithoutStyle(t *testing.T) {
	blobs := newTestBlobs(t)
	var got map[string]any
	srv := ttsServer(t, []byte("x"), &got)
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Model: "mimo-v2.5-tts", Blobs: blobs, HTTP: srv.Client()})
	if _, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "hi", Voice: "Mia"}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "assistant" {
		t.Fatalf("只应有 assistant 消息，得到 %v", msgs)
	}
}

// TestSynthesizeVoiceDesign 音色设计：描述进 user 消息，audio 不带 voice。
func TestSynthesizeVoiceDesign(t *testing.T) {
	blobs := newTestBlobs(t)
	var got map[string]any
	srv := ttsServer(t, []byte("x"), &got)
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Model: "mimo-v2.5-tts-voicedesign", Blobs: blobs, HTTP: srv.Client()})
	if _, err := c.Synthesize(context.Background(), port.SpeechRequest{
		Text: "你好", Design: "一位声音清亮的年轻女性", Style: "语速稍快",
	}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	msgs := got["messages"].([]any)
	if u := msgs[0].(map[string]any); u["content"] != "一位声音清亮的年轻女性\n语速稍快" {
		t.Fatalf("设计描述 = %v", u["content"])
	}
	if audio := got["audio"].(map[string]any); audio["voice"] != nil {
		t.Fatalf("voicedesign 不应发送 voice: %v", audio)
	}

	if _, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "你好"}); err == nil || !strings.Contains(err.Error(), "音色描述") {
		t.Fatalf("缺音色描述应报因，得到 %v", err)
	}
}

// TestSynthesizeVoiceClone 音色克隆：voice 是样本路径，转 Base64 发出（不是 data: URL）。
func TestSynthesizeVoiceClone(t *testing.T) {
	blobs := newTestBlobs(t)
	sample := []byte("RIFF-sample")
	path := filepath.Join(t.TempDir(), "sample.wav")
	if err := os.WriteFile(path, sample, 0o644); err != nil {
		t.Fatalf("写样本: %v", err)
	}
	var got map[string]any
	srv := ttsServer(t, []byte("x"), &got)
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Model: "mimo-v2.5-tts-voiceclone", Blobs: blobs, HTTP: srv.Client()})
	if _, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "你好", Voice: path}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	audio := got["audio"].(map[string]any)
	if audio["voice"] != base64.StdEncoding.EncodeToString(sample) {
		t.Fatalf("样本未按 Base64 发出: %v", audio["voice"])
	}

	if _, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "x"}); err == nil || !strings.Contains(err.Error(), "样本文件路径") {
		t.Fatalf("缺样本路径应报因，得到 %v", err)
	}
	bad := filepath.Join(t.TempDir(), "sample.ogg")
	if err := os.WriteFile(bad, sample, 0o644); err != nil {
		t.Fatalf("写样本: %v", err)
	}
	if _, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "x", Voice: bad}); err == nil || !strings.Contains(err.Error(), "wav / mp3") {
		t.Fatalf("不支持的样本格式应报因，得到 %v", err)
	}
}

// TestSynthesizeReportsServerError 非 2xx 明确报因。
func TestSynthesizeReportsServerError(t *testing.T) {
	blobs := newTestBlobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "voice not allowed", http.StatusBadRequest)
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Model: "mimo-v2.5-tts", Blobs: blobs, HTTP: srv.Client()})
	_, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "voice not allowed") {
		t.Fatalf("错误应含状态码与响应体，得到 %v", err)
	}
}

// TestSynthesizeRejectsResponseWithoutAudio 服务端收下请求却没有音频：明确报因。
func TestSynthesizeRejectsResponseWithoutAudio(t *testing.T) {
	blobs := newTestBlobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"no audio"}}]}`)
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Model: "mimo-v2.5-tts", Blobs: blobs, HTTP: srv.Client()})
	_, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "没有音频") {
		t.Fatalf("无音频应报因，得到 %v", err)
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
