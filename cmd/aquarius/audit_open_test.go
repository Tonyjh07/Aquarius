package main

// 审计降级（§8「审计不得打断对话」）：audit.log 开不出来时启动照常，只留一行 warn。
//
// 这条以前是反的：openAudit 的错误直接进 fatal，双击启动就只剩一个「Access is denied」
// 消息框，主窗一闪而过。审计是旁路记录，丢的是事后可查性，不该换来进不去。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/logging"
)

// TestOpenAuditDegradesWhenFileUnwritable 用一个同名目录占住 audit.log，让开文件必然失败。
func TestOpenAuditDegradesWhenFileUnwritable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "audit.log"), 0o755); err != nil {
		t.Fatalf("占位目录建不出来：%v", err)
	}
	w := &wiring{profileDir: dir}
	w.openAudit()
	if w.audit != nil {
		t.Fatal("开不出来却拿到了审计句柄")
	}
	if len(w.pendingLog) != 1 {
		t.Fatalf("应该只排一条 warn，实际 %d 条", len(w.pendingLog))
	}
	p := w.pendingLog[0]
	if p.level != logging.LevelWarn {
		t.Fatalf("级别应为 warn，实际 %v", p.level)
	}
	if p.ctx != logging.Boot {
		t.Fatalf("上下文应为 boot，实际 %v", p.ctx)
	}
	if p.event != "log.boot.audit_unavailable" || p.action != "log.act.audit_off" {
		t.Fatalf("事件/动作不对：%q / %q", p.event, p.action)
	}
	if p.msg == "" {
		t.Fatal("报因应当留在 msg 里，才看得出为什么开不出来")
	}
	// 再调一次（stackDecorators 会补开）：同一次启动只报一遍，也不重新试。
	w.openAudit()
	if len(w.pendingLog) != 1 {
		t.Fatalf("补开不该再报一条，实际 %d 条", len(w.pendingLog))
	}
}

// TestOpenAuditOpensAndIsIdempotent 正常路径：拿到句柄，再调一次不重复建。
func TestOpenAuditOpensAndIsIdempotent(t *testing.T) {
	w := &wiring{profileDir: t.TempDir()}
	w.openAudit()
	if w.audit == nil {
		t.Fatal("应当拿到审计句柄")
	}
	first := w.audit
	w.openAudit()
	if w.audit != first {
		t.Fatal("第二次调用换了句柄：幂等被破坏")
	}
	if err := w.audit.Close(); err != nil {
		t.Fatalf("关闭失败：%v", err)
	}
}
