package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// crash.go 顶层 panic 的落盘与「上次崩过」的提示。
//
// 为什么必须有：双击启动的 GUI 没有控制台（D108 自建控制台已脱离），顶层 panic 在
// 用户眼里等于「窗口一闪就没了」——没有栈、没有时间、没有版本，连从哪儿查都不知道。
// 落一份文件是唯一能在事后回答「刚才崩的是什么」的办法。
//
// 三个刻意的克制：
//   - 写盘失败不改变退出码。崩溃已经发生，报告写不进去不能再把退出码改得更奇怪。
//   - 只提示一次。同一个文件每次都弹一遍，用户很快学会无视它。
//   - 目录未定（profile 还没解析出来就崩）写临时目录，并在提示里给出真实路径——
//     「报告在哪」不能靠猜。

// crashDir 当前 profile 的日志目录（bootstrap 解析出 profile 后设置）。
// 空 = 启动太早，退到系统临时目录。
var crashDir atomic.Value

// setCrashDir 记下崩溃报告的落点（run 在 profile 就位后调用一次）。
func setCrashDir(dir string) { crashDir.Store(dir) }

// crashDirPath 报告落点：已设用设的，未设用临时目录。
func crashDirPath() string {
	if v, ok := crashDir.Load().(string); ok && v != "" {
		return v
	}
	return filepath.Join(os.TempDir(), "aquarius-crash")
}

// crashExitCode 顶层 panic 的退出码。与启动失败（1）分开：脚本里能一眼分清
// 「配置写错了」和「程序自己崩了」。
const crashExitCode = 2

// guard 跑一次主流程并把顶层 panic 落盘；返回进程退出码。
func guard(fn func() int, stderr io.Writer) (code int) {
	code = 1 // 未执行到 fn 就 panic 的兜底（defer 里会被覆盖）
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		path, err := writeCrash(r)
		var msg string
		if err != nil {
			msg = i18n.Tf("cmd.crash.write_failed", "error", err.Error())
		} else {
			msg = i18n.Tf("cmd.crash.saved", "path", path)
		}
		fmt.Fprintln(stderr, msg)
		notifyFatal(msg) // 双击启动的唯一可见面
		code = crashExitCode
	}()
	return fn()
}

// writeCrash 把 panic 值与栈写成一份报告，返回文件路径。
func writeCrash(r any) (string, error) {
	dir := crashDirPath()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "crash-"+time.Now().Format("20060102-150405")+".txt")
	var b strings.Builder
	fmt.Fprintf(&b, "panic: %v\n\n", r)
	fmt.Fprintf(&b, "time: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "version: %s\n", version)
	fmt.Fprintf(&b, "platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "go: %s\n\n", runtime.Version())
	b.Write(debug.Stack())
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// crashAckName 记录「已提示过哪份报告」的文件名。放在同目录：报告被整目录清掉时，
// 这个标记一起消失，下次真崩了还能再提示——不留「提示过但文件已被删」的死状态。
const crashAckName = "crash-ack"

// pendingCrash 返回尚未提示过的崩溃报告路径；没有则空串。
//
// 只看最新一份：连崩几次时，用户要处理的是最近这次，而历次报告都还在目录里。
func pendingCrash(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var newest string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "crash-") || !strings.HasSuffix(name, ".txt") {
			continue
		}
		if name > newest { // 文件名带时间戳，字典序即时间序
			newest = name
		}
	}
	if newest == "" {
		return ""
	}
	ack, _ := os.ReadFile(filepath.Join(dir, crashAckName))
	if strings.TrimSpace(string(ack)) == newest {
		return ""
	}
	return filepath.Join(dir, newest)
}

// ackCrash 记下已提示的报告（写失败就下次再提示一次，不影响本次启动）。
func ackCrash(path string) {
	_ = os.WriteFile(filepath.Join(filepath.Dir(path), crashAckName),
		[]byte(filepath.Base(path)), 0o644)
}

// noticePendingCrash 启动时提示上一次崩溃的报告路径（提示过就记账，只提示一次）。
func (w *wiring) noticePendingCrash(ctx context.Context) {
	path := pendingCrash(filepath.Join(w.profileDir, "logs"))
	if path == "" {
		return
	}
	w.emitNotice(ctx, i18n.Tf("cmd.crash.notice", "path", path))
	ackCrash(path)
}
