package plugingo

// view.go 插件面板 → 内核面板端口（D346）。
//
// 与 toolset.go 同一套思路：一次调用现问插件，不缓存声明——视图列表跟着插件的运行状态走，
// 缓存没有收益（面板是低频打开的东西），却有「列表里有、点开 404」的风险。
//
// 组件树是平铺转换：pluginapi.Node 的 Table 是嵌套的 *TableSpec，内核侧摊成 Columns/Rows，
// 少一层指针，渲染器不用再判空。

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// Views 一个插件的面板面在内核侧的身份。
type Views struct {
	name string
	p    v1.ViewProvider
}

// NewViews 把插件的面板面接成内核端口。名字只用于报因与界面归组。
func NewViews(name string, p v1.ViewProvider) *Views { return &Views{name: name, p: p} }

var _ port.PluginViews = (*Views)(nil)

// Views 本插件的面板声明（带上插件名，界面据此区分同名视图）。
func (v *Views) Views() []port.UIViewInfo {
	specs := v.p.Views()
	out := make([]port.UIViewInfo, 0, len(specs))
	for _, s := range specs {
		out = append(out, port.UIViewInfo{Plugin: v.name, ID: s.ID, Title: s.Title})
	}
	return out
}

// Render 取当前帧。
func (v *Views) Render(ctx context.Context, _ string, viewID string) (port.UINode, error) {
	n, err := v.p.Render(toV1Session(ctx), viewID)
	if err != nil {
		return port.UINode{}, err
	}
	return toPortNode(n), nil
}

// Act 交回一次操作。
func (v *Views) Act(ctx context.Context, act port.UIViewAction) (port.UINode, error) {
	n, err := v.p.OnAction(toV1Session(ctx), v1.ViewAction{
		ViewID:   act.ViewID,
		ActionID: act.ActionID,
		Values:   act.Values,
	})
	if err != nil {
		return port.UINode{}, err
	}
	return toPortNode(n), nil
}

// toPortNode 组件树 → 内核中性树。
func toPortNode(n v1.Node) port.UINode {
	out := port.UINode{
		Kind:    port.UIKind(n.Kind),
		Text:    n.Text,
		Items:   n.Items,
		Fields:  toPortFields(n.Fields),
		Actions: toPortActions(n.Actions),
	}
	if n.Table != nil {
		out.Columns = n.Table.Columns
		out.Rows = n.Table.Rows
	}
	if len(n.Children) > 0 {
		out.Children = make([]port.UINode, 0, len(n.Children))
		for _, c := range n.Children {
			out.Children = append(out.Children, toPortNode(c))
		}
	}
	return out
}

func toPortFields(in []v1.FormField) []port.UIFormField {
	if len(in) == 0 {
		return nil
	}
	out := make([]port.UIFormField, 0, len(in))
	for _, f := range in {
		out = append(out, port.UIFormField{
			Key:    f.Key,
			Label:  f.Label,
			Help:   f.Help,
			Kind:   string(f.Kind),
			Value:  f.Value,
			Values: f.Values,
		})
	}
	return out
}

func toPortActions(in []v1.Action) []port.UIAction {
	if len(in) == 0 {
		return nil
	}
	out := make([]port.UIAction, 0, len(in))
	for _, a := range in {
		out = append(out, port.UIAction{ID: a.ID, Label: a.Label})
	}
	return out
}
