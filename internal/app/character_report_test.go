package app

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// reportText 把回执正文拼成一段，便于子串断言。
func reportText(card port.CharacterCard, opts CharacterOptions, budget int) string {
	return strings.Join(characterReportLines(card, opts, budget), "\n")
}

// TestCharacterReportWorldBook 第一类：世界书报总数与「constant 且 enabled」的条数，
// 按开关说明注没注入（§5.17）。
func TestCharacterReportWorldBook(t *testing.T) {
	card := port.CharacterCard{
		Info: port.CharacterInfo{BookEntries: 3},
		Profile: port.CharacterProfile{BookEntries: []port.BookEntry{
			{Comment: "A", Content: "a", Constant: true, Enabled: true},
			{Comment: "B", Content: "b", Constant: true, Enabled: false},
			{Comment: "C", Content: "c", Constant: false, Enabled: true},
		}},
	}
	on := reportText(card, CharacterOptions{InjectConstantBook: true}, 0)
	if !strings.Contains(on, "共 3 条，其中 1 条常驻条目已注入人格") {
		t.Fatalf("注入态世界书行缺失/口径不对:\n%s", on)
	}
	off := reportText(card, CharacterOptions{}, 0)
	if !strings.Contains(off, "共 3 条全部保留未注入，其中 1 条是常驻条目") {
		t.Fatalf("保留态世界书行缺失/口径不对:\n%s", off)
	}
}

// TestCharacterReportAssets 第二类：图标作头像、其余资产随原包保留。图标本身计入 data.assets，
// 故「其余」要把它扣掉；没有资产也没有头像时不出这一行。
func TestCharacterReportAssets(t *testing.T) {
	withIcon := port.CharacterCard{Info: port.CharacterInfo{Assets: 3, HasAvatar: true}}
	if got := reportText(withIcon, CharacterOptions{}, 0); !strings.Contains(got, "头像") || !strings.Contains(got, "其余 2 个资产随原包保留") {
		t.Fatalf("资产行（含图标）不对:\n%s", got)
	}
	noAvatar := port.CharacterCard{Info: port.CharacterInfo{Assets: 2}}
	if got := reportText(noAvatar, CharacterOptions{}, 0); !strings.Contains(got, "其余 2 个资产") {
		t.Fatalf("资产行（无图标）不对:\n%s", got)
	}
	none := port.CharacterCard{}
	if got := reportText(none, CharacterOptions{}, 0); strings.Contains(got, "资产：") {
		t.Fatalf("没有资产时不该出资产行:\n%s", got)
	}
}

// TestCharacterReportUnsupported 第三类：不执行的机制明说保留、不运行。
func TestCharacterReportUnsupported(t *testing.T) {
	card := port.CharacterCard{Info: port.CharacterInfo{
		Unsupported: []string{"regex_scripts", "depth_prompt"},
	}}
	got := reportText(card, CharacterOptions{}, 0)
	if !strings.Contains(got, "regex_scripts、depth_prompt") || !strings.Contains(got, "本版一律不运行") {
		t.Fatalf("不执行机制行不对:\n%s", got)
	}
	card.Info.Unsupported = nil
	if got := reportText(card, CharacterOptions{}, 0); strings.Contains(got, "不执行的机制") {
		t.Fatalf("没有不执行机制时不该出这一行:\n%s", got)
	}
}

// TestCharacterReportDisplayFields 第四类：只展示字段固定说明一条，任何卡都有。
func TestCharacterReportDisplayFields(t *testing.T) {
	got := reportText(port.CharacterCard{}, CharacterOptions{}, 0)
	if !strings.Contains(got, "只展示字段：") || !strings.Contains(got, "不进模型上下文") {
		t.Fatalf("只展示字段行缺失:\n%s", got)
	}
}

// TestCharacterReportHTML 界面卡降级提示：投影字段含 HTML 块时提示，卡方言标签（<START> 等）
// 不误报。
func TestCharacterReportHTML(t *testing.T) {
	withHTML := port.CharacterCard{Profile: port.CharacterProfile{
		Description: "<div style=\"x\">正文</div>",
	}}
	if got := reportText(withHTML, CharacterOptions{}, 0); !strings.Contains(got, "HTML 标签") || !strings.Contains(got, "按纯文本显示") {
		t.Fatalf("HTML 提示缺失:\n%s", got)
	}
	cardDialect := port.CharacterCard{Profile: port.CharacterProfile{
		Examples: "<START>\n<BOT>: hi\n<USER>: yo",
	}}
	if got := reportText(cardDialect, CharacterOptions{}, 0); strings.Contains(got, "HTML 标签") {
		t.Fatalf("卡方言标签误报为 HTML:\n%s", got)
	}
}

// TestCharacterReportOpeningHint 超长开场白提示：估算超预算时给 token 与序号；短开场不给。
func TestCharacterReportOpeningHint(t *testing.T) {
	long := port.CharacterCard{Profile: port.CharacterProfile{
		FirstMes: strings.Repeat("a", 400), // ≈100 tokens
	}}
	got := reportText(long, CharacterOptions{}, 10)
	if !strings.Contains(got, "开场白偏长：第 1 条约") || !strings.Contains(got, "不截断") {
		t.Fatalf("超长开场白提示缺失:\n%s", got)
	}
	short := port.CharacterCard{Profile: port.CharacterProfile{FirstMes: "hi"}}
	if got := reportText(short, CharacterOptions{}, 10); strings.Contains(got, "开场白偏长") {
		t.Fatalf("短开场不该提示:\n%s", got)
	}
	if got := reportText(long, CharacterOptions{}, 0); strings.Contains(got, "开场白偏长") {
		t.Fatalf("预算为 0 时不该判开场白长度:\n%s", got)
	}
}
