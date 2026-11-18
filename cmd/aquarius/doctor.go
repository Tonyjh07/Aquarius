package main

// doctor.go `/doctor` 的装配根一半（§7.14/D306）：七项检查都在这里，因为配置路径、
// 密钥库的口令链、目录布局、卷标只有装配根知道。app 只拿结论并排版。
//
// 体检的纪律：**只读**。不发模型请求（要钱）、不连 MCP server（子进程、信任门、能力
// 授权都是副作用）、不写盘、不清理。跑一遍的结果只随环境变，不随跑的次数变。

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
)

// 阈值（§7.14）：只决定「注意 / 异常」什么时候出现，不改变任何行为，也不进 config——
// 做成配置项等于让人去调一个自己没有依据的数字。
const (
	logsWarnBytes    = 64 << 20  // logs/ 超过这个记注意
	exportsWarnBytes = 512 << 20 // exports/ 超过这个记注意
	diskWarnBytes    = 5 << 30   // 卷可用低于这个记注意
	diskFailBytes    = 1 << 30   // 卷可用低于这个记异常
)

// dirUsageMaxEntries 目录遍历的条目上限：塞了几十万文件的目录不该让体检卡住（超了标
// 「至少这么多」，见 cmd.doctor.dir_capped）。
const dirUsageMaxEntries = 20000

// healthProbe app.HealthProbe 的装配根实现（D306）。
type healthProbe struct{ w *wiring }

// Probe 跑七项检查。顺序固定：从「最上游、坏了一点都动不了」排到「只影响体验」。
func (h healthProbe) Probe(ctx context.Context) []app.HealthCheck {
	w := h.w
	return []app.HealthCheck{
		checkConfig(w),
		checkSecrets(ctx, w),
		checkSessions(ctx, w),
		checkMCP(w),
		checkDir("logs", filepath.Join(w.profileDir, "logs"), logsWarnBytes),
		checkDir("exports", filepath.Join(w.profileDir, "exports"), exportsWarnBytes),
		checkDisk(w.profileDir),
	}
}

// checkConfig 重新读一遍配置文件的磁盘现状。
//
// 读的是文件而不是启动快照：启动快照已经成功解析过了（否则启动就失败了），能坏掉的只有
// 「程序跑着的时候有人改了它」——这正是体检要抓的一类。
func checkConfig(w *wiring) app.HealthCheck {
	c := app.HealthCheck{ID: "config"}
	cfg, err := loadConfig(w.cfgPath)
	if err != nil {
		c.State = app.HealthFail
		c.Detail = i18n.Tf("cmd.doctor.config_bad", "err", err)
		c.Fix = i18n.Tf("cmd.doctor.config_fix", "path", w.cfgPath)
		return c
	}
	models := 0
	for _, p := range cfg.Model.Providers {
		models += len(p.Models)
	}
	c.Detail = i18n.Tf("cmd.doctor.config_ok", "providers", len(cfg.Model.Providers), "models", models)
	return c
}

// checkSecrets 试着解一次密钥库（Entries 会真的解密）。
//
// 解不开的两种原因分开说话：换机/换账户（DPAPI 绑用户、AES 缺 secrets.key）是「文件还在、
// 钥匙不对」，修法是把原机的钥匙带回来或重填；其余读失败（权限、磁盘）另说。
func checkSecrets(ctx context.Context, w *wiring) app.HealthCheck {
	c := app.HealthCheck{ID: "secrets"}
	if w.secretsBin == nil {
		c.State = app.HealthWarn
		c.Detail = i18n.T("cmd.doctor.secrets_unwired")
		return c
	}
	entries, err := w.secretsBin.Entries(ctx)
	if err != nil {
		c.State = app.HealthFail
		c.Detail = i18n.Tf("cmd.doctor.secrets_bad", "err", err)
		if errors.Is(err, secretbin.ErrUnreadable) {
			c.Fix = i18n.T("cmd.doctor.secrets_unreadable_fix")
		} else {
			c.Fix = i18n.Tf("cmd.doctor.secrets_fix", "path", w.secretsBin.Path())
		}
		return c
	}
	c.Detail = i18n.Tf("cmd.doctor.secrets_ok", "n", len(entries), "path", w.secretsBin.Path())
	return c
}

// checkSessions 逐个解码会话文件。
//
// 不走 ConversationStore.List：它把解不开的文件静默跳过（对 /list 是对的，坏一个不该
// 拖垮整张列表），于是那些会话在 /list 里就是「不存在」。体检要报的正是这一批。
func checkSessions(ctx context.Context, w *wiring) app.HealthCheck {
	c := app.HealthCheck{ID: "sessions"}
	if w.storeJSON == nil {
		c.State = app.HealthWarn
		c.Detail = i18n.T("cmd.doctor.sessions_unwired")
		return c
	}
	total, broken, err := w.storeJSON.Audit(ctx)
	if err != nil {
		c.State = app.HealthFail
		c.Detail = i18n.Tf("cmd.doctor.sessions_list_bad", "err", err)
		c.Fix = i18n.T("cmd.doctor.sessions_list_fix")
		return c
	}
	if len(broken) > 0 {
		c.State = app.HealthFail
		c.Detail = i18n.Tf("cmd.doctor.sessions_broken",
			"total", total, "n", len(broken), "name", broken[0].Name, "err", broken[0].Reason)
		if rest := len(broken) - 1; rest > 0 {
			c.Detail += i18n.Tf("cmd.doctor.sessions_more", "n", rest)
		}
		c.Fix = i18n.T("cmd.doctor.sessions_fix")
		return c
	}
	c.Detail = i18n.Tf("cmd.doctor.sessions_ok", "n", total)
	return c
}

// checkMCP 读插件宿主已有的状态快照（与 `/plugin list` 同一份），**不发起连接**。
//
// 声明解析失败、首连失败、授权被拒、崩溃退避都在快照里；「现在就再连一次」是
// `/plugin enable` 的动作，不属于体检。
func checkMCP(w *wiring) app.HealthCheck {
	c := app.HealthCheck{ID: "mcp"}
	if w.host == nil {
		c.State = app.HealthWarn
		c.Detail = i18n.T("cmd.doctor.mcp_unwired")
		return c
	}
	list := w.host.List()
	if len(list) == 0 {
		c.Detail = i18n.T("cmd.doctor.mcp_none")
		return c
	}
	var ready, disabled int
	var failed, pending []plugin.Info
	for _, info := range list {
		switch info.Status {
		case plugin.StatusReady:
			ready++
		case plugin.StatusDisabled:
			disabled++
		case plugin.StatusConnecting, plugin.StatusRestarting:
			pending = append(pending, info)
		default: // failed / revoked / crashed
			failed = append(failed, info)
		}
	}
	switch {
	case len(failed) > 0:
		c.State = app.HealthFail
		c.Detail = i18n.Tf("cmd.doctor.mcp_failed",
			"name", failed[0].Name, "err", failed[0].LastErr, "ready", ready, "disabled", disabled)
		if rest := len(failed) - 1; rest > 0 {
			c.Detail += i18n.Tf("cmd.doctor.mcp_more", "n", rest)
		}
		c.Fix = i18n.T("cmd.doctor.mcp_fix")
	case len(pending) > 0:
		c.State = app.HealthWarn
		c.Detail = i18n.Tf("cmd.doctor.mcp_pending",
			"name", pending[0].Name, "ready", ready, "disabled", disabled)
		c.Fix = i18n.T("cmd.doctor.mcp_pending_fix")
	default:
		c.Detail = i18n.Tf("cmd.doctor.mcp_ok", "ready", ready, "disabled", disabled)
	}
	return c
}

// checkDir 一个目录的占用（只读遍历：不清理、不轮转）。
func checkDir(id, dir string, warn int64) app.HealthCheck {
	c := app.HealthCheck{ID: id}
	size, files, capped, err := dirUsage(dir)
	if err != nil {
		c.State = app.HealthFail
		c.Detail = i18n.Tf("cmd.doctor.dir_bad", "dir", dir, "err", err)
		c.Fix = i18n.Tf("cmd.doctor.dir_fix", "dir", dir)
		return c
	}
	if size > warn {
		c.State = app.HealthWarn
		c.Detail = i18n.Tf("cmd.doctor.dir_big",
			"files", files, "size", humanSize(size), "limit", humanSize(warn))
		c.Fix = i18n.Tf("cmd.doctor.dir_fix", "dir", dir)
	} else {
		c.Detail = i18n.Tf("cmd.doctor.dir_ok", "files", files, "size", humanSize(size))
	}
	if capped {
		c.Detail += i18n.Tf("cmd.doctor.dir_capped", "n", dirUsageMaxEntries)
	}
	return c
}

// checkDisk 卷余量。取不到不算异常（有些文件系统不报），记注意并说明——比拿不到数字
// 却报「正常」诚实。
func checkDisk(dir string) app.HealthCheck {
	c := app.HealthCheck{ID: "disk"}
	free, total, err := diskSpace(dir)
	if err != nil {
		c.State = app.HealthWarn
		c.Detail = i18n.Tf("cmd.doctor.disk_unknown", "err", err)
		return c
	}
	c.Detail = i18n.Tf("cmd.doctor.disk_ok", "free", humanSize(int64(free)), "total", humanSize(int64(total)))
	switch {
	case free < diskFailBytes:
		c.State = app.HealthFail
		c.Fix = i18n.Tf("cmd.doctor.disk_fix", "dir", dir)
	case free < diskWarnBytes:
		c.State = app.HealthWarn
		c.Fix = i18n.Tf("cmd.doctor.disk_fix", "dir", dir)
	}
	return c
}

// dirUsage 递归统计一个目录的字节数与文件数；目录不存在 = 0（还没写过不是错误）。
// 条目超过上限即停，返回 capped。
func dirUsage(dir string) (size int64, files int, capped bool, err error) {
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if p == dir && errors.Is(werr, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return werr
		}
		if d.IsDir() {
			return nil
		}
		if files >= dirUsageMaxEntries {
			capped = true
			return fs.SkipAll
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil // 单个文件读不到不影响总量：体检不是审计
		}
		size += info.Size()
		files++
		return nil
	})
	if walkErr != nil {
		return 0, 0, false, walkErr
	}
	return size, files, capped, nil
}

// humanSize 字节数的可读写法（回执里给人看）。与 app / toolbuiltin 的两份同一套规则：
// 1024 进制、一位小数、小于 1 KiB 写整数。三处各一份是有意的——它们分属三层，抽公共包
// 只为了省十行，反而让依赖方向多一条。
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	for _, suffix := range []string{"KB", "MB", "GB"} {
		v /= unit
		if v < unit {
			return fmt.Sprintf("%.1f %s", v, suffix)
		}
	}
	return fmt.Sprintf("%.1f TB", v/unit)
}
