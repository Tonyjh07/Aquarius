package uicommon

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 表格要真的对齐：每条数据行的显示宽等于表头行（CJK 列宽记 2，不然第二列起全歪）。
func TestPlainNodeTableAlignsCJK(t *testing.T) {
	n := port.UINode{
		Kind:    port.UITable,
		Columns: []string{"层", "标题", "次数"},
		Rows: [][]string{
			{"1", "部署脚本放在哪", "3"},
			{"7", "memo", "12"},
		},
	}
	out := PlainNode(n, 80)
	lines := strings.Split(out, "\n")
	if len(lines) != 4 { // 表头 + 分隔 + 两行
		t.Fatalf("行数不对：%q", out)
	}
	want := TextWidth(lines[0])
	for i, ln := range lines {
		if i == 1 {
			continue // 分隔线
		}
		if got := TextWidth(ln); got != want {
			t.Errorf("第 %d 行宽 %d，表头宽 %d：%q", i, got, want, ln)
		}
	}
	if !strings.Contains(lines[0], "次数") || !strings.Contains(lines[2], "部署脚本放在哪") {
		t.Errorf("内容缺：%q", out)
	}
}

func TestPlainNodeKV(t *testing.T) {
	out := PlainNode(port.UINode{Kind: port.UIKV, Items: []string{"层", "3", "回忆", "7", "孤立"}}, 0)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("三对（最后一对只有名字）：%q", out)
	}
	if !strings.Contains(lines[0], "层") || !strings.Contains(lines[0], "3") {
		t.Errorf("第一对：%q", lines[0])
	}
	if strings.Contains(lines[2], "孤立") == false {
		t.Errorf("落单的名字要留着：%q", lines[2])
	}
}

func TestPlainNodeList(t *testing.T) {
	out := PlainNode(port.UINode{Kind: port.UIList, Items: []string{"甲", "乙"}}, 0)
	if out != "  - 甲\n  - 乙" {
		t.Fatalf("列表形状：%q", out)
	}
}

// 表单：开关说 on/off，下拉把可选值列出来（行式界面看不到控件形状），说明缩进一行。
func TestPlainNodeForm(t *testing.T) {
	n := port.UINode{Kind: port.UIForm, Fields: []port.UIFormField{
		{Key: "on", Label: "启用", Kind: "switch", Value: "true"},
		{Key: "mode", Label: "模式", Kind: "select", Value: "fast", Values: []string{"fast", "slow"}},
		{Key: "note", Label: "备注", Kind: "text", Value: "abc", Help: "只在这个会话里生效"},
	}}
	out := PlainNode(n, 80)
	for _, want := range []string{"启用  on", "fast  (fast / slow)", "备注  abc", "只在这个会话里生效"} {
		if !strings.Contains(out, want) {
			t.Errorf("缺 %q：%s", want, out)
		}
	}
}

func TestPlainNodeButtonsAndChildren(t *testing.T) {
	n := port.UINode{
		Kind:    port.UIButtons,
		Actions: []port.UIAction{{ID: "a", Label: "刷新"}, {ID: "b", Label: "清空"}},
		Children: []port.UINode{
			{Kind: port.UIText, Text: "下面是说明"},
		},
	}
	out := PlainNode(n, 0)
	if !strings.Contains(out, "[1] 刷新") || !strings.Contains(out, "[2] 清空") {
		t.Errorf("按钮编号：%q", out)
	}
	if !strings.Contains(out, "下面是说明") {
		t.Errorf("子节点没渲染：%q", out)
	}
}

// 未知种类不吞内容：文本与子节点都要出来（前端比插件旧时不至于一片空白）。
func TestPlainNodeUnknownKindKeepsContent(t *testing.T) {
	n := port.UINode{Kind: port.UIKind("chart"), Text: "一份图", Children: []port.UINode{
		{Kind: port.UIText, Text: "子项"},
	}}
	out := PlainNode(n, 0)
	if !strings.Contains(out, "一份图") || !strings.Contains(out, "子项") {
		t.Fatalf("未知种类丢了内容：%q", out)
	}
}

func TestTextWidthAndPad(t *testing.T) {
	if got := TextWidth("ab中文"); got != 6 {
		t.Errorf("CJK 记 2：%d", got)
	}
	if got := PadRight("中", 4); got != "中  " {
		t.Errorf("补空格按显示宽：%q", got)
	}
	if got := PadRight("已经超宽了", 2); got != "已经超宽了" {
		t.Errorf("已超宽原样返回：%q", got)
	}
}
