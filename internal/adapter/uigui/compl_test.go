package uigui

import (
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeCatalog 补全测试用命令清单（port.CommandCatalog 假实现）。
type fakeCatalog struct{ list []port.CommandInfo }

func (f fakeCatalog) Commands() []port.CommandInfo { return f.list }

// complFixture 清单样例：覆盖主名前缀、别名条目（quit/exit）、枚举参数、自由文本参数、
// 可重复开关与吃值开关。
func complFixture() []port.CommandInfo {
	return []port.CommandInfo{
		{Names: []string{"edit"}, Usage: "<id> [--keep|--copy] [--attach <路径>] [<文本>]", Desc: "Revise 修订",
			Params: []port.CommandParam{
				{Name: "<id>", Kind: port.ParamText, Desc: "要改的消息 id"},
				{Name: "--keep", Kind: port.ParamFlag, Desc: "转到新分支"},
				{Name: "--copy", Kind: port.ParamFlag, Desc: "深拷贝"},
				{Name: "--attach", Kind: port.ParamFlag, ValueName: "<路径>", Repeatable: true, Desc: "追加附件"},
				{Name: "<文本>", Kind: port.ParamText, Desc: "新正文"},
			}},
		{Names: []string{"effort"}, Usage: "[档位]", Desc: "推理档位",
			Params: []port.CommandParam{{
				Name: "档位", Kind: port.ParamChoice, Desc: "推理档位",
				Values: []string{"minimal", "low", "medium", "high", "off"},
			}}},
		{Names: []string{"quit", "exit"}, Desc: "退出"},
	}
}

// complFrame 一帧布局 + 提交 hit 树（Gio 次序：渲染之后、present 之前）。
func complFrame(q *input.Router, u *UI) {
	gtx, ops := frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)
}

// complFixtureUI 带清单、焦点已进编辑器的帧测试 UI。
func complFixtureUI() (*UI, *input.Router) {
	u := newFrameUI()
	u.opts.Commands = fakeCatalog{list: complFixture()}
	u.focusPending = true // 首帧焦点入编辑器（Focus 过滤器路由前提，newUI 同款）
	q := new(input.Router)
	complFrame(q, u)
	return u, q
}

// TestComplPhase 浮层开启判据（D250）：以 "/" 开头即开——空格不再关闭（参数相接上）。
func TestComplPhase(t *testing.T) {
	for text, want := range map[string]bool{
		"": false, "/": true, "/ed": true, "/EDIT": true, "/中文": true,
		"/edit ": true, "/edit x": true, "ed": false, " /edit": false,
	} {
		if got := complPhase(text); got != want {
			t.Fatalf("complPhase(%q) = %v, want %v", text, got, want)
		}
	}
}

// TestComplToken 末尾 token 切分：Tab 替换的就是这一段。
func TestComplToken(t *testing.T) {
	for _, c := range []struct {
		text  string
		start int
		tok   string
	}{
		{"/edit", 0, "/edit"},  // 命令相：整段就是那个 token（斜杠含在内）
		{"/edit ", 6, ""},      // 刚敲完分隔符：下一个参数还没开始
		{"/edit  u1", 7, "u1"}, // 连续空白也算分隔
		{"  x", 2, "x"},
		{"", 0, ""},
	} {
		if s, tok := complToken(c.text); s != c.start || tok != c.tok {
			t.Fatalf("complToken(%q) = (%d, %q), want (%d, %q)", c.text, s, tok, c.start, c.tok)
		}
	}
}

// TestComplFilter 命令相候选（D103③）：保持清单序、大小写不敏感、别名命中不回写主名。
func TestComplFilter(t *testing.T) {
	all := complFixture()
	got := complFilter("E", all)
	if len(got) != 3 || got[0].insert != "/edit" || got[1].insert != "/effort" || got[2].insert != "/exit" {
		t.Fatalf("filter(E) = %+v", got)
	}
	got = complFilter("exit", all)
	if len(got) != 1 || got[0].insert != "/exit" {
		t.Fatalf("filter(exit) = %+v", got)
	}
	if len(complFilter("zz", all)) != 0 {
		t.Fatal("无命中应为空")
	}
}

// TestComplTargetOf 相判定：命令相（还没敲空格）与参数相（已敲）分得清，命令名认得
// 主名与别名。
func TestComplTargetOf(t *testing.T) {
	all := complFixture()
	if tgt := complTargetOf("/ed", all); !tgt.cmdPhase || tgt.prefix != "ed" {
		t.Fatalf("/ed = %+v, want 命令相 prefix=ed", tgt)
	}
	tgt := complTargetOf("/effort m", all)
	if tgt.cmdPhase || !tgt.known || tgt.tok != "m" || tgt.start != 8 || len(tgt.args) != 0 {
		t.Fatalf("/effort m = %+v", tgt)
	}
	tgt = complTargetOf("/EXIT ", all)
	if !tgt.known || tgt.tok != "" || len(tgt.args) != 0 {
		t.Fatalf("别名大小写不应影响认命令: %+v", tgt)
	}
	if tgt = complTargetOf("/nope ", all); tgt.known {
		t.Fatalf("未注册的 /nope 被当成 known: %+v", tgt)
	}
}

// TestComplArgItems 参数相候选：枚举给可选值、自由文本只给一句提示、开关按已用过的
// 去重（可重复的照旧列出）、吃值开关的值位按声明的 Values 给。
func TestComplArgItems(t *testing.T) {
	all := complFixture()
	effort, edit := all[1], all[0]

	// 枚举：前缀过滤。
	got := complArgItems(effort, nil, "m")
	if len(got) != 2 || got[0].insert != "minimal" || got[1].insert != "medium" {
		t.Fatalf("effort m = %+v", got)
	}
	// 空 token：没有开关可列，只给这一位参数的选项。
	got = complArgItems(effort, nil, "")
	if len(got) != 5 || got[0].insert != "minimal" {
		t.Fatalf("effort 空 = %+v", got)
	}

	// 自由文本：只提示不填（insert 为空）。
	got = complArgItems(edit, nil, "u")
	if len(got) != 1 || got[0].label != "<id>" || got[0].insert != "" {
		t.Fatalf("edit 首位 = %+v", got)
	}

	// 已给过 --keep：开关清单里不再出现，--attach（可重复）仍在。
	got = complArgItems(edit, []string{"u1", "--keep"}, "")
	var hasKeep, hasAttach bool
	for _, it := range got {
		if it.insert == "--keep" {
			hasKeep = true
		}
		if it.insert == "--attach" {
			hasAttach = true
		}
	}
	if hasKeep || !hasAttach {
		t.Fatalf("开关去重不对: %+v", got)
	}

	// 正在敲 --：只列开关，位置参数的提示不出现。
	got = complArgItems(edit, []string{"u1"}, "--a")
	if len(got) != 1 || got[0].insert != "--attach" || got[0].label != "--attach <路径>" {
		t.Fatalf("--a = %+v", got)
	}

	// --attach 吃值：这一位是路径，声明没有 Values → 提示行（ValueName）。
	got = complArgItems(edit, []string{"u1", "--attach"}, "")
	if len(got) != 1 || got[0].label != "<路径>" || got[0].insert != "" {
		t.Fatalf("--attach 值位 = %+v", got)
	}
}

// TestComplArgItemsValueFromValues 吃值开关带枚举值（/export --format）：值位按 Values
// 给候选，而不是退成提示行。
func TestComplArgItemsValueFromValues(t *testing.T) {
	ci := port.CommandInfo{Names: []string{"export"}, Usage: "[--format <名字>]",
		Params: []port.CommandParam{
			{Name: "--format", Kind: port.ParamFlag, ValueName: "<名字>", Values: []string{"aqx", "chatgpt"}},
		}}
	got := complArgItems(ci, []string{"--format"}, "")
	if len(got) != 2 || got[0].insert != "aqx" || got[1].insert != "chatgpt" {
		t.Fatalf("--format 值位 = %+v", got)
	}
}

// TestComplArgItemsNoParams 不吃参数的命令同样进参数相（D250）：给一行说明，免得浮层
// 在用户敲完空格后无来由地消失。
func TestComplArgItemsNoParams(t *testing.T) {
	got := complArgItems(port.CommandInfo{Names: []string{"list"}}, nil, "")
	if len(got) != 1 || got[0].insert != "" {
		t.Fatalf("无参数命令 = %+v", got)
	}
}

// TestComplCycle 轮换状态机：锁定候选只轮可填行、环回、点选不改变候选表。
func TestComplCycle(t *testing.T) {
	items := []complItem{
		{label: "<id>", insert: ""}, // 提示行：不进轮换
		{label: "--keep", insert: "--keep"},
		{label: "--copy", insert: "--copy"},
	}
	var c complCycle
	sel, ok := c.begin(items, 6)
	if !ok || sel != 1 || !c.on || c.start != 6 {
		t.Fatalf("begin = (%d, %v) on=%v start=%d", sel, ok, c.on, c.start)
	}
	if sel, ok := c.step(false); !ok || sel != 2 {
		t.Fatalf("step(false) = (%d, %v), want 2", sel, ok)
	}
	if sel, ok := c.step(false); !ok || sel != 1 {
		t.Fatalf("step(false) 环回 = (%d, %v), want 1", sel, ok)
	}
	// 逆向：从首项往后一退落到末项（不越界停住），再退一步回到首项前面的那一项。
	if sel, ok := c.step(true); !ok || sel != 2 {
		t.Fatalf("step(true) = (%d, %v), want 2（末项）", sel, ok)
	}
	if sel, ok := c.step(true); !ok || sel != 1 {
		t.Fatalf("step(true) 环回 = (%d, %v), want 1", sel, ok)
	}
	if ok := c.seek(2); !ok || c.at != 1 {
		t.Fatalf("seek = %v at=%d", ok, c.at)
	}
	c.reset()
	if sel, ok := c.step(false); ok {
		t.Fatalf("reset 后仍能步进: %d", sel)
	}
	// 全是提示行时不该进入轮换。
	if _, ok := c.begin([]complItem{{label: "x"}}, 0); ok {
		t.Fatal("没有可填行却进入了轮换")
	}
}
