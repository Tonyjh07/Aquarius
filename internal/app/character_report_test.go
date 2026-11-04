package app

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
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
	if !strings.Contains(on, i18n.Tf("char.report.book_injected", "total", 3, "constant", 1)) {
		t.Fatalf("注入态世界书行缺失/口径不对:\n%s", on)
	}
	off := reportText(card, CharacterOptions{}, 0)
	if !strings.Contains(off, i18n.Tf("char.report.book_kept", "total", 3, "constant", 1)) {
		t.Fatalf("保留态世界书行缺失/口径不对:\n%s", off)
	}
}

// TestCharacterReportAssets 第二类：图标作头像、其余资产随原包保留。图标本身计入 data.assets，
// 故「其余」要把它扣掉；没有资产也没有头像时不出这一行。
func TestCharacterReportAssets(t *testing.T) {
	withIcon := port.CharacterCard{Info: port.CharacterInfo{Assets: 3, HasAvatar: true}}
	iconLine := i18n.T("char.report.assets_head") + i18n.T("char.report.assets_icon") +
		i18n.Tf("char.report.assets_extra", "n", 2)
	if got := reportText(withIcon, CharacterOptions{}, 0); !strings.Contains(got, iconLine) {
		t.Fatalf("资产行（含图标）不对:\n%s", got)
	}
	noAvatar := port.CharacterCard{Info: port.CharacterInfo{Assets: 2}}
	noIconLine := i18n.T("char.report.assets_head") + i18n.T("char.report.assets_no_icon") +
		i18n.Tf("char.report.assets_extra", "n", 2)
	if got := reportText(noAvatar, CharacterOptions{}, 0); !strings.Contains(got, noIconLine) {
		t.Fatalf("资产行（无图标）不对:\n%s", got)
	}
	none := port.CharacterCard{}
	if got := reportText(none, CharacterOptions{}, 0); strings.Contains(got, i18n.T("char.report.assets_head")) {
		t.Fatalf("没有资产时不该出资产行:\n%s", got)
	}
}

// TestCharacterReportUnsupported 第三类：不执行的机制明说保留、不运行。
func TestCharacterReportUnsupported(t *testing.T) {
	card := port.CharacterCard{Info: port.CharacterInfo{
		Unsupported: []string{"regex_scripts", "depth_prompt"},
	}}
	got := reportText(card, CharacterOptions{}, 0)
	unsupported := strings.Join([]string{"regex_scripts", "depth_prompt"}, i18n.T("char.list_join"))
	if !strings.Contains(got, i18n.Tf("char.report.unsupported", "list", unsupported)) {
		t.Fatalf("不执行机制行不对:\n%s", got)
	}
	card.Info.Unsupported = nil
	// 负向断言取模板占位符之前的前缀：整行缺席时开头一段不该出现。
	unsupportedHead := strings.SplitN(i18n.T("char.report.unsupported"), "{list}", 2)[0]
	if got := reportText(card, CharacterOptions{}, 0); strings.Contains(got, unsupportedHead) {
		t.Fatalf("没有不执行机制时不该出这一行:\n%s", got)
	}
}

// TestCharacterReportDisplayFields 第四类：只展示字段固定说明一条，任何卡都有。
func TestCharacterReportDisplayFields(t *testing.T) {
	got := reportText(port.CharacterCard{}, CharacterOptions{}, 0)
	if !strings.Contains(got, i18n.T("char.report.display_only")) {
		t.Fatalf("只展示字段行缺失:\n%s", got)
	}
}

// TestCharacterReportHTML 界面卡降级提示：投影字段含 HTML 块时提示，卡方言标签（<START> 等）
// 不误报。
func TestCharacterReportHTML(t *testing.T) {
	withHTML := port.CharacterCard{Profile: port.CharacterProfile{
		Description: "<div style=\"x\">正文</div>",
	}}
	if got := reportText(withHTML, CharacterOptions{}, 0); !strings.Contains(got, i18n.T("char.report.html")) {
		t.Fatalf("HTML 提示缺失:\n%s", got)
	}
	cardDialect := port.CharacterCard{Profile: port.CharacterProfile{
		Examples: "<START>\n<BOT>: hi\n<USER>: yo",
	}}
	if got := reportText(cardDialect, CharacterOptions{}, 0); strings.Contains(got, i18n.T("char.report.html")) {
		t.Fatalf("卡方言标签误报为 HTML:\n%s", got)
	}
}

// TestCharacterReportOpeningHint 超长开场白提示：估算超预算时给 token 与序号；短开场不给。
func TestCharacterReportOpeningHint(t *testing.T) {
	firstMes := strings.Repeat("a", 400) // ≈100 tokens
	long := port.CharacterCard{Profile: port.CharacterProfile{
		FirstMes: firstMes,
	}}
	got := reportText(long, CharacterOptions{}, 10)
	want := i18n.Tf("char.report.opening_long",
		"index", 1, "tokens", estimateTokens(firstMes), "budget", 10)
	if !strings.Contains(got, want) {
		t.Fatalf("超长开场白提示缺失:\n%s", got)
	}
	short := port.CharacterCard{Profile: port.CharacterProfile{FirstMes: "hi"}}
	// 负向断言取模板占位符之前的前缀：整行缺席时开头一段不该出现。
	openingHead := strings.SplitN(i18n.T("char.report.opening_long"), "{index}", 2)[0]
	if got := reportText(short, CharacterOptions{}, 10); strings.Contains(got, openingHead) {
		t.Fatalf("短开场不该提示:\n%s", got)
	}
	if got := reportText(long, CharacterOptions{}, 0); strings.Contains(got, openingHead) {
		t.Fatalf("预算为 0 时不该判开场白长度:\n%s", got)
	}
}
