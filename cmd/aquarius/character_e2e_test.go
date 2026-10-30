package main

// character_e2e_test.go 角色卡只读导入的端到端验收（§5.17/D243/D247）：三类容器
// （V2 JSON / V3 PNG / V3 .charx）各导一张真卡 → 开新会话 → 首轮请求逐段核对
// （人格段在前、示例节点在 persona 之后、尾部指令在历史之后）→ 压缩一次后确认示例
// 被吸收而人格与常驻世界书仍在 → 删卡后会话照常可装配。
//
// 这里走真实二进制：真实 config → 真实 characterfs（临时目录）→ 真实 storejson，
// 只把 LLM 换成按序回话的脚本服务。卡文件在测试里按三种容器的真实字节形态构造。

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cardBody 一张卡的投影字段（三种容器共用同一套标记：断言请求体里各段的来路与顺序）。
type cardBody struct {
	Name    string
	Desc    string
	Pers    string
	Scene   string
	System  string
	Example string
	Greet   string
	Tail    string
	Book    string
	BookOff string
}

func markerBody(name string) cardBody {
	return cardBody{
		Name:    name,
		Desc:    name + "Desc",
		Pers:    name + "Pers",
		Scene:   name + "Scene",
		System:  name + "System",
		Example: name + "Example",
		Greet:   name + "Greeting",
		Tail:    name + "Tail",
		Book:    name + "Book",
		BookOff: name + "BookOff",
	}
}

// cardJSON 拼一张卡 JSON（spec 决定 V2/V3）：常驻世界书含一条启用、一条禁用。
func (b cardBody) cardJSON(spec, specVersion string) []byte {
	body := map[string]any{
		"name":                      b.Name,
		"description":               b.Desc,
		"personality":               b.Pers,
		"scenario":                  b.Scene,
		"system_prompt":             b.System,
		"mes_example":               b.Example + "\n<START>\nmore",
		"first_mes":                 b.Greet,
		"post_history_instructions": b.Tail,
		"alternate_greetings":       []string{b.Name + "Alt1", b.Name + "Alt2"},
		"character_book": map[string]any{"entries": []any{
			map[string]any{"comment": "Book", "content": b.Book, "constant": true, "enabled": true},
			map[string]any{"comment": "Off", "content": b.BookOff, "constant": true, "enabled": false},
		}},
	}
	raw, _ := json.Marshal(map[string]any{"spec": spec, "spec_version": specVersion, "data": body})
	return raw
}

// smallPNG 一张可解码的最小 PNG（8×8 渐变）。
func smallPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 30), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// injectPNGText 在 PNG 的 IHDR 之后插一个 tEXt 块（key + 0x00 + 文本，CRC 正确）。
func injectPNGText(t *testing.T, src []byte, key, text string) []byte {
	t.Helper()
	data := append([]byte(key), 0)
	data = append(data, text...)
	var chunk bytes.Buffer
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(data)))
	chunk.Write(lb[:])
	chunk.WriteString("tEXt")
	chunk.Write(data)
	var cb [4]byte
	binary.BigEndian.PutUint32(cb[:], crc32.ChecksumIEEE(append([]byte("tEXt"), data...)))
	chunk.Write(cb[:])

	const insertAt = 8 + 25 // 签名 + IHDR
	if len(src) < insertAt {
		t.Fatalf("PNG 过短，无法注入 tEXt")
	}
	out := append([]byte{}, src[:insertAt]...)
	out = append(out, chunk.Bytes()...)
	return append(out, src[insertAt:]...)
}

// writeCardFile 把卡字节写进临时文件，返回路径。
func writeCardFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("写卡文件 %s: %v", name, err)
	}
	return p
}

// zipCard 内存拼一个 .charx：根 card.json + `embeded://` 图标资产。
func zipCard(t *testing.T, cardJSON, icon []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	put := func(name string, data []byte) {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	put("card.json", cardJSON)
	put("assets/icon/main.png", icon)
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// TestCharacterCardE2E 三类容器导入 → 角色会话装配 → 压缩 → 删卡，逐段核对请求体。
func TestCharacterCardE2E(t *testing.T) {
	srv, reqs := scriptServer(t, []string{
		"回复·爱丽丝首轮",           // 0：/character start Alice 后 "HELLO-USER-MARKER"
		"## Objective\n- 继续", // 1：/compact 摘要（须含模板小节）
		"回复·压缩后",             // 2：压缩后 "bye"
		"回复·删卡后",             // 3：删卡后 "next"
		"回复·Bob",             // 4：PNG 卡会话
		"回复·Carol",           // 5：charx 卡会话
	})
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{
  "model": {"providers":[{"name":"p","base_url":%q,"keys":[{"api_key":"secret:AQ_E2E_KEY"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui": {"kind":"repl"},
  "prompts": {"persona":["@default","@env"]},
  "characters": {"prefer_system_prompt":false,"apply_post_history":true,"inject_constant_book":true,"inject_examples":true},
  "limits": {"max_turns":8,"max_context_tokens":64000,"compact_threshold":0.7,"tool_output_chars":20000,"tool_timeout_sec":60}
}`, srv.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("AQ_E2E_KEY", "test-key")

	// 三张真卡：V2 JSON / V3 PNG（tEXt ccv3，base64）/ V3 .charx。
	alice := markerBody("Alice")
	bob := markerBody("Bob")
	carol := markerBody("Carol")
	jsonPath := writeCardFile(t, dir, "alice.json", alice.cardJSON("chara_card_v2", "2.0"))
	pngPath := writeCardFile(t, dir, "bob.png",
		injectPNGText(t, smallPNG(t), "ccv3", base64.StdEncoding.EncodeToString(bob.cardJSON("chara_card_v3", "3.0"))))
	charxPath := writeCardFile(t, dir, "carol.charx",
		zipCard(t, carol.cardJSON("chara_card_v3", "3.0"), smallPNG(t)))

	stdin := strings.Join([]string{
		"/character import " + jsonPath,
		"/character import " + pngPath,
		"/character import " + charxPath,
		"/character list",
		"/character start Alice",
		"HELLO-USER-MARKER",
		"/compact",
		"bye",
		"/character remove Alice",
		"next",
		"/character start Bob",
		"msg-bob",
		"/character start Carol",
		"msg-carol",
		"/quit",
	}, "\n") + "\n"

	var out bytes.Buffer
	if code := run([]string{"-data", dir, "-yes"}, strings.NewReader(stdin), &out, io.Discard); code != 0 {
		t.Fatalf("run code = %d\nstdout:\n%s", code, out.String())
	}
	// 三张卡都导入成功（清单里能看到显示名）。
	for _, name := range []string{"Alice", "Bob", "Carol"} {
		if !strings.Contains(out.String(), name) {
			t.Fatalf("清单未含 %s:\n%s", name, out.String())
		}
	}
	if reqs.len() != 6 {
		t.Fatalf("LLM 请求数 = %d, want 6（三容器 + 压缩 + 删卡）", reqs.len())
	}

	// 首轮（V2 JSON 卡）：人格段在前，示例在 persona 之后，开场白与其后的用户消息，
	// 尾部指令在历史之后。
	first := string(reqs.at(0).body)
	assertOrder(t, first, "AliceBook", "You are Alice.")
	assertOrder(t, first, "You are Alice.", "AliceDesc")
	assertOrder(t, first, "AliceDesc", "AlicePers")
	assertOrder(t, first, "AlicePers", "AliceScene")
	assertOrder(t, first, "AliceScene", "AliceSystem")
	assertOrder(t, first, "AliceSystem", "AliceExample")   // 示例在 persona 之后
	assertOrder(t, first, "AliceExample", "AliceGreeting") // 开场白在示例之后
	assertOrder(t, first, "AliceGreeting", "HELLO-USER-MARKER")
	assertOrder(t, first, "HELLO-USER-MARKER", "AliceTail") // 尾部指令在历史之后
	if strings.Contains(first, "AliceBookOff") {
		t.Fatalf("禁用的常驻条目不该进上下文:\n%s", first)
	}

	// 压缩后（请求 2）：示例（注入节点）被吸收，人格与常驻世界书仍在，尾部指令照发。
	after := string(reqs.at(2).body)
	if strings.Contains(after, "AliceExample") {
		t.Fatalf("压缩后示例应被吸收:\n%s", after)
	}
	for _, want := range []string{"You are Alice.", "AliceBook", "AliceTail"} {
		if !strings.Contains(after, want) {
			t.Fatalf("压缩后缺 %q（人格/常驻世界书/尾部指令均应仍在）:\n%s", want, after)
		}
	}

	// 删卡后（请求 3）：卡目录已删，会话照常可装配（文本是快照）。
	removed := string(reqs.at(3).body)
	for _, want := range []string{"You are Alice.", "AliceBook", "AliceTail"} {
		if !strings.Contains(removed, want) {
			t.Fatalf("删卡后会话缺 %q（快照应照常装配）:\n%s", want, removed)
		}
	}

	// PNG（V3）与 .charx（V3）各开一会话：容器解析出的卡内容确实进了上下文。
	for i, b := range []cardBody{bob, carol} {
		body := string(reqs.at(4 + i).body)
		for _, want := range []string{b.Desc, b.Book, b.Tail} {
			if !strings.Contains(body, want) {
				t.Fatalf("容器 %d（%s）会话缺 %q:\n%s", i, b.Name, want, body)
			}
		}
	}
}

// assertOrder 断言 a 在 body 里出现在 b 之前（都出现）。
func assertOrder(t *testing.T, body, a, b string) {
	t.Helper()
	ia, ib := strings.Index(body, a), strings.Index(body, b)
	if ia < 0 {
		t.Fatalf("请求体缺 %q", a)
	}
	if ib < 0 {
		t.Fatalf("请求体缺 %q", b)
	}
	if ia >= ib {
		t.Fatalf("%q 应在 %q 之前（%d vs %d）", a, b, ia, ib)
	}
}
