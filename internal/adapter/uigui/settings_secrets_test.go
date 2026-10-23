package uigui

// 「密钥库」页测试（S9-6b/D161）：条目行、入库校验、掩码、二次确认删除、导入旧密钥。

import (
	"errors"
	"image"
	"sort"
	"strings"
	"sync"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// fakeSecretsAdmin SecretsAdmin 测试替身：内存键值表 + Revision 递增 + 动作记录。
// 语义对齐 secretbin：Put 空值/空名报错、Delete 不存在报错、快照按名字排序。
type fakeSecretsAdmin struct {
	mu        sync.Mutex
	snap      SecretsSnapshot
	rev       int64
	store     map[string]string
	actions   []string
	importRes SecretsImportResult
	importErr error
}

func (a *fakeSecretsAdmin) Snapshot() SecretsSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.snap
	s.Revision = a.rev
	return s
}

func (a *fakeSecretsAdmin) Put(name, value string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, "put:"+name)
	if strings.TrimSpace(name) == "" {
		return errors.New("密钥名不可为空")
	}
	if strings.TrimSpace(value) == "" {
		return errors.New("值不可为空")
	}
	if a.store == nil {
		a.store = map[string]string{}
	}
	a.store[name] = value
	a.rebuild()
	return nil
}

func (a *fakeSecretsAdmin) Delete(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, "delete:"+name)
	if _, ok := a.store[name]; !ok {
		return errors.New("密钥库中已没有同名条目")
	}
	delete(a.store, name)
	a.rebuild()
	return nil
}

// Import 一键导入的测试替身：按预设的 importRes / importErr 返回（真口径见
// cmd/aquarius 侧；本层只关心动作分派、状态行与「值不回显」）。
func (a *fakeSecretsAdmin) Import() (SecretsImportResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, "import")
	return a.importRes, a.importErr
}

// rebuild 从 store 重建快照条目（按名字排序，掩码只出尾 4 位；与 secretbin 口径一致）。
func (a *fakeSecretsAdmin) rebuild() {
	names := make([]string, 0, len(a.store))
	for n := range a.store {
		names = append(names, n)
	}
	sort.Strings(names)
	a.snap.Entries = a.snap.Entries[:0]
	for _, n := range names {
		v := []rune(a.store[n])
		mask := strings.Repeat("•", 6)
		if len(v) >= 8 {
			mask += string(v[len(v)-4:])
		}
		a.snap.Entries = append(a.snap.Entries, SecretEntry{Name: n, Mask: mask})
	}
	a.rev++
}

func (a *fakeSecretsAdmin) actionList() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.actions...)
}

// secretsPageUI 搭一个带假 SecretsAdmin 的 headless 设置窗。
func secretsPageUI(t *testing.T, snap SecretsSnapshot, store map[string]string) (*UI, *settingsForm, *fakeSecretsAdmin) {
	t.Helper()
	sa := &fakeSecretsAdmin{snap: snap, store: store}
	if store == nil {
		sa.store = map[string]string{}
	}
	// Revision 起点 0 会被 newSettingsForm 当成「未变」而跳过——带初始条目的用例把
	// rev 抬到 1，开窗即采纳快照（帧首同款语义）。
	if len(snap.Entries) > 0 {
		sa.rev = 1
	}
	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot { return fillSnapshot() },
		Secrets:  sa,
	})
	f := newSettingsForm(u)
	return u, f, sa
}

// TestSecretsPageRows 密钥库页行计划：有条目时每有一条 → 一行（名字），再加新增区。
func TestSecretsPageRows(t *testing.T) {
	u, f, _ := secretsPageUI(t, SecretsSnapshot{Entries: []SecretEntry{
		{Name: "AQUARIUS_OPENAI_API_KEY", Mask: "••••••1234"},
		{Name: "MCP_DOCS_TOKEN", Mask: "••••••"},
	}}, nil)
	_ = u
	f.secretsSnap = f.secrets.Snapshot() // 铺快照（帧首同款）
	var names []string
	for _, r := range f.pageRows(pageSecrets) {
		if r.kind == rowSecretEntry {
			names = append(names, r.text)
		}
	}
	if len(names) != 2 || names[0] != "AQUARIUS_OPENAI_API_KEY" || names[1] != "MCP_DOCS_TOKEN" {
		t.Fatalf("条目行 = %v, want 两条按名排", names)
	}
}

// TestSecretsPageHasNoImportEntry 导入入口已从界面撤下（D206：功能保留、不给入口）：
// 行计划里既不该再有「从旧处收进来」分区，也不该有导入按钮。功能本身见
// TestSecretsPageImport（动作分支保留、照常可用）。
func TestSecretsPageHasNoImportEntry(t *testing.T) {
	_, f, _ := secretsPageUI(t, SecretsSnapshot{Entries: []SecretEntry{
		{Name: "AQUARIUS_OPENAI_API_KEY", Mask: "••••••1234"},
	}}, nil)
	f.secretsSnap = f.secrets.Snapshot()
	for _, r := range f.pageRows(pageSecrets) {
		if r.kind == rowSection && strings.Contains(r.text, "旧") {
			t.Fatalf("密钥库页仍有导入分区: %q", r.text)
		}
		if strings.Contains(r.text, "导入") {
			t.Fatalf("密钥库页仍有导入相关行: %q", r.text)
		}
	}
}

// TestSecretsPagePut 新增/覆盖：填入键名与值 → 存入 → 假管理面记录 + 状态行含名字 +
// **值字段恒清空**（值永不回显）。
func TestSecretsPagePut(t *testing.T) {
	restoreLight(t)
	u, f, sa := secretsPageUI(t, SecretsSnapshot{}, nil)
	f.secretName.SetText("AQUARIUS_OPENAI_API_KEY")
	f.secretValue.SetText("sk-secret-value-1234")
	u.secretsAction(f, "put", "")

	if got := sa.actionList(); len(got) != 1 || got[0] != "put:AQUARIUS_OPENAI_API_KEY" {
		t.Fatalf("动作 = %v", got)
	}
	if f.secretsErr || !strings.Contains(f.secretsStatus, "AQUARIUS_OPENAI_API_KEY") {
		t.Fatalf("反馈 = (%v, %q)", f.secretsErr, f.secretsStatus)
	}
	if f.secretsStatus == "" || strings.Contains(f.secretsStatus, "sk-secret-value-1234") {
		t.Fatalf("状态行不得回显值：%q", f.secretsStatus)
	}
	if f.secretValue.Text() != "" {
		t.Fatalf("值字段应恒清空，实为 %q", f.secretValue.Text())
	}
	if f.secretName.Text() != "" {
		t.Fatalf("成功后键名应清空，实为 %q", f.secretName.Text())
	}
	// 快照已刷新（Revision 变了 → 条目行出现）。
	if len(f.secretsSnap.Entries) != 1 || f.secretsSnap.Entries[0].Name != "AQUARIUS_OPENAI_API_KEY" {
		t.Fatalf("快照未刷新: %+v", f.secretsSnap)
	}
}

// TestSecretsPagePutValidation 空名/空值拦截：不触达管理面，值字段仍清空（不留在框里）。
func TestSecretsPagePutValidation(t *testing.T) {
	restoreLight(t)
	cases := []struct{ name, value, wantMsg string }{
		{"", "sk-x-1234", "密钥名"},
		{"KEY", "", "密钥值"},
	}
	for _, c := range cases {
		u, f, sa := secretsPageUI(t, SecretsSnapshot{}, nil)
		f.secretName.SetText(c.name)
		f.secretValue.SetText(c.value)
		u.secretsAction(f, "put", "")
		if !f.secretsErr || !strings.Contains(f.secretsStatus, c.wantMsg) {
			t.Fatalf("%q/%q: 反馈 = (%v, %q), want 含 %q",
				c.name, c.value, f.secretsErr, f.secretsStatus, c.wantMsg)
		}
		if n := len(sa.actionList()); n != 0 {
			t.Fatalf("%q/%q: 不应触达管理面，实 %d 次", c.name, c.value, n)
		}
		if f.secretValue.Text() != "" {
			t.Fatalf("校验失败也应清空值字段：%q", f.secretValue.Text())
		}
	}
}

// TestSecretsPageMask 掩码口径：只出尾 4 位；值短于 8 位整条打码（长值不可整条暴露）。
func TestSecretsPageMask(t *testing.T) {
	_, _, sa := secretsPageUI(t, SecretsSnapshot{}, nil)
	if err := sa.Put("LONG", "sk-abcdefgh-9876"); err != nil {
		t.Fatal(err)
	}
	if err := sa.Put("SHORT", "abc"); err != nil {
		t.Fatal(err)
	}
	snap := sa.Snapshot()
	byName := map[string]string{}
	for _, e := range snap.Entries {
		byName[e.Name] = e.Mask
	}
	if got := byName["LONG"]; got != "••••••9876" {
		t.Fatalf("长值掩码 = %q, want ••••••9876", got)
	}
	if got := byName["SHORT"]; got != "••••••" {
		t.Fatalf("短值掩码 = %q, want 整条打码", got)
	}
	if strings.Contains(byName["LONG"], "abcdefgh") {
		t.Fatalf("掩码泄漏了中段值：%q", byName["LONG"])
	}
}

// TestSecretsPageDeleteNeedsConfirm 删除走行内二次确认：点「删除」只进确认态（不触达）；
// 点「取消」退回；点「确认删除」才真删。
func TestSecretsPageDeleteNeedsConfirm(t *testing.T) {
	restoreLight(t)
	u, f, sa := secretsPageUI(t, SecretsSnapshot{Entries: []SecretEntry{{Name: "K1", Mask: "••••••1234"}}}, map[string]string{"K1": "sk-1234abcd"})
	f.syncSecretsSnapshot() // 帧首同款：铺条目行与按钮
	th := testTheme()
	frame := func() {
		var ops op.Ops
		settingsFrame(layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(720, 560)),
		}, th, u, f)
	}
	f.page = pageSecrets
	frame()
	if f.secretBtns["del:K1"] == nil {
		t.Fatalf("删除按钮未渲染（已渲染 %d 个）", len(f.secretBtns))
	}

	// ① 点「删除」→ 进确认态，不触达管理面。
	f.secretBtns["del:K1"].Click()
	frame()
	if f.secretConfirm != "K1" {
		t.Fatalf("点删除后 secretConfirm = %q, want K1", f.secretConfirm)
	}
	if n := len(sa.actionList()); n != 0 {
		t.Fatalf("二次确认前不应删除，实 %d 次", n)
	}

	// ② 点「取消」→ 退出确认态。
	f.secretBtns["cancel:K1"].Click()
	frame()
	if f.secretConfirm != "" {
		t.Fatalf("取消后 secretConfirm = %q, want 空", f.secretConfirm)
	}
	if n := len(sa.actionList()); n != 0 {
		t.Fatalf("取消不应删除，实 %d 次", n)
	}

	// ③ 再点删除 → 确认删除 → 真删 + 快照条目消失。
	f.secretBtns["del:K1"].Click()
	frame()
	f.secretBtns["confirm:K1"].Click()
	frame()
	if got := sa.actionList(); len(got) != 1 || got[0] != "delete:K1" {
		t.Fatalf("确认删除动作 = %v", got)
	}
	if f.secretsErr || !strings.Contains(f.secretsStatus, "K1") {
		t.Fatalf("删除反馈 = (%v, %q)", f.secretsErr, f.secretsStatus)
	}
	if len(f.secretsSnap.Entries) != 0 {
		t.Fatalf("删除后快照条目 = %+v, want 空", f.secretsSnap.Entries)
	}
}

// TestSecretsPageConfirmClearedOnSnapshot 条目在别处被删（Revision 变化且条目消失）→
// 行内二次确认目标被清掉，免得确认按钮指着一条已不在的条目。
func TestSecretsPageConfirmClearedOnSnapshot(t *testing.T) {
	u, f, sa := secretsPageUI(t, SecretsSnapshot{Entries: []SecretEntry{{Name: "K1", Mask: "••••••1234"}}}, map[string]string{"K1": "sk-1234abcd"})
	f.syncSecretsSnapshot()
	f.secretConfirm = "K1"
	if err := sa.Delete("K1"); err != nil { // 外部删除（Revision 递增且 K1 消失）
		t.Fatal(err)
	}
	f.syncSecretsSnapshot()
	if f.secretConfirm != "" {
		t.Fatalf("条目消失后 secretConfirm = %q, want 空", f.secretConfirm)
	}
	_ = u
}

// TestSecretsPageImport 一键导入：动作分派到管理面，状态行只报条数、**不含值**；
// 失败项以错误态呈现。
func TestSecretsPageImport(t *testing.T) {
	restoreLight(t)
	u, f, sa := secretsPageUI(t, SecretsSnapshot{}, nil)
	sa.importRes = SecretsImportResult{
		Imported:  []string{"AQUARIUS_OPENAI_API_KEY", "MCP_DOCS_TOKEN"},
		Rewritten: []string{"AQUARIUS_OPENAI_API_KEY"},
		Skipped:   []string{"AQ_NOT_SET"},
	}
	u.secretsAction(f, "import", "")
	if got := sa.actionList(); len(got) != 1 || got[0] != "import" {
		t.Fatalf("动作 = %v", got)
	}
	if f.importErr {
		t.Fatalf("无失败项不应是错误态: %q", f.importStatus)
	}
	// 反馈只报条数（名字在上方列表里），故断言条数 + 改写说明，不列键名。
	for _, want := range []string{"已导入 2 条", "1 条配置里的明文已改为 secret: 引用", "1 条两处都取不到"} {
		if !strings.Contains(f.importStatus, want) {
			t.Fatalf("状态行应含 %q: %q", want, f.importStatus)
		}
	}
	if strings.Contains(f.importStatus, "sk-") {
		t.Fatalf("状态行不得回显值: %q", f.importStatus)
	}

	// 失败项 → 错误态（红字），并点名失败说明。
	sa.importRes = SecretsImportResult{Failed: []string{`provider "a" 写入密钥库失败：磁盘满`}}
	u.secretsAction(f, "import", "")
	if !f.importErr || !strings.Contains(f.importStatus, "磁盘满") {
		t.Fatalf("失败项反馈 = (%v, %q)", f.importErr, f.importStatus)
	}

	// 管理面报错（如库解不开被拒）→ 错误态。
	sa.importErr = errors.New("本机密钥库解不开")
	u.secretsAction(f, "import", "")
	if !f.importErr || !strings.Contains(f.importStatus, "导入失败") {
		t.Fatalf("拒绝导入反馈 = (%v, %q)", f.importErr, f.importStatus)
	}
}

// TestSecretsPageImportNoSource 没有可导入的来源：状态行如实说明（不是错误态）。
func TestSecretsPageImportNoSource(t *testing.T) {
	restoreLight(t)
	u, f, _ := secretsPageUI(t, SecretsSnapshot{}, nil)
	u.secretsAction(f, "import", "")
	if f.importErr || !strings.Contains(f.importStatus, "没有需要导入") {
		t.Fatalf("空导入反馈 = (%v, %q)", f.importErr, f.importStatus)
	}
}

// TestSecretsPageNotWired 未接线（Secrets = nil）：只读占位 + 存入按钮禁用，动作报错。
func TestSecretsPageNotWired(t *testing.T) {
	restoreLight(t)
	u := newHeadless(t, Options{Settings: func() SettingsSnapshot { return fillSnapshot() }})
	f := newSettingsForm(u)
	rows := f.pageRows(pageSecrets)
	found := false
	for _, r := range rows {
		if r.kind == rowSecretEmpty {
			found = true
		}
	}
	if len(rows) < 2 || rows[0].kind != rowTitle || !found {
		t.Fatalf("未接线页行计划 = %+v, want 标题 + 占位", rows)
	}
	f.secretName.SetText("X")
	f.secretValue.SetText("sk-val-1234")
	u.secretsAction(f, "put", "")
	if !f.secretsErr || !strings.Contains(f.secretsStatus, "未接线") {
		t.Fatalf("未接线反馈 = (%v, %q)", f.secretsErr, f.secretsStatus)
	}
	// 「导入旧密钥」同样只置只读反馈（未接线时不触达管理面）。
	u.secretsAction(f, "import", "")
	if !f.importErr || !strings.Contains(f.importStatus, "未接线") {
		t.Fatalf("未接线导入反馈 = (%v, %q)", f.importErr, f.importStatus)
	}
}
