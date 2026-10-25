package app

// session_misc_test.go 会话杂项命令：/jobs、/title 与 /exit、/permission、
// 无人值守模式、/compact 与取消、保存失败上浮、依赖校验。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newTestAgent 组装注入脚本流 LLM 的最小 Agent（会话测试用）。
func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	a, err := New(
		Deps{LLM: &scriptLLM{t: t}, UI: &recorder{}, IDs: &seqIDs{}, Clock: fixedClock{testTime}},
		Config{Model: "m"},
	)
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	return a
}

// TestSessionTitleAndExitCommands /title 无参显示、有参改写并落盘；/exit = /quit 别名。
// TestSessionJobsCommand /jobs 命令（M3）：list/logs/kill、前缀解析、用法与空表提示。
func TestSessionJobsCommand(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	jobs := &fakeJobs{
		jobs: []port.Job{{
			ID: "j001", Status: port.JobRunning, PID: 7, StartedAt: testTime,
			Spec: port.JobSpec{Command: "srv", Args: []string{"--port", "1"}},
		}},
		logs: map[port.JobID]string{"j001": "listening\n"},
	}
	s.jobs = jobs

	// list：展示状态与命令行。
	out, err := handleCmd(s, "jobs")
	if err != nil || !strings.Contains(out, "j001") || !strings.Contains(out, "running") ||
		!strings.Contains(out, "srv --port 1") {
		t.Fatalf("list = %q, %v", out, err)
	}
	// logs：前缀解析 + 缺省 tail + 内容回显。
	out, err = handleCmd(s, "jobs", "logs", "j0")
	if err != nil || !strings.Contains(out, "listening") {
		t.Fatalf("logs = %q, %v", out, err)
	}
	if jobs.lastTail != defaultJobLogsTail {
		t.Fatalf("tail = %d, want %d", jobs.lastTail, defaultJobLogsTail)
	}
	// logs：自定义行数透传。
	if _, err := handleCmd(s, "jobs", "logs", "j001", "5"); err != nil {
		t.Fatalf("logs n: %v", err)
	}
	if jobs.lastTail != 5 {
		t.Fatalf("tail = %d, want 5", jobs.lastTail)
	}
	// kill：前缀解析并转发。
	out, err = handleCmd(s, "jobs", "kill", "j0")
	if err != nil || !strings.Contains(out, "已终止 j001") {
		t.Fatalf("kill = %q, %v", out, err)
	}
	if len(jobs.killed) != 1 || jobs.killed[0] != "j001" {
		t.Fatalf("killed = %v", jobs.killed)
	}
	// 未知子命令与坏参数报用法。
	if _, err := handleCmd(s, "jobs", "wat"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("wat err = %v", err)
	}
	if _, err := handleCmd(s, "jobs", "logs", "j001", "-3"); err == nil ||
		!strings.Contains(err.Error(), "正整数") {
		t.Fatalf("bad tail err = %v", err)
	}
	if _, err := handleCmd(s, "jobs", "logs", "j999"); err == nil ||
		!strings.Contains(err.Error(), "no task") {
		t.Fatalf("unknown id err = %v", err)
	}
}

// TestSessionJobsEmpty 空表提示。
func TestSessionJobsEmpty(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	s.jobs = &fakeJobs{}
	out, err := handleCmd(s, "jobs", "list")
	if err != nil || !strings.Contains(out, "暂无后台任务") {
		t.Fatalf("out = %q, %v", out, err)
	}
}

// TestSessionJobsEdges /jobs 边界：前缀歧义、List 故障上抛、空日志提示。
func TestSessionJobsEdges(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	jobs := []port.Job{
		{ID: "j001", Status: port.JobDone, StartedAt: testTime, Spec: port.JobSpec{Command: "a"}},
		{ID: "j002", Status: port.JobDone, StartedAt: testTime, Spec: port.JobSpec{Command: "b"}},
	}

	// 前缀歧义（"j0" 命中两个）。
	s.jobs = &fakeJobs{jobs: jobs}
	if _, err := handleCmd(s, "jobs", "logs", "j0"); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("歧义 err = %v", err)
	}
	if _, err := handleCmd(s, "jobs", "kill", "j0"); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("kill 歧义 err = %v", err)
	}

	// List 故障上抛（不静默）。
	s.jobs = &fakeJobs{err: errors.New("存储炸了")}
	if _, err := handleCmd(s, "jobs"); err == nil || !strings.Contains(err.Error(), "存储炸了") {
		t.Fatalf("list err = %v", err)
	}

	// 空日志提示。
	s.jobs = &fakeJobs{jobs: jobs, logs: map[port.JobID]string{"j001": "  \n"}}
	out, err := handleCmd(s, "jobs", "logs", "j001")
	if err != nil || !strings.Contains(out, "日志为空") {
		t.Fatalf("out = %q, %v", out, err)
	}
}

func TestSessionTitleAndExitCommands(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)

	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "title"}})
	if err != nil || !strings.Contains(out, defaultTitle) {
		t.Fatalf("title 无参 = %q, %v", out, err)
	}
	out, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "title", Args: []string{"改个", "名字"}}})
	if err != nil || !strings.Contains(out, "改个 名字") {
		t.Fatalf("title 有参 = %q, %v", out, err)
	}
	if store.convs[s.Current().ID].Title != "改个 名字" {
		t.Fatalf("标题未落盘: %q", store.convs[s.Current().ID].Title)
	}
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "exit"}}); !errors.Is(err, ErrQuit) {
		t.Fatalf("exit = %v, want ErrQuit", err)
	}
}

// TestSessionPermissionCommand /permission 展示矩阵、切换等级并写回 config（D22）。
func TestSessionPermissionCommand(t *testing.T) {
	store := newMemStore()
	rec := &recorder{}
	llm := &scriptLLM{t: t}
	agent := newAgent(t, llm, rec, Deps{}, Config{})
	perms := &fakePerms{}
	s, err := NewSession(context.Background(), SessionDeps{
		Store:       store,
		Agent:       agent,
		IDs:         &seqIDs{},
		Clock:       fixedClock{testTime},
		SandboxPath: "/data/sandbox",
		Perms:       perms,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	// 无参：默认 strict + 矩阵 + 特权目录。
	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission"}})
	if err != nil {
		t.Fatalf("permission: %v", err)
	}
	for _, want := range []string{"strict", "/data/sandbox", "read-only", "permissive", "full-access", "特权 rw"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report 缺 %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "* strict") {
		t.Fatalf("当前等级应带 * 标记:\n%s", out)
	}

	// 非法等级报因。
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission", Args: []string{"root"}}}); err == nil {
		t.Fatal("非法等级应报错")
	}
	// 切换并写回。
	out, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission", Args: []string{"PERMISSIVE"}}})
	if err != nil || !strings.Contains(out, "permissive") {
		t.Fatalf("switch = %q, %v", out, err)
	}
	if len(perms.levels) != 1 || perms.levels[0] != perm.Permissive {
		t.Fatalf("persisted = %v, want [permissive]", perms.levels)
	}
	out, _ = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission"}})
	if !strings.Contains(out, "* permissive") {
		t.Fatalf("切换后报告:\n%s", out)
	}
	// 相同等级短路：不再写回。
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission", Args: []string{"permissive"}}}); err != nil {
		t.Fatalf("same level: %v", err)
	}
	if len(perms.levels) != 1 {
		t.Fatalf("persisted = %v, want 不重复写回", perms.levels)
	}

	// 无持久化回调时拒绝切换。
	s2, err := NewSession(context.Background(), SessionDeps{
		Store: newMemStore(), Agent: agent, IDs: &seqIDs{}, Clock: fixedClock{testTime},
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := s2.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission", Args: []string{"full-access"}}}); err == nil {
		t.Fatal("无持久化回调应报错")
	}
}

// TestSessionUnattendedCommand /unattended（D185 口径四/五）：无参 = 切换、有参 = 显式
// 设置；写回成功后活开关同步；回执要说清与 -yes 的反向关系（拒绝 vs 放行）。
func TestSessionUnattendedCommand(t *testing.T) {
	perms := &fakePerms{}
	agent := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
	s, err := NewSession(context.Background(), SessionDeps{
		Store: newMemStore(),
		Agent: agent,
		IDs:   &seqIDs{},
		Clock: fixedClock{testTime},
		Perms: perms,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	run := func(args ...string) (string, error) {
		return s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "unattended", Args: args}})
	}

	// 无参 = 切换（关 → 开）。
	out, err := run()
	if err != nil {
		t.Fatalf("toggle on: %v", err)
	}
	if !perms.unattended || len(perms.unattend) != 1 || !perms.unattend[0] {
		t.Fatalf("切换后 on=%v persisted=%v", perms.unattended, perms.unattend)
	}
	for _, want := range []string{"无人值守已开启", "-yes", "权限边界不变"} {
		if !strings.Contains(out, want) {
			t.Fatalf("开启回执缺 %q:\n%s", want, out)
		}
	}

	// 显式设置同值 = 短路（不重复写回）。
	if _, err := run("on"); err != nil {
		t.Fatalf("set on: %v", err)
	}
	if len(perms.unattend) != 1 {
		t.Fatalf("同值不应重复写回: %v", perms.unattend)
	}
	// 显式关闭。
	out, err = run("off")
	if err != nil || perms.unattended || len(perms.unattend) != 2 || perms.unattend[1] {
		t.Fatalf("off = %q, %v, on=%v persisted=%v", out, err, perms.unattended, perms.unattend)
	}
	if !strings.Contains(out, "恢复逐次确认") {
		t.Fatalf("关闭回执: %q", out)
	}
	// 大小写不敏感（同值 = 短路，不重复写回）+ 非法值报因。
	out, err = run("OFF")
	if err != nil {
		t.Fatalf("OFF: %v", err)
	}
	if !strings.Contains(out, "已是") {
		t.Fatalf("同值时回执应说明已是该状态: %q", out)
	}
	if perms.unattended || len(perms.unattend) != 2 {
		t.Fatalf("同值不应写回: on=%v persisted=%v", perms.unattended, perms.unattend)
	}
	if _, err := run("maybe"); err == nil {
		t.Fatal("非法值应报错")
	}
	if _, err := run("on", "off"); err == nil {
		t.Fatal("多参应报错")
	}

	// 无持久化回调：有参报错，无参也报错（无法落盘就不该假装切了）。
	s2, err := NewSession(context.Background(), SessionDeps{
		Store: newMemStore(), Agent: agent, IDs: &seqIDs{}, Clock: fixedClock{testTime},
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := s2.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "unattended", Args: []string{"on"}}}); err == nil {
		t.Fatal("无持久化回调应报错")
	}
}

// TestSessionCompactCommand D21 手动轨：/compact 生成摘要节点、落盘、报记账；重复压缩短路。
func TestSessionCompactCommand(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store,
		textStream("模型回复"),
		withUsage(textStream("## Objective\n- 压缩后的摘要"), conversation.Usage{InputTokens: 40, OutputTokens: 15}),
	)
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "hi"}); err != nil {
		t.Fatalf("handle: %v", err)
	}

	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "compact"}})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	// 会话 = [root,persona,user,assistant]：absorbed 应为 2（persona 恒回传不计）。
	if !strings.Contains(out, "已压缩 2 条历史") || !strings.Contains(out, "in=40 out=15") {
		t.Fatalf("out = %q, want 精确计数与记账", out)
	}
	path := s.Current().Path()
	last := path[len(path)-1]
	if last.Role != conversation.RoleSystem || last.Content[0].Text != "## Objective\n- 压缩后的摘要" {
		t.Fatalf("last = %+v, want 摘要节点", last)
	}
	if _, ok := store.convs[s.Current().ID].Find(last.ID); !ok {
		t.Fatal("摘要节点应已落盘")
	}

	// 重复压缩：其后无新内容 → 短路提示，不发起生成。
	out, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "compact"}})
	if err != nil || !strings.Contains(out, "无需压缩") {
		t.Fatalf("second compact = %q, %v", out, err)
	}
}

// TestSessionCompactCancelFriendly 压缩被取消：友好文案、会话树不动。
func TestSessionCompactCancelFriendly(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store,
		textStream("回复"),
		&scriptStream{steps: []scriptStep{{err: context.Canceled}}},
	)
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "hi"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	before := s.Current().NodeCount()

	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "compact"}})
	if err != nil || !strings.Contains(out, "已取消压缩") {
		t.Fatalf("compact cancel = %q, %v", out, err)
	}
	if s.Current().NodeCount() != before {
		t.Fatalf("nodes = %d, want 会话未改动 %d", s.Current().NodeCount(), before)
	}
}

func TestSessionSaveFailureSurfaces(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store, textStream("x"))
	store.err = errors.New("disk full")

	_, err := s.Handle(context.Background(), port.UserInput{Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want 保存失败上抛", err)
	}

	// Turn 错误与保存失败同时发生：两个错误都要保留。
	store2 := newMemStore()
	s2, llm2, _ := newTestSession(t, store2, textStream("x"))
	llm2.genErr = errors.New("boom")
	store2.err = errors.New("disk full")
	_, err = s2.Handle(context.Background(), port.UserInput{Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want 同时保留 boom 与 disk full", err)
	}
}

func TestNewSessionDependenciesValidated(t *testing.T) {
	if _, err := NewSession(context.Background(), SessionDeps{}); err == nil {
		t.Fatal("缺少依赖应报错")
	}
}

// ---------------------------------------------------------------------------
// M1 树交互命令：/goto /edit /branch /rm
// ---------------------------------------------------------------------------
