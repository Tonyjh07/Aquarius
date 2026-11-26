package uicommon

// plainview.go 组件树的文本渲染（plugin-design.md §6「降级有定义」）。
//
// 一套数据两个消费者：GUI 用 Gio 画，没有次窗的前端（TUI）把同一棵树染成对齐文本。
// 这里是**纯函数**，不碰终端、不碰状态——两个前端与测试都能直接调。
//
// 降级口径（写死，词汇表扩一项两份渲染器各配一段）：
//   text / markdown → 原样输出（行式界面没有 markdown 管线，标记留着比剥掉更有信息）
//   table           → 列对齐的等宽表
//   kv              → 每对一行「名  值」
//   list            → 每项一行「  - 」
//   form            → 每字段「标签  当前值」，Help 缩进一行
//   buttons         → 一行「[1] 标签  [2] 标签」（编号只是形状，交互归调用方）
//   children        → 依次渲染，空行分隔

import (
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// plainDefaultWidth 没给宽度时的折行上限（终端 80 列减去边距的常见值）。
const plainDefaultWidth = 80

// PlainNode 把组件树渲染成文本。width 是折行参考宽度（<=0 用 80）。
func PlainNode(n port.UINode, width int) string {
	if width <= 0 {
		width = plainDefaultWidth
	}
	var b strings.Builder
	writePlain(&b, n, width)
	return strings.TrimRight(b.String(), "\n")
}

func writePlain(b *strings.Builder, n port.UINode, width int) {
	switch n.Kind {
	case port.UITable:
		writePlainTable(b, n)
	case port.UIKV:
		writePlainKV(b, n)
	case port.UIList:
		for _, it := range n.Items {
			b.WriteString("  - " + it + "\n")
		}
	case port.UIForm:
		writePlainForm(b, n, width)
	case port.UIButtons:
		writePlainButtons(b, n)
	case port.UIText, port.UIMarkdown:
		if n.Text != "" {
			b.WriteString(n.Text + "\n")
		}
	default:
		// 未知种类不静默丢内容：文本在就写文本，子节点照渲染。
		if n.Text != "" {
			b.WriteString(n.Text + "\n")
		}
	}
	for _, c := range n.Children {
		writePlain(b, c, width)
	}
}

// writePlainTable 列对齐表。列宽取该列最宽值；整体超出宽度时按比例压到各列（放不下就
// 截断加省略号）——宁可窄，不要把终端撑出横向滚动。
func writePlainTable(b *strings.Builder, n port.UINode) {
	if len(n.Columns) == 0 && len(n.Rows) == 0 {
		return
	}
	cols := len(n.Columns)
	for _, r := range n.Rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	if cols == 0 {
		return
	}
	widths := make([]int, cols)
	for i, c := range n.Columns {
		widths[i] = TextWidth(c)
	}
	for _, r := range n.Rows {
		for i, c := range r {
			if w := TextWidth(c); w > widths[i] {
				widths[i] = w
			}
		}
	}
	if len(n.Columns) > 0 {
		writePlainRow(b, n.Columns, widths)
		b.WriteString("  " + strings.Repeat("-", totalWidth(widths)+2*(cols-1)) + "\n")
	}
	for _, r := range n.Rows {
		writePlainRow(b, r, widths)
	}
}

func totalWidth(widths []int) int {
	sum := 0
	for _, w := range widths {
		sum += w
	}
	return sum
}

func writePlainRow(b *strings.Builder, cells []string, widths []int) {
	b.WriteString("  ")
	for i, w := range widths {
		if i > 0 {
			b.WriteString("  ")
		}
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		b.WriteString(PadRight(cell, w))
	}
	b.WriteString("\n")
}

// writePlainKV Items 按「名」「值」成对；奇数个时最后一名只写名字。
func writePlainKV(b *strings.Builder, n port.UINode) {
	for i := 0; i < len(n.Items); i += 2 {
		name := n.Items[i]
		if i+1 < len(n.Items) {
			b.WriteString("  " + name + "  " + n.Items[i+1] + "\n")
			continue
		}
		b.WriteString("  " + name + "\n")
	}
}

// writePlainForm 每字段「标签  当前值」+ 可选的说明行。开关与下拉把可选值列出来——行式
// 界面看不到控件形状，不列出来就不知道这个字段能填什么。
func writePlainForm(b *strings.Builder, n port.UINode, width int) {
	for _, f := range n.Fields {
		val := f.Value
		switch f.Kind {
		case "switch":
			if val == "true" {
				val = "on"
			} else {
				val = "off"
			}
		case "select":
			if val == "" {
				val = "-"
			}
			if len(f.Values) > 0 {
				val += "  (" + strings.Join(f.Values, " / ") + ")"
			}
		case "":
			// 插件没给种类：当单行文本
		}
		label := f.Label
		if label == "" {
			label = f.Key
		}
		b.WriteString("  " + label + "  " + wrapPlain(val, width-4, width-6) + "\n")
		if f.Help != "" {
			b.WriteString("    " + wrapPlain(f.Help, width-6, width-6) + "\n")
		}
	}
}

// writePlainButtons 一行编号按钮。编号是给人看的顺序，不是动作 ID——调用方拿到序号后要
// 自己映射回 Actions[i].ID。
func writePlainButtons(b *strings.Builder, n port.UINode) {
	if len(n.Actions) == 0 {
		return
	}
	parts := make([]string, 0, len(n.Actions))
	for i, a := range n.Actions {
		parts = append(parts, "["+strconv.Itoa(i+1)+"] "+a.Label)
	}
	b.WriteString("  " + strings.Join(parts, "   ") + "\n")
}

// wrapPlain 折行（按显示宽，CJK 记 2）。first 是首行预算（前面已有缩进），rest 是续行
// 的总预算（含缩进）。只在表单的说明与值上用到，缩进固定四格。
func wrapPlain(s string, first, rest int) string {
	if TextWidth(s) <= first {
		return s
	}
	const indent = "    "
	var out strings.Builder
	line, budget := 0, first
	for _, r := range s {
		w := runeWidthOf(r)
		if line+w > budget {
			out.WriteString("\n" + indent)
			line, budget = len(indent), rest
		}
		out.WriteRune(r)
		line += w
	}
	return out.String()
}

// TextWidth 字符串显示宽（CJK/全角 = 2、其余 = 1）。与 app/commands.go 的 displayWidth
// 同一套近似：只用于对齐，不做终端图形学的精确计算。
func TextWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidthOf(r)
	}
	return w
}

// PadRight 右侧补空格到给定显示宽（已超宽则原样返回，由调用方决定是否截断）。
func PadRight(s string, w int) string {
	if pad := w - TextWidth(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// runeWidthOf 单字符显示宽。
func runeWidthOf(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 0x1100:
		return 1
	case r <= 0x115F,
		r == 0x2329 || r == 0x232A,
		r >= 0x2E80 && r <= 0x303E,
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF,
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE4F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}
