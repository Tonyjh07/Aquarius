package port

import (
	"context"
	"errors"
	"time"
)

// ErrCharacterNotFound 按 slug 载入角色卡未命中（§5.17）。命令面据此区分
// 「名字打错了」与「读不出」，前者提示用 /character list 看清单。
var ErrCharacterNotFound = errors.New("character not found")

// CharacterInfo 一张已导入角色卡的最小事实（列表与详情展示用）。
//
// 只展示的字段（Creator / Version / Tags / ImportedAt / Warnings）不进模型上下文：
// 它们是给用户看的来路说明，不是要遵循的指令。
type CharacterInfo struct {
	Slug        string   // 目录名：由显示名派生，profile 内唯一
	Name        string   // 卡里的 name
	Display     string   // nickname 优先，否则 name；= {{char}} 的取值
	Creator     string   // 只展示
	Version     string   // character_version，只展示
	Tags        []string // 只展示
	Source      string   // png | json | charx
	Spec        string   // v2 | v3 | v3+（高于 3.x 的未知版本；V1 不读）
	Greetings   int      // alternate_greetings 条数
	BookEntries int      // character_book 条目数（保留；按开关决定是否注入常驻条目）
	Assets      int      // 卡里 data.assets 条数（图标之外的表情 / 背景 / 语音等；保留未用）
	HasAvatar   bool     // 有 avatar.png
	// AvatarPath 头像文件的绝对路径（空 = 没有头像）。只给界面呈现用（设置窗「角色」页）：
	// 它是落位事实，不是卡内容，绝不进模型上下文。
	AvatarPath string
	ImportedAt time.Time // 导入时间（meta.json 缺时为零值）
	// Unsupported 本版不执行的机制（按固定顺序，规范键名）：regex_scripts / tavern_helper /
	// tavernweave / depth_prompt。这些键随原卡留档、本版一律不运行；导入回执据此如实告知。
	Unsupported []string
	Warnings    []string // 跳过 / 不认识的东西（用户面；不进模型上下文）
}

// CharacterProfile 一张卡投影成会话所需的文本（原文，未做宏替换、未做拼装）。
//
// 字段分列而不预拼成一段：哪几段进人格、按什么顺序、要不要留，是投影决策（§4.1/D247），
// 归 app 的人格模块树；适配器只负责如实读出原文。
type CharacterProfile struct {
	Description  string      // 卡的角色描述
	Personality  string      // 卡的性格
	Scenario     string      // 卡的情境
	SystemPrompt string      // 卡里的 system_prompt（{{original}} 留到组合时才展开）
	BookEntries  []BookEntry // 世界书全部条目（启用与常量标志原样带出，过滤在 app）
	Examples     string      // mes_example（<START> 保留为分块符）
	FirstMes     string      // 首条开场白
	Greetings    []string    // alternate_greetings（备用问候）
	PostHistory  string      // post_history_instructions（进会话 Tail）
}

// BookEntry 世界书条目（§5.17/D294）。解析层只如实读出：常量与启用位、检索用的触发词、
// 排序权重与位点。「哪些算常驻、哪些命中、装不装得下」全部由 app 层判定——那是投影口径，
// 不是卡的形状。
type BookEntry struct {
	Comment  string // 条目标题（作常驻段的小节标题、`/lore` 里的条目名）
	Content  string
	Enabled  bool // extensions.enabled / enabled；缺键 = true
	Constant bool // constant；缺键 = false
	// Keys 主触发词（D294）：`keys` 与老格式的 `key` 合并后的结果，宏尚未展开。
	Keys []string
	// SecondaryKeys 次要触发词：与 `selective` 配合，本版只带出、不参与判定。
	SecondaryKeys []string
	// Order 排序权重（`insertion_order` 优先，其次 `order`）：大者优先装进预算。
	Order int
	// Position 插入位点。两代写法冲突时以 `extensions.position` 的数字为准。
	Position BookPosition
	// Depth 按深度插入时，插在倒数第几条消息之前（`extensions.depth`，缺键 = 4）。
	Depth int
	// Role 按深度插入时的身份：system / user / assistant（`extensions.role`，缺键 = system）。
	Role string
	// CaseSensitive 匹配是否区分大小写（`case_sensitive`，缺键 = 不区分）。
	CaseSensitive bool
}

// BookPosition 世界书条目的插入位点（§5.17/D294）。数字沿用 SillyTavern 的
// `extensions.position`：0 人格前、1 人格后、2 作者注前、3 作者注后、4 按深度。
//
// 解析层保留全部五档，app 层只落两档（人格后与按深度）——把「哪几档值得做」留成投影决策。
type BookPosition int

const (
	BookBeforeChar BookPosition = iota
	BookAfterChar
	BookBeforeAuthorNote
	BookAfterAuthorNote
	BookAtDepth
)

// BookRoleSystem / BookRoleUser / BookRoleAssistant 按深度插入的身份（D294）。
const (
	BookRoleSystem    = "system"
	BookRoleUser      = "user"
	BookRoleAssistant = "assistant"
)

// CharacterCard 一张卡的完整内容：事实 + 投影原文（头像路径在 Info.AvatarPath）。
type CharacterCard struct {
	Info    CharacterInfo
	Profile CharacterProfile
}

// CharacterCatalog 角色卡目录只读端口（§5.17）。
//
// 导入 / 删除是用户面动作，不进端口：模型侧没有角色卡工具，命令面与管理面共用
// 装配根实现的那一份导入器（范式同 app.SkillAdmin）。
type CharacterCatalog interface {
	// List 已导入角色卡，按显示名排序。
	List(ctx context.Context) ([]CharacterInfo, error)
	// Load 按 slug 取一张卡；未导入该 slug 时返回包装了 ErrCharacterNotFound 的错误。
	Load(ctx context.Context, slug string) (CharacterCard, error)
}
