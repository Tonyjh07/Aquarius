package ingestfile

// 常见文档转文本提取（Q17/D105，S2b-3）：pdf/docx/xlsx/html 各用现成纯 Go 库
// （选型表与 spike 实证见 D105）；pptx/rtf/legacy 二进制 Office 首批缓，走二进制占位。
// 统一口径：提取尽力而为——任何解析失败由调用方回落二进制占位（不让摄取整体失败）；
// 文本上限 maxDocText 截断、文件上限 maxIngestFile 不解析。

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gomutex/godocx"
	"github.com/gomutex/godocx/wml/ctypes"
	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html"
)

var (
	// maxIngestFile 结构化文档解析的文件大小上限（xlsx 经 OpenReader 全量缓冲，
	// 32MB 封顶内存）；超出不解析、回落占位。
	maxIngestFile int64 = 32 << 20
	// maxDocText 提取文本上限（截断标注沿文本类口径——全文可 file_read 取）。
	maxDocText = 64 << 10
)

// structuredKind 常见文档解析分派（D105）：按扩展名判定——OOXML 是 zip 容器、
// 内容嗅探无能为力，扩展名即分类依据；未收录格式返回 ""（走既有文本/二进制分类）。
func structuredKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		return "pdf"
	case ".html", ".htm":
		return "html"
	case ".docx":
		return "docx"
	case ".xlsx", ".xlsm":
		return "xlsx"
	}
	return ""
}

// extractStructured 分派到各格式提取器；f 已由调用方 Seek(0)（统一口径）。
// docx 走 path 复开（godocx.OpenDocument 按路径读——与入库 fd 分离，D105 后果④）。
// 返回（提取文本, 是否截断, 解析错误）。
func extractStructured(kind string, f *os.File, path string) (string, bool, error) {
	st, err := f.Stat()
	if err != nil {
		return "", false, fmt.Errorf("读取文件大小: %w", err)
	}
	if st.Size() > maxIngestFile {
		return "", false, fmt.Errorf("文件 %d 字节超过解析上限 %d 字节", st.Size(), maxIngestFile)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", false, fmt.Errorf("定位文件头: %w", err)
	}
	switch kind {
	case "pdf":
		return extractPDF(f, st.Size())
	case "docx":
		return extractDOCX(path)
	case "xlsx":
		return extractXLSX(f)
	case "html":
		return extractHTML(f)
	}
	return "", false, fmt.Errorf("未支持的解析类型 %q", kind)
}

// normalizePDFBreaks PDF 版式碎片归一（D105 后果③）：GetPlainText 按布局位置断行——
// 段内单换行折为空格（"第\n12\n章" → "第 12 章"），空行保留为段落分隔。
func normalizePDFBreaks(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			if l == "" || lines[i-1] == "" {
				b.WriteString("\n")
			} else {
				b.WriteString(" ")
			}
		}
		b.WriteString(l)
	}
	return b.String()
}

// extractPDF 逐页提取文本（ledongthuc/pdf，D105）：累积至 maxDocText 即止——页数
// 自然有界；无文本层/加密/复杂编码会返回错误，由调用方回落占位。
func extractPDF(f *os.File, size int64) (string, bool, error) {
	rd, err := pdf.NewReader(f, size)
	if err != nil {
		return "", false, err
	}
	var b strings.Builder
	truncated := false
	for i := 1; i <= rd.NumPage(); i++ {
		txt, err := rd.Page(i).GetPlainText(nil)
		if err != nil {
			return "", false, fmt.Errorf("第 %d 页提取: %w", i, err)
		}
		b.WriteString(normalizePDFBreaks(txt))
		b.WriteString("\n")
		if b.Len() > maxDocText {
			truncated = true
			break
		}
	}
	return b.String(), truncated, nil
}

// extractDOCX 提取段落文本（godocx，D105）：Body.Children → Paragraph.GetCT() →
// ParagraphChild.Run/Link → RunChild.Text；表格单元格内段落同为 Paragraph 出现。
func extractDOCX(path string) (string, bool, error) {
	root, err := godocx.OpenDocument(path)
	if err != nil {
		return "", false, err
	}
	var b strings.Builder
	truncated := false
	var walkChildren func(children []ctypes.ParagraphChild)
	walkChildren = func(children []ctypes.ParagraphChild) {
		for _, pc := range children {
			if pc.Run != nil {
				for _, rc := range pc.Run.Children {
					if rc.Text != nil {
						b.WriteString(rc.Text.Text)
					}
				}
			}
			if pc.Link != nil {
				walkChildren(pc.Link.Children) // 超链接内文字一并提取
			}
		}
	}
	for _, child := range root.Document.Body.Children {
		if child.Para == nil {
			continue
		}
		walkChildren(child.Para.GetCT().Children)
		b.WriteString("\n")
		if b.Len() > maxDocText {
			truncated = true
			break
		}
	}
	return b.String(), truncated, nil
}

// extractXLSX 按工作表顺序提取单元格文本（excelize，D105）：行内 " | " 连接；
// 空单元格由 GetRows 的紧凑行保留相对位置。
func extractXLSX(f *os.File) (string, bool, error) {
	xl, err := excelize.OpenReader(f)
	if err != nil {
		return "", false, err
	}
	defer xl.Close()
	var b strings.Builder
	truncated := false
	for _, sheet := range xl.GetSheetList() {
		rows, err := xl.GetRows(sheet)
		if err != nil {
			return "", false, fmt.Errorf("工作表 %s: %w", sheet, err)
		}
		fmt.Fprintf(&b, "〔工作表 %s〕\n", sheet)
		for _, row := range rows {
			b.WriteString(strings.Join(row, " | "))
			b.WriteString("\n")
			if b.Len() > maxDocText {
				truncated = true
				break
			}
		}
		if truncated {
			break
		}
	}
	return b.String(), truncated, nil
}

// extractHTML 剥 script/style 取文本（x/net/html，D105）：文本节点按空白归一拼接。
func extractHTML(r io.Reader) (string, bool, error) {
	root, err := html.Parse(r)
	if err != nil {
		return "", false, err
	}
	var b strings.Builder
	truncated := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if truncated {
			return
		}
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode {
			if t := strings.TrimSpace(n.Data); t != "" {
				b.WriteString(t)
				b.WriteString(" ")
				if b.Len() > maxDocText {
					truncated = true
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return b.String(), truncated, nil
}
