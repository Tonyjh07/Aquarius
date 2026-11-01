package main

// main_delegate_test.go 外部代理转交的端到端验收（§5.18 第 10 步）：用一个**真**的
// ACP 进程（testdata/fakeagent，现编）跑通「派出 → 切走 → 切回 → 报告在」。

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// buildFakeAgent 现编假 ACP 代理（与 acpgate 包测试用的是同一份源码）。
func buildFakeAgent(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakeagent")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// 用绝对路径定位源码：测试进程的 cwd 不保证是包目录。
	_, file, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(file), "..", "..", "internal", "adapter", "acpgate", "testdata", "fakeagent")
	out, err := exec.Command("go", "build", "-o", bin, src).CombinedOutput()
	if err != nil {
		t.Fatalf("编译假代理: %v\n%s", err, out)
	}
	return bin
}

// loadAllConversations 读出 profile 下的全部会话（跑着的时候也能读）。
func loadAllConversations(t *testing.T, dir string) []*conversation.Conversation {
	t.Helper()
	ctx := context.Background()
	store, err := storejson.New(filepath.Join(dir, "profiles", "default", "conversations"))
	if err != nil {
		t.Fatalf("storejson: %v", err)
	}
	sums, err := store.List(ctx)
	if err != nil {
		t.Fatalf("列出会话: %v", err)
	}
	out := make([]*conversation.Conversation, 0, len(sums))
	for _, s := range sums {
		c, err := store.Load(ctx, s.ID)
		if err != nil {
			continue // 正在写：下轮再看
		}
		out = append(out, c)
	}
	return out
}

// findDelegateInject 找一条带 delegate 来源的注入节点，返回它所在的会话与正文。
func findDelegateInject(convs []*conversation.Conversation) (*conversation.Conversation, string) {
	for _, c := range convs {
		for _, m := range c.Snapshot().Nodes {
			if m.Role != conversation.RoleInject || !strings.Contains(m.Source, "delegate:") {
				continue
			}
			var b strings.Builder
			for _, p := range m.Content {
				b.WriteString(p.Text)
			}
			return c, b.String()
		}
	}
	return nil, ""
}

// TestRunDelegateE2E 派出 → 切走（/new）→ 报告落在**发起**会话。
//
// 时序不是靠 sleep 猜的：转交用真进程跑到终态，注入走 D261 的异地落地；测试轮询
// 会话树直到注入出现，再收掉 stdin 让程序退出。
func TestRunDelegateE2E(t *testing.T) {
	agent := buildFakeAgent(t)
	srv, _ := rawScriptServer(t, [][]string{
		{toolCallData("call_dg", "delegate", `{"agent":"fake","task":"refactor the parser"}`)},
		{contentData("已派出，报告到了我会说。")},
		{contentData("收到报告。")},
	})
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{
  "model": {"providers":[{"name":"openai-compatible","base_url":%q,"keys":[{"api_key":"secret:AQ_E2E_KEY"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui": {"kind":"repl"},
  "permissions": {"level": "strict"},
  "delegate": {"agents": [{"name":"fake","command":%q,"env":{"FAKE_AGENT_MODE":"ok"},"default":true}], "timeout_sec": 60},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, srv.URL, agent)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("AQ_E2E_KEY", "test-key")

	var out bytes.Buffer
	pr, pw := io.Pipe()
	code := make(chan int, 1)
	go func() { code <- run([]string{"-data", dir}, pr, &out, io.Discard) }()

	// 派活（确认 y），然后立刻切到新会话——报告必须落在发起它的那个会话里。
	if _, err := io.WriteString(pw, "把解析器交给外部代理\ny\n/new\n"); err != nil {
		t.Fatalf("写 stdin: %v", err)
	}

	var origin *conversation.Conversation
	var text string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		origin, text = findDelegateInject(loadAllConversations(t, dir))
		if origin != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = pw.Close()
	if origin == nil {
		t.Fatalf("没等到转交报告落地；stdout = %q", out.String())
	}
	for _, want := range []string{"Delegation d001 to fake", "tool calls=2", "files changed=2", "Hello world"} {
		if !strings.Contains(text, want) {
			t.Fatalf("报告缺 %q：%q", want, text)
		}
	}
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("exit code = %d, stdout = %q", c, out.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("程序未退出")
	}

	// 会话树：先有一个 delegate 工具分片（句柄），终态后多一条 delegate: 注入节点。
	if _, ok := findToolPart(origin); !ok {
		t.Fatal("发起会话里应有一个 delegate 工具分片")
	}
	// 另一个会话（/new 之后那个）不该有转交报告——归属按发起会话，不按当前焦点。
	for _, c := range loadAllConversations(t, dir) {
		if c.ID == origin.ID {
			continue
		}
		if _, txt := findDelegateInject([]*conversation.Conversation{c}); txt != "" {
			t.Fatalf("报告落错了会话: %q", txt)
		}
	}
	// 转写区：派出时出一行状态（任务 id），报告以注入出现。
	if !strings.Contains(out.String(), "delegate") {
		t.Fatalf("stdout 应提到 delegate: %q", out.String())
	}
}
