package uigui

// settings_datadir_test.go 「删除数据目录」入口：行计划、未接线禁用、确认词校验与
// 标记后的状态（真删除在装配根退出链，cmd/aquarius 侧测）。headless 驱动同密钥库页。

import (
	"errors"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// fakeDataDirAdmin DataDirAdmin 测试替身：动作计数 + 可注入失败。
type fakeDataDirAdmin struct {
	path    string
	wipeN   int
	wipeErr error
}

func (a *fakeDataDirAdmin) Path() string { return a.path }
func (a *fakeDataDirAdmin) RequestWipe() error {
	a.wipeN++
	return a.wipeErr
}

// dataWipeUI 搭一个带假 DataDirAdmin 的 headless 设置窗。
func dataWipeUI(t *testing.T, admin DataDirAdmin) (*UI, *settingsForm, *fakeDataDirAdmin) {
	t.Helper()
	fake := &fakeDataDirAdmin{path: "/tmp/data"}
	if admin != nil {
		fake = admin.(*fakeDataDirAdmin)
	}
	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot { return fillSnapshot() },
		DataDir:  fake,
	})
	f := newSettingsForm(u)
	return u, f, fake
}

func TestGeneralPageHasDataWipeRow(t *testing.T) {
	t.Parallel()
	_, f, _ := dataWipeUI(t, nil)
	var has bool
	for _, r := range f.pageRows(pageGeneral) {
		if r.kind == rowDataWipe {
			has = true
		}
	}
	if !has {
		t.Fatal("常规页缺「删除数据目录」行")
	}
}

func TestDataWipeUnwiredDisabled(t *testing.T) {
	t.Parallel()
	// 未接线：按钮禁用（enable 位随 dataDir nil 为 false）；说明文案照常给路径空位。
	u := newHeadless(t, Options{Settings: func() SettingsSnapshot { return fillSnapshot() }})
	f := newSettingsForm(u)
	if f.dataDir != nil {
		t.Fatal("未接线时 dataDir 应为 nil")
	}
	_ = u
}

func TestDataWipeConfirmFlow(t *testing.T) {
	t.Parallel()
	u, f, admin := dataWipeUI(t, &fakeDataDirAdmin{path: "/tmp/data"})

	// arm → 展开确认区。
	if u.dataWipeAction(f, "arm") {
		t.Fatal("arm 不应触发删除")
	}
	if !f.wipeArmed {
		t.Fatal("arm 后应处展开态")
	}

	// 确认词不对（空 / 小写 / 多余空白内容）→ 不标记，报错文案点出确认词。
	f.wipeInput.SetText("")
	if u.dataWipeAction(f, "confirm") || admin.wipeN != 0 {
		t.Fatal("空确认词不应放行")
	}
	if !f.wipeErr || f.wipeStatus != i18n.Tf("ui.page.general.data_wipe_mismatch", "word", wipeConfirmWord) {
		t.Fatalf("空确认词应报不匹配：%q", f.wipeStatus)
	}
	f.wipeInput.SetText("delete")
	if u.dataWipeAction(f, "confirm") || admin.wipeN != 0 {
		t.Fatal("小写不应放行（确认词区分大小写）")
	}

	// 输入 DELETE → 标记成功，展开态收起、报「已确认」。
	f.wipeInput.SetText("DELETE")
	if !u.dataWipeAction(f, "confirm") {
		t.Fatal("确认词正确应放行")
	}
	if admin.wipeN != 1 || f.wipeArmed {
		t.Fatalf("wipeN=%d armed=%v, want 1/false", admin.wipeN, f.wipeArmed)
	}
	if f.wipeErr || f.wipeStatus != i18n.T("ui.page.general.data_wipe_ok") {
		t.Fatalf("确认后状态 = %q", f.wipeStatus)
	}

	// cancel 收起清空状态。
	f.wipeArmed = true
	f.wipeInput.SetText("DELETE")
	if u.dataWipeAction(f, "cancel") {
		t.Fatal("cancel 不应触发删除")
	}
	if f.wipeArmed || f.wipeInput.Text() != "" || f.wipeStatus != "" {
		t.Fatalf("cancel 应收起清空：armed=%v input=%q status=%q",
			f.wipeArmed, f.wipeInput.Text(), f.wipeStatus)
	}
}

func TestDataWipeRequestFailureSurfaced(t *testing.T) {
	t.Parallel()
	u, f, _ := dataWipeUI(t, &fakeDataDirAdmin{path: "/tmp/data", wipeErr: errors.New("boom")})
	f.wipeArmed = true
	f.wipeInput.SetText(wipeConfirmWord)
	if u.dataWipeAction(f, "confirm") {
		t.Fatal("标记失败不应放行")
	}
	if !f.wipeErr || f.wipeStatus == "" {
		t.Fatalf("失败应进错误态：%q", f.wipeStatus)
	}
}
