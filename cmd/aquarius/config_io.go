package main

// config 文件的读写与档案：首次运行模板、解析与旧格式判别、profile 指针，以及写回
// （persistConfig 与 cfgWriter）。结构与各节的转换在 config.go。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
)

// ErrLegacyConfig D110 前的旧单文件 config（沿 storejson ErrLegacyFormat 口径：
// 显式识别报因、绝不静默当零值解析、不做自动迁移——旧文件留存原地由用户手工处置）。
var ErrLegacyConfig = errors.New("检测到旧格式 config")

// defaultProfileName 首次运行创建的 profile 名（指针缺省指向它）。
const defaultProfileName = "default"

// legacyConfigKeys D110 前单文件 config 的顶层键：根 config.json 任一出现即判旧格式
// （encoding/json 对未知键静默零值——旧文件「看起来正常」地丢光配置，必须显式识别，D95 同口径）。
var legacyConfigKeys = []string{
	"model", "ui", "mcpServers", "plugins", "permissions",
	"limits", "system_prompt", "output", "input",
}

// persistSkillDirs 把额外技能根写回 config 的 `skills.dirs`（设置窗「技能」页的目录增删）。
//
// 只写「额外目录」：兜底根 `<profile>/skills` 恒在最后，不进配置（D201 口径二）。
// 空表 = 删除该键（老配置里只有空数组时不留下 `"dirs": []` 噪音）。
func (w *wiring) persistSkillDirs(dirs []string) error {
	return persistConfig(w.cfgPath, func(generic map[string]any) {
		if len(dirs) == 0 {
			delete(generic, "skills")
			return
		}
		sec, _ := generic["skills"].(map[string]any)
		if sec == nil {
			sec = map[string]any{}
			generic["skills"] = sec
		}
		list := make([]any, 0, len(dirs))
		for _, d := range dirs {
			list = append(list, d)
		}
		sec["dirs"] = list
	})
}

// defaultConfig 首次运行写入的模板：§8 示例的可运行子集
// （ui.kind 取 gui——D51 模板默认 = 悬浮球前端，tui 显式指定、repl 为测试/e2e
// 后端；mcpServers/plugins 为 M4 MCP 接入的声明与状态，缺省皆空）。
const defaultConfig = `{
  "model": {
    "providers": [
      { "name": "openai",
        "kind": "openai-chat",
        "base_url": "https://api.openai.com/v1",
        "keys": [ { "label": "主账号", "api_key": "secret:AQUARIUS_OPENAI_KEY" } ],
        "models": [
          { "name": "gpt-4o-mini", "context_window": 128000 },
          { "name": "gpt-4o", "caps": ["tools", "reasoning", "vision"], "context_window": 128000 }
        ],
        "tokenizer": "",
        "unsupported_params": [] }
    ],
    "primary": "openai",
    "fallback": [],
    "name": "gpt-4o-mini",
    "think": true,
    "reasoning_effort": "",
    "think_tool": false,
    "echo_thinking": true
  },
  "tools": { "disabled": ["think"] },
  "skills": { "dirs": [] },
  "ui": { "kind": "gui", "theme": "system", "scale": 1.0, "font_size": 15, "window_width": 0, "window_height": 0, "welcomed": false },
  "system_prompt": "",
  "prompts": { "modules": {}, "persona": [], "user_name": "User" },
  "characters": { "prefer_system_prompt": false, "apply_post_history": true,
                  "inject_constant_book": true, "inject_examples": true },
  "backup": { "keep": 3 },
  "input": { "language": "auto" },
  "audio": { "asr": {}, "tts": {} },
  "output": { "tts": false, "notify": true },
  "web": {
    "search": { "providers": [], "count": 8 },
    "fetch": { "providers": [ { "kind": "local" } ], "max_chars": 16000 }
  },
  "delegate": { "agents": [], "timeout_sec": 1800, "max_concurrent": 2, "max_total": 4 },
  "mcpServers": {},
  "plugins": {},
  "permissions": { "level": "strict", "unattended": false },
  "limits": {
    "max_turns": 32,
    "max_context_tokens": 64000,
    "compact_threshold": 0.7,
    "tool_output_chars": 20000,
    "tool_timeout_sec": 60
  }
}
`

// loadConfig 读取并解析配置；model 段为 D110 前单 provider 平铺字段时显式报
// ErrLegacyConfig（detectLegacyModelConfig：Unmarshal 对旧键静默零值，必须先判别）。
func loadConfig(path string) (*fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	if err := detectLegacyModelConfig(path, data); err != nil {
		return nil, err
	}
	var cfg fileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	return &cfg, nil
}

// detectLegacyModelConfig 旧形状判别（D110② 单 provider 平铺 / D173 之前 provider 列表
// 单值密钥）：两种都报 ErrLegacyConfig 与手工改写指引。Unmarshal 对旧键静默零值——
// 不先判别的话，旧配置会「看起来正常」地丢光模型与密钥（D95 影子结构同口径）。
func detectLegacyModelConfig(path string, data []byte) error {
	var probe struct {
		Model struct {
			Providers []map[string]json.RawMessage `json:"providers"`
			Fallback  []json.RawMessage            `json:"fallback"`
			BaseURL   string                       `json:"base_url"`
			APIKey    string                       `json:"api_key"`
			Tokenizer string                       `json:"tokenizer"`
			Provider  string                       `json:"provider"`
		} `json:"model"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil // 非 JSON：交由后续解析报因
	}
	m := probe.Model
	if len(m.Providers) == 0 {
		if m.BaseURL == "" && m.APIKey == "" && m.Tokenizer == "" && m.Provider == "" {
			return nil // 无 model 段：交由后续校验（providers 不可为空）
		}
		return fmt.Errorf("%w：%s 的 model 段为单 provider 平铺字段（检测到 base_url/api_key/tokenizer/provider）。\n"+
			"provider 须写成列表（model.providers）：\n"+
			"  \"model\": { \"providers\": [ { \"name\": \"openai\", \"kind\": \"openai-chat\",\n"+
			"    \"base_url\": \"…\", \"keys\": [ { \"api_key\": \"secret:…\" } ],\n"+
			"    \"models\": [ { \"name\": \"…\" } ], \"tokenizer\": \"\", \"unsupported_params\": [] } ],\n"+
			"    \"primary\": \"openai\", \"fallback\": [], \"name\": \"…\", … }\n"+
			"请手工改写（不做自动迁移），原平铺字段退役；字段说明见 docs/configuration.md。",
			ErrLegacyConfig, path)
	}
	// D173 前的 provider 列表：api_key 是单值字符串、models 是字符串数组、vision 挂在
	// provider 上。三者任一出现即判旧形状。
	for i, p := range m.Providers {
		_, hasKey := p["api_key"]
		_, hasVision := p["vision"]
		var models []json.RawMessage
		if raw, ok := p["models"]; ok {
			_ = json.Unmarshal(raw, &models)
		}
		oldModels := len(models) > 0 && len(models[0]) > 0 && models[0][0] == '"'
		if hasKey || hasVision || oldModels {
			return fmt.Errorf("%w：%s 的 model.providers[%d] 是旧形状（api_key 单值 / models 字符串数组 / provider 上的 vision）。\n"+
				"模型与提供商已改版：provider 承载「怎么连」，模型承载「能做什么」。\n"+
				"  \"model\": { \"providers\": [ { \"name\": \"openai\", \"kind\": \"openai-chat\",\n"+
				"    \"base_url\": \"…\",\n"+
				"    \"keys\": [ { \"label\": \"主账号\", \"api_key\": \"secret:…\" } ],\n"+
				"    \"models\": [ { \"name\": \"gpt-4o-mini\" },\n"+
				"                 { \"name\": \"gpt-4o\", \"caps\": [\"tools\",\"reasoning\",\"vision\"] } ],\n"+
				"    \"tokenizer\": \"\", \"unsupported_params\": [] } ],\n"+
				"    \"primary\": \"openai\",\n"+
				"    \"fallback\": [ { \"provider\": \"mimo\", \"model\": \"mimo-v2.6-flash\" } ],\n"+
				"    \"name\": \"…\", … }\n"+
				"迁移要点：api_key → keys[]（多把即密钥池）；models 字符串 → 对象（能力位写进 caps，\n"+
				"省略 caps 按协议缺省）；provider 上的 vision 移到对应模型的 caps；fallback 字符串 →\n"+
				"provider/model 二元组（省略 model = 该 provider 的 models[0]）。\n"+
				"请手工改写（不做自动迁移）；字段说明见 docs/configuration.md。",
				ErrLegacyConfig, path, i)
		}
	}
	for _, fb := range m.Fallback {
		if len(fb) > 0 && fb[0] == '"' {
			return fmt.Errorf("%w：%s 的 model.fallback 仍是 provider 名字数组。\n"+
				"降级链已改为逐项指定模型：\"fallback\": [ { \"provider\": \"mimo\", \"model\": \"mimo-v2.6-flash\" } ]\n"+
				"（省略 model = 该 provider 的 models[0]）。请手工改写（不做自动迁移），见 docs/configuration.md。",
				ErrLegacyConfig, path)
		}
	}
	return nil
}

// writeDefaultConfig 落首次运行模板。
func writeDefaultConfig(path string) error {
	return os.WriteFile(path, []byte(defaultConfig), 0o644)
}

// pointerTemplate 根 config.json 的指针模板（D110①：仅存当前 profile 名）。
const pointerTemplate = `{
  "profile": "default"
}
`

// pointerContent 指定 profile 名的指针文件内容（切换面写回用，D110③）。
func pointerContent(name string) string {
	return fmt.Sprintf("{\n  \"profile\": %q\n}\n", name)
}

// validProfileName profile 名合法性（目录名安全子集）：非空、无路径分隔/盘符/控制
// 字符、非 . ..、长度 ≤64。指针解析与设置窗新建/复制共用，防手改与注入。
func validProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("profile 名不可为空")
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("profile 名首尾不可含空白: %q", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("profile 名非法: %q", name)
	}
	if strings.ContainsAny(name, `/\:`) || len(name) > 64 {
		return fmt.Errorf("profile 名不可含 / \\ : 且长度 ≤64: %q", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("profile 名不可含控制字符: %q", name)
		}
	}
	return nil
}

// parsePointerFile 解析根 config.json 指针（D110①），返回当前 profile 名。
// 旧单文件 config 显式判别报 ErrLegacyConfig 并给出手工迁移步骤——普通 Unmarshal
// 会把旧键静默丢成零值指针，「看起来正常」地换到空 profile，绝不允许（D95 同口径）。
func parsePointerFile(path string, data []byte) (string, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("解析 %s: %w", path, err)
	}
	for _, k := range legacyConfigKeys {
		if _, ok := raw[k]; ok {
			return "", fmt.Errorf("%w：%s 含旧格式键 %q。\n"+
				"profile 已改为目录化：根 config.json 仅存当前 profile 指针（{\"profile\":\"<name>\"}），\n"+
				"主配置落 profiles/<name>/config.json。请手工处置（不做自动迁移），例如迁移到 %s profile：\n"+
				"  1. 把 %s 移动为 %s\n"+
				"  2. 新建根 %s，内容：{\"profile\": \"%s\"}\n"+
				"若该 profile 已存在请改用其他名字或手工合并；字段说明见 docs/configuration.md。",
				ErrLegacyConfig, path, k, defaultProfileName, path,
				filepath.Join(filepath.Dir(path), "profiles", defaultProfileName, "config.json"),
				path, defaultProfileName)
		}
	}
	v, ok := raw["profile"]
	if !ok {
		return "", fmt.Errorf("%s 缺 profile 键（指针格式：{\"profile\":\"<name>\"}）", path)
	}
	name, ok := v.(string)
	if !ok || name == "" {
		return "", fmt.Errorf("%s 的 profile 键须为非空字符串", path)
	}
	if err := validProfileName(name); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return name, nil
}

// listProfiles 枚举 profiles/ 下可用的 profile 名（含 config.json 的目录）。
// 目录不存在 = 无可用 profile（返回空）。
func listProfiles(dir string) []string {
	entries, err := os.ReadDir(filepath.Join(dir, "profiles"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "profiles", e.Name(), "config.json")); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// cfgWriteMu 串行化全部 config 写回（/permission、/model、/think、/effort、
// /plugin 与 llm 的 unsupported_params 记录可能并发——读-改-写须互斥防丢更新）。
var cfgWriteMu sync.Mutex

// persistConfig 通用 config 键改写：读入 → mutate 只动目标键（map 级重排保留
// 其余键与注释性空白）→ 唯一临时文件 + 原子换入（覆盖沿用既有权限位）。
// /permission、/model、/think、/effort、/plugin 与 unsupported_params 共用。
func persistConfig(cfgPath string, mutate func(generic map[string]any)) error {
	cfgWriteMu.Lock()
	defer cfgWriteMu.Unlock()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		return fmt.Errorf("解析 %s: %w", cfgPath, err)
	}
	mutate(generic)
	out, err := json.MarshalIndent(generic, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 config: %w", err)
	}
	if err := atomicfile.WriteFile(cfgPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", cfgPath, err)
	}
	return nil
}

// modelSection 取/建 config 的 model 段（各写回闭包共用）。
func modelSection(generic map[string]any) map[string]any {
	model, _ := generic["model"].(map[string]any)
	if model == nil {
		model = map[string]any{}
		generic["model"] = model
	}
	return model
}

// providerSection 定位 model.providers 中指定名字的条目（D110②）：
// primary 缺省 = model.primary 键，再缺省 = 首个条目。未命中返回 nil。
func providerSection(generic map[string]any, name string) map[string]any {
	model := modelSection(generic)
	providers, _ := model["providers"].([]any)
	if len(providers) == 0 {
		return nil
	}
	target := name
	if target == "" {
		target, _ = model["primary"].(string)
	}
	for _, pv := range providers {
		p, ok := pv.(map[string]any)
		if !ok {
			continue
		}
		if n, _ := p["name"].(string); target == "" || n == target {
			return p
		}
	}
	return nil
}

// cfgWriter 把 UI/命令面触发的 config 写回收拢为一处（S4a）：path 即当前 profile
// 的 config.json，lvl 供 persistLevel 写回成功后同步活等级。
type cfgWriter struct {
	path       string
	lvl        *levelHolder
	unattended *unattendedHolder
}

// persistLevel 实现 /permission 写回 config（D22）：成功后同步活等级。
func (c *cfgWriter) persistLevel(l perm.Level) error {
	if err := persistConfig(c.path, func(generic map[string]any) {
		perms, _ := generic["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
			generic["permissions"] = perms
		}
		perms["level"] = string(l)
	}); err != nil {
		return err
	}
	c.lvl.Set(l) // 工具链路活等级（D22 执行接入）
	return nil
}

// persistUnattended 实现 /unattended 与菜单/设置窗开关写回 config 的
// permissions.unattended（D185 口径四）：成功后同步活开关，工具与提问链路即时生效。
func (c *cfgWriter) persistUnattended(on bool) error {
	if err := persistConfig(c.path, func(generic map[string]any) {
		perms, _ := generic["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
			generic["permissions"] = perms
		}
		perms["unattended"] = on
	}); err != nil {
		return err
	}
	if c.unattended != nil {
		c.unattended.Set(on)
	}
	return nil
}

// persistPlugins 实现 /plugin 状态写回 config 的 plugins 段（D31）：键缺失语义靠
// 省略表达（enabled 缺省启用、granted 缺省空、trusted 缺省未信任）。
// trusted 是 D138 的启动信任指纹：**随 profile 走**（本文件就在 profiles/<name>/ 下）——两个
// profile 对同一个插件可以有各自的信任决定，不会因切 profile 而串味。
func (c *cfgWriter) persistPlugins(states map[string]plugin.State) error {
	return persistConfig(c.path, func(generic map[string]any) {
		pm := make(map[string]any, len(states))
		for name, st := range states {
			entry := map[string]any{}
			if st.Enabled != nil {
				entry["enabled"] = *st.Enabled
			}
			if len(st.Granted) > 0 {
				entry["granted"] = st.Granted
			}
			if st.Trusted != "" {
				entry["trusted"] = st.Trusted
			}
			pm[name] = entry
		}
		generic["plugins"] = pm
	})
}

// persistModel 实现 /model 切换写回 config 的 model.name（D32）。
func (c *cfgWriter) persistModel(name string) error {
	return persistConfig(c.path, func(generic map[string]any) {
		modelSection(generic)["name"] = name
	})
}

// persistThink / persistEffort 实现 /think /effort 写回 config 的 model 段（D34；
// effort 空档 = 清除键，不发送）。
func (c *cfgWriter) persistThink(on bool) error {
	return persistConfig(c.path, func(generic map[string]any) {
		modelSection(generic)["think"] = on
	})
}

func (c *cfgWriter) persistEffort(level string) error {
	return persistConfig(c.path, func(generic map[string]any) {
		model := modelSection(generic)
		if level == "" {
			delete(model, "reasoning_effort") // off = 清除（不发送）
			return
		}
		model["reasoning_effort"] = level
	})
}

// persistToolsDisabled 写回 config 的 tools.disabled（S6/D143）：只在设置窗动开关时
// 调用（推导态不写盘）；空集 = 显式空数组（≠ 键缺失的推导 ["think"]，见 builtinDisabled）。
func (c *cfgWriter) persistToolsDisabled(disabled []string) error {
	return persistConfig(c.path, func(generic map[string]any) {
		tools, _ := generic["tools"].(map[string]any)
		if tools == nil {
			tools = map[string]any{}
			generic["tools"] = tools
		}
		if len(disabled) == 0 {
			disabled = []string{} // 显式空数组（≠ 键缺失推导 ["think"]；nil 会编码成 null）
		}
		tools["disabled"] = disabled
	})
}
