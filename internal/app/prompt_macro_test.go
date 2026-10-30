package app

import "testing"

// TestExpandMacros 卡文本宏替换（§7.7/D247）：认 `{{char}}` / `{{user}}` / `{{date}}` /
// `{{original}}` 与 `<BOT>` / `<USER>` / `<START>`；大小写不敏感、容忍括号内空格、
// 认不出的原样留、不递归。
func TestExpandMacros(t *testing.T) {
	const (
		char = "Aria"
		user = "Sam"
		date = "2026-10-18"
		orig = "USER PERSONA"
	)
	cases := []struct {
		in   string
		want string
	}{
		// 四种宏的基本形态。
		{"{{char}}", char},
		{"{{user}}", user},
		{"{{date}}", date},
		{"{{original}}", orig},
		// 大小写与空格。
		{"{{Char}} meets {{User}}", "Aria meets Sam"},
		{"{{ char }}: {{user }} / {{ date}}", "Aria: Sam / 2026-10-18"},
		// 尖括号别名（契约只认这三个）。
		{"<BOT> greets <USER>", "Aria greets Sam"},
		{"<bot> and <user>", "Aria and Sam"},
		// <START> 分块符保留本身、小写归一。
		{"a<START>b", "a<START>b"},
		{"a<start>b<START>c", "a<START>b<START>c"},
		// 认不出的原样保留（带冒号/下划线/不在宏表里的名字）。
		{"{{roll:d14}}", "{{roll:d14}}"},
		{"{{cha}}", "{{cha}}"},
		{"{{Rules}}", "{{Rules}}"},
		{"{{user_name}}", "{{user_name}}"},
		// 不是契约别名的形式不动：{{start}}、<char>、<date>。
		{"{{start}}", "{{start}}"},
		{"<char>", "<char>"},
		{"<date>", "<date>"},
		// HTML 标签原样（开场白里成规模用 HTML 的卡实测存在）。
		{"<div class=\"x\">hi</div><style>p{}</style>", "<div class=\"x\">hi</div><style>p{}</style>"},
		// 混合：能认的认、认不出的留。
		{"{{char}} rolls {{roll:d20}} at {{ Date }}", "Aria rolls {{roll:d20}} at 2026-10-18"},
	}
	for _, c := range cases {
		if got := expandMacros(c.in, char, user, date, orig); got != c.want {
			t.Errorf("expandMacros(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestExpandMacrosUserDefault {{user}} 在未配置 user_name 时取 "User"。
func TestExpandMacrosUserDefault(t *testing.T) {
	if got := expandMacros("I am {{user}}.", "Aria", "", "2026-10-18", ""); got != "I am User." {
		t.Fatalf("empty user_name: got %q, want %q", got, "I am User.")
	}
}

// TestExpandMacrosNoRecursion 不递归：替换进去的值里的宏不再扫第二遍——
// 否则 user_name 里写个 {{char}} 就能自我放大（契约明确禁止）。
func TestExpandMacrosNoRecursion(t *testing.T) {
	got := expandMacros("{{user}}", "Aria", "{{char}}", "2026-10-18", "")
	if got != "{{char}}" {
		t.Fatalf("expansion must not recurse: got %q, want %q", got, "{{char}}")
	}
}
