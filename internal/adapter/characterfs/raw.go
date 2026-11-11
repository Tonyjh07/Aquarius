package characterfs

import "encoding/json"

// 卡 JSON 的读取形状（§5.17）。只声明我们读的键——原卡 JSON 逐字节留在 card.json，
// 未知键一个不删，所以这里不需要把它们都列出来。
//
// V2 / V3 的卡字段都在 `data` 对象里；V3 是 V2 的超集，多出的 `nickname` / `assets` 等
// 按需取用。`spec` / `spec_version` 在顶层。

type rawCard struct {
	Spec        string       `json:"spec"`
	SpecVersion string       `json:"spec_version"`
	Data        *rawCardData `json:"data"`
}

// rawCardData 一张卡的字段（data 对象，或没有 data 时的顶层兜底）。
type rawCardData struct {
	Name                    string     `json:"name"`
	Nickname                string     `json:"nickname"`
	Description             string     `json:"description"`
	Personality             string     `json:"personality"`
	Scenario                string     `json:"scenario"`
	FirstMes                string     `json:"first_mes"`
	MesExample              string     `json:"mes_example"`
	SystemPrompt            string     `json:"system_prompt"`
	PostHistoryInstructions string     `json:"post_history_instructions"`
	AlternateGreetings      []string   `json:"alternate_greetings"`
	CharacterBook           *rawBook   `json:"character_book"`
	Creator                 string     `json:"creator"`
	CharacterVersion        string     `json:"character_version"`
	CreatorNotes            string     `json:"creator_notes"`
	Tags                    []string   `json:"tags"`
	GroupOnlyGreetings      []string   `json:"group_only_greetings"`
	Assets                  []rawAsset `json:"assets"`
}

// rawAsset V3 的资产条目（`.charx` 的图标来源）：`type=icon` 的是头像，
// `uri` 三种写法见 §5.17。
type rawAsset struct {
	Type string `json:"type"`
	Name string `json:"name"`
	URI  string `json:"uri"`
	Ext  string `json:"ext"`
}

// rawBook 卡的 `character_book`（§5.17/D294）：只读 entries，卡级的 `scan_depth` 不读
// （扫描深度是全局配置，条目级与卡级都留给以后）。
type rawBook struct {
	Entries []rawBookEntry `json:"entries"`
}

// rawBookEntry 世界书条目。`enabled` 缺键默认 true、`extensions.enabled` 优先。
//
// 触发词、排序与位点都有**两代写法**（真卡里并存且会不一致，D294）：`keys` / `key`、
// `secondary_keys` / `keysecondary`、`insertion_order` / `order`、顶层 `position`（字符串）/
// `extensions.position`（数字）。指针字段用来区分「缺键」与「显式 0」。
type rawBookEntry struct {
	Comment       string          `json:"comment"`
	Name          string          `json:"name"`
	Content       string          `json:"content"`
	Constant      bool            `json:"constant"`
	Enabled       *bool           `json:"enabled"`
	Keys          []string        `json:"keys"`
	Key           []string        `json:"key"`
	SecondaryKeys []string        `json:"secondary_keys"`
	KeySecondary  []string        `json:"keysecondary"`
	InsertionOrdr *int            `json:"insertion_order"`
	Order         *int            `json:"order"`
	Position      json.RawMessage `json:"position"`
	CaseSensitive *bool           `json:"case_sensitive"`
	Extensions    rawBookExt      `json:"extensions"`
}

// rawBookExt 条目扩展：启用位（SillyTavern 有时把它放这里）、位点、深度与身份。
// 位点在这边是**数字**（0 人格前 / 1 人格后 / 2 作者注前 / 3 作者注后 / 4 按深度）。
type rawBookExt struct {
	Enabled       *bool `json:"enabled"`
	Position      *int  `json:"position"`
	Depth         *int  `json:"depth"`
	Role          *int  `json:"role"`
	CaseSensitive *bool `json:"case_sensitive"`
}
