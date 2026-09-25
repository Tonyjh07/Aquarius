package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	// APIKey "secret:<环境变量名>" 引用（port.Secrets 按名取用）或明文（D35：
	// 启动打印警告、不回显）；空值回落默认 secret:AQUARIUS_OPENAI_KEY。
	APIKey string `json:"api_key"`
	// Tokenizer 本地 tokenizer.json 路径（精确计数②，D26）；空 = 通用估算③。
	Tokenizer string `json:"tokenizer"`
	// Think 原生思考总开关（/think 写回，D34）；nil = 键缺失（展示为开、不发布尔）。
	Think *bool `json:"think,omitempty"`
	// ReasoningEffort 推理档位（/effort 写回，D34）；空 = 不发送。
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ThinkTool think 草稿工具可见性（D34）；默认 false（隐藏），改后重启生效。
	ThinkTool bool `json:"think_tool,omitempty"`
	// UnsupportedParams 服务端已知不认的请求参数（D34 自动记录，启动注入省略）。
	UnsupportedParams []string `json:"unsupported_params,omitempty"`
}

// uiConfig UI 形态：repl | tui（D33；模板与键缺省为 tui，repl 为测试/e2e 后端）。
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
    "tokenizer": "",
    "think": true,
    "reasoning_effort": "",
    "think_tool": false,
    "unsupported_params": []
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

// secretName 校验 secret 引用并返回环境变量名。
func secretName(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	name := strings.TrimPrefix(ref, "secret:")
	if ref == name || name == "" {
		return "", fmt.Errorf("model.api_key 的 secret: 引用缺少环境变量名，当前为 %q（或直接填明文，D35）", ref)
	}
	return name, nil
}

// resolveAPIKey 解析 model.api_key（D35，文件内值优先）：
//   - 空 → 回落默认引用 secret:AQUARIUS_OPENAI_KEY（经 Secrets 按名取用；
//     环境变量缺失由 Secrets 报因）
//   - secret:<环境变量名> → 经 port.Secrets 按名取用
//   - 其余 → 视为明文直接使用，启动打印警告（**不回显密钥值**）
func resolveAPIKey(ctx context.Context, ref string, stderr io.Writer) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "secret:AQUARIUS_OPENAI_KEY"
	}
	if !strings.HasPrefix(ref, "secret:") {
		fmt.Fprintln(stderr, "警告：model.api_key 为明文（D35）——密钥直接落在 config.json，"+
			"本机任意可读进程、备份与同步都可能取用；建议改用 \"secret:<环境变量名>\" 引用环境变量。")
		return ref, nil
	}
	name, err := secretName(ref)
	if err != nil {
		return "", err
	}
	return (envSecrets{}).Get(ctx, name)
}
