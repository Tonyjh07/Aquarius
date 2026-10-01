package main

// 设置窗装配根侧（§15.7 读写契约，D60/D61）：读 = config 文本键 + 运行态快照；
// 写 = 单一 patch 回调——文本键泛键改写（persistConfig 复用），权限/模型/思考/档位
// 给出内核命令计划交设置窗排队执行（与键入命令同路径：串行、回执进转写区——
// 壳内不旁路，热生效与 /permission /model /think /effort 同口径）。

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// textSettings 设置窗文本键（config 文件即事实源；密钥永不读回——D35 只写不回显）。
type textSettings struct {
	Model struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
	} `json:"model"`
	UI struct {
		Hotkey       string  `json:"hotkey"`
		Theme        string  `json:"theme"`
		Scale        float64 `json:"scale"`         // D90：元素缩放倍率（0 = 缺省 1.0）
		FontSize     float64 `json:"font_size"`     // D90：正文字号 sp（0 = 缺省 15）
		WindowWidth  int     `json:"window_width"`  // D90：主窗像素宽（0 = 缺省）
		WindowHeight int     `json:"window_height"` // D90：主窗像素高
	} `json:"ui"`
}

// readTextSettings 读 config 的设置窗文本键（运行态四键由调用方补；读失败返回
// 零值——快照容错，字段留空、不阻断开窗）。
func readTextSettings(cfgPath string) uigui.SettingsSnapshot {
	var raw textSettings
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return uigui.SettingsSnapshot{}
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return uigui.SettingsSnapshot{}
	}
	return uigui.SettingsSnapshot{
		Provider:     raw.Model.Provider,
		BaseURL:      raw.Model.BaseURL,
		Hotkey:       raw.UI.Hotkey,
		Theme:        raw.UI.Theme,
		Scale:        raw.UI.Scale,
		FontSize:     raw.UI.FontSize,
		WindowWidth:  raw.UI.WindowWidth,
		WindowHeight: raw.UI.WindowHeight,
	}
}

// persistSettingsTextKeys 无内核命令覆盖的键一次泛键改写（ui.theme/hotkey + D90 三
// 旋钮 scale/font_size/window_width/window_height + model.provider/base_url/api_key；
// 空 provider/base_url/api_key = 保持不变——api_key 仅用户填写时下发。有覆盖的键由
// 命令各自写回，cfgWriteMu 串行防丢更新）。
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
		model := modelSection(generic)
		if p.Provider != "" {
			model["provider"] = p.Provider
		}
		if p.BaseURL != "" {
			model["base_url"] = p.BaseURL
		}
		if p.APIKey != "" {
			model["api_key"] = p.APIKey
		}
	})
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
