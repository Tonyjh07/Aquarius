package platform

// 剪贴板位图解码（D187 口径二）：Windows 剪贴板里的位图并不统一——浏览器与截图
// 工具放注册格式 "PNG"，老一些的工具只放 CF_DIB（BITMAPINFOHEADER + 像素）或
// CF_DIBV5（BITMAPV5HEADER，带 alpha 掩码）。这里把三种形态统一成 **PNG 字节**，
// 上层只认 PNG 一种输入。
//
// 全程纯 Go（无 cgo、不碰 GDI），因此可在任意平台单测：本文件不带 build tag，
// Win32 调用（打开剪贴板、取句柄）留在 clipboard_windows.go。

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
)

// 剪贴板 DIB 的压缩方式（Win32 常量）。
const (
	dibRGB            = 0 // BI_RGB：24/32bpp 无掩码
	dibBitfields      = 3 // BI_BITFIELDS：RGB 三个掩码
	dibAlphaBitfields = 6 // BI_ALPHABITFIELDS：RGB + alpha 四个掩码
)

// dibMaxPixels 转换上限（约 6700 万像素 = 268MB RGBA）：超过就当异常数据拒收，
// 不拿内存与 CPU 去赌一张来路不明的位图。
const dibMaxPixels = 1 << 26

// clipDIBToPNG DIB 字节块（CF_DIB / CF_DIBV5 的剪贴板内容）→ PNG 字节。
func clipDIBToPNG(dib []byte) ([]byte, error) {
	img, err := decodeDIB(dib)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// clipPNGToPNG 校验剪贴板里的 PNG（注册格式 "PNG"）：能读出头部即原样返回——
// 重新编码只会丢元数据、白白多花一次 CPU。
func clipPNGToPNG(b []byte) ([]byte, error) {
	if _, err := png.DecodeConfig(bytes.NewReader(b)); err != nil {
		return nil, fmt.Errorf("clipboard PNG: %w", err)
	}
	return b, nil
}

// dibLayout DIB 解析结果：像素格式 + 调色板/像素数据在字节块里的偏移。
type dibLayout struct {
	w, h        int
	bitCount    int
	compression uint32
	topDown     bool // 负高度 = 自上而下（Windows 缺省是自下而上）
	redMask     uint32
	greenMask   uint32
	blueMask    uint32
	alphaMask   uint32
	entries     int // 调色板项数（无调色板 = 0）
	palette     int // 调色板起点
	bits        int // 像素数据起点
	stride      int // 每行字节数（4 字节对齐）
}

// parseDIB 解析位图头与各段偏移（不解像素）。
func parseDIB(b []byte) (dibLayout, error) {
	var l dibLayout
	le := binary.LittleEndian
	if len(b) < 40 {
		return l, fmt.Errorf("位图头不足 40 字节（实际 %d）", len(b))
	}
	hdr := int(le.Uint32(b[0:]))
	if hdr < 40 || hdr > len(b) {
		return l, fmt.Errorf("位图头长度异常：%d（数据 %d 字节）", hdr, len(b))
	}
	w := int(int32(le.Uint32(b[4:])))
	h := int(int32(le.Uint32(b[8:])))
	if w <= 0 || h == 0 {
		return l, fmt.Errorf("位图尺寸非法：%dx%d", w, h)
	}
	l.topDown = h < 0
	if h < 0 {
		h = -h
	}
	if int64(w)*int64(h) > dibMaxPixels {
		return l, fmt.Errorf("位图过大：%dx%d", w, h)
	}
	l.w, l.h = w, h
	l.bitCount = int(le.Uint16(b[14:]))
	l.compression = le.Uint32(b[16:])
	clrUsed := int(le.Uint32(b[32:]))

	switch l.compression {
	case dibRGB, dibBitfields, dibAlphaBitfields:
	default:
		return l, fmt.Errorf("不支持的位图压缩方式：%d", l.compression)
	}

	next := hdr
	// BITMAPV2 及以上（52/56/108/124 字节头）把掩码放进头部本身。
	if hdr >= 52 {
		l.redMask = le.Uint32(b[40:])
		l.greenMask = le.Uint32(b[44:])
		l.blueMask = le.Uint32(b[48:])
	}
	if hdr >= 56 {
		l.alphaMask = le.Uint32(b[52:])
	}
	// 40 字节头 + BI_BITFIELDS：掩码紧跟头部；BI_ALPHABITFIELDS 多一个 alpha 掩码。
	if hdr == 40 && l.compression != dibRGB {
		n := 3
		if l.compression == dibAlphaBitfields {
			n = 4
		}
		if need := 40 + 4*n; len(b) < need {
			return l, fmt.Errorf("位图掩码不全：需要 %d 字节，实际 %d", need, len(b))
		}
		l.redMask = le.Uint32(b[40:])
		l.greenMask = le.Uint32(b[44:])
		l.blueMask = le.Uint32(b[48:])
		if n == 4 {
			l.alphaMask = le.Uint32(b[52:])
		}
		next = 40 + 4*n
	}

	l.palette = next
	switch l.bitCount {
	case 1, 4, 8:
		l.entries = clrUsed
		if l.entries <= 0 || l.entries > 1<<l.bitCount {
			l.entries = 1 << l.bitCount
		}
		l.bits = l.palette + 4*l.entries
	case 16:
		// BI_RGB 的 16bpp 是固定的 5-5-5；带掩码的走掩码。
		if l.redMask|l.greenMask|l.blueMask == 0 {
			l.redMask, l.greenMask, l.blueMask = 0x7C00, 0x03E0, 0x001F
		}
		l.bits = l.palette + 4*clrUsed
	case 24, 32:
		l.bits = l.palette + 4*clrUsed
	default:
		return l, fmt.Errorf("不支持的位深：%d", l.bitCount)
	}
	l.stride = ((w*l.bitCount + 31) / 32) * 4
	return l, nil
}

// decodeDIB 解析 DIB 并还原成**不预乘**的 NRGBA（PNG 能表达的透明度原样保留）。
func decodeDIB(b []byte) (*image.NRGBA, error) {
	l, err := parseDIB(b)
	if err != nil {
		return nil, err
	}
	if need := l.bits + l.stride*l.h; need > len(b) {
		return nil, fmt.Errorf("位图像素数据不完整：需要 %d 字节，实际 %d", need, len(b))
	}
	dst := image.NewNRGBA(image.Rect(0, 0, l.w, l.h))
	for y := 0; y < l.h; y++ {
		src := y
		if !l.topDown {
			src = l.h - 1 - y // 缺省自下而上：末行在数据块的最后
		}
		row := b[l.bits+src*l.stride:]
		out := dst.Pix[y*dst.Stride:]
		switch l.bitCount {
		case 1:
			for x := 0; x < l.w; x++ {
				idx := int(row[x>>3]>>(7-uint(x&7))) & 1
				writePalette(out[x*4:], b, l, idx)
			}
		case 4:
			for x := 0; x < l.w; x++ {
				v := row[x>>1]
				idx := int(v & 0x0F) // 低半字节 = 偶数位
				if x&1 == 0 {
					idx = int(v >> 4)
				}
				writePalette(out[x*4:], b, l, idx)
			}
		case 8:
			for x := 0; x < l.w; x++ {
				writePalette(out[x*4:], b, l, int(row[x]))
			}
		case 16:
			for x := 0; x < l.w; x++ {
				px := uint32(binary.LittleEndian.Uint16(row[x*2:]))
				out[x*4+0] = channel8(px, l.redMask)
				out[x*4+1] = channel8(px, l.greenMask)
				out[x*4+2] = channel8(px, l.blueMask)
				out[x*4+3] = 0xFF // 16bpp 没有 alpha 通道
			}
		case 24:
			for x := 0; x < l.w; x++ {
				out[x*4+0] = row[x*3+2]
				out[x*4+1] = row[x*3+1]
				out[x*4+2] = row[x*3+0]
				out[x*4+3] = 0xFF
			}
		case 32:
			rgb := l.redMask | l.greenMask | l.blueMask
			for x := 0; x < l.w; x++ {
				if rgb == 0 {
					// BI_RGB 的 32bpp：第 4 字节按 GDI 约定是保留位（不是 alpha），
					// 一律当不透明——否则截图工具放进来的图会整张透明。
					out[x*4+0] = row[x*4+2]
					out[x*4+1] = row[x*4+1]
					out[x*4+2] = row[x*4+0]
					out[x*4+3] = 0xFF
					continue
				}
				px := binary.LittleEndian.Uint32(row[x*4:])
				out[x*4+0] = channel8(px, l.redMask)
				out[x*4+1] = channel8(px, l.greenMask)
				out[x*4+2] = channel8(px, l.blueMask)
				if l.alphaMask != 0 {
					out[x*4+3] = channel8(px, l.alphaMask)
				} else {
					out[x*4+3] = 0xFF
				}
			}
		}
	}
	return dst, nil
}

// writePalette 取调色板第 idx 项写入 dst（BGRX 顺序、四字节；越界按黑色）。
func writePalette(dst, b []byte, l dibLayout, idx int) {
	dst[0], dst[1], dst[2], dst[3] = 0, 0, 0, 0xFF
	if idx < 0 || idx >= l.entries {
		return
	}
	off := l.palette + idx*4
	if off+4 > len(b) {
		return
	}
	dst[0], dst[1], dst[2] = b[off+2], b[off+1], b[off]
}

// channel8 按掩码取一个颜色通道并归一到 0..255（掩码为 0 = 该通道恒 0）。
func channel8(px, mask uint32) byte {
	if mask == 0 {
		return 0
	}
	shift := uint(0)
	for mask&1 == 0 {
		mask >>= 1
		shift++
	}
	bits := uint(0)
	for mask&1 == 1 {
		mask >>= 1
		bits++
	}
	if bits == 0 {
		return 0
	}
	v := uint64(px>>shift) & (1<<bits - 1)
	max := uint64(1)<<bits - 1
	return byte((v*255 + max/2) / max)
}
