package tokenizer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture 文件（结构与 DeepSeek 一致的极小 BPE，期望值由官方 tokenizers 计算）。
const fixturePath = "testdata/fixture_tokenizer.json"

type vector struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

func loadVectors(t *testing.T, path string) []vector {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取向量 %s: %v", path, err)
	}
	var vs []vector
	if err := json.Unmarshal(data, &vs); err != nil {
		t.Fatalf("解析向量: %v", err)
	}
	return vs
}

// TestFixtureVectors 离线自洽：fixture 上的 Go 计数逐条等于官方 tokenizers 期望值。
func TestFixtureVectors(t *testing.T) {
	tok, err := Load(fixturePath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i, v := range loadVectors(t, "testdata/fixture_vectors.json") {
		if got := tok.Count(v.Text); got != v.Count {
			t.Errorf("向量[%d] %q: got %d, want %d", i, v.Text, got, v.Count)
		}
	}
}

// TestLoadDirAndFile 目录与文件两种加载路径等价（目录下取 tokenizer.json）。
func TestLoadDirAndFile(t *testing.T) {
	a, err := Load(fixturePath)
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	dir := t.TempDir()
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Load(dir)
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	s := "hello world"
	if a.Count(s) != b.Count(s) {
		t.Fatalf("file=%d dir=%d", a.Count(s), b.Count(s))
	}
}

// TestLoadRejects 拒绝路径/格式问题：空路径、缺失文件、非 JSON、非 BPE。
func TestLoadRejects(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Fatal("空路径应报错")
	}
	if _, err := Load("testdata/nope.json"); err == nil {
		t.Fatal("缺失文件应报错")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Fatal("非 JSON 应报错")
	}
	notBPE := filepath.Join(t.TempDir(), "unigram.json")
	if err := os.WriteFile(notBPE, []byte(`{"model":{"type":"Unigram","vocab":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(notBPE); err == nil {
		t.Fatal("非 BPE 应报错")
	}
	// 非空 normalizer 拒绝（回落估算的依据）。
	nf := filepath.Join(t.TempDir(), "nfkc.json")
	if err := os.WriteFile(nf, []byte(`{
		"normalizer": {"type":"Sequence","normalizers":[{"type":"NFKC"}]},
		"pre_tokenizer": {"type":"Sequence","pretokenizers":[]},
		"model": {"type":"BPE","vocab":{},"merges":[]}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(nf); err == nil {
		t.Fatal("非恒等 normalizer 应报错")
	}
}

// TestSplitAddedAtomic added token 原子计 1（相邻多个、与普通文本交错）。
func TestSplitAddedAtomic(t *testing.T) {
	tok, err := Load(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]int{
		"":                  0,
		"<s>":               1,
		"<s></s>":           2,
		"<s>hello</s>":      3, // 2 added + hello(1)
		"hello<s>":          2, // hello + added
		"a<s>b</s>c":        5, // a,b,c 各 1 + 2 added
		"<s>hello</s>world": 4, // 2 added + hello + world（合并链）
	}
	for in, want := range cases {
		if got := tok.Count(in); got != want {
			t.Errorf("Count(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestCountEdges 边界：纯空白、纯标点、超长重复（期望值对齐 fixture 语义）。
func TestCountEdges(t *testing.T) {
	tok, err := Load(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if n := tok.Count(strings.Repeat(" ", 20)); n != 20 {
		t.Fatalf("纯空白 = %d, want 20（无 Ġ×Ġ 合并）", n)
	}
	if n := tok.Count(strings.Repeat(".", 12)); n != 12 {
		t.Fatalf("纯标点 = %d, want 12", n)
	}
	if n := tok.Count(strings.Repeat("hello ", 1000)); n != 2000 {
		t.Fatalf("超长重复 = %d, want 2000（hello+Ġ 每单元 2）", n)
	}
}

// TestRealVectors 真实 DeepSeek tokenizer 的官方期望向量。
// 需本机提供 tokenizer.json（环境变量 AQUARIUS_TOKENIZER_JSON 指向文件或目录）；
// 缺省跳过——向量与生成脚本已入库（testdata/gen_vectors.py），有词表的环境可随时重验。
func TestRealVectors(t *testing.T) {
	path := os.Getenv("AQUARIUS_TOKENIZER_JSON")
	if path == "" {
		t.Skip("未设置 AQUARIUS_TOKENIZER_JSON（DeepSeek tokenizer.json 路径），跳过真实向量比对")
	}
	tok, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	vs := loadVectors(t, "testdata/vectors.json")
	if len(vs) < 20 {
		t.Fatalf("向量过少: %d", len(vs))
	}
	for i, v := range vs {
		if got := tok.Count(v.Text); got != v.Count {
			t.Errorf("真实向量[%d] %q: got %d, want %d", i, truncate(v.Text), got, v.Count)
		}
	}
}

func truncate(s string) string {
	if r := []rune(s); len(r) > 24 {
		return string(r[:24]) + "…"
	}
	return s
}
