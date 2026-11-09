package platform

import (
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// Plat 平台实现句柄：主窗句柄与窗口线程投递槽、外壳（托盘/热键/菜单）线程状态都收在
// 这里（原 uigui 的包级全局态），因此可多实例构造、可在测试里替换（D111 修订①）。
//
// 并发口径不变（§15.6）：修改性调用一律经 onWindowThread（铁律 1）送窗口线程；查询类
// 直接调用。字段用原子类型跨 goroutine 存取。
type Plat struct {
	host Host
	cfg  Config

	// mainHWND 主窗句柄（事件循环写、窗口线程/后台线程读）。
	mainHWND atomic.Uintptr
	// mainRun 主窗 Window.Run 投递槽（runWindow 起手写入）。
	mainRun atomic.Pointer[RunFunc]

	// menuNotice 非 Windows 的「无原生菜单」提示已发标记（一次性，见 shell_other.go）。
	menuNotice atomic.Bool

	// dlgReq 通用文件框（单选打开 / 保存）的请求体：请求线程写入、shell 线程取走呈现。
	// 走原子槽而不是消息参数——过滤器、标题、预填名塞不进 wParam/lParam。
	dlgReq atomic.Pointer[dlgRequest]
	// dlgBusy 已有一个文件框在等：对话框挂在 HWND_MESSAGE 上，模态期间主窗并不会被禁用，
	// 用户仍能再点一次「导入」/「导出」。没有这个标记时，第二次点击会覆盖请求体并再投一条
	// 消息，于是要么叠出第二个框，要么第二个框拿到被改写的请求（用途都可能变了）。
	dlgBusy atomic.Bool
	// dlgNotice 非 Windows 的「无文件框命令」提示已发标记（一次性，见 filedlg_other.go）。
	dlgNotice atomic.Bool
}

// dlgRequest 通用文件框请求（打开与保存共用一个槽：同一时刻只可能有一个框在等）。
type dlgRequest struct {
	save        bool
	purpose     FilePurpose
	title       string
	name        string // 保存框预填文件名
	defExt      string
	filters     []FileFilter
	filterIndex int // 保存框打开时预选的那一档（0 起）
}

// New 构造平台实现（各平台方法见同目录 *_windows.go / *_other.go）。
func New(cfg Config) *Plat {
	setLogSink(cfg.Log)
	return &Plat{cfg: cfg}
}

// SetHost 绑定回调宿主（uigui 构造后立即调用；nil = 无宿主，回调静默丢弃）。
func (p *Plat) SetHost(h Host) { p.host = h }

// Config 返回构造选项（只读）。
func (p *Plat) Config() Config { return p.cfg }

// logf 平台层诊断走运行日志（D288）：上下文恒为 `ui`，未注入 = 不记。
func (p *Plat) logf(lv port.LogLevel, event, act string, kv ...any) {
	if p.cfg.Log != nil {
		p.cfg.Log(lv, "ui", event, act, kv...)
	}
}

// 主窗 DIB 与 ULW 状态本身就是进程级单例（`mbits` / `mainPresentN`），释放告警要从
// 无接收者的包级函数里发，于是日志出口也留一份包级引用。多次 New 会覆盖——与那些
// 单例同一假设，测试里不并发断言日志。
var (
	pkgLogMu  sync.RWMutex
	pkgLogOut port.Log
)

func setLogSink(l port.Log) {
	pkgLogMu.Lock()
	pkgLogOut = l
	pkgLogMu.Unlock()
}

// pkgLogf 包级诊断（位图释放）走日志；未注入 = 不记。
func pkgLogf(lv port.LogLevel, event, act string, kv ...any) {
	pkgLogMu.RLock()
	l := pkgLogOut
	pkgLogMu.RUnlock()
	if l != nil {
		l(lv, "ui", event, act, kv...)
	}
}

// Notice 向宿主发降级提示（无宿主 = 丢弃）。
func (p *Plat) Notice(msg string) {
	if p.host != nil {
		p.host.Notice(msg)
	}
}

// noticeOnce 同类降级提示只发一次（缺失能力属启动期一次性告知，不随交互重复刷转写区）。
func (p *Plat) noticeOnce(sent *atomic.Bool, msg string) {
	if sent.CompareAndSwap(false, true) {
		p.Notice(msg)
	}
}

// onWindowThread 把修改性调用送到主窗窗口线程执行（§15.6 铁律 1）。窗口未就绪（理论上
// 不发生）时直接执行兜底。
func (p *Plat) onWindowThread(f func()) {
	if r := p.mainRun.Load(); r != nil {
		(*r)(f)
		return
	}
	f()
}

// hotkeySetting 当前快捷键配置：宿主现取（空 = 未配置，调用方回落默认）。
func (p *Plat) hotkeySetting() string {
	if p.host != nil {
		if s := p.host.HotkeySetting(); s != "" {
			return s
		}
	}
	return p.cfg.Hotkey
}

// darkFromCommands 逐个试桌面环境的深浅色查询命令（非 Windows 用；Windows 走注册表）。
// 首个成功执行的命令给出结论：输出含 dark/Dark/true ⇒ 深色；无命令可用 ⇒ 浅色保守回落。
func darkFromCommands(cmds [][]string) bool {
	for _, c := range cmds {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		out, err := exec.Command(c[0], c[1:]...).Output()
		if err != nil {
			continue // 命令存在但查询失败：试下一条（或落到浅色）
		}
		s := strings.ToLower(strings.TrimSpace(string(out)))
		return strings.Contains(s, "dark") || s == "true"
	}
	return false
}

// unsupported 组装「本平台不支持 X」的降级提示（用户可见，中文）。
func unsupported(what string) string {
	return i18n.Tf("ui.platform.downgrade", "os", runtime.GOOS, "feature", what)
}
