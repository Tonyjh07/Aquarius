package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// contextview.go 装配结果的可读视图（§7.1）：`/context` 的概览、单条全文与整份 dump。
//
// 视图**只读装配产物**，不重拼一份：标签由 buildRequestTracked 在追加处记下（sectionMark），
// 正文直接取自 req.Messages（只去掉段首用于分隔的空行），所以「看到的」就是「发出去的」。
// 任何「按规则再拼一遍」的实现都会随规则演进与真实请求分叉，而这种视图一旦分叉就正好
// 失去该视图唯一的价值。
//
// 视图层自己不写入磁盘、不发事件：写入磁盘与次窗由命令层决定（`/context full` 两者都做）。
//
// 另有一处**必须**守住的界限——该视图是排查工具，不是会话事实：不写入会话树。

// sectionMark 一段内容的坐标（§7.1）：Part < 0 表示整条消息，否则是该消息的第几个分片。
type sectionMark struct {
	Msg    int    // msgs 下标（0 起）
	Part   int    // 分片下标；-1 = 整条消息
	Kind   string // 类别 token（contextLabel*）
	Detail string // 类别补充（世界书条数、记忆构成、注入来源；空 = 无）
}

// ContextSection 概览与 dump 的一行：哪一类、正文是什么、多少 token。
type ContextSection struct {
	Kind       string
	Detail     string
	Msg        int // 1 起（对外编号）；0 = 不属于某条消息
	MsgRole    port.PromptRole
	Text       string
	Tokens     int
	Images     int
	ImageBytes int
}

// ContextReport 一次装配的完整视图。
type ContextReport struct {
	Model     string
	Params    string // 采样参数的人话摘要（render 期拼）
	Budget    int
	CompactAt int
	Sections  []ContextSection
	Tools     int
	ToolNames []string
	ToolText  string // 工具声明的可读清单（dump 用）
	ToolTok   int
	Total     int
	Exact     bool // 精确计数（false = 估算）
	Last      bool // 来自上一轮实发快照（而非「下一轮将发送」）
	// At 这份请求交出去的时刻（只有 Last 有值）。`/diag` 拿该时刻与原始返回的起点对齐，
	// 三部分才落在同一次请求上；`/context` 不展示该时刻。
	At time.Time
	// StabilityOK/Chars/Total 前缀稳定性（D318）：与上一份快照比、跳过 clock 段的最长
	// 公共前缀。OK=false（首次请求 / 跨会话）时概览不出这一行。
	StabilityOK    bool
	StabilityChars int
	StabilityTotal int
}

// contextSnapshot 实发请求的留档：last 是最近一份（/context last），prev 是再上一份
// （前缀稳定性对比的参照，D318）。
type contextSnapshot struct {
	at    time.Time       // 交出去的时刻（`/diag` 的对齐锚点）
	conv  conversation.ID // 所属会话（跨会话不比，D318 决策 3）
	req   port.GenerateRequest
	marks []sectionMark
}

// ContextReport 装配「下一轮将发送」的请求并取其视图。
//
// 命令在回合间隙执行，未提交缓冲（turnBuffer）此刻不存在，所以这里看到的是本轮开头
// 会发的形状；要看刚写入磁盘那一轮的实发内容用 LastContextReport。
func (a *Agent) ContextReport(ctx context.Context, c *conversation.Conversation) (ContextReport, error) {
	if c == nil {
		return ContextReport{}, errors.New("agent: nil conversation")
	}
	req, marks, err := a.buildRequestTracked(ctx, c, nil)
	if err != nil {
		return ContextReport{}, fmt.Errorf("%s: %w", i18n.T("agent.err_assemble"), err)
	}
	rep := a.reportFrom(ctx, req, marks, false, time.Time{})
	// live 的参照是 last（上一轮实发）：缓存命不命中看的是「下一轮请求的前缀」与
	// 「上一轮请求的前缀」是否一致（D318）。
	if cur := (&contextSnapshot{conv: c.ID, req: req, marks: marks}); a.lastSnap.Load() != nil {
		rep.StabilityChars, rep.StabilityTotal, rep.StabilityOK = a.prefixStability(cur, a.lastSnap.Load())
	}
	return rep, nil
}

// LastContextReport 上一轮真正发出去的请求的视图（含自动压缩之后的形态）。
// 没有发过请求时 ok=false。
func (a *Agent) LastContextReport(ctx context.Context) (ContextReport, bool) {
	snap := a.lastSnap.Load()
	if snap == nil {
		return ContextReport{}, false
	}
	rep := a.reportFrom(ctx, snap.req, snap.marks, true, snap.at)
	rep.StabilityChars, rep.StabilityTotal, rep.StabilityOK = a.prefixStability(snap, a.prevSnap.Load())
	return rep, true
}

// saveLast 留档一份实发请求：旧 last 挪进 prev（前缀稳定性对比的参照，D318），last 覆盖
// ——`last` 的语义（最后一次实发）不变。at 取交付时刻，`/diag` 靠该时刻与原始返回对齐。
func (a *Agent) saveLast(at time.Time, conv conversation.ID, req port.GenerateRequest, marks []sectionMark) {
	if old := a.lastSnap.Load(); old != nil {
		a.prevSnap.Store(old)
	}
	a.lastSnap.Store(&contextSnapshot{at: at, conv: conv, req: req, marks: marks})
}

// prefixStability 与上一份快照比前缀稳定性（D318）：两份都按 sectionMark 摊平、跳过 clock
// 段（时间感知戳每轮必变），取段序拼接文本的最长公共前缀。prev 缺失或会话不同 → ok=false
// （缺数据与数据差是两回事）。
func (a *Agent) prefixStability(cur, prev *contextSnapshot) (chars, total int, ok bool) {
	if prev == nil || cur == nil || prev.conv != cur.conv {
		return 0, 0, false
	}
	aText, aOK := flattenSections(cur)
	bText, bOK := flattenSections(prev)
	if !aOK || !bOK {
		return 0, 0, false
	}
	total = len([]rune(aText))
	n := len([]rune(bText))
	if n < total {
		total = n // 公共前缀长不超过较短的串
	}
	i := 0
	ar, br := []rune(aText), []rune(bText)
	for i < total && ar[i] == br[i] {
		i++
	}
	return i, len([]rune(aText)), true
}

// flattenSections 快照 → 段序拼接的文本（clock 段除外，D318）。marks 无效或为空 = 不可比。
func flattenSections(snap *contextSnapshot) (string, bool) {
	if snap == nil || len(snap.marks) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, mk := range snap.marks {
		if mk.Kind == labelClock {
			continue // 时间感知戳每轮必变（D317），排除口径在决策里已排好
		}
		if mk.Msg < 0 || mk.Msg >= len(snap.req.Messages) {
			continue
		}
		msg := snap.req.Messages[mk.Msg]
		switch {
		case mk.Part >= 0 && mk.Part < len(msg.Content):
			t, _, _ := partText(msg.Content[mk.Part])
			b.WriteString(strings.TrimLeft(t, "\n"))
		case mk.Part < 0:
			t, _, _ := partsText(msg.Content)
			b.WriteString(strings.TrimLeft(t, "\n"))
		}
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return "", false
	}
	return b.String(), true
}

// reportFrom 把（请求, 标签）摊成视图。纯读取，不改请求。
func (a *Agent) reportFrom(ctx context.Context, req port.GenerateRequest, marks []sectionMark, last bool, at time.Time) ContextReport {
	rep := ContextReport{
		Model:     req.Model,
		Params:    paramsSummary(req.Params),
		Budget:    a.maxCtx,
		CompactAt: a.compactAt,
		Tools:     len(req.Tools),
		ToolTok:   estimateTools(req.Tools),
		Last:      last,
		At:        at,
	}
	rep.Total, rep.Exact = a.est.Estimate(ctx, req)

	// 标签按（消息, 分片）排序：世界书/记忆/常驻声明的标签是装配后追加进 marks 的，
	// 而这些标签的位置在首条消息内部——不排序会读成「首条消息之后」。
	sorted := append([]sectionMark(nil), marks...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Msg != sorted[j].Msg {
			return sorted[i].Msg < sorted[j].Msg
		}
		return sorted[i].Part < sorted[j].Part
	})
	// 一条消息既有「整条」标签（树里投影出来的 persona）又有后追加的分片标签（世界书、记忆、
	// 常驻声明）时，整条那段只应覆盖**没被单独标走**的分片——否则同一段正文会在账上出现两次。
	marked := map[int]map[int]bool{}
	for _, mk := range marks {
		if mk.Part < 0 {
			continue
		}
		if marked[mk.Msg] == nil {
			marked[mk.Msg] = map[int]bool{}
		}
		marked[mk.Msg][mk.Part] = true
	}

	for _, mk := range sorted {
		if mk.Msg < 0 || mk.Msg >= len(req.Messages) {
			continue
		}
		msg := req.Messages[mk.Msg]
		sec := ContextSection{
			Kind:    mk.Kind,
			Detail:  mk.Detail,
			Msg:     mk.Msg + 1,
			MsgRole: msg.Role,
		}
		switch {
		case mk.Part >= 0 && mk.Part < len(msg.Content):
			sec.Text, sec.Images, sec.ImageBytes = partText(msg.Content[mk.Part])
		case mk.Part < 0 && len(marked[mk.Msg]) == 0:
			sec.Text, sec.Images, sec.ImageBytes = partsText(msg.Content)
		case mk.Part < 0:
			var rest []port.PromptPart
			for i, p := range msg.Content {
				if !marked[mk.Msg][i] {
					rest = append(rest, p)
				}
			}
			sec.Text, sec.Images, sec.ImageBytes = partsText(rest)
		}
		// 段首的空行是装配时用来把分片隔开的，属于分隔不属于正文，读的时候不再叠一层。
		sec.Text = strings.TrimLeft(sec.Text, "\n")
		// 图片按内联字节数粗算（每 token 约 750 字节是常见视觉编码的量级），只为让占比
		// 不出现「一张图 0 token」这种明显违反直觉的读数。
		sec.Tokens = estimateTokens(sec.Text) + sec.ImageBytes/750
		rep.Sections = append(rep.Sections, sec)
	}
	for _, s := range req.Tools {
		rep.ToolNames = append(rep.ToolNames, s.Name)
	}
	rep.ToolText = toolsDump(req.Tools)
	return rep
}

// partsText 一条消息的正文：文本分片按换行拼接，图片分片记数量与字节。
func partsText(parts []port.PromptPart) (string, int, int) {
	var b strings.Builder
	images, bytes := 0, 0
	for _, p := range parts {
		t, im, n := partText(p)
		if t != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(t)
		}
		images += im
		bytes += n
	}
	return b.String(), images, bytes
}

// partText 单个分片的正文：图片不铺 base64，换成一行占位。
func partText(p port.PromptPart) (string, int, int) {
	if p.Kind == port.PromptImage {
		return i18n.Tf("session.ctx.image_placeholder", "mime", p.MIME, "kb", fmt.Sprintf("%.1f", float64(len(p.Data))/1024)), 1, len(p.Data)
	}
	return p.Text, 0, 0
}

// estimateTools 工具声明的估算量：与计数链的 payload 同规则（名字 + 描述 + Schema）。
func estimateTools(specs []tool.Spec) int {
	var b strings.Builder
	for _, s := range specs {
		b.WriteString(s.Name)
		b.WriteByte('\n')
		b.WriteString(s.Description)
		b.WriteByte('\n')
		b.Write(s.Schema)
		b.WriteByte('\n')
	}
	return estimateTokens(b.String())
}

// toolsDump 工具声明的可读清单（dump 用）：一名一行，描述只取首行。
func toolsDump(specs []tool.Spec) string {
	var b strings.Builder
	for _, s := range specs {
		line := "- " + s.Name
		desc := strings.TrimSpace(s.Description)
		if i := strings.IndexByte(desc, '\n'); i >= 0 {
			desc = desc[:i]
		}
		if desc != "" {
			line += ": " + desc
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// paramsSummary 采样参数的人话摘要：只列真正会发出去的项（nil 指针 = 不发送）。
func paramsSummary(p port.Sampling) string {
	var parts []string
	if p.Temperature != nil {
		parts = append(parts, i18n.Tf("session.ctx.param_temp", "v", fmt.Sprintf("%g", *p.Temperature)))
	}
	if p.TopP != nil {
		parts = append(parts, i18n.Tf("session.ctx.param_topp", "v", fmt.Sprintf("%g", *p.TopP)))
	}
	if p.MaxTokens > 0 {
		parts = append(parts, i18n.Tf("session.ctx.param_maxtok", "v", p.MaxTokens))
	}
	if p.Thinking != nil {
		if *p.Thinking {
			parts = append(parts, i18n.T("session.ctx.param_think_on"))
		} else {
			parts = append(parts, i18n.T("session.ctx.param_think_off"))
		}
	}
	if p.ReasoningEffort != "" {
		parts = append(parts, i18n.Tf("session.ctx.param_effort", "v", p.ReasoningEffort))
	}
	if len(parts) == 0 {
		return i18n.T("session.ctx.param_none")
	}
	return strings.Join(parts, " · ")
}
