package uigui

// ihist.go 输入框的输入历史（D250 顺带补上）：↑↓ 在收起态翻历史，Shift+Enter 展开成
// 多行编辑时方向键仍归光标。口径与 TUI 同（internal/adapter/uitui）：去重相邻、上限
// 固定、翻到底回草稿。

import "strings"

// ihistMax 历史条数上限（记住最近这么多条，超出丢最旧的）。
const ihistMax = 50

// ihist 输入历史与浏览位置。
//
// pos 是 1 基游标：0 = 停在草稿上（没在翻历史），k = 正看着 lines[k-1]。用 1 基而不是
// -1 表示草稿，是因为零值必须就是「在草稿上」——零值状态下按 ↓ 不该凭空跳出一条。
type ihist struct {
	lines []string
	pos   int
	draft string // 开始翻历史时框里的内容，翻回草稿要退回它
}

// push 记一条已提交的输入：空行不记（提交空行是取消/仅附件，翻回来没有意义），
// 与上一条相同的也跳过（连发两条一样的话，翻历史只会连着看到同一个）。
func (h *ihist) push(line string) {
	h.reset() // 提交过就回到草稿态——哪怕这一条不入历史
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if n := len(h.lines); n > 0 && h.lines[n-1] == line {
		return
	}
	h.lines = append(h.lines, line)
	if len(h.lines) > ihistMax {
		h.lines = h.lines[len(h.lines)-ihistMax:]
	}
}

// reset 回到草稿态（提交后、清空后都调）。
func (h *ihist) reset() {
	h.pos = 0
	h.draft = ""
}

// prev 上一条（↑）：从草稿开始翻时先把当前内容存成草稿，翻到最旧的停在最旧。
func (h *ihist) prev(current string) (string, bool) {
	if len(h.lines) == 0 {
		return "", false
	}
	if h.pos == 0 {
		h.draft = current
		h.pos = len(h.lines)
	} else if h.pos > 1 {
		h.pos--
	}
	return h.lines[h.pos-1], true
}

// next 下一条（↓）：越过最新一条即回草稿（用户翻出去又翻回来的内容不丢）。
func (h *ihist) next() (string, bool) {
	if h.pos == 0 {
		return "", false // 本来就在草稿上：方向键什么都不做
	}
	h.pos++
	if h.pos > len(h.lines) {
		draft := h.draft
		h.reset()
		return draft, true // 回到草稿（翻出去时那行内容）
	}
	return h.lines[h.pos-1], true
}
