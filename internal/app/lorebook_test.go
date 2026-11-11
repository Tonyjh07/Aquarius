package app

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// loreOn 缺省开着的激活口径（预算按给定上限 1.0 的比例算，测试里好算）。
func loreOn(depth int, ratio float64) LoreOptions {
	return LoreOptions{Enabled: true, ScanDepth: depth, BudgetRatio: ratio}
}

// TestActivateLoreMatchesKeys 命中判定：任一主触发词出现即命中；大小写默认不敏感，
// CaseSensitive 时区分。
func TestActivateLoreMatchesKeys(t *testing.T) {
	entries := []port.BookEntry{
		{Comment: "灰塔", Content: "守门人叫 Old Marn", Keys: []string{"灰塔"}, Enabled: true},
		{Comment: "港口", Content: "港口在南边", Keys: []string{"Harbor"}, Enabled: true},
		{Comment: "严格", Content: "大小写敏感", Keys: []string{"Tower"}, Enabled: true, CaseSensitive: true},
	}
	act := ActivateLore(entries, []string{"我们去灰塔看看"}, loreOn(4, 1.0), 10000, nil)
	if len(act.Entries) != 1 || act.Entries[0].Comment != "灰塔" {
		t.Fatalf("entries = %+v, want 灰塔 only", act.Entries)
	}
	act = ActivateLore(entries, []string{"the harbor is quiet"}, loreOn(4, 1.0), 10000, nil)
	if len(act.Entries) != 1 || act.Entries[0].Comment != "港口" {
		t.Fatalf("entries = %+v, want 港口 only", act.Entries)
	}
	act = ActivateLore(entries, []string{"the tower is tall"}, loreOn(4, 1.0), 10000, nil)
	if len(act.Entries) != 0 {
		t.Fatalf("case-sensitive entry matched lowercase: %+v", act.Entries)
	}
	act = ActivateLore(entries, []string{"the Tower is tall"}, loreOn(4, 1.0), 10000, nil)
	if len(act.Entries) != 1 || act.Entries[0].Comment != "严格" {
		t.Fatalf("entries = %+v, want 严格", act.Entries)
	}
}

// TestActivateLoreExpandsKeys `keys` 里的宏先展开再匹配（否则拿 {{user}} 字面量去比）。
func TestActivateLoreExpandsKeys(t *testing.T) {
	entries := []port.BookEntry{
		{Content: "你是船长", Keys: []string{"{{user}} 的船"}, Enabled: true},
	}
	expand := func(s string) string { return strings.ReplaceAll(s, "{{user}}", "Aria") }
	act := ActivateLore(entries, []string{"Aria 的船搁浅了"}, loreOn(4, 1.0), 10000, expand)
	if len(act.Entries) != 1 {
		t.Fatalf("entries = %+v, want one hit", act.Entries)
	}
	if act.Text != "你是船长" {
		t.Fatalf("text = %q", act.Text)
	}
	// 未展开时不该命中：字面量 {{user}} 不在正文里。
	act = ActivateLore(entries, []string{"Aria 的船搁浅了"}, loreOn(4, 1.0), 10000, nil)
	if len(act.Entries) != 0 {
		t.Fatalf("literal key matched: %+v", act.Entries)
	}
}

// TestActivateLoreSkipsConstantAndDisabled 常驻条目归 @char.book，禁用条目不激活；
// 空触发词的条目永不命中。
func TestActivateLoreSkipsConstantAndDisabled(t *testing.T) {
	entries := []port.BookEntry{
		{Comment: "常驻", Content: "每轮都在", Keys: []string{"灰塔"}, Constant: true, Enabled: true},
		{Comment: "禁用", Content: "关掉了", Keys: []string{"灰塔"}, Enabled: false},
		{Comment: "无键", Content: "没有触发词", Enabled: true},
		{Comment: "空白键", Content: "空串", Keys: []string{"  "}, Enabled: true},
	}
	act := ActivateLore(entries, []string{"灰塔"}, loreOn(4, 1.0), 10000, nil)
	if !act.empty() {
		t.Fatalf("entries = %+v, want none", act.Entries)
	}
	if act.Scanned != 2 { // 只有「无键」与「空白键」参与检索（其余两条被筛掉）
		t.Fatalf("scanned = %d, want 2", act.Scanned)
	}
}

// TestActivateLoreOrderAndBudget 预算淘汰：按 Order 降序装，装不下的整条丢弃并计数。
func TestActivateLoreOrderAndBudget(t *testing.T) {
	long := strings.Repeat("设定", 200) // 每条都在 100+ token
	entries := []port.BookEntry{
		{Comment: "低", Content: long, Keys: []string{"塔"}, Order: 10, Enabled: true},
		{Comment: "高", Content: long, Keys: []string{"塔"}, Order: 90, Enabled: true},
		{Comment: "中", Content: long, Keys: []string{"塔"}, Order: 50, Enabled: true},
	}
	act := ActivateLore(entries, []string{"塔"}, loreOn(4, 1.0), 300, nil)
	if act.Hits != 3 {
		t.Fatalf("hits = %d, want 3", act.Hits)
	}
	if len(act.Entries) == 0 {
		t.Fatalf("budget dropped everything: %+v", act)
	}
	if act.Entries[0].Comment != "高" {
		t.Fatalf("first entry = %q, want 高", act.Entries[0].Comment)
	}
	if act.Dropped+len(act.Entries) != 3 {
		t.Fatalf("dropped %d + kept %d != 3", act.Dropped, len(act.Entries))
	}
	for i := 1; i < len(act.Entries); i++ {
		if act.Entries[i-1].Order < act.Entries[i].Order {
			t.Fatalf("order not descending: %v", act.Entries)
		}
	}
	// 预算 0：一条都不发，但仍报命中，`/lore` 才能说清是被预算挡掉的。
	act = ActivateLore(entries, []string{"塔"}, loreOn(4, 1.0), 0, nil)
	if !act.empty() || act.Hits != 3 || act.Dropped != 3 {
		t.Fatalf("zero budget: %+v", act)
	}
}

// TestActivateLoreSameOrderKeepsCardOrder 同 Order 时保持卡内顺序，输出逐字节稳定。
func TestActivateLoreSameOrderKeepsCardOrder(t *testing.T) {
	entries := []port.BookEntry{
		{Comment: "A", Content: "a", Keys: []string{"塔"}, Enabled: true},
		{Comment: "B", Content: "b", Keys: []string{"塔"}, Enabled: true},
		{Comment: "C", Content: "c", Keys: []string{"塔"}, Enabled: true},
	}
	act := ActivateLore(entries, []string{"塔"}, loreOn(4, 1.0), 10000, nil)
	got := []string{act.Entries[0].Comment, act.Entries[1].Comment, act.Entries[2].Comment}
	if got[0] != "A" || got[1] != "B" || got[2] != "C" {
		t.Fatalf("order = %v, want A B C", got)
	}
	if act.Text != "## A\na\n\n## B\nb\n\n## C\nc" {
		t.Fatalf("text = %q", act.Text)
	}
}

// TestActivateLoreOff 关掉时一条都不出，也不扫描。
func TestActivateLoreOff(t *testing.T) {
	entries := []port.BookEntry{{Content: "x", Keys: []string{"塔"}, Enabled: true}}
	act := ActivateLore(entries, []string{"塔"}, LoreOptions{}, 10000, nil)
	if !act.empty() || act.Scanned != 0 {
		t.Fatalf("off = %+v", act)
	}
}

// TestLoreScanTexts 只取用户与助手的正文：system / 注入 / 工具输出 / 思考 / 文档都不算，
// 音频转写算。
func TestLoreScanTexts(t *testing.T) {
	mk := func(role conversation.Role, parts ...conversation.Part) conversation.Message {
		return conversation.Message{Role: role, Content: parts}
	}
	path := []conversation.Message{
		mk(conversation.RoleSystem, conversation.Part{Kind: conversation.PartText, Text: "persona"}),
		mk(conversation.RoleUser, conversation.Part{Kind: conversation.PartText, Text: "第一句"}),
		mk(conversation.RoleInject, conversation.Part{Kind: conversation.PartText, Text: "注入的宿主事实"}),
		mk(conversation.RoleAssistant,
			conversation.Part{Kind: conversation.PartThinking, Text: "想过灰塔"},
			conversation.Part{Kind: conversation.PartText, Text: "答第一句"}),
		mk(conversation.RoleUser, conversation.Part{Kind: conversation.PartDoc, Text: "附件全文"}),
		mk(conversation.RoleAssistant,
			conversation.Part{Kind: conversation.PartTool, Tool: &conversation.ToolPart{Name: "file_read", Result: nil}},
			conversation.Part{Kind: conversation.PartText, Text: "答第二句"}),
		mk(conversation.RoleUser, conversation.Part{Kind: conversation.PartAudio, Transcript: "语音说的话"}),
	}
	got := loreScanTexts(path, 3)
	want := []string{"答第一句", "答第二句", "语音说的话"}
	if len(got) != len(want) {
		t.Fatalf("scan = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scan[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// depth 截到最近一条。
	got = loreScanTexts(path, 1)
	if len(got) != 1 || got[0] != "语音说的话" {
		t.Fatalf("depth 1 = %q", got)
	}
	if loreScanTexts(path, 0) != nil {
		t.Fatalf("depth 0 should scan nothing")
	}
}
