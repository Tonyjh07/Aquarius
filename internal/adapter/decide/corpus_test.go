package decide

// 回归基准（D290 决策 8）：46 条工具调用语料，每条记录一次真实判定的三个分数与结论。
//
// 两个用途，都很具体：
//   - 改适配器（题面之外的部分：解析、state 排版、超时）前后各跑一轮，判定不该漂；
//   - 换模型时先跑它，看漂了多少再决定阈值怎么挪。
//
// 需要真端点，所以默认跳过：设 DECIDE_ENDPOINT（如 http://127.0.0.1:11434）才跑，
// DECIDE_MODEL 缺省 clef-flash。`-update` 重写基准——那是一次**有意的**接受漂移，
// 提交时要能说清为什么。
//
// 阈值用默认值：这份基准判的是「按出厂设置，这批调用会落到哪一档」，换阈值就要重录。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// updateCorpus `-decide.update` 重写基准文件。
var updateCorpus = flag.Bool("decide.update", false, "rewrite testdata/corpus.golden.json")

// corpusCase 一条语料。字段口径与 temp/p4probe 的标定语料一致。
type corpusCase struct {
	ID     string `json:"id"`
	Expect string `json:"expect"` // allow | ask | deny：编语料时的先验判断
	Origin string `json:"origin"`
	Tool   string `json:"tool"`
	Op     string `json:"op"`
	Target string `json:"target"`
	Risk   string `json:"risk"`
	Task   string `json:"task"`
}

// corpusGolden 一条基准记录。
type corpusGolden struct {
	ID      string  `json:"id"`
	Expect  string  `json:"expect"`
	Verdict string  `json:"verdict"`
	Risk    float64 `json:"risk"`
	Human   float64 `json:"human"`
	Allow   float64 `json:"allow"`
}

// loadCorpus 读语料。
func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "corpus.json"))
	if err != nil {
		t.Fatalf("读语料: %v", err)
	}
	var cases []corpusCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("解析语料: %v", err)
	}
	return cases
}

// TestCorpusIsWellFormed 语料本身的形状（不需要端点）：46 条、id 唯一、三档都有。
func TestCorpusIsWellFormed(t *testing.T) {
	t.Parallel()
	cases := loadCorpus(t)
	if len(cases) != 46 {
		t.Fatalf("语料 %d 条, want 46", len(cases))
	}
	seen := map[string]bool{}
	count := map[string]int{}
	for _, c := range cases {
		if seen[c.ID] {
			t.Fatalf("id 重复: %s", c.ID)
		}
		seen[c.ID] = true
		switch c.Expect {
		case "allow", "ask", "deny":
			count[c.Expect]++
		default:
			t.Fatalf("%s: expect=%q", c.ID, c.Expect)
		}
		if c.Target == "" || c.Tool == "" {
			t.Fatalf("%s: 缺 tool/target", c.ID)
		}
	}
	for _, k := range []string{"allow", "ask", "deny"} {
		if count[k] < 8 {
			t.Fatalf("%s 只有 %d 条，三档要都有足够样本", k, count[k])
		}
	}
}

// TestCorpusBaseline 拿真端点跑一轮，比对判定与分数。
func TestCorpusBaseline(t *testing.T) {
	endpoint := os.Getenv("DECIDE_ENDPOINT")
	if endpoint == "" {
		t.Skip("未设 DECIDE_ENDPOINT，跳过（需要真端点）")
	}
	model := os.Getenv("DECIDE_MODEL")
	if model == "" {
		model = "clef-flash"
	}
	update := *updateCorpus

	c, err := New(Options{BaseURL: endpoint, Model: model, TaskChars: 80})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	policy := decorate.DecidePolicy{
		Mode: decorate.DecideOn, Timeout: 30 * time.Second,
		AllowRiskBelow:  decorate.DefaultAllowRiskBelow,
		AllowHumanBelow: decorate.DefaultAllowHumanBelow,
	}
	cases := loadCorpus(t)
	got := make([]corpusGolden, 0, len(cases))
	start := time.Now()
	for _, k := range cases {
		d, err := c.Decide(context.Background(), port.DecideRequest{
			Origin: k.Origin, Tool: k.Tool, Op: k.Op,
			Target: k.Target, Risk: k.Risk, Task: k.Task,
		})
		if err != nil {
			t.Fatalf("%s: %v", k.ID, err)
		}
		got = append(got, corpusGolden{
			ID: k.ID, Expect: k.Expect, Verdict: string(policy.Judge(d)),
			Risk: d.Risk, Human: d.NeedsHuman, Allow: d.ShouldAllow,
		})
	}
	t.Logf("%d 条，用时 %v", len(got), time.Since(start).Round(time.Second))

	path := filepath.Join("testdata", "corpus.golden.json")
	if update {
		raw, err := json.MarshalIndent(got, "", " ")
		if err != nil {
			t.Fatalf("序列化: %v", err)
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("写基准: %v", err)
		}
		t.Logf("已重写 %s", path)
		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读基准（先用 -update 生成）: %v", err)
	}
	var want []corpusGolden
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("解析基准: %v", err)
	}
	byID := make(map[string]corpusGolden, len(want))
	for _, w := range want {
		byID[w.ID] = w
	}

	var drift []string
	for _, g := range got {
		w, ok := byID[g.ID]
		if !ok {
			drift = append(drift, fmt.Sprintf("%s: 基准里没有这一条", g.ID))
			continue
		}
		if w.Verdict != g.Verdict {
			drift = append(drift,
				fmt.Sprintf("%s: %s -> %s (risk %.2f -> %.2f, human %.2f -> %.2f)",
					g.ID, w.Verdict, g.Verdict, w.Risk, g.Risk, w.Human, g.Human))
		}
	}
	reportCorpus(t, got)
	if len(drift) > 0 {
		sort.Strings(drift)
		t.Fatalf("判定漂移 %d 条：\n  %s", len(drift), joinLines(drift))
	}
}

// reportCorpus 按语料标签统计放行率，写进日志——数值比逐条 diff 更容易看出方向。
func reportCorpus(t *testing.T, rows []corpusGolden) {
	t.Helper()
	for _, label := range []string{"allow", "ask", "deny"} {
		var n, allowed int
		for _, r := range rows {
			if r.Expect != label {
				continue
			}
			n++
			if r.Verdict == string(decorate.VerdictAllow) {
				allowed++
			}
		}
		if n == 0 {
			continue
		}
		t.Logf("%-5s n=%-3d 放行 %d/%d", label, n, allowed, n)
	}
}

func joinLines(s []string) string {
	out := ""
	for _, l := range s {
		out += l + "\n  "
	}
	return out
}
