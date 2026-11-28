package main

// 数据目录删除入口（实现 uigui.DataDirAdmin，消费方定义接口）：设置窗「常规」页存储区
// 的删除请求。这里只标记待删——进程正往 profile 的 logs 写日志、密钥库开着句柄，Windows
// 下运行中删不掉正被占用的文件；真删除在 run() 收尾的最后一步（runPendingWipe，注册为
// run 的第一个 defer = 全部收尾 defer 之后才跑），尽力而为，删不掉的留在原处不阻断退出。

import (
	"os"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// dataDirAdmin uigui.DataDirAdmin 的装配根实现。
type dataDirAdmin struct {
	w *wiring
}

var _ uigui.DataDirAdmin = (*dataDirAdmin)(nil)

// Path 数据根位置（设置窗提示文案展示用）。
func (a *dataDirAdmin) Path() string { return a.w.dir }

// RequestWipe 标记「退出时删除整个数据目录」；置位即生效，UI 随后走菜单退出路径。
func (a *dataDirAdmin) RequestWipe() error {
	a.w.wipePending.Store(true)
	return nil
}

// runPendingWipe 退出链的最后一步：删整个数据根。注册为 run 的第一个 defer（后进先出
// = 最后跑），保证日志句柄、密钥库、审计都关完之后才动目录。删除失败（文件仍被占用等）
// 留残件在原处：目录已不可恢复地少了一部分，如实保留比假装干净更好，且不阻断退出。
func (w *wiring) runPendingWipe() {
	if !w.wipePending.Load() || w.dir == "" {
		return
	}
	_ = os.RemoveAll(w.dir)
}
