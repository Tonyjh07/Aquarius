package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeCharAdmin 内存角色卡管理面替身（§5.17）：记录导入 / 删除调用，返回预置卡。
type fakeCharAdmin struct {
	card     port.CharacterCard
	imported []string
	removed  []string
	err      error
}

func (f *fakeCharAdmin) Import(_ context.Context, source string) (port.CharacterCard, error) {
	if f.err != nil {
		return port.CharacterCard{}, f.err
	}
	f.imported = append(f.imported, source)
	return f.card, nil
}

func (f *fakeCharAdmin) Remove(_ context.Context, slug string) error {
	if f.err != nil {
		return f.err
	}
	f.removed = append(f.removed, slug)
	return nil
}

// newCharCmdSession 造一个带角色卡目录与管理面的会话（命令面测试用）。
func newCharCmdSession(t *testing.T, admin CharacterAdmin, opts CharacterOptions) *Session {
	t.Helper()
	rec := &recorder{}
	agent := newAgent(t, &scriptLLM{t: t}, rec, Deps{}, Config{})
	card := charCard()
	card.Info.BookEntries = 1 // 让回执的「世界书」类有内容可报
	cat := &fakeCharCatalog{cards: map[string]port.CharacterCard{"aria": card}}
	s, err := NewSession(context.Background(), SessionDeps{
		Store:          newMemStore(),
		Agent:          agent,
		IDs:            &seqIDs{},
		Clock:          fixedClock{testTime},
		UI:             rec,
		Persona:        NewPersonaComposer(PromptSpec{}, RuntimeEnv{}, fixedNow),
		Characters:     cat,
		CharacterAdmin: admin,
		CharacterOpts:  opts,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s
}

// TestCharacterCommandList 无参/`list` 列清单：显示名、slug、来源与规范、条数。
func TestCharacterCommandList(t *testing.T) {
	s := newCharCmdSession(t, &fakeCharAdmin{}, CharacterOptions{})
	out, err := s.execCharacter(context.Background(), nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"已导入 1 张角色卡", "Aria（aria）", "世界书"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list 缺 %q:\n%s", want, out)
		}
	}
}

// TestCharacterCommandListEmpty 空目录给导入指引。
func TestCharacterCommandListEmpty(t *testing.T) {
	rec := &recorder{}
	agent := newAgent(t, &scriptLLM{t: t}, rec, Deps{}, Config{})
	s, err := NewSession(context.Background(), SessionDeps{
		Store: newMemStore(), Agent: agent, IDs: &seqIDs{}, Clock: fixedClock{testTime},
		Characters: &fakeCharCatalog{cards: map[string]port.CharacterCard{}},
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	out, err := s.execCharacter(context.Background(), []string{"list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "还没有导入任何角色卡") {
		t.Fatalf("空清单缺导入指引:\n%s", out)
	}
}

// TestCharacterCommandImportReceipt 导入回执四类：世界书 / 资产 / 不执行机制 / 只展示字段。
func TestCharacterCommandImportReceipt(t *testing.T) {
	admin := &fakeCharAdmin{card: port.CharacterCard{
		Info: port.CharacterInfo{
			Slug: "aria", Display: "Aria", BookEntries: 2, Assets: 3, HasAvatar: true,
			Unsupported: []string{"regex_scripts", "depth_prompt"},
		},
		Profile: port.CharacterProfile{BookEntries: []port.BookEntry{
			{Comment: "L", Content: "lore", Constant: true, Enabled: true},
			{Comment: "N", Content: "note", Constant: false},
		}},
	}}
	s := newCharCmdSession(t, admin, CharacterOptions{InjectConstantBook: true})
	out, err := s.execCharacter(context.Background(), []string{"import", `C:\cards\aria.png`})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(admin.imported) != 1 || admin.imported[0] != `C:\cards\aria.png` {
		t.Fatalf("import source = %v", admin.imported)
	}
	for _, want := range []string{
		"已导入角色卡「Aria」（aria）",
		"世界书：共 2 条，其中 1 条常驻条目已注入人格",
		"其余 2 个资产随原包保留",
		"regex_scripts、depth_prompt",
		"本版一律不运行",
		"只展示字段：",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("导入回执缺 %q:\n%s", want, out)
		}
	}
}

// TestCharacterCommandShow 查看：头部事实 + 四类回执。
func TestCharacterCommandShow(t *testing.T) {
	s := newCharCmdSession(t, &fakeCharAdmin{}, CharacterOptions{})
	out, err := s.execCharacter(context.Background(), []string{"show", "aria"})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	for _, want := range []string{"角色卡：Aria（aria）", "规范：", "世界书：共 1 条"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show 缺 %q:\n%s", want, out)
		}
	}
	if _, err := s.execCharacter(context.Background(), []string{"show", "nope"}); err == nil || !strings.Contains(err.Error(), "没有名为") {
		t.Fatalf("unknown slug err = %v", err)
	}
}

// TestCharacterCommandStartSubcommand `/character start <角色> [序号]` 走命令通道。
func TestCharacterCommandStartSubcommand(t *testing.T) {
	s := newCharCmdSession(t, &fakeCharAdmin{}, CharacterOptions{InjectExamples: true})
	out, err := s.execCharacter(context.Background(), []string{"start", "aria", "2"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(out, "人格模式：") {
		t.Fatalf("start 回执缺人格模式说明:\n%s", out)
	}
	path := s.Current().Path()
	last := path[len(path)-1]
	if last.Role != conversation.RoleAssistant || last.Content[0].Text != "Second Aria opening." {
		t.Fatalf("greeting = %+v, want the alternate opening", last)
	}
	if _, err := s.execCharacter(context.Background(), []string{"start", "aria", "x"}); err == nil {
		t.Fatal("非整数序号应报错")
	}
}

// TestCharacterCommandRemove 删除二次确认：拒绝即不删，允许才删。
func TestCharacterCommandRemove(t *testing.T) {
	admin := &fakeCharAdmin{}
	s := newCharCmdSession(t, admin, CharacterOptions{})
	s.confirmer = denyConfirmer{}
	out, err := s.execCharacter(context.Background(), []string{"remove", "aria"})
	if err != nil {
		t.Fatalf("remove(deny): %v", err)
	}
	if len(admin.removed) != 0 || !strings.Contains(out, "已取消删除角色卡") {
		t.Fatalf("拒绝确认后不该删除: out=%q removed=%v", out, admin.removed)
	}
	s.confirmer = allowConfirmer{}
	out, err = s.execCharacter(context.Background(), []string{"remove", "aria"})
	if err != nil {
		t.Fatalf("remove(allow): %v", err)
	}
	if len(admin.removed) != 1 || admin.removed[0] != "aria" || !strings.Contains(out, "已删除角色卡 aria") {
		t.Fatalf("允许确认应删除: out=%q removed=%v", out, admin.removed)
	}
}

// TestCharacterCommandUnknownSub 未知子命令 / 缺参数给用法提示。
func TestCharacterCommandUnknownSub(t *testing.T) {
	s := newCharCmdSession(t, &fakeCharAdmin{}, CharacterOptions{})
	if _, err := s.execCharacter(context.Background(), []string{"bogus"}); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("unknown sub err = %v", err)
	}
	if _, err := s.execCharacter(context.Background(), []string{"show"}); err == nil {
		t.Fatal("show 缺 slug 应报错")
	}
}

// TestCharacterCommandNoCatalog 未接目录 / 管理面时如实报未接入。
func TestCharacterCommandNoCatalog(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	if _, err := s.execCharacter(context.Background(), []string{"list"}); err == nil || !strings.Contains(err.Error(), "未配置角色卡目录") {
		t.Fatalf("list without catalog err = %v", err)
	}
	if _, err := s.execCharacter(context.Background(), []string{"import", "x.png"}); err == nil || !strings.Contains(err.Error(), "未接入") {
		t.Fatalf("import without admin err = %v", err)
	}
}
