// Package asrwhisper 实现 port.Transcriber：在本进程里跑 whisper.cpp。
//
// whisper.cpp 被编成 WebAssembly，再用 wasm2go 翻成 Go 源码（见
// github.com/mpyw/whisper.go-wasm），所以既没有 cgo，也没有 wasm 运行时，
// `go build` 就够。模型是 whisper.cpp 的 ggml 格式（ggml-base.bin 等），
// 由用户在设置里给路径——模型几十 MB 起，不进仓库、不进二进制。
//
// 它只吃 16kHz 单声道 float32 样本，不认任何音频容器。本适配器负责把附件库
// 里的 WAV 解成样本，其余格式直接报错（不做转码，不引解码器）。
package asrwhisper

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	whisper "github.com/mpyw/whisper.go"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.Transcriber = (*Client)(nil)

const (
	// maxAudioBytes 单次转写的音频上限。whisper.cpp 每样本约 6 字节内存，
	// 但这里限的是读进内存的附件体积：4 分钟 16kHz WAV ≈ 7.7MB，留到 24MB
	// 覆盖更长的录音，同时不至于让一次误选的音频文件撑爆内存。
	maxAudioBytes = 24 << 20
	// minSamples 低于约 100ms 的音频 whisper.cpp 不出任何分段（whisper.go
	// 的文档口径），提前判掉，免得白跑一遍还报「没识别出文字」。
	minSamples = 1640
)

// Config 适配器配置。
type Config struct {
	// ModelPath ggml 模型文件的绝对路径（如 ~/.aquarius/models/ggml-base-q5_1.bin）。
	ModelPath string
	// Blobs 音频附件库：Transcribe 只拿到 BlobRef，字节在这里读。
	Blobs port.AttachmentStore
	// Model 已加载的模型（测试注入用）；非 nil 时忽略 ModelPath。
	Model Model
}

// Model 一次转写所需的模型能力（*whisper.Model 满足它）。
type Model interface {
	Transcribe(ctx context.Context, pcm []float32, opts *whisper.TranscribeOptions) (*whisper.Result, error)
	Close() error
}

// Client 本地转写客户端。
type Client struct {
	model Model
	blobs port.AttachmentStore

	mu     sync.Mutex
	closed bool
}

// New 加载模型（fail-fast，装配期报因）。模型只在装配时加载一次：whisper.go
// 每个实例预留 4GiB 地址空间，且同一实例的 Transcribe 串行，逐次加载没有意义。
func New(cfg Config) (*Client, error) {
	if cfg.Blobs == nil {
		return nil, errors.New("asr: 未接附件库")
	}
	model := cfg.Model
	if model == nil {
		path := strings.TrimSpace(cfg.ModelPath)
		if path == "" {
			return nil, errors.New("asr: 未填写本地模型文件路径")
		}
		m, err := loadModel(path)
		if err != nil {
			return nil, err
		}
		model = m
	}
	return &Client{model: model, blobs: cfg.Blobs}, nil
}

// loadModel 读模型文件并交给 whisper.go。模型不在或读不动时报因里带上
// 该去哪拿——这是用户最容易卡住的一步，报错要能自己走完。
func loadModel(path string) (Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("asr: 本地模型文件不存在：%s（到 https://huggingface.co/ggerganov/whisper.cpp "+
				"下载 ggml-base-q5_1.bin 之类的 ggml 模型，放到该路径）", path)
		}
		return nil, fmt.Errorf("asr: 读取本地模型 %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("asr: 本地模型文件为空：%s", path)
	}
	m, err := whisper.New(data, nil)
	if err != nil {
		return nil, fmt.Errorf("asr: 加载本地模型 %s: %w", path, err)
	}
	return m, nil
}

// Transcribe 把一段音频转成文本。lang 为 auto | zh | en（空 = auto）。
func (c *Client) Transcribe(ctx context.Context, audio conversation.BlobRef, lang string) (string, error) {
	if ctx == nil {
		return "", errors.New("asr: nil context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return "", errors.New("asr: 本地转写已关闭")
	}
	pcm, err := c.readPCM(ctx, audio)
	if err != nil {
		return "", err
	}
	if len(pcm) < minSamples {
		return "", errors.New("asr: 录音太短，没听清（至少要有 0.1 秒的声音）")
	}
	lang = strings.TrimSpace(lang)
	if lang == "" {
		lang = "auto"
	}
	res, err := c.model.Transcribe(ctx, pcm, &whisper.TranscribeOptions{Language: lang})
	if err != nil {
		return "", fmt.Errorf("asr: 本地转写失败: %w", err)
	}
	text := joinSegments(res)
	if text == "" {
		return "", errors.New("asr: 没有识别出文字（录音可能为空或太短）")
	}
	return text, nil
}

// Close 释放模型。之后 Transcribe 报错（whisper.go 侧同样如此）。
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.model == nil {
		return nil
	}
	return c.model.Close()
}

// joinSegments 把分段拼成一段话。whisper.cpp 的中文分段自带标点、段间不加空格；
// 英文段间需要一个空格。
func joinSegments(res *whisper.Result) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, s := range res.Segments {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		if b.Len() > 0 && needsSpace(b.String(), text) {
			b.WriteByte(' ')
		}
		b.WriteString(text)
	}
	return b.String()
}

// needsSpace 前一段结尾与后一段开头都是 ASCII 才补空格：中文标点与汉字之间
// 插空格会显得很怪。
func needsSpace(prev, next string) bool {
	if prev == "" || next == "" {
		return false
	}
	last := prev[len(prev)-1]
	first := next[0]
	return last < 0x80 && first < 0x80
}

// readPCM 从附件库取音频并解成 16kHz 单声道 float32 样本。
// 只认 WAV：本地录音就是 WAV，其余格式不在这里转码（whisper.go 也不解码）。
func (c *Client) readPCM(ctx context.Context, audio conversation.BlobRef) ([]float32, error) {
	rc, err := c.blobs.Get(ctx, audio)
	if err != nil {
		return nil, fmt.Errorf("asr: 读取音频附件: %w", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxAudioBytes+1))
	if err != nil {
		return nil, fmt.Errorf("asr: 读取音频附件: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("asr: 音频附件为空")
	}
	if len(data) > maxAudioBytes {
		return nil, fmt.Errorf("asr: 音频超过 %d MB 上限，请缩短录音", maxAudioBytes>>20)
	}
	return decodeWAV(data)
}

// decodeWAV 读 16bit PCM、单声道、16kHz 的 WAV，返回 [-1,1] 的样本。
// 别的采样率/声道数直接报错——whisper.cpp 不做重采样，喂错只会得到乱码文本。
func decodeWAV(b []byte) ([]float32, error) {
	if len(b) < 12 || !bytes.Equal(b[0:4], []byte("RIFF")) || !bytes.Equal(b[8:12], []byte("WAVE")) {
		return nil, errors.New("asr: 本地转写只支持 WAV 录音")
	}
	var seenFmt bool
	for chunk := b[12:]; len(chunk) >= 8; {
		id := string(chunk[0:4])
		size := int(binary.LittleEndian.Uint32(chunk[4:8]))
		chunk = chunk[8:]
		if size > len(chunk) {
			// 写到管道里的 WAV 会把 data 长度留成 0xFFFFFFFF；截断的文件
			// 也会比声明的短。都按实际有的字节读。
			if id != "data" {
				return nil, fmt.Errorf("asr: WAV 的 %q 块被截断", id)
			}
			size = len(chunk)
		}
		body := chunk[:size]
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("asr: WAV 的 fmt 块被截断")
			}
			tag := binary.LittleEndian.Uint16(body[0:])
			channels := binary.LittleEndian.Uint16(body[2:])
			rate := binary.LittleEndian.Uint32(body[4:])
			bits := binary.LittleEndian.Uint16(body[14:])
			// WAVE_FORMAT_EXTENSIBLE：真正的格式在 subformat GUID 的头 2 字节。
			if tag == 0xFFFE && size >= 40 {
				tag = binary.LittleEndian.Uint16(body[24:])
			}
			if tag != 1 || channels != 1 || rate != whisper.SampleRate || bits != 16 {
				return nil, fmt.Errorf("asr: 本地转写要 16bit 单声道 %dHz 的 WAV（当前 格式 %d / %d 声道 / %dHz / %dbit）",
					whisper.SampleRate, tag, channels, rate, bits)
			}
			seenFmt = true
		case "data":
			if !seenFmt {
				return nil, errors.New("asr: WAV 的 data 块出现在 fmt 块之前")
			}
			pcm := make([]float32, size/2)
			for i := range pcm {
				pcm[i] = float32(int16(binary.LittleEndian.Uint16(body[2*i:]))) / 32768
			}
			return pcm, nil
		}
		// 每个块都补齐到偶数长度。
		if size+size%2 < len(chunk) {
			chunk = chunk[size+size%2:]
		} else {
			chunk = nil
		}
	}
	return nil, errors.New("asr: WAV 里没有 data 块")
}
