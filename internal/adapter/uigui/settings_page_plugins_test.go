package uigui

import (
	"context"
	"errors"
	"image"
	"strconv"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakePluginSettings 插件设置面替身：声明两类分区，Apply 时把值写回自己的快照
// （模拟插件对值做归一——界面必须显示归一后的结果，而不是用户刚敲的那个）。
type fakePluginSettings struct {
	secs      []port.PluginSettingsSection
	gotPlugin string
	gotSec    string
	gotValues map[string]string
	applyErr  error
}

func (f *fakePluginSettings) Sections() []port.PluginSettingsSection { return f.secs }

func (f *fakePluginSettings) Apply(_ context.Context, plugin, section string, values map[string]string) error {
	f.gotPlugin, f.gotSec, f.gotValues = plugin, section, values
	if f.applyErr != nil {
		return f.applyErr
	}
	// 归一：预算超过 1000 按 1000 存（插件侧夹取）。
	for i := range f.secs {
		if f.secs[i].ID != section {
			continue
		}
		for j := range f.secs[i].Rows {
			if v, ok := values[f.secs[i].Rows[j].Key]; ok {
				if f.secs[i].Rows[j].Key == "budget" {
					if n, err := strconv.Atoi(v); err == nil && n > 1000 {
						v = "1000"
					}
				}
				f.secs[i].Rows[j].Value = v
			}
		}
	}
	return nil
}

func pluginSettingsFixture() *fakePluginSettings {
	return &fakePluginSettings{secs: []port.PluginSettingsSection{
		{
			Plugin: "memory", ID: "recall", Title: "回忆", Desc: "决定注入多少标题",
			Rows: []port.PluginSettingRow{
				{Key: "budget", Label: "预算", Kind: port.PluginFieldNumber, Value: "600", Help: "token 数"},
				{Key: "dedup", Label: "去重", Kind: port.PluginFieldSwitch, Value: "true"},
				{Key: "mode", Label: "方式", Kind: port.PluginFieldSelect, Value: "a", Values: []string{"a", "b"}},
				{Key: "note", Label: "备注", Kind: port.PluginFieldTextarea, Value: "第一行"},
			},
		},
		{
			Plugin: "memory", ID: "decay", Title: "衰减",
			Rows: []port.PluginSettingRow{{Key: "base", Label: "起始停留", Kind: port.PluginFieldNumber, Value: "1"}},
		},
	}}
}

// openedPluginsForm 造一个已开窗、已取到插件快照的表单。
func openedPluginsForm(t *testing.T, ps port.PluginSettings) (*UI, *settingsForm) {
	t.Helper()
	u := newHeadless(t, Options{PluginSettings: ps})
	f := newSettingsForm(u)
	f.page = pagePlugins
	return u, f
}

// TestPluginsPageRowsAndBody 行计划形状与行型认领：每个 Rows() 产出的行都得有 Body 认领。
func TestPluginsPageRowsAndBody(t *testing.T) {
	t.Parallel()
	u, f := openedPluginsForm(t, pluginSettingsFixture())
	rows := f.pageRows(pagePlugins)
	if rows[0].kind != rowTitle || rows[0].text != pageName(pagePlugins) {
		t.Fatalf("首行应是页标题：%+v", rows[0])
	}
	var groups, items, saves int
	for _, r := range rows {
		switch r.kind {
		case rowPlugGroup:
			groups++
		case rowPlugRow:
			items++
		case rowPlugSave:
			saves++
		}
	}
	if groups != 2 || items != 5 || saves != 2 {
		t.Fatalf("行计划 = 分区 %d / 参数 %d / 保存 %d，want 2/5/2", groups, items, saves)
	}
	th := testTheme()
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
		if _, ok := (pluginsPage{}).Body(gtx, th, f, rows, i); !ok {
			t.Errorf("第 %d 行（kind=%d）无人认领", i, rows[i].kind)
		}
	}
	_ = u
}

// TestPluginsPageEmptyAndUnwired 没有插件 / 没接线时给一行说明，不留空白页。
func TestPluginsPageEmptyAndUnwired(t *testing.T) {
	t.Parallel()
	if _, f := openedPluginsForm(t, &fakePluginSettings{}); len(f.plug.snap) != 0 {
		t.Fatal("空快照不该长出行")
	}
	if _, f := openedPluginsForm(t, nil); f.plug.wired {
		t.Fatal("没接线时不该当成已接线")
	}
	th := testTheme()
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Constraints{Max: image.Pt(540, 400)}}
	for _, ps := range []port.PluginSettings{&fakePluginSettings{}, nil} {
		_, f := openedPluginsForm(t, ps)
		rows := f.pageRows(pagePlugins)
		if _, ok := (pluginsPage{}).Body(gtx, th, f, rows, len(rows)-1); !ok {
			t.Fatalf("空态/未接线行未被认领（%T）", ps)
		}
	}
}

// TestPluginsPageSaveCollectsSectionOnly 保存只收本分区的键，且保存后以插件侧值为准。
func TestPluginsPageSaveCollectsSectionOnly(t *testing.T) {
	t.Parallel()
	ps := pluginSettingsFixture()
	u, f := openedPluginsForm(t, ps)

	// 第一个分区：四行依次是 number / switch / select / textarea。
	f.plug.editors[0].SetText("1500")
	f.plug.checks[1].Value = false
	f.plug.selects[2].choose(1)
	f.plug.editors[3].SetText("改过的正文")

	f.savePluginSection(u, 0)

	if ps.gotPlugin != "memory" || ps.gotSec != "recall" {
		t.Fatalf("提交目标 = %s/%s, want memory/recall", ps.gotPlugin, ps.gotSec)
	}
	if len(ps.gotValues) != 4 {
		t.Fatalf("提交了 %d 个键，want 4（只收本分区）：%+v", len(ps.gotValues), ps.gotValues)
	}
	if ps.gotValues["budget"] != "1500" || ps.gotValues["dedup"] != "false" ||
		ps.gotValues["mode"] != "b" || ps.gotValues["note"] != "改过的正文" {
		t.Fatalf("收集到的值不对：%+v", ps.gotValues)
	}
	// 第二个分区的键不该出现在这次提交里。
	if _, ok := ps.gotValues["base"]; ok {
		t.Fatal("别的分区的键被一起提交了")
	}
	// 插件把 1500 夹到 1000；界面必须显示归一后的值。
	if got := f.plug.editors[0].Text(); got != "1000" {
		t.Fatalf("保存后显示 = %q, want 1000（以插件侧为准）", got)
	}
	if f.plug.err || f.plug.status == "" {
		t.Fatalf("成功保存应有提示：status=%q err=%v", f.plug.status, f.plug.err)
	}
}

// TestPluginsPageSaveErrorShown 插件的报错原样显示在状态行（值是插件给的人话，宿主不翻译）。
func TestPluginsPageSaveErrorShown(t *testing.T) {
	t.Parallel()
	ps := pluginSettingsFixture()
	ps.applyErr = errPlugTest
	u, f := openedPluginsForm(t, ps)
	f.savePluginSection(u, 1)
	if !f.plug.err || f.plug.status != errPlugTest.Error() {
		t.Fatalf("错误没显示出来：status=%q err=%v", f.plug.status, f.plug.err)
	}
}

// errPlugTest 插件侧报错的替身。
var errPlugTest = errors.New("值超出范围")
