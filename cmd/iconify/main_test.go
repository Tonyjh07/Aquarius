package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// synthetic 源图：透明角落 + 不透明红蓝棋盘（覆盖缩放与 alpha 两条路径）。
func synthetic(n int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if x < n/8 || y < n/8 || x >= n-n/8 || y >= n-n/8 {
				continue // 透明角落
			}
			if (x/2+y/2)%2 == 0 {
				img.SetNRGBA(x, y, color.NRGBA{R: 220, A: 255})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{B: 220, A: 255})
			}
		}
	}
	return img
}

// TestScaleBoxIdentity 等尺寸拷贝保内容。
func TestScaleBoxIdentity(t *testing.T) {
	src := synthetic(32)
	got := scaleBox(src, 32)
	if got.Bounds() != image.Rect(0, 0, 32, 32) {
		t.Fatalf("bounds = %v", got.Bounds())
	}
	if got.NRGBAAt(16, 16) != src.NRGBAAt(16, 16) {
		t.Fatalf("等尺寸应原样拷贝: %v vs %v", got.NRGBAAt(16, 16), src.NRGBAAt(16, 16))
	}
}

// TestScaleBoxDown 面积平均缩小：角落保持透明、中心保持不透明、尺寸正确。
func TestScaleBoxDown(t *testing.T) {
	src := synthetic(64)
	got := scaleBox(src, 16)
	if got.Bounds() != image.Rect(0, 0, 16, 16) {
		t.Fatalf("bounds = %v", got.Bounds())
	}
	if a := got.NRGBAAt(0, 0).A; a != 0 {
		t.Fatalf("透明角落缩放后 alpha = %d, want 0", a)
	}
	mid := got.NRGBAAt(8, 8)
	if mid.A != 255 {
		t.Fatalf("中心 alpha = %d, want 255", mid.A)
	}
	if mid.R == 0 && mid.B == 0 {
		t.Fatalf("中心颜色丢失: %v", mid)
	}
}

// TestBuildICO 结构校验：头、条目尺寸、偏移指向 PNG 签名、总长自洽。
func TestBuildICO(t *testing.T) {
	pngs := map[int][]byte{}
	for _, s := range []int{16, 32} {
		var buf bytes.Buffer
		if err := png.Encode(&buf, scaleBox(image.NewNRGBA(image.Rect(0, 0, 8, 8)), s)); err != nil {
			t.Fatalf("encode: %v", err)
		}
		pngs[s] = buf.Bytes()
	}
	ico := buildICO(pngs)

	if len(ico) < 6+16*2 {
		t.Fatalf("ico 过短: %d", len(ico))
	}
	if binary.LittleEndian.Uint16(ico[0:2]) != 0 || binary.LittleEndian.Uint16(ico[2:4]) != 1 {
		t.Fatalf("ICONDIR 头错误: %v", ico[:6])
	}
	if n := binary.LittleEndian.Uint16(ico[4:6]); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	pos := 6
	for i, want := range []int{16, 32} {
		e := ico[pos : pos+16]
		if int(e[0]) != want || int(e[1]) != want {
			t.Fatalf("entry %d 尺寸 = %dx%d, want %d", i, e[0], e[1], want)
		}
		size := int(binary.LittleEndian.Uint32(e[8:12]))
		offset := int(binary.LittleEndian.Uint32(e[12:16]))
		if offset+size > len(ico) {
			t.Fatalf("entry %d 越界: off=%d size=%d len=%d", i, offset, size, len(ico))
		}
		if !bytes.HasPrefix(ico[offset:offset+8], []byte{0x89, 'P', 'N', 'G'}) {
			t.Fatalf("entry %d 数据不是 PNG: %v", i, ico[offset:offset+8])
		}
		pos += 16
	}
}

// TestBuildICNS 结构校验：magic、总长、条目长度链与 PNG 签名。
func TestBuildICNS(t *testing.T) {
	pngs := map[int][]byte{}
	var buf bytes.Buffer
	if err := png.Encode(&buf, synthetic(64)); err != nil {
		t.Fatalf("encode: %v", err)
	}
	pngs[16], pngs[256] = buf.Bytes(), buf.Bytes()

	icns := buildICNS(pngs)
	if string(icns[0:4]) != "icns" {
		t.Fatalf("magic = %q", icns[:4])
	}
	if total := int(binary.BigEndian.Uint32(icns[4:8])); total != len(icns) {
		t.Fatalf("总长字段 = %d, 实际 %d", total, len(icns))
	}
	off := 8
	seen := 0
	for off < len(icns) {
		typ := string(icns[off : off+4])
		size := int(binary.BigEndian.Uint32(icns[off+4 : off+8]))
		if typ != "icp4" && typ != "ic08" {
			t.Fatalf("意外条目类型 %q", typ)
		}
		if off+size > len(icns) {
			t.Fatalf("条目 %s 越界", typ)
		}
		if !bytes.HasPrefix(icns[off+8:off+16], []byte{0x89, 'P', 'N', 'G'}) {
			t.Fatalf("条目 %s 数据不是 PNG", typ)
		}
		off += size
		seen++
	}
	if seen != 2 {
		t.Fatalf("条目数 = %d, want 2", seen)
	}
}

// TestRun 端到端：写出全部产物且可解码/解析。
func TestRun(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "icon.png")
	var buf bytes.Buffer
	if err := png.Encode(&buf, synthetic(64)); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(src, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "icon")

	if err := run(src, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, s := range sizes {
		p := filepath.Join(out, fmt.Sprintf("icon-%d.png", s))
		f, err := os.Open(p)
		if err != nil {
			t.Fatalf("缺产物 %s: %v", p, err)
		}
		cfg, err := png.DecodeConfig(f)
		_ = f.Close()
		if err != nil || cfg.Width != s || cfg.Height != s {
			t.Fatalf("%s = %+v, %v, want %d×%d", p, cfg, err, s, s)
		}
	}
	for _, name := range []string{"aquarius.ico", "aquarius.icns"} {
		if data, err := os.ReadFile(filepath.Join(out, name)); err != nil || len(data) == 0 {
			t.Fatalf("缺产物 %s: %v", name, err)
		}
	}
	// 非正方形源图拒绝。
	squash := filepath.Join(dir, "wide.png")
	wide := image.NewNRGBA(image.Rect(0, 0, 32, 16))
	if err := png.Encode(mustOpen(t, squash), wide); err != nil {
		t.Fatalf("encode wide: %v", err)
	}
	if err := run(squash, filepath.Join(dir, "x")); err == nil {
		t.Fatal("非正方形源图应报错")
	}
}

// mustOpen 建文件句柄（测试辅助）。
func mustOpen(t *testing.T, p string) *os.File {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
