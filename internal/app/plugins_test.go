package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeAdmin PluginAdmin 测试替身。
type fakeAdmin struct {
	list      []PluginInfo
	enabled   []string
	disabled  []string
	enableErr error
}

func (f *fakeAdmin) Plugins() []PluginInfo { return f.list }

func (f *fakeAdmin) Enable(_ context.Context, name string) error {
	if f.enableErr != nil {
		return f.enableErr
	}
	f.enabled = append(f.enabled, name)
	return nil
}

func (f *fakeAdmin) Disable(name string) error {
	f.disabled = append(f.disabled, name)
	return nil
}

// TestSessionPluginCommand /plugin：list 格式（状态/能力授权/统计/报因）、
// enable/disable、用法错误、未配置报因。
func TestSessionPluginCommand(t *testing.T) {
	admin := &fakeAdmin{list: []PluginInfo{
		{
			Name: "web", Source: "config", Transport: "stdio", Status: "ready",
			Capabilities: []string{"network", "fs"}, Granted: []string{"network"},
			Calls: 12, Errors: 1,
		},
		{Name: "bad", Source: "plugins", Status: "failed", LastErr: "未知 risk \"wild\""},
	}}
	s := &Session{plugins: admin}
	ctx := context.Background()

	out, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{
		"插件（2 个）", "[ready]", "web", "config/stdio",
		"能力:network(已授权),fs(未授权)", "调用12, 失败1",
		"[failed]", "bad", "未知 risk",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list 缺 %q: %s", want, out)
		}
	}

	out, err = s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin", Args: []string{"enable", "web"}}})
	if err != nil || !strings.Contains(out, "已启用 web → ready") {
		t.Fatalf("enable = %q, %v", out, err)
	}
	if len(admin.enabled) != 1 || admin.enabled[0] != "web" {
		t.Fatalf("enabled = %v", admin.enabled)
	}

	out, err = s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin", Args: []string{"disable", "web"}}})
	if err != nil || !strings.Contains(out, "已停用 web") {
		t.Fatalf("disable = %q, %v", out, err)
	}
	if len(admin.disabled) != 1 {
		t.Fatalf("disabled = %v", admin.disabled)
	}

	// 用法错误。
	for _, args := range [][]string{{"enable"}, {"disable", "a", "b"}, {"bogus"}} {
		if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin", Args: args}}); err == nil ||
			!strings.Contains(err.Error(), "用法") {
			t.Fatalf("args=%v err=%v, want 用法错误", args, err)
		}
	}
	// 未配置宿主。
	empty := &Session{}
	if _, err := empty.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin"}}); err == nil ||
		!strings.Contains(err.Error(), "未配置插件宿主") {
		t.Fatalf("err = %v, want 未配置报因", err)
	}
}

// TestSessionPluginEmptyList 空声明列表的友好提示。
func TestSessionPluginEmptyList(t *testing.T) {
	s := &Session{plugins: &fakeAdmin{}}
	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "plugin"}})
	if err != nil || !strings.Contains(out, "没有已声明的插件") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

// TestSessionDynamicCommands 动态命令（§6.1 扩展点 #4）：注册即可执行、
// 静态命令优先、SetDynamicCommands 整体替换、未命中仍报未知。
func TestSessionDynamicCommands(t *testing.T) {
	s := &Session{}
	var gotArgs []string
	s.SetDynamicCommands(map[string]CommandHandler{
		"mcp:fake:greet": func(_ context.Context, args []string) (string, error) {
			gotArgs = append([]string{}, args...)
			return "动态输出", nil
		},
		"help": func(context.Context, []string) (string, error) {
			t.Error("静态 help 应优先于同名动态命令")
			return "", nil
		},
	})
	ctx := context.Background()

	out, err := s.Handle(ctx, port.UserInput{Command: &port.Command{
		Name: "mcp:fake:greet", Args: []string{"tony"},
	}})
	if err != nil || out != "动态输出" {
		t.Fatalf("dynamic = %q, %v", out, err)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "tony" {
		t.Fatalf("args = %v, want [tony]", gotArgs)
	}

	// 静态优先：/help 命中静态分支，同名动态命令不被调用。
	out, err = s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "help"}})
	if err != nil || !strings.Contains(out, "/plugin") {
		t.Fatalf("help = %q, %v", out, err)
	}

	// 整体替换后旧命令注销。
	s.SetDynamicCommands(map[string]CommandHandler{})
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "mcp:fake:greet"}}); err == nil ||
		!strings.Contains(err.Error(), "未知命令") {
		t.Fatalf("err = %v, want 未知命令", err)
	}
}
