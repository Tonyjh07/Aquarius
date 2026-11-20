package conversation

// serverpart_test.go 服务端工具块的形态约束（D310）
//
// 两条必须一直成立：只有 assistant 能携带（assistant 节点记的是「这一轮读了什么」），且原文不能空
// （空壳回传等于没读过，问题要到下一轮才浮出来）。

import (
	"strings"
	"testing"
)

func TestCheckNodeShapeServerPart(t *testing.T) {
	ok := &ServerPart{Tool: "deepseek_read_url", Kind: "read_link", Raw: []byte(`{"type":"web_fetch_tool_result"}`)}

	// 放在 assistant 上合法。
	if err := checkNodeShape(Message{
		ID: "a1", Parent: "root", Role: RoleAssistant,
		Content: []Part{{Kind: PartServer, Server: ok}},
	}); err != nil {
		t.Fatalf("assistant 携带应合法：%v", err)
	}

	// 放在 user 上非法。
	err := checkNodeShape(Message{
		ID: "u1", Parent: "root", Role: RoleUser,
		Content: []Part{{Kind: PartServer, Server: ok}},
	})
	if err == nil || !strings.Contains(err.Error(), "server part only allowed") {
		t.Fatalf("validate = %v, want server part only allowed", err)
	}

	// 载荷缺失。
	err = checkNodeShape(Message{
		ID: "a1", Parent: "root", Role: RoleAssistant,
		Content: []Part{{Kind: PartServer}},
	})
	if err == nil || !strings.Contains(err.Error(), "missing payload") {
		t.Fatalf("validate = %v, want missing payload", err)
	}

	// 原文为空。
	err = checkNodeShape(Message{
		ID: "a1", Parent: "root", Role: RoleAssistant,
		Content: []Part{{Kind: PartServer, Server: &ServerPart{Tool: "deepseek_read_url"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "missing raw block") {
		t.Fatalf("validate = %v, want missing raw block", err)
	}
}
