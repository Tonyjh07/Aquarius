package skillfs

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// namePattern 开放规范对 name 的要求：小写字母/数字，段间单个连字符，首尾非连字符。
var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// frontmatter SKILL.md 的 YAML 头（只取驱动行为的字段：其余键原样忽略，
// `allowed-tools` 等实验字段不驱动任何行为，见 D201 口径一）。
type frontmatter struct {
	Name          string `yaml:"name"`
	Description   string `yaml:"description"`
	License       string `yaml:"license"`
	Compatibility string `yaml:"compatibility"`
}

// readSkill 读取并解析一个 SKILL.md：返回 frontmatter、正文（frontmatter 之后、行尾归一
// 为 LF、未截断）与正文首行在文件里的行号（1 基，供 file_read 续读对齐）。
func readSkill(path string) (fm frontmatter, body string, bodyLine int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return fm, "", 0, fmt.Errorf("读不出 SKILL.md：%w", err)
	}
	defer f.Close()

	// 多读 1 字节用于判定超限（不把整份超大文件读进来）。
	data, err := io.ReadAll(io.LimitReader(f, maxSkillFileBytes+1))
	if err != nil {
		return fm, "", 0, fmt.Errorf("读不出 SKILL.md：%w", err)
	}
	if len(data) > maxSkillFileBytes {
		return fm, "", 0, errSkillFileTooLarge
	}
	text := normalizeText(data)

	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm, "", 0, fmt.Errorf("缺少 YAML frontmatter（文件须以 --- 开头）")
	}
	close := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			close = i
			break
		}
	}
	if close < 0 {
		return fm, "", 0, fmt.Errorf("frontmatter 未闭合（缺少结束的 ---）")
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:close], "\n")), &fm); err != nil {
		return fm, "", 0, fmt.Errorf("frontmatter 不是合法 YAML：%w", err)
	}
	fm.Name = strings.TrimSpace(fm.Name)
	fm.Description = strings.TrimSpace(fm.Description)
	if err := validateFrontmatter(fm); err != nil {
		return fm, "", 0, err
	}

	// 正文：结束分隔线之后；跳过紧接的空行，但行号继续往后续读对齐。
	start := close + 1
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	return fm, strings.Join(lines[start:], "\n"), start + 1, nil
}

// normalizeText 去 BOM、CRLF/CR 归一为 LF（Windows 上编辑过的技能文件是常态）。
func normalizeText(data []byte) string {
	s := string(data)
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// validateFrontmatter 校验必填字段（规范口径；这里只拦「模型会因此误判」的错）。
func validateFrontmatter(fm frontmatter) error {
	if fm.Name == "" {
		return fmt.Errorf("frontmatter 缺少必填的 name")
	}
	if len(fm.Name) > maxNameLen {
		return fmt.Errorf("name 超过 %d 字符", maxNameLen)
	}
	if !namePattern.MatchString(fm.Name) {
		return fmt.Errorf("name=%q 不合规（只允许小写字母、数字与单个连字符，首尾不能是连字符）", fm.Name)
	}
	if fm.Description == "" {
		return fmt.Errorf("frontmatter 缺少必填的 description（一句「做什么、什么时候用」）")
	}
	if n := utf8.RuneCountInString(fm.Description); n > maxDescLen {
		return fmt.Errorf("description 超过 %d 字符（当前 %d）", maxDescLen, n)
	}
	return nil
}
