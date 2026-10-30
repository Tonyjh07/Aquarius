package characterfs

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

// zf 一个待写进测试压缩包的条目（有序，图标「取第一个」依赖顺序）。
type zf struct {
	name string
	data []byte
}

// zipWith 在内存里拼一个 zip。
func zipWith(files ...zf) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		fw, _ := w.Create(f.name)
		_, _ = fw.Write(f.data)
	}
	_ = w.Close()
	return buf.Bytes()
}

// v3Card 构一张带 assets 的 V3 卡 JSON。
func v3Card(name string, assets string) []byte {
	body := `{"spec":"chara_card_v3","spec_version":"3.0","data":{"name":"` + name + `"`
	if assets != "" {
		body += `,"assets":` + assets
	}
	body += "}}"
	return []byte(body)
}

// TestParseCharxBasic 根目录 card.json + `embeded://` 图标：读卡、取图标、整包留档。
func TestParseCharxBasic(t *testing.T) {
	card := v3Card("Aria", `[{"type":"icon","name":"main","uri":"embeded://assets/icon/main.png","ext":"png"}]`)
	icon := []byte("ICON-BYTES")
	archive := zipWith(
		zf{"card.json", card},
		zf{"assets/icon/main.png", icon},
	)
	out, err := parseCard("card.charx", archive)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Info.Source != "charx" || out.Info.Name != "Aria" || out.Info.Spec != "v3" {
		t.Fatalf("info = %+v", out.Info)
	}
	if !bytes.Equal(out.RawJSON, card) {
		t.Fatalf("RawJSON = %q, want the card bytes", out.RawJSON)
	}
	if !bytes.Equal(out.Avatar, icon) {
		t.Fatalf("avatar = %q, want the icon asset", out.Avatar)
	}
	if !bytes.Equal(out.Archive, archive) {
		t.Fatalf("archive must be kept verbatim")
	}
}

// TestParseCharxIconPrefersMain 多个图标时优先 name=main，不管它在数组里的位置。
func TestParseCharxIconPrefersMain(t *testing.T) {
	card := v3Card("Aria", `[
		{"type":"icon","name":"alt","uri":"embeded://assets/icon/alt.png"},
		{"type":"icon","name":"main","uri":"embeded://assets/icon/main.png"}
	]`)
	archive := zipWith(
		zf{"card.json", card},
		zf{"assets/icon/alt.png", []byte("ALT")},
		zf{"assets/icon/main.png", []byte("MAIN")},
	)
	out, err := parseCard("card.charx", archive)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if string(out.Avatar) != "MAIN" {
		t.Fatalf("avatar = %q, want the main icon", out.Avatar)
	}
}

// TestParseCharxIconBasenameMapping SillyTavern 私有路径 `/user/images/<卡名>/x.webp`
// 按 basename 映射到包内同名资产。
func TestParseCharxIconBasenameMapping(t *testing.T) {
	card := v3Card("Aria", `[{"type":"icon","uri":"/user/images/Aria/portrait.webp"}]`)
	archive := zipWith(
		zf{"card.json", card},
		zf{"assets/other/portrait.webp", []byte("WEBP")},
	)
	out, err := parseCard("card.charx", archive)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if string(out.Avatar) != "WEBP" {
		t.Fatalf("avatar = %q, want the basename match", out.Avatar)
	}
	if len(out.Info.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", out.Info.Warnings)
	}
}

// TestParseCharxBasenameMiss 私有路径映射不到同名资产：记警告、无头像，仍导入卡。
func TestParseCharxBasenameMiss(t *testing.T) {
	card := v3Card("Aria", `[{"type":"icon","uri":"/user/images/Aria/nope.webp"}]`)
	out, err := parseCard("card.charx", zipWith(zf{"card.json", card}))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Avatar != nil {
		t.Fatalf("avatar = %q, want nil", out.Avatar)
	}
	if !warnsContain(out.Info.Warnings, "找不到同名文件") {
		t.Fatalf("warnings = %v", out.Info.Warnings)
	}
}

// TestParseCharxHTTPNotDownloaded HTTP(S) 资产不下载、记警告。
func TestParseCharxHTTPNotDownloaded(t *testing.T) {
	card := v3Card("Aria", `[{"type":"icon","uri":"https://example.com/a.png"}]`)
	out, err := parseCard("card.charx", zipWith(zf{"card.json", card}))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Avatar != nil {
		t.Fatalf("avatar = %q, want nil", out.Avatar)
	}
	if !warnsContain(out.Info.Warnings, "网络") {
		t.Fatalf("warnings = %v", out.Info.Warnings)
	}
}

// TestParseCharxCCDefault `ccdefault:` 表示用默认图标：无头像、不报警告。
func TestParseCharxCCDefault(t *testing.T) {
	card := v3Card("Aria", `[{"type":"icon","uri":"ccdefault:"}]`)
	out, err := parseCard("card.charx", zipWith(zf{"card.json", card}))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Avatar != nil || len(out.Info.Warnings) != 0 {
		t.Fatalf("avatar/warnings = %q/%v, want nil/none", out.Avatar, out.Info.Warnings)
	}
}

// TestParseCharxNoCardJSON 根目录没有 card.json：报「发行包」而不是「文件损坏」。
func TestParseCharxNoCardJSON(t *testing.T) {
	archive := zipWith(zf{"inner.charx", []byte("PK")})
	_, err := parseCard("dist.zip", archive)
	if err == nil || !strings.Contains(err.Error(), "发行包") {
		t.Fatalf("err = %v, want a distribution-package note", err)
	}
}

// TestParseCharxRejectsSlip zip-slip 条目整包拒绝（沿用技能安装门禁）。
func TestParseCharxRejectsSlip(t *testing.T) {
	archive := zipWith(zf{"card.json", v3Card("Aria", "")}, zf{"../escape.txt", []byte("x")})
	_, err := parseCard("card.charx", archive)
	if err == nil || !strings.Contains(err.Error(), "拒绝") {
		t.Fatalf("err = %v, want a slip rejection", err)
	}
}

// TestParseCharxRejectsSymlink 符号链接条目拒绝。
func TestParseCharxRejectsSymlink(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "link"}
	hdr.SetMode(fs.ModeSymlink | 0o777)
	if _, err := w.CreateHeader(hdr); err != nil {
		t.Fatalf("create symlink header: %v", err)
	}
	fw, err := w.Create("card.json")
	if err != nil {
		t.Fatalf("create card.json: %v", err)
	}
	_, _ = fw.Write(v3Card("Aria", ""))
	_ = w.Close()
	if _, err := parseCard("card.charx", buf.Bytes()); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("err = %v, want a symlink rejection", err)
	}
}

// TestParseCharxEntryLimit 条目数超过上限即拒绝。
func TestParseCharxEntryLimit(t *testing.T) {
	files := make([]zf, 0, maxZipEntries+1)
	files = append(files, zf{"card.json", v3Card("Aria", "")})
	for i := 0; i < maxZipEntries; i++ { // 加 card.json 共 maxZipEntries+1 条
		files = append(files, zf{name: fmt.Sprintf("f%05d.txt", i)})
	}
	_, err := parseCard("card.charx", zipWith(files...))
	if err == nil || !strings.Contains(err.Error(), "条目数") {
		t.Fatalf("err = %v, want an entry-count rejection", err)
	}
}
