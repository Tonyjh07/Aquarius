package plugingo

import (
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// convert_command.go 命令面的转换（plugin-design.md §3.1）：命令声明进 /help 与补全浮层，
// 执行仍走内核分派。名字给主名 + 别名（首名为主名），两端同一规则。

// toV1CommandSpec 内核命令清单条目 → 插件看到的形状（面板展示用）。
func toV1CommandSpec(c port.CommandInfo) v1.CommandSpec {
	out := v1.CommandSpec{
		Names:  append([]string(nil), c.Names...),
		Usage:  c.Usage,
		Desc:   c.Desc,
		Params: toV1Params(c.Params),
	}
	return out
}

// toPortCommandSpec 插件声明的命令 → 内核清单条目（注册表与 /help 用）。
func toPortCommandSpec(c v1.CommandSpec) port.CommandInfo {
	return port.CommandInfo{
		Names:  append([]string(nil), c.Names...),
		Usage:  c.Usage,
		Desc:   c.Desc,
		Params: toPortParams(c.Params),
	}
}

func toV1Params(in []port.CommandParam) []v1.CommandParam {
	if len(in) == 0 {
		return nil
	}
	out := make([]v1.CommandParam, 0, len(in))
	for _, p := range in {
		out = append(out, v1.CommandParam{
			Name:       p.Name,
			Desc:       p.Desc,
			Kind:       v1.ParamKind(p.Kind),
			Values:     append([]string(nil), p.Values...),
			ValueName:  p.ValueName,
			Repeatable: p.Repeatable,
		})
	}
	return out
}

func toPortParams(in []v1.CommandParam) []port.CommandParam {
	if len(in) == 0 {
		return nil
	}
	out := make([]port.CommandParam, 0, len(in))
	for _, p := range in {
		out = append(out, port.CommandParam{
			Name:       p.Name,
			Desc:       p.Desc,
			Kind:       port.ParamKind(p.Kind),
			Values:     append([]string(nil), p.Values...),
			ValueName:  p.ValueName,
			Repeatable: p.Repeatable,
		})
	}
	return out
}
