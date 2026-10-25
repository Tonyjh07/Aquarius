package app

// 命令参数里的 flag 解析（/edit、/inject、/title 三处共用一份实现）。
//
// 三处的历史口径不同，差异只有两条，都在这里显式声明：
//   - 位置：/edit 与 /title 只认正文之前的一段（正文里出现 "--keep" 是字面文本，不再当
//     flag），/inject 认整段任意位置（选项写在正文前后都行）；
//   - 个数：/title 只认一个（第二个 "--id" 归正文），其余不限。
//
// 相同的部分——哪些 token 是 flag、哪些吃一个值、缺值怎么报、剩余 token 怎么拼正文——
// 只写一遍。此前三处各有一套，改一处口径另两处不会跟着变。

import (
	"fmt"
	"strconv"
	"strings"
)

// flagDecl 一个 flag 的声明：name 含前缀（如 "--keep"），value 表示是否吃下一个 token 作值。
type flagDecl struct {
	name  string
	value bool
}

// flagPos flag 允许出现的位置。
type flagPos int

const (
	// flagsLeading 只认正文之前连续的一段：遇到未声明的 token（含未知 "--x"）即为正文起点，
	// 其后即使出现已声明的 flag 也当字面文本。
	flagsLeading flagPos = iota
	// flagsAnywhere 整段任意位置：未声明的 token 归正文，其后的已声明 flag 仍认。
	flagsAnywhere
)

// flagScan 一次扫描的口径。
type flagScan struct {
	pos flagPos
	// limit 最多认几个 flag（0 = 不限）。/title 用 1 保持「--id 只认首参位」的旧口径。
	limit int
}

// cmdFlag 一次命中的 flag，按出现序排列；值型带 value，开关型 value 为空串。
type cmdFlag struct {
	name  string
	value string
}

// scanFlags 按声明表扫一遍参数：命中的 flag 按出现序返回，其余 token 按原序留在 rest
// （调用方拼正文）。值型 flag 后面没有 token 时返回「<flag> 缺参数」，调用方再套自己的
// 用法文案——三处命令的用法句各不相同，但缺参这个事实不该各写一份判断。
func scanFlags(args []string, sc flagScan, decls []flagDecl) ([]cmdFlag, []string, error) {
	var (
		flags []cmdFlag
		rest  []string
	)
	for i := 0; i < len(args); i++ {
		d, known := lookupFlag(decls, args[i])
		if !known || (sc.limit > 0 && len(flags) >= sc.limit) {
			rest = append(rest, args[i])
			if sc.pos == flagsLeading {
				rest = append(rest, args[i+1:]...) // 正文之后不再认 flag
				break
			}
			continue
		}
		f := cmdFlag{name: d.name}
		if d.value {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s 缺参数", d.name)
			}
			f.value = args[i+1]
			i++
		}
		flags = append(flags, f)
	}
	return flags, rest, nil
}

// lookupFlag 按 token 找声明；未声明 = 不是 flag。
func lookupFlag(decls []flagDecl, tok string) (flagDecl, bool) {
	for _, d := range decls {
		if d.name == tok {
			return d, true
		}
	}
	return flagDecl{}, false
}

// joinArgs 把参数按单空格拼回正文并去掉首尾空白：命令面的文本参数一律这么拼
// （"改 这个词" 与 "改 这个词 " 是同一句话）。
func joinArgs(args []string) string {
	return strings.TrimSpace(strings.Join(args, " "))
}

// parseIndex 序号型 flag 的值：非负整数，否则 ok=false（错误措辞由调用方的用法文案给）。
func parseIndex(raw string) (n int, ok bool) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
