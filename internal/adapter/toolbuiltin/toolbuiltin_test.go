package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeMem 内存记忆替身（port.MemoryStore 契约内行为：按名寻址、缺失报哨兵错误）。
type fakeMem struct{ docs map[string]string }

var _ port.MemoryStore = (*fakeMem)(nil)

func newFakeMem() *fakeMem { return &fakeMem{docs: map[string]string{}} }

func (m *fakeMem) Index(context.Context) ([]port.MemoryIndexEntry, error) {
	var out []port.MemoryIndexEntry
	for name, content := range m.docs {
		if strings.TrimSpace(content) == "" {
			continue
		}
		out = append(out, port.MemoryIndexEntry{Name: name, Summary: strings.Split(content, "\n")[0]})
	}
	return out, nil
}

func (m *fakeMem) Read(_ context.Context, name string) (port.MemoryDoc, error) {
	c, ok := m.docs[name]
	if !ok {
		return port.MemoryDoc{}, port.ErrMemoryNotFound
	}
	return port.MemoryDoc{Name: name, Content: c}, nil
}

func (m *fakeMem) Write(_ context.Context, doc port.MemoryDoc) error {
	m.docs[doc.Name] = doc.Content
	return nil
}

func (m *fakeMem) Remove(_ context.Context, name string) error {
	if _, ok := m.docs[name]; !ok {
		return port.ErrMemoryNotFound
	}
	delete(m.docs, name)
	return nil
}

func (m *fakeMem) Search(_ context.Context, q string) ([]port.MemoryHit, error) {
	var out []port.MemoryHit
	ql := strings.ToLower(q)
	for name, content := range m.docs {
		for i, line := range strings.Split(content, "\n") {
			if strings.Contains(strings.ToLower(line), ql) {
				out = append(out, port.MemoryHit{Name: name, Line: i + 1, Snippet: line})
			}
		}
	}
	return out, nil
}

// call 构造工具调用。
func call(args string) tool.Call {
	return tool.Call{ID: tool.CallID("c1"), Args: json.RawMessage(args)}
}

// sessCtx 注入会话 ID 的执行上下文。
func sessCtx(id string) context.Context {
	return port.WithSessionID(context.Background(), conversation.ID(id))
}

// pick 按名字取工具。
func pick(t *testing.T, tools []port.Tool, name string) port.Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Spec().Name == name {
			return tl
		}
	}
	t.Fatalf("工具 %s 未注册", name)
	return nil
}

// TestNewRegistersAllTools 注册集合与 Risk 声明对齐 DESIGN §4.3。
func TestNewRegistersAllTools(t *testing.T) {
	tools := New(newFakeMem(), nil)
	want := map[string]tool.Risk{
		"memory_list": tool.Safe, "memory_read": tool.Safe, "memory_search": tool.Safe,
		"memory_write": tool.Confirm,
		"file_read":    tool.Safe, "file_list": tool.Safe, "file_search": tool.Safe,
		"file_write": tool.Confirm, "file_delete": tool.Confirm,
		"think": tool.Safe,
	}
	if len(tools) != len(want) {
		t.Fatalf("tools = %d, want %d", len(tools), len(want))
	}
	seen := map[string]bool{}
	for _, tl := range tools {
		s := tl.Spec()
		if seen[s.Name] {
			t.Fatalf("重名工具 %s", s.Name)
		}
		seen[s.Name] = true
		r, ok := want[s.Name]
		if !ok {
			t.Fatalf("未知工具 %s", s.Name)
		}
		if s.Risk != r {
			t.Fatalf("%s risk = %v, want %v", s.Name, s.Risk, r)
		}
		if len(s.Schema) == 0 || s.Description == "" {
			t.Fatalf("%s 缺 description/schema", s.Name)
		}
	}
}

// TestMemoryScope 记忆工具只碰全局 + 当前会话两份（DESIGN §4.4）。
func TestMemoryScope(t *testing.T) {
	mem := newFakeMem()
	tools := New(mem, nil)
	ctx := sessCtx("conv1")
	other := port.SessionMemoryDoc("conv2")

	// 写其他会话记忆：Target 拒、Execute 报错。
	w := pick(t, tools, "memory_write")
	if _, _, ok := w.(port.FileTarget).Target(ctx, call(`{"name":"`+other+`","content":"x"}`)); ok {
		t.Fatal("Target 不应申报其他会话的记忆")
	}
	if _, err := w.Execute(ctx, call(`{"name":"`+other+`","content":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "只能访问") {
		t.Fatalf("err = %v, want 会话隔离", err)
	}
	if _, err := w.Execute(context.Background(), call(`{"name":"`+port.SessionMemoryDoc("conv1")+`","content":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "无会话上下文") {
		t.Fatalf("无会话上下文 err = %v", err)
	}

	// 全局写（append 缺省）与会话写。
	if _, err := w.Execute(ctx, call(`{"name":"memories.md","content":"喜欢简洁"}`)); err != nil {
		t.Fatalf("全局写: %v", err)
	}
	sname := port.SessionMemoryDoc("conv1")
	if _, err := w.Execute(ctx, call(`{"name":"`+sname+`","content":"会话要点"}`)); err != nil {
		t.Fatalf("会话写: %v", err)
	}
	if mem.docs[port.GlobalMemoryDoc] != "喜欢简洁" || mem.docs[sname] != "会话要点" {
		t.Fatalf("docs = %+v", mem.docs)
	}

	// append 拼接 / overwrite 覆盖 / 未知 mode 报错。
	if _, err := w.Execute(ctx, call(`{"name":"memories.md","content":"第二条"}`)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if mem.docs[port.GlobalMemoryDoc] != "喜欢简洁\n第二条" {
		t.Fatalf("append 结果 = %q", mem.docs[port.GlobalMemoryDoc])
	}
	if _, err := w.Execute(ctx, call(`{"name":"memories.md","content":"重写","mode":"overwrite"}`)); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if mem.docs[port.GlobalMemoryDoc] != "重写" {
		t.Fatalf("overwrite 结果 = %q", mem.docs[port.GlobalMemoryDoc])
	}
	if _, err := w.Execute(ctx, call(`{"name":"memories.md","content":"x","mode":"merge"}`)); err == nil {
		t.Fatal("未知 mode 应报错")
	}
	if _, err := w.Execute(ctx, call(`{"name":"memories.md"}`)); err == nil ||
		!strings.Contains(err.Error(), "content 不可为空") {
		t.Fatalf("空 content err = %v", err)
	}

	// list 只显示白名单两份（其它会话记忆即使存在也不可见）。
	mem.docs[other] = "别会话"
	l := pick(t, tools, "memory_list")
	res, err := l.Execute(ctx, call(""))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "conv2") {
		t.Fatalf("list 泄漏其他会话: %q", res.Output)
	}
	if !strings.Contains(res.Output, port.GlobalMemoryDoc) || !strings.Contains(res.Output, sname) {
		t.Fatalf("list 缺白名单项: %q", res.Output)
	}

	// read / search 同样受白名单约束。
	r := pick(t, tools, "memory_read")
	if _, err := r.Execute(ctx, call(`{"name":"`+other+`"}`)); err == nil {
		t.Fatal("read 其他会话应报错")
	}
	if res, err := r.Execute(ctx, call(`{"name":"`+port.GlobalMemoryDoc+`"}`)); err != nil || res.Output != "重写" {
		t.Fatalf("read = %q, %v", res.Output, err)
	}
	s := pick(t, tools, "memory_search")
	res, err = s.Execute(ctx, call(`{"query":"别"}`))
	if err != nil || strings.Contains(res.Output, "conv2") {
		t.Fatalf("search 泄漏: %q, %v", res.Output, err)
	}
	res, err = s.Execute(ctx, call(`{"query":"要点"}`))
	if err != nil || !strings.Contains(res.Output, sname+":1:") {
		t.Fatalf("search 缺命中: %q, %v", res.Output, err)
	}

	// memory_write 申报的 FileTarget = 写 + 解析出的磁盘路径。
	pathOf := func(name string) (string, bool) { return filepath.Join("D", name), true }
	wt := pick(t, New(mem, pathOf), "memory_write").(port.FileTarget)
	p, op, ok := wt.Target(ctx, call(`{"name":"memories.md","content":"x"}`))
	if !ok || op.String() != "write" || filepath.Base(p) != port.GlobalMemoryDoc {
		t.Fatalf("Target = %q/%v/%v", p, op, ok)
	}
}

// TestMemoryFileTargetWithoutPathOf 未注入路径解析时不申报（回退执行类判定）。
func TestMemoryFileTargetWithoutPathOf(t *testing.T) {
	w := pick(t, New(newFakeMem(), nil), "memory_write").(port.FileTarget)
	if _, _, ok := w.Target(sessCtx("c"), call(`{"name":"memories.md","content":"x"}`)); ok {
		t.Fatal("pathOf 为 nil 不应申报")
	}
}

// TestFileTools 文件工具：绝对路径强制、读写删、二进制拒绝、搜索。
func TestFileTools(t *testing.T) {
	dir := t.TempDir()
	tools := New(newFakeMem(), nil)
	ctx := context.Background()

	// 相对路径一律拒绝（无工作区概念）。
	for _, name := range []string{"file_read", "file_list", "file_write", "file_delete"} {
		tl := pick(t, tools, name)
		args := `{"path":"rel/x.txt"}`
		if name == "file_write" {
			args = `{"path":"rel/x.txt","content":"c"}`
		}
		if _, err := tl.Execute(ctx, call(args)); err == nil ||
			!strings.Contains(err.Error(), "绝对路径") {
			t.Fatalf("%s 相对路径 err = %v", name, err)
		}
		if _, _, ok := tl.(port.FileTarget).Target(ctx, call(args)); ok {
			t.Fatalf("%s Target 不应申报相对路径", name)
		}
	}

	// write → read 往返（父目录自动创建、原子换入无 .tmp 残留）。
	target := filepath.Join(dir, "sub", "a.txt")
	w := pick(t, tools, "file_write")
	if _, err := w.Execute(ctx, call(`{"path":`+js(target)+`,"content":"你好 world"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("文件未落盘: %v", err)
	}
	if m, _ := filepath.Glob(target + "*"); len(m) > 1 {
		t.Fatalf("残留临时文件: %v", m)
	}
	r := pick(t, tools, "file_read")
	res, err := r.Execute(ctx, call(`{"path":`+js(target)+`}`))
	if err != nil || res.Output != "你好 world" {
		t.Fatalf("read = %q, %v", res.Output, err)
	}
	// 申报：读/写分开。
	if _, op, ok := r.(port.FileTarget).Target(ctx, call(`{"path":`+js(target)+`}`)); !ok || op.String() != "read" {
		t.Fatalf("read Target = %v/%v", op, ok)
	}
	if _, op, ok := w.(port.FileTarget).Target(ctx, call(`{"path":`+js(target)+`,"content":"x"}`)); !ok || op.String() != "write" {
		t.Fatalf("write Target = %v/%v", op, ok)
	}

	// 二进制拒绝。
	bin := filepath.Join(dir, "b.bin")
	if err := os.WriteFile(bin, []byte{1, 2, 0, 4}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(ctx, call(`{"path":`+js(bin)+`}`)); err == nil ||
		!strings.Contains(err.Error(), "二进制") {
		t.Fatalf("binary err = %v", err)
	}

	// list：目录名带 /，空目录提示。
	l := pick(t, tools, "file_list")
	res, err = l.Execute(ctx, call(`{"path":`+js(dir)+`}`))
	if err != nil || !strings.Contains(res.Output, "sub/") || !strings.Contains(res.Output, "b.bin") {
		t.Fatalf("list = %q, %v", res.Output, err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if res, err = l.Execute(ctx, call(`{"path":`+js(empty)+`}`)); err != nil || res.Output != "（空目录）" {
		t.Fatalf("empty list = %q, %v", res.Output, err)
	}

	// search：大小写不敏感、命中绝对路径、缺 query 报错。
	s := pick(t, tools, "file_search")
	res, err = s.Execute(ctx, call(`{"path":`+js(dir)+`,"query":"A.TXT"}`))
	if err != nil || !strings.Contains(res.Output, target) {
		t.Fatalf("search = %q, %v", res.Output, err)
	}
	if _, err := s.Execute(ctx, call(`{"path":`+js(dir)+`,"query":""}`)); err == nil {
		t.Fatal("空 query 应报错")
	}

	// delete：删文件成功、删不存在报错。
	d := pick(t, tools, "file_delete")
	if _, err := d.Execute(ctx, call(`{"path":`+js(target)+`}`)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := d.Execute(ctx, call(`{"path":`+js(target)+`}`)); err == nil {
		t.Fatal("重复删除应报错")
	}
	// 非空目录拒绝。
	if _, err := d.Execute(ctx, call(`{"path":`+js(dir)+`}`)); err == nil {
		t.Fatal("非空目录删除应报错")
	}
}

// TestReadMissingFile 不存在文件的报错可读。
func TestReadMissingFile(t *testing.T) {
	tools := New(newFakeMem(), nil)
	r := pick(t, tools, "file_read")
	p := filepath.Join(t.TempDir(), "nope.txt")
	if _, err := r.Execute(context.Background(), call(`{"path":`+js(p)+`}`)); err == nil {
		t.Fatal("缺失文件应报错")
	}
}

// TestThinkNoOp think 为 Safe no-op。
func TestThinkNoOp(t *testing.T) {
	th := pick(t, New(newFakeMem(), nil), "think")
	if th.Spec().Risk != tool.Safe {
		t.Fatalf("risk = %v", th.Spec().Risk)
	}
	res, err := th.Execute(context.Background(), call(`{"thought":"先想清楚再答"}`))
	if err != nil || !res.OK {
		t.Fatalf("think = %+v, %v", res, err)
	}
}

// TestBadJSONArgs 非法参数统一报 JSON 错误。
func TestBadJSONArgs(t *testing.T) {
	tools := New(newFakeMem(), nil)
	bad := tool.Call{ID: "c", Args: json.RawMessage(`not-json`)}
	for _, name := range []string{"memory_read", "memory_write", "file_read", "think"} {
		if _, err := pick(t, tools, name).Execute(context.Background(), bad); err == nil {
			t.Fatalf("%s 非法 JSON 应报错", name)
		}
	}
}

// js JSON 字符串字面量（路径入参）。
func js(s string) string { b, _ := json.Marshal(s); return string(b) }
