package uigui

// 分页导航与窗几何测试（S4d/D142；D162 页面注册表）：页名集与页序契约、行型覆盖、
// 行列表真渲染回归、窗几何与切页、逐页离屏渲染、行腔留白。

import (
	"fmt"
	"image"
	"image/color"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestSettingsPageNamesContract 页名集与页序为契约（导航项与页标题同源，D162 起由注册表
// 推导）；每页行计划非空且首行为页标题——防「某页忘了挂行」；提示词行只挂「系统提示词」
// 页（S7b/D145）。另锁两条注册表结构不变量：页常量与注册表长度一致、Key 唯一。
func TestSettingsPageNamesContract(t *testing.T) {
	want := []string{"常规", "模型", "模型服务", "密钥库", "对话", "系统提示词", "工具与 MCP", "技能", "联网", "语音", "高级", "关于"}
	if settingsPageCount != len(want) {
		t.Fatalf("settingsPageCount = %d, want %d", settingsPageCount, len(want))
	}
	if int(pageConstCount) != settingsPageCount {
		t.Fatalf("pageConstCount = %d, settingsPageCount = %d（加页必须同时改常量块与注册表）",
			int(pageConstCount), settingsPageCount)
	}
	for i, name := range want {
		if settingsPageNames[i] != name {
			t.Errorf("页 %d 名 = %q, want %q", i, settingsPageNames[i], name)
		}
		if settingsPages[i].Key() != name {
			t.Errorf("注册表页 %d Key = %q, want %q", i, settingsPages[i].Key(), name)
		}
		if i > 0 && settingsPages[i].Key() == settingsPages[i-1].Key() {
			t.Errorf("注册表页 %d 与上一页 Key 重复（%q）", i, settingsPages[i].Key())
		}
	}
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return fillSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
		Profiles:      &fakeProfiles{snap: ProfilesSnapshot{Current: "default"}},
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "a", Primary: true}}},
	})
	f := newSettingsForm(u)
	for p := 0; p < settingsPageCount; p++ {
		rows := f.pageRows(p)
		if len(rows) < 2 {
			t.Fatalf("页 %d（%s）行计划过短: %d 行", p, settingsPageNames[p], len(rows))
		}
		if rows[0].kind != rowTitle {
			t.Errorf("页 %d（%s）首行 = %v, want rowTitle", p, settingsPageNames[p], rows[0].kind)
		} else if rows[0].text != settingsPageNames[p] {
			t.Errorf("页 %d 标题文案 = %q, want %q", p, rows[0].text, settingsPageNames[p])
		}
	}

	// 「模型」页必须含模型名行（D110② 起该行在 UI 上失位，S4d 补回）。
	found := false
	for _, r := range f.pageRows(pageModel) {
		if r.kind == rowModelName {
			found = true
		}
	}
	if !found {
		t.Error("「模型」页缺 rowModelName 行（当前模型名在 UI 上无处可改）")
	}
	// S7b/D145：提示词行归「系统提示词」页；「对话」页只留限额——防两页都挂或都不挂
	// （拆页时最容易犯的错是把 pageChat 的行计划整体复制一份而忘了删原处）。行数随快照
	// 变化的模块/引用/内置行在 TestPromptRowsLiveOnPromptPage 里带快照判定。
	promptFixed := []rowKind{rowSysPrompt, rowPromptNote, rowPromptGrid, rowPromptRefAdd, rowPromptPreview}
	has := func(page int, kind rowKind) bool {
		for _, r := range f.pageRows(page) {
			if r.kind == kind {
				return true
			}
		}
		return false
	}
	for _, k := range promptFixed {
		if !has(pagePrompt, k) {
			t.Errorf("「系统提示词」页缺行 %v", k)
		}
		if has(pageChat, k) {
			t.Errorf("「对话」页仍有提示词行 %v（D145 后应只留限额）", k)
		}
	}
	for _, k := range []rowKind{rowMaxTurns, rowMaxCtx, rowCompact, rowToolChars, rowToolTo} {
		if !has(pageChat, k) {
			t.Errorf("「对话」页缺限额行 %v", k)
		}
		if has(pagePrompt, k) {
			t.Errorf("「系统提示词」页混入限额行 %v", k)
		}
	}
}

// TestSettingsNativeRowsCoveredEveryPage D162：**行型覆盖**——每页 Rows() 产出的每一行
// 都必须被「框架通用行型」或「该页 Body」认领。改前未认领的行静默落进 rowBody 的 default
// 兜底、被画成别的控件（高级页 raw JSON 编辑器）；这里把那条静默去掉，用本测试响亮兜住：
// 新增行型忘了在对应页分派 = 本测试立刻红。
func TestSettingsNativeRowsCoveredEveryPage(t *testing.T) {
	secretsAdmin := &fakeSecretsAdmin{snap: SecretsSnapshot{Entries: []SecretEntry{{Name: "AQUARIUS_OPENAI_API_KEY", Mask: "••••••1234"}}}}
	secretsAdmin.rev = 1 // 让开窗/帧首同步即采纳（Revision 变了）
	u := newHeadless(t, Options{
		Settings:    func() SettingsSnapshot { return promptSnapshot() },
		Tools:       &fakeToolsAdmin{snap: ToolsSnapshot{Revision: 1, Builtins: []BuiltinToolState{{Name: "think", Enabled: true}}, Plugins: []PluginToolState{{Name: "p", Source: "config", Enabled: true}}}},
		ProviderMgr: &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "a", Primary: true}}},
		Secrets:     secretsAdmin,
		Skills: &fakeSkillsAdmin{snap: SkillsSnapshot{
			Revision:    1,
			ManagedRoot: "/profile/skills",
			Skills: []SkillState{{
				Name: "pdf", Description: "Fill PDFs.", Dir: "/profile/skills/pdf",
				Source: "/profile/skills", Managed: true, Kind: "git", Updated: "2026-10-22",
			}},
			Dirs:     []SkillDir{{Path: "/extra"}, {Path: "/profile/skills", Fallback: true}},
			Warnings: []string{"技能 /extra/bad 已跳过：缺少 name"},
		}},
	})
	f := newSettingsForm(u)
	f.page = pageTools
	f.syncToolsSnapshot() // 帧首同款：同步工具快照（铺出内置/插件行）
	f.toolsStatus = "ok"  // 让状态行也进行表（覆盖 rowToolsStatus 行型）
	f.page = pageSecrets
	f.syncSecretsSnapshot() // 同步密钥库快照（铺出条目行）
	f.secretsStatus = "ok"  // 覆盖 rowSecretStatus 行型
	f.page = pageSkills
	f.syncSkillsSnapshot() // 同步技能快照（铺出技能/目录/警告行）
	f.skillsStatus = "ok"  // 覆盖 rowSkillStatus 行型
	f.provOpen = 0         // 展开首张 provider 卡片（覆盖字段/按钮行型；收起态只有摘要行）
	th := testTheme()

	for p := 0; p < settingsPageCount; p++ {
		rows := f.pageRows(p)
		for i := range rows {
			var ops op.Ops
			gtx := layout.Context{
				Ops:         &ops,
				Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
				Constraints: layout.Constraints{Max: image.Pt(540, 400)},
			}
			if _, ok := frameworkRow(gtx, th, rows[i]); ok {
				continue
			}
			if _, ok := settingsPages[p].Body(gtx, th, f, rows, i); !ok {
				t.Errorf("页 %d（%s）第 %d 行（kind=%d）无人认领——加行型时忘了在对应页 Body 分派",
					p, settingsPageNames[p], i, rows[i].kind)
			}
		}
	}
}

func TestPromptRowsLiveOnPromptPage(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return promptSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
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
	kinds := []rowKind{rowPromptGrid, rowPromptRef, rowPromptBuiltin, rowPromptPreview}
	for _, page := range []int{pageChat, pageGeneral, pageModel, pageSecrets, pageTools, pageSkills, pageAdv} {
		if n := count(page, kinds...); n != 0 {
			t.Errorf("页 %s 出现了 %d 条提示词行（应只在「系统提示词」页）", settingsPageNames[page], n)
		}
	}
	if got, want := count(pagePrompt, rowPromptGrid), 1; got != want {
		t.Errorf("「系统提示词」页模块网格行 = %d, want %d", got, want)
	}
	if got, want := count(pagePrompt, rowPromptRef), 2; got != want {
		t.Errorf("「系统提示词」页引用行 = %d, want %d", got, want)
	}
	if got, want := count(pagePrompt, rowPromptBuiltin), 2; got != want {
		t.Errorf("「系统提示词」页内置模块行 = %d, want %d", got, want)
	}
	if got, want := count(pagePrompt, rowPromptPreview), 1; got != want {
		t.Errorf("「系统提示词」页预览行 = %d, want %d", got, want)
	}
}

// TestSettingsFrameRowsFillViewport 设置窗行列表真渲染回归（S4c 根因防线；S4d 分页后按
// 新几何与新帧结构复检）。
//
// 复现过的故障：`settingsForm` 的 `material.List` 未设 `layout.Axis` → 按**水平**轴向做
// 视口预算（按视口宽而非视口高迭代），内容仍按垂直堆叠绘制；累计两行即判定"填满"，
// 整个表单只画出首行，其余是空白窗底与越界的整宽底色带。
//
// 判据取用户可见事实而非内部结构：把设置窗按真窗尺寸/缩放离屏光栅化，要求**页内容区**
// 的下半幅与底带确有内容（轴向错配时下半幅恒为纯底色）。参照实现 = 真窗首帧
// 900×700px（`winSettings` 几何 720×560dp × 1.25 缩放）；底色取自左侧导航栏（纯
// windowBg），统计范围限定在全局底栏之上的页内容区。
func TestSettingsFrameRowsFillViewport(t *testing.T) {
	const (
		viewW, viewH = 900, 700 // 真窗首帧像素尺寸（720×560dp @1.25）
		pxPerDp      = 1.25     // 真窗 Metric（Win32 DPI 96→120 档实测值）
		navWpx       = 140 * pxPerDp
	)
	sz := image.Pt(viewW, viewH)

	w, err := headless.NewWindow(viewW, viewH)
	if err != nil {
		t.Skipf("离屏渲染不可用（无 GPU/软件后端）: %v", err)
	}
	defer w.Release()

	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot {
			return SettingsSnapshot{
				Permission: "strict", Model: "m1", Theme: "system",
				Scale: 1.0, FontSize: 15,
				MaxTurns: 8, MaxContextTokens: 128000, CompactThreshold: 0.8,
				ToolOutputChars: 4000, ToolTimeoutSec: 120,
				SystemPrompt: "persona", RawJSON: `{"model":{"name":"m1"}}`,
			}
		},
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
		Profiles:      &fakeProfiles{snap: ProfilesSnapshot{Current: "default"}},
	})
	f := newSettingsForm(u)
	th := testTheme()

	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Metric:      unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp},
		Constraints: layout.Exact(sz),
	}
	settingsFrame(gtx, th, u, f)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("离屏帧提交失败: %v", err)
	}
	img := image.NewRGBA(image.Rectangle{Max: sz})
	if err := w.Screenshot(img); err != nil {
		t.Fatalf("离屏取像失败: %v", err)
	}

	// 底色 = 窗体铺底（windowBg；导航栏顶角无任何控件覆盖）；逐行统计页内容区与底色
	// 不同的像素（文字/控件/底板）。
	bg := color.RGBAModel.Convert(img.At(2, 2)).(color.RGBA)
	contentBottom := viewH - 100 // 全局底栏（≈64dp@1.25）之上 = 页内容区
	rowsWith := func(y0, y1 int) (rows, px int) {
		for y := y0; y < y1; y++ {
			n := 0
			for x := int(navWpx) + 2; x < viewW; x++ {
				if color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) != bg {
					n++
				}
			}
			if n > 0 {
				rows++
				px += n
			}
		}
		return rows, px
	}

	halfRows, halfPx := rowsWith(contentBottom/2, contentBottom)
	if halfRows < 20 || halfPx < 300 {
		t.Errorf("页内容区下半幅内容过少：有内容的行数=%d 像素=%d（want ≥20 行 / ≥300 px）"+
			"——列表很可能按错误轴向迭代、只画出首行（S4c 根因）", halfRows, halfPx)
	}
	// 底带（内容区末 12%）也必须有内容：轴向错配时内容在首行之后即断流。
	band := contentBottom - contentBottom*12/100
	bottomRows, _ := rowsWith(band, contentBottom)
	if bottomRows < 5 {
		t.Errorf("页内容区底带（末 12%%）内容行数=%d，want ≥5——行列表未填满视口", bottomRows)
	}
	// 上幅有内容（正向对照：窗体确实渲染了表单而非全空）。
	topRows, _ := rowsWith(0, contentBottom/4)
	if topRows < 5 {
		t.Errorf("页内容区上幅内容行数=%d，want ≥5——表单未渲染", topRows)
	}
}

// TestSettingsRowGeometryTracksDensity 多度量几何回归（D113 验收补充）：行几何必须随
// Metric 缩放——固定裸 px 定高在高 DPI 下裁掉编辑器文字底部（CJK 字面行高 25px > 内容区
// 20px @1.25）、标签列变窄导致「接口地址」折行。判据取用户可见事实的几何等价物：
// ① editorBox 返回高 ≥ 编辑器同度量实测行高 + 2×Dp(8)（不裁字）且 ≥ Dp(fieldH)（按压区下限）；
// ② fieldRow 控件起点 = 行宽 − Dp(fieldLabelW) − Dp(10)（标签列随密度缩放，不折行）。
// 裸 px 实现（旧）在 dpi≠1 时两项皆红；dpi=1 恒绿作正向对照。
func TestSettingsRowGeometryTracksDensity(t *testing.T) {
	th := testTheme()
	const rowW = 600
	for _, dpi := range []float32{1, 1.25, 1.5, 2} {
		t.Run(fmt.Sprintf("dpi%.2f", dpi), func(t *testing.T) {
			m := unit.Metric{PxPerDp: dpi, PxPerSp: dpi}

			// 编辑器自然行高（同度量独立实例实测——编辑器有状态，不与被测框共用）。
			var mops op.Ops
			med := &widget.Editor{}
			med.SingleLine = true
			edDims := material.Editor(th, med, "").Layout(layout.Context{
				Ops: &mops, Metric: m,
				Constraints: layout.Constraints{Max: image.Pt(rowW, 1e6)},
			})

			var ops op.Ops
			gtx := layout.Context{
				Ops: &ops, Metric: m,
				Constraints: layout.Constraints{Max: image.Pt(rowW, 1e6)},
			}
			ed := &widget.Editor{}
			ed.SingleLine = true
			box := editorBox(th, ed, "hint")(gtx)
			if want := edDims.Size.Y + 2*gtx.Dp(8); box.Size.Y < want {
				t.Errorf("dpi %.2f: 编辑框高 %dpx < 编辑器行高 %dpx + 2×Dp(8)——文字底部被裁（D113 验收补充）",
					dpi, box.Size.Y, want)
			}
			if want := gtx.Dp(fieldH); box.Size.Y < want {
				t.Errorf("dpi %.2f: 编辑框高 %dpx < 下限 Dp(fieldH)=%dpx", dpi, box.Size.Y, want)
			}

			// 标签列宽（探针控件记录收到的 Flexed 约束）。口径：fieldLabelW 含右间距
			//（Inset{Right:10} 把标签 Min 压到 fieldLabelW−10，rigid 总占位 = fieldLabelW），
			// 故控件起点 = 行宽 − Dp(fieldLabelW)——随密度缩放即不折行。
			var gotCtrlMax int
			fieldRow(gtx, th, "接口地址", func(gtx layout.Context) layout.Dimensions {
				gotCtrlMax = gtx.Constraints.Max.X
				return layout.Dimensions{}
			})
			if want := rowW - gtx.Dp(fieldLabelW); gotCtrlMax != want {
				t.Errorf("dpi %.2f: 控件起点宽 %dpx，want %dpx（行宽 − Dp(fieldLabelW)）——标签列未随密度缩放",
					dpi, gotCtrlMax, want)
			}
		})
	}
}

// TestSettingsWindowGeometry S4d/D142：分页后窗加宽到 720×560（min 620×420）——左侧
// 140dp 导航栏之外仍留 ≈552dp 页内容区（改前 = 532dp，不得倒退）。
func TestSettingsWindowGeometry(t *testing.T) {
	w, h, minW, minH := winSettings.geometry()
	if w != 720 || h != 560 || minW != 620 || minH != 420 {
		t.Fatalf("几何 = (%d,%d,%d,%d), want (720,560,620,420)", w, h, minW, minH)
	}
	if got := w - int(navColumnDp); got < 532 {
		t.Errorf("页内容区宽 = %d, want ≥532（不低于分页前）", got)
	}
}

// TestSettingsNavClickSwitchesPage 导航项点击 = 切页（帧首消费，本帧即按新页渲染）；
// 切页不动任何控件状态（跨页编辑保留），且每页各自的滚动列表独立。
func TestSettingsNavClickSwitchesPage(t *testing.T) {
	restoreLight(t)
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return fillSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
	th := testTheme()
	frame := func() {
		var ops op.Ops
		settingsFrame(layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(720, 560)),
		}, th, u, f)
	}

	frame()
	if f.page != pageGeneral {
		t.Fatalf("初始页 = %d, want %d（常规）", f.page, pageGeneral)
	}
	f.model.SetText("m-keep") // 跨页编辑保留探针
	f.pages[pageGeneral].ScrollBy(3)
	frame()
	if f.pages[pageGeneral].Position.First < 3 {
		t.Fatalf("常规页滚动后 First = %d, want ≥3", f.pages[pageGeneral].Position.First)
	}
	f.navBtns[pageAdv].Click()
	frame()
	if f.page != pageAdv {
		t.Fatalf("点击「高级」导航项后 page = %d, want %d", f.page, pageAdv)
	}
	if f.model.Text() != "m-keep" {
		t.Errorf("切页后模型名档 = %q, want m-keep（跨页编辑应保留）", f.model.Text())
	}
	// 滚动位置按页独立：别的页没滚动过，原页位置保留。
	if got := f.pages[pageAdv].Position.First; got != 0 {
		t.Errorf("高级页未滚动过，First = %d, want 0（每页独立列表）", got)
	}
	if got := f.pages[pageGeneral].Position.First; got < 3 {
		t.Errorf("切页后常规页滚动位置丢失：First = %d, want ≥3", got)
	}
	// 回切：导航项可反复使用，页状态、控件状态与滚动位置都还在。
	f.navBtns[pageModel].Click()
	frame()
	if f.page != pageModel {
		t.Fatalf("回切「模型」后 page = %d, want %d", f.page, pageModel)
	}
	if f.model.Text() != "m-keep" {
		t.Errorf("回切后模型名档 = %q, want m-keep", f.model.Text())
	}
	f.navBtns[pageGeneral].Click()
	frame()
	if got := f.pages[pageGeneral].Position.First; got < 3 {
		t.Errorf("回切常规页后滚动位置丢失：First = %d, want ≥3", got)
	}
}

// TestSettingsPagesRenderContent 逐页离屏单帧渲染：每页**内容区**必须确有内容
// （S4c 同款防线的按页复制——分页后「某页空白」是新的可能故障）。
func TestSettingsPagesRenderContent(t *testing.T) {
	const (
		viewW, viewH = 900, 700 // 真窗首帧像素尺寸（720×560dp @1.25）
		pxPerDp      = 1.25
		navWpx       = 175 // 140dp × 1.25
		barPx        = 100 // 全局底栏（≈64dp@1.25）占位
	)
	w, err := headless.NewWindow(viewW, viewH)
	if err != nil {
		t.Skipf("离屏渲染不可用（无 GPU/软件后端）: %v", err)
	}
	defer w.Release()

	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot {
			return SettingsSnapshot{
				Permission: "strict", Model: "m1", Theme: "system",
				Scale: 1.0, FontSize: 15,
				MaxTurns: 8, MaxContextTokens: 128000, CompactThreshold: 0.8,
				ToolOutputChars: 4000, ToolTimeoutSec: 120,
				SystemPrompt: "persona", MCPServers: []string{"docs"},
				RawJSON: `{"model":{"name":"m1"}}`,
			}
		},
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
		Profiles:      &fakeProfiles{snap: ProfilesSnapshot{Current: "default", Items: []string{"default"}}},
		ProviderMgr: &fakeProviderMgr{snap: []ProviderSnapshot{
			snapProv("openai", "https://api.openai.com/v1", true, "m1"),
		}},
	})
	f := newSettingsForm(u)
	th := testTheme()

	for p := 0; p < settingsPageCount; p++ {
		var ops op.Ops
		gtx := layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp},
			Constraints: layout.Exact(image.Pt(viewW, viewH)),
		}
		f.page = p
		settingsFrame(gtx, th, u, f)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("页 %d 离屏帧提交失败: %v", p, err)
		}
		img := image.NewRGBA(image.Rectangle{Max: image.Pt(viewW, viewH)})
		if err := w.Screenshot(img); err != nil {
			t.Fatalf("页 %d 离屏取像失败: %v", p, err)
		}
		bg := color.RGBAModel.Convert(img.At(2, 2)).(color.RGBA) // 导航栏顶角 = windowBg
		px := 0
		for y := 0; y < viewH-barPx; y++ { // 底栏之上 = 页内容区（底栏恒有内容，不作判据）
			for x := navWpx + 2; x < viewW; x++ {
				if color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) != bg {
					px++
				}
			}
		}
		t.Logf("页 %d（%s）内容区非底色像素 = %d", p, settingsPageNames[p], px)
		if px < 1000 {
			t.Errorf("页 %d（%s）内容区非底色像素 = %d，want ≥1000——该页可能空白",
				p, settingsPageNames[p], px)
		}
	}
}

// TestRowInsetContract 行腔留白是六页共用的单一旋钮（D148）。
//
// 用户对「模块表单」与「运行卡」两处提了同一诉求（上下需要更多留白）——那说明是
// 全局密度问题。这条测试锁住「只在一个地方定义」这个事实：如果有人把某页改回自己的
// 内边距，六页就会重新变得疏密不一，而肉眼很难逐页比对发现。
func TestRowInsetContract(t *testing.T) {
	// 14 是修订前的值（文字贴描边）；抬到 18 是本决策的可观测结果。
	if rowInsetDp < 18 {
		t.Fatalf("rowInsetDp = %d，want ≥18（D148：行腔留白不得退回贴边密度）", rowInsetDp)
	}
	// 上界防止后续为了「更松」一路加码把设置页撑成一屏两行。
	if rowInsetDp > 24 {
		t.Fatalf("rowInsetDp = %d，want ≤24（再大设置页一屏放不下三行）", rowInsetDp)
	}
}
