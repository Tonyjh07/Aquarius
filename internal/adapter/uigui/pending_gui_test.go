package uigui

// pending_gui_test.go 待发清单的界面侧（D272）：生成中键入进清单、拉回并回草稿、
// 改档、满额提示；空闲时键入仍走输入通道（待发清单不是所有输入的必经之路）。

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uicommon"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakePending port.Pending 的内存实现（界面只依赖接口，测试不必拉真会话）。
type fakePending struct {
	items []port.PendingInput
	seq   int
	max   int
}

func (p *fakePending) Add(text string, d port.Delivery) (port.PendingInput, error) {
	if strings.TrimSpace(text) == "" {
		return port.PendingInput{}, port.ErrPendingEmpty
	}
	if p.max > 0 && len(p.items) >= p.max {
		return port.PendingInput{}, port.ErrPendingFull
	}
	p.seq++
	it := port.PendingInput{ID: fmt.Sprintf("p%d", p.seq), Text: strings.TrimSpace(text), Delivery: d}
	p.items = append(p.items, it)
	return it, nil
}

func (p *fakePending) List() []port.PendingInput {
	out := make([]port.PendingInput, len(p.items))
	copy(out, p.items)
	return out
}

func (p *fakePending) Remove(id string) bool {
	for i, it := range p.items {
		if it.ID == id {
			p.items = append(p.items[:i], p.items[i+1:]...)
			return true
		}
	}
	return false
}

func (p *fakePending) SetDelivery(id string, d port.Delivery) bool {
	for i := range p.items {
		if p.items[i].ID == id {
			p.items[i].Delivery = d
			return true
		}
	}
	return false
}

func (p *fakePending) Take(id string) (port.PendingInput, bool) {
	for i, it := range p.items {
		if it.ID == id {
			p.items = append(p.items[:i], p.items[i+1:]...)
			return it, true
		}
	}
	return port.PendingInput{}, false
}

func (p *fakePending) Clear() int {
	n := len(p.items)
	p.items = nil
	return n
}

// newPendingUI 造一个带待发清单的界面（headless 语义：只跑状态机，不开窗）。
func newPendingUI(p *fakePending, generating bool) *UI {
	u := newEditUI()
	u.pendingFn = func() port.Pending { return p }
	u.keys = defaultKeysForTest()
	u.generating.Store(generating)
	return u
}

// defaultKeysForTest 缺省三键（测试不关心可配置性，只关心缺省口径）。
func defaultKeysForTest() uicommon.KeyBindings { return uicommon.DefaultKeyBindings() }

// testGtx 量高用的最小 layout 上下文（1px = 1dp，无可见输出）。
func testGtx() layout.Context {
	var ops op.Ops
	return layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
}

func TestSubmitDuringGenerationGoesToPending(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	u.editor.SetText("顺便看一眼日志")

	u.submitEditor()

	if len(p.items) != 1 || p.items[0].Text != "顺便看一眼日志" || p.items[0].Delivery != port.DeliveryQueue {
		t.Fatalf("清单 = %+v, want 一条排队", p.items)
	}
	if u.editor.Text() != "" {
		t.Fatalf("进清单后编辑器应清空，实际 %q", u.editor.Text())
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("生成中不该再走输入通道，收到 %+v", in)
	default:
	}
	if len(u.m.blocks) != 0 {
		t.Fatalf("待发不该在转写区留下气泡，实际 %d 块", len(u.m.blocks))
	}
}

func TestSteerSubmitDuringGeneration(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	u.editor.SetText("插一句")

	u.submitLine(port.DeliverySteer)

	if len(p.items) != 1 || p.items[0].Delivery != port.DeliverySteer {
		t.Fatalf("清单 = %+v, want 一条引导", p.items)
	}
}

func TestSteerSubmitWhenIdleFallsBackToSend(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, false) // 空闲：插话键与发送等价
	u.editor.SetText("正常一句")

	u.submitLine(port.DeliverySteer)

	if len(p.items) != 0 {
		t.Fatalf("空闲时不该进清单，实际 %+v", p.items)
	}
	select {
	case in := <-u.inCh:
		if in.Text != "正常一句" {
			t.Fatalf("inCh = %+v", in)
		}
	default:
		t.Fatal("空闲时请求没进输入通道")
	}
}

func TestRecallPendingMergesWithDraft(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	_, _ = p.Add("待发那句", port.DeliveryQueue)
	u.editor.SetText("草稿")

	u.recallPending("p1")

	if len(p.items) != 0 {
		t.Fatalf("拉回后清单应清空，实际 %+v", p.items)
	}
	if got := u.editor.Text(); got != "草稿 待发那句" { // 单行编辑器：换行压平成空格
		t.Fatalf("编辑器 = %q, want 草稿 + 空格 + 待发那句", got)
	}
	u.editor.SingleLine = false
	_, _ = p.Add("多行一句", port.DeliveryQueue)
	u.recallPending("p2")
	if got := u.editor.Text(); !strings.Contains(got, "多行一句") || !strings.Contains(got, "\n多行一句") {
		t.Fatalf("多行编辑器应换行分隔，实际 %q", got)
	}
}

func TestTogglePendingDelivery(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	_, _ = p.Add("一条", port.DeliveryQueue)

	u.togglePendingDelivery("p1")
	if p.items[0].Delivery != port.DeliverySteer {
		t.Fatalf("改档后 = %v, want steer", p.items[0].Delivery)
	}
	u.togglePendingDelivery("p1")
	if p.items[0].Delivery != port.DeliveryQueue {
		t.Fatalf("再改一次应回到 queue，实际 %v", p.items[0].Delivery)
	}
}

func TestPendingFullShowsNotice(t *testing.T) {
	p := &fakePending{max: 1}
	u := newPendingUI(p, true)
	_, _ = p.Add("先来一条", port.DeliveryQueue)
	u.editor.SetText("再来一条")

	u.submitEditor()

	if len(p.items) != 1 {
		t.Fatalf("满额时不该收下第二条，实际 %+v", p.items)
	}
	if len(u.m.blocks) != 1 || !strings.Contains(u.m.blocks[0].text, i18n.T("ui.state.pending_full")) {
		t.Fatalf("应有一条满额提示，实际 %+v", u.m.blocks)
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("收不下时不该改走输入通道，收到 %+v", in)
	default:
	}
}

// TestCommandDuringGenerationStaysQueued 命令不进清单：它照旧走输入通道，等本轮之后执行。
func TestCommandDuringGenerationStaysQueued(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	u.editor.SetText("/compact")

	u.submitEditor()

	if len(p.items) != 0 {
		t.Fatalf("命令不该进清单，实际 %+v", p.items)
	}
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "compact" {
			t.Fatalf("inCh = %+v, want /compact", in)
		}
	default:
		t.Fatal("命令应走输入通道")
	}
}

// TestEditDuringGenerationNotPending 编辑既有消息走结构化 /edit（命令），不进清单。
func TestEditDuringGenerationNotPending(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	u.m.editTarget = "n1"
	u.editor.SetText("改一个字")

	u.submitEditor()

	if len(p.items) != 0 {
		t.Fatalf("编辑态不该进清单，实际 %+v", p.items)
	}
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "edit" {
			t.Fatalf("inCh = %+v, want /edit", in)
		}
	default:
		t.Fatal("编辑态应走命令通道")
	}
}

// TestConfirmStateNeverPends 确认态下键入是应答，不进清单也不进输入通道（§15.2）。
func TestConfirmStateNeverPends(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	u.m.confirm = &pendingConfirm{prompt: "允许执行？", reply: make(chan port.ConfirmAnswer, 1)}
	u.editor.SetText("y")

	u.submitEditor()

	if len(p.items) != 0 {
		t.Fatalf("确认态不该进清单，实际 %+v", p.items)
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("确认态不该投递消息，收到 %+v", in)
	default:
	}
}

// TestAttachDuringGenerationQueues 带附件的输入不进清单（清单只收纯文本，§5.19 边界）。
func TestAttachDuringGenerationQueues(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	u.m.stagedFiles = []stagedAtt{{path: "C:/tmp/a.txt"}}
	u.editor.SetText("看看这个文件")

	u.submitEditor()

	if len(p.items) != 0 {
		t.Fatalf("带附件不该进清单，实际 %+v", p.items)
	}
	select {
	case in := <-u.inCh:
		if in.Raw == nil || len(in.Raw.Attachments) != 1 {
			t.Fatalf("inCh = %+v, want Raw 带一个附件", in)
		}
	default:
		t.Fatal("带附件应走输入通道")
	}
}

// TestPendingBandRenders 待发条能真的画出来（一行、两行、超 3 行折计数），不 panic。
func TestPendingBandRenders(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	u.th = testTheme()
	u.frameSize = image.Pt(600, 460)
	for n := 1; n <= 5; n++ {
		_, _ = p.Add(fmt.Sprintf("第 %d 条待发的话", n), port.DeliveryQueue)
		if n == 2 {
			_ = p.SetDelivery("p2", port.DeliverySteer) // 引导徽标与排队徽标两档都要走一遍
		}
		gtx := testGtx()
		u.pendingBand(gtx, 600, 0)
	}
}

func TestPendingBandHeight(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	if got := u.pendingBandPx(testGtx()); got != 0 {
		t.Fatalf("没有待发时高度 = %d, want 0", got)
	}
	for i := 0; i < 5; i++ {
		_, _ = p.Add(fmt.Sprintf("第 %d 条", i), port.DeliveryQueue)
	}
	if got := u.pendingBandPx(testGtx()); got <= 0 {
		t.Fatalf("有待发时高度 = %d, want > 0", got)
	}
}
