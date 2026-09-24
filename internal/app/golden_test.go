package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// updateGolden 为 true 时重写 golden 文件：
// go test ./internal/app -run TestGoldenReplay -update
var updateGolden = flag.Bool("update", false, "重写 golden 回放文件")

// ---------------------------------------------------------------------------
// golden 交互回放框架（DESIGN §11 app 层：脚本流 LLM + 收集器 Presenter +
// 脚本队列 Prompter → 交互回放）。M1 验收：回放覆盖 Revise 两模式与分支导航。
//
// transcript 确定性：节点/会话 ID 一律替换为标签（n1、n2…，按 DFS 首次出现顺序
// 领取、终身不变），golden 不含随机 ULID、不依赖包级计数器，单跑与全量跑一致；
// 脚本里的命令参数同样用标签指代节点，回放前换回真实 ID。
//
// 已知可读性边界（不影响确定性）：标签只回解命令首参，用户正文里偶然出现的
// "n1" 字样不会被回解（归一化后视觉上像标签）；正文里恰好 26 字符的 ULID 也会
// 被归一成标签——ULID 定长互不为前缀、标签非小写 ULID，结果仍确定。
// ---------------------------------------------------------------------------

// replay 一次回放装置。
type replay struct {
	t        *testing.T
	name     string
	session  *Session
	prompter *scriptPrompter
	rec      *recorder
	out      strings.Builder
	labels   map[conversation.MessageID]string // 节点 → 标签（n1、n2…，只增不改）
	next     int                               // 已发放的标签数
	evFrom   int                               // recorder 游标：上次渲染到的事件位置
}

// line 输出一行并做 ID → 标签归一化。
func (r *replay) line(s string) {
	r.out.WriteString(r.norm(s) + "\n")
}

// norm 把文本中出现的节点/会话 ID 替换为标签。各替换相互独立（标签不含 ULID、
// ULID 定长互不为前缀），遍历序不影响结果。
func (r *replay) norm(s string) string {
	for id, label := range r.labels {
		s = strings.ReplaceAll(s, string(id), label)
	}
	return s
}

// syncTree 按 DFS 顺序为尚未登记的节点发放标签（一经发放不再变化）。
func (r *replay) syncTree() {
	for _, id := range dfsOrder(r.session.Current()) {
		if _, ok := r.labels[id]; !ok {
			r.next++
			r.labels[id] = fmt.Sprintf("n%d", r.next)
		}
	}
}

// lookupLabel 反查标签（须完全相等：n1、n12；n1x 不算）。
func (r *replay) lookupLabel(s string) (conversation.MessageID, bool) {
	for id, label := range r.labels {
		if label == s {
			return id, true
		}
	}
	return "", false
}

// resolveRefs 把树交互命令首参里的标签换回真实节点 ID；未知标签原样下传
// （由 Session 报"没有节点"，错误文本同样被归一化）。
func (r *replay) resolveRefs(in port.UserInput) port.UserInput {
	if in.Command == nil {
		return in
	}
	switch in.Command.Name {
	case "goto", "edit", "branch", "rm":
		if len(in.Command.Args) > 0 {
			if id, ok := r.lookupLabel(in.Command.Args[0]); ok {
				cmd := *in.Command
				cmd.Args = append([]string{string(id)}, in.Command.Args[1:]...)
				in.Command = &cmd
			}
		}
	}
	return in
}

// dfsOrder 以 Root 为起点先序遍历（Children 插入序 = 创建序），返回确定性节点序列。
func dfsOrder(c *conversation.Conversation) []conversation.MessageID {
	var out []conversation.MessageID
	seen := map[conversation.MessageID]bool{}
	var walk func(conversation.MessageID)
	walk = func(id conversation.MessageID) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
		for _, ch := range c.Children[id] {
			walk(ch)
		}
	}
	for _, top := range c.Children[""] {
		walk(top)
	}
	return out
}

// renderTree 输出当前会话树：DFS 缩进 + 标签 + 角色 + 内容预览 + Head/修订/终态标记。
func (r *replay) renderTree() {
	c := r.session.Current()
	r.syncTree()
	r.line("会话树:")
	for _, id := range dfsOrder(c) {
		m, ok := c.Find(id)
		if !ok {
			continue
		}
		depth := 0
		for p := m.Parent; p != ""; {
			pm, ok := c.Find(p)
			if !ok {
				break
			}
			depth++
			p = pm.Parent
		}
		var b strings.Builder
		b.WriteString(strings.Repeat("  ", depth))
		fmt.Fprintf(&b, "%s [%s]", r.labels[id], m.Role)
		if m.Role != conversation.RoleRoot {
			if s := nodeSummary(m); s != "（空）" {
				b.WriteString(" " + s)
			}
			if m.Outcome != "" && m.Outcome != conversation.OutcomeDone {
				fmt.Fprintf(&b, " [%s]", m.Outcome)
			}
		}
		if id == c.Head {
			b.WriteString(" ← Head")
		}
		if old, ok := c.RevisedFrom[id]; ok {
			fmt.Fprintf(&b, "（修订自 %s）", old)
		}
		r.line(b.String())
	}
}

// renderEvents 把本步收集的 Turn 事件渲染进 transcript（流式增量合并为一行）。
func (r *replay) renderEvents() {
	events := r.rec.events[r.evFrom:]
	r.evFrom = len(r.rec.events)
	var stream strings.Builder
	flush := func() {
		if stream.Len() == 0 {
			return
		}
		r.line("[stream] " + oneLine(stream.String()))
		stream.Reset()
	}
	for _, ev := range events {
		switch e := ev.(type) {
		case port.DeltaEvent:
			stream.WriteString(e.Delta.Text)
		case port.ToolCallEvent:
			flush()
			r.line("[tool] " + e.Call.Name + " " + oneLine(string(e.Call.Args)))
		case port.ToolResultEvent:
			flush()
			if e.Result.OK {
				r.line("[tool ok] " + oneLine(e.Result.Output))
			} else {
				r.line("[tool failed] " + oneLine(e.Result.Err))
			}
		case port.CommittedEvent:
			flush()
			s := fmt.Sprintf("[committed] %s %s", e.Message.Role, e.Message.Outcome)
			if u := e.Message.Usage; u.InputTokens > 0 || u.OutputTokens > 0 {
				s += fmt.Sprintf(" in=%d out=%d", u.InputTokens, u.OutputTokens)
			}
			r.line(s)
		case port.ErrorEvent:
			flush()
			r.line("[error] " + e.Err.Error())
		default:
			flush()
			r.line(fmt.Sprintf("[%T]", ev))
		}
	}
	flush()
}

// oneLine 压平成单行并截断（transcript 可读性；内容不可信只渲染，DESIGN §9）。
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return s
}

// runReplay 驱动一次完整回放：逐步渲染输入、确认、命令输出、Turn 事件与会话树，
// 每步校验三条不变量，最后与 golden 比对。
func runReplay(t *testing.T, name string, lines []string, answers []bool, streams ...*scriptStream) {
	t.Helper()
	rec := &recorder{}
	llm := &scriptLLM{t: t, streams: streams}
	ids := ulidIDs{}
	agent := newAgent(t, llm, rec, Deps{IDs: ids}, Config{})
	confirmer := &scriptConfirmer{t: t, answers: answers}
	sess, err := NewSession(context.Background(), SessionDeps{
		Store:     newMemStore(),
		Agent:     agent,
		IDs:       ids,
		Clock:     fixedClock{testTime},
		Confirmer: confirmer,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	r := &replay{
		t: t, name: name, session: sess,
		prompter: &scriptPrompter{lines: lines},
		rec:      rec,
		labels:   map[conversation.MessageID]string{},
	}
	r.line("== 回放 " + name + " ==")
	r.renderTree()

	ctx := context.Background()
	for {
		in, err := r.prompter.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("prompter: %v", err)
		}
		raw := r.prompter.lastRaw
		if raw == "" {
			continue
		}
		r.line("> " + raw)
		out, herr := sess.Handle(ctx, r.resolveRefs(in))
		r.syncTree() // 新节点先领标签，后续输出里的 ID 才能归一化
		for _, a := range confirmer.drain() {
			mark := "否"
			if a.answer {
				mark = "是"
			}
			r.line(fmt.Sprintf("[confirm] %s → %s", a.prompt, mark))
		}
		if out != "" {
			r.line(out)
		}
		if herr != nil && !errors.Is(herr, ErrQuit) {
			r.line("! error: " + herr.Error())
		}
		r.renderEvents()
		if errors.Is(herr, ErrQuit) {
			break
		}
		r.renderTree()
		if verr := sess.Current().Validate(); verr != nil {
			t.Fatalf("回放 %s 第 %d 步后不变量被破坏: %v", name, r.prompter.i, verr)
		}
	}
	r.compare()
}

// compare 与 testdata/golden/<name>.golden 比对；-update 时重写。
func (r *replay) compare() {
	r.t.Helper()
	path := filepath.Join("testdata", "golden", r.name+".golden")
	got := r.out.String()
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			r.t.Fatalf("mkdir golden: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			r.t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatalf("读取 golden %s 失败（首次生成: go test ./internal/app -run TestGoldenReplay -update）: %v", path, err)
	}
	if got != string(want) {
		r.t.Errorf("回放 %s 与 golden 不一致（确认无误后 -update 重写）:\n--- want ---\n%s\n--- got ---\n%s",
			r.name, want, got)
	}
}

// TestGoldenReplay 树交互 golden 回放（M1 验收）：
// Revise Fresh/Carry 两模式、分支导航（/goto /branch）、删除确认（/rm）。
func TestGoldenReplay(t *testing.T) {
	cases := []struct {
		name    string
		lines   []string
		answers []bool
		streams []*scriptStream
	}{
		{
			// Fresh：修订留旧分支，两分支各自继续生长；/goto /branch 导航对比。
			name: "revise_fresh",
			lines: []string{
				"你好",
				"/edit n4 更优雅的回复",
				"/branch n4",
				"你看呢",
				"/goto n4",
				"旧分支也聊聊",
				"/branch",
				"/goto n1",
				"/quit",
			},
			streams: []*scriptStream{textStream("你好呀"), textStream("好的"), textStream("嗯嗯")},
		},
		{
			// Carry：边转移保留后续历史（Head 留在转移后的深层），旧节点成空叶子。
			name: "revise_carry",
			lines: []string{
				"问题一",
				"追问",
				"/edit n3 --keep 问题一（改）",
				"/branch n3",
				"继续追问",
				"/goto n3",
				"/branch",
				"/quit",
			},
			streams: []*scriptStream{textStream("答一"), textStream("答二"), textStream("答三")},
		},
		{
			// 分支导航 + 删除：root 不可修订/删除（不消费确认），/rm 拒绝/同意两路径。
			name: "branch_rm",
			lines: []string{
				"你好",
				"/edit n1 不行",
				"/goto n4",
				"/branch",
				"/rm n1",
				"/rm n4",
				"/rm n4",
				"/goto n4",
				"/quit",
			},
			answers: []bool{false, true},
			streams: []*scriptStream{textStream("你好")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runReplay(t, tc.name, tc.lines, tc.answers, tc.streams...)
		})
	}
}
