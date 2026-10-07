package platform

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
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
}

// New 构造平台实现（各平台方法见同目录 *_windows.go / *_other.go）。
func New(cfg Config) *Plat {
	return &Plat{cfg: cfg}
}

// SetHost 绑定回调宿主（uigui 构造后立即调用；nil = 无宿主，回调静默丢弃）。
func (p *Plat) SetHost(h Host) { p.host = h }

// Config 返回构造选项（只读）。
func (p *Plat) Config() Config { return p.cfg }

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
	return fmt.Sprintf("当前平台（%s）暂不支持%s，功能已降级。", runtime.GOOS, what)
}
