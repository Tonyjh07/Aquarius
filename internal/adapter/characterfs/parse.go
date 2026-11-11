// Package characterfs implements the read-only character card catalog (§5.17):
// it reads TavernCard-style cards (PNG tEXt, plain JSON, .charx) into the
// projection types the composer consumes, and stores them under
// <profile>/characters/<slug>/.
//
// Adapters here read the card truthfully; deciding which fields enter the prompt
// is the composer's job. Import and delete are user actions, so this package
// implements app.CharacterAdmin at the assembly root, not a port.
package characterfs

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 上限（§5.17「上限与安全」）：外部文件一律不可信，超限一律报错、不截断。
const (
	// maxCardFileBytes 单卡文件（PNG / JSON）上限。
	maxCardFileBytes = 64 << 20
	// maxCardJSONBytes card.json 解码后上限。
	maxCardJSONBytes = 32 << 20
	// maxFieldBytes 单个投影文本字段上限（只管七个投影字段与常驻世界书单条）。
	maxFieldBytes = 256 << 10
	// maxTailRunes post_history_instructions 折进会话 Tail 的长度上限（字符数）。
	maxTailRunes = 8000
)

var (
	pngMagic = []byte("\x89PNG\r\n\x1a\n")
	zipMagic = []byte("PK\x03\x04")
	bom      = []byte{0xEF, 0xBB, 0xBF}
)

// ParsedCard 一张解析完成的卡（§5.17）：投影字段 + 原卡字节 + 头像来源。
//
// 只读、不猜：RawJSON 是原卡 JSON 逐字节（落 card.json 用，未知键一个不删）；
// Avatar 是头像的**来源像素**（PNG 卡 = 卡文件本身，落盘时去 tEXt 重编码的活另做；
// JSON 卡 = nil）；Archive 是 `.charx` 的原压缩包整份（落 source.charx 用，其余卡为 nil）。
type ParsedCard struct {
	Info    port.CharacterInfo
	Profile port.CharacterProfile
	RawJSON []byte
	Avatar  []byte
	Archive []byte
}

// parseCard 按容器识别并解析一张卡（§5.17）：PNG tEXt / 纯 JSON / .charx。
// sourceName 只用于按扩展名区分 JSON（内容看着像，扩展名也得对得上）。
func parseCard(sourceName string, data []byte) (ParsedCard, error) {
	if len(data) > maxCardFileBytes {
		return ParsedCard{}, errors.New(i18n.Tf("char.load.file_too_big", "size", len(data)>>20, "limit", maxCardFileBytes>>20))
	}
	switch {
	case bytes.HasPrefix(data, pngMagic):
		raw, err := pngCardJSON(data)
		if err != nil {
			return ParsedCard{}, err
		}
		out, _, err := parseCardJSON(raw)
		if err != nil {
			return ParsedCard{}, err
		}
		out.Info.Source = "png"
		out.RawJSON = raw
		out.Avatar = data // 原图；落盘时重编码只留像素（§5.17）
		return out, nil
	case bytes.HasPrefix(data, zipMagic):
		return parseCharxCard(data)
	case strings.EqualFold(filepath.Ext(sourceName), ".json") && looksLikeJSONObject(data):
		out, _, err := parseCardJSON(data)
		if err != nil {
			return ParsedCard{}, err
		}
		out.Info.Source = "json"
		out.RawJSON = data
		return out, nil
	default:
		return ParsedCard{}, errors.New(i18n.T("char.load.unknown_format"))
	}
}

// looksLikeJSONObject 去 BOM 与空白后首字符是否为 `{`。
func looksLikeJSONObject(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(bytes.TrimPrefix(data, bom)), []byte("{"))
}

// parseCardJSON 解析一份卡 JSON（§5.17）：有 `data` 就只读 `data`，否则按顶层尽力读
// （纯 V1 拒绝）。字段分列读入，不预拼——拼法与取舍归 app 的人格模块树。
// 同时返回生效的 data 对象（`.charx` 解析图标资产要用）。
func parseCardJSON(raw []byte) (ParsedCard, rawCardData, error) {
	clean := bytes.TrimPrefix(raw, bom) // 进场先去 BOM：json.Unmarshal 拒它
	if len(clean) > maxCardJSONBytes {
		return ParsedCard{}, rawCardData{}, errors.New(i18n.Tf("char.load.json_too_big", "size", len(clean)>>20, "limit", maxCardJSONBytes>>20))
	}
	var rc rawCard
	if err := json.Unmarshal(clean, &rc); err != nil {
		return ParsedCard{}, rawCardData{}, fmt.Errorf("%s: %w", i18n.T("char.load.json_parse"), err)
	}

	var (
		d        rawCardData
		warnings []string
	)
	spec, warn := detectSpec(rc.Spec, rc.SpecVersion)
	if warn != "" {
		warnings = append(warnings, warn)
	}
	if rc.Data != nil {
		// 有 data 对象就只读 data：实测顶层与 data 之间的镜像字段两边不一致，
		// 合并会把「第二份数据」当成真值。
		d = *rc.Data
	} else {
		var top rawCardData
		if err := json.Unmarshal(clean, &top); err != nil {
			return ParsedCard{}, rawCardData{}, fmt.Errorf("%s: %w", i18n.T("char.load.json_top_parse"), err)
		}
		if isPureV1(top) {
			return ParsedCard{}, rawCardData{}, errors.New(i18n.T("char.load.pure_v1"))
		}
		if spec == "" {
			spec = "v2"
		}
		warnings = append(warnings, i18n.T("char.load.no_data_fallback"))
		d = top
	}
	if spec == "" {
		spec = "v2"
	}

	name := strings.TrimSpace(d.Name)
	if name == "" {
		return ParsedCard{}, rawCardData{}, errors.New(i18n.T("char.load.name_missing"))
	}
	display := strings.TrimSpace(d.Nickname)
	if display == "" {
		display = name
	}

	if err := checkProjectedFields(d); err != nil {
		return ParsedCard{}, rawCardData{}, err
	}

	entries := make([]port.BookEntry, 0, bookLen(d))
	for _, e := range d.bookEntries() {
		if len(e.Content) > maxFieldBytes {
			return ParsedCard{}, rawCardData{}, errors.New(i18n.Tf("char.load.book_entry_too_big", "comment", e.Comment, "limit", maxFieldBytes>>10))
		}
		entries = append(entries, bookEntry(e))
	}

	info := port.CharacterInfo{
		Name:        name,
		Display:     display,
		Creator:     d.Creator,
		Version:     d.CharacterVersion,
		Tags:        d.Tags,
		Spec:        spec,
		Greetings:   len(d.AlternateGreetings),
		BookEntries: len(entries),
		Assets:      len(d.Assets),
		Unsupported: detectUnsupported(clean),
		Warnings:    warnings,
	}
	profile := port.CharacterProfile{
		Description:  d.Description,
		Personality:  d.Personality,
		Scenario:     d.Scenario,
		SystemPrompt: d.SystemPrompt,
		BookEntries:  entries,
		Examples:     d.MesExample,
		FirstMes:     d.FirstMes,
		Greetings:    d.AlternateGreetings,
		PostHistory:  d.PostHistoryInstructions,
	}
	return ParsedCard{Info: info, Profile: profile}, d, nil
}

// checkProjectedFields 七个投影字段各自的 256 KiB 上限 + Tail 的 8000 字符上限（§5.17）。
// 只管投影字段：正则脚本、扩展一类不投影的字段不设单字段上限（卡的真实体积几乎都在那里）。
func checkProjectedFields(d rawCardData) error {
	fields := []struct{ name, val string }{
		{"description", d.Description},
		{"personality", d.Personality},
		{"scenario", d.Scenario},
		{"first_mes", d.FirstMes},
		{"mes_example", d.MesExample},
		{"system_prompt", d.SystemPrompt},
		{"post_history_instructions", d.PostHistoryInstructions},
	}
	for _, f := range fields {
		if len(f.val) > maxFieldBytes {
			return errors.New(i18n.Tf("char.load.field_too_big", "field", f.name, "limit", maxFieldBytes>>10))
		}
	}
	if n := len([]rune(d.PostHistoryInstructions)); n > maxTailRunes {
		return errors.New(i18n.Tf("char.load.tail_too_long", "n", n, "max", maxTailRunes))
	}
	return nil
}

// unsupportedKeys 本版不执行的机制键（固定顺序，规范键名）。检测扫四个 scope（§5.17）：
// 顶层、`data`、顶层 `extensions`、`data.extensions`。
var unsupportedKeys = []string{"regex_scripts", "tavern_helper", "tavernweave", "depth_prompt"}

// detectUnsupported 检测卡里带了哪些本版不执行的机制（§5.17 导入回执的第三类）。
// 只判「键存在且非空」，不解析内容——这些机制一律不运行，内容多寡不影响结论。
func detectUnsupported(clean []byte) []string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(clean, &top); err != nil {
		return nil
	}
	scopes := []map[string]json.RawMessage{top}
	if ext := jsonObject(top["extensions"]); ext != nil {
		scopes = append(scopes, ext)
	}
	if data := jsonObject(top["data"]); data != nil {
		scopes = append(scopes, data)
		if ext := jsonObject(data["extensions"]); ext != nil {
			scopes = append(scopes, ext)
		}
	}
	var out []string
	for _, key := range unsupportedKeys {
		for _, sc := range scopes {
			if raw, ok := sc[key]; ok && !emptyJSON(raw) {
				out = append(out, key)
				break
			}
		}
	}
	return out
}

// jsonObject 把一段 JSON 原始值当对象读；不是对象（含 null）时返回 nil。
func jsonObject(raw json.RawMessage) map[string]json.RawMessage {
	if emptyJSON(raw) {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// emptyJSON 把 `null` / `{}` / `[]` / `""` / 空白视作空值（§5.17：空的机制键不算「带了机制」）。
func emptyJSON(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	switch {
	case len(t) == 0:
		return true
	case bytes.Equal(t, []byte("null")), bytes.Equal(t, []byte("{}")), bytes.Equal(t, []byte("[]")):
		return true
	case t[0] == '"': // 空字符串
		var s string
		return json.Unmarshal(t, &s) == nil && s == ""
	}
	return false
}

// bookEntries 世界书条目（缺 character_book 时为空）。
func (d rawCardData) bookEntries() []rawBookEntry {
	if d.CharacterBook == nil {
		return nil
	}
	return d.CharacterBook.Entries
}

// bookLen 条目数（避免 nil 解引用）。
func bookLen(d rawCardData) int {
	if d.CharacterBook == nil {
		return 0
	}
	return len(d.CharacterBook.Entries)
}

// bookEnabled 条目启用标志（§5.17）：`extensions.enabled` / `enabled`，缺键 = true。
func bookEnabled(e rawBookEntry) bool {
	if e.Extensions.Enabled != nil {
		return *e.Extensions.Enabled
	}
	if e.Enabled != nil {
		return *e.Enabled
	}
	return true
}

// bookEntry 把一条原始条目归一成端口形状（§5.17/D294）。这里只做「两种写法二选一」的合并，
// 不做任何取舍——哪些算常驻、哪些命中、装不装得下，全在 app 层。
func bookEntry(e rawBookEntry) port.BookEntry {
	return port.BookEntry{
		Comment:       bookTitle(e),
		Content:       e.Content,
		Enabled:       bookEnabled(e),
		Constant:      e.Constant,
		Keys:          mergeKeys(e.Keys, e.Key),
		SecondaryKeys: mergeKeys(e.SecondaryKeys, e.KeySecondary),
		Order:         bookOrder(e),
		Position:      bookPosition(e),
		Depth:         bookDepth(e),
		Role:          bookRole(e),
		CaseSensitive: bookCaseSensitive(e),
	}
}

// bookTitle 条目名：`comment` 优先，其次 `name`（V3 用 name，老卡用 comment）。
func bookTitle(e rawBookEntry) string {
	if s := strings.TrimSpace(e.Comment); s != "" {
		return s
	}
	return strings.TrimSpace(e.Name)
}

// mergeKeys 合并两代触发词写法（`keys` 与 `key`、`secondary_keys` 与 `keysecondary`），
// 去空白、去重、保持出现顺序。空串留在列表里会让子串匹配恒命中，必须剔掉。
func mergeKeys(lists ...[]string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, list := range lists {
		for _, k := range list {
			k = strings.TrimSpace(k)
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// bookOrder 排序权重：`insertion_order` 优先，其次老卡的 `order`，都没有 = 0。
func bookOrder(e rawBookEntry) int {
	if e.InsertionOrdr != nil {
		return *e.InsertionOrdr
	}
	if e.Order != nil {
		return *e.Order
	}
	return 0
}

// bookPosition 插入位点：`extensions.position`（数字）优先于顶层 `position`（字符串或数字）。
// 两者并存且不一致是真卡里的常态（D294），数字是较新的一代。
func bookPosition(e rawBookEntry) port.BookPosition {
	if e.Extensions.Position != nil {
		return clampBookPosition(*e.Extensions.Position)
	}
	if len(e.Position) > 0 {
		var n int
		if json.Unmarshal(e.Position, &n) == nil {
			return clampBookPosition(n)
		}
		var s string
		if json.Unmarshal(e.Position, &s) == nil {
			return bookPositionByName(s)
		}
	}
	return port.BookAfterChar
}

// clampBookPosition 数字位点收进已知五档；负数按人格前、超界按人格后（保守：不往历史里塞）。
func clampBookPosition(n int) port.BookPosition {
	switch {
	case n < 0:
		return port.BookBeforeChar
	case n > 4:
		return port.BookAfterChar
	}
	return port.BookPosition(n)
}

// bookPositionByName 字符串写法的位点；认不出的按人格后（缺省档）。
func bookPositionByName(s string) port.BookPosition {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "0", "before_char":
		return port.BookBeforeChar
	case "1", "after_char":
		return port.BookAfterChar
	case "2", "before_an", "before_author_note":
		return port.BookBeforeAuthorNote
	case "3", "after_an", "after_author_note":
		return port.BookAfterAuthorNote
	case "4", "at_depth":
		return port.BookAtDepth
	default:
		return port.BookAfterChar
	}
}

// bookDepth 按深度插入的位置（缺键 = 4）：负数没有意义，收到 0。
func bookDepth(e rawBookEntry) int {
	if e.Extensions.Depth == nil {
		return 4
	}
	if d := *e.Extensions.Depth; d > 0 {
		return d
	}
	return 0
}

// bookRole 按深度插入的身份：0 system / 1 user / 2 assistant（缺键与未知值都按 system）。
func bookRole(e rawBookEntry) string {
	if e.Extensions.Role == nil {
		return port.BookRoleSystem
	}
	switch *e.Extensions.Role {
	case 1:
		return port.BookRoleUser
	case 2:
		return port.BookRoleAssistant
	default:
		return port.BookRoleSystem
	}
}

// bookCaseSensitive 匹配是否区分大小写（`extensions.case_sensitive` 优先，缺键 = 不区分）。
func bookCaseSensitive(e rawBookEntry) bool {
	if e.Extensions.CaseSensitive != nil {
		return *e.Extensions.CaseSensitive
	}
	if e.CaseSensitive != nil {
		return *e.CaseSensitive
	}
	return false
}

// isPureV1 顶层六字段齐且没有 data —— 纯 V1 卡（本版不读）。
func isPureV1(top rawCardData) bool {
	for _, v := range []string{top.Name, top.Description, top.Personality, top.Scenario, top.FirstMes, top.MesExample} {
		if strings.TrimSpace(v) == "" {
			return false
		}
	}
	return true
}

// detectSpec 宽松判定卡的规范版本（§5.17）：`spec_version` 按**字符串**比较（实测都是
// "2.0" / "3.0"，按 JSON 数字解析会漏掉整批卡）。高于 3.x 的仍导入并记 v3+ 警告，已知
// 字段照常读——这是只读导入，拒绝反而让用户没法看新卡。
func detectSpec(spec, version string) (string, string) {
	low := strings.ToLower(spec)
	ver := strings.TrimSpace(version)
	major := ver
	if i := strings.IndexByte(ver, '.'); i >= 0 {
		major = ver[:i]
	}
	switch {
	case strings.Contains(low, "v3") || strings.Contains(low, "ccv3") || major == "3":
		return "v3", ""
	case strings.Contains(low, "v2") || major == "2" || major == "":
		return "v2", ""
	default:
		if n, err := strconv.Atoi(major); err == nil && n > 3 {
			return "v3+", i18n.Tf("char.load.spec_too_new", "version", ver)
		}
		return "v2", i18n.Tf("char.load.spec_unknown", "version", ver)
	}
}

// pngCardJSON 取出 PNG 里内嵌的卡 JSON（§5.17）：遍历 tEXt 块、关键字大小写不敏感、
// `ccv3` 优先于 `chara`；base64 解码后返回 JSON 字节。只读 tEXt——iTXt / zTXt 不读。
func pngCardJSON(data []byte) ([]byte, error) {
	text, err := pngText(data)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeBase64Loose(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("char.load.png_bad_base64"), err)
	}
	return decoded, nil
}

// pngText 返回卡 JSON 的 base64 文本（ccv3 优先，其次 chara）。
func pngText(data []byte) (string, error) {
	var chara, ccv3 string
	var hasChara, hasCCV3 bool
	for off := 8; off+8 <= len(data); {
		length := int(binary.BigEndian.Uint32(data[off : off+4]))
		typ := string(data[off+4 : off+8])
		start := off + 8
		if length < 0 || start+length+4 > len(data) {
			break // 长度异常或尾部截断：停扫，不因后半段损坏否掉已读到的卡
		}
		if typ == "tEXt" {
			if kw, text, ok := splitTextChunk(data[start : start+length]); ok {
				switch strings.ToLower(kw) {
				case "ccv3":
					ccv3, hasCCV3 = text, true
				case "chara":
					chara, hasChara = text, true
				}
			}
		}
		off = start + length + 4 // 跳过数据与 4 字节 CRC
	}
	switch {
	case hasCCV3:
		return ccv3, nil
	case hasChara:
		return chara, nil
	default:
		return "", errors.New(i18n.T("char.load.png_no_card"))
	}
}

// splitTextChunk 拆一个 tEXt 块：`关键字\0文本`。
func splitTextChunk(b []byte) (kw, text string, ok bool) {
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return "", "", false
	}
	return string(b[:i]), string(b[i+1:]), true
}

// decodeBase64Loose 容忍有/无 padding 的 base64（个别卡不带）。
func decodeBase64Loose(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}
