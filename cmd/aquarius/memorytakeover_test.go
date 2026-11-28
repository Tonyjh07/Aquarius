package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/plugin/tierone"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// memFactory 一个记忆类 Tier-1 插件：声明 provides: memory，并沿用内置工具名
// （`memory_list`）——这正是 D340 要求「先撤内置、再启用」的那条路径。
type memFactory struct{ name string }

func (f memFactory) Manifest() v1.Manifest {
	return v1.Manifest{Name: f.name, API: v1.APIVersion, Provides: []string{"memory", "tool"}}
}

func (f memFactory) Create(v1.Host) (v1.Instance, error) { return memInstance{}, nil }

type memInstance struct{}

func (memInstance) Start() error                   { return nil }
func (memInstance) Shutdown(context.Context) error { return nil }

func (memInstance) Recall(context.Context, v1.MemoryRequest) (v1.MemoryContext, error) {
	return v1.MemoryContext{Text: "## memory\n- sit for 20 minutes"}, nil
}

func (memInstance) Tools(context.Context) []v1.ToolSpec {
	return []v1.ToolSpec{{Name: "memory_list", Description: "记忆目录"}}
}

func (memInstance) Execute(context.Context, v1.ToolCall) (v1.ToolResult, error) {
	return v1.ToolResult{}, nil
}

// brokenMemFactory 声明记忆但造不出来：用来验证失败路径不带走内置记忆面。
type brokenMemFactory struct{}

func (brokenMemFactory) Manifest() v1.Manifest {
	return v1.Manifest{Name: "badmem", API: v1.APIVersion, Provides: []string{"memory"}}
}

func (brokenMemFactory) Create(v1.Host) (v1.Instance, error) {
	return nil, errors.New("cannot open the store")
}

// fixedTool 一件静态内置工具（占住名字，验证接管期被撤掉）。
type fixedTool struct{ name string }

func (t fixedTool) Spec() tool.Spec { return tool.Spec{Name: t.name, Description: "builtin"} }

func (t fixedTool) Execute(context.Context, tool.Call) (tool.Result, error) {
	return tool.Result{}, nil
}

// fakeBuiltin 内置记忆面的替身：接管前必须问得到，接管后必须问不到。
type fakeBuiltin struct{}

func (fakeBuiltin) Recall(context.Context, conversation.ID, port.MemoryQuery) (port.MemoryBlock, error) {
	return port.MemoryBlock{Text: "builtin-memory", Detail: "index"}, nil
}

// newMemoryWiring 带四件内置记忆工具、禁用集持有与记忆路由的 wiring。
func newMemoryWiring(t *testing.T, factories ...v1.Factory) *wiring {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := toolrun.New(toolrun.Options{})
	for _, n := range memoryToolNames {
		runner.Add(fixedTool{name: n})
	}
	w := &wiring{
		profileDir:    dir,
		lang:          i18n.LangZh,
		cfg:           &fileConfig{},
		runner:        runner,
		persist:       &cfgWriter{path: cfgPath},
		disabledTools: newDisabledHolder(map[string]bool{}),
		memRoute:      newMemoryRoute(fakeBuiltin{}, nil),
	}
	w.plugins1 = tierone.New(tierone.Options{
		Factories: factories,
		NewHost:   w.pluginHostFor(nil),
		Log:       w.logFrom,
	})
	return w
}

// TestMemoryTakeoverRemovesBuiltinTools 启用记忆插件：内置四工具从模型面撤下，插件自己的
// 同名工具顶上；停用后原样还回来（D337 决策 3 / D340 装配顺序）。
func TestMemoryTakeoverRemovesBuiltinTools(t *testing.T) {
	w := newMemoryWiring(t, memFactory{name: "memplug"})
	admin := tierOneAdmin{w: w}
	ctx := context.Background()

	if err := admin.Enable(ctx, "memplug"); err != nil {
		t.Fatalf("启用失败：%v", err)
	}
	if got := w.memRoute.Owner(); got != "memplug" {
		t.Fatalf("接管者 = %q, want memplug", got)
	}
	blk, err := w.memRoute.Recall(ctx, "conv", port.MemoryQuery{})
	if err != nil || !strings.Contains(blk.Text, "sit for 20 minutes") {
		t.Fatalf("接管后应问插件：%+v / %v", blk, err)
	}
	if blk.Detail != "plugin:memplug" {
		t.Fatalf("段细目 = %q", blk.Detail)
	}
	specs, err := w.runner.Specs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSpec(t, specs, "memory_list") {
		t.Fatal("插件的同名工具应出现在列表里")
	}
	for _, n := range []string{"memory_read", "memory_search", "memory_write"} {
		if hasSpec(t, specs, n) {
			t.Fatalf("内置 %s 应已退出模型面", n)
		}
	}
	if w.disabledTools.snapshot()["memory_write"] {
		t.Fatal("接管集不该写进用户的禁用集")
	}

	if err := admin.Disable("memplug"); err != nil {
		t.Fatalf("停用失败：%v", err)
	}
	if got := w.memRoute.Owner(); got != "" {
		t.Fatalf("停用后应交还内置，实际接管者 %q", got)
	}
	if blk, _ := w.memRoute.Recall(ctx, "conv", port.MemoryQuery{}); !strings.Contains(blk.Text, "builtin-memory") {
		t.Fatalf("交还后应问内置：%+v", blk)
	}
	specs, err = w.runner.Specs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range memoryToolNames {
		if !hasSpec(t, specs, n) {
			t.Fatalf("交还后内置 %s 应回来", n)
		}
	}
}

// TestMemoryTakeoverRefusesSecondOwner 两个记忆插件不能同时接管：第二份被拒之后，
// 内置面与第一份的接管都必须原样保留（不能因为一次被拒的启用把已生效的接管撤掉）。
func TestMemoryTakeoverRefusesSecondOwner(t *testing.T) {
	w := newMemoryWiring(t, memFactory{name: "mem1"}, memFactory{name: "mem2"})
	ctx := context.Background()
	if err := w.setTierOne(ctx, "mem1", true, false); err != nil {
		t.Fatalf("第一份启用失败：%v", err)
	}
	if err := w.setTierOne(ctx, "mem2", true, false); err == nil {
		t.Fatal("第二份记忆插件应被拒")
	}
	if got := w.memRoute.Owner(); got != "mem1" {
		t.Fatalf("被拒的那次不该动接管者：%q", got)
	}
	if blk, _ := w.memRoute.Recall(ctx, "conv", port.MemoryQuery{}); !strings.Contains(blk.Text, "sit for 20 minutes") {
		t.Fatalf("接管应仍在第一份手里：%+v", blk)
	}
}

// TestMemoryTakeoverRevertsOnFailure 启用失败不得带走内置记忆面：一次失败的用户操作
// 不该让记忆从此消失。
func TestMemoryTakeoverRevertsOnFailure(t *testing.T) {
	w := newMemoryWiring(t, brokenMemFactory{})
	ctx := context.Background()
	if err := w.setTierOne(ctx, "badmem", true, false); err == nil {
		t.Fatal("造不出来的插件应启用失败")
	}
	if got := w.memRoute.Owner(); got != "" {
		t.Fatalf("失败后应回到内置，实际接管者 %q", got)
	}
	specs, err := w.runner.Specs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range memoryToolNames {
		if !hasSpec(t, specs, n) {
			t.Fatalf("失败后内置 %s 应仍在", n)
		}
	}
}
