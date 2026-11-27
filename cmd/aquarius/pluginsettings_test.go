package main

// pluginsettings_test.go 设置聚合器在真实注册表上的行为，重点锁住**晚绑定**：
// UI 早于插件宿主构造，交进去的必须是一个「取注册表」的函数，而不是构造那一刻的
// 注册表值（值拷贝会拷到 nil，设置窗永远显示空态）。

import (
	"context"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/plugin/tierone"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// settingsPlug 同时提供面板与设置：两处聚合器共用一份注册表，一起验晚绑定。
type settingsPlug struct{}

func (settingsPlug) Manifest() v1.Manifest {
	return v1.Manifest{Name: "cfg", API: v1.APIVersion, Provides: []string{"view", "settings"}}
}

func (settingsPlug) Create(v1.Host) (v1.Instance, error) { return &settingsInst{}, nil }

type settingsInst struct {
	applied map[string]string
}

func (*settingsInst) Start() error                   { return nil }
func (*settingsInst) Shutdown(context.Context) error { return nil }

func (*settingsInst) Views() []v1.ViewSpec { return []v1.ViewSpec{{ID: "panel", Title: "面板"}} }

func (*settingsInst) Render(context.Context, string) (v1.Node, error) {
	return v1.Node{Kind: v1.NodeText, Text: "帧"}, nil
}

func (*settingsInst) OnAction(context.Context, v1.ViewAction) (v1.Node, error) {
	return v1.Node{Kind: v1.NodeText}, nil
}

func (*settingsInst) Sections() []v1.SettingsSection {
	return []v1.SettingsSection{{
		ID: "main", Title: "参数",
		Rows: []v1.SettingsRow{{Key: "size", Label: "大小", Kind: v1.FieldNumber, Value: "8"}},
	}}
}

func (p *settingsInst) ApplySettings(_ context.Context, patch v1.SettingsPatch) error {
	p.applied = patch.Values
	return nil
}

// 聚合器构造时注册表还是 nil，之后才装上：两处都必须现取，不能钉死构造那一刻的值。
func TestPluginSurfacesBindAfterUISpawn(t *testing.T) {
	var reg *tierone.Registry
	views := pluginViews{reg: func() *tierone.Registry { return reg }}
	settings := pluginSettings{reg: func() *tierone.Registry { return reg }}

	// 构造之后、宿主就绪之前：空列表而不是崩溃。
	if got := views.Views(); len(got) != 0 {
		t.Fatalf("注册表未就绪时不该有面板：%+v", got)
	}
	if got := settings.Sections(); len(got) != 0 {
		t.Fatalf("注册表未就绪时不该有设置分区：%+v", got)
	}

	reg = tierone.New(tierone.Options{
		Factories: []v1.Factory{settingsPlug{}},
		NewHost:   func(v1.Manifest) v1.Host { return nil },
	})
	if err := reg.SetEnabled(context.Background(), "cfg", true); err != nil {
		t.Fatal(err)
	}

	if got := views.Views(); len(got) != 1 || got[0].ID != "panel" {
		t.Fatalf("启用后应有面板：%+v", got)
	}
	secs := settings.Sections()
	if len(secs) != 1 || secs[0].Plugin != "cfg" || secs[0].ID != "main" {
		t.Fatalf("启用后应有设置分区：%+v", secs)
	}
	if len(secs[0].Rows) != 1 || secs[0].Rows[0].Key != "size" || secs[0].Rows[0].Value != "8" {
		t.Fatalf("分区行表转错了：%+v", secs[0].Rows)
	}

	if err := settings.Apply(context.Background(), "cfg", "main", map[string]string{"size": "12"}); err != nil {
		t.Fatal(err)
	}
	inst, _, ok := reg.Active("cfg")
	if !ok {
		t.Fatal("启用后应取得到实例")
	}
	if got := inst.(*settingsInst).applied["size"]; got != "12" {
		t.Fatalf("提交没有送到插件：%q", got)
	}
}
