package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func newMicSession(t *testing.T, ing port.Ingestor, streams ...*scriptStream) (*Session, *recorder) {
	t.Helper()
	rec := &recorder{}
	llm := &scriptLLM{t: t, streams: streams}
	agent := newAgent(t, llm, rec, Deps{}, Config{})
	s, err := NewSession(context.Background(), SessionDeps{
		Store:     newMemStore(),
		Agent:     agent,
		IDs:       &seqIDs{},
		Clock:     fixedClock{testTime},
		Ingestors: []port.Ingestor{ing},
		UI:        rec,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, rec
}

// TestMicCommandRecordsAndSends /mic 5 → 摄取器收到 mic + 5 秒，转写文本作为用户输入
// 提交并跑一轮（D179）。
func TestMicCommandRecordsAndSends(t *testing.T) {
	// micIngestor 记不住 RawInput（接口值），用闭包版别：直接断言秒数需重写。
	ing := &micCaptureIngestor{text: "明天下午三点开会"}
	s, rec := newMicSession(t, ing, textStream("记下了"))
	if _, err := s.Handle(context.Background(), port.UserInput{
		Command: &port.Command{Name: "mic", Args: []string{"5"}},
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if ing.raw.Kind != "mic" || ing.raw.Seconds != 5 {
		t.Fatalf("RawInput = %+v", ing.raw)
	}
	if !slices.Contains(eventNames(rec.events), "committed") {
		t.Fatalf("应跑完一轮，事件 = %v", eventNames(rec.events))
	}
}

// TestMicCommandDefaultsAndRejectsBadSeconds 缺省 60 秒；越界/非数字直接报错，不录音。
func TestMicCommandDefaultsAndRejectsBadSeconds(t *testing.T) {
	ing := &micCaptureIngestor{text: "嗯"}
	s, _ := newMicSession(t, ing, textStream("好"))
	if _, err := s.Handle(context.Background(), port.UserInput{
		Command: &port.Command{Name: "mic"},
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if ing.raw.Seconds != 60 {
		t.Fatalf("缺省时长 = %d, want 60", ing.raw.Seconds)
	}
	for _, arg := range []string{"0", "241", "abc"} {
		_, err := s.Handle(context.Background(), port.UserInput{
			Command: &port.Command{Name: "mic", Args: []string{arg}},
		})
		if err == nil || !strings.Contains(err.Error(), "秒数") {
			t.Fatalf("/mic %s 应报秒数错误，得到 %v", arg, err)
		}
	}
}

// micCaptureIngestor 记下 RawInput 的语音摄取器。
type micCaptureIngestor struct {
	raw  port.RawInput
	text string
}

func (m *micCaptureIngestor) Accepts(raw port.RawInput) bool { return raw.Kind == "mic" }

func (m *micCaptureIngestor) Ingest(_ context.Context, raw port.RawInput) (port.IngestReport, error) {
	m.raw = raw
	return port.IngestReport{Parts: []conversation.Part{{Kind: conversation.PartText, Text: m.text}}}, nil
}
