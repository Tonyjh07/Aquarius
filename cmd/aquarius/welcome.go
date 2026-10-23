package main

// 欢迎 / 首次运行引导的装配根侧（D141/§15.7）：三步向导的数据面
// （uigui.WelcomeManager 实现）+ 「配置不完整」的装配前判定 + 向导窗宿主（可注入，
// 测试不真开窗）。
//
// 判定口径（D141）：**阻断启动** = primary 或任一 fallback 的 api_key 经
// port.Secrets 取不到；**不阻断** = 密钥可用、只是 `ui.welcomed` 缺失（正常启动、
// 启动后自动开一次欢迎窗，见 assemble_ui.go）。探测走端口、不直读 os.Getenv，且
// **不打印明文警告**——警告只在 resolveAPIKey 的真实构造路径发一次（D35）。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/llm"
	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// setupWindowRunner 装配前向导窗宿主：默认 = uigui.RunSetupWindow（真窗）；测试换假实现
// ——首跑/缺密钥路径的测试不得真开窗（仓库既有先例：messageBox / notifySenderImpl）。
var setupWindowRunner = uigui.RunSetupWindow

// welcomeManager uigui.WelcomeManager 的装配根实现（D141）。
type welcomeManager struct {
	cfgPath string
	secrets port.Secrets     // D151/D161：读链（库 → 同名环境变量）
	bin     *secretbin.Store // D161：界面明文密钥的写入口（nil = 未接线）
	stderr  io.Writer        // D35 明文警告出口（os.Stderr；测试可注入）
	hotkey  string           // ui.hotkey（教程页展示用；空 = Alt+A）
	// onStore 明文真的写进密钥库后的回调（S9-6b/D163）：递增密钥库快照 Revision，
	// 让别处（设置窗「密钥库」页）也能看见新条目。
	onStore func()
}

// Snapshot 欢迎窗快照（开窗现取；**密钥不回显**）：primary provider 三项 + 标记。
// 读失败/无 provider 返回零值（不阻断开窗，Q10 同口径）。
func (m *welcomeManager) Snapshot() uigui.WelcomeSnapshot {
	snap := uigui.WelcomeSnapshot{Hotkey: m.hotkey}
	cfg, err := loadConfig(m.cfgPath)
	if err != nil || len(cfg.Model.Providers) == 0 {
		return snap
	}
	p := cfg.Model.primaryProvider()
	snap.Provider = p.Name
	snap.BaseURL = p.BaseURL
	names := make([]string, 0, len(p.Models))
	for _, e := range p.Models {
		names = append(names, e.Name)
	}
	snap.Models = joinCSV(names)
	snap.Welcomed = cfg.UI.Welcomed
	return snap
}

// Apply 写回（D141）：覆盖 `model.providers[0]`（无关键保留，口径同
// providerManager.Apply）+ primary + `ui.welcomed`。两处一致性补丁：
//   - primary 改名 → 清理该名在 fallback 内的悬挂引用（否则下次启动 normalize fail-fast）；
//   - `model.name` 不在新 models 内（含空）→ 改写为 models[0]（否则旧模型名会被发给新
//     端点，向导「首个 = 缺省请求模型」的名分失约）。
func (m *welcomeManager) Apply(p uigui.WelcomePatch) error {
	name := strings.TrimSpace(p.Provider)
	baseURL := strings.TrimSpace(p.BaseURL)
	models := splitCSV(p.Models)
	switch {
	case name == "":
		return fmt.Errorf("provider 名字不可为空")
	case baseURL == "":
		return fmt.Errorf("接口地址（base_url）不可为空")
	case len(models) == 0:
		return fmt.Errorf("模型名不可为空（首个 = 缺省请求模型）")
	}
	// 密钥先落库（config 写回之前，D151/D161）：填的是明文写进本机库、config 只留引用；
	// 填的是引用则照写。写库失败整体放弃，不留「config 已是引用、库里没有值」的半个状态。
	keyRef, err := welcomeKeyRef(m, name, p.APIKey)
	if err != nil {
		return err
	}
	return persistConfig(m.cfgPath, func(generic map[string]any) {
		model := modelSection(generic)
		old, _ := model["providers"].([]any)
		entry := map[string]any{}
		if len(old) > 0 {
			if first, ok := old[0].(map[string]any); ok {
				for k, v := range first {
					entry[k] = v // 无关键保留（Q10 口径：tokenizer / unsupported_params / 未知键不丢）
				}
			}
		}
		entry["name"] = name
		entry["base_url"] = baseURL
		base, _ := old[0].(map[string]any)
		entry["keys"] = welcomeKeys(base, keyRef) // 空 = 保持原值（D35 只写不回显）
		entry["models"] = welcomeModels(base, models)
		// 首条 = 本次填写；其余条目原样保留（只覆盖 providers[0]）。
		names := map[string]bool{name: true}
		rest := make([]any, 0, len(old))
		for _, pv := range old[min(1, len(old)):] {
			rest = append(rest, pv)
			if pm, ok := pv.(map[string]any); ok {
				if n, _ := pm["name"].(string); n != "" {
					names[n] = true
				}
			}
		}
		model["providers"] = append([]any{entry}, rest...)
		model["primary"] = name
		if fbs, ok := model["fallback"].([]any); ok {
			kept := make([]any, 0, len(fbs))
			for _, fb := range fbs {
				pm, ok := fb.(map[string]any)
				if !ok {
					continue
				}
				prov, _ := pm["provider"].(string)
				if names[prov] && !(prov == name && pm["model"] == model["name"]) {
					kept = append(kept, fb) // 悬挂引用（改名/删除）清理
				}
			}
			model["fallback"] = kept
		}
		if cur, _ := model["name"].(string); cur == "" || !slices.Contains(models, cur) {
			model["name"] = models[0]
		}
		uiSec, _ := generic["ui"].(map[string]any)
		if uiSec == nil {
			uiSec = map[string]any{}
			generic["ui"] = uiSec
		}
		uiSec["welcomed"] = true
	})
}

// welcomeKeyRef 欢迎窗密钥写入口径（D161 落点①）：明文 → 写进本机密钥库、返回
// `secret:<名>` 引用；引用 → 原样保留；空 → 返回 ""（保持原值）。
// 键名沿用 providers[0] 现有引用名（改名不 churn），否则按 provider 名生成。
// 明文真的落库时回调 onStore（S9-6b/D163：递增密钥库快照 Revision）。
func welcomeKeyRef(m *welcomeManager, name, typed string) (string, error) {
	if strings.TrimSpace(typed) == "" {
		return "", nil
	}
	origRef := ""
	if cfg, err := loadConfig(m.cfgPath); err == nil && len(cfg.Model.Providers) > 0 {
		origRef = cfg.Model.Providers[0].keyRefs()[0]
	}
	ref, err := storePlaintextKey(context.Background(), m.bin, name, origRef, typed)
	if err != nil {
		return "", err
	}
	if m.onStore != nil && !strings.HasPrefix(strings.TrimSpace(typed), "secret:") {
		m.onStore()
	}
	return ref, nil
}

// welcomeKeys 欢迎窗密钥写回（口径同 writeKeys，但欢迎窗只编辑第一把）：原条目的
// 密钥池原样保留（label / 后续槽位记忆不丢），keyRef 非空时覆盖**第一把**的 api_key。
// 空 = 保持原值（D35 只写不回显）。
func welcomeKeys(base map[string]any, keyRef string) []any {
	var keys []any
	if base != nil {
		if old, ok := base["keys"].([]any); ok {
			keys = append(keys, old...)
		}
	}
	if keyRef != "" {
		if len(keys) == 0 {
			keys = []any{map[string]any{"api_key": keyRef}}
		} else if pm, ok := keys[0].(map[string]any); ok {
			pm["api_key"] = keyRef
		}
	}
	if keys == nil {
		keys = []any{} // 空数组而非 null：键存在但为空 = 一条缺省引用（读侧同口径）
	}
	return keys
}

// welcomeModels 欢迎窗模型条目写回（口径同 writeModels，但欢迎窗只给名字表）：按名字
// 继承原条目的能力位与上下文窗口（欢迎窗不编辑这些）；新增的名字从空起步。顺序以本次
// 编辑为准。
func welcomeModels(base map[string]any, names []string) []any {
	byName := map[string]map[string]any{}
	if base != nil {
		if old, ok := base["models"].([]any); ok {
			for _, mv := range old {
				if pm, ok := mv.(map[string]any); ok {
					if n, _ := pm["name"].(string); n != "" {
						byName[n] = pm
					}
				}
			}
		}
	}
	out := make([]any, 0, len(names))
	for _, n := range names {
		if pm, ok := byName[n]; ok {
			out = append(out, pm)
			continue
		}
		out = append(out, map[string]any{"name": n})
	}
	return out
}

// Test 连通性测试（口径同 providerManager.Test）：以 patch 构造一次性 client（8s 超时）
// 拉 /models。APIKey 空 = 取 config 内 providers[0] 原值，再走 D35 解析。
func (m *welcomeManager) Test(p uigui.WelcomePatch) error {
	_, err := m.Models(p)
	return err
}

// Models 拉取模型列表（欢迎窗「获取模型」按钮，D153）：去重保序，只读。
func (m *welcomeManager) Models(p uigui.WelcomePatch) ([]string, error) {
	ref := p.APIKey
	if ref == "" {
		if cfg, err := loadConfig(m.cfgPath); err == nil && len(cfg.Model.Providers) > 0 {
			ref = cfg.Model.Providers[0].keyRefs()[0]
		}
	}
	key, err := resolvePatchKey(context.Background(), strings.TrimSpace(p.Provider), ref, m.secrets)
	if err != nil {
		return nil, err
	}
	client, err := llm.New(llm.Config{
		BaseURL:    strings.TrimSpace(p.BaseURL),
		APIKey:     key,
		HTTPClient: &http.Client{Timeout: 8 * time.Second},
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	infos, err := client.Models(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(infos))
	seen := make(map[string]bool, len(infos))
	for _, mi := range infos {
		name := strings.TrimSpace(mi.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// MarkWelcomed 仅置 `ui.welcomed`（用户显式关闭 = 已看，不再自动弹，D141）。
func (m *welcomeManager) MarkWelcomed() error {
	return persistConfig(m.cfgPath, func(generic map[string]any) {
		uiSec, _ := generic["ui"].(map[string]any)
		if uiSec == nil {
			uiSec = map[string]any{}
			generic["ui"] = uiSec
		}
		uiSec["welcomed"] = true
	})
}

// apiKeyGap 密钥可解析性探测（D141，装配前）：空值回落默认引用与 resolveAPIKey 同口径；
// 明文恒可解析（**不打警告**——警告只在真实构造路径发一次，D35）；引用型经 port.Secrets
// 按名取用（先本机密钥库、再同名环境变量；不直读 os.Getenv，端口契约优先，D151/D161）。
// D173 密钥池：**任一把**能取到就算可用（轮换本来就是为「某把用完」准备的），
// 全取不到才报因，报第一把的错。
// 返回 "" = 可用。
func apiKeyGap(ctx context.Context, p providerConfig, sec port.Secrets) string {
	var first string
	for _, ref := range p.keyRefs() {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			ref = "secret:AQUARIUS_OPENAI_KEY"
		}
		if !strings.HasPrefix(ref, "secret:") {
			return ""
		}
		name, err := secretName(ref)
		if err != nil {
			if first == "" {
				first = fmt.Sprintf("provider %q 的密钥引用非法：%v", p.Name, err)
			}
			continue
		}
		if _, err := sec.Get(ctx, name); err != nil {
			if first == "" {
				first = fmt.Sprintf("provider %q 取不到密钥：%v", p.Name, err)
			}
			continue
		}
		return ""
	}
	return first
}

// missingKeys 装配前「配置不完整」判定（D141）：primary 与 fallback[] 逐条探测密钥。
// 非空即阻断启动（gui 走向导窗，其余形态 fatal 报因）。
func missingKeys(ctx context.Context, m *modelConfig, sec port.Secrets) []string {
	var out []string
	seen := map[string]bool{}
	check := func(p *providerConfig) {
		if p == nil || seen[p.Name] {
			return
		}
		seen[p.Name] = true
		if gap := apiKeyGap(ctx, *p, sec); gap != "" {
			out = append(out, gap)
		}
	}
	if len(m.Providers) > 0 {
		check(m.primaryProvider())
	}
	for _, fb := range m.Fallback {
		check(m.byName(fb.Provider))
	}
	return out
}

// setupWizard 装配前向导窗（D141）：gui 且「需要引导」时，**先于内核装配**开独立向导窗
// ——保存 → 重新读配置（重跑 bootstrap 的读配置段）→ 同进程继续启动；取消 → 退出 0。
// 只尝试一次：向导后仍不完整即 fatal 报因，不循环弹窗。
// tui/repl 不走窗（首跑已在 bootstrap 打指引退出；密钥缺失仍走 buildLLMs 的 fatal 报因）。
//
// 「需要引导」= **缺密钥 或 本次是这个 profile 的首跑**（D210）。改前只看缺密钥：模板里的
// 引用常常正好由环境变量顶上（`secret:AQUARIUS_OPENAI_KEY` 一旦设了就别处也能取到），于是
// 「配置够用」被当成「用户已经确认过」，装配前向导被跳过；可模板里 `ui.welcomed` 是 false，
// 主窗起来后又自动弹了应用内欢迎窗——两个窗口同屏，用户不知道该跟哪个走。首跑一律先引导：
// 保存即置位 `welcomed`，主窗不再二次打扰，用户填的 key 也一定走在「入库 + 引用」这条路上。
//
// 返回 (退出码, 是否结束 run)。
func (w *wiring) setupWizard(ctx context.Context, dataDir, profileFlag, modelName, baseURL string) (int, bool) {
	if w.cfg.UI.Kind != "gui" || w.setupTried {
		return 0, false
	}
	issues := missingKeys(ctx, &w.cfg.Model, w.secrets)
	if len(issues) == 0 && !w.firstRun {
		return 0, false
	}
	w.setupTried = true
	outcome, err := setupWindowRunner(uigui.SetupRequest{
		Theme: w.cfg.UI.Theme,
		Mgr: &welcomeManager{
			cfgPath: w.cfgPath, secrets: w.secrets, bin: w.secretsBin,
			stderr: w.stderr, hotkey: w.cfg.UI.Hotkey,
			onStore: func() { w.secretsRev.Add(1) }, // S9-6b/D163：明文落库递增快照 Revision
		},
		Issues: issues,
	})
	if err != nil {
		return fatal(w.stderr, "欢迎向导窗: %v", err), true
	}
	if outcome != uigui.SetupDone {
		fmt.Fprintf(w.stderr, "已取消配置引导，配置仍不完整：%s\n", strings.Join(issues, "；"))
		return 0, true
	}
	if err := w.bootstrap(dataDir, profileFlag, modelName, baseURL); err != nil {
		if errors.Is(err, errFirstRun) {
			return 0, true
		}
		return fatal(w.stderr, "%v", err), true
	}
	if issues := missingKeys(ctx, &w.cfg.Model, w.secrets); len(issues) > 0 {
		return fatal(w.stderr, "配置仍不完整：%s", strings.Join(issues, "；")), true
	}
	return 0, false
}

// 编译期接口核对。
var _ uigui.WelcomeManager = (*welcomeManager)(nil)
