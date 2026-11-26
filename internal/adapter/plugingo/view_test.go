package plugingo

import (
	"context"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// fakeViews 只验转换：记住收到的动作，回一棵固定形状的树。
type fakeViews struct {
	got  v1.ViewAction
	spec []v1.ViewSpec
}

func (f *fakeViews) Views() []v1.ViewSpec { return f.spec }

func (f *fakeViews) Render(_ context.Context, viewID string) (v1.Node, error) {
	return v1.Node{
		Kind: v1.NodeText,
		Text: viewID,
		Children: []v1.Node{
			{
				Kind:  v1.NodeTable,
				Table: &v1.TableSpec{Columns: []string{"层", "标题"}, Rows: [][]string{{"1", "甲"}}},
			},
			{
				Kind:    v1.NodeForm,
				Fields:  []v1.FormField{{Key: "k", Label: "标签", Kind: v1.FieldSwitch, Value: "true"}},
				Actions: []v1.Action{{ID: "go", Label: "开始"}},
			},
		},
	}, nil
}

func (f *fakeViews) OnAction(_ context.Context, act v1.ViewAction) (v1.Node, error) {
	f.got = act
	return v1.Node{Kind: v1.NodeText, Text: "done"}, nil
}

func TestViewsConvert(t *testing.T) {
	p := &fakeViews{spec: []v1.ViewSpec{{ID: "tree", Title: "记忆树"}}}
	v := NewViews("memory", p)

	infos := v.Views()
	if len(infos) != 1 || infos[0].Plugin != "memory" || infos[0].ID != "tree" || infos[0].Title != "记忆树" {
		t.Fatalf("视图声明没带上插件名：%+v", infos)
	}

	n, err := v.Render(context.Background(), "memory", "tree")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != port.UIText || n.Text != "tree" || len(n.Children) != 2 {
		t.Fatalf("顶层形状不对：%+v", n)
	}
	// 表格摊平：嵌套的 Table 指针变成本层的列与行。
	tbl := n.Children[0]
	if tbl.Kind != port.UITable || len(tbl.Columns) != 2 || len(tbl.Rows) != 1 || tbl.Rows[0][1] != "甲" {
		t.Fatalf("表格没摊平：%+v", tbl)
	}
	// 字段与动作的字符串枚举要过边界（FieldKind 是 string 底层类型）。
	form := n.Children[1]
	if len(form.Fields) != 1 || form.Fields[0].Kind != "switch" || form.Fields[0].Value != "true" {
		t.Fatalf("字段映射不对：%+v", form.Fields)
	}
	if len(form.Actions) != 1 || form.Actions[0].ID != "go" || form.Actions[0].Label != "开始" {
		t.Fatalf("动作映射不对：%+v", form.Actions)
	}
}

func TestViewsActPassesValues(t *testing.T) {
	p := &fakeViews{}
	v := NewViews("memory", p)
	n, err := v.Act(context.Background(), port.UIViewAction{
		Plugin: "memory", ViewID: "tree", ActionID: "go",
		Values: map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.Text != "done" {
		t.Fatalf("动作后的新帧：%+v", n)
	}
	if p.got.ViewID != "tree" || p.got.ActionID != "go" || p.got.Values["k"] != "v" {
		t.Fatalf("动作没原样交回插件：%+v", p.got)
	}
}
