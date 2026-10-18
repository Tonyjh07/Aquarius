package uigui

// 人格模块树设置页（S7/D144；S7b/D145 独立成「系统提示词」页）：快照填表、模块/引用编辑、
// 保存组装、组合预览、离屏渲染。

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// promptSnapshot 人格模块树测试快照。
func promptSnapshot() SettingsSnapshot {
	return SettingsSnapshot{
		Permission:   "strict",
		Model:        "m1",
		SystemPrompt: "LEGACY",
		PromptModules: []PromptModuleView{
			{Name: "house", Text: "HOUSE:", Children: []string{"identity", "style"}},
			{Name: "identity", Text: "IDENT"},
		},
		PromptPersona: []string{"house", "@env"},
		Builtins: []BuiltinModuleView{
			{Name: "@default", Label: "默认人格"},
			{Name: "@env", Label: "运行环境信息（组）"},
		},
	}
}

// TestPromptsFormLoad 快照填表：模块行（名称/正文/子模块）与人格引用行（保序）就位，
// 内置模块清单进表单。
func TestPromptsFormLoad(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return promptSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
	if len(f.promptMods) != 2 {
		t.Fatalf("模块行 = %d, want 2", len(f.promptMods))
	}
	if got := f.promptMods[0].name.Text(); got != "house" {
		t.Errorf("模块 0 名 = %q, want house", got)
	}
	if got := f.promptMods[0].children.Text(); got != "identity, style" {
		t.Errorf("模块 0 子模块 = %q", got)
	}
	if got := f.promptMods[0].text.Text(); got != "HOUSE:" {
		t.Errorf("模块 0 正文 = %q", got)
	}
	if len(f.promptRefs) != 2 || f.promptRefs[0].name.Text() != "house" || f.promptRefs[1].name.Text() != "@env" {
		t.Errorf("人格引用未保序载入: %v", f.promptRefs)
	}
	if len(f.builtins) != 2 {
		t.Errorf("内置模块清单 = %d, want 2", len(f.builtins))
	}
}

// TestPromptModuleAssembleAndSave 保存组装：views 跳过空名行、children 切分去重；
// patch 带 PromptModules/PromptPersona；人格有变 → 反馈标注「只对新会话生效」。
func TestPromptModuleAssembleAndSave(t *testing.T) {
	var gotPatch SettingsPatch
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return promptSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { gotPatch = p; return nil, nil },
	})
	f := newSettingsForm(u)

	f.addPromptModule() // 空名行：保存时应跳过
	f.promptMods[len(f.promptMods)-1].name.SetText("style")
	f.promptMods[len(f.promptMods)-1].text.SetText("STYLE")
	f.promptMods[len(f.promptMods)-1].children.SetText("identity, identity, ") // 去重 + 去尾
	f.addPromptRef("style")
	f.sysPrompt.SetText("NEW-LEGACY")

	u.saveSettings(f)

	if f.err {
		t.Fatalf("保存报错: %s", f.status)
	}
	if gotPatch.SystemPrompt != "NEW-LEGACY" {
		t.Errorf("SystemPrompt = %q", gotPatch.SystemPrompt)
	}
	if len(gotPatch.PromptModules) != 3 {
		t.Fatalf("patch 模块数 = %d, want 3（空名行跳过）: %+v", len(gotPatch.PromptModules), gotPatch.PromptModules)
	}
	var style *PromptModuleView
	for i := range gotPatch.PromptModules {
		if gotPatch.PromptModules[i].Name == "style" {
			style = &gotPatch.PromptModules[i]
		}
	}
	if style == nil {
		t.Fatalf("新增模块 style 未进 patch: %+v", gotPatch.PromptModules)
	}
	if len(style.Children) != 1 || style.Children[0] != "identity" {
		t.Errorf("children 去重失败: %v", style.Children)
	}
	if len(gotPatch.PromptPersona) != 3 || gotPatch.PromptPersona[2] != "style" {
		t.Errorf("persona = %v, want [house @env style]", gotPatch.PromptPersona)
	}
	if !strings.Contains(f.status, "只对新会话生效") {
		t.Errorf("反馈未标注只对新会话生效: %q", f.status)
	}
}

// TestPromptRefReorderAndDelete 引用上/下移与删除（越界 no-op）。
func TestPromptRefReorderAndDelete(t *testing.T) {
	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot {
			return SettingsSnapshot{PromptPersona: []string{"a", "b", "c"}}
		},
	})
	f := newSettingsForm(u)
	f.movePromptRef(0, -1) // 越界 no-op
	if f.promptRefs[0].name.Text() != "a" {
		t.Fatalf("越界上移改动了顺序")
	}
	f.movePromptRef(2, 1) // 越界 no-op
	if f.promptRefs[2].name.Text() != "c" {
		t.Fatalf("越界下移改动了顺序")
	}
	f.movePromptRef(1, -1) // b 上移 → b a c
	if got := f.promptRefList(); got[0] != "b" || got[1] != "a" {
		t.Fatalf("上移结果 = %v", got)
	}
	f.delPromptRef(0) // 删 b → a c
	if got := f.promptRefList(); len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("删除结果 = %v", got)
	}
	f.delPromptModule(0) // 无模块：no-op 不 panic
}

// TestPromptsPreview 组合预览：PreviewPrompts 收到的就是表单现状（模块 + 引用 + 兜底），
// 结果写进 f.promptPrev 并渲染。
func TestPromptsPreview(t *testing.T) {
	var gotNodes []PromptModuleView
	var gotPersona []string
	var gotLegacy string
	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot { return promptSnapshot() },
		PreviewPrompts: func(nodes []PromptModuleView, persona []string, legacy string) string {
			gotNodes, gotPersona, gotLegacy = nodes, persona, legacy
			return "PREVIEW:" + strings.Join(persona, "+")
		},
	})
	f := newSettingsForm(u)
	f.page = pagePrompt
	f.addPromptRef("style")

	th := testTheme()
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Constraints{Max: image.Pt(600, 4000)}}
	settingsFrame(gtx, th, u, f)

	if gotLegacy != "LEGACY" {
		t.Errorf("预览 legacy = %q, want LEGACY（= system_prompt）", gotLegacy)
	}
	if len(gotPersona) != 3 || gotPersona[2] != "style" {
		t.Errorf("预览引用 = %v", gotPersona)
	}
	if len(gotNodes) != 2 {
		t.Errorf("预览模块 = %+v", gotNodes)
	}
	if !strings.Contains(f.promptPrev, "PREVIEW:house+@env+style") {
		t.Errorf("预览结果 = %q", f.promptPrev)
	}
}

// TestSettingsPromptPageRendersPrompts 离屏渲染：「系统提示词」页（S7b/D145 起独立成页）按真窗
// 尺寸光栅化后，页内容区下半幅确有内容（模块行/预览确实画出；防「新增行没挂进 pageRows」）。
func TestSettingsPromptPageRendersPrompts(t *testing.T) {
	const (
		viewW, viewH = 900, 700
		pxPerDp      = 1.25
		navWpx       = 140 * pxPerDp
	)
	sz := image.Pt(viewW, viewH)

	w, err := headless.NewWindow(viewW, viewH)
	if err != nil {
		t.Skipf("离屏渲染不可用（无 GPU/软件后端）: %v", err)
	}
	defer w.Release()

	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot { return promptSnapshot() },
		PreviewPrompts: func(nodes []PromptModuleView, persona []string, legacy string) string {
			return "组合预览：house = HOUSE:\nIDENT\nSTYLE"
		},
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
	f.page = pagePrompt
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

	bg := color.RGBAModel.Convert(img.At(2, 2)).(color.RGBA)
	contentBottom := viewH - 100
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
		t.Errorf("人格模块树页下半幅内容过少：行数=%d 像素=%d（want ≥20 行 / ≥300 px）——模块行/预览可能没挂进 pageRows",
			halfRows, halfPx)
	}
}
