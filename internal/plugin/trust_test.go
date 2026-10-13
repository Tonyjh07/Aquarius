package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trustHost 搭一个只走插件目录发现的 host（D138 用）。第三个返回是**落盘态取值函数**
// ——Persist 发生在 Start 之后，按值返回 map 头（彼时为 nil）拿不到后写入。
func trustHost(t *testing.T, manifest string, states map[string]State) (*Host, *fakeConfirm, func() map[string]State, *fakeDial) {
	t.Helper()
	dir := t.TempDir()
	if manifest != "" {
		if err := os.MkdirAll(filepath.Join(dir, "p"), 0o755); err != nil {
			t.Fatal(err)
		}
		write := filepath.Join(dir, "p", "plugin.json")
		if err := os.WriteFile(write, []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dial := &fakeDial{}
	conf := &fakeConfirm{answers: []bool{true, true, true, true}}
	var persisted map[string]State
	h := New(Deps{
		Dial:       dial.dialer(),
		Confirm:    conf,
		PluginsDir: dir,
		States:     states,
		Persist: func(s map[string]State) error {
			persisted = s
			return nil
		},
	})
	return h, conf, func() map[string]State { return persisted }, dial
}

// stdioManifest 无 capabilities 的插件目录清单——正是旧路径下会被静默 exec 的形态。
func stdioManifest() string {
	return `{"name":"p","mcp":{"transport":"stdio","command":"evil-mcp","args":["--x"]}}`
}

// TestTrustGateAsksOnceForPluginsDir D138：插件目录发现的条目**首次必须确认**才连接
// （此前 capabilities 为空即静默启动 = 丢个 plugin.json 进去就 exec），提示里带启动方式
// 原文（用户据此判断要不要信），确认后指纹落盘。
func TestTrustGateAsksOnceForPluginsDir(t *testing.T) {
	h, conf, persisted, dial := trustHost(t, stdioManifest(), nil)
	h.Start(context.Background())
	defer h.Close()

	if statusOf(h, "p") != StatusReady {
		t.Fatalf("status = %s, want ready（确认通过后应连接）", statusOf(h, "p"))
	}
	if dial.callCount() != 1 {
		t.Fatalf("dial = %d, want 1", dial.callCount())
	}
	if len(conf.prompts) != 1 {
		t.Fatalf("信任提示 %d 条 = %v, want 1", len(conf.prompts), conf.prompts)
	}
	if !strings.Contains(conf.prompts[0], "evil-mcp") ||
		!strings.Contains(conf.prompts[0], SourcePlugins) {
		t.Fatalf("提示 %q, want 含启动命令与来源标注", conf.prompts[0])
	}
	if persisted()["p"].Trusted == "" {
		t.Fatal("确认后未落盘 trust 指纹（下次启动会再问）")
	}
}

// TestTrustGateConfigSourceIsExplicitTrust config `mcpServers` 写的条目 = 用户显式信任，
// 不过门（否则每次启动都为自己的配置弹窗）。
func TestTrustGateConfigSourceIsExplicitTrust(t *testing.T) {
	dial := &fakeDial{}
	conf := &fakeConfirm{answers: []bool{true}}
	h := New(Deps{
		Dial:          dial.dialer(),
		Confirm:       conf,
		ConfigServers: map[string]MCPServer{"web": stdioDecl()},
	})
	h.Start(context.Background())
	defer h.Close()

	if statusOf(h, "web") != StatusReady {
		t.Fatalf("status = %s, want ready", statusOf(h, "web"))
	}
	if len(conf.prompts) != 0 {
		t.Fatalf("config 源不应弹信任提示: %v", conf.prompts)
	}
}

// TestTrustGateDeniedFailClosed 拒绝信任 → revoked 且不拨号（fail-closed）。
func TestTrustGateDeniedFailClosed(t *testing.T) {
	h, conf, _, dial := trustHost(t, stdioManifest(), nil)
	conf.answers = []bool{false} // 拒绝
	h.Start(context.Background())
	defer h.Close()

	if statusOf(h, "p") != StatusRevoked {
		t.Fatalf("status = %s, want revoked（拒绝信任）", statusOf(h, "p"))
	}
	if dial.callCount() != 0 {
		t.Fatalf("拒绝信任仍拨号 = %d，want 0", dial.callCount())
	}
}

// TestTrustGateSecondStartSilent 已信任且描述未变 → 第二次 Start 不再问、仍能连接。
func TestTrustGateSecondStartSilent(t *testing.T) {
	m := stdioManifest()
	h, conf, persisted, dial := trustHost(t, m, nil)
	h.Start(context.Background())
	h.Close()

	if len(conf.prompts) != 1 {
		t.Fatalf("首次启动应问一次: %v", conf.prompts)
	}
	trusted := persisted()["p"].Trusted
	if trusted == "" {
		t.Fatal("首次启动未写入信任指纹")
	}

	// 同一目录、把上次的指纹作为初值 States 传进来（等价于从 config 读回）。
	h2, conf2, _, dial2 := trustHost(t, m, map[string]State{"p": {Trusted: trusted}})
	h2.Start(context.Background())
	defer h2.Close()

	if statusOf(h2, "p") != StatusReady {
		t.Fatalf("status = %s, want ready", statusOf(h2, "p"))
	}
	if len(conf2.prompts) != 0 {
		t.Fatalf("已信任还被问了: %v", conf2.prompts)
	}
	if dial.callCount() != 1 || dial2.callCount() != 1 {
		t.Fatalf("dial = %d/%d, want 各 1", dial.callCount(), dial2.callCount())
	}
}

// TestTrustGateChangedLaunchReAsks 启动描述被改（command 变了）→ 指纹不符 → 重新问，
// 提示里显示**新**命令。这是「先信任后偷换」的防线。
func TestTrustGateChangedLaunchReAsks(t *testing.T) {
	h, _, persisted, _ := trustHost(t, stdioManifest(), nil)
	h.Start(context.Background())
	h.Close()
	old := persisted()["p"].Trusted

	h2, conf2, _, _ := trustHost(t,
		`{"name":"p","mcp":{"transport":"stdio","command":"other-mcp","args":["--x"]}}`,
		map[string]State{"p": {Trusted: old}})
	h2.Start(context.Background())
	defer h2.Close()

	if len(conf2.prompts) != 1 || !strings.Contains(conf2.prompts[0], "other-mcp") {
		t.Fatalf("改了启动命令应重新问并显示新命令: %v", conf2.prompts)
	}
}

// TestTrustGateNoConfirmerFailClosed 无 Confirmer（headless 装配缺失等）时 fail-closed：
// 不连接、revoked，不静默 exec。
func TestTrustGateNoConfirmerFailClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p", "plugin.json"), []byte(stdioManifest()), 0o644); err != nil {
		t.Fatal(err)
	}
	dial := &fakeDial{}
	h := New(Deps{Dial: dial.dialer(), PluginsDir: dir})
	h.Start(context.Background())
	defer h.Close()

	if statusOf(h, "p") != StatusRevoked {
		t.Fatalf("status = %s, want revoked（无确认器不得静默启动）", statusOf(h, "p"))
	}
	if dial.callCount() != 0 {
		t.Fatalf("无确认器仍拨号 = %d, want 0", dial.callCount())
	}
}
