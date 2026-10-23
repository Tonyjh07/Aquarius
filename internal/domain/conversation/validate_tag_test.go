package conversation

import "testing"

// TestValidateEveryChangeCatchesBrokenTree 证明自检开关不是摆设：
// 在 tag 构建下手动把树弄坏（删掉一条孩子边），checkInvariants 必须报错。
//
// 只在 `-tags aqvalidate` 下有意义——生产构建里 ValidateEveryChange 是常量 false，
// 这条测试会被跳过，正好也验证了「生产零开销」。
func TestValidateEveryChangeCatchesBrokenTree(t *testing.T) {
	if !ValidateEveryChange {
		t.Skip("未开 aqvalidate tag：生产构建恒为 false（这正是要验证的零开销路径）")
	}
	c := buildTree(t)
	root := MessageID(c.ID)
	kids := c.ChildIDs(root)
	if len(kids) == 0 {
		t.Fatal("造树失败：根没有孩子")
	}
	// 弄坏：把 Children 里的孩子边删掉，但节点仍在 Nodes 里。
	delete(c.Children, root)

	if err := c.checkInvariants("手动破坏"); err == nil {
		t.Fatal("树已不合法（孩子边缺失），自检却放行")
	}
}
