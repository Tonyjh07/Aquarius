package uicommon

// command.go 单行输入的切分与分派（终端、TUI、GUI 三个前端共用一份）。
//
// 此前三个前端各有一份 parseInput，全都用 strings.Fields：路径里带空格（Windows 上
// `C:\Users\张三\My Documents\备份.aqx` 很常见）就会被切成多个 token，`/import` 看到两个
// 参数直接判成「用法错误」，用户只能猜是哪一步错了。切分规则收在这里，三处不再各写一份。

import (
	"strings"
	"unicode"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ParseInput 把一行输入分派成「命令」或「正文」：以 `/` 开头的是命令，其余原样当正文。
//
// 正文不做任何引号解释——用户打的是什么，进树的就是什么。只有命令行切 token 时才认引号，
// 因为那是「一个参数里有空格」的唯一写法。
func ParseInput(line string) port.UserInput {
	line = strings.TrimSpace(line)
	if line == "" {
		return port.UserInput{}
	}
	if !strings.HasPrefix(line, "/") {
		return port.UserInput{Text: line}
	}
	fields := CommandFields(line)
	if len(fields) == 0 {
		return port.UserInput{Text: line}
	}
	return port.UserInput{Command: &port.Command{
		Name: strings.TrimPrefix(fields[0], "/"),
		Args: fields[1:],
	}}
}

// CommandFields 把一行命令切成参数：空白分隔，**双引号或单引号括起来的一段算一个参数**
// （`/import "C:\My Docs\备份.aqx"` 是命令 import + 一个参数）。
//
// 不做反斜杠转义：Windows 路径里反斜杠是分隔符，把它当转义符会让 `C:\Users` 变成
// `C:Users`——那比「不认转义」坏得多。引号不配对时把剩下的整段都算作同一个参数（用户漏了
// 收尾引号，报「用法错误」不如把原话交上去让命令面自己说哪里不对）。
func CommandFields(s string) []string {
	var (
		out   []string
		cur   strings.Builder
		quote rune // 0 = 不在引号里；否则是当前引号字符
		open  bool // 已开始一个参数（区分「空参数」与「还没开始」）
	)
	flush := func() {
		if open {
			out = append(out, cur.String())
			cur.Reset()
			open = false
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
			open = true // `""` 是一个空参数，不是「没有参数」
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
			open = true
		}
	}
	flush()
	return out
}
