// Command echocmd 是 terminal 回调测试里的「被代跑的命令」（只用于测试）：把参数、cwd、
// 指定的环境变量打到 stdout，再往 stderr 打一行，然后按 ECHOCMD_EXIT 退出。
//
// 参数全由环境变量给，这样测试不必为每种情形另编一个程序：
//
//	ECHOCMD_ECHO     逗号分隔的变量名，逐个打成 env:NAME=VALUE
//	ECHOCMD_BULK     打这么多行，用来验证缓冲从头部截断
//	ECHOCMD_SLEEP_MS 拖延这么久再退出（测 kill 与 wait_for_exit）
//	ECHOCMD_EXIT     退出码（缺省 0）
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	fmt.Printf("out:%s\n", strings.Join(os.Args[1:], " "))
	if wd, err := os.Getwd(); err == nil {
		fmt.Printf("cwd:%s\n", wd)
	}
	for _, name := range strings.Split(os.Getenv("ECHOCMD_ECHO"), ",") {
		if name == "" {
			continue
		}
		fmt.Printf("env:%s=%s\n", name, os.Getenv(name))
	}
	if n, err := strconv.Atoi(os.Getenv("ECHOCMD_BULK")); err == nil {
		for i := 0; i < n; i++ {
			fmt.Printf("line %03d: abcdefghij\n", i)
		}
	}
	fmt.Fprintln(os.Stderr, "err:boom")
	if ms, err := strconv.Atoi(os.Getenv("ECHOCMD_SLEEP_MS")); err == nil && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	code, _ := strconv.Atoi(os.Getenv("ECHOCMD_EXIT"))
	os.Exit(code)
}
