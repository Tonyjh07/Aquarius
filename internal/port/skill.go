package port

import (
	"context"
	"errors"
)

// ErrSkillNotFound 按名载入技能未命中（D201）。工具层据此区分「名字打错了」与「读不出」。
var ErrSkillNotFound = errors.New("skill not found")

// SkillInfo 一个已发现技能的最小事实（D201 口径三：只有这一层常驻工具描述）。
type SkillInfo struct {
	Name        string // frontmatter name（= 目录名；小写字母/数字/连字符）
	Description string // frontmatter description：做什么 + 什么时候该用
	Dir         string // 技能目录绝对路径（references/、scripts/ 的锚点）
	Source      string // 命中的技能根（多根时说明「哪一份生效了」）
}

// SkillContent 按名载入的技能正文（D201 口径三第二级）。
//
// Body 是 frontmatter 之后的原文（行尾已归一为 LF，未截断）：预算由工具层与
// ToolRunner 统一收口，适配器不猜调用方要多少。
type SkillContent struct {
	Info SkillInfo
	Body string
	Path string // SKILL.md 绝对路径（正文超预算时给 file_read 续读用）
	// BodyLine Body 首行在 SKILL.md 里的行号（1 基）。续读提示要与 file_read 的
	// 行号对齐，而 SKILL.md 开头还有 frontmatter，不能假设正文从第 1 行开始。
	BodyLine int
}

// SkillCatalog 技能清单只读端口（§5.15）。
//
// 安装/卸载/更新是用户面动作（D202），不进端口：模型侧只有「列」与「载」。
type SkillCatalog interface {
	// List 已发现的技能（技能根序 + 根内目录名序，确定性）。
	List(ctx context.Context) ([]SkillInfo, error)
	// Load 按名取正文；未安装该名字时返回包装了 ErrSkillNotFound 的错误。
	Load(ctx context.Context, name string) (SkillContent, error)
	// Warnings 被跳过或被同名覆盖的条目说明（面向用户，进日志与 /skill 列表；
	// 不进模型上下文——那里只有 List 的事实）。
	Warnings(ctx context.Context) []string
}
