// Package legal 打包首次运行向导第 2 页的法律正文（D278 决策 5）。
//
// **正文不进 i18n 表**：表只有当前值，条款升到 v2 后回看不到用户当初同意过的 v1；而长正文
// 塞进 Go 字符串字面量，改一句话的 diff 也没法给律师看。这里用 go:embed 把 texts/v<N>/ 下
// 的 md 原样打进二进制，按版本目录名保留历史。
//
// 边界：**只有正文走这里**。页标题、「我已阅读并同意…」的勾选句、翻页按钮、版本更新提示
// 都照旧走 i18n 表——否则就成了第二个文案来源。
package legal

import (
	"embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// Version 当前生效的条款版本，也是 texts/v<N> 的目录号。
//
// 正文任何**实质**改动都要 +1（错别字不算）：装配根拿它与 `ui.consent_version` 比，
// 版本不足的用户下次启动只重过第 2 页，其余页照常可跳。
const Version = 1

//go:embed texts/v*
var texts embed.FS

// Doc 一份文档。
type Doc string

const (
	// Terms 用户协议。
	Terms Doc = "terms"
	// Privacy 隐私政策。
	Privacy Doc = "privacy"
)

// BlockKind 正文块的类型（解析只管这四种，别的记号当普通段落原样保留）。
type BlockKind int

const (
	// Para 普通段落。
	Para BlockKind = iota
	// Tip 原文用 **…** 整段标记的提示段：加粗并配左侧色条。
	Tip
	// TableRow 表格的一行（首列是行标题，其余折行排在后面）。
	TableRow
	// Bullet `- ` 开头的列表项。
	Bullet
)

// Block 一个正文块。
type Block struct {
	Kind BlockKind
	Text string   // Para / Tip / Bullet 的正文（标记已去掉）
	Cols []string // TableRow：各列原文
	Head bool     // TableRow：表头行（加粗）
}

// Section 一节（`## ` 标题 + 其下的块）。
type Section struct {
	Title  string
	Blocks []Block
}

// Document 一份解析好的文档。Lead = 第一个节标题之前的块（版本行与开篇那段）。
type Document struct {
	Title    string
	Sections []Section
	Lead     []Block
}

// Load 取当前版本的一份正文，按 lang 选语言。
func Load(d Doc, lang i18n.Lang) (Document, error) { return LoadAt(d, lang, Version) }

// LoadAt 取指定版本（回看用户同意过的那一版用得上）。英文缺文件时回落中文母版——
// 与 i18n.T 的回落同口径：界面不会空着，测试会红。
func LoadAt(d Doc, lang i18n.Lang, v int) (Document, error) {
	name := fmt.Sprintf("texts/v%d/%s.%s.md", v, d, lang)
	raw, err := texts.ReadFile(name)
	if err != nil {
		fallback := fmt.Sprintf("texts/v%d/%s.%s.md", v, d, i18n.LangZh)
		raw, err = texts.ReadFile(fallback)
		if err != nil {
			return Document{}, fmt.Errorf("legal: %s: %w", name, err)
		}
	}
	// 版本号由代码注入：正文里只写 `{v}`，别处再手写一遍必然对不上。
	return parse(strings.ReplaceAll(string(raw), "{v}", strconv.Itoa(v)))
}
