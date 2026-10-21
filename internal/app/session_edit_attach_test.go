package app

// D188 编辑消息增删附件的内核测试：flag 解析（三组合 / 空文本 / 越界 / 非法序号）、
// 附件分片增删的作用面（追加序、drop 后序、drop-all、至少剩一片）、只对用户消息开放、
// 以及 port.TreeView.NodeAttachments 的序号口径。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestParseEditAttachArgs 附件 flag 的解析口径：可重复、与既有 flag 混用、文本可空。
func TestParseEditAttachArgs(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		attach int
		drop   []int
		all    bool
		text   string
		fail   bool
	}{
		{name: "attach 单个", args: []string{"u1", "--attach", `C:\a.png`}, attach: 1},
		{name: "attach 可重复", args: []string{"u1", "--attach", `C:\a.png`, "--attach", `C:\b.pdf`}, attach: 2},
		{name: "drop 可重复", args: []string{"u1", "--drop", "0", "--drop", "2", "x"}, drop: []int{0, 2}, text: "x"},
		{name: "drop-all 无文本", args: []string{"u1", "--drop-all"}, all: true},
		{name: "与 keep 同用", args: []string{"u1", "--keep", "--attach", `C:\a.png`, "新话"}, attach: 1, text: "新话"},
		{name: "attach 缺路径", args: []string{"u1", "--attach"}, fail: true},
		{name: "attach 空路径", args: []string{"u1", "--attach", "  "}, fail: true},
		{name: "drop 缺序号", args: []string{"u1", "--drop"}, fail: true},
		{name: "drop 非数字", args: []string{"u1", "--drop", "x"}, fail: true},
		{name: "drop 负数", args: []string{"u1", "--drop", "-1"}, fail: true},
		{name: "无附件操作且空文本", args: []string{"u1", ""}, fail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt, err := parseEditArgs(tc.args)
			if tc.fail {
				if err == nil {
					t.Fatalf("want error, got %+v", opt)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(opt.attach) != tc.attach {
				t.Fatalf("attach = %v, want %d 项", opt.attach, tc.attach)
			}
			if opt.dropAll != tc.all {
				t.Fatalf("dropAll = %v", opt.dropAll)
			}
			if len(opt.drop) != len(tc.drop) {
				t.Fatalf("drop = %v, want %v", opt.drop, tc.drop)
			}
			for i := range tc.drop {
				if opt.drop[i] != tc.drop[i] {
					t.Fatalf("drop = %v, want %v", opt.drop, tc.drop)
				}
			}
			if opt.text != tc.text {
				t.Fatalf("text = %q, want %q", opt.text, tc.text)
			}
		})
	}
}

// editFixture 带附件的用户节点：u1 = 正文 + 图片 + 文档。
// 附件走真实附件库（装配会把图片字节内联，哈希必须真实存在）；一行脚本回答供
// Fresh 修订触发的重新生成消费。
func editFixture(t *testing.T) (*Session, []conversation.Part) {
	t.Helper()
	s, _, _, blobs := newRawSession(t, textStream("收到"))
	ctx := context.Background()
	imgRef, err := blobs.Put(ctx, strings.NewReader("\x89PNG\r\n\x1a\n"+strings.Repeat("\x11", 32)),
		"image/png", "图.png")
	if err != nil {
		t.Fatalf("put image: %v", err)
	}
	docRef, err := blobs.Put(ctx, strings.NewReader("文档内容"), "text/plain", "说明.txt")
	if err != nil {
		t.Fatalf("put doc: %v", err)
	}
	c := conversation.New(conversation.ID("convE"), "编辑附件")
	root := conversation.MessageID(c.ID)
	mk := func(m conversation.Message) {
		t.Helper()
		if err := c.AppendCommitted(m); err != nil {
			t.Fatalf("commit %s: %v", m.ID, err)
		}
	}
	mk(conversation.Message{ID: "p", Parent: root, Role: conversation.RoleSystem,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "人格"}}})
	content := []conversation.Part{
		{Kind: conversation.PartText, Text: "看一下这两件"},
		{Kind: conversation.PartImage, Ref: &imgRef},
		{Kind: conversation.PartDoc, Ref: &docRef, Text: "内容"},
	}
	mk(conversation.Message{ID: "u1", Parent: "p", Role: conversation.RoleUser, Content: content})
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	s.cur = c
	s.publishTree()
	return s, content
}

// newAttachFile 造一个可摄取的小文本文件。
func newAttachFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// attachmentKinds 取节点附件分片的名字（按出现序），便于断言增删结果。
func attachmentKinds(c *conversation.Conversation, id conversation.MessageID) []string {
	m, ok := c.Find(id)
	if !ok {
		return nil
	}
	var out []string
	for _, p := range m.Content {
		if isAttachmentPart(p.Kind) && p.Ref != nil {
			out = append(out, p.Ref.Name)
		}
	}
	return out
}

// TestSessionEditAttachAppends --attach：新附件按序追加到末尾，正文照常改写。
func TestSessionEditAttachAppends(t *testing.T) {
	s, _ := editFixture(t)
	a := newAttachFile(t, "新的.txt", "补充材料")

	if _, err := handleCmd(s, "edit", "u1", "--attach", a, "改后的正文"); err != nil {
		t.Fatalf("edit --attach: %v", err)
	}
	newID := revisedID(s.cur, "u1")
	got := attachmentKinds(s.cur, newID)
	want := []string{"图.png", "说明.txt", "新的.txt"}
	if len(got) != len(want) {
		t.Fatalf("附件 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("附件 = %v, want %v（新附件应追加在末尾）", got, want)
		}
	}
	text, _ := nodeBodyTextOf(s.cur, newID)
	if text != "改后的正文" {
		t.Fatalf("正文 = %q", text)
	}
}

// TestSessionEditAttachOnlyKeepsBody --attach 无文本：正文原样，只多一个附件。
func TestSessionEditAttachOnlyKeepsBody(t *testing.T) {
	s, _ := editFixture(t)
	a := newAttachFile(t, "加的.txt", "x")

	out, err := handleCmd(s, "edit", "u1", "--attach", a)
	if err != nil {
		t.Fatalf("edit --attach（无文本）: %v", err)
	}
	if !strings.Contains(out, "新增 1 个附件") {
		t.Fatalf("回执应说明附件变化: %q", out)
	}
	newID := revisedID(s.cur, "u1")
	if text, _ := nodeBodyTextOf(s.cur, newID); text != "看一下这两件" {
		t.Fatalf("正文应保持原样，got %q", text)
	}
	if got := attachmentKinds(s.cur, newID); len(got) != 3 {
		t.Fatalf("附件 = %v, want 3 个", got)
	}
}

// TestSessionEditDrop --drop N：按附件出现序删除，正文不动，其余附件保序。
func TestSessionEditDrop(t *testing.T) {
	s, _ := editFixture(t)

	if _, err := handleCmd(s, "edit", "u1", "--drop", "0"); err != nil {
		t.Fatalf("edit --drop 0: %v", err)
	}
	newID := revisedID(s.cur, "u1")
	got := attachmentKinds(s.cur, newID)
	if len(got) != 1 || got[0] != "说明.txt" {
		t.Fatalf("附件 = %v, want 只剩说明.txt", got)
	}
	if text, _ := nodeBodyTextOf(s.cur, newID); text != "看一下这两件" {
		t.Fatalf("正文应保持原样，got %q", text)
	}
}

// TestSessionEditDropAll --drop-all：附件全删，正文保留。
func TestSessionEditDropAll(t *testing.T) {
	s, _ := editFixture(t)

	if _, err := handleCmd(s, "edit", "u1", "--drop-all"); err != nil {
		t.Fatalf("edit --drop-all: %v", err)
	}
	newID := revisedID(s.cur, "u1")
	if got := attachmentKinds(s.cur, newID); len(got) != 0 {
		t.Fatalf("附件 = %v, want 全删", got)
	}
	if text, _ := nodeBodyTextOf(s.cur, newID); text != "看一下这两件" {
		t.Fatalf("正文应保留，got %q", text)
	}
}

// TestSessionEditDropOutOfRange --drop 越界：报因且不改树。
func TestSessionEditDropOutOfRange(t *testing.T) {
	s, _ := editFixture(t)
	before := len(s.cur.Nodes)

	_, err := handleCmd(s, "edit", "u1", "--drop", "7")
	if err == nil || !strings.Contains(err.Error(), "越界") {
		t.Fatalf("err = %v, want 越界", err)
	}
	if len(s.cur.Nodes) != before {
		t.Fatal("失败的修订不应改树")
	}
}

// TestSessionEditAttachRejectsNonUser 非用户消息不带附件语义：报因。
func TestSessionEditAttachRejectsNonUser(t *testing.T) {
	s, _ := editFixture(t)
	a := newAttachFile(t, "x.txt", "x")
	c := conversation.New(conversation.ID("convE2"), "助手附件")
	root := conversation.MessageID(c.ID)
	if err := c.AppendCommitted(conversation.Message{ID: "p", Parent: root, Role: conversation.RoleSystem,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "人格"}}}); err != nil {
		t.Fatal(err)
	}
	if err := c.AppendCommitted(conversation.Message{ID: "a1", Parent: "p", Role: conversation.RoleAssistant,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "回复"}}}); err != nil {
		t.Fatal(err)
	}
	s.cur = c
	s.publishTree()

	_, err := handleCmd(s, "edit", "a1", "--attach", a, "改回复")
	if err == nil || !strings.Contains(err.Error(), "用户消息") {
		t.Fatalf("err = %v, want 只对用户消息的拒绝", err)
	}
}

// TestSessionEditDropAllKeepsAtLeastOnePart 删光附件且无正文时拒绝（不能造出空节点）。
func TestSessionEditDropAllKeepsAtLeastOnePart(t *testing.T) {
	s, _, _, _ := newRawSession(t)
	c := conversation.New(conversation.ID("convE3"), "只有附件")
	root := conversation.MessageID(c.ID)
	if err := c.AppendCommitted(conversation.Message{ID: "p", Parent: root, Role: conversation.RoleSystem,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "人格"}}}); err != nil {
		t.Fatal(err)
	}
	if err := c.AppendCommitted(conversation.Message{ID: "u1", Parent: "p", Role: conversation.RoleUser,
		Content: []conversation.Part{{Kind: conversation.PartImage,
			Ref: &conversation.BlobRef{Hash: strings.Repeat("c", 64), MIME: "image/png", Name: "图.png"}}}}); err != nil {
		t.Fatal(err)
	}
	s.cur = c
	s.publishTree()

	_, err := handleCmd(s, "edit", "u1", "--drop-all")
	if err == nil || !strings.Contains(err.Error(), "至少") {
		t.Fatalf("err = %v, want 至少剩一分的拒绝", err)
	}
}

// TestSessionEditAttachRelativePathRejected 相对路径拒绝（本产品无工作区概念）。
func TestSessionEditAttachRelativePathRejected(t *testing.T) {
	s, _ := editFixture(t)

	_, err := handleCmd(s, "edit", "u1", "--attach", "相对.txt")
	if err == nil || !strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("err = %v, want 绝对路径", err)
	}
}

// TestNodeAttachmentsView NodeAttachments 的序号口径：按附件分片出现序，跳过正文/思考/工具。
func TestNodeAttachmentsView(t *testing.T) {
	s, _ := editFixture(t)
	got := s.NodeAttachments("u1")
	if len(got) != 2 {
		t.Fatalf("NodeAttachments = %+v, want 2 项", got)
	}
	if got[0].Index != 0 || got[0].Name != "图.png" || got[0].MIME != "image/png" || got[0].Size <= 0 {
		t.Fatalf("[0] = %+v", got[0])
	}
	if got[1].Index != 1 || got[1].Name != "说明.txt" || got[1].MIME != "text/plain" {
		t.Fatalf("[1] = %+v", got[1])
	}
	if v := s.NodeAttachments("不存在"); v != nil {
		t.Fatalf("未知节点应为 nil，got %+v", v)
	}
}

// nodeBodyTextOf 节点正文（与 port.TreeView.NodeText 同口径，直接查会话树）。
func nodeBodyTextOf(c *conversation.Conversation, id conversation.MessageID) (string, bool) {
	m, ok := c.Find(id)
	if !ok {
		return "", false
	}
	return nodeBodyText(m), true
}

// 编译期断言：Session 与装配侧代理都实现了扩展后的端口。
var _ port.TreeView = (*Session)(nil)

// 保证 context 用完即弃的测试辅助不被误删（handleCmd 内部走 Handle，需 ctx）。
var _ = context.Background
