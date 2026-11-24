package tierone

import (
	"errors"
	"slices"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// Provides 的合法取值（Manifest.Provides 词表冻结；新增供给面先改这里再改契约）。
const (
	PortLLM        = "llm"
	PortTool       = "tool"
	PortMemory     = "memory"
	PortIngest     = "ingest"
	PortOutput     = "output"
	PortTranscribe = "transcribe"
	PortSynthesize = "synthesize"
	PortSecrets    = "secrets"
	PortCommand    = "command"
)

// providesPorts 词表全集，顺序即 /plugin 的展示序。
var providesPorts = []string{
	PortLLM, PortTool, PortMemory, PortIngest, PortOutput,
	PortTranscribe, PortSynthesize, PortSecrets, PortCommand,
}

// validateManifest 启动期的清单校验（§7 第 2 条）。只查**声明**，不碰实例——
// 「声明 llm 就必须实现 LanguageModel」要 Create 之后才验得了（见 checkProvides）。
// 校验失败停在 Stopped 并记因，不影响其他插件（软启动）。
func validateManifest(m v1.Manifest, taken func(string) bool) error {
	if err := validatePluginName(m.Name); err != nil {
		return err
	}
	if m.API != v1.APIVersion {
		return errors.New(i18n.Tf("plugin.t1.api_mismatch",
			"name", m.Name, "api", m.API, "want", v1.APIVersion))
	}
	if taken != nil && taken(m.Name) {
		return errors.New(i18n.Tf("plugin.t1.name_taken", "name", m.Name))
	}
	for _, p := range m.Provides {
		if !slices.Contains(providesPorts, p) {
			return errors.New(i18n.Tf("plugin.t1.unknown_provides", "name", m.Name, "port", p))
		}
	}
	return nil
}

// validatePluginName 插件名同时进工具名（`<name>_<tool>`）与命令名，与 Tier-2 的
// server 名同一套限制：不许冒号与空白，保证按冒号切分无歧义。
func validatePluginName(name string) error {
	if name == "" {
		return errors.New(i18n.T("plugin.cfg.name_empty"))
	}
	if strings.ContainsAny(name, ": \t") {
		return errors.New(i18n.Tf("plugin.cfg.name_chars", "name", name))
	}
	return nil
}

// checkProvides 声明与实现的自检（§7 第 2 条落地在 Create 之后）：声明了哪个面，
// 实例就必须实现对应接口，否则拒载——不然要等到第一次调用才发现接错了。
func checkProvides(inst v1.Instance, provides []string) error {
	for _, p := range provides {
		if !implements(inst, p) {
			return errors.New(i18n.Tf("plugin.t1.provides_mismatch", "port", p))
		}
	}
	return nil
}

// implements 单个供给面的 type assertion（声明与实现的唯一对位表）。
func implements(inst v1.Instance, port string) bool {
	switch port {
	case PortLLM:
		_, ok := inst.(v1.LanguageModel)
		return ok
	case PortTool:
		_, ok := inst.(v1.ToolProvider)
		return ok
	case PortMemory:
		_, ok := inst.(v1.Memory)
		return ok
	case PortIngest:
		_, ok := inst.(v1.Ingestor)
		return ok
	case PortOutput:
		_, ok := inst.(v1.OutputAdapter)
		return ok
	case PortTranscribe:
		_, ok := inst.(v1.Transcriber)
		return ok
	case PortSynthesize:
		_, ok := inst.(v1.Synthesizer)
		return ok
	case PortSecrets:
		_, ok := inst.(v1.SecretProvider)
		return ok
	case PortCommand:
		_, ok := inst.(v1.CommandProvider)
		return ok
	}
	return false
}

// provides 是否声明了某个供给面。
func provides(m v1.Manifest, port string) bool { return slices.Contains(m.Provides, port) }
