package uigui

// winmgr_view_test.go 插件面板窗（D351）：一窗一 (plugin, view)、尺寸按声明夹取、
// 停用按插件收编。真实窗口路径不进 CI，这里只测注册表与形状解析。

import (
	"context"
	"sync"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeViewProvider 面板面替身：只给声明，渲染返回一行文本。
type fakeViewProvider struct {
	specs []port.UIViewInfo
}

func (f *fakeViewProvider) Views() []port.UIViewInfo { return f.specs }

func (f *fakeViewProvider) Render(_ context.Context, plugin, view string) (port.UINode, error) {
	return port.UINode{Kind: port.UIText, Text: plugin + "/" + view}, nil
}

func (f *fakeViewProvider) Act(_ context.Context, _ port.UIViewAction) (port.UINode, error) {
	return port.UINode{}, nil
}

// TestViewWindowsCoexist 同一插件两个视图各开一个窗；同一视图重开只聚焦并挂重取标记。
func TestViewWindowsCoexist(t *testing.T) {
	t.Parallel()
	u := newHeadless(t, Options{})

	var mu sync.Mutex
	var keys []winKey
	u.wins = newWinHost(func(k winKey) *winHandle {
		mu.Lock()
		keys = append(keys, k)
		mu.Unlock()
		return &winHandle{done: make(chan struct{})}
	})

	u.showPluginView(port.OpenViewEvent{Plugin: "memory", ViewID: "tree"})
	u.showPluginView(port.OpenViewEvent{Plugin: "memory", ViewID: "archived"})
	u.showPluginView(port.OpenViewEvent{Plugin: "memory", ViewID: "tree"}) // 重开

	mu.Lock()
	n := len(keys)
	mu.Unlock()
	if n != 2 {
		t.Fatalf("开窗数 = %d，want 2（两个视图各一窗，重开不再开）", n)
	}

	tree := winKey{kind: winView, plugin: "memory", view: "tree"}
	if !u.wins.isOpenKey(tree) || !u.wins.isOpenKey(winKey{kind: winView, plugin: "memory", view: "archived"}) {
		t.Fatal("两个视图都应在开")
	}
	u.wins.mu.Lock()
	hd := u.wins.live[tree]
	u.wins.mu.Unlock()
	if hd == nil || !hd.reload.Load() {
		t.Error("重开应挂重取标记（再打开一次 = 重新问插件要一帧）")
	}

	// 停用插件：按插件收编，别的插件的窗不受影响。
	u.showPluginView(port.OpenViewEvent{Plugin: "note", ViewID: "list"})
	if got := u.wins.closeViewsOf("memory"); got != 2 {
		t.Fatalf("收编数 = %d，want 2", got)
	}
	if u.wins.isOpenKey(tree) {
		t.Error("memory 的窗应已关闭")
	}
	if !u.wins.isOpenKey(winKey{kind: winView, plugin: "note", view: "list"}) {
		t.Error("别的插件的窗不该被顺手关掉")
	}
}

// TestWindowShapeFollowsViewSpec 标题与尺寸取插件声明；未声明的视图走缺省。
func TestWindowShapeFollowsViewSpec(t *testing.T) {
	t.Parallel()
	vp := &fakeViewProvider{specs: []port.UIViewInfo{{
		Plugin: "memory", ID: "tree", Title: "记忆树",
		Window: port.UIWindow{Width: 880, Height: 640, MinWidth: 560, MinHeight: 400},
	}}}
	u := newHeadless(t, Options{Views: vp})

	title, g := u.windowShape(winKey{kind: winView, plugin: "memory", view: "tree"})
	if title != "记忆树" {
		t.Fatalf("标题 = %q，want 记忆树", title)
	}
	if g != (winGeom{880, 640, 560, 400}) {
		t.Fatalf("尺寸 = %+v，want 880×640 min 560×400", g)
	}

	// 视图不存在（插件改了声明、窗还开着）：退回缺省，不 panic。
	_, g2 := u.windowShape(winKey{kind: winView, plugin: "memory", view: "gone"})
	if g2 != winView.geometry() {
		t.Fatalf("未声明视图应走缺省：%+v", g2)
	}
}

// TestApplyWinSpecClamps 尺寸是请求不是命令：零值不动、越界夹取、最小不超过最终尺寸。
func TestApplyWinSpecClamps(t *testing.T) {
	t.Parallel()
	base := winView.geometry()

	cases := []struct {
		name string
		spec port.UIWindow
		want winGeom
	}{
		{"零值不动", port.UIWindow{}, base},
		{"太小抬到下限", port.UIWindow{Width: 100, Height: 80}, winGeom{320, 320, 320, 320}},
		{"太大压到上限", port.UIWindow{Width: 9000, Height: 9000}, winGeom{4096, 4096, 480, 380}},
		{
			"最小尺寸不超过声明尺寸",
			port.UIWindow{Width: 400, Height: 300, MinWidth: 900, MinHeight: 800},
			winGeom{400, 320, 400, 320}, // 高度也先夹到 320 下限，最小尺寸再跟它对齐
		},
	}
	for _, c := range cases {
		if got := applyWinSpec(base, c.spec); got != c.want {
			t.Errorf("%s：得到 %+v，want %+v", c.name, got, c.want)
		}
	}
}
