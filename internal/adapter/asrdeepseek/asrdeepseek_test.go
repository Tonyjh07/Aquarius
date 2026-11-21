package asrdeepseek

// asrdeepseek_test.go DeepSeek 识别端点（D316）的形状锁定：multipart 只有 file 字段
// （没有 model / language——该后端没有语种参数）、8 MiB 上限、服务端错误原文照报。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// asrFixture 附件库 + 假服务端（记录收到的 multipart 字段与文件名）。
func asrFixture(t *testing.T, status int, body string, seen *map[string]string) (*Client, conversation.BlobRef) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			return
		}
		for k, v := range r.MultipartForm.Value {
			(*seen)[k] = v[0]
		}
		for _, f := range r.MultipartForm.File {
			(*seen)["file"] = f[0].Filename
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	blobs, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	ref, err := blobs.Put(context.Background(), strings.NewReader("RIFF....WAVEfmt "), "audio/wav", "录音.wav")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	c, err := New(Config{BaseURL: srv.URL, APIKey: "sk-test", Blobs: blobs})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return c, ref
}

// TestTranscribeShape 请求只有 file 一个字段（D316 决策 1：不发 model / language），
// 200 + {"text":…} → 转写文本。lang 参数收下但被丢弃。
func TestTranscribeShape(t *testing.T) {
	seen := map[string]string{}
	c, ref := asrFixture(t, http.StatusOK, `{"text":"你好世界"}`, &seen)
	for _, lang := range []string{"auto", "zh", "en"} {
		text, err := c.Transcribe(context.Background(), ref, lang)
		if err != nil {
			t.Fatalf("transcribe(%s): %v", lang, err)
		}
		if text != "你好世界" {
			t.Fatalf("text = %q", text)
		}
	}
	if _, ok := seen["model"]; ok {
		t.Fatalf("不该发 model 字段: %v", seen)
	}
	if _, ok := seen["language"]; ok {
		t.Fatalf("不该发 language 字段（无语种参数）: %v", seen)
	}
	if seen["file"] != "录音.wav" {
		t.Fatalf("file 字段 = %q, want 录音.wav", seen["file"])
	}
}

// TestTranscribeTooLarge 超 8 MiB 明确报错，请求不发出去。
func TestTranscribeTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("超限请求不该发到服务端")
	}))
	defer srv.Close()
	blobs, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	big := make([]byte, maxAudioBytes+1)
	ref, err := blobs.Put(context.Background(), strings.NewReader(string(big)), "audio/wav", "大段.wav")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	c, err := New(Config{BaseURL: srv.URL, APIKey: "k", Blobs: blobs})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := c.Transcribe(context.Background(), ref, "auto"); err == nil ||
		!strings.Contains(err.Error(), i18n.Tf("asr.err.audio_too_large", "mb", maxAudioBytes>>20)) {
		t.Fatalf("err = %v, want 8MiB 上限报因", err)
	}
}

// TestTranscribeServerError 服务端 400：状态码与响应体片段照报。
func TestTranscribeServerError(t *testing.T) {
	seen := map[string]string{}
	c, ref := asrFixture(t, http.StatusBadRequest, `{"error":"audio too long"}`, &seen)
	_, err := c.Transcribe(context.Background(), ref, "auto")
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "audio too long") {
		t.Fatalf("err = %v, want 状态码与原文都在", err)
	}
}
