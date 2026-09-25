// Package tokenizer 实现 HuggingFace tokenizer.json 的进程内精确计数（D26②）：
// 恒等归一化 → Sequence[Split×N, ByteLevel] 预切分 → BPE 合并 → 数 token。
//
// 支持范围对齐 DeepSeek 发布的 tokenizer（LlamaTokenizerFast/BPE）：
//   - normalizer：空 Sequence（恒等）；非空一律报错（调用方回落通用估算，D26③）
//   - pre_tokenizer：Sequence[Split(Isolated)…, ByteLevel]；正则含负向前瞻，
//     故用 github.com/dlclark/regexp2（纯 Go、.NET 语义，与 Python/Rust 分词器的
//     最左优先语义一致）
//   - added_tokens：原子匹配（最长优先），每命中计 1
//   - post_processor/decoder/truncation/padding：不影响计数，忽略
//
// 计数语义 = 纯文本 encode（add_special_tokens=false），结构开销由调用方按
// 每消息常数另计（DESIGN D26：不渲染 chat_template）。
package tokenizer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
)

// Tokenizer 已加载的 BPE 分词器（加载后只读；顺序调用 Count，非并发安全）。
type Tokenizer struct {
	vocab  map[string]int
	merges map[string]int // "左 右" → 秩（越小越先合并）
	splits []*regexp2.Regexp
	chars  [256]string       // 字节 → 字节级映射字符（GPT-2 bytes_to_unicode）
	added  map[rune][]string // added token 首 rune → 候选（按字节长降序）
}

// ---------------------------------------------------------------------------
// HF json 结构（只声明计数所需字段）
// ---------------------------------------------------------------------------

type hfFile struct {
	Normalizer   *hfNormalizer `json:"normalizer"`
	PreTokenizer *hfPreTok     `json:"pre_tokenizer"`
	AddedTokens  []hfAdded     `json:"added_tokens"`
	Model        hfModel       `json:"model"`
}

type hfNormalizer struct {
	Type        string            `json:"type"`
	Normalizers []json.RawMessage `json:"normalizers"`
}

type hfPreTok struct {
	Type          string     `json:"type"`
	PreTokenizers []hfPreTok `json:"pretokenizers"`
	Pattern       hfPattern  `json:"pattern"`
}

type hfPattern struct {
	Regex string `json:"Regex"`
}

type hfAdded struct {
	ID      int    `json:"id"`
	Content string `json:"content"`
}

type hfModel struct {
	Type   string          `json:"type"`
	Vocab  map[string]int  `json:"vocab"`
	Merges json.RawMessage `json:"merges"` // "a b" 或 ["a","b"] 两种历史格式
}

// Load 加载 tokenizer.json（path 可为文件或目录）。
func Load(path string) (*Tokenizer, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("tokenizer: 路径为空")
	}
	file := path
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		file = filepath.Join(path, "tokenizer.json")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("tokenizer: 读取 %s: %w", file, err)
	}
	var hf hfFile
	if err := json.Unmarshal(data, &hf); err != nil {
		return nil, fmt.Errorf("tokenizer: 解析 %s: %w", file, err)
	}
	if hf.Model.Type != "BPE" {
		return nil, fmt.Errorf("tokenizer: 不支持的模型类型 %q（仅 BPE）", hf.Model.Type)
	}
	// normalizer：只支持恒等（缺省或空 Sequence）；其余（NFKC/Lowercase…）
	// 若静默放行会导致计数与服务端不符却仍标"精确"——一律报错回落通用估算③。
	if hf.Normalizer != nil && !(hf.Normalizer.Type == "Sequence" && len(hf.Normalizer.Normalizers) == 0) {
		return nil, fmt.Errorf("tokenizer: 不支持的 normalizer %q（仅支持恒等）", hf.Normalizer.Type)
	}
	// pre_tokenizer：Sequence[Split…, ByteLevel]。
	if hf.PreTokenizer == nil || hf.PreTokenizer.Type != "Sequence" {
		return nil, fmt.Errorf("tokenizer: 不支持的 pre_tokenizer（仅 Sequence[Split…, ByteLevel]）")
	}
	t := &Tokenizer{
		vocab:  hf.Model.Vocab,
		merges: map[string]int{},
		added:  map[rune][]string{},
	}
	chars := byteChars()
	t.chars = chars
	for i, st := range hf.PreTokenizer.PreTokenizers {
		switch st.Type {
		case "Split":
			re, cerr := regexp2.Compile(st.Pattern.Regex, 0)
			if cerr != nil {
				return nil, fmt.Errorf("tokenizer: 编译切分正则 %q: %w", st.Pattern.Regex, cerr)
			}
			t.splits = append(t.splits, re)
		case "ByteLevel":
			// 字节级映射恒在最后应用（use_regex=false：不再二次切分）。
			if i != len(hf.PreTokenizer.PreTokenizers)-1 {
				return nil, fmt.Errorf("tokenizer: ByteLevel 必须是 pre_tokenizer 末位")
			}
		default:
			return nil, fmt.Errorf("tokenizer: 不支持的预切分步骤 %q", st.Type)
		}
	}
	// merges：两种历史格式。
	var ms []json.RawMessage
	if err := json.Unmarshal(hf.Model.Merges, &ms); err != nil {
		return nil, fmt.Errorf("tokenizer: 解析 merges: %w", err)
	}
	for i, raw := range ms {
		var s string
		// 注意：JSON null 反序列化进 string 不报错且得空串——空 merge 串一律视为畸形。
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			t.merges[s] = i
			continue
		}
		var pair []string
		if err := json.Unmarshal(raw, &pair); err == nil && len(pair) == 2 {
			t.merges[pair[0]+" "+pair[1]] = i
			continue
		}
		return nil, fmt.Errorf("tokenizer: 无法解析 merges[%d]: %s", i, raw)
	}
	// added tokens：按首 rune 分桶、桶内长者优先（最长匹配）。
	for _, a := range hf.AddedTokens {
		if a.Content == "" {
			continue
		}
		r, _ := utf8.DecodeRuneInString(a.Content)
		t.added[r] = append(t.added[r], a.Content)
	}
	for r := range t.added {
		cands := t.added[r]
		sort.Slice(cands, func(i, j int) bool { return len(cands[i]) > len(cands[j]) })
	}
	return t, nil
}

// Count 统计文本 token 数（等价 add_special_tokens=false 的 encode 长度）。
func (t *Tokenizer) Count(text string) int {
	if text == "" {
		return 0
	}
	n := 0
	for _, seg := range t.splitAdded(text) {
		if seg.added {
			n++ // added token 原子计 1
			continue
		}
		n += t.countText(seg.text)
	}
	return n
}

// segment 切段：added token 原子 / 普通文本。
type segment struct {
	text  string
	added bool
}

// splitAdded 在文本中扫描 added token（最长匹配优先），切出原子段与普通段。
func (t *Tokenizer) splitAdded(s string) []segment {
	var segs []segment
	var buf strings.Builder
	i := 0
	for i < len(s) {
		r, sz := utf8.DecodeRuneInString(s[i:])
		matched := ""
		for _, c := range t.added[r] { // 桶内已按长度降序
			if strings.HasPrefix(s[i:], c) {
				matched = c
				break
			}
		}
		if matched == "" {
			buf.WriteString(s[i : i+sz])
			i += sz
			continue
		}
		if buf.Len() > 0 {
			segs = append(segs, segment{text: buf.String()})
			buf.Reset()
		}
		segs = append(segs, segment{text: matched, added: true})
		i += len(matched)
	}
	if buf.Len() > 0 {
		segs = append(segs, segment{text: buf.String()})
	}
	return segs
}

// countText 普通文本：逐级 Split 切分 → 字节级映射 → BPE → 数 token。
func (t *Tokenizer) countText(s string) int {
	pieces := []string{s}
	for _, re := range t.splits {
		next := make([]string, 0, len(pieces))
		for _, p := range pieces {
			next = append(next, splitPiece(re, p)...)
		}
		pieces = next
	}
	n := 0
	for _, p := range pieces {
		mapped := t.byteLevel(p)
		for _, sym := range t.bpe(mapped) {
			if _, ok := t.vocab[sym]; ok {
				n++
				continue
			}
			// 理论不可达（字节级单字 + 合并结果恒在词表）；按符号数兜底。
			n += utf8.RuneCountInString(sym)
		}
	}
	return n
}

// splitPiece 按正则切分（Isolated：匹配段与非匹配段都保留；空段丢弃）。
// regexp2 为最左优先（.NET）语义，与 Python/Rust 分词器一致；
// Match.Index/Length 以 rune 计（即使输入是 string），故在 rune 切片上开段。
func splitPiece(re *regexp2.Regexp, s string) []string {
	if s == "" {
		return nil
	}
	rs := []rune(s)
	var out []string
	prev := 0 // rune 偏移
	m, err := re.FindStringMatch(s)
	if err != nil { // 运行期正则错误（理论不可达）：整段透传
		return []string{s}
	}
	for m != nil {
		if m.Index > prev {
			out = append(out, string(rs[prev:m.Index]))
		}
		out = append(out, m.String())
		prev = m.Index + m.Length
		if m, err = re.FindNextMatch(m); err != nil {
			break
		}
	}
	if prev < len(rs) {
		out = append(out, string(rs[prev:]))
	}
	res := out[:0:0]
	for _, p := range out {
		if p != "" {
			res = append(res, p)
		}
	}
	return res
}

// byteLevel GPT-2 字节级映射：UTF-8 字节 → 映射字符（ByteLevel 预切分末位）。
func (t *Tokenizer) byteLevel(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteString(t.chars[s[i]])
	}
	return b.String()
}

// bpe 对已映射的符号串执行 BPE：反复合并秩最小的相邻对（一次选定的对
// 全量左→右合并，与 HF tokenizers 语义一致），直至无可合并。
func (t *Tokenizer) bpe(word string) []string {
	if word == "" {
		return nil
	}
	syms := make([]string, 0, len(word))
	for _, r := range word {
		syms = append(syms, string(r))
	}
	for len(syms) > 1 {
		bestRank, bestA, bestB := -1, "", ""
		for i := 0; i+1 < len(syms); i++ {
			rank, ok := t.merges[syms[i]+" "+syms[i+1]]
			if ok && (bestRank < 0 || rank < bestRank) {
				bestRank, bestA, bestB = rank, syms[i], syms[i+1]
			}
		}
		if bestRank < 0 {
			break
		}
		merged := make([]string, 0, len(syms))
		for i := 0; i < len(syms); {
			if i+1 < len(syms) && syms[i] == bestA && syms[i+1] == bestB {
				merged = append(merged, bestA+bestB)
				i += 2
				continue
			}
			merged = append(merged, syms[i])
			i++
		}
		syms = merged
	}
	return syms
}

// byteChars GPT-2 bytes_to_unicode 表：可打印 ASCII/Latin-1 保持原字，
// 其余按序映射到 256+n。
func byteChars() [256]string {
	var in [256]bool
	var chars [256]string
	add := func(lo, hi int) {
		for b := lo; b <= hi; b++ {
			in[b] = true
		}
	}
	add(int('!'), int('~'))
	add(0xA1, 0xAC) // ¡..¬
	add(0xAE, 0xFF) // ®..ÿ
	for b := 0; b < 256; b++ {
		if in[b] {
			chars[b] = string(rune(b))
		}
	}
	n := 256
	for b := 0; b < 256; b++ {
		if !in[b] {
			chars[b] = string(rune(n))
			n++
		}
	}
	return chars
}
