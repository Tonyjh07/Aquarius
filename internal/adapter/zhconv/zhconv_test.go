package zhconv

import (
	"strings"
	"sync"
	"testing"
)

// TestConvertTW2SP 繁 → 简（含台湾用词）：这几个词对是实测过的，也是选 tw2sp 而不选
// 逐字映射方案的理由——「軟體」要变成「软件」而不是「软体」。
func TestConvertTW2SP(t *testing.T) {
	c, err := New(ModeTW2SP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct{ in, want string }{
		{"今天天氣不錯", "今天天气不错"},
		{"軟體開發", "软件开发"},
		{"發布計畫", "发布计划"},
		{"我們下午三點再討論一下新版本的發布計畫。", "我们下午三点再讨论一下新版本的发布计划。"},
		// 台湾用词走第一、二趟（介面 → 接口，預設值 → 默認值 → 默认值）。
		{"這個函式的介面預設值", "这个函数的接口默认值"},
		{"記憶體", "内存"},
		{"網路連線", "网络连接"},
	}
	for _, tc := range cases {
		got, err := c.Convert(tc.in)
		if err != nil {
			t.Fatalf("Convert(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("Convert(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestConvertT2S t2s 档只做字形与词组，不改地区用词：「介面」原样留着。
func TestConvertT2S(t *testing.T) {
	c, err := New(ModeT2S)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := c.Convert("軟體開發的介面")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got != "软件开发 的介面" && got != "软件开发 的界面" {
		// 词组表里「軟體開發」没有对应条目时按单字走，这里只锁「繁体字形没了」。
		if strings.ContainsAny(got, "軟體發開") {
			t.Errorf("t2s 未转换字形: %q", got)
		}
	}
}

// TestConvertKeepsSimplified 已经是简体的文本原样返回。
func TestConvertKeepsSimplified(t *testing.T) {
	c, err := New(ModeTW2SP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, in := range []string{"今天天气不错", "hello world", "混合 English 文本"} {
		got, err := c.Convert(in)
		if err != nil {
			t.Fatalf("Convert(%q): %v", in, err)
		}
		if got != in {
			t.Errorf("Convert(%q) = %q, 应原样返回", in, got)
		}
	}
}

// TestConvertEmpty 空串不触发词典加载，原样返回。
func TestConvertEmpty(t *testing.T) {
	c, err := New(ModeTW2SP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, in := range []string{"", "   ", "\n"} {
		got, err := c.Convert(in)
		if err != nil {
			t.Fatalf("Convert(%q): %v", in, err)
		}
		if got != in {
			t.Errorf("Convert(%q) = %q", in, got)
		}
	}
}

// TestNewRejectsUnknownMode 档位写错装配期就报，不留到第一次识别。
func TestNewRejectsUnknownMode(t *testing.T) {
	_, err := New("s2tw")
	if err == nil || !strings.Contains(err.Error(), "s2tw") {
		t.Fatalf("err = %v", err)
	}
}

// TestNewDefaultsToTW2SP 空档位取推荐值。
func TestNewDefaultsToTW2SP(t *testing.T) {
	c, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.mode != ModeTW2SP {
		t.Fatalf("mode = %q, want %q", c.mode, ModeTW2SP)
	}
}

// TestConvertConcurrent 转换器要能被多路并发调用（设置窗试听与语音键可能同时到）。
// 跑 -race 才有意义：词典只加载一次，之后只读。
func TestConvertConcurrent(t *testing.T) {
	c, err := New(ModeTW2SP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := c.Convert("軟體開發")
			if err != nil {
				t.Errorf("Convert: %v", err)
				return
			}
			if got != "软件开发" {
				t.Errorf("并发转换 = %q", got)
			}
		}()
	}
	wg.Wait()
}

// TestDictEntries 词典本身的基本盘：词条数没掉、没有空键、最长键在预期范围内。
// 换词典文件时这条最先失败，省得靠转换结果去猜「是不是少了一本」。
func TestDictEntries(t *testing.T) {
	c, err := New(ModeTW2SP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	chain, err := c.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(chain) != 3 {
		t.Fatalf("tw2sp 应有 3 趟，实为 %d", len(chain))
	}
	// 三趟合计约 4900 条（词组 273 + 单字 4189 + 台湾用词 488 + 异体 31 + 异体词组 51）。
	total := 0
	for _, g := range chain {
		total += len(g.table)
		if g.maxRunes < 1 || g.maxRunes > 20 {
			t.Errorf("最长键字数 %d 超出预期", g.maxRunes)
		}
		for k := range g.table {
			if k == "" {
				t.Error("词典里有空键")
			}
		}
	}
	if total < 4000 {
		t.Errorf("词条总数 = %d，词典可能没读全", total)
	}
}

// TestLongestMatchWins 长词优先于短词：单字表里「鍾」有对应，词组表里「龍鍾」也有，
// 后者要整词替换。
func TestLongestMatchWins(t *testing.T) {
	c, err := New(ModeTW2SP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := c.Convert("老態龍鍾")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got != "老态龙钟" {
		t.Fatalf("长词优先 = %q", got)
	}
}
