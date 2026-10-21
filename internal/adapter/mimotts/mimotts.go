// Package mimotts 实现 port.Synthesizer（D179）：MiMo-TTS 系列走
// chat/completions，不是 /audio/speech。
//
//	POST {base_url}/chat/completions
//	{ "model": "…-tts[-voicedesign|-voiceclone]",
//	  "messages": [ {"role":"user","content":"风格/音色描述"},
//	                {"role":"assistant","content":"(标签)正文"} ],
//	  "audio": { "format": "wav", "voice": "预置音色名 或 样本 base64" } }
//
// 音频在 `choices[0].message.audio.data`（base64）。三种模型的分派只看模型名
// 后缀：预置音色用 `voice`，音色设计用 user 消息里的 `design`（`voice` 不支持），
// 音色克隆用 `voice` 里的样本文件 base64。整段合成后落附件库，由播放器播放。
package mimotts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	chatPath = "/chat/completions"
	// maxResponseBytes 响应体上限：Base64 音频再大也不该把内存撑爆（10MB 音频
	// 的 Base64 ≈ 13.3MB，留到 32MB 覆盖长文本）。
	maxResponseBytes = 32 << 20
	// maxSampleBytes 音色克隆样本上限（几秒到几十秒的 mp3/wav）。
	maxSampleBytes = 10 << 20
	maxErrorBody   = 4 << 10

	modePreset = "preset"
	modeDesign = "design"
	modeClone  = "clone"
)

var _ port.Synthesizer = (*Client)(nil)

// Config 适配器配置（base_url 与密钥来自被引用的 provider）。
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	// Blobs 合成结果落这里（可重播、随 GC）。
	Blobs port.AttachmentStore
	// HTTP 为 nil 时用带 3 分钟整体超时的默认客户端。
	HTTP *http.Client
}

// Client 合成客户端。
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
		return nil, errors.New("tts: base_url 为空")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("tts: 非法 base_url %q", cfg.BaseURL)
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, errors.New("tts: 模型名为空")
	}
	if cfg.Blobs == nil {
		return nil, errors.New("tts: 未接附件库")
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Minute}
	}
	return &Client{
		base:  strings.TrimRight(raw, "/"),
		key:   strings.TrimSpace(cfg.APIKey),
		model: model,
		blobs: cfg.Blobs,
		hc:    hc,
	}, nil
}

// Mode 返回模型形态（导出给设置窗提示用：音色描述/样本路径该填哪个框）。
func (c *Client) Mode() string { return modeOf(c.model) }

// Synthesize 合成一段语音并落附件库，返回引用。
func (c *Client) Synthesize(ctx context.Context, req port.SpeechRequest) (conversation.BlobRef, error) {
	if ctx == nil {
		return conversation.BlobRef{}, errors.New("tts: nil context")
	}
	if err := ctx.Err(); err != nil {
		return conversation.BlobRef{}, err
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return conversation.BlobRef{}, errors.New("tts: 要合成的文本为空")
	}
	if req.Tags != "" {
		text = "(" + strings.TrimSpace(req.Tags) + ")" + text
	}

	msgs := make([]message, 0, 2)
	voice := strings.TrimSpace(req.Voice)
	audio := audioParam{Format: "wav"}
	switch modeOf(c.model) {
	case modeDesign:
		// 音色设计：user 消息就是音色描述（必填）；风格指令与它同属自然语言
		// 描述，接在后面一起交给模型。voice 字段该模型不支持，必须省略。
		desc := strings.TrimSpace(req.Design)
		if desc == "" {
			return conversation.BlobRef{}, errors.New("tts: 音色设计模型需要在设置里填写音色描述")
		}
		if s := strings.TrimSpace(req.Style); s != "" {
			desc += "\n" + s
		}
		msgs = append(msgs, message{Role: "user", Content: desc})
	case modeClone:
		// 音色克隆：voice 是样本文件绝对路径，读出来转 Base64（接口只收
		// Base64，不收 data: URL）。
		if voice == "" {
			return conversation.BlobRef{}, errors.New("tts: 音色克隆模型需要填写样本文件路径")
		}
		sample, err := readSample(voice)
		if err != nil {
			return conversation.BlobRef{}, err
		}
		audio.Voice = base64.StdEncoding.EncodeToString(sample)
		if s := strings.TrimSpace(req.Style); s != "" {
			msgs = append(msgs, message{Role: "user", Content: s})
		}
	default:
		// 预置音色：voice 是音色名；风格指令可选。
		audio.Voice = voice
		if s := strings.TrimSpace(req.Style); s != "" {
			msgs = append(msgs, message{Role: "user", Content: s})
		}
	}
	msgs = append(msgs, message{Role: "assistant", Content: text})

	payload, err := json.Marshal(request{Model: c.model, Messages: msgs, Audio: audio})
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("tts: 编码请求: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+chatPath, bytes.NewReader(payload))
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("tts: 构造请求: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.hc.Do(hreq)
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("tts: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return conversation.BlobRef{}, fmt.Errorf("tts: 服务端返回 %d: %s", resp.StatusCode, snippet(resp.Body))
	}
	var out response
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return conversation.BlobRef{}, fmt.Errorf("tts: 解析响应: %w", err)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Audio == nil {
		return conversation.BlobRef{}, errors.New("tts: 响应里没有音频（服务端可能拒绝了语气或音色参数）")
	}
	raw, err := base64.StdEncoding.DecodeString(out.Choices[0].Message.Audio.Data)
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("tts: 解码音频: %w", err)
	}
	if len(raw) == 0 {
		return conversation.BlobRef{}, errors.New("tts: 服务端返回了空音频")
	}
	name := fmt.Sprintf("speech-%d.wav", time.Now().UnixMilli())
	ref, err := c.blobs.Put(ctx, bytes.NewReader(raw), "audio/wav", name)
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("tts: 保存音频: %w", err)
	}
	return ref, nil
}

// modeOf 按模型名后缀判断形态（D179）：其余（含未来同族预置音色模型）按预置音色。
func modeOf(model string) string {
	switch {
	case strings.HasSuffix(model, "-voicedesign"):
		return modeDesign
	case strings.HasSuffix(model, "-voiceclone"):
		return modeClone
	default:
		return modePreset
	}
}

// readSample 读克隆样本并校验格式（接口只收 mp3 / wav）。
func readSample(path string) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".wav", ".mp3":
	default:
		return nil, fmt.Errorf("tts: 音色克隆样本只支持 wav / mp3（当前 %q）", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("tts: 读取音色克隆样本: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSampleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("tts: 读取音色克隆样本: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("tts: 音色克隆样本为空")
	}
	if len(data) > maxSampleBytes {
		return nil, fmt.Errorf("tts: 音色克隆样本超过 %d MB", maxSampleBytes>>20)
	}
	return data, nil
}

func snippet(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, maxErrorBody))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "（空响应体）"
	}
	return s
}

type request struct {
	Model    string     `json:"model"`
	Messages []message  `json:"messages"`
	Audio    audioParam `json:"audio"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type audioParam struct {
	Format string `json:"format"`
	Voice  string `json:"voice,omitempty"`
}

type response struct {
	Choices []struct {
		Message struct {
			Audio *struct {
				Data string `json:"data"`
			} `json:"audio"`
		} `json:"message"`
	} `json:"choices"`
}
