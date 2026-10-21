package outputtts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

type fakeSynth struct {
	mu   sync.Mutex
	req  port.SpeechRequest
	ref  conversation.BlobRef
	err  error
	wait bool // true = 阻塞到 ctx 取消（模拟被新一条顶掉）
}

func (f *fakeSynth) Synthesize(ctx context.Context, req port.SpeechRequest) (conversation.BlobRef, error) {
	f.mu.Lock()
	f.req = req
	f.mu.Unlock()
	if f.wait {
		<-ctx.Done()
		return conversation.BlobRef{}, ctx.Err()
	}
	return f.ref, f.err
}

func (f *fakeSynth) request() port.SpeechRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.req
}

type fakePlayer struct {
	played chan conversation.BlobRef
	data   chan []byte
}

func newFakePlayer() *fakePlayer {
	return &fakePlayer{played: make(chan conversation.BlobRef, 4), data: make(chan []byte, 4)}
}

func (p *fakePlayer) Play(_ context.Context, ref conversation.BlobRef, r io.Reader) error {
	b, _ := io.ReadAll(r)
	p.played <- ref
	p.data <- b
	return nil
}

func (p *fakePlayer) Stop() {}

func newBlobs(t *testing.T, data []byte) (*blobfs.Store, conversation.BlobRef) {
	t.Helper()
	b, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	ref, err := b.Put(context.Background(), bytes.NewReader(data), "audio/wav", "a.wav")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	return b, ref
}

// TestDeliverSpeaksBodyOnly 只播正文（跳过思考分片），风格/音色参数原样带上。
func TestDeliverSpeaksBodyOnly(t *testing.T) {
	audio := []byte("WAVE")
	blobs, ref := newBlobs(t, audio)
	synth := &fakeSynth{ref: ref}
	player := newFakePlayer()
	a, err := New(Config{Synth: synth, Player: player, Blobs: blobs, Voice: "冰糖", Design: "温柔女声", Style: "语速稍快", Tags: "温柔"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.Deliver(context.Background(), port.OutputRequest{Parts: []conversation.Part{
		{Kind: conversation.PartThinking, Text: "内心戏"},
		{Kind: conversation.PartText, Text: "你好"},
		{Kind: conversation.PartText, Text: "  世界  "},
	}}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	select {
	case got := <-player.played:
		if got.Hash != ref.Hash {
			t.Fatalf("播放引用 = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时：没有开始播放")
	}
	if b := <-player.data; string(b) != string(audio) {
		t.Fatalf("播放字节 = %q", b)
	}
	req := synth.request()
	if req.Text != "你好\n世界" {
		t.Fatalf("合成文本 = %q", req.Text)
	}
	if req.Voice != "冰糖" || req.Design != "温柔女声" || req.Style != "语速稍快" || req.Tags != "温柔" {
		t.Fatalf("合成参数 = %+v", req)
	}
}

// TestDeliverSkipsWhenNoBody 只有思考分片：不发合成请求。
func TestDeliverSkipsWhenNoBody(t *testing.T) {
	blobs, _ := newBlobs(t, []byte("x"))
	synth := &fakeSynth{}
	a, err := New(Config{Synth: synth, Player: newFakePlayer(), Blobs: blobs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.Deliver(context.Background(), port.OutputRequest{Parts: []conversation.Part{
		{Kind: conversation.PartThinking, Text: "只想了不说"},
	}}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if req := synth.request(); req.Text != "" {
		t.Fatalf("不应发合成请求，得到 %+v", req)
	}
}

// TestDeliverLogsFailure 合成失败只记日志，不 panic、不阻塞。
func TestDeliverLogsFailure(t *testing.T) {
	blobs, _ := newBlobs(t, []byte("x"))
	var logged string
	var mu sync.Mutex
	a, err := New(Config{
		Synth:  &fakeSynth{err: errors.New("服务端返回 400")},
		Player: newFakePlayer(),
		Blobs:  blobs,
		Logf: func(format string, args ...any) {
			mu.Lock()
			logged = format
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.Deliver(context.Background(), port.OutputRequest{Parts: []conversation.Part{
		{Kind: conversation.PartText, Text: "你好"},
	}}); err != nil {
		t.Fatalf("Deliver 应立即返回: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := logged
		mu.Unlock()
		if got != "" {
			if !strings.Contains(got, "语音播报失败") {
				t.Fatalf("日志 = %q", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("超时：没有记日志")
}

// scriptSynth 第一条请求阻塞到取消，其余直接返回引用（用于验证「新一条顶掉上一条」）。
type scriptSynth struct {
	mu     sync.Mutex
	calls  []string
	ref    conversation.BlobRef
	startC chan struct{}
}

func (s *scriptSynth) Synthesize(ctx context.Context, req port.SpeechRequest) (conversation.BlobRef, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req.Text)
	first := len(s.calls) == 1
	s.mu.Unlock()
	if first {
		if s.startC != nil {
			close(s.startC)
		}
		<-ctx.Done()
		return conversation.BlobRef{}, ctx.Err()
	}
	return s.ref, nil
}

func (s *scriptSynth) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// TestNewTaskCancelsPrevious 新回答开始播报时取消上一条（同一时刻只有一条）。
func TestNewTaskCancelsPrevious(t *testing.T) {
	blobs, ref := newBlobs(t, []byte("second"))
	synth := &scriptSynth{ref: ref, startC: make(chan struct{})}
	a, err := New(Config{Synth: synth, Player: newFakePlayer(), Blobs: blobs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = a.Deliver(context.Background(), port.OutputRequest{Parts: []conversation.Part{
		{Kind: conversation.PartText, Text: "第一条"},
	}})
	select {
	case <-synth.startC:
	case <-time.After(time.Second):
		t.Fatal("第一条没有进入合成")
	}
	_ = a.Deliver(context.Background(), port.OutputRequest{Parts: []conversation.Part{
		{Kind: conversation.PartText, Text: "第二条"},
	}})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		calls := synth.called()
		if len(calls) >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("第二条没被合成: %v", synth.called())
}

// TestNewValidatesPorts 端口缺一不可。
func TestNewValidatesPorts(t *testing.T) {
	blobs, _ := newBlobs(t, []byte("x"))
	if _, err := New(Config{Player: newFakePlayer(), Blobs: blobs}); err == nil {
		t.Fatal("缺合成器应报错")
	}
	if _, err := New(Config{Synth: &fakeSynth{}, Blobs: blobs}); err == nil {
		t.Fatal("缺播放器应报错")
	}
	if _, err := New(Config{Synth: &fakeSynth{}, Player: newFakePlayer()}); err == nil {
		t.Fatal("缺附件库应报错")
	}
}
