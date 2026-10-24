//go:build windows

package platform

// 附件选择框的格式清单回归。
//
// 出过的问题：README 把 pptx 写进「自动转文本」，选择框的过滤器也列着 .pptx，可摄取端
// 根本没有 pptx 解析（见 ingestfile.structuredKind）——用户选中后只拿到一条“二进制、未
// 提取”的占位，还得让模型去读一个 zip。清单一旦和解析档脱节，界面就在许诺做不到的事。

import (
	"strings"
	"testing"
)

// unsupportedDocExts 选择框不该主推的扩展名：摄取端没有解析器，只能回落二进制占位。
// legacy 二进制 Office（.doc/.xls/.ppt）同样不在档。
var unsupportedDocExts = []string{"*.pptx", "*.ppt", "*.rtf", "*.doc", "*.xls"}

// TestAttachFilterOnlyOffersParsableDocs 过滤器只列能抽出文字的格式。
func TestAttachFilterOnlyOffersParsableDocs(t *testing.T) {
	if len(attachFilter)%2 != 0 {
		t.Fatalf("过滤器应 label/pattern 成对，现有 %d 段", len(attachFilter))
	}
	docPat := ""
	for i := 0; i+1 < len(attachFilter); i += 2 {
		label, pat := attachFilter[i], attachFilter[i+1]
		if label == "文档" {
			docPat = pat
		}
		if strings.Contains(pat, "*.*") {
			continue // 「所有文件」是故意留的逃生口：确实要附的仍可选
		}
		for _, seg := range strings.Split(strings.ToLower(pat), ";") {
			for _, bad := range unsupportedDocExts {
				if strings.TrimSpace(seg) == bad {
					t.Errorf("%q 过滤器含 %s——该格式没有文本解析，选中只会得到二进制占位", label, bad)
				}
			}
		}
	}
	if docPat == "" {
		t.Fatal("找不到「文档」过滤器")
	}
	// 「文档」档要与摄取端的解析档对齐：漏掉某个已支持的格式 = 用户在框里找不到入口。
	for _, want := range []string{"*.pdf", "*.docx", "*.xlsx", "*.html", "*.txt", "*.md"} {
		found := false
		for _, seg := range strings.Split(strings.ToLower(docPat), ";") {
			if strings.TrimSpace(seg) == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("「文档」过滤器缺 %s（解析支持里有它）：%q", want, docPat)
		}
	}
}
