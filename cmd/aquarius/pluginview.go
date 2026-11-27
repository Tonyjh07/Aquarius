package main

// pluginview.go 面板供给面的聚合处（D346）。
//
// port.PluginViews 是「所有插件」的视角，而 plugingo.Views 只管一个插件，所以这里做一层
// 聚合：遍历当前 Active 的插件，探测 ViewProvider，转成内核端口。
//
// 每次调用都重新取实例（与命令面同一条理由）：面板开着的时候插件可能被停用又启用，缓存的
// 接口值会指向一个已经 Shutdown 的实例。取到实例后用 Guard 记在途，停用流程会等这一帧画完。

import (
	"context"
	"errors"

	"github.com/Tonyjh07/Aquarius/internal/adapter/plugingo"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/plugin/tierone"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// pluginViews 已启用插件的面板面（装配根交给两个前端）。
//
// reg 是**取注册表的函数**，不是注册表本身：UI 在端口装配之后、插件宿主之前建，
// 构造那一刻 w.plugins1 还是 nil，值拷贝会把整个面板面钉死成空列表。与角色页的
// 现取写法同一个理由（`newCharactersView` 拿的也是闭包）。
type pluginViews struct{ reg func() *tierone.Registry }

// registry 现取注册表；未接线（闭包本身为 nil）时返回 nil。
func (v pluginViews) registry() *tierone.Registry {
	if v.reg == nil {
		return nil
	}
	return v.reg()
}

var _ port.PluginViews = pluginViews{}

// Views 当前可打开的面板。只含 Active 且实现了 ViewProvider 的插件。
//
// 这里不取 Guard：只是一次内存里的声明读取，与启用命令表时的 Commands() 同级。真取前者
// 就要在遍历里持有多个许可，而这条路径是界面开窗时调的——把停用挡在「读一个列表」后面不值。
func (v pluginViews) Views() []port.UIViewInfo {
	reg := v.registry()
	if reg == nil {
		return nil
	}
	var out []port.UIViewInfo
	for _, rec := range reg.List() {
		inst, _, ok := reg.Active(rec.Name)
		if !ok {
			continue
		}
		vp, ok := inst.(v1.ViewProvider)
		if !ok {
			continue
		}
		out = append(out, plugingo.NewViews(rec.Name, vp).Views()...)
	}
	return out
}

// Render 取一帧。
func (v pluginViews) Render(ctx context.Context, plugin, viewID string) (port.UINode, error) {
	vp, leave, err := v.viewProvider(plugin)
	if err != nil {
		return port.UINode{}, err
	}
	defer leave()
	return plugingo.NewViews(plugin, vp).Render(ctx, plugin, viewID)
}

// Act 交回一次操作，返回新帧。
func (v pluginViews) Act(ctx context.Context, act port.UIViewAction) (port.UINode, error) {
	vp, leave, err := v.viewProvider(act.Plugin)
	if err != nil {
		return port.UINode{}, err
	}
	defer leave()
	return plugingo.NewViews(act.Plugin, vp).Act(ctx, act)
}

// viewProvider 取插件实例的面板面，并记在途（leave 必须调）。
func (v pluginViews) viewProvider(plugin string) (v1.ViewProvider, func(), error) {
	reg := v.registry()
	if reg == nil {
		return nil, nil, errors.New(i18n.T("plugin.unwired"))
	}
	inst, guard, ok := reg.Active(plugin)
	if !ok {
		return nil, nil, errors.New(i18n.Tf("plugin.t1.view_inactive", "name", plugin))
	}
	if guard != nil {
		if !guard.Enter() {
			return nil, nil, errors.New(i18n.Tf("plugin.t1.view_draining", "name", plugin))
		}
	}
	leave := func() {
		if guard != nil {
			guard.Leave()
		}
	}
	vp, ok := inst.(v1.ViewProvider)
	if !ok {
		leave()
		return nil, nil, errors.New(i18n.Tf("plugin.t1.view_unsupported", "name", plugin))
	}
	return vp, leave, nil
}
