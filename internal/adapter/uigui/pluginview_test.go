package uigui

import (
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 控件只在插件给的值真的变了时才重置——否则用户正在输入的文字会被每一帧刷回去。
func TestViewFieldApplyKeepsTyping(t *testing.T) {
	fs := &viewFieldState{}
	f := port.UIFormField{Key: "k", Kind: "text", Value: "old"}
	fs.apply(f, true)
	if got := fs.ed.Text(); got != "old" {
		t.Fatalf("首次要落插件值：%q", got)
	}

	// 用户改字，插件这一帧还给同一个值：不许重置。
	fs.ed.SetText("typed")
	fs.apply(f, false)
	if got := fs.ed.Text(); got != "typed" {
		t.Fatalf("插件值没变时不该覆盖用户输入：%q", got)
	}

	// 插件给了新值：这是插件的状态权威，要盖掉。
	f.Value = "new"
	fs.apply(f, false)
	if got := fs.ed.Text(); got != "new" {
		t.Fatalf("插件给新值时要跟上：%q", got)
	}
}

func TestViewFieldApplySwitchAndSelect(t *testing.T) {
	fs := &viewFieldState{}
	fs.apply(port.UIFormField{Key: "s", Kind: "switch", Value: "true"}, true)
	if !fs.sw.Value {
		t.Error("开关应打开")
	}
	fs.apply(port.UIFormField{Key: "s", Kind: "switch", Value: "false"}, false)
	if fs.sw.Value {
		t.Error("开关应关闭")
	}

	// 下拉按值定位；值不在候选项里落第 0 项（不回退到「上一个」这种记忆态）。
	fs.apply(port.UIFormField{Key: "m", Kind: "select", Value: "b", Values: []string{"a", "b"}}, true)
	if fs.sel != 1 {
		t.Errorf("下标应为 1：%d", fs.sel)
	}
	fs.apply(port.UIFormField{Key: "m", Kind: "select", Value: "zzz", Values: []string{"a", "b"}}, false)
	if fs.sel != 0 {
		t.Errorf("未知值应落第 0 项：%d", fs.sel)
	}
}

// 动作要带上「此刻可见的全部字段值」：插件据此判断点了哪个按钮、表单填了什么。
func TestCollectValues(t *testing.T) {
	st := &pluginViewState{fields: map[string]*viewFieldState{}}
	node := port.UINode{Kind: port.UIText, Children: []port.UINode{
		{
			Kind: port.UIForm,
			Fields: []port.UIFormField{
				{Key: "name", Kind: "text", Value: "甲"},
				{Key: "on", Kind: "switch", Value: "true"},
				{Key: "mode", Kind: "select", Value: "slow", Values: []string{"fast", "slow"}},
				{Key: "typed", Kind: "text", Value: ""},
			},
		},
	}}
	// 模拟渲染：字段控件在这一帧被建立并同步（值收集读的是当前帧的树）。
	st.node = node
	for _, f := range node.Children[0].Fields {
		st.field(f)
	}
	st.fields["typed"].ed.SetText("用户输入")

	got := st.values()
	if got["name"] != "甲" || got["on"] != "true" || got["mode"] != "slow" {
		t.Fatalf("字段值不全：%+v", got)
	}
	// 控件里的当前值优先于插件声明值——用户刚输入的东西要交回去。
	if got["typed"] != "用户输入" {
		t.Fatalf("应取控件当前值：%+v", got)
	}
}

// 没有表单就没有值表（不是空 map 到处传）。
func TestCollectValuesNone(t *testing.T) {
	st := &pluginViewState{fields: map[string]*viewFieldState{}}
	if got := st.values(); got != nil {
		t.Fatalf("没有表单时应为 nil：%+v", got)
	}
}
