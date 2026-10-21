package platform

// 文件选择框的多选结果解析（D186 口径五）：GetOpenFileNameW 打开 OFN_ALLOWMULTISELECT
// 后，缓冲区有两种形态，都要解析对——
//
//	单选：一个完整路径 + 双 NUL   "C:\\a\\报告.pdf\0\0"
//	多选：目录 + 各文件名 + 双 NUL "C:\\a\0报告.pdf\0图.png\0\0"
//
// 判定依据就是 token 个数：只有一个 token 时它本身已是完整路径（不能再拼目录）。
// 抽成不依赖平台的纯函数，测试可在任意系统上跑。

import (
	"path/filepath"
	"strings"
)

// parseMultiSelect 解析多选缓冲区（UTF-16 码元，含末尾双 NUL）→ 绝对路径列表。
// 空缓冲或首 token 为空 = 用户取消，返回 nil。
func parseMultiSelect(buf []uint16) []string {
	tokens := splitNULTokens(buf)
	if len(tokens) == 0 || strings.TrimSpace(tokens[0]) == "" {
		return nil
	}
	if len(tokens) == 1 {
		return []string{tokens[0]}
	}
	dir := tokens[0]
	out := make([]string, 0, len(tokens)-1)
	for _, name := range tokens[1:] {
		if strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// splitNULTokens 按 NUL 切分并去掉末尾的双 NUL 终止（连续 NUL 视为结尾）。
func splitNULTokens(buf []uint16) []string {
	var (
		out []string
		cur []uint16
	)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(runesFromUTF16(cur)))
			cur = nil
		}
	}
	for _, c := range buf {
		if c == 0 {
			flush()
			continue
		}
		cur = append(cur, c)
	}
	flush()
	return out
}

// runesFromUTF16 UTF-16 码元 → rune（含代理对；孤立代理按替换符处理）。
func runesFromUTF16(units []uint16) []rune {
	out := make([]rune, 0, len(units))
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u >= 0xD800 && u < 0xDC00 && i+1 < len(units) &&
			units[i+1] >= 0xDC00 && units[i+1] < 0xE000:
			hi, lo := rune(u)-0xD800, rune(units[i+1])-0xDC00
			out = append(out, 0x10000+(hi<<10)+lo)
			i++
		case u >= 0xD800 && u < 0xE000:
			out = append(out, '\uFFFD')
		default:
			out = append(out, rune(u))
		}
	}
	return out
}
