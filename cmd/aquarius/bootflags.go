package main

// bootflags.go 启动参数的声明与解析：run 只消费结果，不再自己铺一串 flag。
//
// 拆出来是因为 run 已经贴着行数上限（funcsize_test）：参数只增不减，留在里面迟早要为
// 多一个参数去挤压装配段，而那些行的顺序本身是有意义的。

import (
	"flag"
	"fmt"
	"io"
	"runtime"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// bootFlags 一次启动的全部参数（§8 / §7.9）。
type bootFlags struct {
	data     string // 数据根目录
	profile  string // 本次运行临时覆盖根指针
	model    string // 覆盖 model.name
	baseURL  string // 覆盖 primary provider 的 base_url
	logLevel string // 覆盖 ui.log_level
	logFmt   string // 运行日志形态
	version  bool   // 打印版本后退出
	doctor   bool   // 命令行体检后退出
}

// parseFlags 解析启动参数。done = 不必继续启动（-h 或参数写错），按 code 退出。
func parseFlags(args []string, stderr io.Writer) (f bootFlags, code int, done bool) {
	flags := flag.NewFlagSet("aquarius", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&f.data, "data", "", i18n.T("cmd.boot.flag.data"))
	flags.StringVar(&f.profile, "profile", "", i18n.T("cmd.boot.flag.profile"))
	flags.StringVar(&f.model, "model", "", i18n.T("cmd.boot.flag.model"))
	flags.StringVar(&f.baseURL, "base-url", "", i18n.T("cmd.boot.flag.base_url"))
	flags.StringVar(&f.logLevel, "log-level", "", i18n.T("cmd.boot.flag.log_level"))
	flags.StringVar(&f.logFmt, "log-format", "", i18n.T("cmd.boot.flag.log_format"))
	flags.BoolVar(&f.version, "version", false, i18n.T("cmd.boot.flag.version"))
	flags.BoolVar(&f.doctor, "doctor", false, i18n.T("cmd.boot.flag.doctor"))
	if err := flags.Parse(args); err != nil {
		return bootFlags{}, 2, true
	}
	return f, 0, false
}

// early 两个「打印完就退出」的模式：`-version` 与 `-doctor`。
//
// 两者都在摘控制台之前跑：出口就是终端上的几行字，先把控制台脱离掉，字就打给一个已经
// 看不见的句柄了。`-doctor` 还额外要求只读——所以它在端口装配之前，见 runDoctorMode。
func (f bootFlags) early(stdout, stderr io.Writer) (code int, done bool) {
	switch {
	case f.version:
		fmt.Fprintf(stdout, "Aquarius %s (%s/%s, %s)\n",
			version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return 0, true
	case f.doctor:
		return runDoctorMode(f.data, f.profile, stdout, stderr), true
	}
	return 0, false
}
