package main

// doctorcli.go `-doctor`：不开界面的体检（§7.14/D306）。
//
// 与 `/doctor` 共用同一份检查（healthProbe）与排版（app.RenderDoctor），只换装配：
// 命令行入口不建目录、不开日志、不启动插件宿主。体检的纪律是只读，这条路径才跑得进
// 脚本、远程会话和没有图形界面的机器。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// runDoctorMode 命令行体检：解析目录与 profile，跑七项，把结论写到 stdout。
//
// 退出码：有异常项 1，其余 0。注意项仍是 0——「还能用」与「不能用」在脚本里必须分得开，
// 而阈值本来就是「离出事不远」的提示，让定时体检天天报警等于让人关掉定时体检。
func runDoctorMode(dataDir, profileFlag string, stdout, stderr io.Writer) int {
	dir, err := dataDirPath(dataDir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	profile, err := doctorProfile(dir, profileFlag)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	profileDir := filepath.Join(dir, "profiles", profile)
	w := &wiring{
		profileDir: profileDir,
		cfgPath:    filepath.Join(profileDir, "config.json"),
		sessionDir: filepath.Join(profileDir, "conversations"),
	}
	// 密钥库绑的是目录而不是文件（库文件在首次写入时才出现）：目录不在就是还没建过库，
	// 交给体检报「没有库」，不在这里造一个空目录。
	if st, serr := os.Stat(profileDir); serr == nil && st.IsDir() {
		if bin, berr := secretbin.New(profileDir); berr == nil {
			w.secretsBin = bin
		}
	}
	// 语言与启动同一条链（D275）：环境变量 > 配置 > 系统。配置读不出来时退回系统语言——
	// 配置坏掉那句解释由「配置解析」一项给出，体检不该自己先崩在选语言上。
	doctorLanguage(os.Getenv("AQUARIUS_UI_LANG"), w.cfgPath)
	checks := healthProbe{w: w, cli: true}.Probe(context.Background())
	fmt.Fprintln(stdout, app.RenderDoctor(checks))
	return doctorExitCode(checks)
}

// doctorExitCode 有异常项返回 1。注意项不算——注意项说的是「还能用，但离出事不远」。
func doctorExitCode(checks []app.HealthCheck) int {
	for _, c := range checks {
		if c.State == app.HealthFail {
			return 1
		}
	}
	return 0
}

// doctorProfile 只读地解析 profile 名：`--profile` > 根 config.json 指针 > default。
//
// 与 resolveProfile 的差别只在「不改写磁盘」：首次运行时那边写指针指向 default，体检
// 一个文件都不动，指针不在就按 default 查——查到什么算什么。
func doctorProfile(dir, flagName string) (string, error) {
	if flagName != "" {
		if err := validProfileName(flagName); err != nil {
			return "", err
		}
		avail := listProfiles(dir)
		if !slices.Contains(avail, flagName) {
			joined := strings.Join(avail, ", ")
			if joined == "" {
				joined = i18n.T("ui.common.none")
			}
			return "", fmt.Errorf("%s", i18n.Tf("cmd.profile.err.not_found",
				"name", flagName, "available", joined))
		}
		return flagName, nil
	}
	pointerPath := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(pointerPath)
	if errors.Is(err, os.ErrNotExist) {
		return defaultProfileName, nil
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.Tf("cmd.profile.err.read_pointer", "path", pointerPath), err)
	}
	return parsePointerFile(pointerPath, data)
}

// doctorLanguage 定界面语言：`AQUARIUS_UI_LANG` > 配置的 `ui.lang` > 系统语言。
//
// 写错的值按 auto 算：启动时那条链会为写错的语言值报错退出，体检的活儿却是把别处的问题报出来，
// 不该因为一个语言值退不出结论。
func doctorLanguage(env, cfgPath string) {
	sel := strings.TrimSpace(env)
	if sel == "" {
		if cfg, err := loadConfig(cfgPath); err == nil {
			sel = cfg.UI.Lang
		}
	}
	parsed, err := i18n.Parse(sel)
	if err != nil {
		parsed = i18n.SelectionAuto
	}
	i18n.Use(parsed)
}
