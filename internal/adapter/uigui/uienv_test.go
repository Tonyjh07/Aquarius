package uigui

import (
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// uienv_test.go 环境快照（D352）：只读面交出去的东西必须真的反映当前状态，
// 并且「不知道」与「浅色」要能分开。

// TestEnvironmentReportsCurrentState 深色 + 高分屏 + 字体候选，六个令牌都是 RRGGBB。
func TestEnvironmentReportsCurrentState(t *testing.T) {
	t.Parallel()
	// 深色快照直接写本 UI 的原子槽，**不调 applyTheme**：主题档是包级全局
	// （activeTokens），并行测试里改它会污染别的用例的配色（几何扫描按底色找边界）。
	u := newHeadless(t, Options{})
	p := resolvePalette("dark", true)
	u.pal.Store(&p)
	fp := newFakePlat()
	fp.MainScore = 1.5
	fp.Fonts = []string{`C:\Windows\Fonts\msyh.ttc`}
	fp.Symbols = []string{`C:\Windows\Fonts\seguisym.ttf`}
	u.plat = fp

	env := u.Environment()
	if env.Frontend != port.FrontendGUI {
		t.Fatalf("前端 = %q，want gui", env.Frontend)
	}
	if !env.Dark {
		t.Error("固定深色档应报告 Dark")
	}
	if env.Scale != 1.5 {
		t.Errorf("Scale = %v，want 1.5", env.Scale)
	}
	for name, v := range map[string]string{
		"BG": env.Tokens.BG, "FG": env.Tokens.FG, "Muted": env.Tokens.Muted,
		"Accent": env.Tokens.Accent, "Border": env.Tokens.Border, "Sel": env.Tokens.Sel,
	} {
		if len(v) != 6 || !isHex(v) {
			t.Errorf("%s = %q，want 六位十六进制", name, v)
		}
	}
	if env.Tokens.BG == env.Tokens.FG {
		t.Error("底与字同色：语义色取错了字段")
	}
	if len(env.Fonts) != 1 || len(env.SymbolFonts) != 1 {
		t.Errorf("字体候选没转发：%+v / %+v", env.Fonts, env.SymbolFonts)
	}
}

// TestEnvironmentUnknownIsNotLight 未应用主题时不能编一个「浅色」出来：
// 插件的判断会变成「宿主说浅色」而不是「宿主没说」。
func TestEnvironmentUnknownIsNotLight(t *testing.T) {
	t.Parallel()
	u := newHeadless(t, Options{})
	u.pal.Store(nil)

	env := u.Environment()
	if env.Frontend != port.FrontendGUI {
		t.Fatalf("前端 = %q，want gui（前端与主题无关）", env.Frontend)
	}
	if env.Dark {
		t.Error("没有快照时不该声称深色")
	}
	if env.Tokens != (port.UIThemeTokens{}) {
		t.Errorf("没有快照时令牌应全空：%+v", env.Tokens)
	}
}

// isHex 六位十六进制（大小写都收：宿主输出大写，插件不该靠大小写辨别）。
func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
