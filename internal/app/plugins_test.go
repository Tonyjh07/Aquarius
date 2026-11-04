package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeAdmin PluginAdmin 测试替身。
type fakeAdmin struct {
	list      []PluginInfo
	enabled   []string
	disabled  []string
	enableErr error
	logsOut   string
	logsErr   error
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

// Logs S6/D143：/plugin logs 测试替身。
func (f *fakeAdmin) Logs(name string) (string, error) {
	return f.logsOut, f.logsErr
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
		i18n.Tf("plugin.list.head", "n", "2"), "[ready]", "web", "config/stdio",
		i18n.Tf("plugin.list.caps", "caps",
			"network"+i18n.T("plugin.cap.granted")+",fs"+i18n.T("plugin.cap.ungranted")),
		i18n.Tf("plugin.list.calls", "calls", "12") + i18n.Tf("plugin.list.errors", "errors", "1"),
		"[failed]", "bad", "未知 risk",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list 缺 %q: %s", want, out)
		}
	}

	out, err = s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin", Args: []string{"enable", "web"}}})
	if err != nil || !strings.Contains(out, i18n.Tf("plugin.enabled_status", "name", "web", "status", "ready")) {
		t.Fatalf("enable = %q, %v", out, err)
	}
	if len(admin.enabled) != 1 || admin.enabled[0] != "web" {
		t.Fatalf("enabled = %v", admin.enabled)
	}

	out, err = s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin", Args: []string{"disable", "web"}}})
	if err != nil || !strings.Contains(out, i18n.Tf("plugin.disabled", "name", "web")) {
		t.Fatalf("disable = %q, %v", out, err)
	}
	if len(admin.disabled) != 1 {
		t.Fatalf("disabled = %v", admin.disabled)
	}

	// 用法错误：每个错法对应各自的用法行。
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"enable"}, i18n.T("plugin.usage.enable")},
		{[]string{"disable", "a", "b"}, i18n.T("plugin.usage.disable")},
		{[]string{"bogus"}, i18n.T("plugin.usage")},
	} {
		if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin", Args: tc.args}}); err == nil ||
			err.Error() != tc.want {
			t.Fatalf("args=%v err=%v, want %q", tc.args, err, tc.want)
		}
	}
	// 未配置宿主。
	empty := &Session{}
	if _, err := empty.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin"}}); err == nil ||
		!strings.Contains(err.Error(), i18n.T("plugin.unwired")) {
		t.Fatalf("err = %v, want 未配置报因", err)
	}
}

// TestSessionPluginEmptyList 空声明列表的友好提示。
func TestSessionPluginEmptyList(t *testing.T) {
	s := &Session{plugins: &fakeAdmin{}}
	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "plugin"}})
	if err != nil || !strings.Contains(out, i18n.T("plugin.list.empty")) {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

// TestSessionPluginLogs /plugin logs [name]（S6/D143）：无参/带名透传、用法错误、
// 实现方报错上抛。
func TestSessionPluginLogs(t *testing.T) {
	ctx := context.Background()
	s := &Session{plugins: &fakeAdmin{logsOut: "插件 web 摘要…"}}

	out, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin", Args: []string{"logs"}}})
	if err != nil || !strings.Contains(out, "插件 web 摘要") {
		t.Fatalf("logs 无参 = %q, %v", out, err)
	}
	out, err = s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin", Args: []string{"logs", "web"}}})
	if err != nil || !strings.Contains(out, "插件 web 摘要") {
		t.Fatalf("logs 带名 = %q, %v", out, err)
	}
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin", Args: []string{"logs", "a", "b"}}}); err == nil ||
		!strings.Contains(err.Error(), i18n.T("plugin.usage.logs")) {
		t.Fatalf("logs 多参 err = %v, want 用法错误", err)
	}

	fail := &Session{plugins: &fakeAdmin{logsErr: errors.New("读取审计日志失败")}}
	if _, err := fail.Handle(ctx, port.UserInput{Command: &port.Command{Name: "plugin", Args: []string{"logs", "web"}}}); err == nil ||
		!strings.Contains(err.Error(), "读取审计日志失败") {
		t.Fatalf("logs 实现方报错未上抛: %v", err)
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
	}, nil) // meta 缺省 = nil（D103：按名生成无描述条目）
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
	s.SetDynamicCommands(map[string]CommandHandler{}, nil)
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "mcp:fake:greet"}}); err == nil ||
		!strings.Contains(err.Error(), i18n.Tf("session.unknown_command", "name", "mcp:fake:greet")) {
		t.Fatalf("err = %v, want 未知命令", err)
	}
}
