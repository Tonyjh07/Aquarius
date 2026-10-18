package main

// 人格模块树装配根侧（S7/D144）：设置窗「对话」页数据面——内置模块展示清单、组合预览
// （未保存的表单现状 → 最终 system 文本，纯计算）；config `prompts` 段的读写见
// settings.go 的 readTextSettings / persistSettingsTextKeys。

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/app"
)

// builtinModuleLabels 内置模块中文说明（展示用；名集以 app.BuiltinModuleNames 为准——契约，
// 缺 label 回退引用名）。
var builtinModuleLabels = map[string]string{
	"@default":      "默认人格（system_prompt 或内置默认）",
	"@env":          "运行环境信息（组）",
	"@env.platform": "└ 操作系统 / 架构",
	"@env.terminal": "└ 界面形态与 TERM",
	"@env.sandbox":  "└ 特权沙盒目录（绝对路径时）",
	"@env.timeout":  "└ 工具缺省调用超时",
	"@env.date":     "└ 会话创建当天日期",
}

// builtinModuleViews 内置只读模块展示清单（设置窗展示 + 一键引用）。
func builtinModuleViews() []uigui.BuiltinModuleView {
	names := app.BuiltinModuleNames()
	out := make([]uigui.BuiltinModuleView, 0, len(names))
	for _, n := range names {
		label := builtinModuleLabels[n]
		if label == "" {
			label = "内置模块"
		}
		out = append(out, uigui.BuiltinModuleView{Name: n, Label: label})
	}
	return out
}

// promptViewsOf config `prompts.modules` → 设置窗模块视图（按名排序：JSON map 无序，展示
// 须确定；模块间无顺序语义——顺序只在 persona 引用与 children 列表里）。
func promptViewsOf(p promptsConfig) []uigui.PromptModuleView {
	names := make([]string, 0, len(p.Modules))
	for name := range p.Modules {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]uigui.PromptModuleView, 0, len(names))
	for _, name := range names {
		m := p.Modules[name]
		out = append(out, uigui.PromptModuleView{Name: name, Text: m.Text, Children: append([]string(nil), m.Children...)})
	}
	return out
}

// cleanRefs 引用名清洗（去空白、去空、去重保序）。
func cleanRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, r := range refs {
		name := strings.TrimSpace(r)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// validatePromptModules 用户模块名校验（S7/D144）：空名行跳过（未填完的新增行）；非空名
// 不得以 `@` 开头（保留给内置）、不得重复。
func validatePromptModules(mods []uigui.PromptModuleView) error {
	seen := map[string]bool{}
	for _, m := range mods {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, app.ReservedModulePrefix) {
			return fmt.Errorf("模块名 %q 不能以 %q 开头（该前缀保留给内置模块）", name, app.ReservedModulePrefix)
		}
		if seen[name] {
			return fmt.Errorf("模块名 %q 重复", name)
		}
		seen[name] = true
	}
	return nil
}

// previewPrompts 组合预览（S7/D144）：设置窗当前（未保存）模块树 + 引用 → 最终 system 文本。
// 与新建会话同口径（环境事实取本次装配、日期取当天），仅纯计算、不落盘。
func (w *wiring) previewPrompts(mods []uigui.PromptModuleView, persona []string, legacy string) string {
	spec := app.PromptSpec{Persona: cleanRefs(persona), Legacy: legacy}
	if len(mods) > 0 {
		spec.Modules = make(map[string]app.PromptModule, len(mods))
		for _, m := range mods {
			name := strings.TrimSpace(m.Name)
			if name == "" {
				continue
			}
			spec.Modules[name] = app.PromptModule{Text: m.Text, Children: cleanRefs(m.Children)}
		}
	}
	text, _ := app.NewPersonaComposer(spec, w.runtimeEnv(), time.Now).Compose()
	return text
}

// runtimeEnv 运行环境事实（D37 口径）：平台、终端形态与 TERM、特权沙盒目录，以及缺省调用
// 超时（D38 发现性，保留参数口径）。装配与设置窗预览共用，避免两处漂移。
func (w *wiring) runtimeEnv() app.RuntimeEnv {
	return app.RuntimeEnv{
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
		Terminal:       w.cfg.UI.Kind,
		TERM:           os.Getenv("TERM"),
		SandboxDir:     w.sandboxDir,
		ToolTimeoutSec: int(w.toolTimeout.Seconds()),
	}
}
