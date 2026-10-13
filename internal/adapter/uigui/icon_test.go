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

// TestICOImageTrayAsset 内嵌托盘图标可解析出 trayIconPx 档图像（单二进制取图标路径），
// 且解析出的必须是**那一档**的条目（否则平台侧传的就不是预期分辨率的位图）。
func TestICOImageTrayAsset(t *testing.T) {
	if len(assets.TrayICO) == 0 {
		t.Fatal("assets.TrayICO 未内嵌")
	}
	// 档位口径钉在字面量上（不能拿 trayIconPx 自证：那边改了这边跟着变，等于没钉）。
	// 来源：用户 2026-10-13「托盘图标模糊，可以再清晰一倍」——16 档单张位图在 >100% DPI
	// 的任务栏上只能插值放大；32 档有 4 倍像素量，shell 下采样到 16 仍然干净。
	if trayIconPx != 32 {
		t.Fatalf("trayIconPx = %d，口径应为 32（改它 = 改「托盘清晰一倍」的交付档位）", trayIconPx)
	}
	b, err := icoImage(assets.TrayICO, trayIconPx)
	if err != nil {
		t.Fatalf("icoImage(aquarius.ico): %v", err)
	}
	if len(b) == 0 {
		t.Fatal("图像数据为空")
	}
	// 逐条比对载荷，确认选中的条目宽度 = trayIconPx：资产若被重新生成而丢了该档，
	// icoImage 会挑最近一档（静默降级），这里让它现在就响。
	found := icoEntryWidth(t, assets.TrayICO, b)
	if found != trayIconPx {
		t.Fatalf("选中的条目是 %dpx，want %dpx（aquarius.ico 里缺该档 ⇒ 静默降级）", found, trayIconPx)
	}
}

// icoEntryWidth 在 .ico 里找出载荷 == payload 的条目宽度（0 = 未找到）。
func icoEntryWidth(t *testing.T, data, payload []byte) int {
	t.Helper()
	count := int(uint16(data[4]) | uint16(data[5])<<8)
	for i := 0; i < count; i++ {
		off := 6 + i*16
		size := int(le32(data[off+8 : off+12]))
		imgOff := int(le32(data[off+12 : off+16]))
		if size == len(payload) && imgOff+size <= len(data) &&
			string(data[imgOff:imgOff+size]) == string(payload) {
			w := int(data[off])
			if w == 0 {
				w = 256
			}
			return w
		}
	}
	return 0
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
