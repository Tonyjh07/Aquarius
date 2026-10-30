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
// JSON 卡 = nil）。
type ParsedCard struct {
	Info    port.CharacterInfo
	Profile port.CharacterProfile
	RawJSON []byte
	Avatar  []byte
}

// parseCard 按容器识别并解析一张卡（§5.17）：PNG tEXt / 纯 JSON / .charx。
// sourceName 只用于按扩展名区分 JSON（内容看着像，扩展名也得对得上）。
func parseCard(sourceName string, data []byte) (ParsedCard, error) {
	if len(data) > maxCardFileBytes {
		return ParsedCard{}, fmt.Errorf("卡文件 %d MiB 超过 %d MiB 上限", len(data)>>20, maxCardFileBytes>>20)
	}
	switch {
	case bytes.HasPrefix(data, pngMagic):
		raw, err := pngCardJSON(data)
		if err != nil {
			return ParsedCard{}, err
		}
		out, err := parseCardJSON(raw)
		if err != nil {
			return ParsedCard{}, err
		}
		out.Info.Source = "png"
		out.RawJSON = raw
		out.Avatar = data // 原图；落盘时重编码只留像素（§5.17）
		return out, nil
	case bytes.HasPrefix(data, zipMagic):
		return ParsedCard{}, errors.New("这是压缩包形式的角色卡（.charx），本版暂未接入")
	case strings.EqualFold(filepath.Ext(sourceName), ".json") && looksLikeJSONObject(data):
		out, err := parseCardJSON(data)
		if err != nil {
			return ParsedCard{}, err
		}
		out.Info.Source = "json"
		out.RawJSON = data
		return out, nil
	default:
		return ParsedCard{}, errors.New("认不出这是哪种角色卡（支持 PNG 与 JSON）")
	}
}

// looksLikeJSONObject 去 BOM 与空白后首字符是否为 `{`。
func looksLikeJSONObject(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(bytes.TrimPrefix(data, bom)), []byte("{"))
}

// parseCardJSON 解析一份卡 JSON（§5.17）：有 `data` 就只读 `data`，否则按顶层尽力读
// （纯 V1 拒绝）。字段分列读入，不预拼——拼法与取舍归 app 的人格模块树。
func parseCardJSON(raw []byte) (ParsedCard, error) {
	clean := bytes.TrimPrefix(raw, bom) // 进场先去 BOM：json.Unmarshal 拒它
	if len(clean) > maxCardJSONBytes {
		return ParsedCard{}, fmt.Errorf("卡 JSON 解码后 %d MiB 超过 %d MiB 上限", len(clean)>>20, maxCardJSONBytes>>20)
	}
	var rc rawCard
	if err := json.Unmarshal(clean, &rc); err != nil {
		return ParsedCard{}, fmt.Errorf("卡 JSON 解析失败: %w", err)
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
			return ParsedCard{}, fmt.Errorf("卡 JSON 顶层解析失败: %w", err)
		}
		if isPureV1(top) {
			return ParsedCard{}, errors.New("这是旧版规范（V1）的角色卡，本版不读；请导出为 V2 / V3 后再导入")
		}
		if spec == "" {
			spec = "v2"
		}
		warnings = append(warnings, "卡里没有规范的 data 结构，已按顶层字段尽力读取")
		d = top
	}
	if spec == "" {
		spec = "v2"
	}

	name := strings.TrimSpace(d.Name)
	if name == "" {
		return ParsedCard{}, errors.New("卡里没有 name（角色名必填，不从文件名猜）")
	}
	display := strings.TrimSpace(d.Nickname)
	if display == "" {
		display = name
	}

	if err := checkProjectedFields(d); err != nil {
		return ParsedCard{}, err
	}

	entries := make([]port.BookEntry, 0, bookLen(d))
	for _, e := range d.bookEntries() {
		if len(e.Content) > maxFieldBytes {
			return ParsedCard{}, fmt.Errorf("世界书条目 %q 的正文超过 %d KiB 上限", e.Comment, maxFieldBytes>>10)
		}
		entries = append(entries, port.BookEntry{
			Comment:  e.Comment,
			Content:  e.Content,
			Enabled:  bookEnabled(e),
			Constant: e.Constant,
		})
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
	return ParsedCard{Info: info, Profile: profile}, nil
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
			return fmt.Errorf("投影字段 %s 超过 %d KiB 上限", f.name, maxFieldBytes>>10)
		}
	}
	if n := len([]rune(d.PostHistoryInstructions)); n > maxTailRunes {
		return fmt.Errorf("post_history_instructions 有 %d 字符，超过 %d 字符上限", n, maxTailRunes)
	}
	return nil
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
			return "v3+", fmt.Sprintf("卡片声明规范版本 %s，高于本版读的 3.x；已知字段照常读取", ver)
		}
		return "v2", fmt.Sprintf("卡片声明规范版本 %q 认不出，已按 V2 形状读取", ver)
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
		return nil, fmt.Errorf("PNG 里的角色卡数据不是合法 base64: %w", err)
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
		return "", errors.New("PNG 里没有角色卡数据（缺少 tEXt 块 ccv3 / chara）")
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
