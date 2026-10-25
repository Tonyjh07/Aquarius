package uigui

// overlay_hint_test.go 编辑遮罩的「应用方式后果说明」：三档各自说清后果（旧分支留不留、
// 后续挂到谁下面），无后续内容时说明三档等价，只读节点没有说明。

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
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
		name  string
		ov    *overlayState
		want  []string
		avoid []string
	}{
		{"另起新分支：旧分支原样保留", editable(conversation.Fresh),
			[]string{"留在原分支", "视图移到新版本"}, []string{"副本", "无子叶", "重新生成"}},
		{"转移历史：旧节点留成无子叶", editable(conversation.Carry),
			[]string{"无子叶", "改挂到新版本"}, []string{"副本", "重新生成"}},
		{"复制历史：旧分支不动、新版本带副本", editable(conversation.Clone),
			[]string{"副本", "原样保留"}, []string{"无子叶", "重新生成"}},
		{"用户消息另起分支会重新生成回答", freshUser,
			[]string{"重新生成回答"}, []string{"无子叶", "副本"}},
		{"无后续内容时三档等价", leaf,
			[]string{"三种方式结果相同"}, []string{"无子叶", "副本"}},
		{"只读节点没有方式可选", readOnly, nil, []string{"分支", "子叶", "副本"}},
		{"模板页签谈的是人格模板", personaTpl, nil, []string{"分支", "子叶", "副本"}},
		{"人格锁转移历史并说明代价", &overlayState{persona: true, editable: true, hasChildren: true,
			mode: conversation.Carry},
			[]string{"固定「转移历史」", "无子叶"}, []string{"副本"}},
	} {
		got := tc.ov.modeHint()
		if len(tc.want) == 0 && got != "" {
			t.Fatalf("%s: hint = %q, want 空", tc.name, got)
		}
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Fatalf("%s: hint = %q, want 含 %q", tc.name, got, w)
			}
		}
		for _, a := range tc.avoid {
			if strings.Contains(got, a) {
				t.Fatalf("%s: hint = %q, 不应含 %q", tc.name, got, a)
			}
		}
	}
}
