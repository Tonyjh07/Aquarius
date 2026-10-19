package main

// provider 列表管理面（D110②/③）：设置窗 provider 编辑区的装配根实现。
// 快照 = config providers（容错：旧格式/解析失败返回空，不阻断开窗——Q10 口径）；
// Apply = 全量替换 model.providers（泛键写回：按 Orig 名保留原条目的 api_key 与
// 无关键、清理 fallback 悬挂引用）；Test = 真实最小请求（/models，短超时）。
// provider 变更一律重启生效（port.LLM 构造期固化，D110②）。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/llm"
	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// providerManager uigui.ProviderManager 的装配根实现（D110②/③）。
type providerManager struct {
	cfgPath string
	secrets port.Secrets     // D151/D161：读链（库 → 同名环境变量）
	bin     *secretbin.Store // D161：界面明文密钥的写入口（nil = 未接线）
	stderr  io.Writer        // D35 明文警告出口（os.Stderr；测试可注入）
	// onStore 明文真的写进密钥库后的回调（S9-6b/D163）：递增密钥库快照 Revision，
	// 让同一次设置窗会话里刚改过 provider 密钥后切到「密钥库」页也能看见新条目。
	onStore func()
}

// Snapshot provider 编辑区数据面（开窗现取；读失败 = 空，不阻断开窗）。
func (m *providerManager) Snapshot() []uigui.ProviderSnapshot {
	cfg, err := loadConfig(m.cfgPath)
	if err != nil {
		return nil
	}
	out := make([]uigui.ProviderSnapshot, 0, len(cfg.Model.Providers))
	for i, p := range cfg.Model.Providers {
		out = append(out, uigui.ProviderSnapshot{
			Name: p.Name, BaseURL: p.BaseURL,
			Models:      joinCSV(p.Models),
			Tokenizer:   p.Tokenizer,
			Unsupported: joinCSV(p.UnsupportedParams),
			Primary:     i == 0 && cfg.Model.Primary == "" || p.Name == cfg.Model.Primary,
		})
	}
	return out
}

// Apply 全量替换 providers（D110②）：校验（名字唯一非空、base_url/models 非空、
// primary 命中）→ 泛键写回。原条目无关键（含 api_key）按 Orig 名保留——改名不丢密钥，
// 新条目从空起步。fallback 悬挂引用（删除/改名产生的）一并清理；model.name 不动
// （启动 normalize 兜底缺省）。
//
// 密钥入口径（D151/D161，取代原先「填什么写什么」）：编辑区填的**明文**写进本机密钥库，
// config 里只落 `secret:<名>` 引用（名沿用原引用名，否则按 provider 名生成）；填的本来就
// 是引用则照写。**写库失败即报错、config 不动**——绝不静默退回明文。
func (m *providerManager) Apply(primary string, patches []uigui.ProviderPatch) error {
	if len(patches) == 0 {
		return fmt.Errorf("至少保留一个 provider")
	}
	names := map[string]bool{}
	for _, p := range patches {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("provider 名字不可为空")
		}
		if names[p.Name] {
			return fmt.Errorf("provider 名 %q 重复", p.Name)
		}
		names[p.Name] = true
		if strings.TrimSpace(p.BaseURL) == "" {
			return fmt.Errorf("provider %q 的 base_url 不可为空", p.Name)
		}
		if len(splitCSV(p.Models)) == 0 {
			return fmt.Errorf("provider %q 的 models 不可为空（首个 = 缺省请求模型）", p.Name)
		}
	}
	if primary == "" {
		primary = patches[0].Name
	}
	if !names[primary] {
		return fmt.Errorf("primary %q 不在 provider 列表内", primary)
	}
	// 密钥先落库（config 写回之前）：任何一条写库失败就整体放弃，不留「config 已是引用、
	// 库里却没有值」的半个状态。
	origRefs := m.origRefsByName()
	keyRefs := make([]string, len(patches))
	for i, p := range patches {
		if strings.TrimSpace(p.APIKey) == "" {
			continue // 空 = 保持原值（D35 只写不回显）
		}
		typed := strings.TrimSpace(p.APIKey)
		ref, err := storePlaintextKey(context.Background(), m.bin, strings.TrimSpace(p.Name), origRefs[p.Orig], typed)
		if err != nil {
			return err
		}
		keyRefs[i] = ref
		// 明文真的落库了（非空且不是引用）：递增密钥库快照 Revision，让同一次设置窗
		// 会话里切到「密钥库」页也能看见新条目（S9-6b/D163）。
		if m.onStore != nil && typed != "" && !strings.HasPrefix(typed, "secret:") {
			m.onStore()
		}
	}
	return persistConfig(m.cfgPath, func(generic map[string]any) {
		model := modelSection(generic)
		old, _ := model["providers"].([]any)
		byOrig := func(orig string) map[string]any {
			if orig == "" {
				return nil
			}
			for _, pv := range old {
				if pm, ok := pv.(map[string]any); ok && pm["name"] == orig {
					return pm
				}
			}
			return nil
		}
		list := make([]any, 0, len(patches))
		for i, p := range patches {
			entry := map[string]any{}
			if base := byOrig(p.Orig); base != nil {
				for k, v := range base {
					entry[k] = v // 无关键保留（Q10 口径：未知键不丢）
				}
			}
			entry["name"] = strings.TrimSpace(p.Name)
			entry["base_url"] = strings.TrimSpace(p.BaseURL)
			if keyRefs[i] != "" { // 空 = 保持原值（D35 只写不回显）
				entry["api_key"] = keyRefs[i]
			}
			entry["models"] = anyList(splitCSV(p.Models))
			entry["tokenizer"] = strings.TrimSpace(p.Tokenizer)
			entry["unsupported_params"] = anyList(splitCSV(p.Unsupported))
			list = append(list, entry)
		}
		model["providers"] = list
		model["primary"] = primary
		if fbs, ok := model["fallback"].([]any); ok {
			kept := make([]any, 0, len(fbs))
			for _, fb := range fbs {
				if s, ok := fb.(string); ok && names[s] {
					kept = append(kept, s) // 悬挂引用（删除/改名）清理
				}
			}
			model["fallback"] = kept
		}
	})
}

// origRefsByName 读 config 里各 provider 现有 api_key 引用（按名索引；读失败返回空表）。
// 用途：界面改填明文时，**原引用名**决定库里写哪个槽（改名不 churn 引用，D161 落点③）。
func (m *providerManager) origRefsByName() map[string]string {
	out := map[string]string{}
	cfg, err := loadConfig(m.cfgPath)
	if err != nil {
		return out
	}
	for _, p := range cfg.Model.Providers {
		out[p.Name] = p.APIKey
	}
	return out
}

// Test 连通性测试（D110②：真实最小请求）：以 patch 构造一次性 client（8s 超时）
// 拉 /models。APIKey 空 = 取 config 内原条目值（按 Orig 名），再走 D35 解析。
func (m *providerManager) Test(p uigui.ProviderPatch) error {
	_, err := m.Models(p)
	return err
}

// Models 拉取模型列表（「获取模型」按钮的数据面，D153）：与 Test 同一条最小请求，
// 只是把结果带回。去重保序（服务端有时重复列出同一 id），空列表如实返回空。
func (m *providerManager) Models(p uigui.ProviderPatch) ([]string, error) {
	client, err := m.client(p)
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

// client 以 patch 构造一次性 llm client（凭据解析口径 = Test/Models 共用）：
// APIKey 空 = 取 config 内原条目值（按 Orig 名），再走 D35 解析。
func (m *providerManager) client(p uigui.ProviderPatch) (*llm.Client, error) {
	ref := p.APIKey
	if ref == "" && p.Orig != "" {
		if cfg, err := loadConfig(m.cfgPath); err == nil {
			for _, pr := range cfg.Model.Providers {
				if pr.Name == p.Orig {
					ref = pr.APIKey
					break
				}
			}
		}
	}
	key, err := resolveAPIKey(context.Background(), p.Name, ref, m.secrets, m.stderr)
	if err != nil {
		return nil, err
	}
	return llm.New(llm.Config{
		BaseURL:    strings.TrimSpace(p.BaseURL),
		APIKey:     key,
		HTTPClient: &http.Client{Timeout: 8 * time.Second},
	})
}

// splitCSV 逗号分隔清单解析（表单单行口径）：中英文逗号皆可、去空白、丢空项。
func splitCSV(s string) []string {
	s = strings.ReplaceAll(s, "，", ",")
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// joinCSV 清单 → 逗号分隔展示串。
func joinCSV(list []string) string { return strings.Join(list, ", ") }

// anyList []string → []any（泛键写回口径）。
func anyList(list []string) []any {
	out := make([]any, 0, len(list))
	for _, v := range list {
		out = append(out, v)
	}
	return out
}

// 编译期接口核对。
var (
	_ uigui.ProviderManager = (*providerManager)(nil)
	_ uigui.ProfilesManager = (*profilesManager)(nil)
)
