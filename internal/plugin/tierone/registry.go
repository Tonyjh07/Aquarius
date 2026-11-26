package tierone

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// defaultDrainTimeout 停用时等在途调用的默认上限（§7 第 4 条「内核限时」）。
const defaultDrainTimeout = 10 * time.Second

// Options 注册表的装配参数。
type Options struct {
	// Factories 编译进二进制的插件入口（main 显式列出，§7「注册表归宿主」）。
	Factories []v1.Factory
	// NewHost 造一个插件实例的 Host，参数是注册表手里的清单快照。传清单而不只传名字：
	// Host 的构造有时要看声明（记忆插件才拿得到迁移素材），而此刻注册表锁在 enable 里，
	// 回调再反查注册表就死锁——清单在锁内是现成的。
	NewHost func(v1.Manifest) v1.Host
	// Taken 名字是否已被内置工具、MCP 端点占用：Tier-1 与它们共享模型的命名空间，
	// 撞名即拒载。nil = 不查。
	Taken func(name string) bool
	// Log 五段式日志出口；nil = 不记。
	Log port.Log
	// DrainTimeout 停用时等在途调用的上限；0 = 10 秒。
	DrainTimeout time.Duration
}

// Registry Tier-1 插件的注册表与状态机。所有状态变更都在一把锁里串行——启停是低频
// 人工操作，值得用「简单」换掉「并发正确性证明」。
type Registry struct {
	mu      sync.Mutex
	opts    Options
	order   []string
	entries map[string]*entry
}

// entry 一个插件的内部状态（对外只暴露 Record 快照）。
type entry struct {
	factory  v1.Factory
	manifest v1.Manifest
	state    State
	// validateErr 启动校验的结论：非 nil 的插件永远停在 Stopped，enable 会原样报因。
	validateErr error
	err         error
	inst        v1.Instance
	guard       *Guard
}

// New 造注册表并完成启动校验：每个插件走一遍清单检查，通过的记 Validated 后落到
// Stopped（等启停决定），不通过的停在 Stopped 并记因。单个插件失败不影响其他插件。
func New(opts Options) *Registry {
	if opts.DrainTimeout <= 0 {
		opts.DrainTimeout = defaultDrainTimeout
	}
	r := &Registry{opts: opts, entries: make(map[string]*entry, len(opts.Factories))}
	for _, f := range opts.Factories {
		if f == nil {
			continue
		}
		r.register(f)
	}
	return r
}

// register 登记一个工厂并校验它的清单。重名按「后到的拒载」处理——先登记者已经在
// /plugin 里露面，不因为后来者的错把它顶掉。
func (r *Registry) register(f v1.Factory) {
	m := f.Manifest()
	e := &entry{factory: f, manifest: m, state: StateRegistered}
	if _, dup := r.entries[m.Name]; dup && m.Name != "" {
		e.validateErr = errors.New(i18n.Tf("plugin.t1.name_taken", "name", m.Name))
	} else if err := validateManifest(m, r.opts.Taken); err != nil {
		e.validateErr = err
	}
	if e.validateErr != nil {
		e.state = StateStopped
		e.err = e.validateErr
	} else {
		// 校验通过：跨过 Validated 直接落到 Stopped——「可启用但未启用」是启动后的常态，
		// Validated 只是那一瞬的中间态。
		e.state = StateStopped
	}
	if m.Name != "" {
		r.order = append(r.order, m.Name)
		r.entries[m.Name] = e
	}
}

// List 按登记顺序给出全部插件的快照。
func (r *Registry) List() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Record, 0, len(r.order))
	for _, name := range r.order {
		if e, ok := r.entries[name]; ok {
			out = append(out, e.record())
		}
	}
	return out
}

// Record 单个插件的快照。
func (r *Registry) Record(name string) (Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	if !ok {
		return Record{}, false
	}
	return e.record(), true
}

// Active 取一个已启用插件的实例与在途守卫（装配根据此建工具、模型与命令）。
// 第三个返回值 false = 该插件现在不是 Active（供给面不该被接进内核）。
func (r *Registry) Active(name string) (v1.Instance, *Guard, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	if !ok || e.state != StateActive || e.inst == nil {
		return nil, nil, false
	}
	return e.inst, e.guard, true
}

// SetEnabled 启停一个插件（/plugin 命令、设置窗开关与启动期的配置态都走这一条）。
// 幂等：对已启用者再启用、对已停用者再停用都返回 nil。
func (r *Registry) SetEnabled(ctx context.Context, name string, on bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	if !ok {
		return errors.New(i18n.Tf("plugin.t1.not_found", "name", name))
	}
	if on {
		return r.enable(ctx, e)
	}
	return r.disable(ctx, e)
}

// enable Stopped → Active：造新实例、自检、Start。任何一步失败都回到 Stopped 并记因，
// 不做半装载（实例该收的尾在失败路径上就地收掉）。
func (r *Registry) enable(ctx context.Context, e *entry) error {
	if e.state == StateActive {
		return nil
	}
	if e.validateErr != nil {
		return e.validateErr
	}
	if provides(e.manifest, PortMemory) {
		if other := r.memoryOwner(e.manifest.Name); other != "" {
			return r.refuse(e, errors.New(i18n.Tf("plugin.t1.memory_busy",
				"name", e.manifest.Name, "other", other)))
		}
	}
	inst, err := e.factory.Create(r.opts.NewHost(e.manifest))
	if err != nil {
		return r.refuse(e, err)
	}
	if err := checkProvides(inst, e.manifest.Provides); err != nil {
		return r.refuse(e, shutdownQuietly(inst, err))
	}
	if err := inst.Start(); err != nil {
		return r.refuse(e, shutdownQuietly(inst, err))
	}
	// 不再校验「声明 tool 却一件都没给」：工具面按轮给（D340），空库或游标停在根时
	// 零工具是正常状态，拿一次采样当拒载理由会误杀首次启用的记忆插件。
	e.inst = inst
	e.guard = &Guard{}
	e.state = StateActive
	e.err = nil
	r.log(port.LogInfo, e.manifest.Name, "log.plugin.t1_active", "log.act.ready",
		"name", e.manifest.Name, "provides", strings.Join(e.manifest.Provides, ","))
	return nil
}

// disable Active/Draining → Stopped：先关入口（拒绝新调用），再等在途跑完，最后
// Shutdown。等待超时也照常 Shutdown——卡住的调用不该让停用永远停在半路。
func (r *Registry) disable(ctx context.Context, e *entry) error {
	if e.state != StateActive && e.state != StateDraining {
		e.state = StateStopped
		return nil
	}
	e.state = StateDraining
	e.guard.close()
	waitCtx, cancel := context.WithTimeout(ctx, r.opts.DrainTimeout)
	defer cancel()
	waitErr := e.guard.wait(waitCtx)
	shutErr := e.inst.Shutdown(waitCtx)
	e.inst, e.guard = nil, nil
	e.state = StateStopped
	e.err = errors.Join(waitErr, shutErr)
	r.log(port.LogInfo, e.manifest.Name, "log.plugin.t1_stopped", "log.act.stopped", "name", e.manifest.Name)
	if shutErr != nil {
		return errors.New(i18n.Tf("plugin.t1.shutdown", "name", e.manifest.Name, "err", shutErr))
	}
	return nil
}

// Close 停用全部活着的插件（退出前的收尾）。逐个停，一个失败不拦后面的。
func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	names := make([]string, 0, len(r.order))
	for _, name := range r.order {
		if e := r.entries[name]; e != nil && e.state == StateActive {
			names = append(names, name)
		}
	}
	r.mu.Unlock()

	var errs []error
	for _, name := range names {
		if err := r.SetEnabled(ctx, name, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// memoryOwner 当前拥有记忆的插件名（不含 self）。空 = 没人拥有。
func (r *Registry) memoryOwner(self string) string {
	for _, name := range r.order {
		e := r.entries[name]
		if e == nil || name == self || e.state != StateActive {
			continue
		}
		if provides(e.manifest, PortMemory) {
			return name
		}
	}
	return ""
}

// refuse 启用失败：落到 Stopped、记因、记日志，原样返回。
func (r *Registry) refuse(e *entry, err error) error {
	e.state = StateStopped
	e.inst, e.guard = nil, nil
	e.err = err
	r.log(port.LogWarn, e.manifest.Name, "log.plugin.t1_refused", "log.act.skip", "name", e.manifest.Name, "err", err)
	return err
}

// log 写一条运行日志（未接日志时静默）。上下文段由这里拼成 plugin:<name>，与插件自己
// 经 Host.Logger 写出的日志落在同一组里。
func (r *Registry) log(level port.LogLevel, name, event, act string, kv ...any) {
	if r.opts.Log == nil {
		return
	}
	r.opts.Log(level, "plugin:"+name, event, act, kv...)
}

// record 内部状态 → 对外快照。
func (e *entry) record() Record {
	return Record{
		Name:     e.manifest.Name,
		State:    e.state,
		Provides: append([]string(nil), e.manifest.Provides...),
		Err:      e.err,
		Guard:    e.guard,
	}
}

// shutdownQuietly 失败路径上的收尾：Shutdown 的错误并进原错误，原错误才是拒载原因。
func shutdownQuietly(inst v1.Instance, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDrainTimeout)
	defer cancel()
	if err := inst.Shutdown(ctx); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}
