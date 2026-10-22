package uigui

// settings_provider_card_test.go provider 折叠卡片的**分组边界**回归（D176 口径二）。
//
// 复现过的故障：provider 卡片里的「＋ 添加密钥」「＋ 添加模型」与底部动作行
// （测试连通 / 设为主 / 删除）被判成「不落卡」，卡在这些行之前收边——同一个 provider
// 被切成两三张卡，按钮浮在卡外、谁也不属于（用户 2026-10-22 截图报障）。数据层与
// 行计划断言全绿，只有像素/分组口径能发现。

import (
	"testing"
)

// TestProviderRowsShareOneCard 同一家 provider 的所有行（字段、密钥池、模型表、**按钮行**）
// 必须落在同一张卡里；相邻两家不合并成一张。
func TestProviderRowsShareOneCard(t *testing.T) {
	mgr := &fakeProviderMgr{snap: []ProviderSnapshot{
		snapProv("a", "http://a/v1", true, "am1"),
		snapProv("b", "http://b/v1", false, "bm1"),
	}}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	f.provOpen = 0 // 展开第一张
	rows := f.pageRows(pageProviders)

	// 每家 provider 的行必须落在同一个组号里，且组号 ≠ -1（不落卡 = 被切出卡片）。
	seen := map[int]int{} // prov → 组号
	for _, r := range rows {
		if !provCardRow(r.kind) {
			continue
		}
		g := rowCardGroup(r)
		if g < 0 {
			t.Fatalf("provider %d 的 %v 行不落卡——同一家的卡片会被切成两张", r.prov, r.kind)
		}
		if prev, ok := seen[r.prov]; ok && prev != g {
			t.Fatalf("provider %d 的行分散在组 %d 与 %d——同一家应是一张卡", r.prov, prev, g)
		}
		seen[r.prov] = g
	}
	if len(seen) != 2 {
		t.Fatalf("provider 卡组 = %d 个, want 2（每家一张）", len(seen))
	}
	if seen[0] == seen[1] {
		t.Fatalf("两家 provider 组号相同（%d）——相邻卡片会被拼成一张", seen[0])
	}

	// 按钮行与它所在 provider 的字段行同组（这正是本次修的那一处）。
	var btnGroup, nameGroup = -1, -1
	for _, r := range rows {
		switch r.kind {
		case rowProvBtns:
			if r.prov == 0 {
				btnGroup = rowCardGroup(r)
			}
		case rowProvName:
			if r.prov == 0 {
				nameGroup = rowCardGroup(r)
			}
		}
	}
	if btnGroup < 0 || nameGroup < 0 {
		t.Fatalf("展开后应同时有 rowProvName 与 rowProvBtns（got name=%d btns=%d）", nameGroup, btnGroup)
	}
	if btnGroup != nameGroup {
		t.Fatalf("按钮行组 %d ≠ 字段行组 %d——按钮被切到卡片外", btnGroup, nameGroup)
	}

	// 页级动作（＋ 新增 provider / 应用变更）仍不落卡。
	for _, r := range rows {
		if r.kind == rowProvAdd || r.kind == rowProvApply {
			if g := rowCardGroup(r); g >= 0 {
				t.Fatalf("%v 是页级动作，不应落进 provider 卡（组 %d）", r.kind, g)
			}
		}
	}
}

// TestProviderCardClosesOnCollapse 收起态只有摘要行落卡，且摘要行自成一张卡（不把上一家
// 的按钮行续进来）。
func TestProviderCardClosesOnCollapse(t *testing.T) {
	mgr := &fakeProviderMgr{snap: []ProviderSnapshot{
		snapProv("a", "http://a/v1", true, "am1"),
		snapProv("b", "http://b/v1", false, "bm1"),
	}}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	rows := f.pageRows(pageProviders)

	var groups []int
	for _, r := range rows {
		if r.kind == rowProvSummary {
			groups = append(groups, rowCardGroup(r))
		}
	}
	if len(groups) != 2 {
		t.Fatalf("收起态摘要行 = %d, want 2", len(groups))
	}
	if groups[0] == groups[1] || groups[0] < 0 || groups[1] < 0 {
		t.Fatalf("两张摘要卡的组号 = %v——应收起成两张独立的卡", groups)
	}
}
