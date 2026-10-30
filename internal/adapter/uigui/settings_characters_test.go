package uigui

// settings_characters_test.go 「角色」页（§5.17）：行计划、动作经命令通道、选择框只填
// 来源框、头像异步解码与失败墓碑、元信息与首字回落（行型总覆盖见 settings_page_test.go）。

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// charactersForm 造一个挂了角色卡快照的表单（快照已同步；页停在角色页）。
func charactersForm(t *testing.T, snap CharactersSnapshot) (*settingsForm, *UI) {
	t.Helper()
	u := newHeadless(t, Options{
		Settings:   func() SettingsSnapshot { return promptSnapshot() },
		Characters: func() CharactersSnapshot { return snap },
	})
	f := newSettingsForm(u)
	f.page = pageCharacters
	return f, u
}

// TestCharactersPageRows 行计划：清单 / 来源 / 选择 / 说明 / 状态。
func TestCharactersPageRows(t *testing.T) {
	f, _ := charactersForm(t, CharactersSnapshot{Items: []CharacterState{
		{Slug: "aria", Display: "Aria", Source: "png", Spec: "v3"},
		{Slug: "bob", Display: "Bob", Source: "json"},
	}})
	f.charStatus = "ok"
	rows := f.pageRows(pageCharacters)

	count := map[rowKind]int{}
	for _, r := range rows {
		count[r.kind]++
	}
	want := map[rowKind]int{
		rowTitle: 1, rowCharItem: 2, rowCharSource: 1, rowCharPick: 1,
		rowCharNote: 1, rowCharStatus: 1,
	}
	for k, n := range want {
		if count[k] != n {
			t.Errorf("行型 %d 出现 %d 次，want %d", k, count[k], n)
		}
	}
	if count[rowCharEmpty] != 0 {
		t.Error("有卡时不该出现空态行")
	}
}

// TestCharactersPageEmptyAndUnwired 空清单给引导；未接线给只读占位；读目录失败给告警行。
func TestCharactersPageEmptyAndUnwired(t *testing.T) {
	f, _ := charactersForm(t, CharactersSnapshot{})
	if !hasRowKind(f.pageRows(pageCharacters), rowCharEmpty) {
		t.Error("空清单应给空态引导行")
	}

	fw, _ := charactersForm(t, CharactersSnapshot{Err: "permission denied"})
	if !hasRowKind(fw.pageRows(pageCharacters), rowCharWarn) {
		t.Error("读目录失败应给告警行")
	}

	u := newHeadless(t, Options{Settings: func() SettingsSnapshot { return promptSnapshot() }})
	f2 := newSettingsForm(u)
	f2.page = pageCharacters
	rows := f2.pageRows(pageCharacters)
	if !hasRowKind(rows, rowCharEmpty) {
		t.Fatalf("未接线应给只读占位行：%+v", rows)
	}
	if hasRowKind(rows, rowCharSource) {
		t.Fatalf("未接线不该出现导入入口：%+v", rows)
	}
}

// TestCharPickFillsSource 选择框结果只填来源框，不落盘（导入是另一个动作）。
func TestCharPickFillsSource(t *testing.T) {
	f, u := charactersForm(t, CharactersSnapshot{})
	u.pushCharPick(charPathPick{path: `C:\x\card.png`})
	f.drainCharPicks(u)
	if got := f.charSource.Text(); got != `C:\x\card.png` {
		t.Fatalf("来源框 = %q", got)
	}
	if !strings.Contains(f.charStatus, "点「导入」") {
		t.Fatalf("状态行 = %q", f.charStatus)
	}
}

// TestSendCharacterCmd 动作经 `/character` 命令通道（不直连管理面）。
func TestSendCharacterCmd(t *testing.T) {
	u := &UI{inCh: make(chan port.UserInput, 4)}
	u.sendCharacterCmd("import", `C:\x\card.png`)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "character" {
			t.Fatalf("命令 = %+v", in.Command)
		}
		if len(in.Command.Args) != 2 || in.Command.Args[0] != "import" || in.Command.Args[1] != `C:\x\card.png` {
			t.Fatalf("参数 = %v", in.Command.Args)
		}
	default:
		t.Fatal("inCh 未收到命令")
	}
}

// TestCharInitial 首字回落：去空白；空名给「?」。
func TestCharInitial(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Aria", "A"},
		{"  Bob", "B"},
		{"", "?"},
		{"   ", "?"},
		{"艾莉丝", "艾"},
	}
	for _, c := range cases {
		if got := charInitial(c.in); got != c.want {
			t.Errorf("charInitial(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCharMetaLine 元信息行按来源种类给中文，含条目/资产/导入时间与未支持机制提示。
func TestCharMetaLine(t *testing.T) {
	cases := []struct {
		st   CharacterState
		want string
	}{
		{CharacterState{Source: "png"}, "PNG 卡"},
		{CharacterState{Source: "charx"}, "角色卡包"},
		{CharacterState{Source: "json"}, "JSON 卡"},
		{CharacterState{Source: "png", Spec: "v3", BookEntries: 2, Assets: 1, ImportedAt: "2026-10-22"},
			"PNG 卡 · v3 · 2 条世界书 · 1 个资产 · 导入于 2026-10-22"},
		{CharacterState{Source: "json", Unsupported: []string{"regex_scripts"}},
			"JSON 卡 · 含未支持的机制（见导入回执）"},
	}
	for _, c := range cases {
		if got := c.st.metaLine(); got != c.want {
			t.Errorf("metaLine = %q, want %q", got, c.want)
		}
	}
}

// TestCharAvatarAsyncLoad 头像后台解码：首帧回落兜底，结果入缓存后可绘制。
func TestCharAvatarAsyncLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "avatar.png")
	if err := os.WriteFile(path, pngBytes(t, 24, 24, color.NRGBA{R: 0x20, G: 0x80, B: 0xC0, A: 0xFF}), 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := charactersForm(t, CharactersSnapshot{})
	if got := f.avatarFor(path); got != nil {
		t.Fatal("首帧应回落兜底（解码未就绪）")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.drainCharAvatars()
		src := f.avatarFor(path)
		if src != nil {
			if _, ok := src.imageOp(); !ok {
				t.Fatal("缓存项应可绘制")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("头像解码超时")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestCharAvatarFailureTombstone 解码失败记墓碑：不重试、恒回落兜底。
func TestCharAvatarFailureTombstone(t *testing.T) {
	f, _ := charactersForm(t, CharactersSnapshot{})
	path := filepath.Join(t.TempDir(), "nope.png")
	if f.avatarFor(path) != nil {
		t.Fatal("缺失文件首帧应回落兜底")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.drainCharAvatars()
		if f.charAvatarFail[path] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("解码失败应记墓碑")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if f.avatarFor(path) != nil {
		t.Fatal("墓碑后不该再解")
	}
}
