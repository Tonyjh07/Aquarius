package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestResultsAreASCIIModelFacing 工具回填文本进模型上下文，按硬规则用英文（D36/D171）。
//
// 与 spec_ascii_test.go 的区别：那一个扫声明（Spec 的字面量），这一个**真跑工具**再扫回填。
// 回填文本是运行时拼出来的（截断标注、空态、错误链、适配器包装），扫源码字面量抓不到；
// 只有执行过才知道模型真正读到什么。
func TestResultsAreASCIIModelFacing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	text := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(text, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(bin, []byte{1, 0, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	mem := newFakeMem()
	mem.docs[port.GlobalMemoryDoc] = "- a fact\n"
	tools := newAllTools(mem, nil)
	byName := map[string]port.Tool{}
	for _, tl := range tools {
		byName[tl.Spec().Name] = tl
	}

	cases := []struct {
		name string
		args string
	}{
		{"file_read", `{"path":` + js(text) + `}`},
		{"file_read", `{"path":` + js(bin) + `}`},
		{"file_read", `{"path":` + js(filepath.Join(dir, "missing.txt")) + `}`},
		{"file_read", `{"path":"relative.txt"}`},
		{"file_list", `{"path":` + js(dir) + `}`},
		{"file_list", `{"path":` + js(empty) + `}`},
		{"file_search", `{"path":` + js(dir) + `,"query":"note"}`},
		{"file_search", `{"path":` + js(dir) + `,"query":"nope"}`},
		{"file_write", `{"path":` + js(filepath.Join(dir, "out", "x.txt")) + `,"content":"x"}`},
		{"file_delete", `{"path":` + js(filepath.Join(dir, "out", "x.txt")) + `}`},
		{"file_delete", `{"path":` + js(filepath.Join(dir, "out", "x.txt")) + `}`},
		{"memory_list", `{}`},
		{"memory_read", `{"name":` + js(port.GlobalMemoryDoc) + `}`},
		{"memory_read", `{"name":"nope.md"}`},
		{"memory_search", `{"query":"fact"}`},
		{"memory_search", `{"query":"missing"}`},
		{"memory_write", `{"name":` + js(port.SessionMemoryDoc("s1")) + `,"content":"- x"}`},
		{"think", `{"text":"plan"}`},
		{"sleep", `{"seconds":0}`},
		{"job_start", `{}`},
		{"job_list", `{}`},
		{"job_status", `{"id":"j1"}`},
		{"job_logs", `{"id":"j1"}`},
		{"job_kill", `{"id":"j1"}`},
		{"term_exec", `{"command":"true"}`},
		{"term_exec", `{"command":""}`},
	}

	for _, tc := range cases {
		tl, ok := byName[tc.name]
		if !ok {
			t.Fatalf("%s not registered", tc.name)
		}
		res, err := tl.Execute(withSession(ctx, "s1"), call(tc.args))
		if err != nil {
			// 工具级失败：err 文本同样是模型回填（Runner 转成 OK=false）。
			checkASCII(t, err.Error(), tc.name+" error")
		}
		checkASCII(t, res.Output, tc.name+" output")
		checkASCII(t, res.Err, tc.name+" err")
	}
}

// withSession 给记忆工具注入当前会话（memory_* 只允许全局 + 当前会话两份）。
func withSession(ctx context.Context, id string) context.Context {
	return port.WithSessionID(ctx, conversation.ID(id))
}
