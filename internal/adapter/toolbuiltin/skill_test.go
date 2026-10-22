package toolbuiltin

// skill_test.go 覆盖 skill 工具的三件事（D201 口径三）：清单进工具描述且封顶、
// 载入回填正文与目录、正文超预算时按行切断并给 file_read 续读行号。

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeSkills 最小的技能清单替身（端口只读三方法）。
type fakeSkills struct {
	list     []port.SkillInfo
	bodies   map[string]string
	bodyLine map[string]int
	warns    []string
}

func (f *fakeSkills) List(context.Context) ([]port.SkillInfo, error) { return f.list, nil }

func (f *fakeSkills) Load(_ context.Context, name string) (port.SkillContent, error) {
	body, ok := f.bodies[name]
	if !ok {
		return port.SkillContent{}, fmt.Errorf("%w: %q", port.ErrSkillNotFound, name)
	}
	for _, s := range f.list {
		if s.Name == name {
			return port.SkillContent{
				Info:     s,
				Body:     body,
				Path:     s.Dir + "/SKILL.md",
				BodyLine: f.bodyLine[name],
			}, nil
		}
	}
	return port.SkillContent{}, port.ErrSkillNotFound
}

func (f *fakeSkills) Warnings(context.Context) []string { return f.warns }

// twoSkills 两个技能（一个说明带换行，检查清单里被压成一行）。
func twoSkills() *fakeSkills {
	return &fakeSkills{
		list: []port.SkillInfo{
			{Name: "pdf", Description: "Fill PDF\nforms.", Dir: "/skills/pdf", Source: "/skills"},
			{Name: "release", Description: "Cut a release.", Dir: "/skills/release", Source: "/skills"},
		},
		bodies:   map[string]string{"pdf": "# PDF\n\nUse pdftk.\n", "release": "# Release\n\nTag it.\n"},
		bodyLine: map[string]int{"pdf": 6, "release": 6},
	}
}

// TestSkillSpecListsSkills 第一级披露：工具描述里带名字与一句说明。
func TestSkillSpecListsSkills(t *testing.T) {
	tl := &skillTool{skills: twoSkills()}
	s := tl.Spec()
	if s.Name != "skill" || s.Risk != tool.Safe {
		t.Fatalf("spec = %+v", s)
	}
	for _, want := range []string{"<available_skills>", "- pdf: Fill PDF forms.", "- release: Cut a release."} {
		if !strings.Contains(s.Description, want) {
			t.Fatalf("描述缺 %q：\n%s", want, s.Description)
		}
	}
	if strings.Contains(s.Description, "Fill PDF\n") {
		t.Fatalf("说明没被压成一行")
	}
}

// TestSkillSpecEmptyAndNil 零技能照常注册（工具面稳定）；未接线的装配明确说不可用。
func TestSkillSpecEmptyAndNil(t *testing.T) {
	empty := (&skillTool{skills: &fakeSkills{}}).Spec()
	if !strings.Contains(empty.Description, "No skills are installed.") {
		t.Fatalf("空清单描述 = %q", empty.Description)
	}
	nilSpec := (&skillTool{}).Spec()
	if !strings.Contains(nilSpec.Description, "not available") {
		t.Fatalf("nil 目录描述 = %q", nilSpec.Description)
	}
	if _, err := (&skillTool{}).Execute(context.Background(), call(`{"name":"x"}`)); err == nil {
		t.Fatal("nil 目录执行应报错")
	}
	if _, err := (&skillTool{skills: &fakeSkills{}}).Execute(context.Background(), call(`{"name":"x"}`)); err == nil {
		t.Fatal("零技能时执行应报「没有已安装技能」")
	}
}

// TestSkillListingBudget 清单总长封顶，超出时明确说「还有 N 个没列出来」。
func TestSkillListingBudget(t *testing.T) {
	f := &fakeSkills{}
	for i := 0; i < 200; i++ {
		f.list = append(f.list, port.SkillInfo{
			Name:        fmt.Sprintf("skill-%03d", i),
			Description: strings.Repeat("d", skillDescChars),
		})
	}
	s := (&skillTool{skills: f}).Spec()
	if len(s.Description) > skillListBudget+2000 {
		t.Fatalf("描述长度 %d 超预算（清单上限 %d）", len(s.Description), skillListBudget)
	}
	if !strings.Contains(s.Description, "more skills are installed but not listed") {
		t.Fatalf("截断没说明：\n%s", s.Description)
	}
}

// TestSkillExecuteLoadsBody 二级披露：回填正文 + 技能目录；正文是「要遵循的指令」。
func TestSkillExecuteLoadsBody(t *testing.T) {
	tl := &skillTool{skills: twoSkills()}
	res, err := tl.Execute(context.Background(), call(`{"name":"pdf"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`skill "pdf"`, "# PDF", "Use pdftk.", "Skill directory: /skills/pdf"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("回填缺 %q：\n%s", want, res.Output)
		}
	}
	if !res.OK {
		t.Fatalf("res = %+v", res)
	}
}

// TestSkillExecuteUnknownListsCandidates 名字打错：报错带候选，而不是静默失败。
func TestSkillExecuteUnknownListsCandidates(t *testing.T) {
	tl := &skillTool{skills: twoSkills()}
	_, err := tl.Execute(context.Background(), call(`{"name":"pfd"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown skill "pfd"`) ||
		!strings.Contains(err.Error(), "pdf, release") {
		t.Fatalf("err = %v", err)
	}
	if _, err := tl.Execute(context.Background(), call(`{}`)); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("缺名字 err = %v", err)
	}
}

// TestSkillExecutePagesLongBody 正文超预算：按行切断，续读行号 = 正文首行 + 已给行数。
func TestSkillExecutePagesLongBody(t *testing.T) {
	lines := make([]string, 0, 2000)
	for i := 0; len(strings.Join(lines, "\n")) < skillBodyChars+2000; i++ {
		lines = append(lines, fmt.Sprintf("line %04d: %s", i, strings.Repeat("x", 40)))
	}
	f := &fakeSkills{
		list:     []port.SkillInfo{{Name: "long", Dir: "/skills/long", Description: "Long one."}},
		bodies:   map[string]string{"long": strings.Join(lines, "\n")},
		bodyLine: map[string]int{"long": 7},
	}
	res, err := (&skillTool{skills: f}).Execute(context.Background(), call(`{"name":"long"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "skill body truncated") {
		t.Fatalf("超预算没给截断说明：\n%s", res.Output)
	}
	// 回填里给出的正文行数（截断点之前的那些行）→ 续读行号应为 正文首行 + 已给行数。
	idx := strings.Index(res.Output, "line 0000:")
	if idx < 0 {
		t.Fatalf("回填里没有正文：\n%s", res.Output)
	}
	page, _, _ := strings.Cut(res.Output[idx:], "\n\nSkill directory:")
	taken := strings.Count(page, "\n") + 1
	if !strings.Contains(res.Output, fmt.Sprintf("from line %d]", 7+taken)) {
		t.Fatalf("续读行号不是 %d：\n%s", 7+taken, res.Output[len(res.Output)-160:])
	}
}
