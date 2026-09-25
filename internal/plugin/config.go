// Package plugin 插件宿主（DESIGN §6.4，M4）：发现、校验、授权（grant）、
// 生命周期与可观测。M4 只落 Tier-2（MCP server）；Tier-1 后移见 D29。
//
// 依赖方向：adapter/mcpgate ← plugin（宿主编排协议适配器）；本包只依赖 domain/port/标准库。
package plugin

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// 传输与风险的合法取值（D30/§6.3、§8）。
const (
	TransportStdio = "stdio"
	TransportHTTP  = "streamable-http"
	RiskSafe       = "safe"
	RiskConfirm    = "confirm"
)

// MCPConfig MCP server 启动描述（§6.3）：config `mcpServers.<name>` 的 mcp 段
// 与 `plugin.json` 的 `mcp` 段**字段一致**（声明与状态分离，D31）。
type MCPConfig struct {
	Transport string            `json:"transport"` // TransportStdio | TransportHTTP
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`     // 值可用 "secret:<环境变量名>" 引用
	URL       string            `json:"url,omitempty"`     // streamable-http 端点
	Headers   map[string]string `json:"headers,omitempty"` // 值可用 "secret:<环境变量名>" 引用
}

// Validate 启动声明自检（fail-fast）。
func (m MCPConfig) Validate(name string) error {
	switch m.Transport {
	case TransportStdio:
		if strings.TrimSpace(m.Command) == "" {
			return fmt.Errorf("插件 %q: stdio 形态缺 command", name)
		}
	case TransportHTTP:
		if strings.TrimSpace(m.URL) == "" {
			return fmt.Errorf("插件 %q: streamable-http 形态缺 url", name)
		}
	case "":
		return fmt.Errorf("插件 %q: transport 必填（%s | %s）", name, TransportStdio, TransportHTTP)
	default:
		return fmt.Errorf("插件 %q: 未知 transport %q（%s | %s）", name, m.Transport, TransportStdio, TransportHTTP)
	}
	return nil
}

// MCPServer config `mcpServers.<name>` 条目：启动描述 + 能力与风险声明（§8）。
type MCPServer struct {
	MCPConfig // 匿名内嵌：JSON 字段平铺（transport/command/…）
	// Capabilities 所需能力（如 network）；首次启用逐项确认后写入 State.Granted（D31）。
	Capabilities []string `json:"capabilities,omitempty"`
	// Risk 工具风险（"" 视为 safe）：confirm 的工具逐次走 Confirmer。
	Risk string `json:"risk,omitempty"`
}

// Validate 条目自检（启动 fail-fast）。
func (s MCPServer) Validate(name string) error {
	if err := validateServerName(name); err != nil {
		return err
	}
	if err := s.MCPConfig.Validate(name); err != nil {
		return err
	}
	return validateRisk(s.Risk, name)
}

// State config `plugins.<name>`：启停与授权状态（D31；键缺失 = 启用、未授权）。
type State struct {
	// Enabled 启停；nil = 默认启用（D31）。
	Enabled *bool `json:"enabled,omitempty"`
	// Granted capability allowlist（grant 确认的落盘处）。
	Granted []string `json:"granted,omitempty"`
}

// IsEnabled 键缺失按启用（D31 默认值）。
func (s State) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// HasGranted capability 是否已授权。
func (s State) HasGranted(cap string) bool { return slices.Contains(s.Granted, cap) }

// Decl 一个 server 的完整声明：config `mcpServers` 条目或 `plugin.json` 的合并产物
// （D31 声明与状态分离）。Source 标注发现来源，供 /plugin list 展示。
type Decl struct {
	Name string `json:"name"`
	MCPConfig
	Capabilities []string `json:"capabilities,omitempty"`
	Risk         string   `json:"risk,omitempty"`
	Source       string   `json:"source"` // "config" | "plugins"
}

// 发现来源取值（/plugin list 展示）。
const (
	SourceConfig  = "config"  // config mcpServers 条目
	SourcePlugins = "plugins" // ~/.aquarius/plugins/<name>/plugin.json
)

// ToolRisk 声明风险 → 工具风险（domain/tool；confirm 逐次走 Confirmer，§9）。
func (d Decl) ToolRisk() tool.Risk {
	if d.Risk == RiskConfirm {
		return tool.Confirm
	}
	return tool.Safe
}

// Manifest `plugin.json`（§6.3）：与 config `mcpServers` 同为声明来源（D31）。
type Manifest struct {
	Name         string    `json:"name"`
	MCP          MCPConfig `json:"mcp"`
	Capabilities []string  `json:"capabilities,omitempty"`
	Risk         string    `json:"risk,omitempty"`
}

// ParseManifest 解析并校验 plugin.json 字节。
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("解析 plugin.json: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate manifest 自检（§6.4 #2）。
func (m *Manifest) Validate() error {
	if err := validateServerName(m.Name); err != nil {
		return fmt.Errorf("plugin.json: %w", err)
	}
	if err := m.MCP.Validate(m.Name); err != nil {
		return fmt.Errorf("plugin.json: %w", err)
	}
	if err := validateRisk(m.Risk, m.Name); err != nil {
		return fmt.Errorf("plugin.json: %w", err)
	}
	return nil
}

// validateServerName 服务器名同时出现在 `mcp:<server>:<tool>` 工具名与
// `/mcp:<server>:<prompt>` 命令里——禁止冒号与空白，保证按冒号切分无歧义。
func validateServerName(name string) error {
	if name == "" {
		return fmt.Errorf("插件名为空")
	}
	if strings.ContainsAny(name, ": \t") {
		return fmt.Errorf("插件名 %q 含冒号或空白（工具/命令名按冒号切分，不允许）", name)
	}
	return nil
}

// validateRisk 风险取值校验（"" = safe）。
func validateRisk(risk, name string) error {
	switch risk {
	case "", RiskSafe, RiskConfirm:
		return nil
	default:
		return fmt.Errorf("插件 %q: 未知 risk %q（%s | %s）", name, risk, RiskSafe, RiskConfirm)
	}
}
