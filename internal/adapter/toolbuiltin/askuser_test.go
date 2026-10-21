package toolbuiltin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeAsker 提问替身：记录收到的请求，按脚本作答。
type fakeAsker struct {
	got   port.AskRequest
	ans   port.AskAnswer
	err   error
	calls int
}

var _ port.Asker = (*fakeAsker)(nil)

func (f *fakeAsker) Ask(_ context.Context, req port.AskRequest) (port.AskAnswer, error) {
	f.calls++
	f.got = req
	return f.ans, f.err
}

// askArgs 构造一次两题提问（第二题为纯自由输入、单选）。
func askArgs() string {
	return `{"questions":[` +
		`{"header":"Target","question":"Which platform?","options":[` +
		`{"label":"Windows (Recommended)","description":"desktop"},` +
		`{"label":"Linux"}],"multiple":false},` +
		`{"header":"Scope","question":"What scope?","options":[]}]}`
}

// TestAskUserContract 正常路径：请求形状对齐、答案按序回填。
func TestAskUserContract(t *testing.T) {
	f := &fakeAsker{ans: port.AskAnswer{Answers: [][]string{{"Windows (Recommended)"}, {"just this file"}}}}
	tool := &askUser{asker: f}
	res, err := tool.Execute(context.Background(), call(askArgs()))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.OK {
		t.Fatalf("res = %+v", res)
	}
	if f.calls != 1 {
		t.Fatalf("asker calls = %d", f.calls)
	}
	if len(f.got.Questions) != 2 {
		t.Fatalf("questions = %+v", f.got.Questions)
	}
	q0 := f.got.Questions[0]
	if q0.Header != "Target" || q0.Question != "Which platform?" || len(q0.Options) != 2 || q0.Multiple {
		t.Fatalf("q0 = %+v", q0)
	}
	if q0.Options[0].Description != "desktop" {
		t.Fatalf("q0 opts = %+v", q0.Options)
	}
	if len(f.got.Questions[1].Options) != 0 {
		t.Fatalf("q1 opts = %+v", f.got.Questions[1].Options)
	}
	if !strings.Contains(res.Output, `"Which platform?"="Windows (Recommended)"`) {
		t.Fatalf("output = %q", res.Output)
	}
	if !strings.Contains(res.Output, `"What scope?"="just this file"`) {
		t.Fatalf("output = %q", res.Output)
	}
}

// TestAskUserUnanswered 多题只答一部分：未答按 Unanswered 回填（不吞掉题位）。
func TestAskUserUnanswered(t *testing.T) {
	f := &fakeAsker{ans: port.AskAnswer{Answers: [][]string{{"Linux"}}}}
	tool := &askUser{asker: f}
	res, err := tool.Execute(context.Background(), call(askArgs()))
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if !strings.Contains(res.Output, `"What scope?"="Unanswered"`) {
		t.Fatalf("output = %q", res.Output)
	}
	// 答案比题多/少都不 panic：多出的忽略，少的补 Unanswered。
	f.ans = port.AskAnswer{Answers: [][]string{{"a"}, {"b"}, {"extra"}}}
	if res, err = tool.Execute(context.Background(), call(askArgs())); err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if strings.Contains(res.Output, "extra") {
		t.Fatalf("多余答案应被忽略：%q", res.Output)
	}
}

// TestAskUserDismissed 「忽略」= 放弃全部：OK=false + 明确的「按你自己判断」文案。
func TestAskUserDismissed(t *testing.T) {
	f := &fakeAsker{ans: port.AskAnswer{Dismissed: true}}
	tool := &askUser{asker: f}
	res, err := tool.Execute(context.Background(), call(askArgs()))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.OK {
		t.Fatalf("res = %+v, want OK=false", res)
	}
	if !strings.Contains(res.Err, "dismissed") {
		t.Fatalf("err = %q", res.Err)
	}
}

// TestAskUserAway 无人值守：OK=false + 「用户不在」文案（与「忽略」分开——不是用户决定）。
func TestAskUserAway(t *testing.T) {
	f := &fakeAsker{ans: port.AskAnswer{Away: true}}
	tool := &askUser{asker: f}
	res, err := tool.Execute(context.Background(), call(askArgs()))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.OK {
		t.Fatalf("res = %+v, want OK=false", res)
	}
	if !strings.Contains(res.Err, "not available") || !strings.Contains(res.Err, "do not ask again") {
		t.Fatalf("err = %q", res.Err)
	}
	if strings.Contains(res.Err, "dismissed") {
		t.Fatalf("无人值守不该用「忽略」文案：%q", res.Err)
	}
}

// TestAskUserValidation 入参边界：题数 1–4、选项 0–9、题干/选项标签非空。
func TestAskUserValidation(t *testing.T) {
	f := &fakeAsker{ans: port.AskAnswer{Answers: [][]string{{"x"}}}}
	tool := &askUser{asker: f}

	q := func(header, question string, opts int) string {
		parts := make([]string, 0, opts)
		for i := 0; i < opts; i++ {
			parts = append(parts, `{"label":"o`+string(rune('a'+i))+`"}`)
		}
		return `{"header":` + js(header) + `,"question":` + js(question) +
			`,"options":[` + strings.Join(parts, ",") + `]}`
	}
	wrap := func(qs ...string) string {
		return `{"questions":[` + strings.Join(qs, ",") + `]}`
	}

	bad := []struct {
		name string
		args string
		want string
	}{
		{"空题集", `{"questions":[]}`, "must not be empty"},
		{"超题数", wrap(q("a", "q", 0), q("b", "q", 0), q("c", "q", 0), q("d", "q", 0), q("e", "q", 0)), "at most 4"},
		{"空题干", wrap(`{"header":"a","question":"  "}`), "is empty"},
		{"超选项数", wrap(q("a", "q", 10)), "at most 9"},
		{"空标签", wrap(`{"header":"a","question":"q","options":[{"label":"  "}]}`), "empty label"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tool.Execute(context.Background(), call(tc.args)); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want 含 %q", err, tc.want)
			}
		})
	}

	// 边界内的最小/最大形状都要放行。
	ok := []string{
		wrap(q("a", "q", 0)),
		wrap(q("a", "q", 9)),
		wrap(q("a", "q", 0), q("b", "q", 0), q("c", "q", 0), q("d", "q", 0)),
	}
	f.ans = port.AskAnswer{Answers: [][]string{{"x"}, {"y"}, {"z"}, {"w"}}}
	for _, args := range ok {
		if _, err := tool.Execute(context.Background(), call(args)); err != nil {
			t.Fatalf("合法入参被拒：%v（args = %s）", err, args)
		}
	}
}

// TestAskUserHeaderDefaultsAndClips 页签缺省与超长裁剪：截断比废掉整次提问好。
func TestAskUserHeaderDefaultsAndClips(t *testing.T) {
	f := &fakeAsker{}
	tool := &askUser{asker: f}
	long := strings.Repeat("h", maxAskHeaderChars+5)
	args := `{"questions":[{"header":"","question":"q1"},` +
		`{"header":` + js(long) + `,"question":"q2"}]}`
	if _, err := tool.Execute(context.Background(), call(args)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := f.got.Questions[0].Header; got != "Q1" {
		t.Fatalf("缺省 header = %q, want Q1", got)
	}
	if got := f.got.Questions[1].Header; len([]rune(got)) != maxAskHeaderChars {
		t.Fatalf("header 长度 = %d, want %d", len([]rune(got)), maxAskHeaderChars)
	}
}

// TestAskUserTextClips 题干/标签/说明的超长同样截断（与 header 一致，不废掉整次提问）。
func TestAskUserTextClips(t *testing.T) {
	f := &fakeAsker{}
	tool := &askUser{asker: f}
	longQ := strings.Repeat("问", maxAskQuestionChars+10)
	longL := strings.Repeat("标", maxAskLabelChars+10)
	longD := strings.Repeat("说", maxAskDescriptionChars+10)
	args := `{"questions":[{"header":"a","question":` + js(longQ) +
		`,"options":[{"label":` + js(longL) + `,"description":` + js(longD) + `}]}]}`
	if _, err := tool.Execute(context.Background(), call(args)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	q := f.got.Questions[0]
	if n := len([]rune(q.Question)); n != maxAskQuestionChars {
		t.Fatalf("题干长度 = %d, want %d", n, maxAskQuestionChars)
	}
	if n := len([]rune(q.Options[0].Label)); n != maxAskLabelChars {
		t.Fatalf("标签长度 = %d, want %d", n, maxAskLabelChars)
	}
	if n := len([]rune(q.Options[0].Description)); n != maxAskDescriptionChars {
		t.Fatalf("说明长度 = %d, want %d", n, maxAskDescriptionChars)
	}
}

// TestAskUserUnavailable 没有提问通道（未装配）时明确报因，不静默吞掉。
func TestAskUserUnavailable(t *testing.T) {
	tool := &askUser{}
	_, err := tool.Execute(context.Background(), call(askArgs()))
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("err = %v", err)
	}
}

// TestAskUserContextCancel 取消本轮 ≠ 忽略：ctx 错误原样上抛（装配级故障中止 Turn）。
func TestAskUserContextCancel(t *testing.T) {
	want := errors.New("turn cancelled")
	tool := &askUser{asker: &fakeAsker{err: want}}
	if _, err := tool.Execute(context.Background(), call(askArgs())); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	// 已取消的 ctx 不进 Ask（不做无意义的提问）。
	f := &fakeAsker{}
	tool = &askUser{asker: f}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, call(askArgs())); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if f.calls != 0 {
		t.Fatalf("asker calls = %d, want 0", f.calls)
	}
}

// TestAskUserInteractive 声明 Interactive：Runner 据此不吃默认超时（D184）。
func TestAskUserInteractive(t *testing.T) {
	var tl port.Tool = &askUser{}
	it, ok := tl.(port.Interactive)
	if !ok || !it.Interactive() {
		t.Fatal("ask_user 必须声明 Interactive")
	}
}
