package jobproc

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// decodeOutput 把子进程输出字节解码为 UTF-8 文本（D41）：**按行探测**——
// UTF-8 合法则原样保留（ASCII 与显式 `chcp 65001` 的输出），否则按 GBK/CP936 解码。
//
// 依据（spike 实测）：GBK 字节几乎不可能通过 UTF-8 校验（双字节尾字节不落在
// UTF-8 连续字节形），反过来 UTF-8 中文过 GBK 解码器必成乱码——所以顺序不可换；
// x/text 的 GBK 解码器宽松：结构非法的字节落 U+FFFD，天然覆盖"两者都不是"的
// 回退（与旧 string(bytes) 行为一致），无需第三分支。
//
// 行粒度的好处：单行乱码不拖累整段（`dir` 的表头是 GBK、文件名行可能是 ASCII
// 或 UTF-8，各按各的判）；日志窗口截断出的半行也只影响该行。
// 已知局限（§14）：非 GBK 的遗留代码页（CP437 等）会误判成乱码中文。
func decodeOutput(s string) string {
	if utf8.ValidString(s) { // 快路径：整体合法（含纯 ASCII）不进逐行
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		if utf8.ValidString(line) {
			b.WriteString(line)
			continue
		}
		decoded, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), []byte(line))
		if err != nil {
			// 防御：宽松解码器实测不报错；真出错退化为逐字节替换（旧行为）。
			b.WriteString(string([]byte(line)))
			continue
		}
		b.Write(decoded)
	}
	return b.String()
}
