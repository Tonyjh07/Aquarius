package uigui

// overlay_hint_test.go 编辑遮罩的「应用方式后果说明」：三档各自说清后果（旧分支留不留、
// 后续挂到谁下面），无后续内容时说明三档等价，只读节点没有说明。

import (
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

func TestOverlayModeHint(t *testing.T) {
	editable := func(mode conversation.KeepMode) *overlayState {
		return &overlayState{
			editable:    true,
			hasChildren: true,
			role:        conversation.RoleAssistant,
			mode:        mode,
		}
	}
	freshUser := editable(conversation.Fresh)
	freshUser.role = conversation.RoleUser
	leaf := &overlayState{editable: true, role: conversation.RoleAssistant, mode: conversation.Carry}
	readOnly := &overlayState{role: conversation.RoleAssistant}
	personaTpl := &overlayState{persona: true, editable: true, hasChildren: true,
		page: overlayPageTemplate, mode: conversation.Carry}

	for _, tc := range []struct {
		name string
		ov   *overlayState
		key  string // 期望取自哪条说明 key；空 = 不显示说明
	}{
		{"另起新分支：旧分支原样保留", editable(conversation.Fresh), "ui.overlay.hint.fresh_other"},
		{"转移历史：旧节点留成无子叶", editable(conversation.Carry), "ui.overlay.hint.carry"},
		{"复制历史：旧分支不动、新版本带副本", editable(conversation.Clone), "ui.overlay.hint.clone"},
		{"用户消息另起分支会重新生成回答", freshUser, "ui.overlay.hint.fresh_user"},
		{"无后续内容时三档等价", leaf, "ui.overlay.hint.no_children"},
		{"只读节点没有方式可选", readOnly, ""},
		{"模板页签谈的是人格模板", personaTpl, ""},
		{"人格锁转移历史并说明代价", &overlayState{persona: true, editable: true, hasChildren: true,
			mode: conversation.Carry}, "ui.overlay.hint.persona"},
	} {
		// 每档说明是**一条**表内文案：整串比对，既钉住选哪条 key，也钉住不是别的档串了进来。
		want := ""
		if tc.key != "" {
			want = i18n.T(tc.key)
		}
		if got := tc.ov.modeHint(); got != want {
			t.Fatalf("%s: hint = %q, want %q（key %q）", tc.name, got, want, tc.key)
		}
	}
}
