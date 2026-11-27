package decorate

// 审计句柄为 nil 时的安全面（§8）：装配层在 audit.log 开不出来时留的就是 nil，
// 调用方（LLM 装饰器、策略写入、判定记录）不该为此到处判空。

import (
	"errors"
	"testing"
)

func TestNilAuditIsSafe(t *testing.T) {
	var a *Audit
	if err := a.Close(); err != nil {
		t.Fatalf("nil 审计关闭应返回 nil，实际 %v", err)
	}
	// 下面三个入口都是生产路径上会走到的：策略写入、判定记录、单行追加。
	a.Policy("user", "write", "allow", "workdir", "write", "", true, "")
	a.Decide(DecideRecord{})
	if err := a.record(auditLine{Kind: "llm"}); !errors.Is(err, errAuditClosed) {
		t.Fatalf("nil 审计追加应报已关闭，实际 %v", err)
	}
}
