package main

// 设置窗装配根侧（§15.7 读写契约，D60/D61）：读 = config 文本键 + 运行态快照；
// 写 = 单一 patch 回调——文本键泛键改写（persistConfig 复用），权限/模型/思考/档位
// 给出内核命令计划交设置窗排队执行（与键入命令同路径：串行、回执进转写区——
// 壳内不旁路，热生效与 /permission /model /think /effort 同口径）。

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// textSettings 设置窗文本键与全量分组页数据（config 文件即事实源）。D110②：
// provider/base_url/api_key 编辑走 ProviderManager（providers 列表结构），不在
// 此处；MCPServers/Plugins 只读取名（结构化编辑走高级页 raw JSON，Q10）。
type textSettings struct {
	SystemPrompt string       `json:"system_prompt"`
	Output       outputConfig `json:"output"`
	Limits       limitsConfig `json:"limits"`
	MCPServers   map[string]struct {
		Transport string `json:"transport"`
	} `json:"mcpServers"`
	Plugins map[string]struct {
		Enabled *bool `json:"enabled"`
	} `json:"plugins"`
	UI struct {
		Hotkey       string  `json:"hotkey"`
		Theme        string  `json:"theme"`
		Scale        float64 `json:"scale"`         // D90：元素缩放倍率（0 = 缺省 1.0）
		FontSize     float64 `json:"font_size"`     // D90：正文字号 sp（0 = 缺省 15）
		WindowWidth  int     `json:"window_width"`  // D90：主窗像素宽（0 = 缺省）
		WindowHeight int     `json:"window_height"` // D90：主窗像素高
	} `json:"ui"`
}

// readTextSettings 读 config 的设置窗文本键与全量分组页数据（运行态四键由调用方
// 补；读失败/解析失败返回零值——快照容错不阻断开窗，Q10；RawJSON 原文照带）。
func readTextSettings(cfgPath string) uigui.SettingsSnapshot {
	var raw textSettings
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return uigui.SettingsSnapshot{}
	}
	_ = json.Unmarshal(data, &raw) // 解析失败 = 快照零值 + 原文照呈（高级页手工修）
	snap := uigui.SettingsSnapshot{
		SystemPrompt:     raw.SystemPrompt,
		Notify:           raw.Output.Notify,
		TTS:              raw.Output.TTS,
		MaxTurns:         raw.Limits.MaxTurns,
		MaxContextTokens: raw.Limits.MaxContextTokens,
		CompactThreshold: raw.Limits.CompactThreshold,
		ToolOutputChars:  raw.Limits.ToolOutputChars,
		ToolTimeoutSec:   raw.Limits.ToolTimeoutSec,
		Hotkey:           raw.UI.Hotkey,
		Theme:            raw.UI.Theme,
		Scale:            raw.UI.Scale,
		FontSize:         raw.UI.FontSize,
		WindowWidth:      raw.UI.WindowWidth,
		WindowHeight:     raw.UI.WindowHeight,
		RawJSON:          string(data),
	}
	for name := range raw.MCPServers {
		snap.MCPServers = append(snap.MCPServers, name)
	}
	for name := range raw.Plugins {
		snap.Plugins = append(snap.Plugins, name)
	}
	sort.Strings(snap.MCPServers)
	sort.Strings(snap.Plugins)
	return snap
}

// persistSettingsTextKeys 无内核命令覆盖的键一次泛键改写（ui.theme/hotkey + D90 三
// 旋钮 scale/font_size/window_width/window_height。D110②：provider/base_url/api_key
// 由 ProviderManager.Apply 全量写回，不在此列。有覆盖的键由命令各自写回，cfgWriteMu
// 串行防丢更新）。
// theme 先归一校验（"" → system），非法值拒写防落盘；旋钮数值原样落盘（UI 侧
// clampWindowPx/clampKnobs 统一夹取，config 侧不重复校验）。
func persistSettingsTextKeys(cfgPath string, p uigui.SettingsPatch) error {
	switch p.Theme {
	case "":
		p.Theme = "system"
	case "system", "light", "dark":
	default:
		return fmt.Errorf("非法主题档 %q（须为 system|light|dark）", p.Theme)
	}
	if p.RawJSON != "" {
		// 高级页（Q10）：整文件替换——JSON 对象校验后原子写（cfgWriteMu 串行与其他
		// 写回互斥）；原文即最终态，未知键天然保留。后续 mutate 在新文件上照常叠加
		// 本 patch 的其余键（读-改-写重新读盘，口径一致）。
		if err := writeRawConfig(cfgPath, p.RawJSON); err != nil {
			return err
		}
	}
	return persistConfig(cfgPath, func(generic map[string]any) {
		uiSec, _ := generic["ui"].(map[string]any)
		if uiSec == nil {
			uiSec = map[string]any{}
			generic["ui"] = uiSec
		}
		uiSec["theme"] = p.Theme
		uiSec["hotkey"] = p.Hotkey
		uiSec["scale"] = p.Scale
		uiSec["font_size"] = p.FontSize
		uiSec["window_width"] = p.WindowWidth
		uiSec["window_height"] = p.WindowHeight
		// 输出（布尔无留空语义，恒写）。
		outSec, _ := generic["output"].(map[string]any)
		if outSec == nil {
			outSec = map[string]any{}
			generic["output"] = outSec
		}
		outSec["notify"] = p.Notify
		outSec["tts"] = p.TTS
		// 限额（数值 0 = 不改，表单留空口径）。
		limSec, _ := generic["limits"].(map[string]any)
		if limSec == nil {
			limSec = map[string]any{}
			generic["limits"] = limSec
		}
		if p.MaxTurns > 0 {
			limSec["max_turns"] = p.MaxTurns
		}
		if p.MaxContextTokens > 0 {
			limSec["max_context_tokens"] = p.MaxContextTokens
		}
		if p.CompactThreshold > 0 {
			limSec["compact_threshold"] = p.CompactThreshold
		}
		if p.ToolOutputChars > 0 {
			limSec["tool_output_chars"] = p.ToolOutputChars
		}
		if p.ToolTimeoutSec > 0 {
			limSec["tool_timeout_sec"] = p.ToolTimeoutSec
		}
		// 人格（恒写：清空 = 合法语义「内置默认」，D20）。
		generic["system_prompt"] = p.SystemPrompt
	})
}

// writeRawConfig 高级页整文件替换（S4/D110③/Q10）：JSON 对象校验通过才落盘，
// 解析失败报因且原文件不动。
func writeRawConfig(cfgPath, raw string) error {
	var probe map[string]any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return fmt.Errorf("JSON 解析失败（原文件未动）: %w", err)
	}
	cfgWriteMu.Lock()
	defer cfgWriteMu.Unlock()
	if err := atomicfile.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", cfgPath, err)
	}
	return nil
}

// settingsCommands 内核命令计划：patch 与运行态快照 diff 才发命令（无变更不进
// 转写区）。空值语义：空模型名 = 保持当前不发；空档位 = 清除（/effort off，D34）。
func settingsCommands(cur uigui.SettingsSnapshot, p uigui.SettingsPatch) []port.Command {
	var cmds []port.Command
	if p.Permission != "" && p.Permission != cur.Permission {
		cmds = append(cmds, port.Command{Name: "permission", Args: []string{p.Permission}})
	}
	if p.Model != "" && p.Model != cur.Model {
		cmds = append(cmds, port.Command{Name: "model", Args: []string{p.Model}})
	}
	if p.Think != cur.Think {
		on := "off"
		if p.Think {
			on = "on"
		}
		cmds = append(cmds, port.Command{Name: "think", Args: []string{on}})
	}
	if p.Effort != cur.Effort {
		e := p.Effort
		if e == "" {
			e = "off"
		}
		cmds = append(cmds, port.Command{Name: "effort", Args: []string{e}})
	}
	return cmds
}
