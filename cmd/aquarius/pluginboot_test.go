package main

// pluginboot_test.go 启动期恢复（startTierOne）与 /plugin 投影的负路径（D355 复核结论）：
// 软启动——单插件失败只记一行、继续下一个；校验失败与启用失败都落在 LastErr 上，
// 用户能在 /plugin 看到原因；两个记忆插件同时 enabled 时第一个接管、第二个被拒。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/logging"
	"github.com/Tonyjh07/Aquarius/internal/plugin/tierone"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// bootFactory 可配置成败的最小工厂：名字、供给面与 Create 行为都由测试给。
type bootFactory struct {
	name      string
	provides  []string
	createErr error
}

func (f bootFactory) Manifest() v1.Manifest {
	return v1.Manifest{Name: f.name, API: v1.APIVersion, Provides: f.provides}
}

func (f bootFactory) Create(h v1.Host) (v1.Instance, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return bootInstance{}, nil
}

type bootInstance struct{}

func (bootInstance) Start() error                   { return nil }
func (bootInstance) Shutdown(context.Context) error { return nil }
func (bootInstance) Recall(ctx context.Context, req v1.MemoryRequest) (v1.MemoryContext, error) {
	return v1.MemoryContext{Text: "boot-memory"}, nil
}
func (bootInstance) Tools(context.Context) []v1.ToolSpec { return nil }
func (bootInstance) Execute(context.Context, v1.ToolCall) (v1.ToolResult, error) {
	return v1.ToolResult{}, nil
}

// newBootWiring 与 newMemoryWiring 同形：真 tierone + 真 plugingo Host + 记忆路由，
// config 内容由调用方给（启动恢复读的是 cfg.Plugins）。
func newBootWiring(t *testing.T, cfgJSON string, factories ...v1.Factory) (*wiring, *strings.Builder) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := writeFileE2E(cfgPath, cfgJSON); err != nil {
		t.Fatal(err)
	}
	var logBuf strings.Builder
	w := &wiring{
		log:           logging.Open(logging.Options{Echo: &logBuf, Level: logging.LevelDebug, Format: logging.FormatJSON}),
		profileDir:    dir,
		lang:          i18n.LangZh,
		cfg:           readBootCfg(t, cfgJSON),
		runner:        toolrun.New(toolrun.Options{}),
		persist:       &cfgWriter{path: cfgPath},
		disabledTools: newDisabledHolder(map[string]bool{}),
		memRoute:      newMemoryRoute(fakeBuiltin{}, nil),
	}
	w.plugins1 = tierone.New(tierone.Options{
		Factories: factories,
		NewHost:   w.pluginHostFor(nil),
		Log:       w.logFrom,
	})
	return w, &logBuf
}

func TestStartTierOneBootRestore(t *testing.T) {
	// 缺键 = 启用（与 Tier-2 的 D31 语义一致）：无配置也恢复。
	w, notes := newBootWiring(t, `{}`, bootFactory{name: "booty", provides: []string{"tool"}})
	w.startTierOne(context.Background())
	if _, _, active := w.plugins1.Active("booty"); !active {
		t.Fatal("缺键应恢复启用")
	}
	if !strings.Contains((*notes).String(), "log.plugin.t1_active") {
		t.Fatalf("启动恢复应落日志：%v", *notes)
	}

	// enabled:true 显式恢复；enabled:false 不启用。
	w2, _ := newBootWiring(t, `{"plugins":{"booty":{"enabled":false}}}`,
		bootFactory{name: "booty", provides: []string{"tool"}})
	w2.startTierOne(context.Background())
	if _, _, active := w2.plugins1.Active("booty"); active {
		t.Fatal("enabled:false 不应启用")
	}
}

func TestStartTierOneSoftFailure(t *testing.T) {
	// Create 失败：warn 一行继续，插件停在 Stopped 且 Err 可从 /plugin 投影读到。
	w, notes := newBootWiring(t, `{}`,
		bootFactory{name: "bad", provides: []string{"tool"}, createErr: errors.New("boom")},
		bootFactory{name: "good", provides: []string{"tool"}})
	w.startTierOne(context.Background())
	if _, _, active := w.plugins1.Active("good"); !active {
		t.Fatal("前一个失败不应挡住下一个（软启动）")
	}
	if !strings.Contains((*notes).String(), "log.plugin.t1_refused") {
		t.Fatalf("启用失败应记 refused warn：%v", *notes)
	}
	rec, _ := w.plugins1.Record("bad")
	if rec.State != tierone.StateStopped || rec.Err == nil {
		t.Fatalf("rec = %+v, want Stopped + Err", rec)
	}
	admin := tierOneAdmin{w}
	var lastErr string
	for _, p := range admin.Plugins() {
		if p.Name == "bad" {
			lastErr = p.LastErr
		}
	}
	if lastErr == "" {
		t.Fatal("/plugin 投影缺 LastErr")
	}
}

func TestStartTierOneTwoMemoryOwners(t *testing.T) {
	// 两个记忆插件同时 enabled：第一个接管，第二个被拒且接管者不受影响。
	w, _ := newBootWiring(t, `{}`,
		bootFactory{name: "memA", provides: []string{"memory"}},
		bootFactory{name: "memB", provides: []string{"memory"}})
	w.startTierOne(context.Background())
	if w.memRoute.Owner() != "memA" {
		t.Fatalf("owner = %q, want memA", w.memRoute.Owner())
	}
	rec, _ := w.plugins1.Record("memB")
	if rec.State != tierone.StateStopped || rec.Err == nil {
		t.Fatalf("第二个记忆插件应被拒：%+v", rec)
	}
	if _, _, active := w.plugins1.Active("memA"); !active {
		t.Fatal("接管者应保持 active")
	}
}

func TestStartTierOneValidationFailureSkipped(t *testing.T) {
	// 清单校验失败（API 版本不符）：注册表软启动已把它停住，恢复直接跳过，不碰别的。
	bad := brokenBootFactory{}
	w, notes := newBootWiring(t, `{}`, bad, bootFactory{name: "good", provides: []string{"tool"}})
	w.startTierOne(context.Background())
	if _, _, active := w.plugins1.Active("good"); !active {
		t.Fatal("校验失败不应挡住下一个")
	}
	if rec, _ := w.plugins1.Record(bad.Manifest().Name); rec.Err == nil {
		t.Fatalf("校验失败应落 Err：%+v", rec)
	}
	if strings.Contains((*notes).String(), "log.plugin.t1_refused") {
		t.Fatal("校验失败走 skip，不该有 refused warn")
	}
}

// brokenBootFactory 清单就过不了审的工厂（API 版本不符）。
type brokenBootFactory struct{}

func (brokenBootFactory) Manifest() v1.Manifest {
	return v1.Manifest{Name: "broken", API: v1.APIVersion + 99, Provides: []string{"tool"}}
}

func (brokenBootFactory) Create(v1.Host) (v1.Instance, error) { return nil, errors.New("unreachable") }

func writeFileE2E(p, body string) error { return os.WriteFile(p, []byte(body), 0o600) }

func readBootCfg(t *testing.T, body string) *fileConfig {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "config.json")
	if err := writeFileE2E(tmp, body); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(tmp)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return c
}
