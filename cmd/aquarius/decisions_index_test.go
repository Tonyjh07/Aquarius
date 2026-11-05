package main

// decisions.md 的内部一致性守卫：**正文里写了的决策，索引表里必有一行**。
//
// 只查这一个方向。索引表允许有正文没展开的条目（维护规则第 2 条：新增默认先写一行，够格才展开），
// 所以反方向不成立；但正文写了而索引漏了，就是一个只能靠往下翻才发现的决策——索引是「本文件
// 编号范围的准绳」（见文件头的阅读说明），漏一行等于让编号凭空消失。
//
// 这条测试是在 D277 / D279 两节真的漏了索引行之后补的：两节正文写得很完整，索引表却停在 D276。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// decisionHeadingRe 正文小节标题的编号部分：`### D265–D267 · …` 里取出 `D265–D267`。
// 只吃编号与分隔符，不碰标题正文——分隔符用哪个码位不归本测试管。
var decisionHeadingRe = regexp.MustCompile(`^###\s+(D[0-9]+(?:\s*[、–\-]\s*D?[0-9]+)*)`)

// decisionIDRe 从一段编号文本里取号：`D265`、`D265–D267`、`D269、D270`、`D272-D273`。
var decisionIDRe = regexp.MustCompile(`D(\d+)(?:\s*[–\-]\s*(?:D)?(\d+))?`)

// decisionIDs 一段文本里出现的全部决策号（区间展开；首尾倒置时取较小者为起点）。
func decisionIDs(s string) []int {
	var out []int
	for _, m := range decisionIDRe.FindAllStringSubmatch(s, -1) {
		lo := atoiOrPanic(m[1])
		if m[2] == "" {
			out = append(out, lo)
			continue
		}
		hi := atoiOrPanic(m[2])
		if hi < lo {
			lo, hi = hi, lo
		}
		for n := lo; n <= hi; n++ {
			out = append(out, n)
		}
	}
	return out
}

// atoiOrPanic 只解析正则已保证的十进制串；失败说明正则与解析脱节，属于测试自身的 bug。
func atoiOrPanic(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			panic("decision id 非数字: " + s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// TestDecisionsBodyInIndex 正文的每个 `### D…` 小节，其编号都必须出现在索引表里。
//
// 索引表 = `## 索引` 与下一个一级标题之间的文本；正文 = `## 记录` 之后。两段任一缺失直接失败——
// 章节改名时这条测试要跟着失，而不是静默扫 0 行通过。
func TestDecisionsBodyInIndex(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "docs", "decisions.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 docs/decisions.md: %v", err)
	}
	lines := strings.Split(string(raw), "\n")

	indexAt, bodyAt := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case "## 索引":
			indexAt = i
		case "## 记录":
			bodyAt = i
		}
	}
	if indexAt < 0 || bodyAt < 0 || bodyAt < indexAt {
		t.Fatalf("找不到 `## 索引` / `## 记录` 两节（index=%d body=%d）——章节改名请同步本测试", indexAt, bodyAt)
	}

	// 索引侧：整段取号即可，不按行解析表格（区间、顿号并列都只是写法差异）。
	inIndex := map[int]bool{}
	for _, n := range decisionIDs(strings.Join(lines[indexAt:bodyAt], "\n")) {
		inIndex[n] = true
	}
	// 索引表本该有几百行；个位数说明取号口径坏了（比如表格被挪进别的章节），
	// 让它失，而不是让正文漏行在空集合上「恰好通过」。
	if len(inIndex) < 100 {
		t.Fatalf("索引表只取到 %d 个决策号，明显不对——检查 `## 索引` 是否还在原位", len(inIndex))
	}

	type miss struct {
		line  int
		id    int
		title string
	}
	var missing []miss
	sections := 0
	for i := bodyAt + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "## ") {
			break // 正文结束（未来的新一级标题）
		}
		m := decisionHeadingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		sections++
		for _, id := range decisionIDs(m[1]) {
			if !inIndex[id] {
				missing = append(missing, miss{line: i + 1, id: id, title: strings.TrimSpace(line)})
			}
		}
	}
	if sections == 0 {
		t.Fatal("正文里一个小节都没扫到——标题形态改了吗？")
	}

	if len(missing) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "正文有 %d 处决策没进索引表（索引是编号范围的准绳）：\n", len(missing))
		for _, m := range missing {
			fmt.Fprintf(&b, "  D%d  docs/decisions.md:%d  %s\n", m.id, m.line, m.title)
		}
		b.WriteString("在 `## 索引` 的表格里补一行结论；正文可以没有，索引不能缺。")
		t.Fatal(b.String())
	}
}

// TestDecisionIDsParsing 守住取号口径本身：区间展开、顿号并列、区间不带第二个 D 都要认，
// 且不能把「D 盘」「Base64」这类文本当编号。
func TestDecisionIDsParsing(t *testing.T) {
	t.Parallel()
	cases := map[string][]int{
		"D277":        {277},
		"D265–D267":   {265, 266, 267},
		"D272-D273":   {272, 273},
		"D269、D270":   {269, 270},
		"D1、D2–D4":    {1, 2, 3, 4},
		"D 盘根目录":      nil,
		"Base64 编码":   nil,
		"见 D59 与 D61": {59, 61},
	}
	for in, want := range cases {
		got := decisionIDs(in)
		if len(got) != len(want) {
			t.Fatalf("decisionIDs(%q) = %v, want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("decisionIDs(%q) = %v, want %v", in, got, want)
			}
		}
	}
}
