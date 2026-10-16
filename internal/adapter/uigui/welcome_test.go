package uigui

// 欢迎 / 首次运行引导 headless 测试（§15.5/D141）：表单流（页推进 / 校验 / 保存 / 取消 /
// 连通性异步回灌）、三页离屏单帧渲染、两个宿主（装配前独立窗 / in-app 次窗）的接线、
// 菜单项集契约与设置窗入口——全部无窗口。

import (
	"errors"
	"image"
	"image/color"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// fakeWelcome WelcomeManager 测试替身（记录调用、可注入失败）。
type fakeWelcome struct {
	snap     WelcomeSnapshot
	applied  []WelcomePatch
	applyErr error
	tested   []WelcomePatch
	testErr  error
	marked   int
	markErr  error
}

func (f *fakeWelcome) Snapshot() WelcomeSnapshot { return f.snap }

func (f *fakeWelcome) Apply(p WelcomePatch) error {
	f.applied = append(f.applied, p)
	return f.applyErr
}

func (f *fakeWelcome) Test(p WelcomePatch) error {
	f.tested = append(f.tested, p)
	return f.testErr
}

func (f *fakeWelcome) MarkWelcomed() error { f.marked++; return f.markErr }

// welcomeEnvSnap 表单测试基准环境（快照 + 结局收集）。
func welcomeEnvSnap(mgr *fakeWelcome, setup bool, issues []string) (welcomeEnv, *[]SetupOutcome) {
	outs := &[]SetupOutcome{}
	snap := mgr.snap
	snap.Issues = issues
	env := welcomeEnv{mgr: mgr, setup: setup, snap: snap, done: func(o SetupOutcome) { *outs = append(*outs, o) }}
	return env, outs
}

// baseFake 标准数据面（可用的三类字段 + 已看标记）。
func baseFake() *fakeWelcome {
	return &fakeWelcome{snap: WelcomeSnapshot{
		Provider: "openai", BaseURL: "https://api.example/v1", Models: "m1, m2", Welcomed: false,
	}}
}

// TestWelcomeFormFill D141：快照填表（三类字段）；**密钥字段恒空**（只写不回显，D35）；
// 起始页 = 接入模型。
func TestWelcomeFormFill(t *testing.T) {
	f := newWelcomeForm(mustEnv(t, true, []string{"provider \"openai\" 缺密钥：环境变量 AQ_K 未设置"}))
	if f.name.Text() != "openai" || f.baseURL.Text() != "https://api.example/v1" || f.models.Text() != "m1, m2" {
		t.Fatalf("填表错误: %q %q %q", f.name.Text(), f.baseURL.Text(), f.models.Text())
	}
	if f.apiKey.Text() != "" {
		t.Fatalf("密钥字段恒空（只写不回显，D35）: %q", f.apiKey.Text())
	}
	if f.page != welcomeAccess {
		t.Fatalf("起始页 = %v, want 接入模型", f.page)
	}
}

// mustEnv 基准环境（快照来自 baseFake）。
func mustEnv(t *testing.T, setup bool, issues []string) welcomeEnv {
	t.Helper()
	env, _ := welcomeEnvSnap(baseFake(), setup, issues)
	return env
}

// TestWelcomeFormValidation D141 校验矩阵：必填三类恒拦；**装配前 + 问题清单非空 ⇒ 密钥必填**
// （引用或明文都收）；in-app（无问题清单）⇒ 密钥可留空 = 保持原值（D35）。
func TestWelcomeFormValidation(t *testing.T) {
	keyIssue := []string{"provider \"openai\" 缺密钥：环境变量 AQ_K 未设置"}
	cases := []struct {
		name    string
		setup   bool
		issues  []string
		mutate  func(f *welcomeForm)
		wantErr bool
	}{
		{"装配前缺密钥", true, keyIssue, nil, true},
		{"装配前填引用", true, keyIssue, func(f *welcomeForm) { f.apiKey.SetText("secret:AQ_K") }, false},
		{"装配前填明文", true, keyIssue, func(f *welcomeForm) { f.apiKey.SetText("sk-plain") }, false},
		{"in-app 留空保持原值", false, nil, nil, false},
		{"provider 名空", true, keyIssue, func(f *welcomeForm) {
			f.name.SetText("")
			f.apiKey.SetText("secret:AQ_K")
		}, true},
		{"base_url 空", true, keyIssue, func(f *welcomeForm) {
			f.baseURL.SetText("")
			f.apiKey.SetText("secret:AQ_K")
		}, true},
		{"models 空", true, keyIssue, func(f *welcomeForm) {
			f.models.SetText("  ")
			f.apiKey.SetText("secret:AQ_K")
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newWelcomeForm(mustEnv(t, c.setup, c.issues))
			if c.mutate != nil {
				c.mutate(f)
			}
			err := f.accessErr()
			if (err != nil) != c.wantErr {
				t.Fatalf("accessErr = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

// TestWelcomeFormPageFlow 页推进：接入页校验不过不前移；通过后 0→1→2，上一步可回退且
// 清反馈。
func TestWelcomeFormPageFlow(t *testing.T) {
	f := newWelcomeForm(mustEnv(t, true, []string{"provider \"openai\" 缺密钥：环境变量 AQ_K 未设置"}))
	f.onNext() // 密钥未填：拦在接入页
	if f.page != welcomeAccess || !f.err {
		t.Fatalf("校验不过应留在接入页并置错：page=%v err=%v", f.page, f.err)
	}
	f.apiKey.SetText("secret:AQ_K")
	f.onNext()
	if f.page != welcomeTutorial || f.err || f.status != "" {
		t.Fatalf("推进后应为教程页且清反馈：page=%v err=%v status=%q", f.page, f.err, f.status)
	}
	f.onNext()
	if f.page != welcomeDone {
		t.Fatalf("教程页应可推进到完成页：%v", f.page)
	}
	f.onPrev()
	if f.page != welcomeTutorial {
		t.Fatalf("上一步应回教程页：%v", f.page)
	}
}

// TestWelcomeFormSave 保存：patch 组装（密钥空 = 保持原值语义由数据面处置）→ Apply →
// 结束宿主（SetupDone）。
func TestWelcomeFormSave(t *testing.T) {
	mgr := baseFake()
	env, outs := welcomeEnvSnap(mgr, true, nil)
	f := newWelcomeForm(env)
	f.apiKey.SetText("secret:AQ_K")
	f.onSave()
	if len(mgr.applied) != 1 {
		t.Fatalf("Apply 调用 %d 次, want 1", len(mgr.applied))
	}
	got := mgr.applied[0]
	want := WelcomePatch{Provider: "openai", BaseURL: "https://api.example/v1", APIKey: "secret:AQ_K", Models: "m1, m2"}
	if got != want {
		t.Fatalf("patch = %+v, want %+v", got, want)
	}
	if len(*outs) != 1 || (*outs)[0] != SetupDone {
		t.Fatalf("结局 = %v, want [已完成]", *outs)
	}
}

// TestWelcomeFormSaveError 保存失败：留在接入页、报错、**不结束宿主**（用户可改后重试）。
func TestWelcomeFormSaveError(t *testing.T) {
	mgr := baseFake()
	mgr.applyErr = errors.New("写盘失败：磁盘满")
	env, outs := welcomeEnvSnap(mgr, true, nil)
	f := newWelcomeForm(env)
	f.onSave()
	if !f.err || !strings.Contains(f.status, "磁盘满") {
		t.Fatalf("失败反馈 = (%v, %q)", f.err, f.status)
	}
	if len(*outs) != 0 {
		t.Fatalf("失败不应结束宿主: %v", *outs)
	}
	if f.page != welcomeAccess {
		t.Fatalf("失败应退回接入页: %v", f.page)
	}
}

// TestWelcomeFormSkip 跳过 / 关闭：装配前 = 交回取消（调用方退出 + 指引，不置标记）；
// in-app = 置 welcomed（显式关闭即已看）后关窗。
func TestWelcomeFormSkip(t *testing.T) {
	mgr := baseFake()
	env, outs := welcomeEnvSnap(mgr, true, nil)
	f := newWelcomeForm(env)
	f.onSkip()
	if len(*outs) != 1 || (*outs)[0] != SetupCancelled {
		t.Fatalf("装配前跳过应取消: %v", *outs)
	}
	if mgr.marked != 0 {
		t.Fatalf("装配前取消不应置 welcomed（配置仍不完整，下次还要来）: %d", mgr.marked)
	}

	mgr2 := baseFake()
	env2, outs2 := welcomeEnvSnap(mgr2, false, nil)
	f2 := newWelcomeForm(env2)
	f2.onSkip()
	if mgr2.marked != 1 {
		t.Fatalf("in-app 关闭应置 welcomed: %d", mgr2.marked)
	}
	if len(*outs2) != 1 {
		t.Fatalf("in-app 关闭应关窗: %v", *outs2)
	}
}

// TestWelcomeFormFinishOnce 幂等：连点跳过只结束一次（关窗与按钮竞争面）。
func TestWelcomeFormFinishOnce(t *testing.T) {
	env, outs := welcomeEnvSnap(baseFake(), true, nil)
	f := newWelcomeForm(env)
	f.onSkip()
	f.onSkip()
	f.finish(SetupDone)
	if len(*outs) != 1 || (*outs)[0] != SetupCancelled {
		t.Fatalf("结局应只交一次且为首次者: %v", *outs)
	}
}

// TestWelcomeFormTestConnectivity 连通性测试：立即置「正在测试…」→ 异步结果经通道回灌帧
// 循环（drain）→ 通过/失败文案。
func TestWelcomeFormTestConnectivity(t *testing.T) {
	mgr := baseFake()
	env, _ := welcomeEnvSnap(mgr, false, nil)
	f := newWelcomeForm(env)
	f.onTest()
	if !strings.Contains(f.status, "正在测试") {
		t.Fatalf("应即时报「正在测试…」: %q", f.status)
	}
	waitStatus(t, f, "通过")
	if f.err || len(mgr.tested) != 1 {
		t.Fatalf("结果 = (%v, %q) 调用 %d 次", f.err, f.status, len(mgr.tested))
	}

	mgr2 := baseFake()
	mgr2.testErr = errors.New("连接超时")
	env2, _ := welcomeEnvSnap(mgr2, false, nil)
	f2 := newWelcomeForm(env2)
	f2.onTest()
	waitStatus(t, f2, "失败")
	if !f2.err || !strings.Contains(f2.status, "连接超时") {
		t.Fatalf("失败反馈 = (%v, %q)", f2.err, f2.status)
	}
}

// waitStatus 轮询排空异步结果直到状态含 want（≤1s）。
func waitStatus(t *testing.T, f *welcomeForm, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.drain()
		if strings.Contains(f.status, want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("状态未在时限内变为含 %q: %q", want, f.status)
}

// TestWelcomeUISideHost in-app 宿主（winWelcome 次窗）：完成 = Apply + 关窗；显式关闭 =
// 置 welcomed + 关窗；数据面 nil = 只读（保存报「未接线」）。
func TestWelcomeUISideHost(t *testing.T) {
	mgr := baseFake()
	closed := 0
	f := newUISideWelcome(mgr, func() { closed++ })
	if f.env.setup {
		t.Fatal("in-app 宿主不应是装配前形态")
	}
	f.apiKey.SetText("sk-plain")
	f.onSave()
	if len(mgr.applied) != 1 || closed != 1 {
		t.Fatalf("保存应 Apply 并关窗: applied=%d closed=%d", len(mgr.applied), closed)
	}

	closed = 0
	f2 := newUISideWelcome(mgr, func() { closed++ })
	f2.onSkip()
	if mgr.marked != 1 || closed != 1 {
		t.Fatalf("关闭应置 welcomed 并关窗: marked=%d closed=%d", mgr.marked, closed)
	}

	f3 := newUISideWelcome(nil, func() {})
	f3.onSave()
	if !f3.err || !strings.Contains(f3.status, "未接线") {
		t.Fatalf("无数据面应报未接线: (%v, %q)", f3.err, f3.status)
	}
}

// TestWelcomeFrameRendersPages 三页离屏单帧渲染（真窗尺寸 560×520dp @1.25）：各页都有
// 实质内容（非空窗）；教程页**下半幅**也必须有内容——锁住滚动列表的垂直轴向
// （S4c 教训：轴向错配 = 只画首行、下半幅纯底色）。
func TestWelcomeFrameRendersPages(t *testing.T) {
	const (
		viewW, viewH = 700, 650 // winWelcome 几何 560×520dp @1.25
		pxPerDp      = 1.25
	)
	sz := image.Pt(viewW, viewH)
	w, err := headless.NewWindow(viewW, viewH)
	if err != nil {
		t.Skipf("离屏渲染不可用（无 GPU/软件后端）: %v", err)
	}
	defer w.Release()

	u := newHeadless(t, Options{}) // 只为主题令牌就位（welcomeFrame 不接 *UI）
	_ = u
	th := testTheme()
	mgr := baseFake()
	env, _ := welcomeEnvSnap(mgr, true, []string{"provider \"openai\" 缺密钥：环境变量 AQ_K 未设置"})
	f := newWelcomeForm(env)
	f.apiKey.SetText("secret:AQ_K")

	for _, page := range []welcomePage{welcomeAccess, welcomeTutorial, welcomeDone} {
		f.page = page
		var ops op.Ops
		gtx := layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp},
			Constraints: layout.Exact(sz),
		}
		welcomeFrame(gtx, th, f)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("页 %v 离屏帧提交失败: %v", page, err)
		}
		img := image.NewRGBA(image.Rectangle{Max: sz})
		if err := w.Screenshot(img); err != nil {
			t.Fatalf("页 %v 离屏取像失败: %v", page, err)
		}
		bg := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA)
		rows, px := contentRows(img, bg, 0, viewH, viewW)
		if px < 1500 {
			t.Errorf("页 %v 内容像素过少：%d（疑似空窗）", page, px)
		}
		if page == welcomeTutorial {
			lowRows, _ := contentRows(img, bg, viewH/2, viewH, viewW)
			if lowRows < 8 {
				t.Errorf("教程页下半幅内容行数 = %d（want ≥8）——滚动列表可能按错误轴向迭代、只画出首行", lowRows)
			}
		} else if rows == 0 {
			t.Errorf("页 %v 无内容行", page)
		}
	}
}

// contentRows 统计 [y0,y1) 内含非底色像素的行数与像素数。
func contentRows(img *image.RGBA, bg color.RGBA, y0, y1, width int) (rows, px int) {
	for y := y0; y < y1; y++ {
		n := 0
		for x := 0; x < width; x++ {
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

// TestTrayMenuItemsNoWelcome D141：托盘项集**不含**欢迎窗入口（入口在设置窗）——项序与
// 文案为契约（与 logo 菜单的 TestLogoMenuItems 对偶）。
func TestTrayMenuItemsNoWelcome(t *testing.T) {
	items := trayMenuItems(true)
	want := []struct {
		id    uintptr
		label string
	}{
		{cmdToggle, "显示 / 隐藏输入窗"},
		{cmdTopMost, "窗口置顶"},
		{0, ""},
		{cmdSettings, "设置"},
		{cmdHistory, "会话历史"},
		{0, ""},
		{cmdExit, "退出"},
	}
	if len(items) != len(want) {
		t.Fatalf("项数 = %d, want %d（items=%+v）", len(items), len(want), items)
	}
	for i, w := range want {
		if items[i].id != w.id || items[i].label != w.label {
			t.Fatalf("items[%d] = {id:%d label:%q}, want {id:%d label:%q}", i, items[i].id, items[i].label, w.id, w.label)
		}
		if strings.Contains(items[i].label, "欢迎") {
			t.Fatalf("托盘菜单不应再带欢迎窗入口（D141 移入设置窗）: %q", items[i].label)
		}
	}
}

// TestSettingsRowPlanHasWelcome D141：欢迎引导行紧随 Profile 区（设置窗 = 唯一菜单外入口）。
func TestSettingsRowPlanHasWelcome(t *testing.T) {
	u := newHeadless(t, Options{Settings: func() SettingsSnapshot { return fillSnapshot() }})
	f := newSettingsForm(u)
	rows := f.rowPlan()
	idx := -1
	for i, r := range rows {
		if r.kind == rowWelcome {
			idx = i
			break
		}
	}
	if idx <= 0 {
		t.Fatalf("rowPlan 缺 rowWelcome（idx=%d）", idx)
	}
	if prev := rows[idx-1].kind; prev != rowProfBtns {
		t.Fatalf("欢迎引导行应紧随 Profile 区：前一行 = %v", prev)
	}
}

// TestOpenWelcomeSpawnsWindow D141：设置窗入口 → openWin(winWelcome)；单实例防重开。
func TestOpenWelcomeSpawnsWindow(t *testing.T) {
	var mu sync.Mutex
	var spawned []winKind
	u := newHeadless(t, Options{Welcome: baseFake()})
	u.wins.spawn = func(k winKind) *winHandle {
		mu.Lock()
		spawned = append(spawned, k)
		mu.Unlock()
		return &winHandle{done: make(chan struct{})}
	}
	u.OpenWelcome()
	u.OpenWelcome() // 已开 = 聚焦，不开第二实例
	mu.Lock()
	defer mu.Unlock()
	if len(spawned) != 1 || spawned[0] != winWelcome {
		t.Fatalf("开窗记录 = %v, want [winWelcome]", spawned)
	}
	if !u.wins.isOpen(winWelcome) {
		t.Fatal("winWelcome 应登记为已开")
	}
}

// TestTutorialTipsMatchMenus D141：教程页的「球右键 / 托盘右键」两行必须与真实菜单项集
// **逐项一致**（项集是契约、文案是用户可见事实——菜单改了两行不同步即测试失败）。
func TestTutorialTipsMatchMenus(t *testing.T) {
	menus := map[string][]menuIt{
		"球右键：":  logoMenuItems("strict", true),
		"托盘右键：": trayMenuItems(true),
	}
	found := map[string]bool{}
	for _, tip := range TutorialTips() {
		for prefix, items := range menus {
			rest, ok := strings.CutPrefix(tip, prefix)
			if !ok {
				continue
			}
			found[prefix] = true
			var want []string
			for _, it := range items {
				if it.label != "" { // 分隔线不作项
					want = append(want, it.label)
				}
			}
			if got := strings.Split(rest, "、"); !slices.Equal(got, want) {
				t.Fatalf("%s 行与菜单项集不一致:\n got %q\nwant %q", prefix, got, want)
			}
		}
	}
	for prefix := range menus {
		if !found[prefix] {
			t.Fatalf("教程页缺菜单说明行 %q", prefix)
		}
	}
}

// TestTutorialCommandNamesLocked D141：教程页命令清单的**名字与序**为契约（说明文案可改）；
// 名字与真实命令清单的对照见 cmd/aquarius 的 TestWelcomeTutorialCommandsExist。
func TestTutorialCommandNamesLocked(t *testing.T) {
	want := []string{"/new", "/list", "/switch", "/model", "/permission", "/edit", "/memory", "/usage", "/jobs", "/compact"}
	spec := TutorialCommands()
	if len(spec) != len(want) {
		t.Fatalf("教程命令数 = %d, want %d（%+v）", len(spec), len(want), spec)
	}
	for i, c := range spec {
		if name := strings.Fields(c[0])[0]; name != want[i] {
			t.Fatalf("spec[%d] = %q, want %q", i, name, want[i])
		}
		if strings.TrimSpace(c[1]) == "" {
			t.Fatalf("spec[%d] 缺说明文案: %+v", i, c)
		}
	}
}

// TestWelcomeGeometry D141：欢迎窗几何（三页 + 步骤条 + 状态行 + 底部按钮）。
func TestWelcomeGeometry(t *testing.T) {
	w, h, minW, minH := winWelcome.geometry()
	if w != 560 || h != 520 || minW != 480 || minH != 420 {
		t.Fatalf("几何 = (%d,%d,%d,%d)", w, h, minW, minH)
	}
	if winWelcome.title() != "Aquarius 欢迎" {
		t.Fatalf("标题 = %q", winWelcome.title())
	}
}
