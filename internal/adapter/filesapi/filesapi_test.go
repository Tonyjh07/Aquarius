package filesapi

// filesapi_test.go DeepSeek Files API 上传适配器（D315）的形状锁定：multipart 字段序
// （purpose 先于 file）、expires_after 形状、超限与失败路径明确报因、错误不重试。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// filesServer 假服务端：记录收到的 multipart 字段序，按 wantStatus / wantBody 应答。
func filesServer(t *testing.T, wantStatus int, wantBody string, seen *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			return
		}
		// 字段序：purpose（与 expires_after）必须在 file 之前——服务端对文档类强制。
		for key := range r.MultipartForm.Value {
			*seen = append(*seen, key)
		}
		for _, f := range r.MultipartForm.File {
			*seen = append(*seen, f[0].Filename)
		}
		w.WriteHeader(wantStatus)
		fmt.Fprint(w, wantBody)
	}))
}

// TestUploadShape 字段序与鉴权：purpose 先于 file；expires_after 两段按服务端形状；
// Bearer 头带上。200 + {"id":…} → file_id 原样返回。
func TestUploadShape(t *testing.T) {
	var seen []string
	srv := filesServer(t, http.StatusOK, `{"id":"file-api-123","object":"file"}`, &seen)
	defer srv.Close()
	u := New(Options{BaseURL: srv.URL, APIKey: "sk-test"})
	id, err := u.Upload(context.Background(), "scan.pdf", "application/pdf", []byte("%PDF-1.4"),
		"text_recognition", 30*24*time.Hour)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if id != "file-api-123" {
		t.Fatalf("file_id = %q, want file-api-123", id)
	}
	// ParseMultipartForm 不保留 file 字段名，末位是文件名本身——purpose 第一、file 最后即可。
	if len(seen) < 4 || seen[0] != "purpose" || seen[len(seen)-1] != "scan.pdf" {
		t.Fatalf("字段序 = %v, want purpose 在 file 之前", seen)
	}
	if got := strings.Join(seen, ","); !strings.Contains(got, "expires_after[anchor]") ||
		!strings.Contains(got, "expires_after[seconds]") {
		t.Fatalf("缺 expires_after 字段: %v", seen)
	}
}

// TestUploadNoExpiry ttl=0 不传 expires_after（服务端永久保留是缺省行为，不替用户做决定）。
func TestUploadNoExpiry(t *testing.T) {
	var seen []string
	srv := filesServer(t, http.StatusOK, `{"id":"f1"}`, &seen)
	defer srv.Close()
	u := New(Options{BaseURL: srv.URL, APIKey: "k"})
	if _, err := u.Upload(context.Background(), "a.pdf", "application/pdf", []byte("x"),
		"text_recognition", 0); err != nil {
		t.Fatalf("upload: %v", err)
	}
	for _, key := range seen {
		if strings.HasPrefix(key, "expires_after") {
			t.Fatalf("ttl=0 不该带 expires_after: %v", seen)
		}
	}
}

// TestUploadServerError 服务端 400 + error.message：原文照报（用户看得见才知道改哪）。
func TestUploadServerError(t *testing.T) {
	var seen []string
	srv := filesServer(t, http.StatusBadRequest, `{"error":{"message":"purpose must precede file"}}`, &seen)
	defer srv.Close()
	u := New(Options{BaseURL: srv.URL, APIKey: "k"})
	_, err := u.Upload(context.Background(), "a.pdf", "application/pdf", []byte("x"),
		"user_data", 0)
	if err == nil || !strings.Contains(err.Error(), i18n.T("files.err.rejected")) ||
		!strings.Contains(err.Error(), "purpose must precede file") {
		t.Fatalf("err = %v, want 服务端拒绝原文照报", err)
	}
}

// TestUploadTooLarge 超限（文档 100 MiB / 图片 64 MiB）本地就拒，不打服务端。
func TestUploadTooLarge(t *testing.T) {
	srv := filesServer(t, http.StatusOK, `{"id":"f1"}`, &[]string{})
	defer srv.Close()
	u := New(Options{BaseURL: srv.URL, APIKey: "k"})
	big := make([]byte, MaxDocBytes+1)
	if _, err := u.Upload(context.Background(), "big.pdf", "application/pdf", big,
		"text_recognition", 0); err == nil ||
		!strings.Contains(err.Error(), i18n.Tf("files.err.too_large", "size", len(big), "limit", MaxDocBytes, "what", i18n.T("files.limit.doc"))) {
		t.Fatalf("err = %v, want 文档超限报因", err)
	}
	img := make([]byte, MaxImageBytes+1)
	if _, err := u.Upload(context.Background(), "big.png", "image/png", img,
		"text_recognition", 0); err == nil ||
		!strings.Contains(err.Error(), i18n.T("files.limit.image")) {
		t.Fatalf("err = %v, want 图片超限报因", err)
	}
}

// TestUploadEmptyPurpose purpose 空 = 配置关了，直接拒（不该把空 purpose 发给服务端）。
func TestUploadEmptyPurpose(t *testing.T) {
	u := New(Options{BaseURL: "http://127.0.0.1:1", APIKey: "k"})
	if _, err := u.Upload(context.Background(), "a.pdf", "application/pdf", []byte("x"), "", 0); err == nil ||
		!strings.Contains(err.Error(), i18n.T("files.err.purpose_empty")) {
		t.Fatalf("err = %v, want purpose 空报因", err)
	}
}
