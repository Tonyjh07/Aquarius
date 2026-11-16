package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// wireScript 三帧 + 一帧注释：覆盖 data / event / comment 三种形状与结束标记。
const wireScript = ": keep-alive\n\n" +
	`data: {"choices":[{"delta":{"content":"Hello"}}]}` + "\n\n" +
	"event: ping\n" + `data: {"type":"ping"}` + "\n\n" +
	"data: [DONE]\n\n"

func TestSplitWireFrames(t *testing.T) {
	frames := splitWireFrames([]byte(wireScript))
	if len(frames) != 4 {
		t.Fatalf("frames = %d, want 4: %+v", len(frames), frames)
	}
	if frames[0].Kind != "comment" {
		t.Fatalf("frame 1 kind = %q, want comment", frames[0].Kind)
	}
	if frames[1].Kind != "data" || !strings.Contains(frames[1].Head, "Hello") {
		t.Fatalf("frame 2 = %+v", frames[1])
	}
	if frames[2].Kind != "event" || !strings.Contains(frames[2].Head, "ping") {
		t.Fatalf("frame 3 = %+v", frames[2])
	}
	if !frames[3].Marker {
		t.Fatal("last frame should be marked as an end marker")
	}
	for i, f := range frames {
		if f.Index != i+1 {
			t.Fatalf("frame %d index = %d", i, f.Index)
		}
	}
}

// TestSplitWireFramesCRLF 行尾可能是 \r\n：帧正文里不该残留 \r。
func TestSplitWireFramesCRLF(t *testing.T) {
	frames := splitWireFrames([]byte("data: a\r\n\r\ndata: b\r\n\r\n"))
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(frames))
	}
	if strings.Contains(frames[0].Text, "\r") {
		t.Fatalf("frame kept CR: %q", frames[0].Text)
	}
}

// TestSplitWireFramesWholeBody 非 SSE 的整份正文（错误 JSON、HTML 错误页）算一帧。
func TestSplitWireFramesWholeBody(t *testing.T) {
	frames := splitWireFrames([]byte(`{"error":{"message":"bad"}}`))
	if len(frames) != 1 || frames[0].Kind != "body" {
		t.Fatalf("frames = %+v, want one body frame", frames)
	}
	if got := splitWireFrames(nil); got != nil {
		t.Fatalf("empty raw = %+v, want nil", got)
	}
}

// TestRecordWireKeepsLastFew 环形只留最近几次，越界时如实说没了。
func TestRecordWireKeepsLastFew(t *testing.T) {
	a := &Agent{}
	for i := 1; i <= wireKeep+2; i++ {
		a.RecordWire(port.WireTrace{Meta: port.WireMeta{Status: i}})
	}
	tr, total, ok := a.WireTrace(1)
	if !ok || total != wireKeep || tr.Meta.Status != wireKeep+2 {
		t.Fatalf("latest = %+v total=%d ok=%v", tr.Meta, total, ok)
	}
	tr, _, ok = a.WireTrace(wireKeep)
	if !ok || tr.Meta.Status != 3 {
		t.Fatalf("oldest kept = %+v ok=%v, want status 3", tr.Meta, ok)
	}
	if _, total, ok := a.WireTrace(wireKeep + 1); ok || total != wireKeep {
		t.Fatalf("out of range: ok=%v total=%d", ok, total)
	}
}

func TestRenderWireOverview(t *testing.T) {
	tr := port.WireTrace{
		Meta: port.WireMeta{
			Proto: "openai-chat", URL: "https://api.example.com/v1/chat/completions",
			Model: "m", Status: 200, End: port.WireEndDone,
			Headers: []string{"Content-Type: text/event-stream"},
			Notes:   []string{"note-one"},
			Bytes:   len(wireScript),
		},
		Raw: []byte(wireScript),
	}
	out := renderWireOverview(tr, splitWireFrames(tr.Raw), 1, 1)
	for _, want := range []string{"openai-chat", "200", "Content-Type", "note-one", "4", "Hello"} {
		if !strings.Contains(out, want) {
			t.Fatalf("overview missing %q:\n%s", want, out)
		}
	}
}

// TestExecRaw 命令面：没有请求时报因、帧按编号取、越界与非法参数各自报错。
func TestExecRaw(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	s.exportsDir = t.TempDir()
	ctx := context.Background()

	if _, err := s.execRaw(ctx, port.Command{Name: "raw"}); err == nil {
		t.Fatal("execRaw without any response should fail")
	}
	s.agent.RecordWire(port.WireTrace{
		Meta: port.WireMeta{Proto: "openai-chat", Status: 200, End: port.WireEndDone, Bytes: len(wireScript)},
		Raw:  []byte(wireScript),
	})

	out, err := s.execRaw(ctx, port.Command{Name: "raw"})
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if !strings.Contains(out, "Hello") {
		t.Fatalf("overview missing frame rows:\n%s", out)
	}

	frame, err := s.execRaw(ctx, port.Command{Name: "raw", Args: []string{"2"}})
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if !strings.Contains(frame, "Hello") {
		t.Fatalf("frame 2 = %q", frame)
	}

	if _, err := s.execRaw(ctx, port.Command{Name: "raw", Args: []string{"9"}}); err == nil {
		t.Fatal("out-of-range frame should fail")
	}
	for _, bad := range [][]string{{"-2"}, {"0"}, {"x"}, {"1", "2"}, {"full", "1"}, {"-1", "-2"}} {
		if _, err := s.execRaw(ctx, port.Command{Name: "raw", Args: bad}); err == nil {
			t.Fatalf("args %v should be rejected", bad)
		}
	}

	msg, err := s.execRaw(ctx, port.Command{Name: "raw", Args: []string{"full"}})
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if !strings.Contains(msg, ".txt") {
		t.Fatalf("full receipt = %q", msg)
	}
	// 文件名在回执里，直接把导出目录里那份读回来核对：头部之后必须是原始字节。
	entries, err := os.ReadDir(s.exportsDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("exports = %v err=%v", entries, err)
	}
	body, err := os.ReadFile(filepath.Join(s.exportsDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	if !strings.HasSuffix(string(body), wireScript) {
		t.Fatalf("dump should end with the raw bytes, got:\n%s", body)
	}
}
