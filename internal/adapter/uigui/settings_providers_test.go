package uigui

// 「模型服务」页测试（S11/D173–D177）：降级链编辑、provider 折叠卡片、密钥池、
// 模型能力表、自动拉取与分页、能力探测。

import (
	"errors"
	"fmt"
	"image"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeProviderMgr ProviderManager 假实现（Apply/Test/Models/Probe 分派断言）。
type fakeProviderMgr struct {
	snap         []ProviderSnapshot
	applied      [][]ProviderPatch
	primaryGot   []string
	testErr      error
	testPatches  []ProviderPatch
	models       []string
	modelsErr    error
	modelsMu     sync.Mutex
	modelsGot    []ProviderPatch
	probeErr     error
	probeOutcome ProbeOutcome
	probeGot     []probeCall
}

// probeCall 一次 Probe 调用的入参留痕。
type probeCall struct {
	provider string
	model    string
}

func (f *fakeProviderMgr) Snapshot() []ProviderSnapshot { return f.snap }
func (f *fakeProviderMgr) Apply(primary string, ps []ProviderPatch) error {
	for _, p := range ps { // 镜像 cmd 侧校验口径（空名拒写），失败不记账
		if strings.TrimSpace(p.Name) == "" {
			return errors.New("provider 名字不可为空")
		}
	}
	if primary == "" {
		return errors.New("primary 未命中")
	}
	f.primaryGot = append(f.primaryGot, primary)
	f.applied = append(f.applied, ps)
	return nil
}
func (f *fakeProviderMgr) Test(p ProviderPatch) error {
	f.testPatches = append(f.testPatches, p)
	return f.testErr
}

func (f *fakeProviderMgr) Models(p ProviderPatch) ([]string, error) {
	f.modelsMu.Lock()
	f.modelsGot = append(f.modelsGot, p)
	f.modelsMu.Unlock()
	return f.models, f.modelsErr
}

// modelsCalls Models 调用留痕的快照（自动拉取会并发起多个 goroutine，读取须走锁）。
func (f *fakeProviderMgr) modelsCalls() []ProviderPatch {
	f.modelsMu.Lock()
	defer f.modelsMu.Unlock()
	return append([]ProviderPatch(nil), f.modelsGot...)
}

func (f *fakeProviderMgr) Probe(model string, p ProviderPatch) (ProbeOutcome, error) {
	f.probeGot = append(f.probeGot, probeCall{provider: p.Name, model: model})
	return f.probeOutcome, f.probeErr
}

// TestFallbackChainEditorHeadless 降级链编辑（S11-7①/D176 口径三）：快照填表 →
// 每项 provider/model 控件就位；添加/上移/下移/删除；保存时链恒全量进 patch。
func TestFallbackChainEditorHeadless(t *testing.T) {
	restoreLight(t)
	var got SettingsPatch
	mgr := &fakeProviderMgr{snap: []ProviderSnapshot{
		snapProv("a", "http://a/v1", true, "am1", "am2"),
		snapProv("b", "http://b/v1", false, "bm1"),
	}}
	u := newHeadless(t, Options{
		ProviderMgr: mgr,
		Settings: func() SettingsSnapshot {
			s := fillSnapshot()
			s.Fallback = []FallbackLink{{Provider: "b", Model: "bm1"}}
			return s
		},
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { got = p; return nil, nil },
	})
	f := newSettingsForm(u)
	if len(f.chain) != 1 || len(f.chainChoices) != 2 {
		t.Fatalf("链加载 = %d 项, 选项 = %d", len(f.chain), len(f.chainChoices))
	}
	if f.chain[0].chainProvider(f.chainChoices) != "b" || f.chain[0].model.Text() != "bm1" {
		t.Fatalf("首项 = %q/%q", f.chain[0].chainProvider(f.chainChoices), f.chain[0].model.Text())
	}

	// 添加一项（缺省指向首个选项）→ 上移交换 → 删除。
	f.addFallbackForm()
	if len(f.chain) != 2 || f.chain[1].chainProvider(f.chainChoices) != "a" {
		t.Fatalf("添加后 = %d 项, 末项 = %q", len(f.chain), f.chain[1].chainProvider(f.chainChoices))
	}
	f.chain[1].model.SetText("am2")
	f.moveFallback(1, -1) // 上移：与首项交换
	if f.chain[0].chainProvider(f.chainChoices) != "a" || f.chain[0].model.Text() != "am2" {
		t.Fatalf("上移后首项 = %q/%q", f.chain[0].chainProvider(f.chainChoices), f.chain[0].model.Text())
	}
	f.moveFallback(0, -1) // 越界 no-op
	f.cycleChainProvider(0)
	if f.chain[0].chainProvider(f.chainChoices) != "b" {
		t.Fatalf("循环选择后 = %q, want b", f.chain[0].chainProvider(f.chainChoices))
	}

	u.saveSettings(f)
	want := []FallbackLink{{Provider: "b", Model: "am2"}, {Provider: "b", Model: "bm1"}}
	if len(got.Fallback) != len(want) {
		t.Fatalf("patch.Fallback = %+v, want %+v", got.Fallback, want)
	}
	for i := range want {
		if got.Fallback[i] != want[i] {
			t.Errorf("链[%d] = %+v, want %+v", i, got.Fallback[i], want[i])
		}
	}

	f.removeFallback(0)
	u.saveSettings(f)
	if len(got.Fallback) != 1 || got.Fallback[0].Model != "bm1" {
		t.Fatalf("删除后 patch.Fallback = %+v", got.Fallback)
	}
}

// TestFallbackChainEmptyAddRowSkipped 「＋ 添加」后未填任何内容 = 空行不落盘（链里不出现
// 无主项）；且清空链 = 全量写空（键存在但为空数组）。
func TestFallbackChainEmptyAddRowSkipped(t *testing.T) {
	restoreLight(t)
	var got SettingsPatch
	u := newHeadless(t, Options{
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{snapProv("a", "http://a/v1", true, "m1")}},
		Settings:      func() SettingsSnapshot { return fillSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { got = p; return nil, nil },
	})
	f := newSettingsForm(u)
	// 空链快照 → 添加一个控件但名字/模型都空；选项只有一个 provider，故 provIdx=0。
	// 先把该项 provider 名清空（模拟「没选任何东西」）再保存。
	f.addFallbackForm()
	f.chain[0].provIdx = -1
	f.chain[0].provName = ""
	u.saveSettings(f)
	if got.Fallback == nil || len(got.Fallback) != 0 {
		t.Fatalf("空行应被跳过：patch.Fallback = %+v", got.Fallback)
	}
}

// TestProviderRowsLiveOnProvidersPage 拆页契约（S11-7①/D176 口径一）：provider 编辑行
// 只挂「模型服务」页，「模型」页只剩 当前模型/思考/档位/降级链——防两页都挂或都不挂
// （拆页时最容易犯的错是把行计划整体复制一份而忘了删原处）。
func TestProviderRowsLiveOnProvidersPage(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:    func() SettingsSnapshot { return fillSnapshot() },
		ProviderMgr: &fakeProviderMgr{snap: []ProviderSnapshot{snapProv("a", "", true)}},
	})
	f := newSettingsForm(u)
	provKinds := []rowKind{rowProvName, rowProvKind, rowProvBase, rowProvModels,
		rowProvTok, rowProvUnsup, rowProvKey, rowProvKeyAdd, rowProvModel, rowProvModelAdd,
		rowProvBtns, rowProvAdd, rowProvApply}
	chainKinds := []rowKind{rowChainItem, rowChainAdd}
	count := func(page int, kinds ...rowKind) int {
		n := 0
		for _, r := range f.pageRows(page) {
			for _, k := range kinds {
				if r.kind == k {
					n++
				}
			}
		}
		return n
	}
	// 收起态：只有摘要行（rowProvSummary），没有字段/按钮行；展开后才露出。
	if n := count(pageProviders, rowProvSummary); n != 1 {
		t.Errorf("「模型服务」页收起态摘要行 = %d, want 1", n)
	}
	for _, k := range []rowKind{rowProvName, rowProvBtns} {
		if n := count(pageProviders, k); n != 0 {
			t.Errorf("「模型服务」页收起态不应有 %v 行（收起只露摘要）", k)
		}
	}
	f.provOpen = 0 // 展开第一张卡片
	for _, k := range provKinds {
		if n := count(pageProviders, k); n == 0 {
			t.Errorf("「模型服务」页展开后缺 provider 行型 %v", k)
		}
		if n := count(pageModel, k); n != 0 {
			t.Errorf("「模型」页仍有 provider 行型 %v（D176 拆页后应搬到「模型服务」）", k)
		}
	}
	if n := count(pageModel, chainKinds...); n == 0 {
		t.Errorf("「模型」页缺降级链行（rowChainItem/rowChainAdd）")
	}
	if n := count(pageProviders, chainKinds...); n != 0 {
		t.Errorf("「模型服务」页不应有降级链行")
	}
}

// TestProviderCardCollapse 折叠卡片（S11-7②/D176 口径二）：同一时刻只展开一张；
// 再点摘要行收起；新增条目自动展开；收起后行计划里只留摘要行。
func TestProviderCardCollapse(t *testing.T) {
	restoreLight(t)
	mgr := &fakeProviderMgr{snap: []ProviderSnapshot{
		{Name: "a", Kind: "openai-chat", BaseURL: "http://a/v1", Primary: true,
			Models: []ProviderModelSnapshot{{Name: "am1"}, {Name: "am2"}},
			Keys:   []ProviderKeySnapshot{{Saved: true, Ref: "secret:K1"}, {Saved: true, Ref: "secret:K2"}}},
		{Name: "b", Kind: "anthropic", BaseURL: "http://b/v1",
			Models: []ProviderModelSnapshot{{Name: "bm1"}},
			Keys:   []ProviderKeySnapshot{{Saved: true, Ref: "secret:K3"}}},
	}}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	if f.provOpen != -1 {
		t.Fatalf("开窗应全收起: provOpen = %d", f.provOpen)
	}
	// 摘要文案：名字 · 协议 · N 个模型 · M 把钥匙 · 是否主。
	if got := f.provs[0].providerSummary(true); !strings.Contains(got, "a · chat · 2 个模型 · 2 把钥匙 · ★ 主") {
		t.Fatalf("摘要[0] = %q", got)
	}
	if got := f.provs[1].providerSummary(false); !strings.Contains(got, "b · anthropic · 1 个模型 · 1 把钥匙") || strings.Contains(got, "★") {
		t.Fatalf("摘要[1] = %q", got)
	}

	th := testTheme()
	frame := func() {
		var ops op.Ops
		settingsFrame(layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(720, 560)),
		}, th, u, f)
	}
	// 点第 2 张摘要行 = 展开它（同时收起其它）。
	f.provs[1].toggle.Click()
	frame()
	if f.provOpen != 1 {
		t.Fatalf("点第 2 张后 provOpen = %d, want 1", f.provOpen)
	}
	// 点第 1 张 = 只展开第 1 张（第 2 张自动收起）。
	f.provs[0].toggle.Click()
	frame()
	if f.provOpen != 0 {
		t.Fatalf("点第 1 张后 provOpen = %d, want 0（只展开一张）", f.provOpen)
	}
	// 再点第 1 张 = 收起。
	f.provs[0].toggle.Click()
	frame()
	if f.provOpen != -1 {
		t.Fatalf("再点应收起: provOpen = %d, want -1", f.provOpen)
	}
	// 新增条目自动展开且成为唯一展开项。
	f.addProviderForm()
	if f.provOpen != 2 {
		t.Fatalf("新增后应展开新条目: provOpen = %d, want 2", f.provOpen)
	}
}

// TestProviderEditorHeadless 编辑区生命周期：快照填表（primary 标记、密钥恒空、能力位
// 与窗口）、新增/设主/删除、全量应用分派（Orig 保留、能力位只在动过勾选时固化）、异步
// 连通测试回灌。
func TestProviderEditorHeadless(t *testing.T) {
	mgr := &fakeProviderMgr{snap: []ProviderSnapshot{
		snapProv("a", "http://a/v1", true, "m1"),
		snapProv("b", "http://b/v1", false, "mb"),
	}}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	if len(f.provs) != 2 || f.primaryIdx != 0 {
		t.Fatalf("编辑区 = %d 条, primary = %d", len(f.provs), f.primaryIdx)
	}
	if f.provs[1].keyForms[0].value.Text() != "" {
		t.Error("密钥只写不回显（D35）：编辑档必须恒空")
	}

	// 新增 + 设主 + 应用：patch 全量（Orig 保留）+ primary 分派。
	f.addProviderForm()
	u.applyProviderChanges(f) // 帧外可测段：新增的第 3 条无名字 → 报错
	if !f.provErr || !strings.Contains(f.provStatus, "名字") {
		t.Fatalf("空名应用反馈 = (%v, %q)", f.provErr, f.provStatus)
	}
	latest := f.provs[len(f.provs)-1]
	latest.name.SetText("c")
	latest.baseURL.SetText("http://c/v1")
	latest.modelForms[0].name.SetText("mc")
	u.applyProviderChanges(f)
	if f.provErr || len(mgr.applied) != 1 {
		t.Fatalf("应用反馈 = (%v, %q)", f.provErr, f.provStatus)
	}
	got := mgr.applied[0]
	if len(got) != 3 || got[0].Orig != "a" || got[2].Orig != "" {
		t.Fatalf("patches = %+v", got)
	}
	if mgr.primaryGot[0] != "a" {
		t.Fatalf("primary = %q, want a", mgr.primaryGot[0])
	}
	// 未动过勾选 → 能力位不出现在 patch（保留原声明形态）。
	if got[0].Models[0].Caps != nil {
		t.Fatalf("未动勾选不应固化能力声明: %+v", got[0].Models[0])
	}

	// 动过勾选 → patch 带显式能力列表。
	f.provs[0].modelForms[0].capsVision.Value = true
	u.applyProviderChanges(f)
	if caps := mgr.applied[1][0].Models[0].Caps; caps == nil || len(*caps) == 0 {
		t.Fatalf("动过勾选应固化能力声明: %+v", mgr.applied[1][0].Models[0])
	}

	// 启用位（D180）：缺省启用不进 patch 的禁用位；取消勾选 = Disabled。
	if mgr.applied[1][0].Models[0].Disabled {
		t.Fatal("默认启用不该标成 Disabled")
	}
	f.provs[0].modelForms[0].enabled.Value = false
	u.applyProviderChanges(f)
	if !mgr.applied[2][0].Models[0].Disabled {
		t.Fatal("取消勾选应提交 Disabled")
	}
	f.provs[0].modelForms[0].enabled.Value = true

	// 异步连通测试：结果经 testResults 回灌（帧循环外直接读）。
	u.runProviderTest(f, f.provs[0])
	out := <-f.testResults
	if out.err != nil || out.name != "a" {
		t.Fatalf("test result = %+v", out)
	}
	mgr.testErr = errors.New("401 unauthorized")
	u.runProviderTest(f, f.provs[1])
	out = <-f.testResults
	if out.err == nil {
		t.Fatal("test 失败应回灌错误")
	}
}

// TestProviderKeyPoolHeadless 密钥池多把编辑（S11-7③）：快照逐槽填表（值恒空、形态只标
// 已入库/明文/空）、添加/删除槽（≥1）、patch 逐槽带上 Value（空 = 保持）与 Label。
func TestProviderKeyPoolHeadless(t *testing.T) {
	mgr := &fakeProviderMgr{snap: []ProviderSnapshot{{
		Name: "a", BaseURL: "http://a/v1", Primary: true,
		Keys: []ProviderKeySnapshot{
			{Label: "主账号", Saved: true, Ref: "secret:KA"},
			{Plain: true},
			{Saved: true, Ref: "secret:KC", Label: "备用"},
		},
		Models: []ProviderModelSnapshot{{Name: "m1"}},
	}}}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	pf := f.provs[0]
	if len(pf.keyForms) != 3 {
		t.Fatalf("密钥槽 = %d, want 3", len(pf.keyForms))
	}
	// 值档恒空（D35）；展示名按快照填；形态注脚区分已入库/明文/空。
	if pf.keyForms[0].label.Text() != "主账号" || pf.keyForms[2].label.Text() != "备用" {
		t.Fatalf("展示名未填表: %q / %q", pf.keyForms[0].label.Text(), pf.keyForms[2].label.Text())
	}
	// 形态注脚三种形态各就其位（已入库露引用名、明文只标形态、空标缺省回落）。
	if !strings.Contains(keySlotNote(pf.keyForms[0]), "secret:KA") ||
		!strings.Contains(keySlotNote(pf.keyForms[1]), "明文") ||
		!strings.Contains(keySlotNote(pf.keyForms[2]), "secret:KC") {
		t.Fatalf("形态注脚 = %q / %q / %q",
			keySlotNote(pf.keyForms[0]), keySlotNote(pf.keyForms[1]), keySlotNote(pf.keyForms[2]))
	}

	// 填第 0 槽明文 + 改第 1 槽名 → patch 逐槽带值（空槽 = 保持）。
	pf.keyForms[0].value.SetText("sk-typed")
	pf.keyForms[1].label.SetText("轮换")
	u.applyProviderChanges(f)
	got := mgr.applied[0][0].Keys
	if len(got) != 3 {
		t.Fatalf("patch 密钥槽 = %d, want 3", len(got))
	}
	if got[0].Value != "sk-typed" || got[1].Value != "" || got[1].Label != "轮换" {
		t.Fatalf("patch 密钥 = %+v", got)
	}

	// 添加一槽（空）+ 删除一槽：仍 ≥1 槽。
	pf.keyForms = append(pf.keyForms, newProviderKeyForm(ProviderKeySnapshot{}))
	if len(pf.keyForms) != 4 {
		t.Fatalf("添加后槽数 = %d, want 4", len(pf.keyForms))
	}
	f.removeProvider(0)
	if len(f.provs) != 1 {
		t.Fatalf("唯一 provider 不该被删掉: %d", len(f.provs))
	}
}

// TestProviderModelTableHeadless 模型能力表（S11-7③）：勾选初值 = 生效能力（缺省按协议
// 给全）；上下文窗口填表；「未动」→ Caps nil、「动过」→ 显式列表。
func TestProviderModelTableHeadless(t *testing.T) {
	mgr := &fakeProviderMgr{snap: []ProviderSnapshot{{
		Name: "a", Kind: "anthropic", BaseURL: "http://a/v1", Primary: true,
		Models: []ProviderModelSnapshot{
			{Name: "claude", Caps: []string{port.CapTools, port.CapVision}, CapsExplicit: false, ContextWindow: 200000,
				CostIn: 3, CostOut: 15},
		},
	}}}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	mf := f.provs[0].modelForms[0]
	// 生效能力（anthropic 缺省 = 工具 + 图片）画进勾选，思考不勾。
	if !mf.capsTools.Value || mf.capsReason.Value || !mf.capsVision.Value {
		t.Fatalf("勾选初值 = tools:%v reason:%v vision:%v", mf.capsTools.Value, mf.capsReason.Value, mf.capsVision.Value)
	}
	if mf.contextWin.Text() != "200000" {
		t.Fatalf("窗口填表 = %q", mf.contextWin.Text())
	}
	// 单价（D205）回显：填过的照抄，留空的不写「0」。
	if mf.costIn.Text() != "3" || mf.costOut.Text() != "15" {
		t.Fatalf("单价填表 = (%q, %q)", mf.costIn.Text(), mf.costOut.Text())
	}
	// 未动 → patch 不带 Caps（保留「键缺失」这个形态）；窗口与单价照写。
	u.applyProviderChanges(f)
	got := mgr.applied[0][0].Models[0]
	if got.Caps != nil {
		t.Fatalf("未动能力位不该固化声明: %+v", got)
	}
	if got.ContextWindow != 200000 {
		t.Fatalf("窗口 = %d, want 200000", got.ContextWindow)
	}
	if got.CostIn != 3 || got.CostOut != 15 {
		t.Fatalf("单价 = (%v, %v)", got.CostIn, got.CostOut)
	}
	// 动一下思考 → patch 带显式列表（含新勾的思考）。
	mf = f.provs[0].modelForms[0] // 上一轮 apply 已按快照重建编辑区，需重新取指针
	mf.capsReason.Value = true
	u.applyProviderChanges(f)
	got = mgr.applied[1][0].Models[0]
	if got.Caps == nil || len(*got.Caps) != 3 {
		t.Fatalf("动过能力位应固化显式声明: %+v", got)
	}
}

// TestProviderModelCostValidation 单价语法（D205）：留空合法（不声明）；非法或负数在
// 应用前就拦下，且反馈指名道姓地说是哪家的哪个模型。
func TestProviderModelCostValidation(t *testing.T) {
	mgr := &fakeProviderMgr{snap: []ProviderSnapshot{{
		Name: "a", BaseURL: "http://a/v1", Primary: true,
		Models: []ProviderModelSnapshot{{Name: "m1", Enabled: true}},
	}}}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	mf := f.provs[0].modelForms[0]

	mf.costIn.SetText("abc")
	u.applyProviderChanges(f)
	if !f.provErr || !strings.Contains(f.provStatus, "单价") {
		t.Fatalf("非法单价反馈 = (%v, %q)", f.provErr, f.provStatus)
	}
	if len(mgr.applied) != 0 {
		t.Fatalf("校验失败不该落盘: %+v", mgr.applied)
	}

	mf = f.provs[0].modelForms[0]
	mf.costIn.SetText("-1")
	u.applyProviderChanges(f)
	if !f.provErr {
		t.Fatal("负单价应被拦截")
	}

	mf = f.provs[0].modelForms[0]
	mf.costIn.SetText("0.15")
	if err := func() error {
		p := providerPatchOf(f.provs[0])
		if p.Models[0].CostIn != 0.15 {
			t.Fatalf("单价解析 = %v", p.Models[0].CostIn)
		}
		u.applyProviderChanges(f)
		return nil
	}(); err != nil || f.provErr {
		t.Fatalf("合法单价应放行: %v / %q", err, f.provStatus)
	}
}

// TestFetchModelsAppendsRows 拉服务端清单（S11-7③；D181 改版）：**全部**并进模型列表
// （已存在的名字跳过），新增行默认未启用、只落在本卡片的反馈里；失败不动表；结果按
// provider 名回灌（期间删过条目就丢弃）。
func TestFetchModelsAppendsRows(t *testing.T) {
	mgr := &fakeProviderMgr{
		snap:   []ProviderSnapshot{snapProv("a", "http://a/v1", true, "m1")},
		models: []string{"m1", "m2", "m3"}, // m1 已存在 → 只补 m2/m3
	}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	pf := f.provs[0]
	wait := func() {
		t.Helper()
		out := <-f.testResults
		f.testResults <- out
		f.drainTestResults()
	}

	u.runFetchModels(f, pf, false)
	wait()
	if got := mgr.modelsCalls(); len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("Models 调用 = %+v", got)
	}
	if len(pf.modelForms) != 3 {
		t.Fatalf("模型行 = %d, want 3（补入 m2/m3）", len(pf.modelForms))
	}
	if n := pf.modelForms[1].name.Text(); n != "m2" {
		t.Fatalf("补入行 = %q", n)
	}
	if !pf.modelForms[0].enabled.Value {
		t.Fatal("原有模型应保持启用")
	}
	if pf.modelForms[1].enabled.Value || pf.modelForms[2].enabled.Value {
		t.Fatal("补入的模型行应默认未启用（要用户自己勾）")
	}
	if !strings.Contains(pf.fetchNote, "已补入 2 个模型") || pf.fetchErr {
		t.Fatalf("卡片反馈 = (%v, %q)", pf.fetchErr, pf.fetchNote)
	}
	if !strings.Contains(f.provStatus, "已补入 2 个模型") {
		t.Fatalf("手动拉取也应写全局状态行：%q", f.provStatus)
	}

	// 再拉同一批：都在表里 → 不生长、反馈说明。
	u.runFetchModels(f, pf, false)
	wait()
	if len(pf.modelForms) != 3 {
		t.Fatalf("重复拉取不该生长行：%d", len(pf.modelForms))
	}
	if !strings.Contains(pf.fetchNote, "都已在列表中") {
		t.Fatalf("重复拉取反馈 = %q", pf.fetchNote)
	}

	// 失败：不动模型表，反馈报因。
	before := len(pf.modelForms)
	mgr.modelsErr = errors.New("403 forbidden")
	u.runFetchModels(f, pf, false)
	wait()
	if len(pf.modelForms) != before {
		t.Fatalf("失败不该改模型表：%d → %d", before, len(pf.modelForms))
	}
	if !pf.fetchErr || !strings.Contains(pf.fetchNote, "失败") {
		t.Fatalf("失败反馈 = (%v, %q)", pf.fetchErr, pf.fetchNote)
	}

	// 自动拉取失败不该污染全局状态行（那是进页面的附带动作）。
	f.provStatus = ""
	u.runFetchModels(f, pf, true)
	wait()
	if f.provStatus != "" || !pf.fetchErr {
		t.Fatalf("自动拉取失败只落卡片：status=%q note=%q", f.provStatus, pf.fetchNote)
	}

	// 结果回灌按名字：名字对不上（期间删过该条目）→ 整条结果丢弃，不落到别的条目上。
	other := &providerForm{}
	other.name.SetText("b")
	other.modelForms = []*providerModelForm{newProviderModelForm(ProviderModelSnapshot{Name: "keep", Enabled: true})}
	f.provs = []*providerForm{other}
	mgr.modelsErr = nil
	mgr.models = []string{"x1"}
	u.runFetchModels(f, pf, false) // pf 已不在列表里（名字 a）
	<-f.testResults
	f.drainTestResults()
	if len(other.modelForms) != 1 || other.modelForms[0].name.Text() != "keep" {
		t.Fatalf("结果落到了别的 provider：%+v", other.modelForms)
	}
}

// TestFetchModelsPagination 清单不设上限、界面分页（D181）：一页 provModelPageSize 行，
// 页数按行数算，末页不整；行计划只在当前页给模型行（未渲染的行不排版）；删行后页码夹回。
func TestFetchModelsPagination(t *testing.T) {
	var listed []string
	for i := 0; i < provModelPageSize*2+3; i++ { // 2 整页 + 3 行
		listed = append(listed, fmt.Sprintf("mm%02d", i))
	}
	mgr := &fakeProviderMgr{
		snap:   []ProviderSnapshot{snapProv("a", "http://a/v1", true, "keep")},
		models: listed,
	}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	f.provOpen = 0
	pf := f.provs[0]
	u.runFetchModels(f, pf, false)
	out := <-f.testResults
	f.testResults <- out
	f.drainTestResults()

	want := 1 + len(listed)
	if len(pf.modelForms) != want {
		t.Fatalf("模型行 = %d, want %d（不设上限，全部入表）", len(pf.modelForms), want)
	}
	if pages := pf.modelPageCount(); pages != 3 {
		t.Fatalf("页数 = %d, want 3", pages)
	}
	// 第 1 页：只排 provModelPageSize 行。
	if n := len(pageRowsOfKind(f, pageProviders, rowProvModel)); n != provModelPageSize {
		t.Fatalf("首页模型行 = %d, want %d", n, provModelPageSize)
	}
	// 翻到最后一页：余下 4 行 + 分页条。
	pf.modelPage = 2
	rows := pageRowsOfKind(f, pageProviders, rowProvModel)
	if len(rows) != 4 {
		t.Fatalf("末页模型行 = %d, want 4", len(rows))
	}
	if rows[0].field != provModelPageSize*2 {
		t.Fatalf("末页首行下标 = %d, want %d", rows[0].field, provModelPageSize*2)
	}
	if n := len(pageRowsOfKind(f, pageProviders, rowProvModelPager)); n != 1 {
		t.Fatalf("多页时应有分页条 = %d, want 1", n)
	}
	// 行数回落到一页：分页条消失、页码夹回首页。
	pf.modelForms = pf.modelForms[:2]
	pf.modelPage = 5
	if start, end := pf.modelPageBounds(); start != 0 || end != 2 {
		t.Fatalf("bounds = (%d,%d), want (0,2)", start, end)
	}
	if n := len(pageRowsOfKind(f, pageProviders, rowProvModelPager)); n != 0 {
		t.Fatalf("单页不该有分页条 = %d", n)
	}
}

// pageRowsOfKind 当前页行计划里某行型的行（测试用小工具）。
func pageRowsOfKind(f *settingsForm, page int, kind rowKind) []rowDesc {
	var out []rowDesc
	for _, r := range f.pageRows(page) {
		if r.kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// TestProvidersAutoFetchOnce 切到「模型服务」页自动拉一轮（D181）：每家一次、每窗一次；
// 先手动拉过的卡片不重复请求；只读窗不拉。
func TestProvidersAutoFetchOnce(t *testing.T) {
	mgr := &fakeProviderMgr{
		snap: []ProviderSnapshot{
			snapProv("a", "http://a/v1", true, "m1"),
			snapProv("b", "http://b/v1", false, "bm1"),
		},
		models: []string{"m1", "m2"},
	}
	u := newHeadless(t, Options{ProviderMgr: mgr,
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil }})
	f := newSettingsForm(u)
	if f.provFetched {
		t.Fatal("开窗不该已经拉过")
	}
	providersPage{}.OnEnter(u, f)
	if !f.provFetched {
		t.Fatal("进页面应标记已拉取")
	}
	// 两家各一次（结果通道按「最新一条覆盖」设计，自动拉取不依赖逐条回灌）。
	deadline := time.Now().Add(2 * time.Second)
	for len(mgr.modelsCalls()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	f.drainTestResults()
	if n := len(mgr.modelsCalls()); n != 2 {
		t.Fatalf("Models 调用 = %d, want 2（每家一次）", n)
	}
	// 再来一次 OnEnter：不重复请求。
	providersPage{}.OnEnter(u, f)
	if n := len(mgr.modelsCalls()); n != 2 {
		t.Fatalf("重复进页面不该再拉：%d", n)
	}
}

// TestProbeModelRow 「探测能力」按钮（S11-7③/D174）：走 ProviderManager.Probe，结论回灌
// 该模型行（写回时同步勾选）。
func TestProbeModelRow(t *testing.T) {
	mgr := &fakeProviderMgr{
		snap: []ProviderSnapshot{snapProv("a", "http://a/v1", true, "m1")},
		probeOutcome: ProbeOutcome{
			Provider: "a", Model: "m1", Written: true,
			Results: []port.ProbeResult{
				{Capability: port.CapTools, Verdict: port.ProbeSupported},
				{Capability: port.CapReasoning, Verdict: port.ProbeUnsupported},
			},
		},
	}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	pf := f.provs[0]
	mf := pf.modelForms[0]
	u.runProbeModel(f, pf, mf)
	// 开跑即置忙（按钮右侧转圈）：这是同步置位的，不必等 goroutine 回灌。
	if !mf.probeBusy {
		t.Fatal("探测在途应置忙（转圈要立刻出现）")
	}
	if mf.probeStatus != "" {
		t.Fatalf("在途应清掉上一轮结论，实为 %q", mf.probeStatus)
	}
	// 在途重复点不叠发：否则连点几下会开好几条 30s 探测。
	u.runProbeModel(f, pf, mf)
	out := <-f.testResults
	if out.err != nil || out.kind != probeCaps || out.model != "m1" {
		t.Fatalf("probe result = %+v", out)
	}
	f.testResults <- out
	f.drainTestResults()
	if len(mgr.probeGot) != 1 || mgr.probeGot[0].model != "m1" || mgr.probeGot[0].provider != "a" {
		t.Fatalf("Probe 调用 = %+v（在途重复点不该再发一次）", mgr.probeGot)
	}
	if mf.probeBusy {
		t.Fatal("结果回灌后应解除在途态（转圈收掉）")
	}
	if mf.probeStatus == "" || mf.probeErr {
		t.Fatalf("行内反馈 = (%v, %q)", mf.probeErr, mf.probeStatus)
	}
	if !mf.capsTools.Value || mf.capsReason.Value {
		t.Fatalf("写回后勾选应与结论一致: tools=%v reason=%v", mf.capsTools.Value, mf.capsReason.Value)
	}

	// 三项探测必须**三项都报**：只统计支持/不支持会把「无结论」那一项藏起来，看起来
	// 像是少测了一项（实测踩中：图片返回空正文 → 界面只显示「2 项支持 / 0 项不支持」）。
	f.provs[0].modelForms[0].probeStatus = ""
	mgr.probeOutcome = ProbeOutcome{
		Provider: "a", Model: "m1",
		Results: []port.ProbeResult{
			{Capability: port.CapTools, Verdict: port.ProbeSupported},
			{Capability: port.CapReasoning, Verdict: port.ProbeSupported},
			{Capability: port.CapVision, Verdict: port.ProbeInconclusive, Reason: "回复为空"},
		},
	}
	u.runProbeModel(f, pf, pf.modelForms[0])
	out = <-f.testResults
	f.testResults <- out
	f.drainTestResults()
	want := "2 项支持 / 0 项不支持 / 1 项无结论"
	if !strings.Contains(mf.probeStatus, want) {
		t.Fatalf("三项都要报数：%q 缺 %q", mf.probeStatus, want)
	}
	// 空模型名不发探测。
	empty := pf.modelForms[0]
	empty.name.SetText("")
	u.runProbeModel(f, pf, empty)
	if !empty.probeErr || empty.probeStatus == "" {
		t.Fatalf("空模型名应就地报因: (%v, %q)", empty.probeErr, empty.probeStatus)
	}
}
