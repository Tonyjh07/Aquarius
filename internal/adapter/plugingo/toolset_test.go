package plugingo

import (
	"context"
	"strings"
	"testing"

	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// nameTools 只声明工具表、不关心执行的假插件：用来量「声明入口」的校验。
type nameTools struct{ specs []v1.ToolSpec }

func (n *nameTools) Tools(context.Context) []v1.ToolSpec { return n.specs }
func (n *nameTools) Execute(context.Context, v1.ToolCall) (v1.ToolResult, error) {
	return v1.ToolResult{OK: true}, nil
}

// TestToolSetEmptyIsFine 声明了 tool 但这一轮一件都没有：空库、游标停在根都会这样，
// 是合法状态而不是拒载理由（D340 之前这条会被状态机当实现缺陷拒掉）。
func TestToolSetEmptyIsFine(t *testing.T) {
	set := NewToolSet("empty", &nameTools{})
	specs, err := set.Specs(context.Background())
	if err != nil {
		t.Fatalf("零工具不该报错：%v", err)
	}
	if len(specs) != 0 {
		t.Fatalf("工具数=%d", len(specs))
	}
	if tool, err := set.Lookup(context.Background(), "nope"); tool != nil || err != nil {
		t.Fatalf("不认识的工具应回 (nil, nil)，得到 (%v, %v)", tool, err)
	}
}

// TestToolSetRejectsBadNames 名字不合文法整份拒掉：模型端点不认识的名字会让整个请求
// 400，早点失败报因好读。
func TestToolSetRejectsBadNames(t *testing.T) {
	cases := []struct {
		name  string
		tools []v1.ToolSpec
		want  string
	}{
		{"中文标题直接当名字", []v1.ToolSpec{{Name: "部署脚本的坑"}}, "部署脚本的坑"},
		{"带空格", []v1.ToolSpec{{Name: "mem 01"}}, "mem 01"},
		{"超长", []v1.ToolSpec{{Name: strings.Repeat("a", v1.ToolNameMaxLen+1)}}, "aaaa"},
		{"空名", []v1.ToolSpec{{Name: ""}}, "插件"},
		{"同一轮重名", []v1.ToolSpec{{Name: "mem_1"}, {Name: "mem_1"}}, "mem_1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := NewToolSet("demo", &nameTools{specs: c.tools})
			_, err := set.Specs(context.Background())
			if err == nil {
				t.Fatal("该拒的没拒")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("报因该带上「%s」：%v", c.want, err)
			}
		})
	}
}

// TestToolSetNamesArePerTurn 清单按轮给：同一个源在不同轮次可以给出不同的名字，
// 上一轮的名字这一轮就不再是它（「标题即工具」的下钻语义）。
func TestToolSetNamesArePerTurn(t *testing.T) {
	p := &nameTools{specs: []v1.ToolSpec{{Name: "mem_1"}, {Name: "mem_2"}}}
	set := NewToolSet("demo", p)

	first, err := set.Specs(context.Background())
	if err != nil || len(first) != 2 {
		t.Fatalf("第一轮：%v, %d", err, len(first))
	}

	p.specs = []v1.ToolSpec{{Name: "mem_3"}}
	second, err := set.Specs(context.Background())
	if err != nil || len(second) != 1 || second[0].Name != "mem_3" {
		t.Fatalf("第二轮没跟上新清单：%v, %+v", err, second)
	}
	if tool, err := set.Lookup(context.Background(), "mem_1"); tool != nil || err != nil {
		t.Fatalf("上一轮的名字这一轮不该再查到：%v, %v", tool, err)
	}
}
