package uigui

// settings_about_test.go 「关于」页（D208）：行计划、版本文案、项目主页两条动作
// （打开走平台、复制走剪贴板）、失败反馈与图标加载。全走无窗口路径（§15.5）。

import (
	"errors"
	"image"
	"strings"
	"testing"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// aboutPageUI 关于页测试台：装假平台 + 指定注入版本号的表单。
func aboutPageUI(t *testing.T, version string) (*UI, *settingsForm) {
	t.Helper()
	u := newHeadless(t, Options{Version: version})
	f := newSettingsForm(u)
	f.page = pageAbout
	return u, f
}

// aboutFrame 单帧渲染设置窗并提交 hit 树（剪贴板命令由 Router 收集）。
func aboutFrame(q *input.Router, u *UI, f *settingsForm, ops *op.Ops) {
	settingsFrame(layout.Context{
		Ops:         ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(720, 560)),
		Source:      q.Source(),
	}, testTheme(), u, f)
	q.Frame(ops)
}

// TestAboutPageRows 行计划：身份区 → 构建 → 项目 → 版权；状态行只在有反馈时出现。
func TestAboutPageRows(t *testing.T) {
	restoreLight(t)
	u, f := aboutPageUI(t, "v0.1.0")
	_ = u

	rows := f.pageRows(pageAbout)
	if rows[0].kind != rowTitle || rows[0].text != "关于" {
		t.Fatalf("首行 = %+v, want 页标题「关于」", rows[0])
	}
	want := []rowKind{rowTitle, rowAboutHero, rowSection, rowAboutVersion, rowAboutRuntime,
		rowSection, rowAboutProject, rowSection, rowAboutCopyright, rowAboutLicense, rowAboutNote}
	if len(rows) != len(want) {
		t.Fatalf("行数 = %d, want %d（%+v）", len(rows), len(want), rows)
	}
	for i, k := range want {
		if rows[i].kind != k {
			t.Errorf("行 %d = %v, want %v", i, rows[i].kind, k)
		}
	}
	for _, r := range rows {
		if r.kind == rowAboutStatus {
			t.Fatal("无反馈时不应出现状态行")
		}
	}

	// 有反馈 → 末尾多一条状态行；清空 → 行计划恢复。
	f.aboutStatus = "链接已复制"
	rows = f.pageRows(pageAbout)
	if rows[len(rows)-1].kind != rowAboutStatus {
		t.Fatalf("末行 = %v, want rowAboutStatus", rows[len(rows)-1].kind)
	}
	f.aboutStatus = ""
	if got := len(f.pageRows(pageAbout)); got != len(want) {
		t.Fatalf("清空反馈后行数 = %d, want %d", got, len(want))
	}

	// 身份区与说明/状态行不落卡（不是表单行），字段行落卡。
	for _, k := range []rowKind{rowAboutHero, rowAboutNote, rowAboutStatus} {
		if rowInCard(k) {
			t.Errorf("行型 %v 不应落卡", k)
		}
	}
	for _, k := range []rowKind{rowAboutVersion, rowAboutRuntime, rowAboutProject, rowAboutCopyright, rowAboutLicense} {
		if !rowInCard(k) {
			t.Errorf("行型 %v 应落卡", k)
		}
	}
}

// TestAboutVersionText 版本文案：注入值原样展示；空 / dev 明说开发构建。
func TestAboutVersionText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"v0.1.0", "v0.1.0"},
		{"  v0.1.0  ", "v0.1.0"},
		{"", "开发构建（未注入版本号）"},
		{"dev", "开发构建（未注入版本号）"},
	}
	for _, c := range cases {
		if got := aboutVersionText(c.in); got != c.want {
			t.Errorf("aboutVersionText(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// 运行环境：平台 + Go 版本（两段都在，缺一段说明取值写错）。
	got := aboutRuntimeText()
	for _, part := range []string{"/", " · ", "go"} {
		if !strings.Contains(got, part) {
			t.Errorf("运行环境 = %q，缺 %q", got, part)
		}
	}

	// 表单从 Options.Version 取值（装配根注入点）。
	_, f := aboutPageUI(t, "v0.9.9")
	if f.aboutVersion != "v0.9.9" {
		t.Fatalf("表单版本 = %q, want v0.9.9", f.aboutVersion)
	}
	_, f = aboutPageUI(t, "")
	if got := aboutVersionText(f.aboutVersion); got != "开发构建（未注入版本号）" {
		t.Fatalf("无注入时 = %q", got)
	}
}

// TestAboutPageOpenProject 点「打开项目主页」→ 平台按正确 URL 打开；失败落红字反馈。
func TestAboutPageOpenProject(t *testing.T) {
	restoreLight(t)
	u, f := aboutPageUI(t, "v0.1.0")
	fp := fakeOf(u)
	var ops op.Ops
	aboutFrame(new(input.Router), u, f, &ops) // 首帧建立按钮

	f.aboutOpen.Click()
	ops = op.Ops{}
	aboutFrame(new(input.Router), u, f, &ops)
	if len(fp.OpenURLs) != 1 || fp.OpenURLs[0] != aboutProjectURL {
		t.Fatalf("打开链接 = %v, want [%s]", fp.OpenURLs, aboutProjectURL)
	}
	if f.aboutErr || !strings.Contains(f.aboutStatus, "已在浏览器") {
		t.Fatalf("成功反馈 = (%v, %q)", f.aboutErr, f.aboutStatus)
	}

	// 失败路径：原因就地显示（用户能读懂 + 提示改走复制）。
	fp.OpenURLErr = errors.New("没有默认浏览器")
	f.aboutOpen.Click()
	ops = op.Ops{}
	aboutFrame(new(input.Router), u, f, &ops)
	if !f.aboutErr {
		t.Fatal("打开失败应标记错误态")
	}
	if !strings.Contains(f.aboutStatus, "没有默认浏览器") || !strings.Contains(f.aboutStatus, "复制链接") {
		t.Fatalf("失败反馈 = %q，want 含原因与替代办法", f.aboutStatus)
	}
}

// TestAboutPageCopyLink 点「复制链接」→ 帧内写剪贴板 + 状态行反馈。
func TestAboutPageCopyLink(t *testing.T) {
	restoreLight(t)
	u, f := aboutPageUI(t, "v0.1.0")
	q := new(input.Router)
	var ops op.Ops
	aboutFrame(q, u, f, &ops)

	f.aboutCopy.Click()
	ops = op.Ops{}
	aboutFrame(q, u, f, &ops)

	_, content, ok := q.WriteClipboard()
	if !ok {
		t.Fatal("复制链接应写剪贴板")
	}
	if got := strings.TrimSpace(string(content)); got != aboutProjectURL {
		t.Fatalf("剪贴板 = %q, want %q", got, aboutProjectURL)
	}
	if f.aboutErr || f.aboutStatus != "链接已复制" {
		t.Fatalf("复制反馈 = (%v, %q)", f.aboutErr, f.aboutStatus)
	}
}

// TestAboutPageIcon 图标按目标像素取预缩放图并缓存一档；非法尺寸不取图。
func TestAboutPageIcon(t *testing.T) {
	var c aboutIconCache
	if _, ok := aboutIconOp(&c, 0); ok {
		t.Fatal("边长 0 不应取图")
	}
	if _, ok := aboutIconOp(nil, 64); ok {
		t.Fatal("无缓存槽时不应取图")
	}
	if _, ok := aboutIconOp(&c, 72); !ok {
		t.Fatal("内嵌图标应可解码")
	}
	if err := func() error {
		first, _ := aboutIconOp(&c, 72)
		second, _ := aboutIconOp(&c, 72)
		if first != second {
			return errors.New("同尺寸应命中缓存（每帧新建 ImageOp 会让纹理键每帧变化）")
		}
		third, _ := aboutIconOp(&c, 48)
		if third == first {
			return errors.New("换尺寸应重建")
		}
		return nil
	}(); err != nil {
		t.Fatal(err)
	}
	if c.px != 48 {
		t.Fatalf("缓存边长 = %d, want 48（最近一档）", c.px)
	}
}

// TestAboutPageHasNoEditors 关于页没有编辑档：开窗焦点不落到本页。
func TestAboutPageHasNoEditors(t *testing.T) {
	_, f := aboutPageUI(t, "v0.1.0")
	if ed := (aboutPage{}).FirstEditor(f); ed != nil {
		t.Fatal("关于页不应有编辑档（开窗不抢焦点）")
	}
}
