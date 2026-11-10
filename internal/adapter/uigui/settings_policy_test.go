package uigui

// 放行档位测试（D280/D282）：档位挂在「工具与 MCP」页的工具行上——行内分段直接落值，
// 执行类选「允许」先确认，外部代理只有整来源一条。

import (
	"errors"
	"testing"
)

// policyUI 搭一个停在工具页、带两三个内置工具的快照。
func policyUI(t *testing.T, builtins ...BuiltinToolState) (*UI, *settingsForm, *fakeToolsAdmin) {
	t.Helper()
	snap := ToolsSnapshot{Revision: 1, Level: "strict", Builtins: builtins}
	u, f, ta := toolsPageUI(t, snap)
	f.page = pageTools
	toolsFrame(f, u)
	return u, f, ta
}

// policyKinds 工具页行型序列（断言用）。
func policyKinds(f *settingsForm) []rowKind {
	var out []rowKind
	for _, r := range f.pageRows(pageTools) {
		out = append(out, r.kind)
	}
	return out
}

// hasKind 行型序列里是否出现某种行。
func hasKind(kinds []rowKind, k rowKind) bool {
	for _, x := range kinds {
		if x == k {
			return true
		}
	}
	return false
}

// segClick 点某一行分段控件的第 i 格（控件须已渲染过一帧）。
func segClick(t *testing.T, f *settingsForm, name string, i int) {
	t.Helper()
	seg := f.polSegFor(name)
	if seg == nil || i >= len(seg.btns) {
		t.Fatalf("%s 的分段控件未渲染到第 %d 格（已渲染 %d）", name, i, len(seg.btns))
	}
	seg.btns[i].Click()
}

// TestPolicySegmentsOnToolRows 档位分段就在工具行上，默认/询问/允许三格；外部代理与
// 硬名单说明在页尾。没有独立的策略页——页序契约里也不该再有 "policy"。
func TestPolicySegmentsOnToolRows(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	u, f, _ := policyUI(t,
		BuiltinToolState{Name: "file_write", Label: "写入文件", Enabled: true},
		BuiltinToolState{Name: "term_exec", Label: "执行命令", Enabled: true, Exec: true},
	)
	toolsFrame(f, u)
	seg := f.polSegFor("file_write")
	if len(seg.btns) != 3 {
		t.Fatalf("默认档位应有 3 格，实 %d", len(seg.btns))
	}
	kinds := policyKinds(f)
	for _, want := range []rowKind{rowBuiltin, rowToolsNote, rowToolsAgent, rowToolsHard} {
		if !hasKind(kinds, want) {
			t.Fatalf("缺行型 %v：%v", want, kinds)
		}
	}
	for _, page := range settingsPages {
		if page.Key() == "policy" {
			t.Fatalf("不该再有独立的策略页")
		}
	}
}

// TestPolicySegmentWrites 点「允许」→ 立即写回并刷新快照；点「默认」→ 清掉例外。
func TestPolicySegmentWrites(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	u, f, ta := policyUI(t, BuiltinToolState{Name: "file_write", Label: "写入文件", Enabled: true})
	segClick(t, f, "file_write", 2) // 允许
	toolsFrame(f, u)
	if got := ta.actionList(); len(got) != 1 || got[0] != "policy:file_write/allow" {
		t.Fatalf("动作 = %v", got)
	}
	if f.toolsSnap.Builtins[0].Policy != "allow" {
		t.Fatalf("快照档位 = %q", f.toolsSnap.Builtins[0].Policy)
	}
	if f.toolsStatus == "" || f.toolsErr {
		t.Fatalf("成功应落状态行：(%q, err=%v)", f.toolsStatus, f.toolsErr)
	}

	segClick(t, f, "file_write", 0) // 默认
	toolsFrame(f, u)
	if got := ta.actionList(); len(got) != 2 || got[1] != "policy:file_write/" {
		t.Fatalf("动作 = %v", got)
	}
	if f.toolsSnap.Builtins[0].Policy != "" {
		t.Fatalf("默认应收掉例外：%q", f.toolsSnap.Builtins[0].Policy)
	}
}

// TestPolicySegmentExecNeedsConfirm 执行类选「允许」：先立确认态、不写表；确认才写；
// 取消则保持原档（分段按快照画回旧值）。
func TestPolicySegmentExecNeedsConfirm(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	u, f, ta := policyUI(t, BuiltinToolState{Name: "term_exec", Label: "执行命令", Enabled: true, Exec: true})
	segClick(t, f, "term_exec", 2) // 允许
	toolsFrame(f, u)
	if f.polConfirm != "term_exec" {
		t.Fatalf("执行类选允许应先确认，confirm=%q", f.polConfirm)
	}
	if n := len(ta.actionList()); n != 0 {
		t.Fatalf("确认前不该写表，实 %d 次", n)
	}
	if !hasKind(policyKinds(f), rowToolsConfirm) {
		t.Fatalf("应有行内确认行")
	}

	// 取消：确认态清掉，档位不动。
	f.toolBtns["pol:no"].Click()
	toolsFrame(f, u)
	if f.polConfirm != "" || len(ta.actionList()) != 0 {
		t.Fatalf("取消后 confirm=%q actions=%v", f.polConfirm, ta.actionList())
	}

	// 再来一次并确认。
	segClick(t, f, "term_exec", 2)
	toolsFrame(f, u)
	f.toolBtns["pol:yes"].Click()
	toolsFrame(f, u)
	if got := ta.actionList(); len(got) != 1 || got[0] != "policy:term_exec/allow" {
		t.Fatalf("动作 = %v", got)
	}
	if f.polConfirm != "" {
		t.Fatalf("确认后应清掉确认态")
	}
}

// TestPolicySegmentAssistVisibleWhenSet 配置里已有 assist 的行多给一格「辅助」，否则
// 三格全不亮、看起来像没设过。
func TestPolicySegmentAssistVisibleWhenSet(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	u, f, _ := policyUI(t, BuiltinToolState{Name: "file_write", Label: "写入文件", Enabled: true, Policy: "assist"})
	toolsFrame(f, u)
	if n := len(f.polSegFor("file_write").btns); n != 4 {
		t.Fatalf("assist 行应有 4 格，实 %d", n)
	}
}

// TestPolicyAgentRowWrites 外部代理一行：点「允许」写整来源，空工具。
func TestPolicyAgentRowWrites(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	u, f, ta := policyUI(t, BuiltinToolState{Name: "file_write", Label: "写入文件", Enabled: true})
	segClick(t, f, "", 2) // 外部代理行的分段键是空串
	toolsFrame(f, u)
	got := ta.actionList()
	if len(got) != 1 || got[0] != "agentpolicy:/allow" {
		t.Fatalf("动作 = %v", got)
	}
	if f.toolsSnap.AgentPolicy != "allow" {
		t.Fatalf("快照 = %q", f.toolsSnap.AgentPolicy)
	}
}

// TestPolicyWriteErrorShowsStatus 写回失败：状态行标错，快照与分段保持原值。
func TestPolicyWriteErrorShowsStatus(t *testing.T) {
	t.Parallel()
	restoreLight(t)
	u, f, ta := policyUI(t, BuiltinToolState{Name: "file_write", Label: "写入文件", Enabled: true})
	ta.policyErr = errors.New("disk full")
	segClick(t, f, "file_write", 2)
	toolsFrame(f, u)
	if !f.toolsErr || f.toolsStatus == "" {
		t.Fatalf("失败应落错误状态行：(%q, %v)", f.toolsStatus, f.toolsErr)
	}
	if f.toolsSnap.Builtins[0].Policy != "" {
		t.Fatalf("失败后档位应保持默认：%q", f.toolsSnap.Builtins[0].Policy)
	}
}
