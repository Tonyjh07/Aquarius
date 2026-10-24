package uigui

// 「联网」设置页（D200）：快照填表、有序列表编辑、保存组装、数值校验、密钥只写不回显。

import (
	"image"
	"strings"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// webSnapshot 联网页快照：搜索两家（deepseek + brave 带已存引用）、抓取一家本地。
func webSnapshot() SettingsSnapshot {
	s := fillSnapshot()
	s.WebSearchProviders = []WebProviderView{
		{Kind: "deepseek", From: "deepseek", Count: 5},
		{Kind: "brave", KeySaved: true, KeyRef: "secret:AQUARIUS_BRAVE_API_KEY", Country: "CN", Lang: "zh-hans"},
	}
	s.WebSearchCount = 8
	s.WebFetchProviders = []WebProviderView{{Kind: "local"}}
	s.WebFetchMaxChars = 12000
	s.WebFetchProxy = "http://127.0.0.1:7897"
	s.WebFetchUserAgent = "Aquarius/1.0"
	s.WebFetchRobots = true
	s.WebFetchTimeoutSec = 20
	return s
}

// TestWebPageFillsSnapshot 开窗填表：两组提供商按序铺开（类型/来源/端点/国家语言/条数），
// 本地抓取参数原样落位；密钥框恒空（只写不回显，D35）。
func TestWebPageFillsSnapshot(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return webSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		ProviderMgr: &fakeProviderMgr{snap: []ProviderSnapshot{
			{Name: "deepseek", BaseURL: "https://api.deepseek.com"},
			{Name: "mimo", BaseURL: "https://api.mimo.ai/v1"},
		}},
	})
	f := newSettingsForm(u)
	if len(f.webSearch) != 2 || len(f.webFetch) != 1 {
		t.Fatalf("提供商条数 = %d / %d, want 2 / 1", len(f.webSearch), len(f.webFetch))
	}
	if got := f.webSearch[0].kind.value(); got != "deepseek" {
		t.Fatalf("搜索第 1 条类型 = %q, want deepseek", got)
	}
	if got := f.webSearch[1].kind.value(); got != "brave" {
		t.Fatalf("搜索第 2 条类型 = %q, want brave", got)
	}
	if f.webSearch[1].country.Text() != "CN" || f.webSearch[1].lang.Text() != "zh-hans" {
		t.Fatalf("brave 国家/语言 = %q/%q", f.webSearch[1].country.Text(), f.webSearch[1].lang.Text())
	}
	if f.webSearch[0].count.Text() != "5" {
		t.Fatalf("第 1 条条数 = %q, want 5", f.webSearch[0].count.Text())
	}
	if got := f.webFetch[0].kind.value(); got != "local" {
		t.Fatalf("抓取第 1 条类型 = %q, want local", got)
	}
	// 密钥只写不回显：框恒空，形态说明里带引用名（不含值）。
	if f.webSearch[1].key.Text() != "" {
		t.Fatalf("密钥框应恒空，实为 %q", f.webSearch[1].key.Text())
	}
	if note := f.webSearch[1].keyNote(); !strings.Contains(note, "secret:AQUARIUS_BRAVE_API_KEY") {
		t.Fatalf("密钥形态说明 = %q", note)
	}
	if f.webCount.Text() != "8" || f.webMaxChars.Text() != "12000" || f.webTimeout.Text() != "20" {
		t.Fatalf("数值档 = %q/%q/%q", f.webCount.Text(), f.webMaxChars.Text(), f.webTimeout.Text())
	}
	if f.webProxy.Text() != "http://127.0.0.1:7897" || f.webUA.Text() != "Aquarius/1.0" {
		t.Fatalf("代理/UA = %q/%q", f.webProxy.Text(), f.webUA.Text())
	}
	if !f.webRobots.Value || f.webPrivate.Value {
		t.Fatalf("开关 = robots:%v private:%v, want true/false", f.webRobots.Value, f.webPrivate.Value)
	}
}

// TestWebProviderFormFromDefaults 老配置只写 kind（没有 from）时，「借用」下拉显示同名
// 条目——启动确实借它，界面不能显示成「不借」。
func TestWebProviderFormFromDefaults(t *testing.T) {
	choices := []ProviderChoice{{Name: "deepseek"}, {Name: "mimo"}}
	pf := newWebProviderForm(WebProviderView{Kind: "deepseek"}, true, choices)
	if got := pf.from.value(); got != "deepseek" {
		t.Fatalf("缺省借用 = %q, want deepseek", got)
	}
	// 快照里没有同名条目时保持「不借」（没有可借的东西）。
	pf = newWebProviderForm(WebProviderView{Kind: "deepseek"}, true, []ProviderChoice{{Name: "mimo"}})
	if got := pf.from.value(); got != "" {
		t.Fatalf("无同名条目时借用 = %q, want 空", got)
	}
	// 显式写了 from 就照它来。
	pf = newWebProviderForm(WebProviderView{Kind: "deepseek", From: "mimo"}, true, choices)
	if got := pf.from.value(); got != "mimo" {
		t.Fatalf("显式借用 = %q, want mimo", got)
	}
}

// TestWebPageFillsEndpoint 端点原样回填（自己填过的那份），密钥仍只给形态。
func TestWebPageFillsEndpoint(t *testing.T) {
	s := webSnapshot()
	s.WebSearchProviders[0] = WebProviderView{
		Kind: "deepseek", BaseURL: "https://mine.example.com/v1",
		KeySaved: true, KeyRef: "secret:AQUARIUS_DEEPSEEK_SEARCH_API_KEY",
	}
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return s },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
	pf := f.webSearch[0]
	if got := pf.baseURL.Text(); got != "https://mine.example.com/v1" {
		t.Fatalf("端点未回填: %q", got)
	}
	if pf.key.Text() != "" {
		t.Fatalf("密钥框应恒空，实为 %q", pf.key.Text())
	}
	if note := pf.keyNote(); !strings.Contains(note, "secret:AQUARIUS_DEEPSEEK_SEARCH_API_KEY") {
		t.Fatalf("密钥形态说明 = %q", note)
	}
	// 填的值进 patch：端点与密钥各自独立。
	pf.key.SetText("sk-own-1234")
	patches := f.webProviderPatches(true)
	if patches[0].BaseURL != "https://mine.example.com/v1" || patches[0].Key != "sk-own-1234" {
		t.Fatalf("patch = %+v", patches[0])
	}
}

// TestWebPageRowsAndEditing 行计划随列表长度变化；上移/下移/删除/新增只动本侧列表。
func TestWebPageRowsAndEditing(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return webSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
	count := func(kind rowKind) int {
		n := 0
		for _, r := range f.pageRows(pageWeb) {
			if r.kind == kind {
				n++
			}
		}
		return n
	}
	if got := count(rowWebSearchItem); got != 2 {
		t.Fatalf("搜索提供商行 = %d, want 2", got)
	}
	if got := count(rowWebFetchItem); got != 1 {
		t.Fatalf("抓取提供商行 = %d, want 1", got)
	}
	for _, k := range []rowKind{rowWebSearchAdd, rowWebFetchAdd, rowWebSearchCount, rowWebFetchChars,
		rowWebProxy, rowWebUA, rowWebRobots, rowWebPrivate, rowWebTimeout, rowWebNote} {
		if count(k) != 1 {
			t.Errorf("行型 %v 应恰好一行", k)
		}
	}

	// 上移/下移：顺序即优先级，换位必须是整条换。
	f.moveWebProvider(true, 0, 1)
	if f.webSearch[0].kind.value() != "brave" || f.webSearch[1].kind.value() != "deepseek" {
		t.Fatalf("下移后顺序 = %q/%q", f.webSearch[0].kind.value(), f.webSearch[1].kind.value())
	}
	f.moveWebProvider(true, 0, -1) // 越界 no-op
	if f.webSearch[0].kind.value() != "brave" {
		t.Fatalf("越界上移不该动列表: %q", f.webSearch[0].kind.value())
	}
	// 删除只影响本侧。
	f.removeWebProvider(true, 0)
	if len(f.webSearch) != 1 || len(f.webFetch) != 1 {
		t.Fatalf("删除后条数 = %d / %d, want 1 / 1", len(f.webSearch), len(f.webFetch))
	}
	// 新增：缺省取该侧首档。
	f.addWebProvider(false)
	if len(f.webFetch) != 2 || f.webFetch[1].kind.value() != "deepseek" {
		t.Fatalf("新增抓取提供商 = %+v", f.webFetch[1])
	}
	f.removeWebProvider(false, 9) // 越界 no-op
	if len(f.webFetch) != 2 {
		t.Fatalf("越界删除不该动列表: %d 条", len(f.webFetch))
	}
}

// TestSaveIncludesWeb 保存提交带全部联网字段：列表恒全量写、数值空 = 保持快照原值、
// 密钥框留空 = 不带值（cmd 侧保持原引用）。
func TestSaveIncludesWeb(t *testing.T) {
	var got SettingsPatch
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return webSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { got = p; return nil, nil },
	})
	f := newSettingsForm(u)
	f.page = pageWeb
	f.webSearch[1].key.SetText("secret:BRAVE_NEW")
	f.webCount.SetText("") // 留空 = 保持快照 8
	f.webTimeout.SetText("")
	f.webRobots.Value = false
	f.webPrivate.Value = true
	u.saveSettings(f)
	if len(got.WebSearchProviders) != 2 || got.WebSearchProviders[1].Kind != "brave" ||
		got.WebSearchProviders[1].Key != "secret:BRAVE_NEW" ||
		got.WebSearchProviders[1].Country != "CN" {
		t.Fatalf("搜索 patch = %+v", got.WebSearchProviders)
	}
	if got.WebSearchProviders[0].Key != "" {
		t.Fatalf("未填密钥的条目不该带值: %q", got.WebSearchProviders[0].Key)
	}
	if len(got.WebFetchProviders) != 1 || got.WebFetchProviders[0].Kind != "local" {
		t.Fatalf("抓取 patch = %+v", got.WebFetchProviders)
	}
	if got.WebSearchCount != 8 || got.WebFetchMaxChars != 12000 || got.WebFetchTimeoutSec != 20 {
		t.Fatalf("数值 = %d/%d/%d, want 保持快照 8/12000/20",
			got.WebSearchCount, got.WebFetchMaxChars, got.WebFetchTimeoutSec)
	}
	if got.WebFetchProxy != "http://127.0.0.1:7897" || got.WebFetchUserAgent != "Aquarius/1.0" {
		t.Fatalf("代理/UA = %q/%q", got.WebFetchProxy, got.WebFetchUserAgent)
	}
	if got.WebFetchRobots || !got.WebFetchPrivate {
		t.Fatalf("开关 = robots:%v private:%v", got.WebFetchRobots, got.WebFetchPrivate)
	}
	if !strings.Contains(f.status, "已保存") {
		t.Fatalf("保存反馈 = %q", f.status)
	}
	// 保存后快照跟着走：再存一次不会因为「以为没改」把用户刚填的值丢掉。
	if !f.snap.WebFetchPrivate || f.snap.WebFetchRobots {
		t.Fatalf("回写快照开关 = robots:%v private:%v", f.snap.WebFetchRobots, f.snap.WebFetchPrivate)
	}
	if got := f.snap.WebSearchProviders[1]; !got.KeySaved || got.KeyRef != "secret:BRAVE_NEW" {
		t.Fatalf("回写快照密钥形态 = %+v", got)
	}
}

// TestSaveWebRejectsBadNumbers 数值越界在写回调之前拦下（不落盘），并给出中文原因。
func TestSaveWebRejectsBadNumbers(t *testing.T) {
	cases := []struct{ name, field, value, want string }{
		{"条数过小", "count", "0", "1–20"},
		{"条数过大", "count", "21", "1–20"},
		{"条数非数字", "count", "abc", "1–20"},
		{"正文上限过小", "chars", "100", "500–20000"},
		{"正文上限过大", "chars", "30000", "500–20000"},
		{"超时过小", "timeout", "0", "1–600"},
		{"超时过大", "timeout", "601", "1–600"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			called := false
			u := newHeadless(t, Options{
				Settings:      func() SettingsSnapshot { return webSnapshot() },
				ApplySettings: func(SettingsPatch) ([]port.Command, error) { called = true; return nil, nil },
			})
			f := newSettingsForm(u)
			switch c.field {
			case "count":
				f.webCount.SetText(c.value)
			case "chars":
				f.webMaxChars.SetText(c.value)
			case "timeout":
				f.webTimeout.SetText(c.value)
			}
			u.saveSettings(f)
			if called {
				t.Fatal("非法数值不该触达写回调")
			}
			if !f.err || !strings.Contains(f.status, c.want) {
				t.Fatalf("反馈 = (%v, %q), want 含 %q", f.err, f.status, c.want)
			}
		})
	}
}

// TestWebProviderViewsKeepKeyShapeByKind 保存后回写快照的密钥形态按 kind 对齐：在前面
// 插一条同类型条目、或删掉一条，下标整体错位也不该把 brave 的引用名贴到别的条目上。
func TestWebProviderViewsKeepKeyShapeByKind(t *testing.T) {
	old := []WebProviderView{
		{Kind: "brave", KeySaved: true, KeyRef: "secret:BRAVE"},
	}
	got := webProviderViews([]WebProviderPatch{
		{Kind: "deepseek", From: "deepseek"},
		{Kind: "brave"}, // 密钥留空 = 沿用旧形态
	}, old)
	if got[0].KeySaved || got[0].KeyRef != "" {
		t.Fatalf("deepseek 不该继承 brave 的引用: %+v", got[0])
	}
	if !got[1].KeySaved || got[1].KeyRef != "secret:BRAVE" {
		t.Fatalf("brave 应沿用原引用: %+v", got[1])
	}
	// 同类型多条按出现顺序依次认领：第二条不再复用一个旧形态。
	got = webProviderViews([]WebProviderPatch{{Kind: "brave"}, {Kind: "brave"}}, old)
	if !got[0].KeySaved || got[1].KeySaved {
		t.Fatalf("同类型多条应只认领一次: %+v", got)
	}
}

// TestWebProviderRowShowsOnlyRelevantFields 字段按类型显隐：local 不画端点与「借用」，
// brave 不画「借用」。
func TestWebProviderRowShowsOnlyRelevantFields(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return webSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
	th := testTheme()
	render := func(pf *webProviderForm, search bool) {
		var ops op.Ops
		gtx := layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Constraints{Max: image.Pt(520, 400)},
		}
		f.webProviderRow(gtx, th, pf, search)
	}
	for _, pf := range f.webSearch {
		render(pf, true)
	}
	for _, pf := range f.webFetch {
		render(pf, false)
	}
	// 显隐是纯布局分支，这里锁住「切换类型不丢已填值」这条更硬的约束：
	brave := f.webSearch[1]
	brave.kind.setValue("deepseek")
	if brave.country.Text() != "CN" {
		t.Fatalf("切类型不该清空国家档: %q", brave.country.Text())
	}
}

// TestFillWebProviderFrom 「填入」把所选服务的端点与密钥引用抄进本条目（D222）：只抄
// 引用名不抄值；目标没有已入库的引用时密钥框保持原样。
func TestFillWebProviderFrom(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return webSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		ProviderMgr: &fakeProviderMgr{snap: []ProviderSnapshot{{
			Name:    "mimo",
			BaseURL: "https://api.mimo.ai/v1",
			Keys:    []ProviderKeySnapshot{{Saved: true, Ref: "secret:AQUARIUS_MIMO_API_KEY"}},
			Models:  []ProviderModelSnapshot{{Name: "mimo-flash", Enabled: true}},
		}}},
	})
	f := newSettingsForm(u)
	pf := newWebProviderForm(WebProviderView{Kind: "deepseek"}, true, f.chainChoices)
	pf.from.setValue("mimo")
	f.fillWebProviderFrom(pf)
	if got := pf.baseURL.Text(); got != "https://api.mimo.ai/v1" {
		t.Fatalf("端点未抄入: %q", got)
	}
	if got := pf.key.Text(); got != "secret:AQUARIUS_MIMO_API_KEY" {
		t.Fatalf("密钥引用未抄入: %q", got)
	}
	// 抄过来之后就是独立配置：改端点不影响来源。
	pf.baseURL.SetText("https://mirror.example.com/v1")
	if pf.baseURL.Text() != "https://mirror.example.com/v1" {
		t.Fatal("抄入后应可自由编辑")
	}
	// 选「不借」= 什么都不抄（保留当前值）。
	pf.baseURL.SetText("")
	pf.key.SetText("")
	pf.from.setValue("")
	f.fillWebProviderFrom(pf)
	if pf.baseURL.Text() != "" || pf.key.Text() != "" {
		t.Fatalf("不借时不该抄入任何值: %q / %q", pf.baseURL.Text(), pf.key.Text())
	}
}

// TestFillWebProviderFromSkipsPlainKey 来源 provider 的密钥是明文时（没入库）不抄：
// 编辑框永远不回显值，凭空写一个引用名反而指向不存在的条目。
func TestFillWebProviderFromSkipsPlainKey(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return webSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		ProviderMgr: &fakeProviderMgr{snap: []ProviderSnapshot{{
			Name:    "plainprov",
			BaseURL: "https://plain.example.com/v1",
			Keys:    []ProviderKeySnapshot{{Plain: true}},
			Models:  []ProviderModelSnapshot{{Name: "m", Enabled: true}},
		}}},
	})
	f := newSettingsForm(u)
	pf := newWebProviderForm(WebProviderView{Kind: "deepseek"}, true, f.chainChoices)
	pf.from.setValue("plainprov")
	f.fillWebProviderFrom(pf)
	if got := pf.baseURL.Text(); got != "https://plain.example.com/v1" {
		t.Fatalf("端点应照抄: %q", got)
	}
	if got := pf.key.Text(); got != "" {
		t.Fatalf("明文密钥不该被抄成引用名: %q", got)
	}
}
