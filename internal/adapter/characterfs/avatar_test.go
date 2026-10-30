package characterfs

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// injectText 在 PNG 的 IHDR 之后插一个 tEXt 块（模拟 PNG 卡把卡 JSON 塞在 tEXt 里）。
func injectText(t *testing.T, src []byte, kw, text string) []byte {
	t.Helper()
	data := append([]byte(kw), 0)
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

	// 8 字节签名 + IHDR（长度 13 + 类型 4 + 数据 13 + CRC 4 = 25）。
	const insertAt = 8 + 25
	if len(src) < insertAt {
		t.Fatalf("PNG too short to inject into")
	}
	out := append([]byte{}, src[:insertAt]...)
	out = append(out, chunk.Bytes()...)
	return append(out, src[insertAt:]...)
}

// TestEncodeAvatarStripsTextChunks 重编码后只剩像素：卡 JSON 所在的 tEXt 块消失，
// 尺寸与像素不变，体积大幅下降。
func TestEncodeAvatarStripsTextChunks(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for x := 0; x < 8; x++ {
		for y := 0; y < 6; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 40), B: 128, A: 255})
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, img); err != nil {
		t.Fatalf("encode source: %v", err)
	}
	withText := injectText(t, raw.Bytes(), "chara", strings.Repeat("x", 50_000))

	out, err := encodeAvatar(withText)
	if err != nil {
		t.Fatalf("encodeAvatar: %v", err)
	}
	if bytes.Contains(out, []byte("tEXt")) {
		t.Fatalf("tEXt survived the re-encode")
	}
	if len(out) >= len(withText)/2 {
		t.Fatalf("re-encode did not shrink: %d → %d", len(withText), len(out))
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode re-encoded: %v", err)
	}
	if cfg.Width != 8 || cfg.Height != 6 {
		t.Fatalf("size = %dx%d, want 8x6", cfg.Width, cfg.Height)
	}
	back, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode re-encoded image: %v", err)
	}
	if got := back.At(3, 2); got != img.At(3, 2) {
		t.Fatalf("pixel drifted: got %v, want %v", got, img.At(3, 2))
	}
}

// TestEncodeAvatarEmptyAndGarbage 空来源 = 无头像（返回 nil）；不是图片即报错。
func TestEncodeAvatarEmptyAndGarbage(t *testing.T) {
	if out, err := encodeAvatar(nil); err != nil || out != nil {
		t.Fatalf("empty src = %q, %v; want nil, nil", out, err)
	}
	if _, err := encodeAvatar([]byte("not an image")); err == nil {
		t.Fatalf("garbage src should error")
	}
}
