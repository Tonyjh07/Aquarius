package uigui

// avatar_cache.go 角色卡头像的异步解码缓存（§5.17）：设置窗「角色」页与会话历史窗左栏
// 各持一份。
//
// **为什么不做包级单例**：两个窗分属不同 goroutine（主窗事件循环 / 次窗 goroutine，
// §15.7），共享一份可变态会构成数据竞争。故缓存类型在这里共享、**实例**由每个消费窗口
// 各自构造，只由该窗 goroutine 读写；跨 goroutine 的只有「后台解码结果经通道回灌 +
// invalidate 触发重绘」。
//
// 三条纪律（同 imgthumb.go）：解码不占 UI 线程（后台 goroutine + 结果通道）、同一路径只解
// 一次（在途记账）、失败留墓碑不重试。

import "os"

// avatarMsg 后台头像解码结果（goroutine → 帧首）。
type avatarMsg struct {
	path  string
	thumb *thumbSrc // nil = 解码失败（记墓碑，不重试）
}

// avatarCache 头像缓存：键 = 头像文件绝对路径。invalidate 在结果回灌后触发本窗重绘
// （次窗传 w.Invalidate；本窗已有帧时留空亦可）。
type avatarCache struct {
	side       int // 解码边长（px）
	invalidate func()

	srcs map[string]*thumbSrc
	busy map[string]bool
	fail map[string]bool
	res  chan avatarMsg
}

// newAvatarCache 构造一份缓存（构造发生在窗口 goroutine 启动前，之后无并发）。
func newAvatarCache(side int, invalidate func()) *avatarCache {
	return &avatarCache{
		side:       side,
		invalidate: invalidate,
		srcs:       map[string]*thumbSrc{},
		busy:       map[string]bool{},
		fail:       map[string]bool{},
		res:        make(chan avatarMsg, 32),
	}
}

// get 取头像缩略图；未就绪则登记并后台解码（返回 nil，本帧画兜底）。仅本窗 goroutine 调用。
func (c *avatarCache) get(path string) *thumbSrc {
	if path == "" {
		return nil
	}
	if t, ok := c.srcs[path]; ok {
		return t
	}
	if c.fail[path] || c.busy[path] {
		return nil
	}
	c.busy[path] = true
	go func() {
		thumb := decodeAvatarFile(path, c.side)
		select {
		case c.res <- avatarMsg{path: path, thumb: thumb}:
		default:
			// 通道满：结果丢弃。帧首每帧排空，容量 32 远大于同屏头像数；万一发生只是
			// 这一张不显示（在途记账保持，不重试也不竞态）。
		}
		if c.invalidate != nil {
			c.invalidate()
		}
	}()
	return nil
}

// drain 帧首收下后台结果（入缓存 / 记墓碑），排空通道。仅本窗 goroutine 调用。
func (c *avatarCache) drain() {
	for {
		select {
		case m := <-c.res:
			delete(c.busy, m.path)
			if m.thumb != nil {
				c.srcs[m.path] = m.thumb
			} else {
				c.fail[m.path] = true
			}
		default:
			return
		}
	}
}

// decodeAvatarFile 后台读文件 + 解码（后台 goroutine；只读不可变输入，不碰 UI 状态）。
func decodeAvatarFile(path string, side int) *thumbSrc {
	rc, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer rc.Close()
	return loadThumbSide(rc, side, thumbMaxPixels)
}
