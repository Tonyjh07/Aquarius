package main

// 本机密钥库在装配根的测试（D151/D161）：读链的解析顺序（库 → 同名环境变量 → 报因）、
// 库解不开时警告 + 兜底、写入口径（明文入库 + 改引用）、以及**守卫**——日志与报错
// 字符串里不得出现密钥值（§9）。

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// testSecrets 测试用 port.Secrets（只有环境变量这一档，bin 未接线）：
// 判定矩阵与探测器测试不关心密钥库，用最简档。
func testSecrets(t *testing.T) port.Secrets {
	t.Helper()
	return newSecretChain(nil, io.Discard)
}

// testChain 测试用读链 + 库：库落在临时目录（每条测试自成一库）。
func testChain(t *testing.T, warn io.Writer) (*secretChain, *secretbin.Store) {
	t.Helper()
	bin, err := secretbin.New(t.TempDir())
	if err != nil {
		t.Fatalf("secretbin.New: %v", err)
	}
	if warn == nil {
		warn = io.Discard
	}
	return newSecretChain(bin, warn), bin
}

// TestSecretChainEnvFallback 库里没有 → 回落同名环境变量（D161 双查的「兜底」一半）。
func TestSecretChainEnvFallback(t *testing.T) {
	ctx := context.Background()
	ch, _ := testChain(t, nil)
	t.Setenv("AQ_ENV_ONLY", "env-value-1234")
	if got, err := ch.Get(ctx, "AQ_ENV_ONLY"); err != nil || got != "env-value-1234" {
		t.Fatalf("Get = (%q, %v)，want 环境变量兜底", got, err)
	}
}

// TestSecretChainBinWins 库里有 → 用库里的值，不看同名环境变量（D161 双查的「优先」一半）。
func TestSecretChainBinWins(t *testing.T) {
	ctx := context.Background()
	ch, bin := testChain(t, nil)
	t.Setenv("AQ_BOTH", "from-env-9999")
	if err := bin.Set(ctx, "AQ_BOTH", "from-bin-1111"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, err := ch.Get(ctx, "AQ_BOTH"); err != nil || got != "from-bin-1111" {
		t.Fatalf("Get = (%q, %v)，want 库优先", got, err)
	}
}

// TestSecretChainMissNamesBothPlaces 两处都没有 → 报因**点名查过哪两处**，且不回显值。
func TestSecretChainMissNamesBothPlaces(t *testing.T) {
	ctx := context.Background()
	ch, _ := testChain(t, nil)
	_, err := ch.Get(ctx, "AQ_NEITHER_PLACE")
	if err == nil {
		t.Fatal("两处都没有应报因")
	}
	msg := err.Error()
	if !strings.Contains(msg, "本机密钥库") || !strings.Contains(msg, "环境变量") {
		t.Fatalf("报因应点名查过的两处: %q", msg)
	}
	if !strings.Contains(msg, "AQ_NEITHER_PLACE") {
		t.Fatalf("报因应点名缺失的名字: %q", msg)
	}
}

// TestSecretChainUnreadableWarnsAndFallsBack 库解不开 → 一行警告 + 环境变量兜底，
// **不致命**（D161 落点②）；同一份坏文件只警告一次（启动期逐 provider 探测不刷屏）。
func TestSecretChainUnreadableWarnsAndFallsBack(t *testing.T) {
	ctx := context.Background()
	var warn bytes.Buffer
	ch, bin := testChain(t, &warn)
	if err := os.WriteFile(bin.Path(), []byte("broken-blob"), 0o600); err != nil {
		t.Fatalf("埋坏文件: %v", err)
	}
	t.Setenv("AQ_FALLBACK", "env-fallback-4242")
	if got, err := ch.Get(ctx, "AQ_FALLBACK"); err != nil || got != "env-fallback-4242" {
		t.Fatalf("解不开应回落环境变量 = (%q, %v)", got, err)
	}
	if n := strings.Count(warn.String(), "本机密钥库"); n != 1 {
		t.Fatalf("解不开应恰好警告一次，得 %d 次: %q", n, warn.String())
	}
	if !strings.Contains(warn.String(), bin.Path()) {
		t.Fatalf("警告应点名库文件路径: %q", warn.String())
	}
	// 第二次取另一个名字：仍以环境变量兜底，但不再重复警告。
	t.Setenv("AQ_FALLBACK2", "env-fallback-5252")
	if got, err := ch.Get(ctx, "AQ_FALLBACK2"); err != nil || got != "env-fallback-5252" {
		t.Fatalf("第二次取用 = (%q, %v)", got, err)
	}
	if n := strings.Count(warn.String(), "本机密钥库"); n != 1 {
		t.Fatalf("警告应仍只有一次，得 %d 次", n)
	}
}

// TestSecretChainNoBinOnlyEnv 库未接线（bin nil）= 只有环境变量这一档，行为与旧 envSecrets 一致。
func TestSecretChainNoBinOnlyEnv(t *testing.T) {
	ctx := context.Background()
	ch := newSecretChain(nil, io.Discard)
	t.Setenv("AQ_ONLY_ENV", "v-12345678")
	if got, err := ch.Get(ctx, "AQ_ONLY_ENV"); err != nil || got != "v-12345678" {
		t.Fatalf("Get = (%q, %v)", got, err)
	}
	if _, err := ch.Get(ctx, "AQ_NOT_SET"); err == nil {
		t.Fatal("未设置应报因")
	}
}

// TestSecretChainErrorHidesValue 守卫：任何错误串都不得包含密钥值（§9 红线）。
func TestSecretChainErrorHidesValue(t *testing.T) {
	ctx := context.Background()
	const secret = "sk-SUPERSECRET-should-never-appear"
	ch, bin := testChain(t, io.Discard)
	if err := bin.Set(ctx, "AQ_GUARDED", secret); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// 命中路径返回的是值本身（调用方拿去用），但**报错路径**不得带值。
	if err := os.WriteFile(bin.Path(), []byte("broken"), 0o600); err != nil {
		t.Fatalf("埋坏文件: %v", err)
	}
	_, err := ch.Get(ctx, "AQ_GUARDED")
	if err == nil {
		t.Fatal("解不开且无环境变量应报因")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("报错串泄漏了密钥值: %q", err.Error())
	}
}

// TestKeySlotName 键名规则：有引用沿用（每一把都如此）、没有就按 provider 名派生。
func TestKeySlotName(t *testing.T) {
	cases := []struct {
		provider, origRef string
		slot              int
		want              string
	}{
		{"openai", "", 0, "AQUARIUS_OPENAI_API_KEY"},
		{"My-Provider.v2", "", 0, "AQUARIUS_MY_PROVIDER_V2_API_KEY"},
		{"  ", "", 0, "AQUARIUS_PROVIDER_API_KEY"},
		{"openai", "secret:CUSTOM_NAME", 0, "CUSTOM_NAME"},
		{"openai", "secret:", 0, "AQUARIUS_OPENAI_API_KEY"}, // 非法引用 → 回落派生
		{"openai", "sk-plain-ref", 0, "AQUARIUS_OPENAI_API_KEY"},
		// 第 N 把：无引用时按派生名加后缀。
		{"openai", "", 1, "AQUARIUS_OPENAI_API_KEY_2"},
		{"openai", "", 2, "AQUARIUS_OPENAI_API_KEY_3"},
		// 第 N 把已有引用：**沿用**，不再拼后缀（曾经会变成 CUSTOM_2_2_2）。
		{"openai", "secret:CUSTOM", 1, "CUSTOM"},
		{"openai", "secret:AQUARIUS_OPENAI_API_KEY_2", 1, "AQUARIUS_OPENAI_API_KEY_2"},
	}
	for _, c := range cases {
		if got := keySlotName(c.provider, c.origRef, c.slot); got != c.want {
			t.Errorf("keySlotName(%q, %q, %d) = %q，want %q", c.provider, c.origRef, c.slot, got, c.want)
		}
	}
}

// TestDeriveKeyNameNonASCII provider 名里有非 ASCII 内容字符时补指纹：否则「深度求索」
// 与「月之暗面」会折成同一个 AQUARIUS_PROVIDER_API_KEY，两家静默共用一格密钥。
func TestDeriveKeyNameNonASCII(t *testing.T) {
	a := deriveKeyName("深度求索")
	b := deriveKeyName("月之暗面")
	if a == b {
		t.Fatalf("两个中文 provider 名派生出同一键名 %q——会互相覆盖", a)
	}
	for _, n := range []string{a, b} {
		if err := validTestName(n); err != nil {
			t.Errorf("派生键名 %q 不合法: %v", n, err)
		}
	}
	// 同名恒同值（跨进程稳定，用户能手写这个引用）。
	if deriveKeyName("深度求索") != a {
		t.Error("同名应派生出同一键名")
	}
	// 纯 ASCII 与「只差分隔符」的名字保持可读形态，不加指纹。
	for _, c := range []struct{ in, want string }{
		{"deepseek", "AQUARIUS_DEEPSEEK_API_KEY"},
		{"My-Provider.v2", "AQUARIUS_MY_PROVIDER_V2_API_KEY"},
		{"OpenAI 官方", "AQUARIUS_OPENAI_03F5_API_KEY"},
	} {
		if got := deriveKeyName(c.in); got != c.want {
			t.Errorf("deriveKeyName(%q) = %q，want %q", c.in, got, c.want)
		}
	}
}

// validTestName 借用 secretbin 的名字约束做校验（键名最终要能写进库）。
func validTestName(name string) error {
	if name == "" || strings.TrimSpace(name) != name || len(name) > 128 {
		return errors.New("长度/空白不合规")
	}
	if strings.ContainsAny(name, ": \t\r\n") {
		return errors.New("含空白或冒号")
	}
	return nil
}

// TestStorePlaintextKey 写入口径（D161 落点①）：明文 → 入库 + `secret:<名>`；
// 引用 → 原样保留、不动库；库未接线 → 报错（不静默退回明文）。
func TestStorePlaintextKey(t *testing.T) {
	ctx := context.Background()
	bin, err := secretbin.New(t.TempDir())
	if err != nil {
		t.Fatalf("secretbin.New: %v", err)
	}
	ref, err := storePlaintextKey(ctx, bin, "openai", "", "sk-plain-value-1")
	if err != nil {
		t.Fatalf("storePlaintextKey: %v", err)
	}
	if ref != "secret:AQUARIUS_OPENAI_API_KEY" {
		t.Fatalf("引用 = %q", ref)
	}
	if got, _ := bin.Get(ctx, "AQUARIUS_OPENAI_API_KEY"); got != "sk-plain-value-1" {
		t.Fatalf("库里的值 = %q", got)
	}

	// 已是引用：照写、不进库。
	ref2, err := storePlaintextKey(ctx, bin, "openai", "", "secret:EXISTING_REF")
	if err != nil {
		t.Fatalf("引用直写: %v", err)
	}
	if ref2 != "secret:EXISTING_REF" {
		t.Fatalf("引用应原样保留: %q", ref2)
	}
	if _, err := bin.Get(ctx, "EXISTING_REF"); !errors.Is(err, secretbin.ErrNotFound) {
		t.Fatalf("引用型不应写入库: %v", err)
	}

	// 原有引用名沿用（改名不 churn）。
	ref3, err := storePlaintextKey(ctx, bin, "renamed", "secret:AQUARIUS_OPENAI_API_KEY", "sk-second-2222")
	if err != nil {
		t.Fatalf("沿用引用名: %v", err)
	}
	if ref3 != "secret:AQUARIUS_OPENAI_API_KEY" {
		t.Fatalf("应沿用原引用名: %q", ref3)
	}
	if got, _ := bin.Get(ctx, "AQUARIUS_OPENAI_API_KEY"); got != "sk-second-2222" {
		t.Fatalf("沿用名应换值 = %q", got)
	}

	// 库未接线：报错，不静默退回明文。
	if _, err := storePlaintextKey(ctx, nil, "openai", "", "sk-plain-value-2"); err == nil {
		t.Fatal("库未接线应报错")
	}
}

// TestKeySlotNameStableAcrossRewrite 重填明文不换引用名、不攒孤儿条目（回归）。
//
// 复现过的问题：第 2 把钥匙重填明文时，键名取「原引用名」再拼一次 `_2`——原引用名本身
// 已经带 `_2`，于是每重填一次就长一截（CUSTOM_2 → CUSTOM_2_2 → CUSTOM_2_2_2），库里
// 留下一串没人引用的旧条目，而密钥库页把它们全列出来，用户看到一堆不知道是什么的东西。
//
// 判据：同一槽位连续重填三次，引用名恒定；库里条目数不随重填次数增长。
func TestKeySlotNameStableAcrossRewrite(t *testing.T) {
	ctx := context.Background()
	bin, err := secretbin.New(t.TempDir())
	if err != nil {
		t.Fatalf("secretbin.New: %v", err)
	}
	ref := "secret:CUSTOM"
	for round := 1; round <= 3; round++ {
		next, err := storePlaintextKeyAt(ctx, bin, "p", ref, "value-round", 1)
		if err != nil {
			t.Fatalf("第 %d 次重填: %v", round, err)
		}
		if next != ref {
			t.Fatalf("第 %d 次重填后引用变成 %q，want 恒为 %q（重填只换值、不改名）", round, next, ref)
		}
	}
	entries, err := bin.Entries(ctx)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "CUSTOM" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name
		}
		t.Fatalf("重填三次后库条目 = %v，want 只有 CUSTOM（不许攒孤儿）", names)
	}
	if got, _ := bin.Get(ctx, "CUSTOM"); got != "value-round" {
		t.Fatalf("值未更新: %q", got)
	}
}

// TestResolveAPIKeyPlaintextWarnsOnce 明文仍允许但启动警告（D35 兼容），警告不得回显值。
func TestResolveAPIKeyPlaintextWarnsOnce(t *testing.T) {
	var buf bytes.Buffer
	ch, _ := testChain(t, &buf)
	const secret = "sk-plain-INLINE-9999"
	key, err := resolveAPIKey(context.Background(), "p", secret, ch, &buf)
	if err != nil || key != secret {
		t.Fatalf("明文应直接可用: (%q, %v)", key, err)
	}
	if n := strings.Count(buf.String(), "明文"); n != 1 {
		t.Fatalf("明文警告应恰好一次: %d 次，%q", n, buf.String())
	}
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("警告不得回显密钥值: %q", buf.String())
	}
}

// TestResolvePatchKeyNoPlaintextWarn D210（用户实测故障的回归）：探测路径解析草稿里的
// 明文**不得**打落盘警告。
//
// 复现过的误报：欢迎窗「测试连通」与「获取模型」各解析一次输入框里的 key，`resolveAPIKey`
// 见到非 `secret:` 就打「密钥直接落在 config.json」——可那份草稿根本还没落盘（保存时才入库、
// config 只留引用），于是同一次填写在日志里留下两条一模一样的假警告。
//
// 判据：解析结果与 resolveAPIKey 一致（明文照用），但**日志端一个字都不写**；真实构造路径
// 那份警告由 TestResolveAPIKeyPlaintextWarnsOnce 锁定，两边不重复。
func TestResolvePatchKeyNoPlaintextWarn(t *testing.T) {
	var buf bytes.Buffer
	ch, _ := testChain(t, &buf)
	const draft = "sk-draft-NEVER-ON-DISK-1234"
	key, err := resolvePatchKey(context.Background(), "p", draft, ch)
	if err != nil || key != draft {
		t.Fatalf("草稿明文应照用: (%q, %v)", key, err)
	}
	if buf.Len() != 0 {
		t.Fatalf("探测路径不该打落盘警告（草稿还没落盘）: %q", buf.String())
	}
	// 引用形态两边同口径（走 Secrets 按名取用，成功即静默）。
	t.Setenv("AQ_PATCH_REF", "sk-from-env")
	if got, err := resolvePatchKey(context.Background(), "p", "secret:AQ_PATCH_REF", ch); err != nil || got != "sk-from-env" {
		t.Fatalf("引用应正常解析: (%q, %v)", got, err)
	}
	if buf.Len() != 0 {
		t.Fatalf("引用解析不该产生警告: %q", buf.String())
	}
}

// TestWelcomeProbeDoesNotWarnPlaintextDraft D210 端到端那半条：管理器收到的日志端（
// `welcomeManager.stderr`——就是用户的控制台）在「测试连通 / 获取模型」时不得出现落盘警告。
//
// 上一条只锁 resolvePatchKey 本身；这条从 `WelcomeManager.Test` / `Models` 这两个真实入口
// 走一遍，防「哪天有人把它们改回 resolveAPIKey」。
func TestWelcomeProbeDoesNotWarnPlaintextDraft(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	t.Cleanup(srv.Close)

	cfgPath, _ := writeProvidersConfig(t)
	var logBuf bytes.Buffer
	m := &welcomeManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: &logBuf}
	patch := uigui.WelcomePatch{Provider: "draftprov", BaseURL: srv.URL, APIKey: "sk-draft-NEVER-ON-DISK", Models: "m1"}
	if err := m.Test(patch); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if _, err := m.Models(patch); err != nil {
		t.Fatalf("Models: %v", err)
	}
	if s := logBuf.String(); strings.Contains(s, "明文") || strings.Contains(s, "config.json") {
		t.Fatalf("探测不得输出落盘形态警告（草稿还没落盘）: %q", s)
	}
}

// TestBootstrapWiresSecretChain bootstrap 之后读链与库就位（装配顺序契约：
// setupWizard 的探测与真实构造用的是同一份链）。
func TestBootstrapWiresSecretChain(t *testing.T) {
	w := &wiring{stdout: io.Discard, stderr: io.Discard}
	if err := w.bootstrap(filepath.Join(t.TempDir(), "data"), "", "", ""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if w.secrets == nil || w.secretsBin == nil {
		t.Fatalf("bootstrap 应装配读链与库")
	}
	if got := w.secretsBin.Path(); filepath.Dir(got) != w.profileDir {
		t.Fatalf("库应落在 profile 内: %q vs %q", got, w.profileDir)
	}
}
