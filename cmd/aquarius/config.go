package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/plugin"
)

// fileConfig config.json 中当前里程碑消费的部分。
// DESIGN §8 的其余键（input 等）同样写入模板但由对应里程碑启用，
// encoding/json 对未知键宽容。
type fileConfig struct {
	Model        modelConfig       `json:"model"`
	UI           uiConfig          `json:"ui"`
	SystemPrompt string            `json:"system_prompt"` // 人格：空 = 内置默认（快照进 persona 首节点，D20）
	Permissions  permissionsConfig `json:"permissions"`
	Output       outputConfig      `json:"output"`
	Limits       limitsConfig      `json:"limits"`
	// MCPServers MCP server 声明（D30/D31，M4 启用；校验见 plugin.MCPServer.Validate）。
	MCPServers map[string]plugin.MCPServer `json:"mcpServers"`
	// Plugins 启停与授权状态（D31，config 与 plugin.json 两种发现源共用）。
	Plugins map[string]plugin.State `json:"plugins"`
}

// outputConfig 输出器开关（DESIGN §8；M3 启用 notify，tts 见 D27 解析但不启用）。
// 键缺失 = false（保守：老配置不自动弹通知，模板默认 notify=true）。
type outputConfig struct {
	Notify bool `json:"notify"`
	TTS    bool `json:"tts"`
}

// permissionsConfig 权限配置（D22：仅等级预设；allow/ask 明细键已移除，矩阵外一律 ask）。
type permissionsConfig struct {
	Level string `json:"level"` // 空 = strict（perm.DefaultLevel）
}

// modelConfig 模型接入配置。
type modelConfig struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	// APIKey 只允许 "secret:<环境变量名>" 引用——明文密钥禁止入配置（AGENTS 硬性规则 8 / DESIGN §8）。
	APIKey string `json:"api_key"`
	// Tokenizer 本地 tokenizer.json 路径（精确计数②，D26）；空 = 通用估算③。
	Tokenizer string `json:"tokenizer"`
}

// uiConfig UI 形态；M0 仅支持 "repl"（TUI 见里程碑 M4）。
type uiConfig struct {
	Kind string `json:"kind"`
}

// limitsConfig 运行限额（DESIGN §8）。
type limitsConfig struct {
	MaxContextTokens int     `json:"max_context_tokens"`
	MaxTurns         int     `json:"max_turns"`
	CompactThreshold float64 `json:"compact_threshold"` // 自动压缩阈值（D21 轨2，M2）
	ToolOutputChars  int     `json:"tool_output_chars"` // 工具结果截断（ToolRunner，M2）
	ToolTimeoutSec   int     `json:"tool_timeout_sec"`  // 工具超时秒（ToolRunner，M2）
}

// defaultConfig 首次运行写入的模板：DESIGN §8 示例的可运行子集
// （ui.kind 取 tui——D33 模板默认，repl 为测试/e2e 后端；mcpServers/plugins 为
// M4 MCP 接入的声明与状态，缺省皆空）。
const defaultConfig = `{
  "model": {
    "provider": "openai-compatible",
    "name": "gpt-4o-mini",
    "base_url": "https://api.openai.com/v1",
    "api_key": "secret:AQUARIUS_OPENAI_KEY",
    "tokenizer": ""
  },
  "ui": { "kind": "tui" },
  "system_prompt": "",
  "input": { "asr": "whisper-api", "mic": true },
  "output": { "tts": false, "notify": true },
  "mcpServers": {},
  "plugins": {},
  "permissions": { "level": "strict" },
  "limits": {
    "max_turns": 8,
    "max_context_tokens": 64000,
    "compact_threshold": 0.7,
    "tool_output_chars": 20000,
    "tool_timeout_sec": 60
  }
}
`

// loadConfig 读取并解析配置。
func loadConfig(path string) (*fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	var cfg fileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	return &cfg, nil
}

// writeDefaultConfig 落首次运行模板。
func writeDefaultConfig(path string) error {
	return os.WriteFile(path, []byte(defaultConfig), 0o644)
}

// secretName 校验 secret 引用并返回环境变量名（禁止明文密钥）。
func secretName(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	name := strings.TrimPrefix(ref, "secret:")
	if ref == name || name == "" {
		return "", fmt.Errorf("model.api_key 必须是 \"secret:<环境变量名>\"（禁止明文密钥写入配置），当前为 %q", ref)
	}
	return name, nil
}
