package toolbuiltin

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

// fakeJobs 脚本化 port.JobManager 替身：记录调用、返回预设值。
type fakeJobs struct {
	started  []port.JobSpec
	ran      []port.JobSpec
	runRes   tool.Result
	runErr   error
	runTails []int
	jobs     []port.Job
	logs     string
	killed   []port.JobID
}

func (f *fakeJobs) Start(_ context.Context, spec port.JobSpec) (port.Job, error) {
	f.started = append(f.started, spec)
	return port.Job{
		ID:        port.JobID(fmt.Sprintf("j%03d", len(f.started))),
		Spec:      spec,
		Status:    port.JobRunning,
		PID:       42,
		StartedAt: time.Now(),
	}, nil
}

func (f *fakeJobs) Run(_ context.Context, spec port.JobSpec) (tool.Result, error) {
	f.ran = append(f.ran, spec)
	return f.runRes, f.runErr
}

func (f *fakeJobs) List(context.Context) ([]port.Job, error) { return f.jobs, nil }

func (f *fakeJobs) Status(_ context.Context, id port.JobID) (port.Job, error) {
	for _, j := range f.jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return port.Job{}, fmt.Errorf("没有任务 %s", id)
}

func (f *fakeJobs) Logs(_ context.Context, id port.JobID, tail int) (string, error) {
	for _, j := range f.jobs {
		if j.ID == id {
			f.runTails = append(f.runTails, tail)
			return f.logs, nil
		}
	}
	return "", fmt.Errorf("没有任务 %s", id)
}

func (f *fakeJobs) Kill(_ context.Context, id port.JobID) error {
	f.killed = append(f.killed, id)
	return nil
}

// Wait 立即返回预设的终态快照（fakeJobs 的任务表是静态的）。
func (f *fakeJobs) Wait(_ context.Context, id port.JobID) (port.Job, error) {
	for _, j := range f.jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return port.Job{}, fmt.Errorf("没有任务 %s", id)
}

// newAllTools 用脚本任务管理器注册全部内置工具（测试缺省；无沙盒提示）。
func newAllTools(mem port.MemoryStore, pathOf PathOf) []port.Tool {
	return New(Deps{Memory: mem, PathOf: pathOf, Jobs: &fakeJobs{}})
}

// ---------------------------------------------------------------------------
// term_exec
// ---------------------------------------------------------------------------

// TestTermExecShellWrap 命令行包平台 shell 执行（windows=cmd /c，其余=sh -c）。
func TestTermExecShellWrap(t *testing.T) {
	jobs := &fakeJobs{runRes: tool.Result{OK: true, Output: "hello"}}
	te := &termExec{jobs: jobs}
	res, err := te.Execute(context.Background(), call(`{"command":"echo hello"}`))
	if err != nil || !res.OK || res.Output != "hello" {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if len(jobs.ran) != 1 {
		t.Fatalf("ran = %v", jobs.ran)
	}
	spec := jobs.ran[0]
	wantCmd, wantArg := "cmd", "/c"
	if runtime.GOOS != "windows" {
		wantCmd, wantArg = "/bin/sh", "-c"
	}
	if spec.Command != wantCmd || len(spec.Args) < 2 || spec.Args[0] != wantArg ||
		spec.Args[1] != "echo hello" {
		t.Fatalf("shell spec = %+v, want %s %s 'echo hello'", spec, wantCmd, wantArg)
	}
}

// TestTermExecRejectsEmpty 空命令报错（模型可纠正）。
func TestTermExecRejectsEmpty(t *testing.T) {
	te := &termExec{jobs: &fakeJobs{}}
	if _, err := te.Execute(context.Background(), call(`{"command":"  "}`)); err == nil ||
		!strings.Contains(err.Error(), "command") {
		t.Fatalf("err = %v", err)
	}
	if _, err := te.Execute(context.Background(), call(`not-json`)); err == nil {
		t.Fatal("非法参数应报错")
	}
}

// TestTermExecSpecEncodingNote 工具声明告知模型输出自动转 UTF-8、无须 chcp（D41）。
func TestTermExecSpecEncodingNote(t *testing.T) {
	spec := (&termExec{}).Spec()
	if !strings.Contains(spec.Description, "UTF-8") || !strings.Contains(spec.Description, "chcp") {
		t.Fatalf("description 应提示输出自动转 UTF-8/无须 chcp: %q", spec.Description)
	}
}

// TestTermExecTrimsHeadTail 超长输出保头尾截断（§4.3）。
func TestTermExecTrimsHeadTail(t *testing.T) {
	long := strings.Repeat("A", 10000) + strings.Repeat("B", 10000) + strings.Repeat("C", 30000)
	te := &termExec{jobs: &fakeJobs{runRes: tool.Result{OK: true, Output: long}}}
	res, err := te.Execute(context.Background(), call(`{"command":"x"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasPrefix(res.Output, "AAAA") || !strings.HasSuffix(res.Output, "CCCC") {
		t.Fatalf("头尾未保留: %.60q … %.40q", res.Output, res.Output[len(res.Output)-40:])
	}
	if !strings.Contains(res.Output, "middle") {
		t.Fatalf("缺省略标注: %.80q", res.Output)
	}
	if n := len([]rune(res.Output)); n > maxTermOut+120 { // 标注行的余量
		t.Fatalf("截断后长度 = %d, 超上限 %d", n, maxTermOut)
	}
}

// TestTermExecNilJobs 未装配任务管理器时明确报错（不 panic）。
func TestTermExecNilJobs(t *testing.T) {
	te := &termExec{}
	if _, err := te.Execute(context.Background(), call(`{"command":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "not available in this build") {
		t.Fatalf("err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// job_*
// ---------------------------------------------------------------------------

// TestJobStartSpecs job_start 参数映射与缺参校验。
func TestJobStartSpecs(t *testing.T) {
	jobs := &fakeJobs{}
	js := &jobStart{jobs: jobs}
	args := `{"command":"srv","args":["--port","1"],"workdir":"/tmp","timeout_sec":5}`
	res, err := js.Execute(context.Background(), call(args))
	if err != nil || !res.OK || !strings.Contains(res.Output, "j001") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	spec := jobs.started[0]
	if spec.Command != "srv" || len(spec.Args) != 2 || spec.WorkDir != "/tmp" ||
		spec.Timeout != 5*time.Second {
		t.Fatalf("spec = %+v", spec)
	}
	if _, err := js.Execute(context.Background(), call(`{}`)); err == nil {
		t.Fatal("缺 command 应报错")
	}
	// §14 M3 遗留：负 timeout_sec 与相对 workdir 拒收。
	if _, err := js.Execute(context.Background(), call(`{"command":"srv","timeout_sec":-1}`)); err == nil ||
		!strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("负 timeout_sec err = %v, want 拒收", err)
	}
	// 上限：防 Duration 乘法溢出（审查修复）。
	if _, err := js.Execute(context.Background(), call(`{"command":"srv","timeout_sec":9999999999}`)); err == nil ||
		!strings.Contains(err.Error(), "exceeds the upper bound") {
		t.Fatalf("超上限 timeout_sec err = %v, want 拒收", err)
	}
	// 校验与启动同值：带尾空格的绝对路径以 trim 后的值启动（审查修复）。
	if _, err := js.Execute(context.Background(), call(`{"command":"srv","workdir":"/tmp "}`)); err != nil {
		t.Fatalf("带尾空格的根起始 workdir 应接受: %v", err)
	}
	if got := jobs.started[len(jobs.started)-1].WorkDir; got != "/tmp" {
		t.Fatalf("WorkDir = %q, want trim 后的 /tmp", got)
	}
	if _, err := js.Execute(context.Background(), call(`{"command":"srv","workdir":"rel/dir"}`)); err == nil ||
		!strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("相对 workdir err = %v, want 拒收", err)
	}
	// 根起始路径（Windows 无盘符口径）仍接受。
	if _, err := js.Execute(context.Background(), call(`{"command":"srv","workdir":"/tmp"}`)); err != nil {
		t.Fatalf("根起始 workdir 应接受: %v", err)
	}
	if len(jobs.started) != 3 {
		t.Fatalf("非法参数不应启动: started=%d, want 3（两次合法 /tmp + 尾空格 trim）", len(jobs.started))
	}
}

// TestJobListOutput job_list 输出格式与空表提示。
func TestJobListOutput(t *testing.T) {
	jl := &jobList{jobs: &fakeJobs{}}
	res, err := jl.Execute(context.Background(), call(`{}`))
	if err != nil || !strings.Contains(res.Output, "no entries") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	jl = &jobList{jobs: &fakeJobs{jobs: []port.Job{{
		ID: "j001", Status: port.JobRunning, PID: 7, StartedAt: time.Now(),
		Spec: port.JobSpec{Command: "srv"},
	}}}}
	res, err = jl.Execute(context.Background(), call(`{}`))
	if err != nil || !strings.Contains(res.Output, "j001") ||
		!strings.Contains(res.Output, "running") || !strings.Contains(res.Output, "srv") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

// TestJobStatusAndPrefixResolve job_status 精确/前缀解析、未知 ID 报错。
func TestJobStatusAndPrefixResolve(t *testing.T) {
	now := time.Now()
	js := &jobStatus{jobs: &fakeJobs{jobs: []port.Job{{
		ID: "j001", Status: port.JobDone, PID: 7, ExitCode: 0,
		StartedAt: now, EndedAt: now.Add(time.Second),
	}}}}
	res, err := js.Execute(context.Background(), call(`{"id":"j0"}`)) // 唯一前缀
	if err != nil || !res.OK || !strings.Contains(res.Output, "status=done") ||
		!strings.Contains(res.Output, "exit=0") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if _, err := js.Execute(context.Background(), call(`{"id":"j999"}`)); err == nil ||
		!strings.Contains(err.Error(), "no task") {
		t.Fatalf("err = %v", err)
	}
	if _, err := js.Execute(context.Background(), call(`{}`)); err == nil {
		t.Fatal("缺 id 应报错")
	}
}

// TestJobLogsTail job_logs 缺省 tail=50、显式 tail 透传、空日志提示。
func TestJobLogsTail(t *testing.T) {
	jobs := &fakeJobs{logs: "line", jobs: []port.Job{{ID: "j001"}}}
	jl := &jobLogs{jobs: jobs}
	res, err := jl.Execute(context.Background(), call(`{"id":"j001"}`))
	if err != nil || res.Output != "line" {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if len(jobs.runTails) != 1 || jobs.runTails[0] != defaultJobLogsTail {
		t.Fatalf("tails = %v, want [%d]", jobs.runTails, defaultJobLogsTail)
	}
	if _, err := jl.Execute(context.Background(), call(`{"id":"j001","tail":3}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if jobs.runTails[1] != 3 {
		t.Fatalf("tails = %v, want 透传 3", jobs.runTails)
	}
	jobs.logs = "  \n"
	res, err = jl.Execute(context.Background(), call(`{"id":"j001"}`))
	if err != nil || !strings.Contains(res.Output, "no entries") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

// TestJobKill job_kill 前缀解析并转发终止。
func TestJobKill(t *testing.T) {
	jobs := &fakeJobs{jobs: []port.Job{{ID: "j001"}}}
	jk := &jobKill{jobs: jobs}
	res, err := jk.Execute(context.Background(), call(`{"id":"j0"}`))
	if err != nil || !res.OK || !strings.Contains(res.Output, "j001") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if len(jobs.killed) != 1 || jobs.killed[0] != "j001" {
		t.Fatalf("killed = %v", jobs.killed)
	}
}

// TestJobWaitReturnsResult job_wait：等到终态就地回填状态行与日志尾部，
// 不传 timeout_sec 一直等（D189）。
func TestJobWaitReturnsResult(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	jobs := &fakeJobs{
		logs: "line one\nline two\n",
		jobs: []port.Job{{
			ID: "j001", Status: port.JobDone, ExitCode: 0,
			StartedAt: now, EndedAt: now.Add(90 * time.Second),
		}},
	}
	jw := &jobWait{jobs: jobs}
	res, err := jw.Execute(context.Background(), call(`{"id":"j0"}`))
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	for _, want := range []string{"j001 status=done exit=0 duration=1m30s", "untrusted process output", "line one", "line two"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output = %q, want 含 %q", res.Output, want)
		}
	}
	if len(jobs.runTails) != 1 || jobs.runTails[0] != defaultJobLogsTail {
		t.Fatalf("tails = %v, want [%d]", jobs.runTails, defaultJobLogsTail)
	}
	// 参数校验：缺 id / 负数 / 超上限。
	if _, err := jw.Execute(context.Background(), call(`{}`)); err == nil {
		t.Fatal("缺 id 应报错")
	}
	if _, err := jw.Execute(context.Background(), call(`{"id":"j001","timeout_sec":-1}`)); err == nil ||
		!strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("负 timeout_sec err = %v, want 拒收", err)
	}
	if _, err := jw.Execute(context.Background(), call(`{"id":"j001","timeout_sec":9999999999}`)); err == nil ||
		!strings.Contains(err.Error(), "exceeds the upper bound") {
		t.Fatalf("超上限 timeout_sec err = %v, want 拒收", err)
	}
}

// blockingJobs 等不到终态的任务管理器：Wait 阻塞到 ctx 结束（测 job_wait 的
// 「仍在跑」回填与「不吃默认工具超时」的口径）。
type blockingJobs struct {
	fakeJobs
}

func (b *blockingJobs) Wait(ctx context.Context, _ port.JobID) (port.Job, error) {
	<-ctx.Done()
	return port.Job{}, ctx.Err()
}

func (b *blockingJobs) Status(context.Context, port.JobID) (port.Job, error) {
	return port.Job{ID: "j001", Status: port.JobRunning}, nil
}

// TestJobWaitTimeoutKeepsTaskRunning 自己等超时只回填「仍在跑」，不是错误：
// 任务继续跑，收尾时仍由观察者汇报。
func TestJobWaitTimeoutKeepsTaskRunning(t *testing.T) {
	jobs := &blockingJobs{fakeJobs: fakeJobs{jobs: []port.Job{{ID: "j001", Status: port.JobRunning}}}}
	jw := &jobWait{jobs: jobs}
	start := time.Now()
	res, err := jw.Execute(context.Background(), call(`{"id":"j001","timeout_sec":1}`))
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if !strings.Contains(res.Output, "still running after waiting 1s") {
		t.Fatalf("output = %q", res.Output)
	}
	if el := time.Since(start); el < 900*time.Millisecond {
		t.Fatalf("应等满约 1s，实际 %v", el)
	}
	// 用户中断（父 ctx 取消）照常上抛，不吞成「仍在跑」。
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := jw.Execute(ctx, call(`{"id":"j001"}`)); err == nil {
		t.Fatal("父 ctx 取消应上抛")
	}
}

// TestJobWaitNotInteractiveChain job_wait 申报 Interactive（不吃缺省工具超时），
// 否则「等到任务结束」会被 limits.tool_timeout_sec 拦腰截断。
func TestJobWaitNotInteractiveChain(t *testing.T) {
	var it port.Interactive = &jobWait{}
	if !it.Interactive() {
		t.Fatal("job_wait 应申报 Interactive")
	}
}

// TestTrimHeadTailOddMax §14 M3 遗留：max 为奇数时省略计数按实际保留量（2×keep）计——
// keep=max/2 使头尾合计 max-1，按 len-max 会少报 1。
func TestTrimHeadTailOddMax(t *testing.T) {
	s := strings.Repeat("a", 100)
	out := trimHeadTail(s, 11) // keep=5，实际保留 10，省略 90（旧口径报 89）
	if !strings.Contains(out, "middle 90 chars omitted") {
		t.Fatalf("奇数上限省略计数错: %q", out)
	}
	if !strings.Contains(out, "100 chars total") {
		t.Fatalf("原长标注错: %q", out)
	}
	if head := strings.SplitN(out, "\n", 2)[0]; head != "aaaaa" {
		t.Fatalf("头部保留 = %q, want 5 字符", head)
	}
	// 偶数上限：不变式（2×keep = max）。
	if out := trimHeadTail(s, 10); !strings.Contains(out, "middle 90 chars omitted") {
		t.Fatalf("偶数上限省略计数错: %q", out)
	}
	// 未超限原样返回。
	if out := trimHeadTail("short", 10); out != "short" {
		t.Fatalf("未超限应原样: %q", out)
	}
}
