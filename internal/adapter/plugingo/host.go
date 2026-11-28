package plugingo

import (
	"context"
	"io"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// host.go 把内核服务包成 v1 Host（plugin-guide.md §5）。首发八项，每项都只是转发—
// 这里刻意不放任何策略：确认档、密钥解析、去重语义都在各自的内核实现里，插件面只拿
// 「能做这件事的句柄」。
//
// 可选扩展（NoticePoster / LegacyMemoryHost）不在 Host 本体上：由构造器按是否给了实现
// 决定返回哪一种包装类型。断言命中才可用，未命中插件自己退回 Notify 的语义。

// HostOptions 宿主侧的实现装配。缺项按「不提供」处理：Log / Secrets / Blobs 为 nil 时
// 对应方法返回明确错误（不是 panic），这样某个插件在没接好依赖的环境里失败得好读。
type HostOptions struct {
	Plugin    string       // 插件名：日志上下文段与两个目录的归属都靠它
	Log       port.Log     // 五段式日志（一处写出，D276）
	Secrets   port.Secrets // secret:<name> 解析
	Blobs     port.AttachmentStore
	DataDir   string                         // 插件私有持久目录（随 profile，跨重启存活）
	WorkDir   string                         // 插件私有临时目录（随 profile，关机清理）
	Locale    string                         // 内核当前界面语言 "zh" / "en"（空 = 不给）
	Confirmer port.Confirmer                 // 内核确认链
	Notify    func(title, body string) error // 系统通知的发送实现；nil = 静默
	// Notice 非 nil 时返回的 Host 额外实现 NoticePoster（消息流内联提示）。
	Notice func(level, text string, kv ...any)
	// LegacyMemory 非 nil 时返回的 Host 额外实现 LegacyMemoryHost（内置记忆快照）。
	LegacyMemory func(ctx context.Context) (v1.LegacyMemorySnapshot, error)
	// Environ 非 nil 时返回的 Host 额外实现 UIEnviron（D352）：界面环境只读快照。
	// 存函数而不是快照：深浅色、DPI 都会变，插件每次问都要拿到当时的值。
	Environ func() v1.UIEnvironment
}

// Host v1 Host 的实现：八项内核服务的转发壳。
type Host struct {
	opts HostOptions
}

// NewHost 按 HostOptions 造 Host。返回的是 v1.Host 而不是 *Host：需要时换成带可选扩展
// 的包装类型，插件对句柄做 type assertion 才有意义。
func NewHost(opts HostOptions) v1.Host {
	if opts.Notice == nil && opts.LegacyMemory == nil && opts.Environ == nil {
		return &Host{opts: opts}
	}
	return &hostExt{Host: Host{opts: opts}}
}

// hostExt 带可选扩展的 Host。三个扩展都是「实现即被探测到」，不需要开关位。
type hostExt struct {
	Host
}

var (
	_ v1.Host             = (*Host)(nil)
	_ v1.NoticePoster     = (*hostExt)(nil)
	_ v1.LegacyMemoryHost = (*hostExt)(nil)
	_ v1.UIEnviron        = (*hostExt)(nil)
)

// Logger 插件日志出口。上下文段由宿主填成 plugin:<name>，插件改不了——日志按来源
// 归组靠这一格。event / act 是插件自己写的句子，内核的 i18n 查不到就原样输出（插件
// 文案不进内核文案表）。
func (h *Host) Logger() v1.Logger { return pluginLogger{log: h.opts.Log, name: h.opts.Plugin} }

func (h *Host) Secrets() v1.SecretResolver { return secretResolver{secrets: h.opts.Secrets} }

func (h *Host) Blobs() v1.BlobStore { return blobStore{blobs: h.opts.Blobs} }

// DataDir 持久目录，WorkDir 临时目录：两个都随 profile，生存期不同（D341）。宿主没给时
// 返回空串而不是报错——目录是「有就有」的能力，插件自己降级成不留存。
func (h *Host) DataDir() string { return h.opts.DataDir }

func (h *Host) WorkDir() string { return h.opts.WorkDir }

// Locale 内核界面语言。契约里是值不是访问器：语言整次运行内不变（D275），宿主在造
// Host 时把当时生效的那种填进来，插件不必每轮问。
func (h *Host) Locale() string { return h.opts.Locale }

// Confirm 走内核确认链：请求转成 port 形状（Op / Risk 的零值语义两边一致：Has* 位说了算），
// 应答再转回来。插件自造确认 UI 不算数，所以这里没有旁路。
func (h *Host) Confirm(ctx context.Context, req v1.ConfirmRequest) (v1.ConfirmAnswer, error) {
	if h.opts.Confirmer == nil {
		return v1.ConfirmAnswer{}, errNoService("confirm")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// 插件带来的 ctx 可能带着 v1 的会话键：搬到 port 的键上，确认链与审计才认得出是谁。
	ans, err := h.opts.Confirmer.Confirm(toPortSession(ctx), toPortConfirmRequest(req))
	if err != nil {
		return v1.ConfirmAnswer{}, err
	}
	return toV1ConfirmAnswer(ans), nil
}

// Notify 系统通知出口。发送失败只记日志不返回错误：契约里 Notify 没有错误返回，
// 提示送达失败不该打断一条已经完成的回答（与 notify 输出器的取舍一致）。
func (h *Host) Notify(level, text string) {
	if h.opts.Notify == nil {
		return
	}
	title := "Aquarius"
	if strings.TrimSpace(level) != "" {
		title += " · " + strings.TrimSpace(level)
	}
	if err := h.opts.Notify(title, text); err != nil && h.opts.Log != nil {
		h.opts.Log(port.LogWarn, "plugin:"+h.opts.Plugin, "log.plugin.notify_failed",
			"log.act.skipped", "name", h.opts.Plugin, "err", err)
	}
}

// PostNotice 消息流内联提示（可选扩展）。
func (x *hostExt) PostNotice(level, text string) {
	if x.opts.Notice != nil {
		x.opts.Notice(level, text)
	}
}

// LegacyMemory 内置记忆的一次性只读快照（可选扩展）。
func (x *hostExt) LegacyMemory(ctx context.Context) (v1.LegacyMemorySnapshot, error) {
	if x.opts.LegacyMemory == nil {
		return v1.LegacyMemorySnapshot{}, errNoService("legacy-memory")
	}
	return x.opts.LegacyMemory(ctx)
}

// Environ 界面环境快照（可选扩展，D352）。没接线时返回零快照而不是报错：契约里零值
// 就是「什么都不知道」，插件按自己的缺省退化——为一个可选信息面让调用点写错误分支不值得。
func (x *hostExt) Environ() v1.UIEnvironment {
	if x.opts.Environ == nil {
		return v1.UIEnvironment{}
	}
	return x.opts.Environ()
}

// ---------- Logger ----------

type pluginLogger struct {
	log  port.Log
	name string
}

func (l pluginLogger) Log(level v1.LogLevel, event, act string, kv ...any) {
	if l.log == nil {
		return
	}
	// 五段里只有「上下文」由宿主填；其余原样。kv 的「名, 值」成对规则照旧由插件守。
	l.log(port.LogLevel(level), "plugin:"+l.name, event, act, kv...)
}

// ---------- Secrets ----------

type secretResolver struct{ secrets port.Secrets }

func (s secretResolver) Resolve(ctx context.Context, name string) (string, error) {
	if s.secrets == nil {
		return "", errNoService("secrets")
	}
	return s.secrets.Get(ctx, name)
}

// ---------- Blobs ----------

type blobStore struct{ blobs port.AttachmentStore }

func (b blobStore) Put(ctx context.Context, r io.Reader, mime, name string) (v1.BlobRef, error) {
	if b.blobs == nil {
		return v1.BlobRef{}, errNoService("blobs")
	}
	ref, err := b.blobs.Put(ctx, r, mime, name)
	if err != nil {
		return v1.BlobRef{}, err
	}
	return v1.BlobRef{Hash: ref.Hash, MIME: ref.MIME, Name: ref.Name, Size: ref.Size}, nil
}

func (b blobStore) Get(ctx context.Context, ref v1.BlobRef) (io.ReadCloser, error) {
	if b.blobs == nil {
		return nil, errNoService("blobs")
	}
	return b.blobs.Get(ctx, *toPortBlobRef(&ref))
}

func (b blobStore) Stat(ctx context.Context, ref v1.BlobRef) (bool, error) {
	if b.blobs == nil {
		return false, errNoService("blobs")
	}
	return b.blobs.Stat(ctx, *toPortBlobRef(&ref))
}

// errNoService 宿主没接某项服务时的报因。用一句话而不是哨兵：插件侧没有可判定的分类，
// 能读懂「这台宿主没给你这一项」就够了。
type errNoService string

func (e errNoService) Error() string { return "host: service not wired: " + string(e) }
