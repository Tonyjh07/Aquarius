package toolbuiltin

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSpecsAreASCIIModelFacing 工具声明进模型上下文，按硬规则用英文（D36）。
// 逐字段检查 Name / Description / 参数描述与枚举值：出现非 ASCII 字符即失败——
// 工具描述漏进中文不会改变任何行为，只有门禁能拦住。
func TestSpecsAreASCIIModelFacing(t *testing.T) {
	for _, tl := range New(nil, nil, nil, "") {
		s := tl.Spec()
		checkASCII(t, s.Name, "name")
		checkASCII(t, s.Description, "description")
		checkSchemaASCII(t, s.Name, string(s.Schema))
	}
}

// checkASCII 断言字符串全是可打印 ASCII（换行/制表符除外）。
func checkASCII(t *testing.T, s, where string) {
	t.Helper()
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e {
			if c == '\n' || c == '\t' {
				continue
			}
			t.Fatalf("%s 含非 ASCII 字节 0x%02x（偏移 %d）：%q", where, c, i, s)
		}
	}
}

// checkSchemaASCII schema 是嵌入的 JSON 文本，逐字段取值检查（不解析结构：
// 只是防止中文字面量漏进去，保持与上面同口径）。
func checkSchemaASCII(t *testing.T, name, schema string) {
	t.Helper()
	for _, line := range strings.Split(schema, "\n") {
		if strings.Contains(line, `"description"`) || strings.Contains(line, `"enum"`) {
			checkASCII(t, line, fmt.Sprintf("%s schema", name))
		}
	}
	// schema 本身必须仍是合法 UTF-8（防写坏文件后的半截字节）。
	if !utf8.ValidString(schema) {
		t.Fatalf("%s schema is not valid UTF-8", name)
	}
}
