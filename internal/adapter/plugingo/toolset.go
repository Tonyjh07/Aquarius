package plugingo

// toolset.go 插件工具面 → 内核工具源（D340）。
//
// 插件声明的工具不是注册一次就固定的条目：清单按轮给（宿主每次装配请求都问一次），
// 名字可以随插件的导航状态变（「标题即工具」——调用某个记忆点就是下钻一层）。所以这个
// 适配器的两个方法都带 ctx，会话从 ctx 里取，不在构造时缓存声明。

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// ToolSet 一个插件的工具面在内核侧的身份。必须用指针：Runner 注销工具源靠接口值相等。
type ToolSet struct {
	name string
	p    v1.ToolProvider
	desc v1.Describer // nil = 插件没实现确认摘要
}

// NewToolSet 把插件的工具面接成工具源。名字只用于报因与日志归组。
func NewToolSet(name string, p v1.ToolProvider) *ToolSet {
	desc, _ := p.(v1.Describer)
	return &ToolSet{name: name, p: p, desc: desc}
}

var _ port.ToolSet = (*ToolSet)(nil)

// Name 源的名字（插件名）。
func (s *ToolSet) Name() string { return s.name }

// Specs 本轮可见的工具声明。空清单是合法状态（空库、游标停在根），不在这里兜底报错；
// 名字不合文法、同一轮内重名属于实现缺陷，整份拒掉并报因——模型端点不认识的名字会让
// 整个请求失败，早报比在协议层拿 400 好读。
func (s *ToolSet) Specs(ctx context.Context) ([]tool.Spec, error) {
	decls, err := s.decls(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tool.Spec, 0, len(decls))
	for _, d := range decls {
		out = append(out, s.wrap(d).Spec())
	}
	return out, nil
}

// Lookup 按名找本源的工具；不属于本源返回 (nil, nil)。名字与同一轮 Specs 同源，
// 所以模型点开的一定是它看到的那一条。
func (s *ToolSet) Lookup(ctx context.Context, name string) (port.Tool, error) {
	decls, err := s.decls(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range decls {
		if d.Name == name {
			return s.wrap(d), nil
		}
	}
	return nil, nil
}

// decls 取并校验本轮声明。
func (s *ToolSet) decls(ctx context.Context) ([]v1.ToolSpec, error) {
	decls := s.p.Tools(toV1Session(ctx))
	seen := make(map[string]bool, len(decls))
	for _, d := range decls {
		if !v1.ValidToolName(d.Name) {
			return nil, errToolName(s.name, d.Name)
		}
		if seen[d.Name] {
			return nil, errToolNameDup(s.name, d.Name)
		}
		seen[d.Name] = true
	}
	return decls, nil
}

// wrap 一条插件声明 → 内核工具。声明在查找时就地取当前的，不缓存：同一轮里插件中途
// 改表也不会让「看到的名字」和「执行的东西」错位（契约要求同轮稳定，这条只是兜底）。
func (s *ToolSet) wrap(d v1.ToolSpec) T1Tool {
	spec := toPortToolSpec(d)
	if s.desc != nil {
		name := spec.Name
		spec.Describe = func(args json.RawMessage) string {
			return s.desc.Describe(v1.ToolCall{Name: name, Args: args})
		}
	}
	return T1Tool{p: s.p, spec: spec, desc: s.desc}
}

// errToolName 工具名不合文法（`[A-Za-z0-9_-]{1,64}`）。
func errToolName(plugin, name string) error {
	return errors.New(i18n.Tf("plugin.t1.bad_tool_name", "name", plugin, "tool", name))
}

// errToolNameDup 同一轮声明里出现重名。
func errToolNameDup(plugin, name string) error {
	return errors.New(i18n.Tf("plugin.t1.dup_tool_name", "name", plugin, "tool", name))
}
