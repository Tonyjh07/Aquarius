package main

// pluginsettings.go 插件设置供给面的聚合处（D347）。
//
// 与 pluginview.go 同一层：port.PluginSettings 是「所有插件」的视角，而 plugingo.Settings
// 只管一个插件，这里遍历 Active 的实例做聚合。
//
// 取实例的规则也一样：每次现取，不缓存接口值（面板/设置窗开着时插件可能被停用又启用）。
// 写路径额外取一次 Guard——ApplySettings 会让插件改自己的状态，停用流程正在等的话，
// 这一次写应该被挡住，而不是写进一个已经 Shutdown 的实例。

import (
	"context"
	"errors"

	"github.com/Tonyjh07/Aquarius/internal/adapter/plugingo"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/plugin/tierone"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// pluginSettings 已启用插件的设置面（装配根交给设置窗）。
//
// reg 同 pluginViews：UI 建得比插件宿主早，构造那一刻注册表还是 nil，所以存取用的
// 函数而不是注册表本身——否则设置窗永远是「没有已启用的插件提供可调参数」。
type pluginSettings struct{ reg func() *tierone.Registry }

var _ port.PluginSettings = pluginSettings{}

// registry 现取注册表；未接线（闭包本身为 nil）时返回 nil。
func (s pluginSettings) registry() *tierone.Registry {
	if s.reg == nil {
		return nil
	}
	return s.reg()
}

// Sections 当前可调的设置分区。只含 Active 且实现了 SettingsSectionProvider 的插件。
//
// 不取 Guard：与 Views() 同级，只是一次内存里的声明读取。
func (s pluginSettings) Sections() []port.PluginSettingsSection {
	reg := s.registry()
	if reg == nil {
		return nil
	}
	var out []port.PluginSettingsSection
	for _, rec := range reg.List() {
		inst, _, ok := reg.Active(rec.Name)
		if !ok {
			continue
		}
		sp, ok := inst.(v1.SettingsSectionProvider)
		if !ok {
			continue
		}
		out = append(out, plugingo.NewSettings(rec.Name, sp).Sections()...)
	}
	return out
}

// Apply 把某个分区的一次提交交给插件。
func (s pluginSettings) Apply(ctx context.Context, plugin, section string, values map[string]string) error {
	reg := s.registry()
	if reg == nil {
		return errors.New(i18n.T("plugin.unwired"))
	}
	inst, guard, ok := reg.Active(plugin)
	if !ok {
		return errors.New(i18n.Tf("plugin.t1.view_inactive", "name", plugin))
	}
	if guard != nil {
		if !guard.Enter() {
			return errors.New(i18n.Tf("plugin.t1.view_draining", "name", plugin))
		}
		defer guard.Leave()
	}
	sp, ok := inst.(v1.SettingsSectionProvider)
	if !ok {
		return errors.New(i18n.Tf("plugin.t1.settings_unsupported", "name", plugin))
	}
	return plugingo.NewSettings(plugin, sp).Apply(ctx, section, values)
}
