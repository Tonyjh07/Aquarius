package uigui

// 设置窗左栏：页数增长后的滚动与「页名折行时长高」两条（en 布局走查发现）。

import (
	"image"
	"testing"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// navFrame 单帧渲染设置窗（720×560，与生产窗同高），返回后可直接读 f.navList.Position。
func navFrame(t *testing.T, q *input.Router, u *UI, f *settingsForm, size image.Point) {
	t.Helper()
	ops := new(op.Ops)
	settingsFrame(layout.Context{
		Ops:         ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(size),
		Source:      q.Source(),
	}, testTheme(), u, f)
	q.Frame(ops)
}

// TestSettingsNavScrolls 左栏可滚：窗高装不下全部页时，把选中页滚进视口。
//
// 窗高 220dp 只放得下五六个导航项，选中「关于」（最后一页）后左栏必须真的滚起来；
// 从前 Flex 一路 Rigid 排下来，装不下的项被裁掉且没有滚动条，只能靠拉大窗口看见。
func TestSettingsNavScrolls(t *testing.T) {
	restoreLight(t)
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return fillSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
	q := new(input.Router)
	size := image.Pt(720, 220)

	navFrame(t, q, u, f, size) // 首帧：滚到第 0 页（= 顶部）
	if off := f.navList.Position.Offset; off != 0 {
		t.Fatalf("首帧左栏偏移 = %d, want 0", off)
	}

	f.page = settingsPageCount - 1
	navFrame(t, q, u, f, size)
	if off := f.navList.Position.Offset; off <= 0 {
		t.Fatalf("选中末页后左栏偏移 = %d, want > 0（没有滚动就等于末页在窗外）", off)
	}
}

// TestSettingsNavItemGrows 页名折两行时导航项跟着长高。
//
// en 下 "External agents" / "Tools & MCP" / "System prompt" 都会折两行，而项高从前写死
// 36dp：两行字画在同一个 36 格里，行距看着是歪的、并且压到下一项的位置上。
func TestSettingsNavItemGrows(t *testing.T) {
	restoreLight(t)
	sel, err := i18n.Parse("en")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	i18n.Use(sel)
	t.Cleanup(func() {
		zh, _ := i18n.Parse("zh")
		i18n.Use(zh)
	})

	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return fillSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
	th := testTheme()

	// 宽度取 110dp：140 减去栏内留白后，"External agents" 一定折行。
	ops := new(op.Ops)
	gtx := layout.Context{
		Ops:         ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Constraints{Max: image.Pt(110, 1000)},
		Source:      new(input.Router).Source(),
	}
	if got := f.navItem(gtx, th, pageDelegates).Size.Y; got <= int(navItemDpH) {
		t.Fatalf("折行页名的项高 = %d, want > %d", got, int(navItemDpH))
	}
}
