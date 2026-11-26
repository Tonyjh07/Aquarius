package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/plugin/tierone"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// t1Factory 一个最小的工具类 Tier-1 插件：声明 tool、实现 ToolProvider，工具清单随轮给。
type t1Factory struct{}

func (t1Factory) Manifest() v1.Manifest {
	return v1.Manifest{Name: "memplug", API: v1.APIVersion, Provides: []string{"tool"}}
}

func (t1Factory) Create(v1.Host) (v1.Instance, error) { return t1Instance{}, nil }

type t1Instance struct{}

func (t1Instance) Start() error                   { return nil }
func (t1Instance) Shutdown(context.Context) error { return nil }
func (t1Instance) Tools(context.Context) []v1.ToolSpec {
	return []v1.ToolSpec{{Name: "memplug_list", Description: "list memories"}}
}
func (t1Instance) Execute(context.Context, v1.ToolCall) (v1.ToolResult, error) {
	return v1.ToolResult{}, nil
}

// newTierOneWiring 装配一个只含 Tier-1 与 Runner 的 wiring；config 里带一条 Tier-2 条目，
// 用来守「写回只改自己那一个键」。
func newTierOneWiring(t *testing.T) *wiring {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	body := `{"plugins":{"mcp1":{"enabled":true,"granted":["network"]}},"model":{"name":"x"}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &wiring{
		profileDir: dir,
		lang:       i18n.LangZh,
		cfg:        &fileConfig{},
		runner:     toolrun.New(toolrun.Options{}),
		persist:    &cfgWriter{path: cfgPath},
	}
	w.plugins1 = tierone.New(tierone.Options{
		Factories: []v1.Factory{t1Factory{}},
		NewHost:   w.pluginHostFor(nil),
		Log:       w.logFrom,
	})
	return w
}

// TestTierOneEnableAttachesToolSet 启用即挂工具源、停用即摘：这是「插件工具进模型面」的
// 唯一路径（D340 挂的是源，不是一次性注册的条目）。
func TestTierOneEnableAttachesToolSet(t *testing.T) {
	w := newTierOneWiring(t)
	admin := tierOneAdmin{w: w}
	ctx := context.Background()

	if err := admin.Enable(ctx, "memplug"); err != nil {
		t.Fatalf("启用失败：%v", err)
	}
	specs, err := w.runner.Specs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSpec(t, specs, "memplug_list") {
		t.Fatal("启用后工具的声明应出现在 Specs 里")
	}

	if err := admin.Disable("memplug"); err != nil {
		t.Fatalf("停用失败：%v", err)
	}
	specs, err = w.runner.Specs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hasSpec(t, specs, "memplug_list") {
		t.Fatal("停用后工具的声明应从 Specs 里消失")
	}
}

// TestTierOnePluginsProjection /plugin list 要能看出这是编译进二进制的插件、提供哪个面。
func TestTierOnePluginsProjection(t *testing.T) {
	w := newTierOneWiring(t)
	admin := tierOneAdmin{w: w}
	list := admin.Plugins()
	if len(list) != 1 {
		t.Fatalf("应有一条 Tier-1 记录，得到 %d", len(list))
	}
	p := list[0]
	if p.Name != "memplug" || p.Source != "binary" || p.Transport != "in-process" {
		t.Fatalf("投影字段不对：%+v", p)
	}
	if len(p.Provides) != 1 || p.Provides[0] != "tool" {
		t.Fatalf("供给面应透出：%+v", p.Provides)
	}
	if p.Enabled {
		t.Fatal("启动态应为停用（配置没写 enabled，但启动恢复还没跑）")
	}
}

// TestTierOnePersistTouchesOnlyOwnKey 写回只改 plugins.<name>.enabled：Tier-2 的 granted
// 等字段由宿主自己维护，整体重写会拿旧快照覆盖别人刚写的值。
func TestTierOnePersistTouchesOnlyOwnKey(t *testing.T) {
	w := newTierOneWiring(t)
	admin := tierOneAdmin{w: w}
	if err := admin.Enable(context.Background(), "memplug"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(w.persist.path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Plugins map[string]map[string]any `json:"plugins"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if on, ok := got.Plugins["memplug"]["enabled"].(bool); !ok || !on {
		t.Fatalf("应写回 enabled=true：%v", got.Plugins["memplug"])
	}
	if g, ok := got.Plugins["mcp1"]["granted"].([]any); !ok || len(g) != 1 || g[0] != "network" {
		t.Fatalf("Tier-2 的 granted 被冲掉了：%v", got.Plugins["mcp1"])
	}
}

// hasSpec 声明清单里是否有这个名字。
func hasSpec(t *testing.T, specs []tool.Spec, name string) bool {
	t.Helper()
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}
