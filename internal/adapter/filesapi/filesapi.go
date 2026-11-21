package filesapi

// filesapi.go DeepSeek Files API 上传适配器（D315）：`POST {base}/v1/files` multipart，
// 解析在上传时完成、返回的 file_id 即刻可用。端点与密钥由装配根照 D222 的借用模式解析好
// 传进来（本适配器不懂 provider）。**不做重试**——服务端解析按页计费（D315 决策 6）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// 上传超时（DeepSeek 文档：文档类 120 秒；其余类 10 分钟，本适配器只收文档类，统一 120s）。
const uploadTimeout = 120 * time.Second

// Limits 上传候选的大小上限（服务端口径，超限明确报错不静默回落，D315 决策 6）。
const (
	// MaxDocBytes 文档类（PDF/Word/PPT）100 MiB。
	MaxDocBytes = 100 << 20
	// MaxImageBytes 图片类 64 MiB。
	MaxImageBytes = 64 << 20
)

// Options 构造参数。
type Options struct {
	// BaseURL 路径前缀（如 https://api.deepseek.com/v1；文件端点在其后追加 /files）。
	BaseURL string
	// APIKey 已解析的明文密钥（装配根经 Secrets 取好）。
	APIKey string
	// HTTP 覆盖 HTTP 客户端（测试用）；nil = 缺省。
	HTTP *http.Client
}

// Uploader port.FileUploader 的 DeepSeek 实现。
type Uploader struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New 构造适配器。
func New(opts Options) *Uploader {
	h := opts.HTTP
	if h == nil {
		h = &http.Client{Timeout: uploadTimeout}
	}
	return &Uploader{baseURL: opts.BaseURL, apiKey: opts.APIKey, http: h}
}

// filesResponse `POST /files` 的响应体（只声明本程序读写的字段）。
type filesResponse struct {
	ID string `json:"id"`
	// Error DeepSeek 的错误形状（{"error":{"message":…}}）；成功时缺键。
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Upload 实现 port.FileUploader：multipart 上传，`purpose` 必须先于 `file`（服务端对
// 文档类强制这个字段序，顺序反了 400）。
func (u *Uploader) Upload(ctx context.Context, name, mime string, data []byte, purpose string, ttl time.Duration) (string, error) {
	if purpose == "" {
		return "", fmt.Errorf("%s", i18n.T("files.err.purpose_empty"))
	}
	if err := checkSize(mime, len(data)); err != nil {
		return "", err
	}
	body, ctype, err := buildMultipart(name, data, purpose, ttl)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.baseURL+"/files", body)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("files.err.build_request"), err)
	}
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Authorization", "Bearer "+u.apiKey)
	resp, err := u.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("files.err.upload_failed"), err)
	}
	defer resp.Body.Close()
	var out filesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("files.err.bad_response"), err)
	}
	if out.Error != nil && out.Error.Message != "" {
		// 服务端拒绝（含超限与 purpose 顺序错）：原文照报，用户看得见才知道去改哪。
		return "", fmt.Errorf("%s: %s", i18n.T("files.err.rejected"), out.Error.Message)
	}
	if resp.StatusCode != http.StatusOK || out.ID == "" {
		return "", fmt.Errorf("%s: HTTP %d", i18n.T("files.err.bad_status"), resp.StatusCode)
	}
	return out.ID, nil
}

// checkSize 超限明确报错（D315 决策 6）：静默回落会让「扫描件没解出来」伪装成「模型没看到」。
func checkSize(mime string, n int) error {
	limit, what := MaxDocBytes, i18n.T("files.limit.doc")
	if strings.HasPrefix(mime, "image/") {
		limit, what = MaxImageBytes, i18n.T("files.limit.image")
	}
	if n > limit {
		return fmt.Errorf("%s", i18n.Tf("files.err.too_large", "size", n, "limit", limit, "what", what))
	}
	return nil
}

// buildMultipart 组请求体：purpose / expires_after 两个字段都在 file 之前（服务端要求）。
func buildMultipart(name string, data []byte, purpose string, ttl time.Duration) (io.Reader, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("purpose", purpose); err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("files.err.build_request"), err)
	}
	if ttl > 0 {
		// expires_after 的形状是两个字段：anchor 固定 created_at，seconds = 保留秒数。
		if err := mw.WriteField("expires_after[anchor]", "created_at"); err != nil {
			return nil, "", fmt.Errorf("%s: %w", i18n.T("files.err.build_request"), err)
		}
		if err := mw.WriteField("expires_after[seconds]",
			fmt.Sprintf("%d", int(ttl.Seconds()))); err != nil {
			return nil, "", fmt.Errorf("%s: %w", i18n.T("files.err.build_request"), err)
		}
	}
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("files.err.build_request"), err)
	}
	if _, err := fw.Write(data); err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("files.err.build_request"), err)
	}
	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("files.err.build_request"), err)
	}
	return &buf, mw.FormDataContentType(), nil
}
