package acpgate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Tonyjh07/Aquarius/internal/adapter/pathres"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestHeadBufferTruncatesFromHead 有界缓冲：超限从**头部**丢，落在字符边界上，
// 长度不忽增忽减（代理按长度差取增量）。
//
// 用西里尔字母而不是中文：要的是「多字节字符」，而测试里的 CJK 字面量是禁的（i18n 锁）。
func TestHeadBufferTruncatesFromHead(t *testing.T) {
	b := newHeadBuffer(10)
	if _, err := b.Write([]byte("абвгде")); err != nil { // 12 字节
		t.Fatalf("write: %v", err)
	}
	out, truncated := b.Visible()
	if !truncated {
		t.Fatal("超限之后 truncated 应为真")
	}
	if len(out) > 10 {
		t.Fatalf("保留量 = %d 字节，超了上限", len(out))
	}
	if !utf8.ValidString(out) {
		t.Fatalf("截断不能产出非法 UTF-8：%q", out)
	}
	if !strings.HasSuffix(out, "е") {
		t.Fatalf("保留的应是尾部：%q", out)
	}

	// 再写一段：可见长度只增不减，且仍然是尾部（长度差取增量才不错位）。
	prev := len(out)
	b.Write([]byte("ж"))
	out2, _ := b.Visible()
	if len(out2) < prev {
		t.Fatalf("可见长度缩短了：%d → %d", prev, len(out2))
	}
	if !strings.HasSuffix(out2, "ж") {
		t.Fatalf("可见内容应是尾部：%q", out2)
	}
}

// TestTerminalTableLimits 名额与额度的记账：并发、总数、缓冲合计三道口子，release 归还。
func TestTerminalTableLimits(t *testing.T) {
	var tbl terminalTable
	ids := make([]string, 0, maxLiveTerminals)
	for i := 0; i < maxLiveTerminals; i++ {
		id, err := tbl.reserve(1024)
		if err != nil {
			t.Fatalf("第 %d 条应成功：%v", i+1, err)
		}
		tbl.attach(&terminal{id: id, out: newHeadBuffer(1024)})
		ids = append(ids, id)
	}
	if _, err := tbl.reserve(1024); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("并发上限应拒，got %v", err)
	}
	if _, ok := tbl.drop(ids[0]); !ok {
		t.Fatal("drop 应成功")
	}
	if _, err := tbl.reserve(1024); err != nil {
		t.Fatalf("release 之后应能再开：%v", err)
	}

	// 缓冲合计：一条就吃掉大半个预算，第二条就装不下。
	var big terminalTable
	if _, err := big.reserve(terminalBufferBudget - 8); err != nil {
		t.Fatalf("第一条应成功：%v", err)
	}
	if _, err := big.reserve(1024); err == nil || !strings.Contains(err.Error(), "output are already held") {
		t.Fatalf("缓冲合计上限应拒，got %v", err)
	}

	// 总数：与并发无关（每条都 release 掉也照样累计）。
	var total terminalTable
	for i := 0; i < maxTerminalCreates; i++ {
		id := reserveAttached(t, &total, 16)
		total.drop(id)
	}
	if _, err := total.reserve(16); err == nil || !strings.Contains(err.Error(), "already created") {
		t.Fatalf("总数上限应拒，got %v", err)
	}

	// id 不复用：release 之后再 create 必须是新 id（迟到的 output 不能打到别的命令上）。
	var ids2 terminalTable
	a := reserveAttached(t, &ids2, 16)
	ids2.drop(a)
	b := reserveAttached(t, &ids2, 16)
	if a == b {
		t.Fatalf("id 被复用了：%s", a)
	}
}

// reserveAttached 占一个名额并挂上（drop 只认真实挂过的终端）。
func reserveAttached(t *testing.T, tbl *terminalTable, bytes int) string {
	t.Helper()
	id, err := tbl.reserve(bytes)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	tbl.attach(&terminal{id: id, out: newHeadBuffer(bytes)})
	return id
}

// TestTerminalRuntime 五个方法直连：缺省 cwd、环境变量过手、截断、kill 后仍可取输出与退出码、
// release 之后 id 失效、cwd 越界直接拒（不问）。
func TestTerminalRuntime(t *testing.T) {
	root := t.TempDir()
	escape := t.TempDir()
	conf := &fakeConfirmer{allow: true}
	m := New(Options{Confirmer: conf, TerminalAsks: intPtr(-1)})
	t.Cleanup(m.Close)
	c := newTestClient(t, m, root)
	ctx := context.Background()

	// 1. 一条正常结束的命令：cwd 缺省 = 会话 cwd，环境变量传下去。
	mk, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{
		SessionId: "sess-1", Command: echoCmd, Args: []string{"one", "two"},
		Env: []acp.EnvVariable{
			{Name: "ECHOCMD_ECHO", Value: "FOO"},
			{Name: "FOO", Value: "bar"},
			{Name: "AQUARIUS_SECRET", Value: "leak"},
		},
		OutputByteLimit: intPtr(64 << 10),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp := pollTerminal(t, c, mk.TerminalId)
	if resp.ExitStatus == nil || resp.ExitStatus.ExitCode == nil || *resp.ExitStatus.ExitCode != 0 {
		t.Fatalf("停止状态 = %+v，want 退出码 0", resp.ExitStatus)
	}
	for _, want := range []string{"out:one two", "err:boom", "env:FOO=bar", "cwd:"} {
		if !strings.Contains(resp.Output, want) {
			t.Fatalf("输出缺 %q：\n%s", want, resp.Output)
		}
	}
	if strings.Contains(resp.Output, "leak") {
		t.Fatalf("AQUARIUS_* 不该跟着命令走：\n%s", resp.Output)
	}
	if !cwdMatches(resp.Output, root) {
		t.Fatalf("缺省 cwd 应为会话 cwd（%s）：\n%s", root, resp.Output)
	}
	if resp.Truncated {
		t.Fatal("没超限不该标 truncated")
	}
	// wait_for_exit 对已结束的命令立刻返回同一个退出码。
	wait, err := c.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{SessionId: "sess-1", TerminalId: mk.TerminalId})
	if err != nil || wait.ExitCode == nil || *wait.ExitCode != 0 {
		t.Fatalf("wait = %+v, err = %v", wait, err)
	}

	// 2. 输出超限：从头部丢，保留尾部，仍是合法 UTF-8。
	big, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{
		SessionId: "sess-1", Command: echoCmd,
		Env:             []acp.EnvVariable{{Name: "ECHOCMD_BULK", Value: "100"}},
		OutputByteLimit: intPtr(300),
	})
	if err != nil {
		t.Fatalf("create bulk: %v", err)
	}
	bulk := pollTerminal(t, c, big.TerminalId)
	if !bulk.Truncated {
		t.Fatal("超限应标 truncated")
	}
	if len(bulk.Output) > 300 || !utf8.ValidString(bulk.Output) {
		t.Fatalf("保留量 = %d 字节、合法 UTF-8 = %v", len(bulk.Output), utf8.ValidString(bulk.Output))
	}
	if !strings.Contains(bulk.Output, "line 099") {
		t.Fatalf("应保留尾部（最后几行）：\n%s", bulk.Output)
	}
	if strings.Contains(bulk.Output, "line 000") {
		t.Fatal("头部应被丢掉")
	}

	// 3. kill：不释放，之后仍能取输出与退出信息。
	long, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{
		SessionId: "sess-1", Command: echoCmd, Args: []string{"sleep"},
		Env: []acp.EnvVariable{{Name: "ECHOCMD_SLEEP_MS", Value: "30000"}},
	})
	if err != nil {
		t.Fatalf("create long: %v", err)
	}
	if _, err := c.KillTerminal(ctx, acp.KillTerminalRequest{SessionId: "sess-1", TerminalId: long.TerminalId}); err != nil {
		t.Fatalf("kill: %v", err)
	}
	after := pollTerminal(t, c, long.TerminalId)
	if after.ExitStatus == nil {
		t.Fatal("kill 之后应能取到退出信息（kill 不释放）")
	}
	if after.ExitStatus.ExitCode == nil && after.ExitStatus.Signal == nil {
		t.Fatalf("退出码与信号不能都为空：%+v", after.ExitStatus)
	}
	if !strings.Contains(after.Output, "out:sleep") {
		t.Fatalf("kill 之后输出仍应可取：\n%s", after.Output)
	}
	if _, err := c.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{SessionId: "sess-1", TerminalId: long.TerminalId}); err != nil {
		t.Fatalf("kill 之后 wait 应立刻返回：%v", err)
	}

	// 4. release：id 失效，缓冲归还。
	if _, err := c.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{SessionId: "sess-1", TerminalId: mk.TerminalId}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := c.TerminalOutput(ctx, acp.TerminalOutputRequest{SessionId: "sess-1", TerminalId: mk.TerminalId}); err == nil {
		t.Fatal("release 之后 id 应失效")
	}

	// 5. cwd 越界：直接拒，不问用户（它只是起点，问也拦不住）。
	before := conf.calls
	if _, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{
		SessionId: "sess-1", Command: echoCmd, Cwd: &escape,
	}); err == nil || !strings.Contains(err.Error(), "outside the session root set") {
		t.Fatalf("cwd 越界应拒，got %v", err)
	}
	if conf.calls != before {
		t.Fatalf("cwd 越界不该问用户：calls %d → %d", before, conf.calls)
	}
	// 相对路径同样是参数错。
	rel := "relative/dir"
	if _, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{
		SessionId: "sess-1", Command: echoCmd, Cwd: &rel,
	}); err == nil {
		t.Fatal("相对 cwd 应拒")
	}
}

// TestTerminalConcurrency 并发上限：第四条之后拒新的 create，release 之后又能开。
func TestTerminalConcurrency(t *testing.T) {
	root := t.TempDir()
	conf := &fakeConfirmer{allow: true}
	m := New(Options{Confirmer: conf, TerminalAsks: intPtr(-1)})
	t.Cleanup(m.Close)
	c := newTestClient(t, m, root)
	ctx := context.Background()

	for i := 0; i < maxLiveTerminals; i++ {
		if _, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{
			SessionId: "sess-1", Command: echoCmd,
			Env: []acp.EnvVariable{{Name: "ECHOCMD_SLEEP_MS", Value: "30000"}},
		}); err != nil {
			t.Fatalf("第 %d 条应成功：%v", i+1, err)
		}
	}
	_, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{SessionId: "sess-1", Command: echoCmd})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("并发上限应拒，got %v", err)
	}
}

// TestTerminalAsks 命令确认的额度：问过才算、额度用完直接拒且不问、配置成 0 时一次都不问。
func TestTerminalAsks(t *testing.T) {
	root := t.TempDir()
	conf := &fakeConfirmer{allow: true}
	m := New(Options{Confirmer: conf, TerminalAsks: intPtr(1)})
	t.Cleanup(m.Close)
	c := newTestClient(t, m, root)
	ctx := context.Background()

	if _, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{
		SessionId: "sess-1", Command: echoCmd,
		Env: []acp.EnvVariable{{Name: "ECHOCMD_EXIT", Value: "7"}},
	}); err != nil {
		t.Fatalf("第一次应放行：%v", err)
	}
	_, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{SessionId: "sess-1", Command: echoCmd})
	if err == nil || !strings.Contains(err.Error(), "approval limit") {
		t.Fatalf("额度用完应拒，got %v", err)
	}
	if conf.calls != 1 {
		t.Fatalf("额度用完后不该再问：calls = %d", conf.calls)
	}
	// 确认框里命令、目录、环境变量三者都要出现。
	for _, want := range []string{echoCmd, root, "ECHOCMD_EXIT=7"} {
		if !strings.Contains(conf.prompt, want) {
			t.Fatalf("确认框缺 %q：\n%s", want, conf.prompt)
		}
	}

	// 配置成 0：一次都不问，直接拒。
	conf2 := &fakeConfirmer{allow: true}
	m2 := New(Options{Confirmer: conf2, TerminalAsks: intPtr(0)})
	t.Cleanup(m2.Close)
	c2 := newTestClient(t, m2, root)
	if _, err := c2.CreateTerminal(ctx, acp.CreateTerminalRequest{SessionId: "sess-1", Command: echoCmd}); err == nil {
		t.Fatal("配置成不问时应直接拒")
	}
	if conf2.calls != 0 {
		t.Fatalf("不该问用户：calls = %d", conf2.calls)
	}
}

// TestTerminalCallbacks 走完整条链路（假代理发五个方法）：五个方法各有应答，
// 退出后仍可取输出，release 之后 id 失效。
func TestTerminalCallbacks(t *testing.T) {
	root := t.TempDir()
	conf := &fakeConfirmer{allow: true}
	m := newManager(t, "terminal", func(o *Options) {
		o.Home = root
		o.Confirmer = conf
		o.Agents[0].Env["FAKE_AGENT_CMD"] = echoCmd
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "run some commands"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(run.Report)), &got); err != nil {
		t.Fatalf("report is not the expected JSON: %v\n%s", err, run.Report)
	}
	if !strings.Contains(got["create_a"], "terminalId") || !strings.Contains(got["create_b"], "terminalId") {
		t.Fatalf("两次 create 都应成功：%s / %s", got["create_a"], got["create_b"])
	}
	for _, want := range []string{"out:one two", "err:boom", "env:FOO=bar", `"exitCode":0`} {
		if !strings.Contains(got["output_a"], want) {
			t.Fatalf("output_a 缺 %q：\n%s", want, got["output_a"])
		}
	}
	if got["wait_a"] != `{"exitCode":0}` {
		t.Fatalf("wait_a = %s", got["wait_a"])
	}
	if strings.Contains(got["output_b"], "exitStatus") {
		t.Fatalf("还活着的命令不该有 exitStatus：\n%s", got["output_b"])
	}
	if got["kill_b"] != "{}" || got["release_a"] != "{}" || got["release_b"] != "{}" {
		t.Fatalf("kill/release 应回空结果：%s / %s / %s", got["kill_b"], got["release_a"], got["release_b"])
	}
	if !strings.Contains(got["wait_b"], "exitCode") && !strings.Contains(got["wait_b"], "signal") {
		t.Fatalf("kill 之后 wait_for_exit 应给出退出信息：%s", got["wait_b"])
	}
	if !strings.HasPrefix(got["output_released"], "error") {
		t.Fatalf("release 之后取输出应报错：%s", got["output_released"])
	}
	if conf.calls != 2 {
		t.Fatalf("两条命令应各问一次，calls = %d", conf.calls)
	}

	// 报告要列出「它替你跑了什么」：两条命令与各自结局，被拒的才算备注。
	if len(run.Commands) != 2 {
		t.Fatalf("commands = %+v，want 两条", run.Commands)
	}
	if run.Commands[0].Line != echoCmd+" one two" {
		t.Fatalf("第一条 = %q，want 原样命令行", run.Commands[0].Line)
	}
	if run.Commands[0].ExitCode == nil || *run.Commands[0].ExitCode != 0 {
		t.Fatalf("第一条退出码 = %v", run.Commands[0].ExitCode)
	}
	killed := run.Commands[1]
	if killed.ExitCode == nil && killed.Signal == "" {
		t.Fatalf("被杀的这条也要给出结局：%+v", killed)
	}
	text := port.DelegateReportText(task.ID, run)
	if !strings.Contains(text, "Commands run through this session: ") || !strings.Contains(text, "one two") {
		t.Fatalf("报告缺命令清单：\n%s", text)
	}
}

// newTestClient 造一个「已建好会话」的客户端（根集 = roots），供直连测试用。
func newTestClient(t *testing.T, m *Manager, roots ...string) *client {
	t.Helper()
	set, err := pathres.NewRootSet(roots)
	if err != nil {
		t.Fatalf("roots: %v", err)
	}
	r := newRun("d1", Agent{Name: "fake"}, port.DelegateSpec{}, "c1")
	r.setSession("sess-1")
	r.setRoots(set)
	if resolved, err := pathres.Resolve(roots[0]); err == nil {
		r.setWorkDir(resolved)
	}
	t.Cleanup(r.closeTerminals)
	return &client{m: m, r: r}
}

// pollTerminal 轮询到命令结束（最多 10 秒）。
func pollTerminal(t *testing.T, c *client, id string) acp.TerminalOutputResponse {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := c.TerminalOutput(context.Background(), acp.TerminalOutputRequest{
			SessionId: "sess-1", TerminalId: id,
		})
		if err != nil {
			t.Fatalf("output(%s): %v", id, err)
		}
		if resp.ExitStatus != nil {
			return resp
		}
		if time.Now().After(deadline) {
			t.Fatalf("命令 %s 没在 10 秒内结束：\n%s", id, resp.Output)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// cwdMatches 命令报回的 cwd 是不是那个根。Windows 上同一目录可以有多种写法（8.3 短名、
// 盘符大小写），所以整条不等时退回比较末级目录名。
func cwdMatches(output, root string) bool {
	var printed string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "cwd:") {
			printed = strings.TrimSpace(strings.TrimPrefix(line, "cwd:"))
		}
	}
	if printed == "" {
		return false
	}
	if strings.EqualFold(filepath.Clean(printed), filepath.Clean(root)) {
		return true
	}
	r, err := pathres.Resolve(root)
	if err == nil && strings.EqualFold(filepath.Clean(printed), filepath.Clean(r)) {
		return true
	}
	return strings.EqualFold(filepath.Base(printed), filepath.Base(root))
}
