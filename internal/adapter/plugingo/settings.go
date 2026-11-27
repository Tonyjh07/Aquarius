package plugingo

// settings.go 插件设置贡献 → 内核设置端口。
//
// 与 view.go 同一条思路：声明现问、不缓存。这里只做形状转换与 API 版本对齐，不做任何
// 取值校验——键与取值范围属于插件自己的语义，内核插手就会变成「宿主替插件做决定」。

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// Settings 一个插件的设置面在内核侧的身份。
type Settings struct {
	name string
	p    v1.SettingsSectionProvider
}

// NewSettings 把插件的设置面接成内核端口。名字只用于分组与回传定位。
func NewSettings(name string, p v1.SettingsSectionProvider) *Settings {
	return &Settings{name: name, p: p}
}

// Sections 本插件的设置分区（带插件名）。
func (s *Settings) Sections() []port.PluginSettingsSection {
	in := s.p.Sections()
	out := make([]port.PluginSettingsSection, 0, len(in))
	for _, sec := range in {
		rows := make([]port.PluginSettingRow, 0, len(sec.Rows))
		for _, r := range sec.Rows {
			rows = append(rows, port.PluginSettingRow{
				Key:    r.Key,
				Label:  r.Label,
				Help:   r.Help,
				Kind:   pluginKind(r.Kind),
				Value:  r.Value,
				Values: r.Values,
			})
		}
		out = append(out, port.PluginSettingsSection{
			Plugin:    s.name,
			ID:        sec.ID,
			Title:     sec.Title,
			Desc:      sec.Desc,
			Rows:      rows,
			Placement: pluginPlacement(sec.Placement),
		})
	}
	return out
}

// Apply 把一次提交原样交给插件。
func (s *Settings) Apply(ctx context.Context, section string, values map[string]string) error {
	return s.p.ApplySettings(toV1Session(ctx), v1.SettingsPatch{SectionID: section, Values: values})
}

// pluginPlacement 分区位置的词表归一。
//
// 未知值按内联处理：认不出的位置至少要能显示出来，把整段藏起来会让用户以为参数丢了。
func pluginPlacement(p v1.SettingsPlacement) string {
	if string(p) == port.PluginPlacementPage {
		return port.PluginPlacementPage
	}
	return port.PluginPlacementInline
}

// pluginKind 控件种类归一。
// 未知种类回落到 text 而不是丢弃：插件可能用上新版契约里的控件（或拼错），旧宿主至少
// 还能把当前值显示成一行文本——直接让那行消失，用户会以为参数不见了。
func pluginKind(k v1.FieldKind) string {
	switch string(k) {
	case port.PluginFieldText, port.PluginFieldTextarea, port.PluginFieldSwitch,
		port.PluginFieldSelect, port.PluginFieldNumber:
		return string(k)
	}
	return port.PluginFieldText
}
