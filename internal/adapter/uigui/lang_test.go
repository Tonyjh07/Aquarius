package uigui

import (
	"os"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// TestMain 选定本包测试的界面语言：`AQUARIUS_UI_LANG` 说了算，缺省 zh。
//
// 以前硬钉 zh，是因为文案断言按中文写死；I6 之后断言的期望值从 i18n 表里取——被测代码
// 与断言读同一张表，语言定成哪种两边都一致。钉 zh 只是保底（内部包的单测、CI 默认路径），
// 环境变量一设，整包就在 en 下跑一遍：这正是门禁 `AQUARIUS_UI_LANG=en go test ./...` 的
// 作用面（本包是唯一把语言写进 TestMain 的包，其余包吃 i18n 的缺省 zh）。
func TestMain(m *testing.M) {
	raw := os.Getenv("AQUARIUS_UI_LANG")
	if raw == "" {
		raw = "zh" // 缺省钉 zh：auto 会跟随系统语言，跨机器跑出两套结果
	}
	sel, err := i18n.Parse(raw)
	if err != nil {
		panic(err)
	}
	i18n.Use(sel)
	os.Exit(m.Run())
}

// TestPageKeysResolve 页签取词的闭环：`pageName(i)` 用 "ui.settings.page." + Key() 拼接，
// 缺键时 `T` 会把拼出来的 key 原样印在页签上，而 i18n 包的缺键扫描看不到这种拼法
// （那边只登记了前缀）。逐页确认取得到词，才能说拼接那一侧也守住了。
func TestPageKeysResolve(t *testing.T) {
	for i := 0; i < settingsPageCount; i++ {
		key := "ui.settings.page." + settingsPages[i].Key()
		if got := i18n.T(key); got == key {
			t.Errorf("页 %d（%s）取不到词：%s 不在表里", i, settingsPages[i].Key(), key)
		}
	}
}
