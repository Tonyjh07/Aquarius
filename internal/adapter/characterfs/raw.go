package characterfs

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

// rawBook 卡的 `character_book`（只取 entries；keys / scan_depth 一类本版不用于检索）。
type rawBook struct {
	Entries []rawBookEntry `json:"entries"`
}

// rawBookEntry 世界书条目。`enabled` 缺键默认 true；`extensions.enabled` 优先。
type rawBookEntry struct {
	Comment    string     `json:"comment"`
	Content    string     `json:"content"`
	Constant   bool       `json:"constant"`
	Enabled    *bool      `json:"enabled"`
	Extensions rawBookExt `json:"extensions"`
}

// rawBookExt 条目扩展里的启用位（SillyTavern 有时把它放这里）。
type rawBookExt struct {
	Enabled *bool `json:"enabled"`
}
