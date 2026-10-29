package main

// main_memory_test.go 记忆与编辑器：脚本流原始数据构造、编辑器挑选与创建、
// 写记忆的确认拦截、编辑器子进程替身、/memory 命令。

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/memoryfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// rawScriptServer 按序回话的 SSE 服务：第 i 次请求逐行回放 dataLines[i]（可含 tool_calls）。
func rawScriptServer(t *testing.T, dataLines [][]string) (*httptest.Server, *reqLog) {
	t.Helper()
	reqs := &reqLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		i := reqs.add(recordedReq{body: body})
		if i >= len(dataLines) {
			t.Errorf("第 %d 次请求超出脚本", i+1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range dataLines[i] {
			fmt.Fprint(w, line+"\n")
		}
		fmt.Fprint(w, "data: [DONE]\n")
	}))
	t.Cleanup(srv.Close)
	return srv, reqs
}

// contentData 纯文本回复的 data 行。
func contentData(text string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"delta": map[string]any{"content": text}}},
	})
	return "data: " + string(b)
}

// toolCallData 工具调用回复的 data 行（单分片完整参数）。
func toolCallData(id, name, args string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"delta": map[string]any{
			"tool_calls": []map[string]any{{
				"index": 0, "id": id, "type": "function",
				"function": map[string]any{"name": name, "arguments": args},
			}},
		}}},
	})
	return "data: " + string(b)
}

// TestPickEditor 编辑器选择（D24）：$VISUAL → $EDITOR → 平台默认。
func TestPickEditor(t *testing.T) {
	cases := []struct {
		name                 string
		visual, editor, goos string
		want                 []string
	}{
		{"VISUAL 优先且可带参数", "code -w", "vi", "windows", []string{"code", "-w"}},
		{"EDITOR 兜底", "  ", "emacs -nw", "linux", []string{"emacs", "-nw"}},
		{"windows 默认记事本", "", "", "windows", []string{"notepad"}},
		{"unix 默认 vi", "", "", "linux", []string{"vi"}},
		{"空白 VISUAL 落到 EDITOR", "   ", "nano", "darwin", []string{"nano"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickEditor(tc.visual, tc.editor, tc.goos)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("pickEditor = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestOpenMemoryEditorCreatesFile 打开前确保文件存在（全局/会话路径，D23 布局）；
// 编辑器启动失败报因但文件已建；非法文档名拒绝。
func TestOpenMemoryEditorCreatesFile(t *testing.T) {
	dir := t.TempDir()
	mem, err := memoryfs.New(filepath.Join(dir, port.GlobalMemoryDoc), filepath.Join(dir, "conversations"))
	if err != nil {
		t.Fatalf("memoryfs: %v", err)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "aquarius-no-such-editor-xyz") // 必失败的编辑器：断言文件创建与报错
	open := openMemoryEditor(mem, nil, strings.NewReader(""), io.Discard, io.Discard)

	if _, err := open(port.GlobalMemoryDoc); err == nil || !strings.Contains(err.Error(), "运行编辑器") {
		t.Fatalf("err = %v, want 运行编辑器失败", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, port.GlobalMemoryDoc)); serr != nil {
		t.Fatalf("全局记忆文件应已创建: %v", serr)
	}

	sname := port.SessionMemoryDoc("convX")
	if _, err := open(sname); err == nil {
		t.Fatal("会话记忆也应报编辑器失败")
	}
	if _, serr := os.Stat(filepath.Join(dir, "conversations", sname)); serr != nil {
		t.Fatalf("会话记忆文件应已创建: %v", serr)
	}

	if _, err := open("../evil.md"); err == nil {
		t.Fatal("非法文档名应报错")
	}
}

// writeConfigLevel 带指定权限等级的 config。
func writeConfigLevel(t *testing.T, dir, baseURL, name, level string) {
	t.Helper()
	cfg := fmt.Sprintf(`{
  "model": {"providers":[{"name":"openai-compatible","base_url":%q,"keys":[{"api_key":"secret:AQ_E2E_KEY"}],"models":[{"name":%q}]}],"name":%q},
  "ui": {"kind":"repl"},
  "permissions": {"level": %q},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, baseURL, name, name, level)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("AQ_E2E_KEY", "test-key")
}

// loadTree 读取唯一会话树并做不变量自检（D110：数据落 profiles/default/）。
func loadTree(t *testing.T, dir string) *conversation.Conversation {
	t.Helper()
	return loadStored(t, dir)
}

// findToolPart 找到首个工具分片（D95：调用+结果内嵌 assistant 节点）。
func findToolPart(c *conversation.Conversation) (*conversation.ToolPart, bool) {
	for _, m := range c.Snapshot().Nodes {
		for _, p := range m.Content {
			if p.Kind == conversation.PartTool && p.Tool != nil {
				return p.Tool, true
			}
		}
	}
	return nil, false
}

// TestRunMemoryWriteConfirmE2E M2 验收：模型经 memory_write 写记忆；
// Confirm 拦截（strict 答 y 落盘 / 答 n 拒绝不写）；permissive 等级矩阵免确认（矩阵生效）。
func TestRunMemoryWriteConfirmE2E(t *testing.T) {
	toolArgs := `{"name":"memories.md","content":"喜欢绿茶","mode":"overwrite"}`

	t.Run("strict 同意后落盘", func(t *testing.T) {
		srv, reqs := rawScriptServer(t, [][]string{
			{toolCallData("call_mw", "memory_write", toolArgs)},
			{contentData("记下了。")},
		})
		dir := t.TempDir()
		writeConfig(t, dir, srv.URL, "m")

		var out bytes.Buffer
		if code := run([]string{"-data", dir},
			strings.NewReader("记住我喜欢绿茶\ny\n/quit\n"), &out, io.Discard); code != 0 {
			t.Fatalf("code = %d, out = %q", code, out.String())
		}
		got := out.String()
		for _, want := range []string{"[y/N]", "memory_write", "[tool ok]", "记下了。"} {
			if !strings.Contains(got, want) {
				t.Fatalf("stdout 缺 %q: %q", want, got)
			}
		}
		data, err := os.ReadFile(filepath.Join(dir, "profiles", "default", port.GlobalMemoryDoc))
		if err != nil {
			t.Fatalf("记忆文件未落盘: %v", err)
		}
		if !strings.Contains(string(data), "喜欢绿茶") {
			t.Fatalf("memory = %q", data)
		}
		// 第二次请求带上 tool 结果；首次请求带 tools 声明。
		if !strings.Contains(string(reqs.at(0).body), `"tools"`) {
			t.Fatal("首次请求应带工具声明")
		}
		if !strings.Contains(string(reqs.at(1).body), "wrote memories.md") {
			t.Fatalf("第二次请求应带工具结果: %.300s", reqs.at(1).body)
		}
		c := loadTree(t, dir)
		tp, ok := findToolPart(c)
		if !ok || tp.Result == nil || !tp.Result.OK {
			t.Fatalf("tool 分片 = %+v", tp)
		}
	})

	t.Run("strict 拒绝不落盘", func(t *testing.T) {
		srv, _ := rawScriptServer(t, [][]string{
			{toolCallData("call_mw", "memory_write", toolArgs)},
			{contentData("好的，不写。")},
		})
		dir := t.TempDir()
		writeConfig(t, dir, srv.URL, "m")

		var out bytes.Buffer
		if code := run([]string{"-data", dir},
			strings.NewReader("别记了\nn\n/quit\n"), &out, io.Discard); code != 0 {
			t.Fatalf("code = %d, out = %q", code, out.String())
		}
		got := out.String()
		if !strings.Contains(got, "[y/N]") || !strings.Contains(got, "user denied") {
			t.Fatalf("stdout 缺拒绝痕迹: %q", got)
		}
		if _, err := os.Stat(filepath.Join(dir, "profiles", "default", port.GlobalMemoryDoc)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("拒绝后记忆文件不应存在: %v", err)
		}
		c := loadTree(t, dir)
		tp, ok := findToolPart(c)
		if !ok || tp.Result == nil || tp.Result.OK || !strings.Contains(tp.Result.Err, "user denied") {
			t.Fatalf("tool 分片 = %+v", tp)
		}
	})

	t.Run("permissive 矩阵免确认", func(t *testing.T) {
		srv, _ := rawScriptServer(t, [][]string{
			{toolCallData("call_mw", "memory_write", toolArgs)},
			{contentData("已记录。")},
		})
		dir := t.TempDir()
		writeConfigLevel(t, dir, srv.URL, "m", "permissive")

		var out bytes.Buffer
		// 无 y/n 应答行：确认若被触发会吞掉 /quit 并卡在等输入。
		if code := run([]string{"-data", dir},
			strings.NewReader("记住我喜欢绿茶\n/quit\n"), &out, io.Discard); code != 0 {
			t.Fatalf("code = %d, out = %q", code, out.String())
		}
		got := out.String()
		if strings.Contains(got, "[y/N]") {
			t.Fatalf("permissive 不应确认: %q", got)
		}
		if !strings.Contains(got, "[tool ok]") {
			t.Fatalf("工具应直接执行: %q", got)
		}
		if _, err := os.Stat(filepath.Join(dir, "profiles", "default", port.GlobalMemoryDoc)); err != nil {
			t.Fatalf("记忆文件应落盘: %v", err)
		}
	})
}

// TestEditorHelperProcess 假编辑器（helper 进程模式）：EDITOR 指向本测试二进制时，
// 向命令行末尾的路径写入标记，证明编辑器收到了正确目标（P1-4 e2e 用）。
func TestEditorHelperProcess(t *testing.T) {
	if os.Getenv("AQUARIUS_EDITOR_HELPER") != "1" {
		t.Skip("非 helper 进程运行")
	}
	args := flag.Args() // -test.run 之后的尾随参数 = 记忆文件路径
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "helper: 缺少路径参数")
		os.Exit(2)
	}
	if err := os.WriteFile(args[len(args)-1], []byte("edited-by-helper"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		os.Exit(2)
	}
	os.Exit(0)
}

// ---------------------------------------------------------------------------
// M3：term_exec / 后台任务（§12 M3 验收）
// ---------------------------------------------------------------------------

// TestRunMemoryCommandE2E /memory 端到端（M2 验收）：run() 注入的标准流直达编辑器
// 子进程（helper 假编辑器）——收到目标路径、写入标记，stdout 出现打开提示。
func TestRunMemoryCommandE2E(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "http://127.0.0.1:1", "m") // /memory 不发起生成
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", os.Args[0]+" -test.run=TestEditorHelperProcess")
	t.Setenv("AQUARIUS_EDITOR_HELPER", "1")

	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("/memory\n/quit\n"), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %q, stdout = %q", code, errBuf.String(), out.String())
	}
	got := out.String()
	if !strings.Contains(got, "已用系统编辑器打开") || !strings.Contains(got, port.GlobalMemoryDoc) {
		t.Fatalf("stdout 缺打开提示: %q", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "profiles", "default", port.GlobalMemoryDoc))
	if err != nil {
		t.Fatalf("记忆文件: %v", err)
	}
	if string(data) != "edited-by-helper" {
		t.Fatalf("假编辑器未收到目标路径: %q", data)
	}
}
