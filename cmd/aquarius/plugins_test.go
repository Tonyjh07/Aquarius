package main

import (
	"context"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// specTool 只带声明的工具替身。
type specTool struct{ spec tool.Spec }

func (s *specTool) Spec() tool.Spec { return s.spec }
func (s *specTool) Execute(context.Context, tool.Call) (tool.Result, error) {
	return tool.Result{OK: true}, nil
}

// fakePluginSession plugin.Session 替身（刷新逻辑只需 Tools/Prompts）。
type fakePluginSession struct {
	tools      []port.Tool
	prompts    []plugin.PromptInfo
	toolsErr   error
	promptsErr error
	block      chan struct{} // 非 nil 时 Tools 阻塞其上（超时测试）
}

func (f *fakePluginSession) Tools(ctx context.Context) ([]port.Tool, error) {
	if f.block != nil {
		select { // 与真实实现一致：尊重 ctx（超时由调用方注入）
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.tools, f.toolsErr
}
func (f *fakePluginSession) Memory() port.MemoryStore { return nil }
func (f *fakePluginSession) Prompts(context.Context) ([]plugin.PromptInfo, error) {
	return f.prompts, f.promptsErr
}
func (f *fakePluginSession) RenderPrompt(context.Context, string, []string) (string, error) {
	return "", nil
}
func (f *fakePluginSession) Wait() error  { return nil }
func (f *fakePluginSession) Close() error { return nil }
func (f *fakePluginSession) Stats() plugin.Stats {
	return plugin.Stats{}
}

// TestPluginSurfacesRefresh 工具面随就绪集增减（session 尚未建好时只刷工具）：
// 上线注册、下线移除、再上线换新——registered 记录驱动移除。
func TestPluginSurfacesRefresh(t *testing.T) {
	runner := toolrun.New(toolrun.Options{})
	var sess *app.Session // nil：验证 session 未建时安全短路
	ready := map[string]plugin.Session{
		"a": &fakePluginSession{tools: []port.Tool{
			&specTool{spec: tool.Spec{Name: "mcp:a:t1"}},
			&specTool{spec: tool.Spec{Name: "mcp:a:t2"}},
		}},
	}
	s := &pluginSurfaces{
		runner:     runner,
		session:    &sess,
		ready:      func() map[string]plugin.Session { return ready },
		callCtx:    context.Background(),
		logf:       func(string, ...any) {},
		registered: map[string][]string{},
	}
	ctx := context.Background()

	s.refresh()
	specs, err := runner.Specs(ctx)
	if err != nil || len(specs) != 2 {
		t.Fatalf("specs = %+v, %v, want 2", specs, err)
	}

	// a 下线、b 上线：a 的工具移除，b 的注册。
	ready = map[string]plugin.Session{
		"b": &fakePluginSession{tools: []port.Tool{
			&specTool{spec: tool.Spec{Name: "mcp:b:t1"}},
		}},
	}
	s.refresh()
	specs, _ = runner.Specs(ctx)
	if len(specs) != 1 || specs[0].Name != "mcp:b:t1" {
		t.Fatalf("specs = %+v, want 仅 mcp:b:t1", specs)
	}

	// Tools 拉取失败：保留上次注册（不误删），只记日志。
	ready = map[string]plugin.Session{
		"b": &fakePluginSession{toolsErr: errTest},
	}
	s.refresh()
	specs, _ = runner.Specs(ctx)
	if len(specs) != 1 || specs[0].Name != "mcp:b:t1" {
		t.Fatalf("拉取失败不应移除既有工具: %+v", specs)
	}
}

// errTest 拉取失败占位错误。
var errTest = &testErr{"list failed"}

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }

// TestPluginSurfacesToolDiff 审查修复：server 侧缩表/改名 → 旧工具按集合 diff 摘除
// （旧实现只覆盖登记簿，残留名再也摘不掉，disable 也清不干净）；prompts 拉取失败
// 保留上次成功清单，与工具"失败保留旧登记"同语义。
func TestPluginSurfacesToolDiff(t *testing.T) {
	runner := toolrun.New(toolrun.Options{})
	var sess *app.Session // nil：只验工具与簿记
	fake := &fakePluginSession{
		tools: []port.Tool{
			&specTool{spec: tool.Spec{Name: "mcp:a:t1"}},
			&specTool{spec: tool.Spec{Name: "mcp:a:t2"}},
		},
		prompts: []plugin.PromptInfo{{Name: "p1"}},
	}
	ready := map[string]plugin.Session{"a": fake}
	s := &pluginSurfaces{
		runner:     runner,
		session:    &sess,
		ready:      func() map[string]plugin.Session { return ready },
		callCtx:    context.Background(),
		logf:       func(string, ...any) {},
		registered: map[string][]string{},
		prompts:    map[string][]plugin.PromptInfo{},
	}
	specNames := func() []string {
		specs, err := runner.Specs(context.Background())
		if err != nil {
			t.Fatalf("specs: %v", err)
		}
		var out []string
		for _, sp := range specs {
			out = append(out, sp.Name)
		}
		return out
	}

	s.refresh()
	if got := specNames(); len(got) != 2 {
		t.Fatalf("specs = %v, want 2", got)
	}
	if len(s.prompts["a"]) != 1 {
		t.Fatalf("prompts = %+v", s.prompts["a"])
	}

	// 缩表：t1 必须被 diff 摘除（旧实现残留）。
	fake.tools = fake.tools[1:]
	s.refresh()
	if got := specNames(); len(got) != 1 || got[0] != "mcp:a:t2" {
		t.Fatalf("specs = %v, want 只剩 t2", got)
	}

	// prompts 拉取失败：保留上次成功清单，工具不受影响。
	fake.promptsErr = errTest
	s.refresh()
	if len(s.prompts["a"]) != 1 {
		t.Fatalf("prompts 失败后应保留上次成功: %+v", s.prompts["a"])
	}
	if got := specNames(); len(got) != 1 || got[0] != "mcp:a:t2" {
		t.Fatalf("prompts 失败不应影响工具: %v", got)
	}

	// 整机下线：工具与 prompt 簿一起清。
	ready = map[string]plugin.Session{}
	s.refresh()
	if got := specNames(); len(got) != 0 {
		t.Fatalf("下线后 specs = %v, want 空", got)
	}
	if _, ok := s.prompts["a"]; ok {
		t.Fatal("下线后 prompt 簿应删除")
	}
}

// TestPluginSurfacesFetchTimeout 审查修复：I/O 在锁外且带超时——
// 阻塞的 Tools 在 ioTimeout 内放弃，本轮保留旧登记，刷新不挂起。
func TestPluginSurfacesFetchTimeout(t *testing.T) {
	runner := toolrun.New(toolrun.Options{})
	var sess *app.Session
	fake := &fakePluginSession{
		tools: []port.Tool{&specTool{spec: tool.Spec{Name: "mcp:a:t1"}}},
	}
	ready := map[string]plugin.Session{"a": fake}
	s := &pluginSurfaces{
		runner:     runner,
		session:    &sess,
		ready:      func() map[string]plugin.Session { return ready },
		callCtx:    context.Background(),
		ioTimeout:  50 * time.Millisecond,
		logf:       func(string, ...any) {},
		registered: map[string][]string{},
		prompts:    map[string][]plugin.PromptInfo{},
	}
	s.refresh() // 首轮成功登记 t1

	fake.block = make(chan struct{})
	fake.tools = []port.Tool{&specTool{spec: tool.Spec{Name: "mcp:a:t2"}}}
	done := make(chan struct{})
	go func() {
		s.refresh()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("阻塞的 Tools 应在 ioTimeout 内放弃（持锁做网络会卡死整个面）")
	}
	close(fake.block) // 释放拉取 goroutine

	// 本轮失败 → 保留旧登记（t1 仍在，未被半截结果清掉）。
	specs, err := runner.Specs(context.Background())
	if err != nil || len(specs) != 1 || specs[0].Name != "mcp:a:t1" {
		t.Fatalf("specs = %+v, %v, want 保留 t1", specs, err)
	}
}
