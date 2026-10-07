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
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// providerManager uigui.ProviderManager 的装配根实现（D110②/③）。
type providerManager struct {
	cfgPath string
	stderr  io.Writer // D35 明文警告出口（os.Stderr；测试可注入）
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
		for _, p := range patches {
			entry := map[string]any{}
			if base := byOrig(p.Orig); base != nil {
				for k, v := range base {
					entry[k] = v // 无关键保留（Q10 口径：未知键不丢）
				}
			}
			entry["name"] = strings.TrimSpace(p.Name)
			entry["base_url"] = strings.TrimSpace(p.BaseURL)
			if p.APIKey != "" { // 空 = 保持原值（D35 只写不回显）
				entry["api_key"] = strings.TrimSpace(p.APIKey)
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

// Test 连通性测试（D110②：真实最小请求）：以 patch 构造一次性 client（8s 超时）
// 拉 /models。APIKey 空 = 取 config 内原条目值（按 Orig 名），再走 D35 解析。
func (m *providerManager) Test(p uigui.ProviderPatch) error {
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
	key, err := resolveAPIKey(context.Background(), p.Name, ref, m.stderr)
	if err != nil {
		return err
	}
	client, err := llm.New(llm.Config{
		BaseURL:    strings.TrimSpace(p.BaseURL),
		APIKey:     key,
		HTTPClient: &http.Client{Timeout: 8 * time.Second},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := client.Models(ctx); err != nil {
		return err
	}
	return nil
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
