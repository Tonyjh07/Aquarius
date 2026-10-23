package main

// provider 列表管理面（D110②/③）：设置窗 provider 编辑区的装配根实现。
// 快照 = config providers（容错：旧格式/解析失败返回空，不阻断开窗——Q10 口径）；
// Apply = 全量替换 model.providers（泛键写回：按 Orig 名保留原条目的无关键与逐槽密钥、
// 清理 fallback 悬挂引用）；Test = 真实最小请求（/models，短超时）；Probe = 能力探测
// 写回（与 `/model probe` 同一条逻辑，D174）。
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
	// probe 能力探测数据面（S11-7③/D174；D181 起以编辑区 patch 为准）：由装配根接到
	// wiring.probeForSettings。nil = 未接线（探测按钮报因）。
	probe func(ctx context.Context, patch uigui.ProviderPatch, model string) (uigui.ProbeOutcome, error)
	// onStore 明文真的写进密钥库后的回调（S9-6b/D163）：递增密钥库快照 Revision，
	// 让同一次设置窗会话里刚改过 provider 密钥后切到「密钥库」页也能看见新条目。
	onStore func()
}

// keyEntry 磁盘上某 provider 现有密钥池的一槽（label + 引用原文）。
// 只在装配根内部流转，**不进快照**——引用原文可能含明文密钥（D35 只写不回显）。
type keyEntry struct {
	label string
	ref   string
}

// Snapshot provider 编辑区数据面（开窗现取；读失败 = 空，不阻断开窗）。
func (m *providerManager) Snapshot() []uigui.ProviderSnapshot {
	cfg, err := loadConfig(m.cfgPath)
	if err != nil {
		return nil
	}
	out := make([]uigui.ProviderSnapshot, 0, len(cfg.Model.Providers))
	for i := range cfg.Model.Providers {
		p := &cfg.Model.Providers[i]
		out = append(out, uigui.ProviderSnapshot{
			Name:        p.Name,
			Kind:        p.protocolKind(),
			BaseURL:     p.BaseURL,
			Tokenizer:   p.Tokenizer,
			Unsupported: joinCSV(p.UnsupportedParams),
			Models:      modelSnapshots(p),
			Keys:        keySnapshots(p),
			Primary:     i == 0 && cfg.Model.Primary == "" || p.Name == cfg.Model.Primary,
		})
	}
	return out
}

// modelSnapshots 该 provider 模型条目的展示快照（S11-7③）：Caps 取**生效值**（键缺失时
// 按协议缺省），CapsExplicit 区分「缺省」与「显式声明」（用户没动过就不固化声明）。
func modelSnapshots(p *providerConfig) []uigui.ProviderModelSnapshot {
	out := make([]uigui.ProviderModelSnapshot, 0, len(p.Models))
	for i := range p.Models {
		e := &p.Models[i]
		out = append(out, uigui.ProviderModelSnapshot{
			Name:          e.Name,
			Caps:          p.capsOf(*e).CapNames(),
			CapsExplicit:  e.Caps != nil,
			ContextWindow: e.ContextWindow,
			Enabled:       e.isEnabled(),
			CostIn:        e.CostIn,
			CostOut:       e.CostOut,
		})
	}
	return out
}

// keySnapshots 密钥池逐槽的**形态**快照（值绝不进快照，D35）：已入库的只给引用名，
// 明文的只标形态，空的标缺省回落。Label 为空的槽不给展示名（界面按「第 N 把」显示）。
func keySnapshots(p *providerConfig) []uigui.ProviderKeySnapshot {
	refs := p.keyRefs()
	out := make([]uigui.ProviderKeySnapshot, 0, len(refs))
	for i, ref := range refs {
		s := uigui.ProviderKeySnapshot{}
		if i < len(p.Keys) {
			s.Label = strings.TrimSpace(p.Keys[i].Label)
		}
		switch {
		case ref == "":
			// 缺省引用：界面按「回落缺省」显示。
		case strings.HasPrefix(ref, "secret:"):
			s.Saved = true
			s.Ref = ref
		default:
			s.Plain = true
		}
		out = append(out, s)
	}
	return out
}

// Apply 全量替换 providers（D173：provider 列表 + 每家的密钥池与模型条目）：
// 校验（名字唯一非空、base_url/models 非空、primary 命中）→ 泛键写回。原条目无关键
// （含 keys 与 model 的 caps/context_window）按 Orig 名保留——改名不丢密钥与能力声明，
// 新条目从空起步。fallback 悬挂引用（删除/改名产生的）一并清理；model.name 不动
// （启动 normalize 兜底缺省）。
//
// 密钥入口径（D151/D161）：编辑区填的**明文**写进本机密钥库，config 里只落
// `secret:<名>` 引用（名沿用同槽原引用名，否则按 provider 名 + 槽号生成）；填的本来就
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
		if len(p.Models) == 0 {
			return fmt.Errorf("provider %q 的 models 不可为空（首个 = 缺省请求模型）", p.Name)
		}
		enabled := 0
		for _, me := range p.Models {
			if !me.Disabled {
				enabled++
			}
			// 单价（D205）：写出去的配置必须能过启动校验（normalize 同一条口径）——
			// 负数与其等到重启才报错，不如现在拦住。
			if me.CostIn < 0 || me.CostOut < 0 {
				return fmt.Errorf("provider %q 的模型 %q 单价不可为负数（每百万 token）", p.Name, me.Name)
			}
		}
		if enabled == 0 {
			// 写出去的配置必须能过启动校验（normalize 同一条口径）：没有启用的模型
			// 等于这家 provider 没有可请求的模型，与其等到重启才报错，不如现在拦住。
			return fmt.Errorf("provider %q 的模型全部未启用；至少勾选一个", p.Name)
		}
	}
	if primary == "" {
		primary = patches[0].Name
	}
	if !names[primary] {
		return fmt.Errorf("primary %q 不在 provider 列表内", primary)
	}
	// 密钥先落库（config 写回之前）：任何一条写库失败就整体放弃，不留「config 已是引用、
	// 库里却没有值」的半个状态。逐槽原引用名从落盘 config 读——改名不 churn 引用。
	disk, _ := loadConfig(m.cfgPath)
	writes := make([][]keyWrite, len(patches))
	for i := range patches {
		p := &patches[i]
		orig := keyEntriesOf(disk, p.Orig)
		for j := range p.Keys {
			kp := p.Keys[j]
			w := keyWrite{label: strings.TrimSpace(kp.Label)}
			typed := strings.TrimSpace(kp.Value)
			if typed != "" {
				var origRef string
				if j < len(orig) {
					origRef = orig[j].ref
				}
				ref, err := storePlaintextKeyAt(context.Background(), m.bin, strings.TrimSpace(p.Name), origRef, typed, j)
				if err != nil {
					return err
				}
				w.ref = ref
				// 明文真的落库了（非空且不是引用）：递增密钥库快照 Revision，让同一次
				// 设置窗会话里切到「密钥库」页也能看见新条目（S9-6b/D163）。
				if m.onStore != nil && !strings.HasPrefix(typed, "secret:") {
					m.onStore()
				}
			}
			writes[i] = append(writes[i], w)
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
		for i := range patches {
			p := &patches[i]
			base := byOrig(p.Orig)
			entry := map[string]any{}
			if base != nil {
				for k, v := range base {
					entry[k] = v // 无关键保留（Q10 口径：未知键不丢）
				}
			}
			entry["name"] = strings.TrimSpace(p.Name)
			entry["base_url"] = strings.TrimSpace(p.BaseURL)
			if k := strings.TrimSpace(p.Kind); k != "" {
				entry["kind"] = k
			} else {
				delete(entry, "kind") // 空 = openai-chat（读侧归一）
			}
			entry["keys"] = writeKeys(base, writes[i])
			entry["models"] = writeModels(base, p.Models)
			entry["tokenizer"] = strings.TrimSpace(p.Tokenizer)
			entry["unsupported_params"] = anyList(splitCSV(p.Unsupported))
			list = append(list, entry)
		}
		model["providers"] = list
		model["primary"] = primary
		if fbs, ok := model["fallback"].([]any); ok {
			kept := make([]any, 0, len(fbs))
			for _, fb := range fbs {
				if pm, ok := fb.(map[string]any); ok {
					if s, _ := pm["provider"].(string); names[s] {
						kept = append(kept, fb) // 悬挂引用（删除/改名）清理
					}
				}
			}
			model["fallback"] = kept
		}
	})
}

// keyWrite 单槽密钥的落库结果：ref 空 = 保持原值（含原引用名）；label 空 = 沿用原 label。
type keyWrite struct {
	ref   string
	label string
}

// writeKeys 密钥池写回（D173）：逐槽以上次落盘条目为底（无关键保留），ref 非空覆盖
// api_key、label 非空覆盖 label；槽数少于一前次声明的按本次编辑为准（多余槽消失）。
// ws 为空（本次没提交任何密钥槽）= 整池保持原值（与「配置文件外部编辑」并存的口径）。
func writeKeys(base map[string]any, ws []keyWrite) []any {
	var old []any
	if base != nil {
		if v, ok := base["keys"].([]any); ok {
			old = v
		}
	}
	if len(ws) == 0 {
		if old != nil {
			return append([]any(nil), old...)
		}
		return []any{}
	}
	out := make([]any, 0, len(ws))
	for i, w := range ws {
		entry := map[string]any{}
		if i < len(old) {
			if pm, ok := old[i].(map[string]any); ok {
				for k, v := range pm {
					entry[k] = v
				}
			}
		}
		if w.ref != "" {
			entry["api_key"] = w.ref
		} else if _, ok := entry["api_key"]; !ok {
			entry["api_key"] = "" // 键恒存在（读侧 keyRefs 同口径）
		}
		if w.label != "" {
			entry["label"] = w.label
		}
		out = append(out, entry)
	}
	return out
}

// writeModels 模型条目写回（D173）：按名字继承原有条目的无关键与 caps（未动能力位时
// Caps = nil → 原样保留，含「键缺失」这个形态）；动了就以本次为准（含显式空数组）。
// context_window > 0 写入，否则不声明（跟随全局）。单价（D205）> 0 写入、0 删键。顺序以本次编辑为准。
func writeModels(base map[string]any, models []uigui.ProviderModelPatch) []any {
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
	out := make([]any, 0, len(models))
	for _, me := range models {
		name := strings.TrimSpace(me.Name)
		if name == "" {
			continue // 空名条目跳过（同启动 normalize 口径）
		}
		entry := map[string]any{}
		if pm, ok := byName[name]; ok {
			for k, v := range pm {
				entry[k] = v
			}
		}
		entry["name"] = name
		if me.ContextWindow > 0 {
			entry["context_window"] = me.ContextWindow
		} else {
			delete(entry, "context_window")
		}
		// 单价（D205）：> 0 才落键；0 = 未声明（留空即此形态，不写一个假的「免费」）。
		if me.CostIn > 0 {
			entry["cost_in"] = me.CostIn
		} else {
			delete(entry, "cost_in")
		}
		if me.CostOut > 0 {
			entry["cost_out"] = me.CostOut
		} else {
			delete(entry, "cost_out")
		}
		if me.Disabled {
			entry["enabled"] = false // 显式落键；启用是缺省形态，键不落盘（D180）
		} else {
			delete(entry, "enabled")
		}
		if me.Caps != nil {
			entry["caps"] = anyList(*me.Caps) // 显式声明（含空数组 = 三项全关）
		}
		out = append(out, entry)
	}
	return out
}

// keyEntriesOf 读磁盘配置里某 provider 现有密钥池（label + 引用原文）；未命中返回 nil。
func keyEntriesOf(cfg *fileConfig, orig string) []keyEntry {
	if cfg == nil || orig == "" {
		return nil
	}
	p := cfg.Model.byName(orig)
	if p == nil {
		return nil
	}
	refs := p.keyRefs()
	out := make([]keyEntry, 0, len(refs))
	for i, ref := range refs {
		e := keyEntry{ref: ref}
		if i < len(p.Keys) {
			e.label = strings.TrimSpace(p.Keys[i].Label)
		}
		out = append(out, e)
	}
	return out
}

// Test 连通性测试（D110②：真实最小请求）：以 patch 构造一次性 client（8s 超时）
// 拉 /models。
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

// Probe 对某模型发起三项能力探测并带回结论（S11-7③/D174；D181 修口径）：以**编辑区当前
// patch**（端点 / 协议 / 第一把钥匙）构造客户端并探测——改了端点还没应用时测的是「将
// 要应用的那份」，模型行是编辑区里新建的也能探。结论**无条件带回界面**（回填勾选）；
// 是否写回配置由装配根按「该模型是否已在配置里」判定（未声明 → Pending，等应用落盘）。
func (m *providerManager) Probe(model string, p uigui.ProviderPatch) (uigui.ProbeOutcome, error) {
	if m.probe == nil {
		return uigui.ProbeOutcome{}, fmt.Errorf("能力探测未接线")
	}
	return m.probe(context.Background(), p, strings.TrimSpace(model))
}

// client 以 patch 构造一次性 llm client（凭据解析口径 = Test/Models 共用）：协议取
// patch 的 Kind（S11-7③ 起协议在编辑面）；Kind 空则回退按 Orig 名从 config 取（兼容不
// 填 Kind 的调用方）。第一把密钥值空 = 取 config 内原条目同槽引用（按 Orig 名），再走
// D35 解析。
func (m *providerManager) client(p uigui.ProviderPatch) (*llm.Client, error) {
	cfg := m.loadCfg()
	var ref, kind string
	if len(p.Keys) > 0 {
		ref = strings.TrimSpace(p.Keys[0].Value)
	}
	if p.Orig != "" && cfg != nil {
		if e := keyEntriesOf(cfg, p.Orig); len(e) > 0 && ref == "" {
			ref = e[0].ref
		}
		if p.Kind == "" {
			if op := cfg.Model.byName(p.Orig); op != nil {
				kind = op.protocolKind()
			}
		}
	}
	if p.Kind != "" {
		kind = p.Kind
	}
	key, err := resolvePatchKey(context.Background(), p.Name, ref, m.secrets)
	if err != nil {
		return nil, err
	}
	return llm.New(llm.Config{
		BaseURL:    strings.TrimSpace(p.BaseURL),
		APIKey:     key,
		Protocol:   llmProtocol(kind),
		HTTPClient: &http.Client{Timeout: 8 * time.Second},
	})
}

// loadCfg 读配置（失败返回 nil；client 的凭据回落用）。
func (m *providerManager) loadCfg() *fileConfig {
	cfg, err := loadConfig(m.cfgPath)
	if err != nil {
		return nil
	}
	return cfg
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
