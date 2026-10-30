package characterfs

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// b64 JSON 的 base64（PNG tEXt 载荷）。
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// pngWithText 拼一个带 tEXt 块的 PNG（CRC 置零——读取端不校验）。
func pngWithText(chunks ...[2]string) []byte {
	var b bytes.Buffer
	b.Write(pngMagic)
	for _, c := range chunks {
		data := append([]byte(c[0]), 0)
		data = append(data, c[1]...)
		var lb [4]byte
		binary.BigEndian.PutUint32(lb[:], uint32(len(data)))
		b.Write(lb[:])
		b.WriteString("tEXt")
		b.Write(data)
		b.Write([]byte{0, 0, 0, 0})
	}
	return b.Bytes()
}

// TestParseJSONV2 纯 JSON V2 卡的字段归一：投影字段分列读入、元数据只留展示、
// 世界书条目带出 enabled / constant（缺 enabled 默认 true）。
func TestParseJSONV2(t *testing.T) {
	raw := []byte(`{"spec":"chara_card_v2","spec_version":"2.0","data":{
		"name":"Aria","nickname":"Ari","description":"desc","personality":"calm","scenario":"room",
		"first_mes":"hi {{user}}","mes_example":"ex","system_prompt":"sys","post_history_instructions":"tail",
		"alternate_greetings":["g1","g2"],"creator":"me","character_version":"1.2","tags":["a","b"],
		"character_book":{"entries":[
			{"comment":"Lore","content":"body","constant":true,"enabled":true},
			{"comment":"Off","content":"x","constant":true,"enabled":false},
			{"comment":"Def","content":"y","constant":false}
		]}}}`)
	out, err := parseCard("card.json", raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Info.Source != "json" || out.Info.Spec != "v2" {
		t.Fatalf("source/spec = %q/%q, want json/v2", out.Info.Source, out.Info.Spec)
	}
	if out.Info.Name != "Aria" || out.Info.Display != "Ari" {
		t.Fatalf("name/display = %q/%q", out.Info.Name, out.Info.Display)
	}
	if out.Info.Creator != "me" || out.Info.Version != "1.2" || len(out.Info.Tags) != 2 {
		t.Fatalf("metadata = %+v", out.Info)
	}
	if out.Info.Greetings != 2 || out.Info.BookEntries != 3 {
		t.Fatalf("counts = greetings %d, book %d", out.Info.Greetings, out.Info.BookEntries)
	}
	if out.Profile.Description != "desc" || out.Profile.Personality != "calm" ||
		out.Profile.Scenario != "room" || out.Profile.SystemPrompt != "sys" ||
		out.Profile.Examples != "ex" || out.Profile.FirstMes != "hi {{user}}" ||
		out.Profile.PostHistory != "tail" {
		t.Fatalf("profile = %+v", out.Profile)
	}
	if !bytes.Equal(out.RawJSON, raw) {
		t.Fatalf("RawJSON must be the card bytes verbatim")
	}
	want := []port.BookEntry{
		{Comment: "Lore", Content: "body", Constant: true, Enabled: true},
		{Comment: "Off", Content: "x", Constant: true, Enabled: false},
		{Comment: "Def", Content: "y", Constant: false, Enabled: true},
	}
	if len(out.Profile.BookEntries) != len(want) {
		t.Fatalf("book len = %d, want %d", len(out.Profile.BookEntries), len(want))
	}
	for i := range want {
		if out.Profile.BookEntries[i] != want[i] {
			t.Fatalf("book[%d] = %+v, want %+v", i, out.Profile.BookEntries[i], want[i])
		}
	}
}

// TestParseCardHasDataOnlyReadsData 有 data 对象时顶层镜像字段一律不看（实测两边不一致）。
func TestParseCardHasDataOnlyReadsData(t *testing.T) {
	raw := []byte(`{"spec_version":"2.0","name":"TOP","description":"TOP-DESC","data":{"name":"Inner","description":"INNER"}}`)
	out, err := parseCard("card.json", raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Info.Name != "Inner" || out.Profile.Description != "INNER" {
		t.Fatalf("must read data only, got name=%q desc=%q", out.Info.Name, out.Profile.Description)
	}
}

// TestParseCardV3Nickname V3 的 nickname 优先作 {{char}} 取值。
func TestParseCardV3Nickname(t *testing.T) {
	out, err := parseCard("card.json", []byte(`{"spec":"chara_card_v3","spec_version":"3.0","data":{"name":"N","nickname":"Nick"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Info.Spec != "v3" || out.Info.Display != "Nick" {
		t.Fatalf("spec/display = %q/%q, want v3/Nick", out.Info.Spec, out.Info.Display)
	}
}

// TestParseCardHigherVersionImported 高于 3.x 的版本仍导入并记 v3+ 警告（只读导入，拒收更糟）。
func TestParseCardHigherVersionImported(t *testing.T) {
	out, err := parseCard("card.json", []byte(`{"spec_version":"4.0","data":{"name":"N"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Info.Spec != "v3+" {
		t.Fatalf("spec = %q, want v3+", out.Info.Spec)
	}
	if !warnsContain(out.Info.Warnings, "4.0") {
		t.Fatalf("warnings = %v, want a note about 4.0", out.Info.Warnings)
	}
}

// TestParseCardPureV1Rejected 顶层六字段齐、没有 data 的纯 V1 卡报「本版不读」。
func TestParseCardPureV1Rejected(t *testing.T) {
	raw := []byte(`{"name":"N","description":"d","personality":"p","scenario":"s","first_mes":"f","mes_example":"e"}`)
	_, err := parseCard("card.json", raw)
	if err == nil || !strings.Contains(err.Error(), "V1") {
		t.Fatalf("pure V1 err = %v, want a V1 rejection", err)
	}
}

// TestParseCardNoDataBestEffort 没有 data 又不是纯 V1：按顶层字段尽力读并记警告。
func TestParseCardNoDataBestEffort(t *testing.T) {
	raw := []byte(`{"spec":"chara_card_v2","spec_version":"2.0","name":"N","description":"d"}`)
	out, err := parseCard("card.json", raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Info.Name != "N" || out.Profile.Description != "d" {
		t.Fatalf("best-effort read failed: %+v", out.Info)
	}
	if !warnsContain(out.Info.Warnings, "data") {
		t.Fatalf("warnings = %v, want a note about the missing data object", out.Info.Warnings)
	}
}

// TestParseCardNameRequired name 去掉空白后为空即拒绝。
func TestParseCardNameRequired(t *testing.T) {
	_, err := parseCard("card.json", []byte(`{"spec_version":"2.0","data":{"description":"d"}}`))
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("err = %v, want a name-required error", err)
	}
}

// TestParseCardBOMStripped 带 BOM 的 JSON 也能读（json.Unmarshal 本身拒 BOM）。
func TestParseCardBOMStripped(t *testing.T) {
	raw := append(append([]byte{}, bom...), []byte(`{"spec_version":"2.0","data":{"name":"N"}}`)...)
	out, err := parseCard("card.json", raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Info.Name != "N" {
		t.Fatalf("name = %q", out.Info.Name)
	}
}

// TestParseCardBookEnabled 条目启用位：extensions.enabled 优先于 enabled，二者都缺 = true。
func TestParseCardBookEnabled(t *testing.T) {
	raw := []byte(`{"spec_version":"2.0","data":{"name":"N","character_book":{"entries":[
		{"comment":"a","content":"c","enabled":false,"extensions":{"enabled":true}},
		{"comment":"b","content":"c"},
		{"comment":"c","content":"c","enabled":false}
	]}}}`)
	out, err := parseCard("card.json", raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []bool{true, true, false}
	for i, e := range out.Profile.BookEntries {
		if e.Enabled != want[i] {
			t.Fatalf("entry %d enabled = %v, want %v", i, e.Enabled, want[i])
		}
	}
}

// TestParsePNGCharaOnly 只带 chara 块的 PNG 卡能读，头像来源 = 原图字节。
func TestParsePNGCharaOnly(t *testing.T) {
	js := `{"spec_version":"2.0","data":{"name":"FromChara"}}`
	data := pngWithText([2]string{"chara", b64(js)})
	out, err := parseCard("card.png", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Info.Name != "FromChara" || out.Info.Source != "png" {
		t.Fatalf("name/source = %q/%q", out.Info.Name, out.Info.Source)
	}
	if !bytes.Equal(out.RawJSON, []byte(js)) {
		t.Fatalf("RawJSON = %q, want decoded JSON", out.RawJSON)
	}
	if !bytes.Equal(out.Avatar, data) {
		t.Fatalf("avatar source must be the original PNG bytes")
	}
}

// TestParsePNGCCV3Wins ccv3 优先于 chara，关键字大小写不敏感。
func TestParsePNGCCV3Wins(t *testing.T) {
	chara := `{"spec_version":"2.0","data":{"name":"FromChara"}}`
	ccv3 := `{"spec_version":"3.0","data":{"name":"FromCCV3"}}`
	data := pngWithText([2]string{"chara", b64(chara)}, [2]string{"CCV3", b64(ccv3)})
	out, err := parseCard("card.png", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Info.Name != "FromCCV3" {
		t.Fatalf("name = %q, want ccv3 to win", out.Info.Name)
	}
}

// TestParsePNGNoCard PNG 里没有 tEXt 卡数据即报错。
func TestParsePNGNoCard(t *testing.T) {
	_, err := parseCard("card.png", append([]byte{}, pngMagic...))
	if err == nil || !strings.Contains(err.Error(), "tEXt") {
		t.Fatalf("err = %v, want a missing-tEXt error", err)
	}
}

// TestParseCardLimits 上限（§5.17）：投影字段 256 KiB、Tail 8000 字符、单卡文件 64 MiB，
// 超限一律报错、不截断。
func TestParseCardLimits(t *testing.T) {
	big := strings.Repeat("x", maxFieldBytes+1)
	if _, err := parseCard("card.json", cardJSONWith(map[string]any{"name": "N", "description": big})); err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("oversized field err = %v", err)
	}

	tail := strings.Repeat("y", maxTailRunes+1)
	if _, err := parseCard("card.json", cardJSONWith(map[string]any{"name": "N", "post_history_instructions": tail})); err == nil || !strings.Contains(err.Error(), "post_history") {
		t.Fatalf("oversized tail err = %v", err)
	}

	if _, err := parseCard("card.json", make([]byte, maxCardFileBytes+1)); err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("oversized file err = %v", err)
	}
}

// TestParseCardUnrecognized 认不出的容器明确报因，不瞎猜。
func TestParseCardUnrecognized(t *testing.T) {
	if _, err := parseCard("card.bin", []byte("hello")); err == nil || !strings.Contains(err.Error(), "认不出") {
		t.Fatalf("err = %v, want an unrecognized-container error", err)
	}
}

// cardJSONWith 拼一张最小 V2 卡 JSON，data 用给定字段。
func cardJSONWith(data map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"spec_version": "2.0", "data": data})
	return b
}

// warnsContain 警告列表里是否有含子串的一条。
func warnsContain(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
