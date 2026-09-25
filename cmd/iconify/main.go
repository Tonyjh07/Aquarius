// Command iconify 从源图标生成项目图标集（开发工具，不参与 aquarius 二进制）：
//
//   - assets/icon/icon-{16,32,48,64,128,256}.png —— 透明底多尺寸（面积平均缩放）
//   - assets/icon/aquarius.ico —— Windows：多尺寸 PNG 压缩条目（Vista+）
//   - assets/icon/aquarius.icns —— macOS：多尺寸 PNG 条目
//
// 用法（仓库根）：go run ./cmd/iconify [-in assets/icon.png] [-out assets/icon]
// 换源图后重跑即可再生成；ICO/ICNS 容器为手写纯 Go 编码，无三方依赖。
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// sizes 生成的 PNG 尺寸（ICO 同步使用；512 直接取自源图，供 ICNS 用）。
var sizes = []int{16, 32, 48, 64, 128, 256}

// icnsTypes ICNS 条目类型 → 尺寸（Apple 惯例：icp4/5/6 = 16/32/48，
// ic12 = 32@2x=64，ic07/08/09 = 128/256/512）。
var icnsTypes = []struct {
	size int
	typ  string
}{
	{16, "icp4"},
	{32, "icp5"},
	{48, "icp6"},
	{64, "ic12"},
	{128, "ic07"},
	{256, "ic08"},
	{512, "ic09"},
}

func main() {
	in := flag.String("in", "assets/icon.png", "源图标路径（建议 512×512 透明底 PNG）")
	out := flag.String("out", "assets/icon", "输出目录")
	flag.Parse()
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "iconify:", err)
		os.Exit(1)
	}
}

// run 读源图 → 生成全部产物并写盘。
func run(in, out string) error {
	src, err := loadPNG(in)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("创建输出目录: %w", err)
	}
	pngs, err := renderPNGs(src)
	if err != nil {
		return err
	}
	for _, s := range sizes {
		if err := writeFile(filepath.Join(out, fmt.Sprintf("icon-%d.png", s)), pngs[s]); err != nil {
			return err
		}
	}
	if err := writeFile(filepath.Join(out, "aquarius.ico"), buildICO(pngs)); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(out, "aquarius.icns"), buildICNS(pngs)); err != nil {
		return err
	}
	fmt.Printf("已生成 %s：PNG %v + aquarius.ico + aquarius.icns\n", out, sizes)
	return nil
}

// loadPNG 读取并解码源 PNG（必须是正方形）。
func loadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开源图: %w", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("解码源图: %w", err)
	}
	b := img.Bounds()
	if b.Dx() != b.Dy() {
		return nil, fmt.Errorf("源图须为正方形，当前 %d×%d", b.Dx(), b.Dy())
	}
	return img, nil
}

// renderPNGs 渲染全部尺寸（含 512 源尺寸）并编码为 PNG 字节。
func renderPNGs(src image.Image) (map[int][]byte, error) {
	want := append(append([]int{}, sizes...), 512)
	out := make(map[int][]byte, len(want))
	for _, s := range want {
		data, err := encodePNG(scaleBox(src, s))
		if err != nil {
			return nil, err
		}
		out[s] = data
	}
	return out, nil
}

// encodePNG 编码 PNG。
func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("编码 PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// writeFile 原子写出（唯一临时名 + rename，与仓库写盘口径一致）。
func writeFile(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("换入 %s: %w", path, err)
	}
	return nil
}

// scaleBox 面积平均缩放（预乘 alpha 求均值再还原，避免边缘暗边/彩边）。
// 目标尺寸与源一致时原样拷贝；本项目尺寸均 ≤ 源尺寸（纯缩小）。
func scaleBox(src image.Image, size int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	if b.Dx() == size && b.Dy() == size {
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
				dst.SetNRGBA(x, y, color.NRGBA{
					R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8), A: uint8(a >> 8),
				})
			}
		}
		return dst
	}
	sw, sh := float64(b.Dx()), float64(b.Dy())
	sz := float64(size)
	for dy := 0; dy < size; dy++ {
		y0, y1 := sh*float64(dy)/sz, sh*float64(dy+1)/sz
		for dx := 0; dx < size; dx++ {
			x0, x1 := sw*float64(dx)/sz, sw*float64(dx+1)/sz
			var sumW, sumR, sumG, sumB, sumA float64
			for iy := int(math.Floor(y0)); iy < int(math.Ceil(y1)); iy++ {
				wy := math.Min(y1, float64(iy+1)) - math.Max(y0, float64(iy))
				if wy <= 0 {
					continue
				}
				for ix := int(math.Floor(x0)); ix < int(math.Ceil(x1)); ix++ {
					wx := math.Min(x1, float64(ix+1)) - math.Max(x0, float64(ix))
					if wx <= 0 {
						continue
					}
					w := wx * wy
					r, g, bl, a := src.At(b.Min.X+ix, b.Min.Y+iy).RGBA()
					af := float64(a) / 65535
					sumR += float64(r) / 65535 * af * w // 预乘
					sumG += float64(g) / 65535 * af * w
					sumB += float64(bl) / 65535 * af * w
					sumA += af * w
					sumW += w
				}
			}
			if sumW == 0 || sumA == 0 {
				continue // 全透明，保持零值
			}
			dst.SetNRGBA(dx, dy, color.NRGBA{
				R: to255(sumR / sumA),
				G: to255(sumG / sumA),
				B: to255(sumB / sumA),
				A: to255(sumA / sumW),
			})
		}
	}
	return dst
}

// to255 归一化值 → 0..255 字节。
func to255(v float64) uint8 {
	v = math.Round(v * 255)
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// buildICO 打包 Windows ICO（ICONDIR + 条目 + PNG 压缩数据，Vista+ 支持）。
func buildICO(pngs map[int][]byte) []byte {
	keys := make([]int, 0, len(sizes))
	for _, s := range sizes {
		if _, ok := pngs[s]; ok {
			keys = append(keys, s)
		}
	}
	sort.Ints(keys)

	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // type: icon
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(keys)))

	offset := 6 + 16*len(keys)
	for _, s := range keys {
		data := pngs[s]
		dim := byte(s)
		if s >= 256 {
			dim = 0 // 0 表示 256
		}
		buf.WriteByte(dim)                                      // width
		buf.WriteByte(dim)                                      // height
		buf.WriteByte(0)                                        // colors (palette)
		buf.WriteByte(0)                                        // reserved
		_ = binary.Write(&buf, binary.LittleEndian, uint16(1))  // planes
		_ = binary.Write(&buf, binary.LittleEndian, uint16(32)) // bitcount
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(data)))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(data)
	}
	for _, s := range keys {
		buf.Write(pngs[s])
	}
	return buf.Bytes()
}

// buildICNS 打包 macOS ICNS（magic + 总长 + 各条目 [类型|长度|PNG]）。
func buildICNS(pngs map[int][]byte) []byte {
	type entry struct {
		typ  string
		data []byte
	}
	var entries []entry
	for _, t := range icnsTypes {
		if data, ok := pngs[t.size]; ok {
			entries = append(entries, entry{typ: t.typ, data: data})
		}
	}

	var body bytes.Buffer
	for _, e := range entries {
		body.WriteString(e.typ)
		_ = binary.Write(&body, binary.BigEndian, uint32(8+len(e.data)))
		body.Write(e.data)
	}

	var buf bytes.Buffer
	buf.WriteString("icns")
	_ = binary.Write(&buf, binary.BigEndian, uint32(8+body.Len()))
	buf.Write(body.Bytes())
	return buf.Bytes()
}
