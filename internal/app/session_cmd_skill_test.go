package app

// session_cmd_skill_test.go /skill（D201）：清单展示、按名载入的注入口径（宿主信任、
// 不触发、来源可读）、名字打错与未接线的报错。

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeSkills 技能清单替身（端口只读三方法）。
type fakeSkills struct {
	list   []port.SkillInfo
	bodies map[string]string
	warns  []string
}

func (f *fakeSkills) List(context.Context) ([]port.SkillInfo, error) { return f.list, nil }

func (f *fakeSkills) Load(_ context.Context, name string) (port.SkillContent, error) {
	body, ok := f.bodies[name]
	if !ok {
		return port.SkillContent{}, fmt.Errorf("%w: %q", port.ErrSkillNotFound, name)
	}
	for _, s := range f.list {
		if s.Name == name {
			return port.SkillContent{Info: s, Body: body, Path: s.Dir + "/SKILL.md", BodyLine: 6}, nil
		}
	}
	return port.SkillContent{}, port.ErrSkillNotFound
}

func (f *fakeSkills) Warnings(context.Context) []string { return f.warns }

// skillSession 造一个带技能清单的会话（其余同 newTestSession）。
func skillSession(t *testing.T, cat port.SkillCatalog, streams ...*scriptStream) (*Session, *scriptLLM, *recorder) {
	t.Helper()
	rec := &recorder{}
	llm := &scriptLLM{t: t, streams: streams}
	agent := newAgent(t, llm, rec, Deps{}, Config{})
	s, err := NewSession(context.Background(), SessionDeps{
		Store:  newMemStore(),
		Agent:  agent,
		IDs:    &seqIDs{},
		Clock:  fixedClock{testTime},
		UI:     rec,
		Skills: cat,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, llm, rec
}

// pdfSkills 一个技能 + 一条被跳过的坏条目。
func pdfSkills() *fakeSkills {
	return &fakeSkills{
		list:   []port.SkillInfo{{Name: "pdf", Description: "Fill PDF\nforms.", Dir: `/skills/pdf`, Source: `/skills`}},
		bodies: map[string]string{"pdf": "# PDF\n\nUse pdftk."},
		warns:  []string{"技能 /skills/broken 已跳过：frontmatter 缺少必填的 name"},
	}
}

// TestSkillCommandList /skill 无参与 list 同义：列名字、说明、来源与未加载条目。
func TestSkillCommandList(t *testing.T) {
	s, _, _ := skillSession(t, pdfSkills())
	for _, args := range [][]string{nil, {"list"}} {
		out, err := s.execSkill(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"已安装 1 个技能", "- pdf — Fill PDF forms.", "来源：/skills", "未加载的条目"} {
			if !strings.Contains(out, want) {
				t.Fatalf("清单缺 %q：\n%s", want, out)
			}
		}
	}
}

// TestSkillCommandLoadInjectsText /skill <名字>：正文进上下文（宿主信任、不触发），清屏回放。
func TestSkillCommandLoadInjectsText(t *testing.T) {
	s, llm, rec := skillSession(t, pdfSkills(), textStream("不应该被调用"))
	s.Handle(context.Background(), port.UserInput{Text: "hi"})
	baseReqs := len(llm.requests)

	out, err := handleCmd(s, "skill", "pdf")
	if err != nil {
		t.Fatalf("skill: %v", err)
	}
	if !strings.Contains(out, "已载入技能 pdf") {
		t.Fatalf("out = %q", out)
	}
	if len(llm.requests) != baseReqs {
		t.Fatalf("载入技能不应起轮：requests = %d, want %d", len(llm.requests), baseReqs)
	}
	last := s.Current().Path()[len(s.Current().Path())-1]
	if last.Role != conversation.RoleInject || last.Trigger || last.Source != "skill:pdf" {
		t.Fatalf("注入 = %+v（role/source/trigger）", last)
	}
	if last.Trust != conversation.TrustHost {
		t.Fatalf("Trust = %q, want host", last.Trust)
	}
	text := last.Content[0].Text
	for _, want := range []string{`Skill "pdf" loaded by the user`, "# PDF", "Use pdftk.", "Skill directory: /skills/pdf"} {
		if !strings.Contains(text, want) {
			t.Fatalf("注入正文缺 %q：\n%s", want, text)
		}
	}
	names := eventNames(rec.events)
	ci, hi := -1, -1
	for i, n := range names {
		if n == "clear" && ci < 0 {
			ci = i
		}
		if n == "history" && hi < 0 {
			hi = i
		}
	}
	if ci < 0 || hi < 0 || hi < ci {
		t.Fatalf("应清屏后回放: %v", names)
	}
	if err := s.Current().Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestSkillCommandUnknownName 名字打错报中文原因（不泄露内部编号）。
func TestSkillCommandUnknownName(t *testing.T) {
	s, _, _ := skillSession(t, pdfSkills())
	if _, err := handleCmd(s, "skill", "pfd"); err == nil || !strings.Contains(err.Error(), "没有名为") {
		t.Fatalf("err = %v", err)
	}
	if _, err := handleCmd(s, "skill", "pdf", "extra"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("多余参数 err = %v", err)
	}
}

// TestSkillCommandUnavailable 未接线时报「未配置技能目录」。
func TestSkillCommandUnavailable(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	if _, err := handleCmd(s, "skill"); err == nil || !strings.Contains(err.Error(), "未配置技能目录") {
		t.Fatalf("err = %v", err)
	}
}
