// Package zhconv 把繁体中文转成简体。
//
// 本地语音识别（whisper 系列模型）对中文默认输出繁体：同一段普通话，模型给的
// 是「今天天氣不錯」。这是模型的训练数据决定的，不是识别错误。要给大陆用户看
// 简体，就得在识别之后过一道转换。
//
// 用的是 OpenCC 的词典（Apache-2.0，见 dict/ 下的文件与 THIRD-PARTY-NOTICES.md）
// 和它 tw2sp 档的转换链：「繁体 → 简体（含词组与台湾用词）」，「軟體開發」能正确
// 变成「软件开发」。只做逐字映射的方案（如 ftz）会把「軟體」译成「软体」，不够用。
//
// 转换算法自己写（最长前缀匹配，三趟链）：上游的 Go 移植（github.com/liuzl/gocc）
// 走 github.com/liuzl/da → github.com/liuzl/cedar-go，而后者是 GPL-2.0——静态链进
// 单二进制会把整份交付物拖进 GPL。词典本身是 Apache-2.0，单独取来用没问题。
package zhconv

import (
	"embed"
	"fmt"
	"strings"
	"sync"
)

// 转换档位（OpenCC 的转换链名）。
const (
	// ModeTW2SP 繁体 → 简体，含台湾用词与词组（推荐：语音识别输出走这一档）。
	ModeTW2SP = "tw2sp"
	// ModeT2S 繁体 → 简体，只做字形与词组，不改地区用词。
	ModeT2S = "t2s"
)

//go:embed dict/*.txt
var dictFS embed.FS

// 词典文件名（OpenCC 的原始文件名，内容未改）。
const (
	dictTSPhrases            = "dict/TSPhrases.txt"
	dictTSCharacters         = "dict/TSCharacters.txt"
	dictTWVariantsRevPhrases = "dict/TWVariantsRevPhrases.txt"
	dictTWVariantsRev        = "dict/TWVariantsRev.txt"
	dictTWPhrasesRev         = "dict/TWPhrasesRev.txt"
)

// Converter 繁 → 简转换器。可并发使用。
type Converter struct {
	mode string

	mu    sync.Mutex // 保护 chain 的惰性加载
	chain []group
}

// New 创建转换器。词典在首次转换时才加载（约几十毫秒，只发生一次），
// 这样未启用中文识别的用户不必为它付启动成本。
func New(mode string) (*Converter, error) {
	if strings.TrimSpace(mode) == "" {
		mode = ModeTW2SP
	}
	switch mode {
	case ModeTW2SP, ModeT2S:
	default:
		return nil, fmt.Errorf("繁简转换档位 %q 不认识（支持 %s / %s）", mode, ModeTW2SP, ModeT2S)
	}
	return &Converter{mode: mode}, nil
}

// Convert 把文本转成简体。空串原样返回；转换失败时返回原文与错误——
// 调用方应当把「转不了」当降级而不是失败（用户宁可见繁体，也不想丢文字）。
func (c *Converter) Convert(text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return text, nil
	}
	chain, err := c.load()
	if err != nil {
		return text, err
	}
	for _, g := range chain {
		text = g.convert(text)
	}
	return text, nil
}

// load 惰性加载转换链（只加载一次）。
func (c *Converter) load() ([]group, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.chain != nil {
		return c.chain, nil
	}
	files, err := chainFiles(c.mode)
	if err != nil {
		return nil, err
	}
	chain := make([]group, 0, len(files))
	for _, fs := range files {
		g, err := loadGroup(fs)
		if err != nil {
			return nil, fmt.Errorf("繁简转换词典加载失败: %w", err)
		}
		chain = append(chain, g)
	}
	c.chain = chain
	return chain, nil
}

// chainFiles 档位对应的转换链（与 OpenCC 的 config/<mode>.json 一致）：
// 每一趟是一个「组」，组内多本词典同时参与最长前缀匹配，组间顺序执行。
func chainFiles(mode string) ([][]string, error) {
	switch mode {
	case ModeTW2SP:
		return [][]string{
			{dictTWVariantsRevPhrases, dictTWVariantsRev},
			{dictTWPhrasesRev},
			{dictTSPhrases, dictTSCharacters},
		}, nil
	case ModeT2S:
		return [][]string{
			{dictTSPhrases, dictTSCharacters},
		}, nil
	default:
		return nil, fmt.Errorf("繁简转换档位 %q 不认识", mode)
	}
}

// group 一趟转换：若干本词典合并成一张「词 → 词」表。
//
// 合并是安全的：OpenCC 的组内各本词典键不重叠（词组表只收多字词，单字表只收单字），
// 同键时先声明的那本生效（与 OpenCC 的组语义一致）。
type group struct {
	table    map[string]string
	maxRunes int // 最长键的字数（决定每个位置往回试几档）
}

// loadGroup 读入并合并一组词典。
func loadGroup(files []string) (group, error) {
	g := group{table: map[string]string{}}
	for _, name := range files {
		data, err := dictFS.ReadFile(name)
		if err != nil {
			return group{}, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "\t")
			if !ok {
				return group{}, fmt.Errorf("%s 有一行不是「键<TAB>值」: %q", name, line)
			}
			// 值可以给多个候选（空格分隔），取第一个——与 OpenCC 的默认行为一致。
			if fields := strings.Fields(value); len(fields) > 0 {
				value = fields[0]
			}
			if _, seen := g.table[key]; seen {
				continue // 组内同键：先声明的那本生效
			}
			g.table[key] = value
			if n := len([]rune(key)); n > g.maxRunes {
				g.maxRunes = n
			}
		}
	}
	if len(g.table) == 0 {
		return group{}, fmt.Errorf("%v 一个词条都没读到", files)
	}
	return g, nil
}

// convert 一趟最长前缀匹配：每个位置试到最长键，命中就整词替换并跳过，
// 否则原样抄一个字。
func (g group) convert(s string) string {
	if s == "" || g.maxRunes == 0 {
		return s
	}
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(runes); {
		hit := 0
		limit := g.maxRunes
		if i+limit > len(runes) {
			limit = len(runes) - i
		}
		for n := limit; n >= 1; n-- {
			if v, ok := g.table[string(runes[i:i+n])]; ok {
				b.WriteString(v)
				i += n
				hit = n
				break
			}
		}
		if hit == 0 {
			b.WriteRune(runes[i])
			i++
		}
	}
	return b.String()
}
