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
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/legal"
)

// fakeWelcome WelcomeManager 测试替身（记录调用、可注入失败）。
type fakeWelcome struct {
	snap     WelcomeSnapshot
	applied  []WelcomePatch
	applyErr error
	tested   []WelcomePatch
	testErr  error
	models   []string
	marked   int
	markErr  error
	langs    []string // SetLang 调用序（只记值，不模拟落盘）
	langErr  error
	consents []int // SetConsent 调用序
	consErr  error
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

func (f *fakeWelcome) Models(p WelcomePatch) ([]string, error) {
	f.tested = append(f.tested, p)
	return f.models, f.testErr
}

func (f *fakeWelcome) MarkWelcomed() error { f.marked++; return f.markErr }

func (f *fakeWelcome) SetLang(lang string) error {
	f.langs = append(f.langs, lang)
	return f.langErr
}

func (f *fakeWelcome) SetConsent(version int) error {
	f.consents = append(f.consents, version)
	return f.consErr
}

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

// TestWelcomeLangSelection D278 第 1 页语言三态：初值取快照 ui.lang（空 = auto）；
// 装配前选了**当场生效**（文案与窗标题都是每帧现取词）；装配后只记录不热切；
// 落盘时机 = 离开第 1 页（同一个值只写一次），写失败拦在第 1 页并报因。
func TestWelcomeLangSelection(t *testing.T) {
	defer i18n.Use(i18n.SelectionZh)
	i18n.Use(i18n.SelectionZh)

	mgr := baseFake()
	env, _ := welcomeEnvSnap(mgr, true, nil)
	f := newWelcomeForm(env)
	if f.lang != i18n.SelectionAuto {
		t.Fatalf("快照 ui.lang 空应归一为 auto: %q", f.lang)
	}
	f.pickLang(i18n.SelectionEn)
	if i18n.Current() != i18n.LangEn {
		t.Fatalf("装配前选语言应当场生效: %q", i18n.Current())
	}
	if len(mgr.langs) != 0 {
		t.Fatalf("选语言本身不该落盘（翻页时才写）: %v", mgr.langs)
	}
	f.onNext()
	if f.page != welcomeTerms {
		t.Fatalf("落盘成功应正常翻页: %v / %q", f.page, f.status)
	}
	if len(mgr.langs) != 1 || mgr.langs[0] != "en" {
		t.Fatalf("离开第 1 页应写一次 ui.lang: %v", mgr.langs)
	}
	f.onPrev()
	f.onNext()
	if len(mgr.langs) != 1 {
		t.Fatalf("同一个值不该重复写: %v", mgr.langs)
	}

	// 装配后（in-app 次窗）：只记录，不热切——主窗文案已散进各视图与测量（D275）。
	i18n.Use(i18n.SelectionZh)
	mgr2 := baseFake()
	env2, _ := welcomeEnvSnap(mgr2, false, nil)
	f2 := newWelcomeForm(env2)
	f2.pickLang(i18n.SelectionEn)
	if i18n.Current() != i18n.LangZh {
		t.Fatalf("装配后不该热切界面语言: %q", i18n.Current())
	}
	f2.onSkip() // 显式关闭同样保留语言选择
	if len(mgr2.langs) != 1 || mgr2.langs[0] != "en" {
		t.Fatalf("关闭时也应落盘语言: %v", mgr2.langs)
	}

	// 写盘失败：留在第 1 页、报因、不静默往下走。
	mgr3 := baseFake()
	mgr3.langErr = errors.New("read-only file system")
	env3, _ := welcomeEnvSnap(mgr3, true, nil)
	f3 := newWelcomeForm(env3)
	f3.pickLang(i18n.SelectionEn)
	f3.onNext()
	if f3.page != welcomeIntro || !f3.err || !strings.Contains(f3.status, "read-only file system") {
		t.Fatalf("写盘失败应拦在第 1 页并报因: page=%v err=%v status=%q", f3.page, f3.err, f3.status)
	}
}

// TestWelcomeLangFromSnapshot 初值来自快照（不是恒 auto）：已存的 zh/en 进来就是选中态。
func TestWelcomeLangFromSnapshot(t *testing.T) {
	mgr := baseFake()
	mgr.snap.Lang = "en"
	env, _ := welcomeEnvSnap(mgr, true, nil)
	if f := newWelcomeForm(env); f.lang != i18n.SelectionEn {
		t.Fatalf("初值 = %q, want en", f.lang)
	}
	// 非法值（旧配置手写）不该拦住向导：回落 auto。
	mgr.snap.Lang = "fr"
	env, _ = welcomeEnvSnap(mgr, true, nil)
	if f := newWelcomeForm(env); f.lang != i18n.SelectionAuto {
		t.Fatalf("非法 ui.lang 应回落 auto: %q", f.lang)
	}
}

// TestWelcomeConsentOnly 条款改版只重过第 2 页（D278 决策 4）：页序从条款页起、
// 勾选后才可继续、落盘走 SetConsent（**不重写 provider**），完成后交回 Done。
func TestWelcomeConsentOnly(t *testing.T) {
	mgr := baseFake()
	env, outs := welcomeEnvSnap(mgr, true, nil)
	env.consentOnly = true
	f := newWelcomeForm(env)
	if f.page != welcomeTerms {
		t.Fatalf("只补同意时应直接落在条款页: %v", f.page)
	}
	if f.canAdvance() {
		t.Fatal("未勾选时「同意并继续」不该可点")
	}
	f.onNext() // 未勾选：拦在本页
	if f.page != welcomeTerms || !f.err {
		t.Fatalf("未勾选应拦在条款页: page=%v err=%v", f.page, f.err)
	}
	f.consent.Value = true
	f.onNext()
	if len(mgr.consents) != 1 || mgr.consents[0] != legal.Version {
		t.Fatalf("应写一次同意版本 %d: %v", legal.Version, mgr.consents)
	}
	if len(mgr.applied) != 0 {
		t.Fatalf("只补同意时不该重写 provider: %+v", mgr.applied)
	}
	if len(*outs) != 1 || (*outs)[0] != SetupDone {
		t.Fatalf("同意后应完成向导: %v", *outs)
	}

	// 关闭 = 取消（没有「以后再说」这条路：不同意就不能继续用）。
	mgr2 := baseFake()
	env2, outs2 := welcomeEnvSnap(mgr2, true, nil)
	env2.consentOnly = true
	f2 := newWelcomeForm(env2)
	f2.onSkip()
	if len(*outs2) != 1 || (*outs2)[0] != SetupCancelled {
		t.Fatalf("只补同意时关闭应取消: %v", *outs2)
	}
	if mgr2.marked != 0 {
		t.Fatal("只补同意时不该置 welcomed")
	}
}

// TestWelcomeTermsRows 第 2 页正文换成真文本（D278 决策 5）：两份文档都进了滚动区，
// 且有节标题与正文块——不再是「一段说明 + 一个勾选框」。
func TestWelcomeTermsRows(t *testing.T) {
	f := newWelcomeForm(mustEnv(t, true, nil))
	f.loadTerms()
	if f.termsErr != nil {
		t.Fatalf("载入正文失败: %v", f.termsErr)
	}
	var titles, sections, paras int
	for _, r := range f.terms {
		switch r.kind {
		case termsDocTitle:
			titles++
		case termsSection:
			sections++
		case termsPara:
			paras++
		}
	}
	if titles != 2 {
		t.Fatalf("应含两份文档标题，实际 %d", titles)
	}
	if sections < 20 || paras < 20 {
		t.Fatalf("正文块太少（节 %d / 段 %d）——正文可能没载进来", sections, paras)
	}
}

// TestWelcomeFormFill D278：快照填表（三类字段）；**密钥字段恒空**（只写不回显，D35）；
// 起始页 = 第 1 页（欢迎与语言），不再是接入页。
func TestWelcomeFormFill(t *testing.T) {
	f := newWelcomeForm(mustEnv(t, true, []string{"provider \"openai\" 缺密钥：环境变量 AQ_K 未设置"}))
	if f.name.Text() != "openai" || f.baseURL.Text() != "https://api.example/v1" || f.models.Text() != "m1, m2" {
		t.Fatalf("填表错误: %q %q %q", f.name.Text(), f.baseURL.Text(), f.models.Text())
	}
	if f.apiKey.Text() != "" {
		t.Fatalf("密钥字段恒空（只写不回显，D35）: %q", f.apiKey.Text())
	}
	if f.page != welcomeIntro {
		t.Fatalf("起始页 = %v, want 欢迎页", f.page)
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

// TestWelcomeFormPageFlow 五页推进（D278）：顺序 = 欢迎 → 条款 → 接入 → 教程 → 完成；
// 闸门两处（条款页未勾选不前移、接入页缺密钥不前移）；上一步可回退且清反馈；第 1 页无上一步。
func TestWelcomeFormPageFlow(t *testing.T) {
	f := newWelcomeForm(mustEnv(t, true, []string{"provider \"openai\" 缺密钥：环境变量 AQ_K 未设置"}))
	f.onPrev() // 第 1 页：无上一步，原地不动
	if f.page != welcomeIntro {
		t.Fatalf("第 1 页的上一步应无效：%v", f.page)
	}
	f.onNext()
	if f.page != welcomeTerms {
		t.Fatalf("欢迎页应推进到条款页：%v", f.page)
	}
	f.onNext() // 未勾选：拦在条款页
	if f.page != welcomeTerms || !f.err {
		t.Fatalf("未勾选应留在条款页并置错：page=%v err=%v", f.page, f.err)
	}
	f.consent.Value = true
	f.onNext()
	if f.page != welcomeAccess || f.err || f.status != "" {
		t.Fatalf("勾选后应到接入页且清反馈：page=%v err=%v status=%q", f.page, f.err, f.status)
	}
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
	f.onPrev()
	f.onPrev()
	if f.page != welcomeTerms {
		t.Fatalf("应能一路退回条款页：%v", f.page)
	}
	f.consent.Value = false
	f.onSave() // 退回改过勾选：保存要重新过闸门
	if f.page != welcomeTerms || !f.err {
		t.Fatalf("未勾选时保存应退回条款页并报错：page=%v err=%v", f.page, f.err)
	}
}

// TestWelcomeFormSave 保存：patch 组装（密钥空 = 保持原值语义由数据面处置）→ Apply →
// 结束宿主（SetupDone）。
func TestWelcomeFormSave(t *testing.T) {
	mgr := baseFake()
	env, outs := welcomeEnvSnap(mgr, true, nil)
	f := newWelcomeForm(env)
	f.consent.Value = true
	f.apiKey.SetText("secret:AQ_K")
	f.onSave()
	if len(mgr.applied) != 1 {
		t.Fatalf("Apply 调用 %d 次, want 1", len(mgr.applied))
	}
	got := mgr.applied[0]
	// ConsentVersion 由向导填（勾选 + 保存 = 同意当前这一版正文，D278 决策 4）。
	want := WelcomePatch{Provider: "openai", BaseURL: "https://api.example/v1", APIKey: "secret:AQ_K", Models: "m1, m2", ConsentVersion: legal.Version}
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
	f.consent.Value = true
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

// TestWelcomeFormSkip 跳过 / 关闭：装配前 = 交回 Done 且**不写盘**（D278 决策 3：第 3 页的
// 「稍后配置」直接进主窗，可启动空态；welcomed 由装配根收口置位，表单不碰数据面）；
// in-app = 置 welcomed（显式关闭即已看）后关窗。
func TestWelcomeFormSkip(t *testing.T) {
	mgr := baseFake()
	env, outs := welcomeEnvSnap(mgr, true, nil)
	f := newWelcomeForm(env)
	f.onSkip()
	if len(*outs) != 1 || (*outs)[0] != SetupDone {
		t.Fatalf("装配前跳过 = 完成（未写 provider）: %v", *outs)
	}
	if mgr.marked != 0 {
		t.Fatalf("装配前跳过不该由表单置 welcomed（装配根收口）: %d", mgr.marked)
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
// 装配前宿主上的跳过 = Done（D278 决策 3：跳过就是「没写 provider 的完成」）。
func TestWelcomeFormFinishOnce(t *testing.T) {
	env, outs := welcomeEnvSnap(baseFake(), true, nil)
	f := newWelcomeForm(env)
	f.onSkip()
	f.onSkip()
	f.finish(SetupDone)
	if len(*outs) != 1 || (*outs)[0] != SetupDone {
		t.Fatalf("结局应只交一次且为首次者: %v", *outs)
	}
}

// TestWelcomeFormTestConnectivity 连通性测试：立即置「正在测试…」→ 异步结果经通道回灌帧
// 循环（drain）→ 内联「● 连通正常 · N ms」/ 失败原因（D153：结果落在测试按钮这一行）。
func TestWelcomeFormTestConnectivity(t *testing.T) {
	mgr := baseFake()
	env, _ := welcomeEnvSnap(mgr, false, nil)
	f := newWelcomeForm(env)
	f.onTest()
	if !f.probeBusy {
		t.Fatalf("应即时报「正在测试…」（probeBusy=%v）", f.probeBusy)
	}
	waitProbe(t, f, "连通正常")
	if f.probeErr || len(mgr.tested) != 1 {
		t.Fatalf("结果 = (%v, %q) 调用 %d 次", f.probeErr, f.probe, len(mgr.tested))
	}
	if !strings.Contains(f.probe, " ms") {
		t.Fatalf("成功应带时延：%q", f.probe)
	}
	if f.status != "" {
		t.Fatalf("测试结果不应写进页脚状态行：%q", f.status)
	}

	mgr2 := baseFake()
	mgr2.testErr = errors.New("连接超时")
	env2, _ := welcomeEnvSnap(mgr2, false, nil)
	f2 := newWelcomeForm(env2)
	f2.onTest()
	waitProbe(t, f2, "连通失败")
	if !f2.probeErr || !strings.Contains(f2.probe, i18n.Tf("ui.welcome.probe_failed", "err", "连接超时")) {
		t.Fatalf("失败反馈 = (%v, %q)", f2.probeErr, f2.probe)
	}
	if strings.Contains(f2.probe, " ms") {
		t.Fatalf("失败不应报时延：%q", f2.probe)
	}
}

// waitProbe 轮询排空异步结果直到内联结果含 want（≤1s）。
func waitProbe(t *testing.T, f *welcomeForm, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.drain()
		if strings.Contains(f.probe, want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("内联结果未在时限内变为含 %q: %q", want, f.probe)
}

// waitHint 轮询排空异步结果直到候选说明含 want（≤1s）。
func waitHint(t *testing.T, f *welcomeForm, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.drain()
		if strings.Contains(f.modelsHint, want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("候选说明未在时限内变为含 %q: %q", want, f.modelsHint)
}

// TestWelcomeFetchModels 「获取模型」（D153）：拉列表 → 自动填入前 8 个 + 候选 chip；
// 点 chip = **替换**成单选；用户手写过模型名就不覆盖（一次网络结果不能冲掉手写内容）。
func TestWelcomeFetchModels(t *testing.T) {
	mgr := baseFake()
	mgr.models = []string{"m1", "m2", "m3"}
	env, _ := welcomeEnvSnap(mgr, true, nil)
	f := newWelcomeForm(env) // 模型名 = 快照原值（基线）→ 允许自动填入
	f.onFetchModels()
	if !strings.Contains(f.modelsHint, i18n.T("ui.welcome.models_fetching")) {
		t.Fatalf("应即时报「正在获取…」: %q", f.modelsHint)
	}
	waitHint(t, f, "已自动填入")
	if got := f.models.Text(); got != "m1, m2, m3" {
		t.Fatalf("自动填入 = %q", got)
	}
	if len(f.cands) != 3 || len(f.candClicks) != 3 {
		t.Fatalf("候选 = %d/%d", len(f.cands), len(f.candClicks))
	}
	if len(mgr.tested) != 1 || mgr.tested[0].Provider != "openai" {
		t.Fatalf("Models 调用 = %+v", mgr.tested)
	}

	// chip 点击在帧首事件循环里消费（不是表单方法），故跑一帧真渲染。
	th := testTheme()
	router := new(input.Router)
	clickFrame := func() {
		var ops op.Ops
		welcomeFrame(layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(660, 640)),
			Source:      router.Source(),
		}, th, f)
	}
	f.candClicks[1].Click()
	clickFrame()
	if got := f.models.Text(); got != "m2" {
		t.Fatalf("点 chip 后模型名 = %q, want m2（替换而非追加）", got)
	}

	// 手写过：成功也只挂候选，不覆盖。
	f.models.SetText("hand")
	mgr.models = []string{"x1", "x2"}
	f.onFetchModels()
	waitHint(t, f, "未覆盖")
	if got := f.models.Text(); got != "hand" {
		t.Fatalf("手写内容被覆盖：%q", got)
	}
	if len(f.cands) != 2 {
		t.Fatalf("仍应挂出候选：%v", f.cands)
	}

	// 失败：不动模型名，说明行报因。
	mgr.testErr = errors.New("403 forbidden")
	f.onFetchModels()
	waitHint(t, f, "失败")
	if got := f.models.Text(); got != "hand" {
		t.Fatalf("失败后模型名 = %q", got)
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
	f.consent.Value = true
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
	if !f3.err || !strings.Contains(f3.status, i18n.T("ui.welcome.err.not_wired")) {
		t.Fatalf("无数据面应报未接线: (%v, %q)", f3.err, f3.status)
	}
}

// TestWelcomeFrameRendersPages 三页离屏单帧渲染（真窗尺寸 560×520dp @1.25）：各页都有
// 实质内容（非空窗）；教程页**下半幅**也必须有内容——锁住滚动列表的垂直轴向
// （S4c 教训：轴向错配 = 只画首行、下半幅纯底色）。
func TestWelcomeFrameRendersPages(t *testing.T) {
	const (
		viewW, viewH = 825, 800 // winWelcome 几何 660×640dp @1.25
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

	for _, page := range []welcomePage{welcomeIntro, welcomeTerms, welcomeAccess, welcomeTutorial, welcomeDone} {
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

// TestTrayMenuItemsNoWelcome D141/D185：托盘项集**不含**欢迎窗入口（入口在设置窗）、
// 含无人值守勾选项——项序与文案为契约（与 logo 菜单的 TestLogoMenuItems 对偶）。
func TestTrayMenuItemsNoWelcome(t *testing.T) {
	items := trayMenuItems(true, false)
	want := []struct {
		id    uintptr
		label string
	}{
		{cmdToggle, i18n.T("ui.menu.tray_toggle")},
		{cmdTopMost, i18n.T("ui.menu.tray_topmost")},
		{cmdUnattended, i18n.T("ui.menu.unattended")},
		{0, ""},
		{cmdSettings, i18n.T("ui.menu.settings")},
		{cmdHistory, i18n.T("ui.menu.tray_history")},
		{0, ""},
		{cmdExit, i18n.T("ui.menu.quit")},
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
	if items[2].checked {
		t.Fatal("无人值守关闭时托盘项不应勾选")
	}
	if !trayMenuItems(true, true)[2].checked {
		t.Fatal("无人值守开启时托盘项应勾选")
	}
}

// TestSettingsRowPlanHasWelcome D141：欢迎引导行紧随 Profile 区（设置窗 = 唯一菜单外入口）；
// S4d/D142 起 Profile 区与欢迎行同在「常规」页。
func TestSettingsRowPlanHasWelcome(t *testing.T) {
	u := newHeadless(t, Options{Settings: func() SettingsSnapshot { return fillSnapshot() }})
	f := newSettingsForm(u)
	rows := f.pageRows(pageGeneral)
	idx := -1
	for i, r := range rows {
		if r.kind == rowWelcome {
			idx = i
			break
		}
	}
	if idx <= 0 {
		t.Fatalf("「常规」页列缺 rowWelcome（idx=%d）", idx)
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
		"球右键：":  logoMenuItems("strict", true, false),
		"托盘右键：": trayMenuItems(true, false),
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

// TestWelcomeGeometry D153：欢迎窗几何（标题 + 副标题 + 圆徽步骤条 + 四行卡 + 页脚）。
func TestWelcomeGeometry(t *testing.T) {
	w, h, minW, minH := winWelcome.geometry()
	if w != 660 || h != 640 || minW != 560 || minH != 520 {
		t.Fatalf("几何 = (%d,%d,%d,%d)", w, h, minW, minH)
	}
	if winWelcome.title() != i18n.T("ui.win.title.welcome") {
		t.Fatalf("标题 = %q", winWelcome.title())
	}
}

// TestWelcomePageOrder D278：五页固定顺序（欢迎 → 条款 → 接入 → 教程 → 完成），每页都有
// 非空页名；步进条的 n/N 与页序同源，页号变化整条跟着变。
func TestWelcomePageOrder(t *testing.T) {
	order := []welcomePage{welcomeIntro, welcomeTerms, welcomeAccess, welcomeTutorial, welcomeDone}
	if welcomePageCount != len(order) {
		t.Fatalf("页数 = %d, want %d", welcomePageCount, len(order))
	}
	for i, p := range order {
		if int(p) != i {
			t.Fatalf("第 %d 页的枚举值 = %d（页序必须与常量顺序一致）", i+1, int(p))
		}
		if name := welcomePageName(p); strings.TrimSpace(name) == "" {
			t.Fatalf("第 %d 页缺页名", i+1)
		}
	}
	// 页名互不相同：步进条只显示当前页名，重名就等于没说这一页是什么。
	seen := map[string]welcomePage{}
	for _, p := range order {
		n := welcomePageName(p)
		if prev, ok := seen[n]; ok {
			t.Fatalf("第 %v 页与第 %v 页同名 %q", prev, p, n)
		}
		seen[n] = p
	}
}

// TestWelcomeCanAdvance D278：页脚「继续」的禁用态与翻页闸门同源——第 2 页要勾选、
// 第 3 页要接入参数齐全，其余页恒可点。按钮亮着却在点了以后报错是两套判据的典型症状。
func TestWelcomeCanAdvance(t *testing.T) {
	f := newWelcomeForm(mustEnv(t, false, nil)) // in-app：密钥留空合法
	f.page = welcomeIntro
	if !f.canAdvance() {
		t.Fatal("第 1 页不该有闸门")
	}
	f.page = welcomeTerms
	if f.canAdvance() {
		t.Fatal("第 2 页未勾选时不该可点")
	}
	f.consent.Value = true
	if !f.canAdvance() {
		t.Fatal("第 2 页勾选后应可点")
	}
	f.page = welcomeAccess
	f.name.SetText("  ")
	if f.canAdvance() {
		t.Fatal("第 3 页 provider 为空时不该可点")
	}
	f.name.SetText("openai")
	if !f.canAdvance() {
		t.Fatalf("第 3 页填齐后应可点：%v", f.pageErr())
	}
	for _, p := range []welcomePage{welcomeTutorial, welcomeDone} {
		f.page = p
		if !f.canAdvance() {
			t.Fatalf("第 %v 页不该有闸门", p)
		}
	}
}
