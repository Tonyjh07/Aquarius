// Package asropenai 实现 port.Transcriber：走 OpenAI 的
// `/v1/audio/transcriptions` multipart 协议。
//
//	POST {base_url}/audio/transcriptions
//	Content-Type: multipart/form-data
//	  file=<音频字节>  model=<模型名>  language=<语种>
//	→ {"text": "..."}
//
// 这是业界的事实标准，所以同一个适配器覆盖一大片服务端：OpenAI 官方、FunASR
// 的 openai_api 示例、whisper.cpp 的 whisper-server、以及各类自建网关。差异
// 只在 `audio.asr.kind` 与端点，上层拿到的仍是 (BlobRef, lang) → string。
package asropenai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	transcribePath = "/audio/transcriptions"
	// maxAudioBytes 单次转写的音频上限：多数服务端收 25MB 以内。
	maxAudioBytes = 24 << 20
	maxErrorBody  = 4 << 10
	maxRespBody   = 1 << 20
)

var _ port.Transcriber = (*Client)(nil)

// Config 适配器配置（base_url 与密钥来自 audio.asr 自己的配置或借来的 provider）。
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	// Blobs 音频附件库：Transcribe 只拿到 BlobRef，字节在这里读。
	Blobs port.AttachmentStore
	// HTTP 为 nil 时用带 2 分钟整体超时的默认客户端（一次请求一次响应）。
	HTTP *http.Client
}

// Client 转写客户端。
type Client struct {
	base  string
	key   string
	model string
	blobs port.AttachmentStore
	hc    *http.Client
}

// New 创建客户端并校验配置（fail-fast，装配期报因）。
func New(cfg Config) (*Client, error) {
	raw := strings.TrimSpace(cfg.BaseURL)
	if raw == "" {
		return nil, errors.New(i18n.T("asr.err.base_url_empty"))
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New(i18n.Tf("asr.err.base_url_invalid", "url", cfg.BaseURL))
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, errors.New(i18n.T("asr.err.model_empty"))
	}
	if cfg.Blobs == nil {
		return nil, errors.New(i18n.T("asr.err.no_blobs"))
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Client{
		base:  strings.TrimRight(raw, "/"),
		key:   strings.TrimSpace(cfg.APIKey),
		model: model,
		blobs: cfg.Blobs,
		hc:    hc,
	}, nil
}

// Transcribe 把一段音频转成文本。lang 为 auto | zh | en（空 = auto，不发该字段）。
func (c *Client) Transcribe(ctx context.Context, audio conversation.BlobRef, lang string) (string, error) {
	if ctx == nil {
		return "", errors.New(i18n.T("asr.err.nil_context"))
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, name, err := readAudio(ctx, c.blobs, audio)
	if err != nil {
		return "", err
	}
	body, contentType, err := buildMultipart(c.model, lang, data, name)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+transcribePath, body)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("asr.err.build_request"), err)
	}
	req.Header.Set("Content-Type", contentType)
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("asr.err.request_failed"), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", errors.New(i18n.Tf("asr.err.server_status", "code", resp.StatusCode, "body", snippet(resp.Body)))
	}
	var out response
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRespBody)).Decode(&out); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("asr.err.parse_response"), err)
	}
	text := strings.TrimSpace(out.Text)
	if text == "" {
		return "", errors.New(i18n.T("asr.err.no_text"))
	}
	return text, nil
}

// buildMultipart 组请求体。language 只在明确给了语种时才发：`auto` 是本地
// 约定的「自己判断」，多数服务端不认这个词，发过去反而会被拒。
func buildMultipart(model, lang string, audio []byte, name string) (io.Reader, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("model", model); err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("asr.err.encode_request"), err)
	}
	if l := languageField(lang); l != "" {
		if err := mw.WriteField("language", l); err != nil {
			return nil, "", fmt.Errorf("%s: %w", i18n.T("asr.err.encode_request"), err)
		}
	}
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("asr.err.encode_request"), err)
	}
	if _, err := fw.Write(audio); err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("asr.err.encode_request"), err)
	}
	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("asr.err.encode_request"), err)
	}
	return &buf, mw.FormDataContentType(), nil
}

// languageField 把本地语种词换成请求里的 language 字段（空 = 不发）。
func languageField(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "auto":
		return ""
	default:
		return strings.ToLower(strings.TrimSpace(lang))
	}
}

// readAudio 从附件库取音频，返回字节与上传用的文件名（服务端常按扩展名
// 判断容器，所以名字要带对）。
func readAudio(ctx context.Context, blobs port.AttachmentStore, audio conversation.BlobRef) ([]byte, string, error) {
	rc, err := blobs.Get(ctx, audio)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("asr.err.read_audio"), err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxAudioBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("asr.err.read_audio"), err)
	}
	if len(data) == 0 {
		return nil, "", errors.New(i18n.T("asr.err.audio_empty"))
	}
	if len(data) > maxAudioBytes {
		return nil, "", errors.New(i18n.Tf("asr.err.audio_too_large", "mb", maxAudioBytes>>20))
	}
	name := audio.Name
	if strings.TrimSpace(name) == "" {
		name = "audio" + extension(audio.MIME)
	}
	return data, name, nil
}

// extension 按 MIME 给上传文件配扩展名（空 = 按 wav，本地录音就是 WAV）。
func extension(mime string) string {
	m := strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	switch m {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/flac", "audio/x-flac":
		return ".flac"
	case "audio/ogg":
		return ".ogg"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return ".m4a"
	default:
		return ".wav"
	}
}

func snippet(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, maxErrorBody))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return i18n.T("audio.detail.empty_body")
	}
	return s
}

type response struct {
	Text string `json:"text"`
}
