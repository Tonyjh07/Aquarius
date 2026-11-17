package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// auditview.go `/audit` 的读与渲染：审计文件每行已经记了 kind、工具名、耗时、
// 成败与 usage，这里只是把它读出来——**不新增字段、不显示参数内容**。
//
// 为什么另起一个查看口：审计行是全仓唯一「模型与工具实际做了什么」的磁盘记录，
// 但此前只有 `/plugin logs` 顺带 tail 插件那几行（D143）。出问题时翻 profile 目录
// 的 JSONL 不是给用户准备的读法。
//
// 读法上有一条刻意的克制：视图不做统计、不做时间窗过滤，只按文件顺序倒着取 N 行。
// 统计要另立规则（`/cost` 才是账），这里要的是「刚才那几次调用长什么样」。

// auditDefaultLines 缺省显示行数。
const auditDefaultLines = 20

// auditNameWidth 名字列宽（超出按 rune 截断，工具名多是 ASCII）。
const auditNameWidth = 22

// auditErrMax 错误摘要上限（完整内容在 audit.log 里，视图只给够判断的长度）。
const auditErrMax = 72

// auditRow 审计行的读侧形态：只解视图要用的字段。
//
// 不复用写侧结构：写侧在 adapter/decorate，app 不该为读一行日志反向依赖适配器；
// 且读侧对未知字段本就该宽容（以后加字段不该让旧视图报错）。
type auditRow struct {
	TS     string `json:"ts"`
	Kind   string `json:"kind"`
	Model  string `json:"model,omitempty"`
	Name   string `json:"name,omitempty"`
	DurMS  int64  `json:"dur_ms"`
	OK     bool   `json:"ok"`
	Err    string `json:"err,omitempty"`
	In     int    `json:"in,omitempty"`
	Out    int    `json:"out,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Op     string `json:"op,omitempty"`
	Target string `json:"target,omitempty"`
	// Decide kind=decide 的结论（`verdict` 是判定的落点，比 ok 更能说明那一行在干什么）。
	Decide *auditDecide `json:"decide,omitempty"`
}

// auditDecide kind=decide 的载荷（只解视图用到的两项：结论与「有没有拿到分数」）。
type auditDecide struct {
	Verdict string `json:"verdict"`
	Scored  bool   `json:"scored"`
}

// readAudit 读整份审计文件，返回可解析的行与总行数（含解析失败的空行/坏行）。
//
// 文件不存在 = 空结果（没有记录不是错误，与 TailToolCalls 同规则）；坏行跳过——
// 审计文件可能被截断或正在被另一个进程追加，读侧不该因此整份失败。
func readAudit(path string) ([]auditRow, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("%s: %w", i18n.T("session.audit.err_read"), err)
	}
	lines := strings.Split(string(data), "\n")
	total := 0
	rows := make([]auditRow, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		total++
		var row auditRow
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		rows = append(rows, row)
	}
	return rows, total, nil
}

// pickAuditRows 倒着取最后 limit 条（可再按工具名过滤），返回保持文件顺序的子集。
func pickAuditRows(rows []auditRow, limit int, tool string) []auditRow {
	if limit <= 0 {
		limit = auditDefaultLines
	}
	out := make([]auditRow, 0, limit)
	for i := len(rows) - 1; i >= 0 && len(out) < limit; i-- {
		if tool != "" && !strings.EqualFold(rows[i].Name, tool) {
			continue
		}
		out = append(out, rows[i])
	}
	for l, r := 0, len(out)-1; l < r; l, r = l+1, r-1 {
		out[l], out[r] = out[r], out[l]
	}
	return out
}

// renderAudit 审计行表格：一行一条，时间/种类/名字/耗时/状态 + 该 kind 的要点。
func renderAudit(rows []auditRow, total int, tool string) string {
	if total == 0 {
		return i18n.T("session.audit.empty")
	}
	if len(rows) == 0 {
		return i18n.Tf("session.audit.no_match", "tool", tool)
	}
	var b strings.Builder
	if tool == "" {
		b.WriteString(i18n.Tf("session.audit.head", "n", fmt.Sprint(len(rows)), "all", fmt.Sprint(total)))
	} else {
		b.WriteString(i18n.Tf("session.audit.head_tool", "n", fmt.Sprint(len(rows)),
			"all", fmt.Sprint(total), "tool", tool))
	}
	for _, r := range rows {
		b.WriteByte('\n')
		b.WriteString(auditRowLine(r))
	}
	b.WriteString("\n")
	b.WriteString(i18n.T("session.audit.hint"))
	return b.String()
}

// auditRowLine 单行的定宽排版。列宽固定而不是按内容算：同一份日志前后两次看，
// 列位置不该因为最近几行的长短而漂移。
func auditRowLine(r auditRow) string {
	kind := r.Kind
	if kind == "" {
		kind = "-"
	}
	name := r.Name
	if name == "" {
		name = r.Model
	}
	line := fmt.Sprintf("%s  %-6s  %-*s  %6s  %-4s%s",
		auditClock(r.TS), kind, auditNameWidth, auditClip(name, auditNameWidth),
		auditDur(r.DurMS), auditStatus(r), auditDetail(r))
	return strings.TrimRight(line, " ") // 无要点时不留行尾空白（终端选中会带上）
}

// auditClock 审计时间转本地时分秒：文件里是 UTC，直接给 UTC 要用户自己换算时区。
func auditClock(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		if len(ts) >= 8 {
			return ts[len(ts)-8:] // 解析不了就给个能对上的尾巴，不整列留空
		}
		return ts
	}
	return t.Local().Format("15:04:05")
}

// auditDur 耗时列；没记（0）时留短横线，别写成「0ms」让人以为是零耗时。
func auditDur(ms int64) string {
	switch {
	case ms <= 0:
		return "-"
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	default:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
}

// auditStatus 状态列：decide 行给判定结论（比 ok/FAIL 更准确），其余给 ok/FAIL。
func auditStatus(r auditRow) string {
	if r.Decide != nil && r.Decide.Verdict != "" {
		return r.Decide.Verdict
	}
	if r.OK {
		return i18n.T("session.audit.ok")
	}
	return i18n.T("session.audit.fail")
}

// auditDetail 该行的要点：llm 给 usage，policy 给放宽范围，其余给错误摘要。
//
// 错误摘要对所有 kind 都优先——一行失败记录的价值全在那句原因上，比 usage 重要。
func auditDetail(r auditRow) string {
	if r.Err != "" {
		return "  " + auditClip(r.Err, auditErrMax)
	}
	switch r.Kind {
	case "llm":
		if r.In > 0 || r.Out > 0 {
			return i18n.Tf("session.audit.usage", "in", fmt.Sprint(r.In), "out", fmt.Sprint(r.Out))
		}
	case "policy":
		parts := make([]string, 0, 3)
		for _, p := range []string{r.Mode, r.Op, r.Target} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) > 0 {
			return "  " + strings.Join(parts, " · ")
		}
	}
	return ""
}

// auditClip 按 rune 截断并补省略号（按字节切会把多字节字符切坏）。
func auditClip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	out := []rune(s)[:max]
	return string(out) + "…"
}
