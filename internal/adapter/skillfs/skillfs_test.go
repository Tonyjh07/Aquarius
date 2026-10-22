package skillfs

// skillfs_test.go 覆盖发现与校验口径（D201 口径一/二/六）：目录布局、坏条目跳过、
// 同名先声明者胜、正文行号（file_read 续读要对齐）、CRLF/BOM 容忍、相对根丢弃。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// writeSkill 在 dir 下写 SKILL.md（自动建目录）。
func writeSkill(t *testing.T, dir, name, desc, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, skillFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCatalogDiscoversSkill 一级/二级目录都能命中；List 给出名字、说明、目录与来源。
func TestCatalogDiscoversSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "pdf"), "pdf", "Fill and split PDF files.", "# PDF\n\nUse pdftk.")
	writeSkill(t, filepath.Join(root, "bundle", "release"), "release", "Cut a release.", "# Release")

	c := New(Options{Dirs: []string{root}})
	list, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %+v, want 2", list)
	}
	// 根内目录名序：bundle/release 先于 pdf。
	if list[0].Name != "release" || list[1].Name != "pdf" {
		t.Fatalf("顺序 = %s, %s", list[0].Name, list[1].Name)
	}
	if list[1].Description != "Fill and split PDF files." || list[1].Source != root {
		t.Fatalf("pdf = %+v", list[1])
	}
	if list[1].Dir != filepath.Join(root, "pdf") {
		t.Fatalf("dir = %q", list[1].Dir)
	}
	if warns := c.Warnings(context.Background()); len(warns) != 0 {
		t.Fatalf("warnings = %v", warns)
	}
}

// TestCatalogSkipsInvalidEntries 坏条目跳过并记警告，其余照常可用（装坏的技能不该让助手起不来）。
func TestCatalogSkipsInvalidEntries(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "good"), "good", "A good skill.", "body")
	// 名字与目录不一致（规范要求同名，否则模型按名字找目录会错位）。
	writeSkill(t, filepath.Join(root, "mismatch"), "other", "Wrong dir.", "body")
	// 缺 description。
	writeSkill(t, filepath.Join(root, "nodesc"), "nodesc", "", "body")
	// 坏 YAML。
	bad := filepath.Join(root, "badyaml")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, skillFileName), []byte("---\nname: [unclosed\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 没有 frontmatter。
	raw := filepath.Join(root, "raw")
	if err := os.MkdirAll(raw, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(raw, skillFileName), []byte("# no frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(Options{Dirs: []string{root}})
	list, _ := c.List(context.Background())
	if len(list) != 1 || list[0].Name != "good" {
		t.Fatalf("list = %+v, want 只有 good", list)
	}
	warns := c.Warnings(context.Background())
	if len(warns) != 4 {
		t.Fatalf("warnings = %d 条：%v", len(warns), warns)
	}
	for _, w := range warns {
		if !strings.Contains(w, "已跳过") {
			t.Fatalf("警告缺原因说明：%q", w)
		}
	}
}

// TestCatalogDuplicateNameFirstRootWins 多根同名：先声明者胜，后一份记警告。
func TestCatalogDuplicateNameFirstRootWins(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(a, "same"), "same", "From root A.", "A")
	writeSkill(t, filepath.Join(b, "same"), "same", "From root B.", "B")

	c := New(Options{Dirs: []string{a, b}})
	doc, err := c.Load(context.Background(), "same")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info.Description != "From root A." || doc.Body != "A" {
		t.Fatalf("生效的是后一份：%+v", doc.Info)
	}
	warns := c.Warnings(context.Background())
	if len(warns) != 1 || !strings.Contains(warns[0], "重复") {
		t.Fatalf("warnings = %v", warns)
	}
}

// TestCatalogDepthAndNesting 超过两层不扫；技能的 references/ 里再有 SKILL.md 不重复登记。
func TestCatalogDepthAndNesting(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "deep", "a", "b"), "b", "Too deep.", "body")
	writeSkill(t, filepath.Join(root, "top"), "top", "Top level.", "body")
	// 技能目录内部：命中即停，不再下钻。
	writeSkill(t, filepath.Join(root, "top", "examples"), "examples", "Nested.", "body")

	c := New(Options{Dirs: []string{root}})
	names := c.Names()
	if len(names) != 1 || names[0] != "top" {
		t.Fatalf("names = %v, want 只有 top（超深与嵌套都不算）", names)
	}
}

// TestCatalogLoadBodyLine 正文行号与 file_read 的 1 基 offset 对齐（续读提示要用）。
func TestCatalogLoadBodyLine(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "lines")
	doc := "\ufeff---\r\nname: lines\r\ndescription: Line numbers.\r\n---\r\n\r\nfirst\r\nsecond\r\nthird\r\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, skillFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(Options{Dirs: []string{root}})
	content, err := c.Load(context.Background(), "lines")
	if err != nil {
		t.Fatal(err)
	}
	// 第 1 行 ---、2 name、3 description、4 ---、5 空行、6 first。
	if content.BodyLine != 6 {
		t.Fatalf("BodyLine = %d, want 6", content.BodyLine)
	}
	if content.Body != "first\nsecond\nthird\n" {
		t.Fatalf("body = %q（应归一为 LF 且保留正文）", content.Body)
	}
}

// TestCatalogDropsRelativeRoot 相对路径根丢弃（无 cwd 概念），空根只记警告。
func TestCatalogDropsRelativeRoot(t *testing.T) {
	c := New(Options{Dirs: []string{"skills", "", filepath.Join(t.TempDir(), "missing")}})
	if dirs := c.Dirs(); len(dirs) != 1 {
		t.Fatalf("dirs = %v, want 只保留绝对路径", dirs)
	}
	if warns := c.Warnings(context.Background()); len(warns) != 1 || !strings.Contains(warns[0], "不存在") {
		t.Fatalf("warnings = %v", warns)
	}
}

// TestCatalogLoadUnknown 未安装的名字返回 ErrSkillNotFound（工具据此给出候选）。
func TestCatalogLoadUnknown(t *testing.T) {
	c := New(Options{Dirs: []string{t.TempDir()}})
	if _, err := c.Load(context.Background(), "nope"); !errors.Is(err, port.ErrSkillNotFound) {
		t.Fatalf("err = %v, want ErrSkillNotFound", err)
	}
}

// TestCatalogOversizeSkillSkipped 超过 1 MB 的 SKILL.md 跳过（多半是误放的文件）。
func TestCatalogOversizeSkillSkipped(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "huge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat("x", maxSkillFileBytes+16)
	doc := "---\nname: huge\ndescription: Too big.\n---\n\n" + pad
	if err := os.WriteFile(filepath.Join(dir, skillFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(Options{Dirs: []string{root}})
	if names := c.Names(); len(names) != 0 {
		t.Fatalf("names = %v, want 空", names)
	}
	if warns := c.Warnings(context.Background()); len(warns) != 1 || !strings.Contains(warns[0], "1 MB") {
		t.Fatalf("warnings = %v", warns)
	}
}
