package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// TestModelTextFileIsASCII model_text.go 集中了「进模型上下文的内置文案」，按硬规则
// 一律英文（D36）。这里用 go/parser 逐字面量断言 ASCII，而不是逐个引用常量：
// 新增一条文案不用改测试，且注释/标识符不受影响（只查 StringLit）。
//
// 中文一旦漏进上下文不会有行为差异，只有门禁能拦住它。
func TestModelTextFileIsASCII(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "model_text.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse model_text.go: %v", err)
	}
	n := 0
	ast.Inspect(f, func(node ast.Node) bool {
		lit, ok := node.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, uerr := strconv.Unquote(lit.Value)
		if uerr != nil {
			t.Fatalf("%s: unquote %s: %v", fset.Position(lit.Pos()), lit.Value, uerr)
		}
		n++
		for i := 0; i < len(s); i++ {
			if c := s[i]; c < 0x20 || c > 0x7e {
				if c == '\n' || c == '\t' {
					continue
				}
				t.Fatalf("%s 含非 ASCII 字节 0x%02x（%q）：模型面文案一律英文",
					fset.Position(lit.Pos()), c, s)
			}
		}
		return true
	})
	if n == 0 {
		t.Fatal("model_text.go 未解析到任何字符串字面量：文件名/结构变了？")
	}
}

// TestToolResultTextBranches 工具回填各分支的文案取自 model_text.go（防手写漂移）。
func TestToolResultTextBranches(t *testing.T) {
	if got := toolResultText(nil); got != toolNoOutput {
		t.Fatalf("nil result = %q, want %q", got, toolNoOutput)
	}
	if got := toolResultText(&tool.Result{CallID: "c", OK: true}); got != toolNoOutput {
		t.Fatalf("empty ok = %q, want %q", got, toolNoOutput)
	}
	if got := toolResultText(&tool.Result{CallID: "c"}); got != toolFailedPrefix+toolFailedNoReason {
		t.Fatalf("empty failure = %q", got)
	}
	if got := toolResultText(&tool.Result{CallID: "c", OK: false, Err: "boom", Output: "tail"}); got != toolFailedPrefix+"boom\ntail" {
		t.Fatalf("failure with output = %q", got)
	}
}
