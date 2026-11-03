package uigui

import (
	"os"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// TestMain 把界面语言钉成 zh。本包的文案断言（页名列表、菜单项集、教程行）都按中文写死，
// 而取词默认跟随本机 locale——换一台英文机器跑，这些断言会整片失败，且失败原因看着像
// 文案改坏了。钉住之后，跨语言的正确性由 i18n 包的同构测试与各处的英文表负责。
func TestMain(m *testing.M) {
	sel, err := i18n.Parse("zh")
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
