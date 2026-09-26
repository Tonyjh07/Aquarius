package uigui

import "fmt"

// icoImage 从 .ico 字节中选出尺寸最接近 wantPx 的图像条目并返回其数据。
// 纯 Go 目录解析（无 Win32 依赖，可全平台测试）；数据格式 = DIB 或 PNG 位图块，
// CreateIconFromResourceEx 可直接消费。
func icoImage(data []byte, wantPx int) ([]byte, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("ico: 头部不完整（%d 字节）", len(data))
	}
	// ICONDIR：reserved u16=0、type u16=1（图标）、count u16（小端）。
	if data[0] != 0 || data[1] != 0 {
		return nil, fmt.Errorf("ico: reserved 非零")
	}
	typ := uint16(data[2]) | uint16(data[3])<<8
	count := int(uint16(data[4]) | uint16(data[5])<<8)
	if typ != 1 {
		return nil, fmt.Errorf("ico: 类型 %d 非图标（1）", typ)
	}
	if count <= 0 {
		return nil, fmt.Errorf("ico: 无图像条目")
	}
	best := -1
	bestDist := 0
	for i := 0; i < count; i++ {
		off := 6 + i*16
		if off+16 > len(data) {
			return nil, fmt.Errorf("ico: 条目 %d 越界", i)
		}
		w := int(data[off])
		if w == 0 {
			w = 256 // 0 = 256px（扩展约定）
		}
		size := int(le32(data[off+8 : off+12]))
		imgOff := int(le32(data[off+12 : off+16]))
		if size <= 0 || int64(imgOff)+int64(size) > int64(len(data)) {
			return nil, fmt.Errorf("ico: 条目 %d 数据越界", i)
		}
		dist := w - wantPx
		if dist < 0 {
			dist = -dist
		}
		if best < 0 || dist < bestDist {
			best, bestDist = off, dist
		}
	}
	if best < 0 {
		return nil, fmt.Errorf("ico: 无可用条目")
	}
	size := int(le32(data[best+8 : best+12]))
	imgOff := int(le32(data[best+12 : best+16]))
	return data[imgOff : imgOff+size], nil
}

// le32 小端 u32。
func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
