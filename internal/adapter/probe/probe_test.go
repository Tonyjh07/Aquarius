package probe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestCommandProbe 命令取样：非零退出码是「取到了」，进 Status 而非 error。
func TestCommandProbe(t *testing.T) {
	p := New()
	res, err := p.Probe(context.Background(), port.WatchProbe{Kind: port.WatchCommand, Target: "echo hello"})
	if err != nil || !strings.Contains(res.Output, "hello") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if res.Status != 0 {
		t.Fatalf("status = %d, want 0", res.Status)
	}
	// 非零退出：输出照回，状态码进 Status，不当错误。
	res, err = p.Probe(context.Background(), port.WatchProbe{Kind: port.WatchCommand, Target: "exit 3"})
	if err != nil {
		t.Fatalf("非零退出不该报错: %v", err)
	}
	if res.Status != 3 {
		t.Fatalf("status = %d, want 3", res.Status)
	}
	// 空命令是调用方的错。
	if _, err := p.Probe(context.Background(), port.WatchProbe{Kind: port.WatchCommand, Target: "  "}); err == nil {
		t.Fatal("空命令应报错")
	}
}

// TestCommandProbeWorkDir 工作目录透传：kind=command 才用 workdir。
func TestCommandProbeWorkDir(t *testing.T) {
	dir := t.TempDir()
	target := "pwd"
	if os.PathSeparator == '\\' {
		target = "cd"
	}
	res, err := New().Probe(context.Background(), port.WatchProbe{
		Kind: port.WatchCommand, Target: target, WorkDir: dir,
	})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !strings.Contains(strings.ToLower(res.Output), strings.ToLower(filepath.Base(dir))) {
		t.Fatalf("output = %q, want 含 %q", res.Output, filepath.Base(dir))
	}
}

// TestFileProbe 文件取样：存在则读内容；不存在按空内容（观察「文件出现」的基线）。
func TestFileProbe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.txt")
	if err := os.WriteFile(path, []byte("ready\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := New().Probe(context.Background(), port.WatchProbe{Kind: port.WatchFile, Target: path})
	if err != nil || res.Output != "ready\n" {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	missing := filepath.Join(dir, "nope.txt")
	res, err = New().Probe(context.Background(), port.WatchProbe{Kind: port.WatchFile, Target: missing})
	if err != nil || res.Output != "" {
		t.Fatalf("缺失文件 res = %+v, err = %v（应为空内容）", res, err)
	}
}

// TestHTTPProbe GET 取样：状态码进 Status，非 2xx 也不是错误。
func TestHTTPProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	res, err := New().Probe(context.Background(), port.WatchProbe{Kind: port.WatchHTTP, Target: srv.URL})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.Status)
	}
	if !strings.Contains(res.Output, "boom") {
		t.Fatalf("output = %q", res.Output)
	}
	// 连不上是错误（取不到）。
	if _, err := New().Probe(context.Background(), port.WatchProbe{Kind: port.WatchHTTP, Target: "http://127.0.0.1:1/"}); err == nil {
		t.Fatal("连接失败应报错")
	}
}

// TestProbeUnknownKind 未声明类型报错（不猜）。
func TestProbeUnknownKind(t *testing.T) {
	if _, err := New().Probe(context.Background(), port.WatchProbe{Kind: "db"}); err == nil {
		t.Fatal("未知 kind 应报错")
	}
}

// TestProbeCancel ctx 取消照常上抛（探针不吞取消）。
func TestProbeCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target := "sleep 5"
	if os.PathSeparator == '\\' {
		target = "timeout /t 5 >nul"
	}
	_, err := New().Probe(ctx, port.WatchProbe{Kind: port.WatchCommand, Target: target})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestClipKeepsHeadAndTail 超长取样保头尾并标注总量。
func TestClipKeepsHeadAndTail(t *testing.T) {
	long := strings.Repeat("a", keepChars) + "MIDDLE" + strings.Repeat("z", keepChars)
	got := clip(long)
	if len([]rune(got)) >= len([]rune(long)) {
		t.Fatalf("未裁剪：%d chars", len([]rune(got)))
	}
	if !strings.HasPrefix(got, strings.Repeat("a", 10)) || !strings.HasSuffix(got, strings.Repeat("z", 10)) {
		t.Fatal("应保头尾")
	}
	if !strings.Contains(got, "probe output truncated") {
		t.Fatalf("缺省略标注：%q", got)
	}
}
