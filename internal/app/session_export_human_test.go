package app

// session_export_human_test.go 人读导出（md / html，D314）的命令面行为：
// --format md/html 被认、回执带保真说明、文件后缀与 -2 规则走注册表。
// （writer 本体在 adapter/exchange 的 golden 与单测里锁。）

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestExportHumanFormatsReceipt md / html：保真行（Lossy 格式要说明丢什么）与缺省文件名
// 的后缀都从格式表来（注册表是唯一权威，命令面零特判）。
func TestExportHumanFormatsReceipt(t *testing.T) {
	for _, tc := range []struct {
		format, ext string
	}{
		{"md", ".md"},
		{"html", ".html"},
	} {
		ex := &fakeExchange{payload: "人读内容"}
		s, dir := exportSession(t, ex)
		out, err := s.execExport(context.Background(), port.Command{Args: []string{"--format", tc.format}})
		if err != nil {
			t.Fatalf("%s export: %v", tc.format, err)
		}
		if len(ex.exported) != 1 || ex.exported[0] != tc.format {
			t.Fatalf("导出格式 = %v, want [%s]", ex.exported, tc.format)
		}
		f, _ := formatOf(t, tc.format)
		if !strings.Contains(out, fidelityLine(f)) {
			t.Fatalf("有损格式应写明丢什么:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(dir, port.FileStem(s.cur.Title)+tc.ext)); err != nil {
			t.Fatalf("缺省落点应是 %s 后缀: %v", tc.ext, err)
		}
	}
}

// TestExportHumanFormatsNoOverwrite 同名不覆盖：-2 后缀对 md 同样成立（通用规则，无特判）。
func TestExportHumanFormatsNoOverwrite(t *testing.T) {
	ex := &fakeExchange{payload: "md"}
	s, dir := exportSession(t, ex)
	if _, err := s.execExport(context.Background(), port.Command{Args: []string{"--format", "md"}}); err != nil {
		t.Fatalf("export#1: %v", err)
	}
	if _, err := s.execExport(context.Background(), port.Command{Args: []string{"--format", "md"}}); err != nil {
		t.Fatalf("export#2: %v", err)
	}
	stem := port.FileStem(s.cur.Title)
	if _, err := os.Stat(filepath.Join(dir, stem+"-2.md")); err != nil {
		t.Fatalf("第二份应是 -2 后缀: %v", err)
	}
}

// TestExportAllRejectsHumanFormats 非 aqx 与 --all 一起用就报错——md / html 不例外
// （契约「多会话只对 aqx 成立」不变，D314 决策 5）。
func TestExportAllRejectsHumanFormats(t *testing.T) {
	for _, format := range []string{"md", "html"} {
		ex := &fakeExchange{}
		s, _ := exportSession(t, ex)
		_, err := s.execExport(context.Background(), port.Command{
			Args: []string{"--all", "--format", format},
		})
		if err == nil || !strings.Contains(err.Error(),
			i18n.Tf("exchange.format_single", "format", format)) {
			t.Fatalf("%s + --all 应被拒: %v", format, err)
		}
		if len(ex.exported) != 0 {
			t.Fatalf("报错时不该走到导出: %v", ex.exported)
		}
	}
}
