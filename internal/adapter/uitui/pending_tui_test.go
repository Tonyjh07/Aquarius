package uitui

// pending_tui_test.go 终端侧的插话（D272）：生成中键入的一行进清单、Alt+Enter 是引导、
// 状态行报出待发条数、空输入时 Ctrl+U 撤销最后一条。

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// userMessage 造一个只有一段正文的 user 节点（commit 路径用）。
func userMessage(text string) conversation.Message {
	return conversation.Message{
		ID:      conversation.NewMessageID(),
		Role:    conversation.RoleUser,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: text}},
	}
}

// tuiFakePending port.Pending 的内存实现（只验界面这一侧的口径）。
type tuiFakePending struct {
	items []port.PendingInput
	seq   int
}

func (p *tuiFakePending) Add(text string, d port.Delivery) (port.PendingInput, error) {
	if strings.TrimSpace(text) == "" {
		return port.PendingInput{}, port.ErrPendingEmpty
	}
	p.seq++
	it := port.PendingInput{ID: fmt.Sprintf("p%d", p.seq), Text: strings.TrimSpace(text), Delivery: d}
	p.items = append(p.items, it)
	return it, nil
}

func (p *tuiFakePending) List() []port.PendingInput {
	out := make([]port.PendingInput, len(p.items))
	copy(out, p.items)
	return out
}

func (p *tuiFakePending) Remove(id string) bool {
	for i, it := range p.items {
		if it.ID == id {
			p.items = append(p.items[:i], p.items[i+1:]...)
			return true
		}
	}
	return false
}

func (p *tuiFakePending) SetDelivery(id string, d port.Delivery) bool {
	for i := range p.items {
		if p.items[i].ID == id {
			p.items[i].Delivery = d
			return true
		}
	}
	return false
}

func (p *tuiFakePending) Take(id string) (port.PendingInput, bool) {
	for i, it := range p.items {
		if it.ID == id {
			p.items = append(p.items[:i], p.items[i+1:]...)
			return it, true
		}
	}
	return port.PendingInput{}, false
}

func (p *tuiFakePending) Clear() int {
	n := len(p.items)
	p.items = nil
	return n
}

// newPendingModel 带待发清单的模型（生成中与否由 generating 决定）。
func newPendingModel(t *testing.T, p *tuiFakePending, generating bool) (*model, *UI) {
	t.Helper()
	m, u := newTestModel(t)
	u.opts.Pending = func() port.Pending { return p }
	u.opts.Keys = defaultTUIKeys()
	u.SetInterrupt(nil)
	if generating {
		u.SetInterrupt(func() {})
	}
	return m, u
}

// TestTUIEnterDuringGenerationQueues 生成中回车 = 进清单（排队），不进输入通道。
func TestTUIEnterDuringGenerationQueues(t *testing.T) {
	p := &tuiFakePending{}
	m, u := newPendingModel(t, p, true)
	m.input = []rune("顺便看一眼")

	m.key(tea.KeyMsg{Type: tea.KeyEnter})

	if len(p.items) != 1 || p.items[0].Delivery != port.DeliveryQueue {
		t.Fatalf("清单 = %+v, want 一条排队", p.items)
	}
	if len(m.input) != 0 {
		t.Fatalf("进清单后输入行应清空，实际 %q", string(m.input))
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("生成中不该走输入通道，收到 %+v", in)
	default:
	}
}

// TestTUIAltEnterSteers Alt+Enter = 引导；空闲时回车照旧走输入通道。
func TestTUIAltEnterSteers(t *testing.T) {
	p := &tuiFakePending{}
	m, _ := newPendingModel(t, p, true)
	m.input = []rune("插一句")
	m.key(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if len(p.items) != 1 || p.items[0].Delivery != port.DeliverySteer {
		t.Fatalf("清单 = %+v, want 一条引导", p.items)
	}

	m2, u2 := newPendingModel(t, &tuiFakePending{}, false)
	m2.input = []rune("正常发送")
	m2.key(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case in := <-u2.inCh:
		if in.Text != "正常发送" {
			t.Fatalf("inCh = %+v", in)
		}
	default:
		t.Fatal("空闲时回车应走输入通道")
	}
}

// TestTUIStatusLineCount 状态行报出待发条数（TUI 没有待发条，只有这一处可见）。
func TestTUIStatusLineCount(t *testing.T) {
	p := &tuiFakePending{}
	m, _ := newPendingModel(t, p, true)
	if strings.Contains(m.statusLine(), "待发") {
		t.Fatal("没有待发时状态行不该提它")
	}
	_, _ = p.Add("一条", port.DeliveryQueue)
	_, _ = p.Add("两条", port.DeliverySteer)
	if got := m.statusLine(); !strings.Contains(got, "待发 2 条") {
		t.Fatalf("状态行 = %q, want 待发 2 条", got)
	}
}

// TestTUICtrlUUndoesLastPending 空输入时 Ctrl+U 撤销最后一条（有输入时仍是清行）。
func TestTUICtrlUUndoesLastPending(t *testing.T) {
	p := &tuiFakePending{}
	m, _ := newPendingModel(t, p, true)
	_, _ = p.Add("第一条", port.DeliveryQueue)
	_, _ = p.Add("第二条", port.DeliveryQueue)

	m.key(tea.KeyMsg{Type: tea.KeyCtrlU})
	if len(p.items) != 1 || p.items[0].Text != "第一条" {
		t.Fatalf("清单 = %+v, want 只剩第一条", p.items)
	}
	// 有输入时 Ctrl+U 仍是清行，不动清单。
	m.input = []rune("还在打")
	m.key(tea.KeyMsg{Type: tea.KeyCtrlU})
	if len(m.input) != 0 || len(p.items) != 1 {
		t.Fatalf("有输入时 Ctrl+U 应只清行：input=%q items=%+v", string(m.input), p.items)
	}
}

// TestTUIUserNodeWithoutEcho 轮内落成的 user 节点没有本地回显 → 补一块；有回显的核销不重复。
func TestTUIUserNodeWithoutEcho(t *testing.T) {
	m, _ := newTestModel(t)
	m.echoed = []string{"普通一句"}
	m.commit(userMessage("普通一句"))
	if len(m.blocks) != 0 {
		t.Fatalf("有回显的节点不该重复入块，实际 %+v", m.blocks)
	}
	m.commit(userMessage("轮内插的一句"))
	if len(m.blocks) != 1 || m.blocks[0].kind != blockUser ||
		!strings.Contains(m.blocks[0].text, "轮内插的一句") {
		t.Fatalf("轮内 user 节点应补块，实际 %+v", m.blocks)
	}
}
