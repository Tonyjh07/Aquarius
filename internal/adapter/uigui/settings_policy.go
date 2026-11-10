package uigui

// 放行档位的界面侧（D280/D282）：档位表、执行类确认态、写回入口。
//
// 档位没有独立页面：它挂在「工具与 MCP」页每一行上（一个三段控件），因为用户要回答的
// 问题就是「这个工具允不允许自动跑」，与「它开不开」是同一条行的事。外部代理同理，
// 只多一行「全部请求」——ACP 的 fs/terminal 回调没有稳定的工具名。
//
// 界面只读 ToolsSnapshot（档位随工具一起下发），写回走 ToolsAdmin；判定仍在
// domain/perm，这里不做任何规则判断。

import (
	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// policyModeValues 档位键值（顺序 = 从严到宽）；首项 "" = 默认（跟随权限档）。
func policyModeValues() []string { return []string{"", "ask", "allow"} }

// policyModeLabels 档位展示名。**必须是函数**：语言在 main 里才定（同 effortLevels）。
//
// 标签只取两个字：设置窗最小 620dp 宽，四字标签（「始终允许」）在英文下会把工具名挤
// 到折行。完整语义由分区说明交代（「允许」= 以后不再询问）。
func policyModeLabels() []string {
	return []string{
		i18n.T("ui.page.policy.seg.default"),
		i18n.T("ui.page.policy.seg.ask"),
		i18n.T("ui.page.policy.seg.allow"),
	}
}

// policySegments 某一行当前的档位选项：默认 / 询问 / 允许。
//
// 已写成 `assist` 的行（手改配置，或 P4 落地决策模型之后设过）要多给一格，否则三项
// 全不亮，读起来像「没设过」。这一格只在值本来就是 assist 时出现，不构成入口。
func policySegments(cur string) (values, labels []string) {
	values, labels = policyModeValues(), policyModeLabels()
	if cur != "assist" {
		return values, labels
	}
	values = []string{"", "ask", "assist", "allow"}
	labels = []string{
		labels[0], labels[1],
		i18n.T("ui.page.policy.mode.assist_short"),
		labels[2],
	}
	return values, labels
}

// policyIndex 当前档位在选项里的下标（找不到 → 0 = 默认）。
func policyIndex(values []string, cur string) int {
	for i, v := range values {
		if v == cur {
			return i
		}
	}
	return 0
}

// polSegFor 某一行（工具名 / 外部代理用空串）的分段控件 get-or-create。
//
// 控件按地址缓存：换实例 = 丢掉用户正按下的那次点击（同工具页按钮口径）。
func (f *settingsForm) polSegFor(name string) *segmented {
	seg := f.polSegs[name]
	if seg == nil {
		seg = new(segmented)
		f.polSegs[name] = seg
	}
	return seg
}

// applyToolPolicy 写一个内置工具的档位（"" = 回默认）并刷新快照。
//
// 同步执行：写回只是一小段 JSON + 一次内存替换（与密钥库页同款），用户点了就该当场
// 看到结果，走异步反而要多一帧滞后。失败落状态行，快照不动。
func (f *settingsForm) applyToolPolicy(name, mode string) {
	if f.tools == nil {
		return
	}
	if err := f.tools.SetToolPolicy(name, mode); err != nil {
		f.toolsStatus, f.toolsErr = i18n.Tf("ui.common.failed", "err", err.Error()), true
		return
	}
	f.toolsStatus, f.toolsErr = i18n.T("ui.page.policy.updated"), false
	f.syncToolsSnapshot()
}

// applyAgentPolicy 写外部代理整来源的档位（"" = 回默认）。
func (f *settingsForm) applyAgentPolicy(mode string) {
	if f.tools == nil {
		return
	}
	if err := f.tools.SetAgentPolicy(mode); err != nil {
		f.toolsStatus, f.toolsErr = i18n.Tf("ui.common.failed", "err", err.Error()), true
		return
	}
	f.toolsStatus, f.toolsErr = i18n.T("ui.page.policy.updated"), false
	f.syncToolsSnapshot()
}

// polExec 该工具是否执行类（读快照标记，不在这里另写一份名单）。
func (f *settingsForm) polExec(name string) bool {
	for _, b := range f.toolsSnap.Builtins {
		if b.Name == name {
			return b.Exec
		}
	}
	return false
}

// polConfirmButtons 执行类确认按钮（get-or-create，与工具页动作按钮同一张表）。
func (f *settingsForm) polConfirmButtons() (*widget.Clickable, *widget.Clickable) {
	yes, no := f.toolBtns["pol:yes"], f.toolBtns["pol:no"]
	if yes == nil {
		yes = new(widget.Clickable)
		f.toolBtns["pol:yes"] = yes
	}
	if no == nil {
		no = new(widget.Clickable)
		f.toolBtns["pol:no"] = no
	}
	return yes, no
}
