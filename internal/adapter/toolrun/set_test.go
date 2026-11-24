package toolrun

// set_test.go 会话感知的工具源（D340）：清单按轮给、名字按会话解析。
//
// 这一组测的是「同一批名字在不同会话里指向不同东西」——那正是工具源不能进全局
// byName 的原因。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// sessionSet 按会话给不同工具面的源。每个工具执行时把会话 ID 拼进输出，用来验证
// Execute 也走 ctx。
type sessionSet struct {
	specs map[string][]string // 会话 ID → 工具名
	err   error               // 非空 = 声明期报错
	tools []string            // 收到的会话（按调用序）
}

func (s *sessionSet) Name() string { return "demo" }

func (s *sessionSet) Specs(ctx context.Context) ([]tool.Spec, error) {
	sid, _ := port.SessionIDFrom(ctx)
	s.tools = append(s.tools, string(sid))
	if s.err != nil {
		return nil, s.err
	}
	names := s.specs[string(sid)]
	out := make([]tool.Spec, 0, len(names))
	for _, n := range names {
		out = append(out, tool.Spec{Name: n, Risk: tool.Safe})
	}
	return out, nil
}

func (s *sessionSet) Lookup(ctx context.Context, name string) (port.Tool, error) {
	sid, _ := port.SessionIDFrom(ctx)
	for _, n := range s.specs[string(sid)] {
		if n == name {
			return execTool(n, tool.Safe, n+"@"+string(sid)), nil
		}
	}
	return nil, nil
}

func sessCtx(id string) context.Context {
	return port.WithSessionID(context.Background(), conversation.ID(id))
}

func specNames(specs []tool.Spec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return out
}

// TestSpecsMergeSetBySession 同一个源在两个会话给出不同清单，合成结果跟着 ctx 走。
func TestSpecsMergeSetBySession(t *testing.T) {
	set := &sessionSet{specs: map[string][]string{
		"a": {"mem_1", "mem_2"},
		"b": {"mem_9"},
	}}
	r := New(Options{Tools: []port.Tool{execTool("builtin", tool.Safe, "b")}})
	r.AddSet(set)

	got, err := r.Specs(sessCtx("a"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(specNames(got), ",") != "builtin,mem_1,mem_2" {
		t.Fatalf("会话 a 的清单=%v", specNames(got))
	}

	got, err = r.Specs(sessCtx("b"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(specNames(got), ",") != "builtin,mem_9" {
		t.Fatalf("会话 b 的清单=%v", specNames(got))
	}
}

// TestExecuteRoutesSetByName 同名工具在不同会话里指向不同东西：查找必须按 ctx 的会话走。
func TestExecuteRoutesSetByName(t *testing.T) {
	set := &sessionSet{specs: map[string][]string{
		"a": {"mem_1", "mem_2"},
		"b": {"mem_1"},
	}}
	r := New(Options{})
	r.AddSet(set)

	res, err := r.Execute(sessCtx("a"), c("mem_1", `{}`))
	if err != nil || !res.OK || res.Output != "mem_1@a" {
		t.Fatalf("会话 a：res=%+v err=%v", res, err)
	}
	res, err = r.Execute(sessCtx("b"), c("mem_1", `{}`))
	if err != nil || !res.OK || res.Output != "mem_1@b" {
		t.Fatalf("会话 b：res=%+v err=%v", res, err)
	}
	// 不在该会话可见集合里的名字（别的会话才有）按未知工具回填。
	res, err = r.Execute(sessCtx("b"), c("mem_2", `{}`))
	if err != nil || res.OK || !strings.Contains(res.Err, "unknown tool") {
		t.Fatalf("跨会话借名：res=%+v err=%v", res, err)
	}
}

// TestSetToolsBypassDisabled 禁用集只管静态工具：源的名字按会话逐轮变，逐名禁用
// 没有意义（开关是插件自己的启停）。
func TestSetToolsBypassDisabled(t *testing.T) {
	set := &sessionSet{specs: map[string][]string{"a": {"mem_1"}}}
	r := New(Options{Tools: []port.Tool{execTool("builtin", tool.Safe, "b")}})
	r.AddSet(set)
	r.SetDisabled(map[string]bool{"mem_1": true, "builtin": true})

	specs, err := r.Specs(sessCtx("a"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(specNames(specs), ",") != "mem_1" {
		t.Fatalf("清单=%v（builtin 应被禁掉、mem_1 应留下）", specNames(specs))
	}
	res, err := r.Execute(sessCtx("a"), c("mem_1", `{}`))
	if err != nil || !res.OK {
		t.Fatalf("源工具被执行集禁掉了：res=%+v err=%v", res, err)
	}
}

// TestSetNameCollisionPrefersBuiltin 与内置同名时以内置为准，插件那一条被丢弃，
// 执行也走内置——不能让一个插件顶掉确认链见过名字的工具。
func TestSetNameCollisionPrefersBuiltin(t *testing.T) {
	set := &sessionSet{specs: map[string][]string{"a": {"mem_1"}}}
	r := New(Options{Tools: []port.Tool{execTool("mem_1", tool.Safe, "builtin-wins")}})
	r.AddSet(set)

	specs, err := r.Specs(sessCtx("a"))
	if err != nil {
		t.Fatal(err)
	}
	if n := specNames(specs); len(n) != 1 || n[0] != "mem_1" {
		t.Fatalf("同名应只留一条：%v", n)
	}
	res, err := r.Execute(sessCtx("a"), c("mem_1", `{}`))
	if err != nil || res.Output != "builtin-wins" {
		t.Fatalf("撞名时执行没走内置：res=%+v err=%v", res, err)
	}
}

// TestSetSpecsErrorFailsAssembly 源声明期报错让本轮装配失败并带上源名——静默跳过
// 会让插件写错名字时看起来「工具就是没出现」。
func TestSetSpecsErrorFailsAssembly(t *testing.T) {
	set := &sessionSet{err: errors.New("store unavailable")}
	r := New(Options{})
	r.AddSet(set)
	_, err := r.Specs(sessCtx("a"))
	if err == nil || !strings.Contains(err.Error(), "demo") || !strings.Contains(err.Error(), "store unavailable") {
		t.Fatalf("报因该带源名与原因：%v", err)
	}
}

// TestRemoveSet 停用后清单里不再有它，执行按未知工具回填。
func TestRemoveSet(t *testing.T) {
	set := &sessionSet{specs: map[string][]string{"a": {"mem_1"}}}
	r := New(Options{})
	r.AddSet(set)
	r.RemoveSet(set)

	specs, err := r.Specs(sessCtx("a"))
	if err != nil || len(specs) != 0 {
		t.Fatalf("摘掉后不该再有声明：%v %v", specs, err)
	}
	res, _ := r.Execute(sessCtx("a"), c("mem_1", `{}`))
	if res.OK || !strings.Contains(res.Err, "unknown tool") {
		t.Fatalf("res=%+v", res)
	}
}

// TestPTCHidesSetTools PTC 模式下模型面只出 run_code：工具源是按轮导航的形态，
// 脚本里用不上，也会让 stub 生成多算一遍全量清单。
func TestPTCHidesSetTools(t *testing.T) {
	set := &sessionSet{specs: map[string][]string{"a": {"mem_1"}}}
	r := New(Options{PTC: true, Tools: []port.Tool{execTool(port.RunCodeTool, tool.Safe, "rc")}})
	r.AddSet(set)

	specs, err := r.Specs(sessCtx("a"))
	if err != nil || len(specs) != 1 || specs[0].Name != port.RunCodeTool {
		t.Fatalf("PTC 该只出 run_code：%v %v", specNames(specs), err)
	}
}
