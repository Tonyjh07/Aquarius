package tierone

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// fakeHost 只用来喂 Create：状态机的测试不关心 Host 提供了什么。
type fakeHost struct{}

func (fakeHost) Logger() v1.Logger          { return nil }
func (fakeHost) Secrets() v1.SecretResolver { return nil }
func (fakeHost) Blobs() v1.BlobStore        { return nil }
func (fakeHost) DataDir() string            { return "" }
func (fakeHost) WorkDir() string            { return "" }
func (fakeHost) Locale() string             { return "zh" }
func (fakeHost) Notify(string, string)      {}
func (fakeHost) Confirm(context.Context, v1.ConfirmRequest) (v1.ConfirmAnswer, error) {
	return v1.ConfirmAnswer{}, nil
}

// fakePlugin 一个可配置的插件：声明什么、实现什么、哪一步失败都由字段决定。
type fakePlugin struct {
	name     string
	api      int
	provides []string
	// implement 声明「实例实际实现了哪个供给面」；空 = 与 provides 一致（正常插件）。
	// 填一个别的值就造出「声明了但没实现」。
	implement []string
	tools     []v1.ToolSpec
	startErr  error
	shutErr   error
	started   int
	shutdowns int
}

func (p *fakePlugin) Manifest() v1.Manifest {
	api := p.api
	if api == 0 {
		api = v1.APIVersion
	}
	return v1.Manifest{Name: p.name, API: api, Provides: p.provides}
}

// factory 一个按插件配置造实例的工厂。
type factory struct{ p *fakePlugin }

func (f factory) Manifest() v1.Manifest { return f.p.Manifest() }

func (f factory) Create(v1.Host) (v1.Instance, error) {
	base := &fakeInstance{p: f.p}
	port := firstPort(f.p.implement, f.p.provides)
	switch port {
	case PortLLM:
		return &faceLLM{fakeFace{base}}, nil
	case PortTool:
		return &faceTool{fakeFace{base}}, nil
	case PortMemory:
		return &faceMemory{fakeFace{base}}, nil
	}
	return base, nil
}

// firstPort 实例实际实现的供给面：优先 implement，其次 provides 的第一项。
func firstPort(implement, provides []string) string {
	if len(implement) > 0 {
		return implement[0]
	}
	if len(provides) > 0 {
		return provides[0]
	}
	return ""
}

// fakeInstance 只有生命周期，没有供给面——供给面由 face* 类型逐个加上，这样「声明了
// 但没实现」才构造得出来。
type fakeInstance struct{ p *fakePlugin }

func (i *fakeInstance) Start() error {
	if i.p.startErr != nil {
		return i.p.startErr
	}
	i.p.started++
	return nil
}

func (i *fakeInstance) Shutdown(context.Context) error {
	i.p.shutdowns++
	return i.p.shutErr
}

type fakeFace struct{ *fakeInstance }

type faceLLM struct{ fakeFace }

func (faceLLM) Generate(context.Context, v1.GenerateRequest) (v1.Stream, error) {
	return nil, errors.New("not used in the state machine")
}
func (faceLLM) Models(context.Context) ([]v1.ModelInfo, error) { return nil, nil }

type faceTool struct{ fakeFace }

func (f faceTool) Tools(context.Context) []v1.ToolSpec { return f.p.tools }
func (faceTool) Execute(context.Context, v1.ToolCall) (v1.ToolResult, error) {
	return v1.ToolResult{OK: true}, nil
}

type faceMemory struct{ fakeFace }

func (faceMemory) Recall(context.Context, v1.MemoryRequest) (v1.MemoryContext, error) {
	return v1.MemoryContext{}, nil
}

func newRegistry(t *testing.T, ps ...*fakePlugin) *Registry {
	t.Helper()
	fs := make([]v1.Factory, 0, len(ps))
	for _, p := range ps {
		fs = append(fs, factory{p: p})
	}
	return New(Options{Factories: fs, NewHost: func(string) v1.Host { return fakeHost{} }})
}

func TestManifestValidationStopsPlugin(t *testing.T) {
	// 断言用报因里的稳定片段（版本号、非法名、词表外的词），不绑中文措辞。
	cases := []struct {
		name   string
		plugin *fakePlugin
		want   string
	}{
		{"api 版本不符", &fakePlugin{name: "old", api: 99}, "v99"},
		{"名字带冒号", &fakePlugin{name: "a:b"}, "a:b"},
		{"未知供给面", &fakePlugin{name: "x", provides: []string{"magic"}}, "magic"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRegistry(t, c.plugin)
			rec, ok := r.Record(c.plugin.name)
			if !ok {
				t.Fatalf("插件没登记上")
			}
			if rec.State != StateStopped || rec.Err == nil {
				t.Fatalf("状态=%v err=%v，想要 stopped + 报因", rec.State, rec.Err)
			}
			if !strings.Contains(rec.Err.Error(), c.want) {
				t.Fatalf("报因=%q，想看到 %q", rec.Err, c.want)
			}
			if err := r.SetEnabled(context.Background(), c.plugin.name, true); err == nil {
				t.Fatal("校验失败的插件不该启用成功")
			}
			if c.plugin.started != 0 {
				t.Fatal("校验失败不该走到 Start")
			}
		})
	}
}

func TestEmptyNameIsNotRegistered(t *testing.T) {
	r := newRegistry(t, &fakePlugin{name: ""})
	if got := len(r.List()); got != 0 {
		t.Fatalf("无名插件不该进注册表，List=%d", got)
	}
}

func TestNameTakenIsRejected(t *testing.T) {
	p := &fakePlugin{name: "dup", provides: []string{PortTool}, tools: []v1.ToolSpec{{Name: "t"}}}
	r := New(Options{
		Factories: []v1.Factory{factory{p: p}},
		NewHost:   func(string) v1.Host { return fakeHost{} },
		Taken:     func(string) bool { return true },
	})
	rec, _ := r.Record("dup")
	if rec.State != StateStopped || rec.Err == nil {
		t.Fatalf("撞名应拒载：state=%v err=%v", rec.State, rec.Err)
	}
}

func TestEnableDisableRoundTrip(t *testing.T) {
	p := &fakePlugin{name: "mem", provides: []string{PortMemory}}
	r := newRegistry(t, p)
	if rec, _ := r.Record("mem"); rec.State != StateStopped {
		t.Fatalf("启动后应是 stopped，得到 %v", rec.State)
	}
	if err := r.SetEnabled(context.Background(), "mem", true); err != nil {
		t.Fatalf("启用失败：%v", err)
	}
	rec, _ := r.Record("mem")
	if rec.State != StateActive || rec.Guard == nil {
		t.Fatalf("应是 active + 有守卫，得到 %v guard=%v", rec.State, rec.Guard)
	}
	if _, _, ok := r.Active("mem"); !ok {
		t.Fatal("Active 取不到实例")
	}
	if err := r.SetEnabled(context.Background(), "mem", true); err != nil {
		t.Fatalf("重复启用不该报错：%v", err)
	}
	if err := r.SetEnabled(context.Background(), "mem", false); err != nil {
		t.Fatalf("停用失败：%v", err)
	}
	rec, _ = r.Record("mem")
	if rec.State != StateStopped || p.shutdowns != 1 {
		t.Fatalf("停用后 state=%v shutdowns=%d", rec.State, p.shutdowns)
	}
	if _, _, ok := r.Active("mem"); ok {
		t.Fatal("停用后不该再取到实例")
	}
	// 重新启用＝新实例
	if err := r.SetEnabled(context.Background(), "mem", true); err != nil {
		t.Fatalf("重新启用失败：%v", err)
	}
	if p.started != 2 {
		t.Fatalf("重新启用要造新实例，started=%d", p.started)
	}
}

func TestStartFailureLeavesStopped(t *testing.T) {
	p := &fakePlugin{name: "bad", provides: []string{PortMemory}, startErr: errors.New("boom")}
	r := newRegistry(t, p)
	if err := r.SetEnabled(context.Background(), "bad", true); err == nil {
		t.Fatal("Start 失败应报错")
	}
	rec, _ := r.Record("bad")
	if rec.State != StateStopped || rec.Err == nil {
		t.Fatalf("state=%v err=%v", rec.State, rec.Err)
	}
	if p.shutdowns != 1 {
		t.Fatalf("失败路径要收尾，shutdowns=%d", p.shutdowns)
	}
}

func TestProvidesMismatchRejected(t *testing.T) {
	p := &fakePlugin{name: "liar", provides: []string{PortLLM}, implement: []string{PortTool}}
	r := newRegistry(t, p)
	err := r.SetEnabled(context.Background(), "liar", true)
	if err == nil {
		t.Fatal("声明 llm 但没实现应拒载")
	}
	if !strings.Contains(err.Error(), "plugin.t1.provides_mismatch") &&
		!strings.Contains(err.Error(), "llm") {
		t.Fatalf("报因应指向供给面不符：%v", err)
	}
	if rec, _ := r.Record("liar"); rec.State != StateStopped {
		t.Fatalf("state=%v", rec.State)
	}
}

// TestEmptyToolListAllowed 声明 tool 但这一轮零工具是合法状态（D340）：工具面按轮给，
// 空库、游标停在根都会这样。拿启用时的一次采样当拒载理由，会把首次启用的记忆插件误杀。
func TestEmptyToolListAllowed(t *testing.T) {
	p := &fakePlugin{name: "empty", provides: []string{PortTool}}
	r := newRegistry(t, p)
	if err := r.SetEnabled(context.Background(), "empty", true); err != nil {
		t.Fatalf("零工具不该拒载：%v", err)
	}
	if rec, _ := r.Record("empty"); rec.State != StateActive {
		t.Fatalf("state=%v", rec.State)
	}
	if p.shutdowns != 0 {
		t.Fatalf("启用的插件不该被收尾，shutdowns=%d", p.shutdowns)
	}
}

func TestMemoryIsExclusive(t *testing.T) {
	a := &fakePlugin{name: "a", provides: []string{PortMemory}}
	b := &fakePlugin{name: "b", provides: []string{PortMemory}}
	r := newRegistry(t, a, b)
	if err := r.SetEnabled(context.Background(), "a", true); err != nil {
		t.Fatal(err)
	}
	if err := r.SetEnabled(context.Background(), "b", true); err == nil {
		t.Fatal("两套记忆同时启用应被拒")
	}
	if err := r.SetEnabled(context.Background(), "a", false); err != nil {
		t.Fatal(err)
	}
	if err := r.SetEnabled(context.Background(), "b", true); err != nil {
		t.Fatalf("前一个记忆插件停用后应能启用：%v", err)
	}
}

func TestDrainingRefusesNewCalls(t *testing.T) {
	p := &fakePlugin{name: "slow", provides: []string{PortMemory}}
	r := newRegistry(t, p)
	if err := r.SetEnabled(context.Background(), "slow", true); err != nil {
		t.Fatal(err)
	}
	_, g, _ := r.Active("slow")
	if !g.Enter() {
		t.Fatal("Active 时 Enter 应成功")
	}

	done := make(chan error, 1)
	go func() { done <- r.SetEnabled(context.Background(), "slow", false) }()

	// 等 Draining 的观测点是守卫本身：Registry 的锁在停用全程被持有，探 Record/Auth
	// 会一直阻塞到停完，探不出中间态。
	deadline := time.Now().Add(time.Second)
	closedEntry := false
	for time.Now().Before(deadline) {
		if g.Enter() {
			g.Leave()
			time.Sleep(time.Millisecond)
			continue
		}
		closedEntry = true
		break
	}
	if !closedEntry {
		t.Fatal("停用没有关掉入口")
	}
	select {
	case err := <-done:
		t.Fatalf("在途调用没跑完就不该返回：%v", err)
	case <-time.After(20 * time.Millisecond):
	}

	g.Leave()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("停用应成功：%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("交还在途后停用没有返回")
	}
	if rec, _ := r.Record("slow"); rec.State != StateStopped {
		t.Fatalf("state=%v", rec.State)
	}
}

func TestDrainTimeoutStillShutsDown(t *testing.T) {
	p := &fakePlugin{name: "stuck", provides: []string{PortMemory}}
	r := New(Options{
		Factories:    []v1.Factory{factory{p: p}},
		NewHost:      func(string) v1.Host { return fakeHost{} },
		DrainTimeout: 10 * time.Millisecond,
	})
	if err := r.SetEnabled(context.Background(), "stuck", true); err != nil {
		t.Fatal(err)
	}
	_, g, _ := r.Active("stuck")
	g.Enter() // 故意不交还：模拟卡住的调用
	if err := r.SetEnabled(context.Background(), "stuck", false); err != nil {
		t.Fatalf("等待超时也该完成停用：%v", err)
	}
	if p.shutdowns != 1 {
		t.Fatalf("超时也照常 Shutdown，shutdowns=%d", p.shutdowns)
	}
	g.Leave()
}

func TestShutdownErrorSurfaces(t *testing.T) {
	p := &fakePlugin{name: "err", provides: []string{PortMemory}, shutErr: errors.New("half closed")}
	r := newRegistry(t, p)
	if err := r.SetEnabled(context.Background(), "err", true); err != nil {
		t.Fatal(err)
	}
	if err := r.SetEnabled(context.Background(), "err", false); err == nil {
		t.Fatal("Shutdown 失败要冒出来")
	}
	if rec, _ := r.Record("err"); rec.State != StateStopped {
		t.Fatalf("即便报错也要落到 stopped，得到 %v", rec.State)
	}
}

func TestCloseStopsEverything(t *testing.T) {
	a := &fakePlugin{name: "a", provides: []string{PortMemory}}
	b := &fakePlugin{name: "b", provides: []string{PortTool}, tools: []v1.ToolSpec{{Name: "t"}}}
	r := newRegistry(t, a, b)
	for _, n := range []string{"a", "b"} {
		if err := r.SetEnabled(context.Background(), n, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close 报错：%v", err)
	}
	for _, rec := range r.List() {
		if rec.State != StateStopped {
			t.Fatalf("%s 还在 %v", rec.Name, rec.State)
		}
	}
}
