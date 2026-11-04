package uigui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

func TestZZShotPages(t *testing.T) {
	// 离屏截图工具，不是断言型测试：不设 SHOT_PREFIX 就跳过，免得 `go test ./...`
	// 顺手往包目录里写一堆 png（被当成未跟踪文件混进提交）。
	prefix := os.Getenv("SHOT_PREFIX")
	if prefix == "" {
		t.Skip("设 SHOT_PREFIX 才跑（离屏截图）")
	}
	restoreLight(t)
	storeTokens(lightPalette())
	applyPaper(paperLight())
	lang := os.Getenv("SHOT_LANG")
	if lang == "" {
		lang = "zh"
	}
	sel, err := i18n.Parse(lang)
	if err != nil {
		t.Fatal(err)
	}
	i18n.Use(sel)
	defer func() { zh, _ := i18n.Parse("zh"); i18n.Use(zh) }()

	pxPerDp, pxPerSp := float32(1.25), float32(1.25)
	if os.Getenv("SHOT_BIG") != "" {
		pxPerSp = 2.5
	}
	w, h := int(660*pxPerDp), int(640*pxPerDp)
	_ = newHeadless(t, Options{})
	th := testTheme()
	mgr := baseFake()
	env, _ := welcomeEnvSnap(mgr, true, true)
	f := newWelcomeForm(env)
	f.apiKey.SetText("secret:AQ_K")
	if os.Getenv("SHOT_KEY_READY") != "" {
		f.env.snap.KeyReady = true
	}
	if os.Getenv("SHOT_DONE_ISSUES") != "" {
		// 末页「启动前检查」的两种形态：密钥根本取不到 / 空态没配。
		mgr.issues = []string{i18n.Tf("cmd.welcome.key.fetch_failed", "provider", `"deepseek"`,
			"err", i18n.Tf("cmd.secrets.err.missing", "name", `"DEEPSEEK_API_KEY"`,
				"where", i18n.T("cmd.secrets.where.both"), "path", `C:/Users/me/AppData/.../secrets.bin`))}
	}
	if os.Getenv("SHOT_SKIP_KEEP") != "" {
		// 现有配置还在、只是密钥取不到：按「稍后配置」跳过这一页的形态。
		f.accessSkipped = true
	}
	if os.Getenv("SHOT_SKIP_ACCESS") != "" {
		f.name.SetText("")
		f.baseURL.SetText("")
		f.models.SetText("")
		f.apiKey.SetText("")
		f.accessSkipped = true
	}

	q := new(input.Router)
	win, err := headless.NewWindow(w, h)
	if err != nil {
		t.Fatal(err)
	}
	for p := welcomePage(0); int(p) < welcomePageCount; p++ {
		if p == welcomeTerms {
			f.consent.Value = true
		}
		f.page = p
		f.refreshDoneIssues()
		var ops op.Ops
		welcomeFrame(layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerSp},
			Constraints: layout.Exact(image.Pt(w, h)),
			Source:      q.Source(),
		}, th, f)
		if err := win.Frame(&ops); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		if err := win.Screenshot(img); err != nil {
			t.Fatal(err)
		}
		out := fmt.Sprintf("%s_p%d_%s.png", prefix, int(p)+1, os.Getenv("SHOT_SUFFIX"))
		fh, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(fh, img); err != nil {
			t.Fatal(err)
		}
		fh.Close()
	}
}
