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
