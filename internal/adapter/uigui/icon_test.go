package uigui

import (
	"testing"

	"github.com/Tonyjh07/Aquarius/assets"
)

// mkICO 合成 .ico 字节（ICONDIR + 目录 + 载荷；载荷格式解析器不校验）。
func mkICO(entries []struct {
	w       int
	payload string
}) []byte {
	var b []byte
	b = append(b, 0, 0, 1, 0) // reserved、type=1（图标）
	b = append(b, byte(len(entries)), 0)
	off := 6 + 16*len(entries)
	for _, e := range entries {
		w := e.w
		if w == 256 {
			w = 0 // 0 = 256px 扩展约定
		}
		b = append(b, byte(w), byte(w), 0, 0) // width、height、colors、reserved
		b = append(b, 1, 0, 0, 0)             // planes、bpp（不校验）
		sz := len(e.payload)
		b = append(b, byte(sz), byte(sz>>8), byte(sz>>16), byte(sz>>24))
		b = append(b, byte(off), byte(off>>8), byte(off>>16), byte(off>>24))
		off += sz
	}
	for _, e := range entries {
		b = append(b, e.payload...)
	}
	return b
}

// TestICOImagePicksNearest 条目选择：取尺寸最接近 wantPx 的图像。
func TestICOImagePicksNearest(t *testing.T) {
	data := mkICO([]struct {
		w       int
		payload string
	}{{16, "img16"}, {48, "img48"}})
	for _, c := range []struct {
		want int
		exp  string
	}{{10, "img16"}, {20, "img16"}, {40, "img48"}, {48, "img48"}} {
		b, err := icoImage(data, c.want)
		if err != nil {
			t.Fatalf("icoImage(%d): %v", c.want, err)
		}
		if string(b) != c.exp {
			t.Errorf("icoImage(%d) = %q, want %q", c.want, b, c.exp)
		}
	}
}

// TestICOImageTrayAsset 内嵌托盘图标可解析出 16px 档图像（单二进制取图标路径）。
func TestICOImageTrayAsset(t *testing.T) {
	if len(assets.TrayICO) == 0 {
		t.Fatal("assets.TrayICO 未内嵌")
	}
	b, err := icoImage(assets.TrayICO, 16)
	if err != nil {
		t.Fatalf("icoImage(aquarius.ico): %v", err)
	}
	if len(b) == 0 {
		t.Fatal("图像数据为空")
	}
}

// TestICOImageRejectsGarbage 非法输入报错（不 panic，回落由调用方处理）。
func TestICOImageRejectsGarbage(t *testing.T) {
	cases := map[string][]byte{
		"空":     nil,
		"短":     {0, 0, 1},
		"非图标类型": {0, 0, 9, 0, 1, 0, 6, 0},
		"零条目":   {0, 0, 1, 0, 0, 0},
		"条目越界":  append([]byte{0, 0, 1, 0, 1, 0}, make([]byte, 8)...),
		"载荷越界":  append([]byte{0, 0, 1, 0, 1, 0}, append(make([]byte, 16), 0xFF, 0xFF, 0xFF, 0x7F, 0, 0, 0, 0)...),
	}
	for name, data := range cases {
		if _, err := icoImage(data, 16); err == nil {
			t.Errorf("%s: 应报错", name)
		}
	}
}
