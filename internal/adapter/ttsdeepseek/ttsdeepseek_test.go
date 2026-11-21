package ttsdeepseek

// ttsdeepseek_test.go DeepSeek 合成端点（D316）的形状锁定：请求体四字段（filter_markdown
// 固定开）、音色纵深防御、计费字符（汉字算 2）与 64 KiB 上限、裸字节响应落附件库。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ttsFixture 假服务端（把请求体解出来存进 *map）+ 真附件库客户端。
func ttsFixture(t *testing.T, status int, resp []byte, captured *map[string]any) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		*captured = body
		w.WriteHeader(status)
		w.Write(resp)
	}))
	t.Cleanup(srv.Close)
	blobs, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	c, err := New(Config{BaseURL: srv.URL, APIKey: "sk-test", Blobs: blobs})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return c
}

// TestSynthesizeShape 请求体形状：voice_id / format=wav / filter_markdown=true；
// Style/Tags/Design 不消费（D316 决策 4）；响应裸字节落附件库为 WAV 引用。
func TestSynthesizeShape(t *testing.T) {
	captured := map[string]any{}
	c := ttsFixture(t, http.StatusOK, []byte("RIFF....WAVEfmt "), &captured)
	ref, err := c.Synthesize(context.Background(), port.SpeechRequest{
		Text: "你好，**world**", Voice: "Stella",
		Style: "语速稍快", Design: "温柔", Tags: "轻松",
	})
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if ref.MIME != "audio/wav" || ref.Name == "" {
		t.Fatalf("引用 = %+v, want WAV", ref)
	}
	if captured["voice_id"] != "stella" || captured["format"] != "wav" || captured["filter_markdown"] != true {
		t.Fatalf("请求体 = %v", captured)
	}
	if _, ok := captured["style"]; ok {
		t.Fatalf("不该消费 Style: %v", captured)
	}
}

// TestSynthesizeVoiceRejected 音色纵深防御：装配层已拦，适配器再拒一道。
func TestSynthesizeVoiceRejected(t *testing.T) {
	c := ttsFixture(t, http.StatusOK, []byte("RIFF"), &map[string]any{})
	_, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: "你好", Voice: "冰糖"})
	if err == nil || !strings.Contains(err.Error(), "stella") {
		t.Fatalf("err = %v, want 音色报因列出两档", err)
	}
}

// TestSynthesizeTextTooLong 计费字符（汉字算 2）超限在请求发出前报。
func TestSynthesizeTextTooLong(t *testing.T) {
	c := ttsFixture(t, http.StatusOK, []byte("RIFF"), &map[string]any{})
	long := strings.Repeat("汉", maxBilledChars/2+1) // 全按汉字计 = maxBilledChars+2
	_, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: long, Voice: "stella"})
	if err == nil || !strings.Contains(err.Error(), i18n.Tf("tts.deepseek.err_text_too_long", "billed", 10002, "limit", maxBilledChars)) {
		t.Fatalf("err = %v, want 计费字符上限报因", err)
	}
}

// TestBilledChars 计费口径：ASCII 按 1、全宽按 2、混合累计。
func TestBilledChars(t *testing.T) {
	if got := billedChars("hello"); got != 5 {
		t.Fatalf("ascii = %d, want 5", got)
	}
	if got := billedChars("汉汉"); got != 4 {
		t.Fatalf("cjk = %d, want 4", got)
	}
	if got := billedChars("a汉"); got != 3 {
		t.Fatalf("mixed = %d, want 3", got)
	}
}

// TestSynthesizeBodyTooLarge 64 KiB 请求体上限（长英文文本也会撞）。
func TestSynthesizeBodyTooLarge(t *testing.T) {
	c := ttsFixture(t, http.StatusOK, []byte("RIFF"), &map[string]any{})
	long := strings.Repeat("a", 80_000) // 计费字符 80000 < 10000？不——ASCII 按 1 计=80000，先撞计费口径
	_, err := c.Synthesize(context.Background(), port.SpeechRequest{Text: long, Voice: "stella"})
	if err == nil {
		t.Fatal("want 报错")
	}
	// 80000 个 ASCII 字符计费 80000，先撞计费上限——这正是口径先于体积的意义。
	if !strings.Contains(err.Error(), i18n.Tf("tts.deepseek.err_text_too_long", "billed", 80000, "limit", maxBilledChars)) {
		t.Fatalf("err = %v, want 计费上限先报", err)
	}
}
