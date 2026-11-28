// Package asrfunasr 实现 port.Transcriber：走 FunASR 自带的 HTTP 服务
// （`runtime/python/http/server.py`），该服务不是 OpenAI 形状。
//
//	POST {base_url}/recognition
//	Content-Type: multipart/form-data
//	  audio=<音频字节>
//	→ {"text": "...", "sentences": [{"text","start","end"}], "code": 0}
//
// 与 asropenai 的差别只有三处：路径、表单字段名（`audio` 而非 `file`）、响应
// 形状（`code` 非 0 即失败）。FunASR 另有一个 OpenAI 兼容示例
// （`examples/openai_api`），那种部署直接用 `kind=openai` 就行。
package asrfunasr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/audiohttp"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	recognitionPath = "/recognition"
	maxAudioBytes   = 24 << 20
	maxRespBody     = 1 << 20
)

// errKeys 本适配器的报因词表（key 与既有口径一致）。
var errKeys = audiohttp.ErrKeys{
	BaseURLEmpty:   "asr.err.base_url_empty",
	BaseURLInvalid: "asr.err.base_url_invalid",
	ServerStatus:   "asr.err.server_status",
	RequestFailed:  "asr.err.request_failed",
	ReadAudio:      "asr.err.read_audio",
	AudioEmpty:     "asr.err.audio_empty",
	AudioTooLarge:  "asr.err.audio_too_large",
}

var _ port.Transcriber = (*Client)(nil)

// Config 适配器配置。
type Config struct {
	BaseURL string
	APIKey  string
	// Blobs 音频附件库：Transcribe 只拿到 BlobRef，字节在这里读。
	Blobs port.AttachmentStore
	// HTTP 为 nil 时用带 2 分钟整体超时的默认客户端。
	HTTP *http.Client
}

// Client 转写客户端。
type Client struct {
	base  string
	key   string
	blobs port.AttachmentStore
	hc    *http.Client
}

// New 创建客户端并校验配置（fail-fast，装配期报因）。
func New(cfg Config) (*Client, error) {
	base, hc, err := audiohttp.ValidateBaseURL(cfg.BaseURL, 2*time.Minute, errKeys)
	if err != nil {
		return nil, err
	}
	if cfg.HTTP != nil {
		hc = cfg.HTTP
	}
	if cfg.Blobs == nil {
		return nil, errors.New(i18n.T("asr.err.no_blobs"))
	}
	return &Client{
		base:  base,
		key:   strings.TrimSpace(cfg.APIKey),
		blobs: cfg.Blobs,
		hc:    hc,
	}, nil
}

// Transcribe 把一段音频转成文本。lang 不参与请求：FunASR 的模型自己定语种
// （`paraformer-zh` 就是中文），没有可传的 language 参数。
func (c *Client) Transcribe(ctx context.Context, audio conversation.BlobRef, lang string) (string, error) {
	if ctx == nil {
		return "", errors.New(i18n.T("asr.err.nil_context"))
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := audiohttp.ReadBlob(ctx, c.blobs, audio, maxAudioBytes, errKeys)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(audio.Name)
	if name == "" {
		name = "audio.wav"
	}
	body, contentType, err := buildMultipart(data, name)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+recognitionPath, body)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("asr.err.build_request"), err)
	}
	req.Header.Set("Content-Type", contentType)
	if c.key != "" {
		req.Header.Set("X-API-KEY", c.key)
	}
	resp, err := audiohttp.Do(ctx, c.hc, req, errKeys)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", audiohttp.StatusError(resp.StatusCode, resp.Body, errKeys)
	}
	var out response
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRespBody)).Decode(&out); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("asr.err.parse_response"), err)
	}
	if out.Code != 0 {
		msg := strings.TrimSpace(out.Msg)
		if msg == "" {
			msg = i18n.T("asr.funasr.no_reason")
		}
		return "", errors.New(i18n.Tf("asr.funasr.err_server_code", "code", out.Code, "message", msg))
	}
	text := strings.TrimSpace(out.Text)
	if text == "" {
		return "", errors.New(i18n.T("asr.err.no_text"))
	}
	return text, nil
}

// buildMultipart 组请求体：FunASR 的表单字段叫 `audio`。
func buildMultipart(audio []byte, name string) (io.Reader, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("audio", name)
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

type response struct {
	Text string `json:"text"`
	Msg  string `json:"msg"`
	Code int    `json:"code"`
}
