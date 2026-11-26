package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/plugin/tierone"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// fakeSink 收下合并结果，供断言最后交出去的那张表。
type fakeSink struct {
	cmds map[string]app.CommandHandler
	meta map[string]port.CommandInfo
}

func (f *fakeSink) SetDynamicCommands(cmds map[string]app.CommandHandler, meta map[string]port.CommandInfo) {
	f.cmds, f.meta = cmds, meta
}

func newFakeSink() *fakeSink { return &fakeSink{} }

// cmdPlugin 一个只声明命令面的 Tier-1 插件。
type cmdPlugin struct {
	name string
	spec v1.CommandSpec
}

func (p *cmdPlugin) Manifest() v1.Manifest {
	return v1.Manifest{Name: p.name, API: v1.APIVersion, Provides: []string{"command"}}
}
func (p *cmdPlugin) Create(v1.Host) (v1.Instance, error) { return p, nil }
func (p *cmdPlugin) Start() error                        { return nil }
func (p *cmdPlugin) Shutdown(context.Context) error      { return nil }
func (p *cmdPlugin) Commands() []v1.CommandSpec          { return []v1.CommandSpec{p.spec} }
func (p *cmdPlugin) Run(_ context.Context, name string, args []string) (string, error) {
	return "ran " + name + " " + strings.Join(args, " "), nil
}

// hostForRegistry Tier-1 的 Create 需要一个 Host；命令面用不到它的任何能力。
type hostForRegistry struct{}

func (hostForRegistry) Logger() v1.Logger          { return nil }
func (hostForRegistry) Secrets() v1.SecretResolver { return nil }
func (hostForRegistry) Blobs() v1.BlobStore        { return nil }
func (hostForRegistry) DataDir() string            { return "" }
func (hostForRegistry) WorkDir() string            { return "" }
func (hostForRegistry) Locale() string             { return "zh" }
func (hostForRegistry) Notify(string, string)      {}
func (hostForRegistry) Confirm(context.Context, v1.ConfirmRequest) (v1.ConfirmAnswer, error) {
	return v1.ConfirmAnswer{Allow: true}, nil
}

func newTestWiring(t *testing.T, plugins ...v1.Factory) (*wiring, *fakeSink) {
	t.Helper()
	sink := newFakeSink()
	w := &wiring{
		cmdTables: newCmdTables(func() cmdSink { return sink }),
		plugins1: tierone.New(tierone.Options{
			Factories: plugins,
			NewHost:   func(v1.Manifest) v1.Host { return hostForRegistry{} },
		}),
	}
	return w, sink
}

// 两个来源都在：MCP 刷新与前后的 Tier-1 重建不能互相清空。
func TestCommandTablesMergeBothSources(t *testing.T) {
	w, sink := newTestWiring(t, &cmdPlugin{
		name: "mem",
		spec: v1.CommandSpec{Names: []string{"mem", "m"}, Usage: "<id>", Desc: "记忆"},
	})
	if err := w.plugins1.SetEnabled(context.Background(), "mem", true); err != nil {
		t.Fatal(err)
	}
	w.rebuildTierOneCommands()

	// MCP 之后刷新一次：Tier-1 的命令必须还在。
	sink.cmds = nil
	w.cmdTables.setMCP(map[string]app.CommandHandler{
		"mcp:srv:ask": func(context.Context, []string) (string, error) { return "", nil },
	}, map[string]port.CommandInfo{"mcp:srv:ask": {Names: []string{"mcp:srv:ask"}}})

	for _, want := range []string{"mem", "m", "mcp:srv:ask"} {
		if _, ok := sink.cmds[want]; !ok {
			t.Errorf("合并后缺 %q：%v", want, keysOf(sink.cmds))
		}
	}
	if _, ok := sink.meta["m"]; !ok {
		t.Errorf("别名也该带清单元数据：%v", keysOfInfo(sink.meta))
	}
}

// 同名时 MCP 赢（与「静态优先」同一方向：只留一条，不留两个看起来一样的东西）。
func TestCommandTablesMCPWinsOnCollision(t *testing.T) {
	w, sink := newTestWiring(t, &cmdPlugin{
		name: "p1",
		spec: v1.CommandSpec{Names: []string{"dup"}},
	})
	if err := w.plugins1.SetEnabled(context.Background(), "p1", true); err != nil {
		t.Fatal(err)
	}
	w.rebuildTierOneCommands()

	mcpCalled := false
	w.cmdTables.setMCP(map[string]app.CommandHandler{
		"dup": func(context.Context, []string) (string, error) { mcpCalled = true; return "mcp", nil },
	}, nil)

	out, err := sink.cmds["dup"](context.Background(), nil)
	if err != nil || !mcpCalled || out != "mcp" {
		t.Fatalf("同名命令应走 MCP 那条：out=%q err=%v called=%v", out, err, mcpCalled)
	}
}

// 撞内置命令的名字不注册（不整插件拒载：命令名冲突不影响工具面）。
func TestTierOneCommandSkipsBuiltinName(t *testing.T) {
	w, sink := newTestWiring(t, &cmdPlugin{
		name: "clash",
		spec: v1.CommandSpec{Names: []string{"help", "clashok"}},
	})
	if err := w.plugins1.SetEnabled(context.Background(), "clash", true); err != nil {
		t.Fatal(err)
	}
	w.rebuildTierOneCommands()

	if _, ok := sink.cmds["help"]; ok {
		t.Error("内置命令名不该被插件占用")
	}
	if _, ok := sink.cmds["clashok"]; !ok {
		t.Errorf("同一声明里的另一个名字应该照常注册：%v", keysOf(sink.cmds))
	}
}

// 停用后命令消失：留一条指向已停用插件的名字，用户点了只会得到报错。
func TestTierOneCommandsFollowEnableState(t *testing.T) {
	w, sink := newTestWiring(t, &cmdPlugin{
		name: "gone",
		spec: v1.CommandSpec{Names: []string{"gone"}},
	})
	if err := w.plugins1.SetEnabled(context.Background(), "gone", true); err != nil {
		t.Fatal(err)
	}
	w.rebuildTierOneCommands()
	if _, ok := sink.cmds["gone"]; !ok {
		t.Fatal("启用后应有命令")
	}

	if err := w.plugins1.SetEnabled(context.Background(), "gone", false); err != nil {
		t.Fatal(err)
	}
	w.rebuildTierOneCommands()
	if _, ok := sink.cmds["gone"]; ok {
		t.Error("停用后命令该消失")
	}
}

// 分派时把内核侧的会话身份搬到插件侧：命令与工具看到的是同一个会话。
func TestTierOneCommandCarriesSession(t *testing.T) {
	var seen string
	plug := &sessionEchoPlugin{name: "echo", got: &seen}
	w, _ := newTestWiring(t, plug)
	if err := w.plugins1.SetEnabled(context.Background(), "echo", true); err != nil {
		t.Fatal(err)
	}
	w.rebuildTierOneCommands()

	h := w.tierOneCommandHandler("echo", "echo")
	if _, err := h(port.WithSessionID(context.Background(), "conv-7"), nil); err != nil {
		t.Fatal(err)
	}
	if seen != "conv-7" {
		t.Errorf("插件侧会话：想要 conv-7，得到 %q", seen)
	}
}

// sessionEchoPlugin 把收到的会话 ID 写回测试变量。
type sessionEchoPlugin struct {
	name string
	got  *string
}

func (p *sessionEchoPlugin) Manifest() v1.Manifest {
	return v1.Manifest{Name: p.name, API: v1.APIVersion, Provides: []string{"command"}}
}
func (p *sessionEchoPlugin) Create(v1.Host) (v1.Instance, error) { return p, nil }
func (p *sessionEchoPlugin) Start() error                        { return nil }
func (p *sessionEchoPlugin) Shutdown(context.Context) error      { return nil }
func (p *sessionEchoPlugin) Commands() []v1.CommandSpec {
	return []v1.CommandSpec{{Names: []string{"echo"}}}
}
func (p *sessionEchoPlugin) Run(ctx context.Context, _ string, _ []string) (string, error) {
	id, _ := v1.SessionFrom(ctx)
	*p.got = id
	return "", nil
}

func keysOf(m map[string]app.CommandHandler) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfInfo(m map[string]port.CommandInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
