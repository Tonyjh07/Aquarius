package main

// 放行策略例外表的装配侧（D280）：配置 → perm.Overrides，以及运行期活状态。
//
// 表本身与判定在 domain/perm（纯规则）；这里只管两件与启动、写回有关的事：
// 拼错就报错退出，以及让设置窗改完立即生效。

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// parsePolicyOverrides 校验并转换配置里的例外表。
//
// 启动期 fail-fast：来源与档位拼错就报错退出，不静默跳过——一张悄悄失效的策略表
// 比没有更危险（用户以为已经设过）。条目顺序保留，判定时按具体程度取。
func parsePolicyOverrides(raw []policyOverride) (perm.Overrides, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(perm.Overrides, 0, len(raw))
	for i, ov := range raw {
		origin, err := perm.ParseOrigin(ov.Origin)
		if err != nil {
			return nil, errors.New(i18n.Tf("perm.err.override_at", "index", i, "err", err))
		}
		mode, err := perm.ParseMode(ov.Mode)
		if err != nil {
			return nil, errors.New(i18n.Tf("perm.err.override_at", "index", i, "err", err))
		}
		tool := strings.TrimSpace(ov.Tool)
		if tool == "" && origin == perm.OriginTool {
			return nil, errors.New(i18n.Tf("perm.err.override_tool", "index", i))
		}
		out = append(out, perm.Override{Origin: origin, Tool: tool, Mode: mode})
	}
	return out, nil
}

// policyHolder 例外表活状态：设置窗写回成功后 Set，ToolRunner 与转交管理器经 Get
// 现取。用 atomic.Value 的整体替换而不是原地改切片——并发读者可能正持有旧表
// （与 levelHolder 同口径）。
type policyHolder struct{ v atomic.Value } // perm.Overrides

// newPolicyHolder 构造并写入初值；nil 表存成 nil，读取侧据此跳过查表。
func newPolicyHolder(o perm.Overrides) *policyHolder {
	h := &policyHolder{}
	h.v.Store(o)
	return h
}

// Get 当前例外表（可能为 nil）。
func (h *policyHolder) Get() perm.Overrides {
	v, _ := h.v.Load().(perm.Overrides)
	return v
}

// Set 整体替换。
func (h *policyHolder) Set(o perm.Overrides) { h.v.Store(o) }

// sensitiveRoots 硬名单的敏感根（D280 第 6 条）：配置文件、密钥库的两个文件，
// 以及系统目录。命中这些位置的自动放行一律降回询问。
//
// 不列整个 profile 目录：记忆、会话树、附件都在里面，memory_write 之类的工具
// 本来就是往里写；把整目录当硬名单会让 permissive 的记忆写入每次都被拦。
func (w *wiring) sensitiveRoots() []string {
	roots := []string{
		w.cfgPath,
		filepath.Join(w.profileDir, "secrets.bin"),
		filepath.Join(w.profileDir, "secrets.key"),
	}
	for _, r := range systemRoots() {
		roots = append(roots, r)
	}
	return roots
}

// systemRoots 系统目录：按平台取常见根。不做存在性检查——多一个不存在的根
// 不影响判定（withinRoot 只是字符串前缀比较）。
func systemRoots() []string {
	if runtime.GOOS == "windows" {
		var out []string
		for _, k := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return []string{"/etc", "/usr", "/bin", "/sbin", "/boot"}
}
