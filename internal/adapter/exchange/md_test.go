package exchange

// md_test.go 人读导出（D314）的行为锁定：与 openai 同一套丢弃、角色标题、正文原样、
// html 转义与目录锚点。

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestHumanFormatsDropsMatchOpenAI md / html 与 openai 的 Dropped 逐字一致——同一个会话
// 两种导出丢的东西必须一样，否则回执在撒谎（D314 决策 1）。
func TestHumanFormatsDropsMatchOpenAI(t *testing.T) {
	src := linearConv(t)
	var openaiRep, mdRep, htmlRep = exportRep(t, src, "openai"), exportRep(t, src, "md"), exportRep(t, src, "html")
	if strings.Join(openaiRep.Dropped, "|") != strings.Join(mdRep.Dropped, "|") {
		t.Fatalf("md dropped = %v, want same as openai %v", mdRep.Dropped, openaiRep.Dropped)
	}
	if strings.Join(openaiRep.Dropped, "|") != strings.Join(htmlRep.Dropped, "|") {
		t.Fatalf("html dropped = %v, want same as openai %v", htmlRep.Dropped, openaiRep.Dropped)
	}
	if mdRep.Nodes != openaiRep.Nodes || htmlRep.Nodes != openaiRep.Nodes {
		t.Fatalf("nodes md/html = %d/%d, want %d", mdRep.Nodes, htmlRep.Nodes, openaiRep.Nodes)
	}
}

// TestMDRoleHeadingsAndRawBody md 的角色标题跟随 i18n 表，正文原样（不转义不加工）：
// 正文里的「## 不是标题」「<标签>」都按原文字节带出（D314 决策 2）。
func TestMDRoleHeadingsAndRawBody(t *testing.T) {
	src := linearConv(t)
	raw := "## 这不是标题\n<b>&原样</b>"
	if _, err := src.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: raw}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	var buf bytes.Buffer
	if _, err := exportOne(context.Background(), New(Options{}), src, "md", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"# 线性往返",
		"## " + i18n.T("exchange.md.role_system"),
		"## " + i18n.T("exchange.md.role_user"),
		"## " + i18n.T("exchange.md.role_assistant"),
		raw,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("md 缺 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "&lt;b&gt;") {
		t.Fatal("md 正文被 HTML 转义了——可粘贴性破产（D314 否决 2 的另一半）")
	}
}

// TestHTMLEscapesAndAnchors html：正文转义、换行成段，目录锚点与节 id 一一对应。
func TestHTMLEscapesAndAnchors(t *testing.T) {
	src := linearConv(t)
	if _, err := src.Append(conversation.RoleUser, []conversation.Part{
		{Kind: conversation.PartText, Text: "第一行\n第二行 <script>"},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	var buf bytes.Buffer
	if _, err := exportOne(context.Background(), New(Options{}), src, "html", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatal("html 必须转义正文（安全的下限）")
	}
	if strings.Contains(out, "<script>") {
		t.Fatal("html 不该带出未转义的标签")
	}
	anchors, sections := strings.Count(out, `href="#m`), strings.Count(out, `<h2 id="m`)
	if anchors != 4 || sections != 4 { // persona + user + assistant + 追加的 user
		t.Fatalf("锚点 %d / 节 %d, want 4/4", anchors, sections)
	}
	if !strings.Contains(out, "第一行") || !strings.Contains(out, "第二行") {
		t.Fatalf("换行分节丢了正文:\n%s", out)
	}
}

// exportRep 导出并只取回执（丢弃一致性断言用）。
func exportRep(t *testing.T, c *conversation.Conversation, format string) port.ExchangeReport {
	t.Helper()
	var buf bytes.Buffer
	rep, err := exportOne(context.Background(), New(Options{}), c, format, &buf)
	if err != nil {
		t.Fatalf("%s export: %v", format, err)
	}
	return rep
}
