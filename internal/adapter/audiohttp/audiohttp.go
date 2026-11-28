// Package audiohttp 语音适配器（ASR/TTS）共用的 HTTP 骨架与音频原语：base_url
// 校验、状态码检查与报因、超限读取、WAV 判定、合成结果落库。各适配器沿用既有
// 报因 key（ErrKeys 带各自词表），传输层失败与非 2xx 状态里的 429/408/5xx 顺带
// 标注 port.ErrTransient（与 LLM 链路同口径），供 decorate 的语音重试装饰器判据。
package audiohttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// MaxErrorBody 错误响应体进报因的上限（各适配器原值一致，收拢为常量）。
const MaxErrorBody = 4 << 10

// ErrKeys 适配器各自的报因词表（key 不变，语义不变；值由适配器给死）。
type ErrKeys struct {
	BaseURLEmpty   string // base_url 为空
	BaseURLInvalid string // base_url 解析失败（报因里已带 url 参数）
	ServerStatus   string // 非 2xx 状态码（code / body 参数）
	RequestFailed  string // 传输层失败
	ReadAudio      string // 音频读失败（附件库与响应体共用形状）
	AudioEmpty     string // 音频为空
	AudioTooLarge  string // 音频超上限（mb 参数由本包填）
	SaveAudio      string // 合成结果落库失败（仅 TTS 用）
}

// ValidateBaseURL 校验 base_url：非空、http(s)、有 host；返回去掉尾斜杠的基址
// 与带整体超时的客户端（超时口径：识别 2 分钟、合成 3 分钟，由调用方给）。
func ValidateBaseURL(raw string, defaultTimeout time.Duration, keys ErrKeys) (string, *http.Client, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil, errors.New(i18n.T(keys.BaseURLEmpty))
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", nil, errors.New(i18n.Tf(keys.BaseURLInvalid, "url", raw))
	}
	return strings.TrimRight(raw, "/"), &http.Client{Timeout: defaultTimeout}, nil
}

// Do 发请求并统一处理传输层失败：连接拒绝/超时/DNS 抖动视为瞬时（ctx 已取消的
// 不算），报因带 RequestFailed 文案并标注 port.ErrTransient。
func Do(ctx context.Context, hc *http.Client, req *http.Request, keys ErrKeys) (*http.Response, error) {
	resp, err := hc.Do(req)
	if err != nil {
		msg := i18n.T(keys.RequestFailed)
		if ctx.Err() == nil {
			return nil, fmt.Errorf("%s: %w: %w", msg, err, port.ErrTransient)
		}
		return nil, fmt.Errorf("%s: %w", msg, err)
	}
	return resp, nil
}

// StatusError 非 2xx 响应的统一报因：server_status 文案带状态码与错误体摘要；
// 429/408/5xx 与 LLM 链路同判为瞬时，标注 port.ErrTransient。
func StatusError(code int, body io.Reader, keys ErrKeys) error {
	msg := i18n.Tf(keys.ServerStatus, "code", code, "body", Snippet(body))
	if transientStatus(code) {
		return fmt.Errorf("%s: %w", msg, port.ErrTransient)
	}
	return errors.New(msg)
}

// transientStatus 可重试的 HTTP 状态（限流/请求超时/服务端错误），对齐 LLM 链路口径。
func transientStatus(code int) bool {
	return code == http.StatusTooManyRequests ||
		code == http.StatusRequestTimeout ||
		code >= http.StatusInternalServerError
}

// Snippet 错误响应体摘要（截到 MaxErrorBody）；空体给 audio.detail.empty_body 文案。
func Snippet(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, MaxErrorBody))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return i18n.T("audio.detail.empty_body")
	}
	return s
}

// ReadBlob 从附件库读一段音频：超限读取、空判、上限判。name 的缺省规则各适配器
// 不同（扩展名映射/固定 audio.wav），留在适配器里。
func ReadBlob(ctx context.Context, blobs port.AttachmentStore, ref conversation.BlobRef, maxBytes int64, keys ErrKeys) ([]byte, error) {
	rc, err := blobs.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T(keys.ReadAudio), err)
	}
	defer rc.Close()
	return readLimited(rc, maxBytes, keys)
}

// ReadAudio 读响应体里的音频字节，形状与 ReadBlob 相同：超限读取、空判、上限判。
func ReadAudio(r io.Reader, maxBytes int64, keys ErrKeys) ([]byte, error) {
	return readLimited(r, maxBytes, keys)
}

// readLimited 共用的「读一段有上限的字节」骨架。
func readLimited(r io.Reader, maxBytes int64, keys ErrKeys) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T(keys.ReadAudio), err)
	}
	if len(data) == 0 {
		return nil, errors.New(i18n.T(keys.AudioEmpty))
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New(i18n.Tf(keys.AudioTooLarge, "mb", maxBytes>>20))
	}
	return data, nil
}

// IsWAV 粗判 WAV 头（RIFF....WAVE）。
func IsWAV(b []byte) bool {
	return len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WAVE"
}

// Sniff 报因里带上响应体的开头，便于判断服务端到底回了什么（TTS 两个适配器同词表）。
func Sniff(b []byte) string {
	n := min(len(b), 80)
	s := strings.TrimSpace(string(b[:n]))
	if s == "" {
		return i18n.T("tts.err.sniff_empty")
	}
	return i18n.Tf("tts.err.sniff_head", "text", strconv.Quote(s))
}

// SaveWAV 把合成结果按 speech-%d.wav 命名落附件库（各 TTS 适配器同规则）。
func SaveWAV(ctx context.Context, blobs port.AttachmentStore, raw []byte, keys ErrKeys) (conversation.BlobRef, error) {
	name := fmt.Sprintf("speech-%d.wav", time.Now().UnixMilli())
	ref, err := blobs.Put(ctx, bytes.NewReader(raw), "audio/wav", name)
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("%s: %w", i18n.T(keys.SaveAudio), err)
	}
	return ref, nil
}
