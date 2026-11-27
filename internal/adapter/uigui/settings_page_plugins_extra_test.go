package uigui

// settings_page_plugins_extra_test.go 插件页「清单 + 独立页」的部分（D347 第二批）。
// 参数表的渲染/Save 语义在 settings_page_plugins_test.go；这里只管开关与放置位置。

import (
	"sync"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakePluginAdmin 插件清单替身：记录收到的启停请求。
//
// 启停各自在自己的 goroutine 里跑（界面不串行化它们），记录的锁不能省。
type fakePluginAdmin struct {
	mu     sync.Mutex
	list   []PluginState
	toggle []string // "name:on" / "name:off"
}

// requests 已收到的启停请求快照。
func (a *fakePluginAdmin) requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.toggle...)
}

func (a *fakePluginAdmin) Snapshot() []PluginState { return a.list }

func (a *fakePluginAdmin) SetEnabled(name string, on bool) error {
	arg := name + ":off"
	if on {
		arg = name + ":on"
	}
	a.mu.Lock()
	a.toggle = append(a.toggle, arg)
	a.mu.Unlock()
	return nil
}

// pluginsWithList 造一个带清单的表单：memory 有内联分区，recall 的分区声明为独立页。
func pluginsWithList(t *testing.T) (*settingsForm, *fakePluginAdmin, *fakePluginSettings) {
	t.Helper()
	ps := pluginSettingsFixture()
	ps.secs = append(ps.secs, port.PluginSettingsSection{
		Plugin: "recall", ID: "page", Title: "长参数", Placement: port.PluginPlacementPage,
		Rows: []port.PluginSettingRow{{Key: "x", Label: "X", Kind: port.PluginFieldNumber, Value: "1"}},
	})
	admin := &fakePluginAdmin{list: []PluginState{
		{Name: "memory", State: "active", Enabled: true, Provides: []string{"memory", "settings"}},
		{Name: "recall", State: "stopped", Enabled: false, Err: "清单校验失败"},
	}}
	u := newHeadless(t, Options{PluginSettings: ps, PluginAdmin: admin})
	f := newSettingsForm(u)
	f.page = pagePlugins
	return f, admin, ps
}

// TestPluginsPageListsAndToggles 清单在页上，开关把目标状态交给数据面（取反由界面决定，
// 数据面只收「要什么」）。
func TestPluginsPageListsAndToggles(t *testing.T) {
	t.Parallel()
	f, admin, _ := pluginsWithList(t)

	var items int
	for _, r := range f.pageRows(pagePlugins) {
		if r.kind == rowPlugItem {
			items++
		}
	}
	if items != 2 {
		t.Fatalf("清单行 = %d，want 2", items)
	}
	if len(f.plug.items) != 2 {
		t.Fatalf("清单按钮 = %d，want 2", len(f.plug.items))
	}

	// memory 已启用 → 点一下应请求停用；recall 未启用 → 应请求启用。
	// 动作走旁路 goroutine（启用可能弹确认），这里等两个结果回灌再断言。
	f.togglePlugin(nil, f.plug.list[0])
	f.togglePlugin(nil, f.plug.list[1])
	for i := 0; i < 2; i++ {
		<-f.toolRes
	}
	// 两个动作各在自己的 goroutine 里跑，顺序不定；断言收到的请求集合。
	got := map[string]bool{}
	reqs := admin.requests()
	for _, v := range reqs {
		got[v] = true
	}
	if len(got) != 2 || !got["memory:off"] || !got["recall:on"] {
		t.Fatalf("启停请求 = %v，want memory:off 与 recall:on", reqs)
	}
}

// TestPluginsPageFocusPageSections 声明为独立页的分区不出现在清单里，进入后这一页只属于它。
func TestPluginsPageFocusPageSections(t *testing.T) {
	t.Parallel()
	f, _, ps := pluginsWithList(t)

	// 清单模式：recall 的 page 分区不内联（只有 memory 的两段）。
	for _, si := range f.plugSectionsOf("") {
		if f.plug.snap[si].Plugin == "recall" {
			t.Fatalf("独立页分区被内联渲染了：%+v", f.plug.snap[si])
		}
	}
	// memory 没有 page 分区 → 清单行不给入口；recall 有。
	if f.plugHasPageSections("memory") || !f.plugHasPageSections("recall") {
		t.Fatalf("入口判定错了：memory=%v recall=%v",
			f.plugHasPageSections("memory"), f.plugHasPageSections("recall"))
	}

	// 进入 recall 的独立页：只显示它自己的分区，且带返回条。
	f.plug.focus = "recall"
	var groups, rowsN, backs int
	for _, r := range f.pageRows(pagePlugins) {
		switch r.kind {
		case rowPlugGroup:
			groups++
			if f.plug.snap[r.prov].Plugin != "recall" {
				t.Fatalf("独立页里出现了别的插件：%+v", f.plug.snap[r.prov])
			}
		case rowPlugRow:
			rowsN++
		case rowPlugBack:
			backs++
		}
	}
	if groups != 1 || rowsN != 1 || backs != 1 {
		t.Fatalf("独立页行 = 分区 %d / 参数 %d / 返回 %d，want 1/1/1", groups, rowsN, backs)
	}

	// 焦点插件没了（停用或改了声明）→ 自动退回清单。
	ps.secs = ps.secs[:2] // 只剩 memory 的两段
	f.reloadPluginSettings()
	if f.plug.focus != "" {
		t.Fatalf("焦点插件没有分区了，应退回清单：%q", f.plug.focus)
	}
}

// TestPluginsPageMovesWholePluginWhenAnyPageSection 一段声明为独立页 → 该插件其余分区一起搬家。
// 同一插件的参数不劈成两半（D350），否则用户在清单页改一半、在独立页改另一半。
func TestPluginsPageMovesWholePluginWhenAnyPageSection(t *testing.T) {
	t.Parallel()
	ps := pluginSettingsFixture() // memory 的两段（全内联）
	ps.secs = append(ps.secs,
		port.PluginSettingsSection{
			Plugin: "mixed", ID: "short", Title: "短参数",
			Rows: []port.PluginSettingRow{{Key: "a", Label: "A", Kind: port.PluginFieldNumber, Value: "1"}},
		},
		port.PluginSettingsSection{
			Plugin: "mixed", ID: "long", Title: "长参数", Placement: port.PluginPlacementPage,
			Rows: []port.PluginSettingRow{{Key: "b", Label: "B", Kind: port.PluginFieldNumber, Value: "2"}},
		},
	)
	u := newHeadless(t, Options{PluginSettings: ps})
	f := newSettingsForm(u)
	f.page = pagePlugins

	for _, si := range f.plugSectionsOf("") {
		if f.plug.snap[si].Plugin == "mixed" {
			t.Fatalf("有独立页分区的插件被内联了一半：%+v", f.plug.snap[si])
		}
	}
	if n := len(f.plugSectionsOf("mixed")); n != 2 {
		t.Fatalf("独立页里应有该插件的全部分区，得到 %d 段", n)
	}

	// 焦点落在这个插件上时，两段都在同一页；清单让位给参数，出口只有返回条。
	f.plug.focus = "mixed"
	var groups, items, backs int
	for _, r := range f.pageRows(pagePlugins) {
		switch r.kind {
		case rowPlugGroup:
			groups++
		case rowPlugItem:
			items++
		case rowPlugBack:
			backs++
		}
	}
	if groups != 2 || items != 0 || backs != 1 {
		t.Fatalf("独立页 = 分区 %d / 清单行 %d / 返回 %d，want 2/0/1", groups, items, backs)
	}
}
