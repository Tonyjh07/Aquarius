package logging

// 日志目录读侧（v0.9.0 日志页）的单元测试：列文件只认本包写出的前缀、按时间倒序；
// 末尾读取按行取、丢掉切进去的半行、不把整份读进来。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeLog 在 dir 下造一份文件（名字即日期，便于断言顺序）。
func writeLog(t *testing.T, dir, name, body string, mod time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写入 %s: %v", name, err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("改时间 %s: %v", name, err)
	}
	return path
}

// TestListFiltersAndOrders 只列 `aquarius-` 前缀，新的在前；同一天的正本与 `.1` 按名字定序。
func TestListFiltersAndOrders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeLog(t, dir, "aquarius-2026-11-17.log", "old\n", time.Date(2026, 11, 17, 9, 0, 0, 0, time.Local))
	writeLog(t, dir, "aquarius-2026-11-18.log", "new\n", time.Date(2026, 11, 18, 9, 0, 0, 0, time.Local))
	writeLog(t, dir, "aquarius-2026-11-18.log.1", "rotated\n", time.Date(2026, 11, 18, 8, 0, 0, 0, time.Local))
	writeLog(t, dir, "audit.log", "not ours\n", time.Date(2026, 11, 18, 10, 0, 0, 0, time.Local))
	writeLog(t, dir, "crash-20261118.txt", "not ours\n", time.Date(2026, 11, 18, 10, 0, 0, 0, time.Local))

	files, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	want := []string{"aquarius-2026-11-18.log", "aquarius-2026-11-18.log.1", "aquarius-2026-11-17.log"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("文件表 = %v, want %v", names, want)
	}
	if files[0].Size != int64(len("new\n")) {
		t.Fatalf("大小 = %d, want %d", files[0].Size, len("new\n"))
	}
}

// TestListMissingDir 目录还不存在（首次运行）= 空表而不是错误。
func TestListMissingDir(t *testing.T) {
	t.Parallel()
	files, err := List(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(files) != 0 {
		t.Fatalf("List = (%v, %v), want (空, nil)", files, err)
	}
	if files, err := List(""); err != nil || files != nil {
		t.Fatalf("空目录参数 = (%v, %v), want (nil, nil)", files, err)
	}
}

// TestTailKeepsLastLines 只取末尾 N 行，末尾空行不占位。
func TestTailKeepsLastLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeLog(t, dir, filePrefix+"2026-11-18.log", "l1\nl2\nl3\nl4\nl5\n", time.Now())
	got, err := Tail(path, 3)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if got != "l3\nl4\nl5" {
		t.Fatalf("Tail = %q", got)
	}
}

// TestTailDropsPartialFirstLine 窗口从中间切进去时首行是半行——丢掉，不冒充一条完整记录。
func TestTailDropsPartialFirstLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// 每行 8 字节以上，总长超过最小窗口（64 KiB）才会触发切窗。
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		b.WriteString("line-")
		b.WriteString(strings.Repeat("x", 20))
		b.WriteString("\n")
	}
	path := writeLog(t, dir, filePrefix+"2026-11-18.log", b.String(), time.Now())
	got, err := Tail(path, 3)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("行数 = %d, want 3（%q）", len(lines), got)
	}
	for _, ln := range lines {
		if !strings.HasPrefix(ln, "line-") || len(ln) != 25 {
			t.Fatalf("行不完整：%q", ln)
		}
	}
}

// TestTailSmallFileKeepsFirstLine 整份读进来时首行是完整的，不能被当成半行丢掉。
func TestTailSmallFileKeepsFirstLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeLog(t, dir, filePrefix+"2026-11-18.log", "first\nsecond\n", time.Now())
	got, err := Tail(path, 10)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if got != "first\nsecond" {
		t.Fatalf("Tail = %q", got)
	}
}

// TestTailCRLF Windows 上文本形态是 CRLF，读回来不该带 `\r`。
func TestTailCRLF(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeLog(t, dir, filePrefix+"2026-11-18.log", "a\r\nb\r\n", time.Now())
	got, err := Tail(path, 5)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if got != "a\nb" {
		t.Fatalf("Tail = %q", got)
	}
}

// TestTailEmptyFile 空文件返回空串，不报错。
func TestTailEmptyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeLog(t, dir, filePrefix+"2026-11-18.log", "", time.Now())
	got, err := Tail(path, 10)
	if err != nil || got != "" {
		t.Fatalf("Tail = (%q, %v)", got, err)
	}
}

// TestLogFileNameAndDir 写侧与读侧同一条命名/路径规则。
func TestLogFileNameAndDir(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 11, 18, 23, 59, 0, 0, time.Local)
	if got := LogFileName(day); got != "aquarius-2026-11-18.log" {
		t.Fatalf("LogFileName = %q", got)
	}
	if got := Dir(filepath.Join("profile")); got != filepath.Join("profile", "logs") {
		t.Fatalf("Dir = %q", got)
	}
}
