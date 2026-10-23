package main

// 欢迎 / 首次运行引导的装配根侧测试（D141）：密钥可解析性判定矩阵（含「明文不阻断」
// 与「探测不打明文警告」）、数据面（快照 / Apply 覆盖首条 / MarkWelcomed / 连通性测试）、
// 装配前启动流三结局（取消 / 继续启动 / 仍不完整报因）、教程文案与真实命令清单对照。
// 一律假宿主 + 假 server，禁真实网络、禁真开窗。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/app"
)

// readCfgMap 读回 config 原文（无关键保留/清点用）。
func readCfgMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
	return m
}

// TestAPIKeyGapMatrix D141 判定矩阵：引用可解析/缺失、空值回落默认引用、明文不报、
// 引用缺变量名；探测**不打印明文警告**（警告只由 resolveAPIKey 真实路径发一次，
// 对照见 TestSetupWizardPlaintextNotBlocked）。
func TestAPIKeyGapMatrix(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		ref     string
		env     string
		envVal  string
		wantGap bool
	}{
		{"引用可解析", "secret:KA", "KA", "k", false},
		{"引用缺失", "secret:KA", "KA", "", true},
		{"空值回落默认引用缺失", "", "AQUARIUS_OPENAI_KEY", "", true},
		{"空值回落默认引用可解析", "", "AQUARIUS_OPENAI_KEY", "k", false},
		{"明文不阻断", "sk-plain", "", "", false},
		{"引用缺变量名", "secret:", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv(c.env, c.envVal)
			}
			gap := apiKeyGap(ctx, providerConfig{Name: "p", Keys: []keyConfig{{APIKey: c.ref}}}, testSecrets(t))
			if (gap != "") != c.wantGap {
				t.Fatalf("gap = %q, wantGap = %v", gap, c.wantGap)
			}
		})
	}
}

// TestMissingKeysCoversFallback D141：primary 与 fallback[] 逐条探测——降级目标缺密钥
// 同样阻断（否则降级时才炸，D110 修订③「提前建客户端」同口径）。
func TestMissingKeysCoversFallback(t *testing.T) {
	ctx := context.Background()
	t.Setenv("KA", "k")
	t.Setenv("KB", "")
	m := &modelConfig{
		Providers: []providerConfig{
			{Name: "a", BaseURL: "http://a/v1", Keys: []keyConfig{{APIKey: "secret:KA"}}, Models: []modelEntry{{Name: "m1"}}},
			{Name: "b", BaseURL: "http://b/v1", Keys: []keyConfig{{APIKey: "secret:KB"}}, Models: []modelEntry{{Name: "mb"}}},
		},
		Primary:  "a",
		Fallback: []fallbackEntry{{Provider: "b"}},
	}
	got := missingKeys(ctx, m, testSecrets(t))
	if len(got) != 1 || !strings.Contains(got[0], `provider "b"`) {
		t.Fatalf("缺失清单 = %+v, want 仅 provider b", got)
	}
	m.Fallback = nil
	if got := missingKeys(ctx, m, testSecrets(t)); len(got) != 0 {
		t.Fatalf("无 fallback 时不应报: %+v", got)
	}
}

// TestWelcomeManagerSnapshot 快照：primary provider 三项 + `ui.welcomed`；密钥永不入快照；
// 读失败/无 provider = 零值（不阻断开窗）。
func TestWelcomeManagerSnapshot(t *testing.T) {
	cfgPath, cfg := writeProvidersConfig(t)
	m := &welcomeManager{cfgPath: cfgPath, secrets: testSecrets(t), hotkey: "Ctrl+B"}
	snap := m.Snapshot()
	if snap.Provider != "a" || snap.BaseURL != "http://a/v1" || snap.Models != "m1" {
		t.Fatalf("快照 = %+v", snap)
	}
	if snap.Welcomed || snap.Hotkey != "Ctrl+B" {
		t.Fatalf("标记/快捷键 = %+v", snap)
	}
	if strings.Contains(fmt.Sprintf("%+v", snap), "secret:") || strings.Contains(fmt.Sprintf("%+v", snap), "KA") {
		t.Fatalf("密钥引用不得进快照（D35）: %+v", snap)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatal(err)
	}
	_ = cfg
	if got := (&welcomeManager{cfgPath: "missing.json", secrets: testSecrets(t)}).Snapshot(); got.Provider != "" {
		t.Fatalf("缺失文件快照应为零值: %+v", got)
	}
}

// TestWelcomeManagerApply 写回（D141）：覆盖 `providers[0]` 三项 + primary + `ui.welcomed`；
// 无关键（tokenizer / 自定义键）按原条目保留；`model.name` 不在新 models 内 ⇒ 改写为
// models[0]（否则旧模型名发给新端点）。密钥口径见 D161：填的明文入库、config 只留引用。
func TestWelcomeManagerApply(t *testing.T) {
	cfgPath, _ := writeProvidersConfig(t)
	ch, bin := testChain(t, nil)
	stores := 0
	m := &welcomeManager{cfgPath: cfgPath, secrets: ch, bin: bin, stderr: io.Discard, onStore: func() { stores++ }}
	err := m.Apply(uigui.WelcomePatch{
		Provider: "myapi", BaseURL: "http://my/v1", APIKey: "sk-plain", Models: "mm1, mm2",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if stores != 1 {
		t.Fatalf("onStore 调用 = %d, want 1（填入明文落库；S9-6b/D163）", stores)
	}
	generic := readCfgMap(t, cfgPath)
	model := generic["model"].(map[string]any)
	providers := model["providers"].([]any)
	if len(providers) != 2 {
		t.Fatalf("providers 数 = %d, want 2（只覆盖首条，第二条原样保留）", len(providers))
	}
	first := providers[0].(map[string]any)
	if first["name"] != "myapi" || first["base_url"] != "http://my/v1" {
		t.Fatalf("providers[0] = %+v", first)
	}
	// D161：明文已被改写成 `secret:<名>`（沿用原引用名，引用不 churn），值落在库内；
	// config 里搜不到明文。
	if got := firstKeyRef(first); got != "secret:KA" {
		t.Fatalf("api_key 应改写为引用: %+v", first)
	}
	if got, _ := bin.Get(context.Background(), "KA"); got != "sk-plain" {
		t.Fatalf("明文应已入库原引用名槽位: %q", got)
	}
	if raw, _ := os.ReadFile(cfgPath); strings.Contains(string(raw), "sk-plain") {
		t.Fatalf("config 里不该再出现明文密钥")
	}
	if fmt.Sprint(modelNames(first)) != "[mm1 mm2]" {
		t.Fatalf("models = %v", first["models"])
	}
	if first["tokenizer"] != "" || first["custom_hint"] != "keep-me" {
		t.Fatalf("无关键应保留: %+v", first)
	}
	if model["primary"] != "myapi" {
		t.Fatalf("primary = %v", model["primary"])
	}
	if model["name"] != "mm1" {
		t.Fatalf("model.name 应改写为新 models[0]: %v", model["name"])
	}
	if ui := generic["ui"].(map[string]any); ui["welcomed"] != true {
		t.Fatalf("ui.welcomed 应置位: %v", ui)
	}
	if second := providers[1].(map[string]any); second["name"] != "b" || firstKeyRef(second) != "secret:KB" {
		t.Fatalf("第二条应原样保留: %+v", second)
	}
}

// firstKeyRef / modelNames 读泛键写回后的 provider 条目（D173 形状：keys[] / models[]）。
func firstKeyRef(p map[string]any) string {
	keys, _ := p["keys"].([]any)
	if len(keys) == 0 {
		return ""
	}
	km, _ := keys[0].(map[string]any)
	s, _ := km["api_key"].(string)
	return s
}

func modelNames(p map[string]any) []string {
	list, _ := p["models"].([]any)
	out := make([]string, 0, len(list))
	for _, mv := range list {
		if m, ok := mv.(map[string]any); ok {
			if n, _ := m["name"].(string); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// TestWelcomeManagerApplyKeepsKeyOnEmpty D141：api_key 留空 = 保持原值（只写不回显）——
// 首条原引用不被清掉。
func TestWelcomeManagerApplyKeepsKeyOnEmpty(t *testing.T) {
	cfgPath, _ := writeProvidersConfig(t)
	m := &welcomeManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: io.Discard}
	if err := m.Apply(uigui.WelcomePatch{Provider: "a", BaseURL: "http://a/v2", Models: "m1"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	first := readCfgMap(t, cfgPath)["model"].(map[string]any)["providers"].([]any)[0].(map[string]any)
	if got := firstKeyRef(first); got != "secret:KA" {
		t.Fatalf("留空应保持原引用: %+v", first)
	}
	if first["base_url"] != "http://a/v2" {
		t.Fatalf("base_url 应更新: %+v", first)
	}
}

// TestWelcomeManagerApplyDropsDanglingFallback D141：providers[0] 改名后，旧名留在
// fallback 内即成悬挂引用（启动 normalize 会 fail-fast）——写回时清理。
func TestWelcomeManagerApplyDropsDanglingFallback(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{"model":{"providers":[
	  {"name":"a","base_url":"http://a/v1","keys":[{"api_key":"secret:KA"}],"models":[{"name":"m1"}]},
	  {"name":"b","base_url":"http://b/v1","keys":[{"api_key":"secret:KB"}],"models":[{"name":"mb"}]}],
	  "primary":"b","fallback":[{"provider":"a"}],"name":"mb"},"ui":{"kind":"repl"}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &welcomeManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: io.Discard}
	if err := m.Apply(uigui.WelcomePatch{Provider: "a2", BaseURL: "http://a2/v1", Models: "m1"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	generic := readCfgMap(t, cfgPath)
	model := generic["model"].(map[string]any)
	if model["primary"] != "a2" {
		t.Fatalf("primary = %v", model["primary"])
	}
	if fb, _ := model["fallback"].([]any); len(fb) != 0 {
		t.Fatalf("改名的悬挂 fallback 应清理: %v", fb)
	}
	// 结果必须能通过启动校验（normalize 是 fail-fast 的守门人）。
	var typed fileConfig
	data, _ := os.ReadFile(cfgPath)
	if err := json.Unmarshal(data, &typed); err != nil {
		t.Fatal(err)
	}
	if err := typed.Model.normalize(); err != nil {
		t.Fatalf("写回结果应能通过 normalize: %v", err)
	}
}

// TestWelcomeManagerApplyValidation 校验矩阵：三类必填项缺失即报因、不落盘。
func TestWelcomeManagerApplyValidation(t *testing.T) {
	cfgPath, cfg := writeProvidersConfig(t)
	m := &welcomeManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: io.Discard}
	cases := []struct {
		name string
		p    uigui.WelcomePatch
		want string
	}{
		{"名字空", uigui.WelcomePatch{BaseURL: "http://x/v1", Models: "m"}, "名字"},
		{"base_url 空", uigui.WelcomePatch{Provider: "x", Models: "m"}, "base_url"},
		{"models 空", uigui.WelcomePatch{Provider: "x", BaseURL: "http://x/v1"}, "模型名"},
	}
	for _, c := range cases {
		if err := m.Apply(c.p); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want 含 %q", c.name, err, c.want)
		}
	}
	if got, _ := os.ReadFile(cfgPath); string(got) != cfg {
		t.Fatal("校验失败不得写盘")
	}
}

// TestWelcomeManagerMarkWelcomed 仅置标记：其余 ui 键（kind 等）保留。
func TestWelcomeManagerMarkWelcomed(t *testing.T) {
	cfgPath, _ := writeProvidersConfig(t)
	m := &welcomeManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: io.Discard}
	if err := m.MarkWelcomed(); err != nil {
		t.Fatalf("MarkWelcomed: %v", err)
	}
	ui := readCfgMap(t, cfgPath)["ui"].(map[string]any)
	if ui["welcomed"] != true || ui["kind"] != "repl" {
		t.Fatalf("ui 段 = %v", ui)
	}
}

// TestWelcomeManagerTest 连通性测试：patch 自建一次性 client 拉 /models（假 server）；
// 密钥留空 = 取 config 内 providers[0] 原值；失败路径报因。
func TestWelcomeManagerTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	t.Cleanup(srv.Close)

	cfgPath, _ := writeProvidersConfig(t)
	t.Setenv("KA", "k")
	m := &welcomeManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: io.Discard}
	if err := m.Test(uigui.WelcomePatch{Provider: "a", BaseURL: srv.URL, Models: "m1"}); err != nil {
		t.Fatalf("Test: %v", err)
	}
	t.Setenv("NOPE", "")
	if err := m.Test(uigui.WelcomePatch{Provider: "x", BaseURL: srv.URL, APIKey: "secret:NOPE", Models: "m"}); err == nil {
		t.Fatal("密钥环境变量缺失应报因")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(bad.Close)
	if err := m.Test(uigui.WelcomePatch{Provider: "x", BaseURL: bad.URL, APIKey: "sk-x", Models: "m"}); err == nil {
		t.Fatal("401 应报错")
	}
}

// TestSetupWizardContinuesAfterDone D141 启动流②（真首跑路径）：profile 配置**不存在**
// ⇒ 写模板（ui.kind=gui）→ 模型密钥引用取不到 ⇒ 开向导窗（此处换假宿主）→ 用户填好保存
// ⇒ **重新读配置、同进程继续启动**。判据 = 内核真的走起来了（repl 启动提示出现）且
// 「已生成配置」只报一次（重跑 bootstrap 不得重复报，firstRun 每次重算）。
// 假宿主写回 repl 形态，是为了在门禁里不真开 GUI 窗（真实向导改的是 gui profile 的 provider 段）。
func TestSetupWizardContinuesAfterDone(t *testing.T) {
	srv, _ := scriptServer(t, []string{"好的"})
	dir := t.TempDir() // 空目录 = 真首跑
	t.Setenv("AQUARIUS_OPENAI_KEY", "")
	t.Setenv("AQ_KEY", "k")
	profCfg := filepath.Join(dir, "profiles", "default", "config.json")
	calls := 0
	defer swapSetupWindowRunner(func(req uigui.SetupRequest) (uigui.SetupOutcome, error) {
		calls++
		// 首跑路径的次序事实：模板已落盘（写模板先于开窗），问题清单指名默认环境变量。
		if got, err := os.ReadFile(profCfg); err != nil || !strings.Contains(string(got), `"kind": "gui"`) ||
			!strings.Contains(string(got), "secret:AQUARIUS_OPENAI_KEY") {
			t.Errorf("向导窗应在模板落盘之后打开: err=%v cfg=%s", err, got)
		}
		if req.Theme != "system" {
			t.Errorf("向导窗应带 ui.theme 档（模板 system）: %q", req.Theme)
		}
		if joined := strings.Join(req.Issues, "；"); !strings.Contains(joined, "AQUARIUS_OPENAI_KEY") {
			t.Errorf("问题清单应指名缺失的环境变量: %+v", req.Issues)
		}
		cfg := fmt.Sprintf(`{"model":{"providers":[{"name":"openai","base_url":%q,"keys":[{"api_key":"secret:AQ_KEY"}],"models":[{"name":"m1"}]}],"name":"m1"},"ui":{"kind":"repl","welcomed":true}}`, srv.URL)
		if err := os.WriteFile(profCfg, []byte(cfg), 0o644); err != nil {
			t.Errorf("假宿主写回失败: %v", err)
		}
		return uigui.SetupDone, nil
	})()
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	if calls != 1 {
		t.Fatalf("向导窗调用 %d 次, want 1", calls)
	}
	if !strings.Contains(out.String(), "Aquarius — 输入 /help 查看命令") {
		t.Fatalf("保存后应继续启动（内核起来）: stdout = %q, stderr = %q", out.String(), errBuf.String())
	}
	if n := strings.Count(out.String(), "已生成配置"); n != 1 {
		t.Fatalf("「已生成配置」应只报一次（重跑 bootstrap 的 firstRun 需重算）: %d 次，stdout = %q", n, out.String())
	}
}

// TestSetupWizardRunsOnFirstRunEvenWithKey D210（用户实测故障的回归）：**首跑**即使
// 密钥已能从环境变量取到，也必须开装配前向导。
//
// 复现过的观感：模板里的引用是 `secret:AQUARIUS_OPENAI_KEY`，用户恰好设了这个环境变量，
// 于是 `missingKeys` 为空、向导被跳过；可模板里 `ui.welcomed = false`，主窗起来后又自动
// 弹了应用内欢迎窗——**欢迎窗与主窗同屏**，用户不知道该跟哪个走，填进去的 key 也落在了
// 「应用内引导」这条较晚的路径上。
//
// 判据：① 密钥可用（不报缺）也照样调一次向导；② 取消后 `welcomed` 仍为 false（下次仍会引导，
// 不静默吞掉首跑）；③ 保存路径（假宿主写回）之后主窗不再二次开窗——由 `welcomed` 已置位保证。
func TestSetupWizardRunsOnFirstRunEvenWithKey(t *testing.T) {
	dir := t.TempDir()                             // 空目录 = 真首跑（bootstrap 会写模板）
	t.Setenv("AQUARIUS_OPENAI_KEY", "sk-from-env") // 模板引用恰好解析得出来
	profCfg := filepath.Join(dir, "profiles", "default", "config.json")
	var reqs []uigui.SetupRequest
	defer swapSetupWindowRunner(func(req uigui.SetupRequest) (uigui.SetupOutcome, error) {
		reqs = append(reqs, req)
		return uigui.SetupCancelled, nil
	})()
	w := &wiring{stdout: io.Discard, stderr: io.Discard}
	if err := w.bootstrap(dir, "", "", ""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !w.firstRun {
		t.Fatal("空目录 bootstrap 应标记首跑")
	}
	code, done := w.setupWizard(context.Background(), dir, "", "", "")
	if !done || code != 0 {
		t.Fatalf("setupWizard = (%d, %v), want (0, true)", code, done)
	}
	if len(reqs) != 1 {
		t.Fatalf("首跑应开一次向导窗（密钥可用也要确认），实际 %d 次", len(reqs))
	}
	if len(reqs[0].Issues) != 0 {
		t.Fatalf("密钥可用时问题清单应为空，实际 %+v", reqs[0].Issues)
	}
	cfg := readCfgMap(t, profCfg)
	if ui, _ := cfg["ui"].(map[string]any); ui["welcomed"] != false {
		t.Fatalf("取消不得写 welcomed: %+v", ui)
	}
}

// TestSetupWizardSkippedWhenWelcomedAndKeyOK D210 的另一半：**不是首跑**、密钥也可用时
// 不再打扰——向导只拦「缺密钥」与「首跑」两种情形，日常启动不该每次都弹窗。
//
// 直接调 setupWizard（不跑 run）：gui 形态的全量 run 会进真实事件循环，门禁里不能走。
func TestSetupWizardSkippedWhenWelcomedAndKeyOK(t *testing.T) {
	dir := t.TempDir()
	writeProfileLayout(t, dir, `{"model":{"providers":[{"name":"openai","base_url":"http://127.0.0.1:1/v1","keys":[{"api_key":"secret:AQ_KEY"}],"models":[{"name":"m1"}]}],"name":"m1"},"ui":{"kind":"gui","welcomed":true}}`)
	t.Setenv("AQ_KEY", "k")
	calls := 0
	defer swapSetupWindowRunner(func(uigui.SetupRequest) (uigui.SetupOutcome, error) {
		calls++
		return uigui.SetupCancelled, nil
	})()
	w := &wiring{stdout: io.Discard, stderr: io.Discard}
	if err := w.bootstrap(dir, "", "", ""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if w.firstRun {
		t.Fatal("该 profile 不是首跑（配置已存在）")
	}
	code, done := w.setupWizard(context.Background(), dir, "", "", "")
	if done || code != 0 {
		t.Fatalf("setupWizard = (%d, %v), want (0, false)", code, done)
	}
	if calls != 0 {
		t.Fatalf("已引导过且密钥可用时不应开向导窗，实际 %d 次", calls)
	}
}

// 取不到的引用）⇒ 只尝试一次、报因退出 1——不循环弹窗。
func TestSetupWizardStillIncompleteFatal(t *testing.T) {
	dir := t.TempDir()
	writeProfileLayout(t, dir, `{"model":{"providers":[{"name":"openai","base_url":"http://127.0.0.1:1/v1","keys":[{"api_key":"secret:AQ_MISSING"}],"models":[{"name":"m1"}]}],"name":"m1"},"ui":{"kind":"gui"}}`)
	t.Setenv("AQ_MISSING", "")
	calls := 0
	defer swapSetupWindowRunner(func(uigui.SetupRequest) (uigui.SetupOutcome, error) {
		calls++ // 不写盘：模拟用户填了同样取不到的引用
		return uigui.SetupDone, nil
	})()
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1（stderr=%s）", code, errBuf.String())
	}
	if calls != 1 {
		t.Fatalf("向导窗调用 %d 次, want 1（只尝试一次，不循环）", calls)
	}
	if !strings.Contains(errBuf.String(), "配置仍不完整") {
		t.Fatalf("stderr = %q", errBuf.String())
	}
}

// TestSetupWizardCancelledExitsZero D141 启动流①：已存在的配置缺密钥 + 用户取消 ⇒ 退出 0
// 并指出缺哪个 provider 的哪个环境变量（不再是 fatal 英文错误框，Q4）。
func TestSetupWizardCancelledExitsZero(t *testing.T) {
	dir := t.TempDir()
	writeProfileLayout(t, dir, `{"model":{"providers":[{"name":"openai","base_url":"http://127.0.0.1:1/v1","keys":[{"api_key":"secret:AQ_MISSING"}],"models":[{"name":"m1"}]}],"name":"m1"},"ui":{"kind":"gui"}}`)
	t.Setenv("AQ_MISSING", "")
	defer swapSetupWindowRunner(func(uigui.SetupRequest) (uigui.SetupOutcome, error) {
		return uigui.SetupCancelled, nil
	})()
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, want 0（stderr=%s）", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "AQ_MISSING") {
		t.Fatalf("应指名缺失的环境变量: %q", errBuf.String())
	}
}

// TestSetupWizardSkippedForREPL D141：tui/repl 不走向导窗——密钥缺失仍走内核装配期的
// fatal 报因（旧口径）。
func TestSetupWizardSkippedForREPL(t *testing.T) {
	dir := t.TempDir()
	writeProfileLayout(t, dir, `{"model":{"providers":[{"name":"openai","base_url":"http://127.0.0.1:1/v1","keys":[{"api_key":"secret:AQ_MISSING"}],"models":[{"name":"m1"}]}],"name":"m1"},"ui":{"kind":"repl"}}`)
	t.Setenv("AQ_MISSING", "")
	calls := 0
	defer swapSetupWindowRunner(func(uigui.SetupRequest) (uigui.SetupOutcome, error) {
		calls++
		return uigui.SetupCancelled, nil
	})()
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if calls != 0 {
		t.Fatalf("repl 形态不应开向导窗（调用 %d 次）", calls)
	}
	if !strings.Contains(errBuf.String(), "AQ_MISSING") {
		t.Fatalf("stderr = %q", errBuf.String())
	}
}

// TestSetupWizardPlaintextNotBlocked D141：明文密钥不阻断启动、不开向导窗；明文警告只在
// 真实构造路径（resolveAPIKey）发**一次**（探测不打警告——否则同一 provider 警告两遍）。
func TestSetupWizardPlaintextNotBlocked(t *testing.T) {
	srv, _ := scriptServer(t, []string{"好的"})
	dir := t.TempDir()
	writeProfileLayout(t, dir, fmt.Sprintf(`{"model":{"providers":[{"name":"p","base_url":%q,"keys":[{"api_key":"sk-plain"}],"models":[{"name":"m1"}]}],"name":"m1"},"ui":{"kind":"repl","welcomed":true}}`, srv.URL))
	calls := 0
	defer swapSetupWindowRunner(func(uigui.SetupRequest) (uigui.SetupOutcome, error) {
		calls++
		return uigui.SetupCancelled, nil
	})()
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	if calls != 0 {
		t.Fatalf("明文密钥不应触发向导窗（调用 %d 次）", calls)
	}
	if n := strings.Count(errBuf.String(), "明文"); n != 1 {
		t.Fatalf("明文警告应恰好一次（探测不得重复打）: %d 次，stderr = %q", n, errBuf.String())
	}
}

// TestWelcomeTutorialCommandsExist D141 对照测试：教程页展示的命令必须真实存在于内核
// 命令清单（单一事实源 = internal/app 的 commandCatalog）——命令改了没同步教程即失败。
func TestWelcomeTutorialCommandsExist(t *testing.T) {
	known := map[string]bool{}
	for _, ci := range app.CommandCatalog() {
		for _, n := range ci.Names {
			known["/"+n] = true
		}
	}
	if len(known) == 0 {
		t.Fatal("命令清单为空，对照无意义")
	}
	for _, c := range uigui.TutorialCommands() {
		name := strings.Fields(c[0])[0]
		if !known[name] {
			t.Errorf("教程页命令 %q 不在内核命令清单内（清单改了没同步教程）", name)
		}
	}
}
