package uigui

// 遮罩「人格节点双模式编辑」（D154）测试：
//  1. 可编辑门——persona（parent == Root 的 system）可编辑、Root 不可、摘要只读；
//  2. 应用方式锁 Carry——菜单预选 Fresh/Clone 一律落 --keep；
//  3. 模板页签与设置页**同一份**编辑区（同一 promptEditor 类型/同一动作），保存只写
//     system_prompt + prompts 两键、「应用到本会话」= 按当前模板展开的 /edit --keep；
//  4. 逻辑宽度：人格卡片放宽到 640dp、其余节点仍 560dp。

import (
	"image"
	"strings"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ovPersonaID 夹具里的人首节点（parent = 结构根的 system）。
func ovPersonaID(st *historyState) conversation.MessageID {
	for _, n := range st.model.nodes {
		if n.role == conversation.RoleSystem && n.parent >= 0 &&
			st.model.nodes[n.parent].role == conversation.RoleRoot {
			return n.id
		}
	}
	return ""
}

// ovSystemNodeID 夹具里**非** persona 的 system 节点（摘要之类；只读）。
func ovSystemNodeID(st *historyState) conversation.MessageID {
	for _, n := range st.model.nodes {
		if n.role != conversation.RoleSystem || n.parent < 0 {
			continue
		}
		if st.model.nodes[n.parent].role != conversation.RoleRoot {
			return n.id
		}
	}
	return ""
}

// TestOverlayPersonaEditable 可编辑门：persona 可编辑且默认落 Carry；Root 与非 persona 的
// system 节点仍只读。
func TestOverlayPersonaEditable(t *testing.T) {
	u, st, _ := relGraphFixture(t)
	pid := ovPersonaID(st)
	if pid == "" {
		t.Fatal("夹具缺人格节点")
	}
	histOpenOverlay(t, u, st, pid, conversation.Fresh, overlayPageRendered)
	ov := st.ov
	if !ov.persona {
		t.Fatal("人格节点未被识别为 persona")
	}
	if !ov.editable {
		t.Fatal("人格节点应可编辑")
	}
	if ov.ed.ReadOnly {
		t.Fatal("人格节点的源码编辑器不应只读")
	}
	if ov.mode != conversation.Carry {
		t.Fatalf("人格节点应用方式 = %v, want Carry（改人格必须保留后续历史）", ov.mode)
	}

	// Root 仍只读。
	var rootID conversation.MessageID
	for _, n := range st.model.nodes {
		if n.role == conversation.RoleRoot {
			rootID = n.id
		}
	}
	st.openOverlay(u, rootID, conversation.Fresh, overlayPageSource)
	if st.ov.editable || st.ov.persona {
		t.Fatal("Root 节点应只读且不是 persona")
	}
	st.closeOverlay()

	// 非 persona 的 system 节点（摘要）只读：它改的是一段上下文快照，不是人格。
	if sid := ovSystemNodeID(st); sid != "" {
		st.openOverlay(u, sid, conversation.Fresh, overlayPageRendered)
		if st.ov.editable {
			t.Fatalf("摘要节点 %s 应只读", sid)
		}
		if st.ov.page == overlayPageTemplate {
			t.Fatal("非 persona 节点不应有模板页签")
		}
		st.closeOverlay()
	}
}

// TestOverlayPersonaFreshForcedToCarry 菜单预选 Fresh/Clone 也不能把人格改成那一档：
// Fresh 会把整段对话挪成旧分支。
func TestOverlayPersonaFreshForcedToCarry(t *testing.T) {
	for _, preset := range []conversation.KeepMode{conversation.Fresh, conversation.Clone} {
		u, st, _ := relGraphFixture(t)
		pid := ovPersonaID(st)
		histOpenOverlay(t, u, st, pid, preset, overlayPageSource)
		if st.ov.mode != conversation.Carry {
			t.Fatalf("预选 %v 后 mode = %v, want Carry", preset, st.ov.mode)
		}
		st.ov.ed.SetText("新人格")
		st.ov.submit(u, st)
		select {
		case in := <-u.inCh:
			args := in.Command.Args
			if len(args) != 3 || args[0] != string(pid) || args[1] != "--keep" {
				t.Fatalf("人格提交 Args = %v, want [%s --keep 文本]", args, pid)
			}
		default:
			t.Fatal("inCh 空：人格提交未投 /edit")
		}
	}
}

// TestOverlayPersonaTabs 页签集：人格节点三档（呈现/源码/默认模板），其余节点两档；
// 模板页签在打开时不构造编辑区（惰性），切过去才构造。
func TestOverlayPersonaTabs(t *testing.T) {
	u, st, _ := relGraphFixture(t)
	pid := ovPersonaID(st)
	st.openOverlay(u, pid, conversation.Fresh, overlayPageRendered)
	if st.ov.prompt != nil {
		t.Fatal("未切到模板页签就不该构造编辑区")
	}
	st.closeOverlay()

	st.openOverlay(u, pid, conversation.Fresh, overlayPageTemplate)
	if st.ov.prompt == nil {
		t.Fatal("直接落在模板页签时应构造编辑区")
	}
	if len(st.ov.prompt.promptMods) == 0 && len(st.ov.prompt.builtins) == 0 {
		t.Log("快照为空（夹具未配 Settings 回调）——不影响本用例的构造断言")
	}

	// 非人格节点：模板页签不可达（ensureTemplate 不构造），页签行只画两枚。
	st.closeOverlay()
	st.openOverlay(u, ovUserID(st), conversation.Fresh, overlayPageTemplate)
	if st.ov.prompt != nil {
		t.Fatal("非人格节点不应构造模板编辑区")
	}
}

// ovTemplateUI 带人格快照与窄写回回调的遮罩测试 UI（模板页签用例专用）。
func ovTemplateUI(t *testing.T) (*UI, *historyState, *input.Router, *[]PromptTemplatePatch, *[]string) {
	t.Helper()
	u, st, q := relGraphFixture(t)
	var saved []PromptTemplatePatch
	u.opts.Settings = func() SettingsSnapshot { return promptSnapshot() }
	u.opts.ApplyPromptTemplate = func(p PromptTemplatePatch) error {
		saved = append(saved, p)
		return nil
	}
	var previews []string
	u.opts.PreviewPrompts = func(mods []PromptModuleView, persona []string, legacy string) string {
		names := make([]string, 0, len(mods))
		for _, m := range mods {
			names = append(names, m.Name+"="+m.Text)
		}
		out := "EXPANDED[" + strings.Join(names, ",") + "|" + strings.Join(persona, ",") + "|" + legacy + "]"
		previews = append(previews, out)
		return out
	}
	return u, st, q, &saved, &previews
}

// TestOverlayTemplateSavesOnlyPromptKeys 模板页签 = 设置页同一份编辑区的实例；
// 「保存模板」走窄写回（只带人格两键），不改其它设置字段。
func TestOverlayTemplateSavesOnlyPromptKeys(t *testing.T) {
	u, st, _, saved, _ := ovTemplateUI(t)
	pid := ovPersonaID(st)
	st.openOverlay(u, pid, conversation.Fresh, overlayPageTemplate)
	ov := st.ov
	if ov.prompt == nil {
		t.Fatal("模板编辑区未构造")
	}
	// 与设置页同一实现：直接改共享编辑区的模块与引用，保存后应原样出现在窄 patch 里。
	ov.prompt.addPromptRef("@default")
	ov.prompt.promptMods[0].text.SetText("HOUSE-V2")
	ov.saveTemplate(u)

	if len(*saved) != 1 {
		t.Fatalf("窄写回调用 %d 次, want 1", len(*saved))
	}
	p := (*saved)[0]
	if got := p.Modules[0].Text; got != "HOUSE-V2" {
		t.Errorf("写回模块正文 = %q, want HOUSE-V2", got)
	}
	if len(p.Persona) != 3 || p.Persona[2] != "@default" {
		t.Errorf("写回人格引用 = %v, want [house @env @default]", p.Persona)
	}
	if p.SystemPrompt != "LEGACY" {
		t.Errorf("写回默认人格 = %q, want LEGACY", p.SystemPrompt)
	}
	if ov.tplErr || !strings.Contains(ov.tplStatus, "只对新会话生效") {
		t.Errorf("保存反馈 = %q（err=%v），want 成功提示", ov.tplStatus, ov.tplErr)
	}
}

// TestOverlayTemplateApplySubmitsExpandedEdit 「应用到本会话」= 按当前（未保存的）模板
// 展开最终文本 → /edit <persona> --keep <文本>，并关遮罩。
func TestOverlayTemplateApplySubmitsExpandedEdit(t *testing.T) {
	u, st, _, saved, _ := ovTemplateUI(t)
	pid := ovPersonaID(st)
	st.openOverlay(u, pid, conversation.Fresh, overlayPageTemplate)
	ov := st.ov
	ov.prompt.promptMods[0].text.SetText("HOUSE-APPLY")
	ov.applyTemplate(u, st)

	if len(*saved) != 0 {
		t.Fatalf("应用不该顺手写配置（写回 %d 次）", len(*saved))
	}
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "edit" {
			t.Fatalf("inCh = %+v, want /edit", in)
		}
		args := in.Command.Args
		if len(args) != 3 || args[0] != string(pid) || args[1] != "--keep" {
			t.Fatalf("Args = %v, want [%s --keep <展开文本>]", args, pid)
		}
		if !strings.Contains(args[2], "HOUSE-APPLY") || !strings.Contains(args[2], "EXPANDED[") {
			t.Fatalf("提交文本不是按模板展开的结果: %q", args[2])
		}
	default:
		t.Fatal("inCh 空：应用未投 /edit")
	}
	if st.ov != nil {
		t.Fatal("应用后应关遮罩（树已变，卡片里的快照已过期）")
	}
}

// TestOverlayTemplateApplyUnwiredPreview 预览未接线：明确报错、不投命令、不关遮罩
// （静默回退到「保存过的模板」会让用户以为改的行生效了）。
func TestOverlayTemplateApplyUnwiredPreview(t *testing.T) {
	u, st, _, _, _ := ovTemplateUI(t)
	u.opts.PreviewPrompts = nil
	pid := ovPersonaID(st)
	st.openOverlay(u, pid, conversation.Fresh, overlayPageTemplate)
	st.ov.applyTemplate(u, st)
	if st.ov == nil {
		t.Fatal("展开失败不应关遮罩（改的内容会凭空消失）")
	}
	if !st.ov.tplErr || st.ov.tplStatus == "" {
		t.Fatalf("应给红字反馈，实为 %q（err=%v）", st.ov.tplStatus, st.ov.tplErr)
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("未接线仍投出了命令: %+v", in)
	default:
	}
}

// TestOverlayPersonaCardWider 人格模式卡片上限 640dp（三列模块网格 + 预览需要宽度），
// 其余节点仍 560dp。
func TestOverlayPersonaCardWider(t *testing.T) {
	gtx, _ := frameGtxSize(input.Source{}, 900, 700)
	plain := overlayCardBoxFor(gtx, false)
	persona := overlayCardBoxFor(gtx, true)
	if plain.Dx() > 560 {
		t.Fatalf("普通卡片宽 = %d, want ≤560", plain.Dx())
	}
	if persona.Dx() != 640 {
		t.Fatalf("人格卡片宽 = %d, want 640（视口 900 足够）", persona.Dx())
	}
	if persona.Dy() <= plain.Dy() {
		t.Fatalf("人格卡片高 = %d, want > 普通 %d（85%% vs 70%%）", persona.Dy(), plain.Dy())
	}
}

// TestOverlayPersonaFrameRendersTabs 离屏渲染：人格遮罩的两个页签都画得出来（呈现实底
// + 模板页有模块磁贴），且页签行三枚底板——只读节点回归时这里会红。
func TestOverlayPersonaFrameRendersTabs(t *testing.T) {
	const viewW, viewH = 900, 700
	w, err := headless.NewWindow(viewW, viewH)
	if err != nil {
		t.Skipf("离屏渲染不可用: %v", err)
	}
	defer w.Release()

	u, st, q, _, _ := ovTemplateUI(t)
	pid := ovPersonaID(st)
	render := func() *image.RGBA {
		gtx, ops := frameGtxSize(q.Source(), viewW, viewH)
		historyFrame(gtx, u.th, u, st)
		q.Frame(ops)
		if err := w.Frame(ops); err != nil {
			t.Fatalf("离屏渲染失败: %v", err)
		}
		img := image.NewRGBA(image.Rect(0, 0, viewW, viewH))
		if err := w.Screenshot(img); err != nil {
			t.Skipf("取像失败: %v", err)
		}
		return img
	}

	st.openOverlay(u, pid, conversation.Fresh, overlayPageRendered)
	rendered := render()
	st.closeOverlay()
	st.openOverlay(u, pid, conversation.Fresh, overlayPageTemplate)
	tpl := render()

	diff := 0
	for y := 0; y < viewH; y++ {
		for x := 0; x < viewW; x++ {
			if rendered.RGBAAt(x, y) != tpl.RGBAAt(x, y) {
				diff++
			}
		}
	}
	if diff < 1000 {
		t.Fatalf("模板页签与呈现页几乎一致（差异像素 %d）——模板页没有画出来", diff)
	}
}

// 编译期钉住：设置表单（经内嵌提升）与遮罩宿主用的是同一个编辑区实现（漂移会直接编译失败）。
var (
	_ = (*settingsForm).promptViews
	_ = (*promptEditor).promptViews
	_ = (*settingsForm).rows
)

// TestOverlayPersonaEscFromTemplate Esc 在模板页签也关遮罩（焦点落在遮罩自身）。
func TestOverlayPersonaEscFromTemplate(t *testing.T) {
	u, st, q, _, _ := ovTemplateUI(t)
	pid := ovPersonaID(st)
	st.openOverlay(u, pid, conversation.Fresh, overlayPageTemplate)
	histFrameFor(q, u, st) // 焦点立起来（FocusCmd 在帧尾生效）
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	histFrameFor(q, u, st)
	if st.ov != nil {
		t.Fatal("模板页签 Esc 未关闭遮罩")
	}
}

// 编译期钉住：两个宿主用的都是同一个类型（漂移会直接编译失败）。
var (
	_ = (*settingsForm).promptViews
	_ = (*promptEditor).promptViews
)

// 占位断言：模板页签的命令走 inCh（不是主窗消息），与键入同路径。
var _ = port.Command{}
