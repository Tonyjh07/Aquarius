package main

import (
	"context"
	"testing"

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
	tools    []port.Tool
	prompts  []plugin.PromptInfo
	toolsErr error
}

func (f *fakePluginSession) Tools(context.Context) ([]port.Tool, error) {
	return f.tools, f.toolsErr
}
func (f *fakePluginSession) Memory() port.MemoryStore { return nil }
func (f *fakePluginSession) Prompts(context.Context) ([]plugin.PromptInfo, error) {
	return f.prompts, nil
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
