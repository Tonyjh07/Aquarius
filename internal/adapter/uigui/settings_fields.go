package uigui

// 保存流采集期的判等与换算：列表等价、密钥形态、成本解析、能力位与补丁构造。

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// webProvidersEqual 联网提供商列表等价（kind/来源/端点/国家语言/条数；密钥**不参与**
// ——值不回显，patch 里的空 = 保持，不能拿它判变更）。用于保存提示的变更判定。
func webProvidersEqual(a []WebProviderPatch, b []WebProviderView) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].From != b[i].From || a[i].BaseURL != b[i].BaseURL ||
			a[i].Country != b[i].Country || a[i].Lang != b[i].Lang || a[i].Count != b[i].Count {
			return false
		}
	}
	return true
}

// keyShapeAfterSave 保存后密钥框的形态：填了 `secret:` 引用 = 已存引用；填了明文 = 已收进
// 密钥库（引用名由保存路径派生，界面不重复那套规则，故不带名字）；留空 = 沿用原形态
// （D35 只写不回显，patch 里的空没有「清空」语义）。
func keyShapeAfterSave(typed string, wasSaved bool) bool {
	if typed == "" {
		return wasSaved
	}
	return true
}

// keyShapePlainAfterSave 同上，明文形态：只有「留空且原本就是明文」才保留明文标记
// ——填了新值之后 config 里落的是引用，不再标成明文（否则界面会一直说「启动会警告」）。
func keyShapePlainAfterSave(typed string, wasPlain bool) bool {
	return typed == "" && wasPlain
}

// keyRefAfterSave 保存后回显的引用名：填了引用就用新名；留空沿用旧名；填了明文则无名
// （名字由保存路径按规则派生，界面不重复实现，下次开窗自然显示）。
func keyRefAfterSave(typed, old string) string {
	switch {
	case typed == "":
		return old
	case strings.HasPrefix(typed, "secret:"):
		return typed
	}
	return ""
}

// webProviderViews 提交值 + 旧快照的密钥形态 → 新快照视图（密钥永不回显，只带形态）：
// 填了新值 = 明文（除非是 `secret:` 引用）；没填 = 沿用旧形态。
//
// 旧形态按 **kind** 对齐（与装配根侧写回同口径）：用户在前面插一条、或删掉一条，下标会
// 整体错位，按位置硬套会把 brave 的引用名贴到 deepseek 条目上——那条本来就不用自己的
// 密钥，界面会凭空显示一行「已存引用」。同类型多条按出现顺序依次认领。
func webProviderViews(ps []WebProviderPatch, old []WebProviderView) []WebProviderView {
	used := make([]bool, len(old))
	out := make([]WebProviderView, 0, len(ps))
	for _, p := range ps {
		v := WebProviderView{
			Kind: p.Kind, From: p.From, BaseURL: p.BaseURL,
			Country: p.Country, Lang: p.Lang, Count: p.Count,
		}
		for i := range old {
			if used[i] || old[i].Kind != p.Kind {
				continue
			}
			used[i] = true
			v.KeySaved, v.KeyPlain, v.KeyRef = old[i].KeySaved, old[i].KeyPlain, old[i].KeyRef
			break
		}
		if p.Key != "" {
			if strings.HasPrefix(p.Key, "secret:") {
				v.KeySaved, v.KeyPlain, v.KeyRef = true, false, p.Key
			} else {
				v.KeySaved, v.KeyPlain, v.KeyRef = false, true, ""
			}
		}
		out = append(out, v)
	}
	return out
}

// fallbackList 组装降级链提交（编辑区现值 → []FallbackLink）。provider 名与模型名都为空
// 的行整体跳过（用户点了「添加」却没填任何东西）——避免落一条无主的链项；模型名允许为空
// （= 取该 provider 的缺省模型）。
func (f *settingsForm) fallbackList() []FallbackLink {
	out := make([]FallbackLink, 0, len(f.chain))
	for _, ff := range f.chain {
		prov := strings.TrimSpace(ff.chainProvider())
		model := strings.TrimSpace(ff.model.Text())
		if prov == "" && model == "" {
			continue
		}
		out = append(out, FallbackLink{Provider: prov, Model: model})
	}
	return out
}

// promptModulesEqual 模块树视图等价（名称 + 正文 + 子模块序；保存提示的变更判定）。
func promptModulesEqual(a, b []PromptModuleView) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Text != b[i].Text || !slices.Equal(a[i].Children, b[i].Children) {
			return false
		}
	}
	return true
}

// profileActionLabel 三动作的反馈前缀（渲染与反馈共用）。
func profileActionLabel(act string) string {
	switch act {
	case "create":
		return "新建"
	case "copy":
		return "复制"
	case "switch":
		return "切换"
	}
	return act
}

// profileAction profile 区三动作（D110③/修订④）：名字非空校验 → ProfilesManager
// 执行 → 反馈写表单状态（成功绿/错误红，同保存流口径）并刷新快照。headless 可测
// （不进帧渲染）。

func providerPatchOf(pf *providerForm) ProviderPatch {
	p := ProviderPatch{
		Orig:        pf.orig,
		Name:        strings.TrimSpace(pf.name.Text()),
		Kind:        pf.kind.value(),
		BaseURL:     strings.TrimSpace(pf.baseURL.Text()),
		Tokenizer:   strings.TrimSpace(pf.tokenizer.Text()),
		Unsupported: strings.TrimSpace(pf.unsupported.Text()),
	}
	for _, kf := range pf.keyForms {
		p.Keys = append(p.Keys, ProviderKeyPatch{
			Value: strings.TrimSpace(kf.value.Text()),
			Label: strings.TrimSpace(kf.label.Text()),
		})
	}
	for _, mf := range pf.modelForms {
		name := strings.TrimSpace(mf.name.Text())
		if name == "" {
			continue // 空名模型行 = 尚未创建（cmd 侧同口径跳过）
		}
		win := 0
		if v := strings.TrimSpace(mf.contextWin.Text()); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				win = n
			}
		}
		mp := ProviderModelPatch{Name: name, ContextWindow: win, Disabled: !mf.enabled.Value}
		mp.CostIn = parseCost(mf.costIn.Text())
		mp.CostOut = parseCost(mf.costOut.Text())
		// 只有用户动过能力勾选才把声明固化成显式（未动 = nil，保持键缺失/原声明形态）。
		if mf.touched() {
			names := mf.capNames()
			mp.Caps = &names
		}
		p.Models = append(p.Models, mp)
	}
	return p
}

// parseCost 单价文本 → 数值（每百万 token，D205）：空 = 0（未声明、不折算）；非法或负数
// 按 0 取值——报错由 applyProviderChanges 先行拦截（那里能给出「哪家的哪个模型」，patch
// 组装本身不带错返回）。
func parseCost(s string) float64 {
	v := strings.TrimSpace(s)
	if v == "" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return 0
	}
	return f
}

// badCost 单价格子的语法问题（空 = 合法）：非法数字或负数。留空合法 = 不声明。
func badCost(s string) bool {
	v := strings.TrimSpace(s)
	if v == "" {
		return false
	}
	f, err := strconv.ParseFloat(v, 64)
	return err != nil || f < 0
}

// capNames 该模型行勾选的能力名（按 port.CapNames 顺序；空 = 三项全关的显式声明）。
func (mf *providerModelForm) capNames() []string {
	var out []string
	if mf.capsTools.Value {
		out = append(out, port.CapTools)
	}
	if mf.capsReason.Value {
		out = append(out, port.CapReasoning)
	}
	if mf.capsVision.Value {
		out = append(out, port.CapVision)
	}
	return out
}
