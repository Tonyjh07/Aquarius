package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/plugin/tierone"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// viewPlug 只实现面板面：验聚合器在真实注册表上的行为（未启用的插件不该出现在列表里）。
type viewPlug struct{}

func (viewPlug) Manifest() v1.Manifest {
	return v1.Manifest{Name: "panel", API: v1.APIVersion, Provides: []string{"view"}}
}

func (viewPlug) Create(v1.Host) (v1.Instance, error) { return viewInst{}, nil }

type viewInst struct{}

func (viewInst) Start() error                   { return nil }
func (viewInst) Shutdown(context.Context) error { return nil }

func (viewInst) Views() []v1.ViewSpec {
	return []v1.ViewSpec{{ID: "tree", Title: "记忆树"}}
}

func (viewInst) Render(_ context.Context, viewID string) (v1.Node, error) {
	if viewID != "tree" {
		return v1.Node{}, errors.New("no such view")
	}
	return v1.Node{Kind: v1.NodeText, Text: "帧内容"}, nil
}

func (viewInst) OnAction(_ context.Context, act v1.ViewAction) (v1.Node, error) {
	return v1.Node{Kind: v1.NodeText, Text: "点了 " + act.ActionID}, nil
}

// 面板只出现在已启用的插件里：停用之后 /plugin view 报「没启用」，不是「没有面板」。
func TestPluginViewsFollowsEnableState(t *testing.T) {
	reg := tierone.New(tierone.Options{
		Factories: []v1.Factory{viewPlug{}},
		NewHost:   func(v1.Manifest) v1.Host { return nil },
	})
	pv := pluginViews{reg: func() *tierone.Registry { return reg }}

	if got := pv.Views(); len(got) != 0 {
		t.Fatalf("未启用时不该有面板：%+v", got)
	}
	if _, err := pv.Render(context.Background(), "panel", "tree"); err == nil {
		t.Fatal("未启用时应报错")
	}

	if err := reg.SetEnabled(context.Background(), "panel", true); err != nil {
		t.Fatal(err)
	}
	got := pv.Views()
	if len(got) != 1 || got[0].Plugin != "panel" || got[0].ID != "tree" {
		t.Fatalf("启用后应有一条：%+v", got)
	}

	n, err := pv.Render(context.Background(), "panel", "tree")
	if err != nil || n.Text != "帧内容" {
		t.Fatalf("取帧失败：%+v %v", n, err)
	}
	// 动作带着插件名进来，聚合器据此找实例。
	n, err = pv.Act(context.Background(), port.UIViewAction{Plugin: "panel", ViewID: "tree", ActionID: "go"})
	if err != nil || !strings.Contains(n.Text, "go") {
		t.Fatalf("动作失败：%+v %v", n, err)
	}

	if err := reg.SetEnabled(context.Background(), "panel", false); err != nil {
		t.Fatal(err)
	}
	if got := pv.Views(); len(got) != 0 {
		t.Fatalf("停用后又该空了：%+v", got)
	}
}
