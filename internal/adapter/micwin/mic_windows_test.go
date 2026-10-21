//go:build windows

package micwin

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestWrapWAV WAV 头字段与长度（16kHz / mono / 16bit）。
func TestWrapWAV(t *testing.T) {
	pcm := bytes.Repeat([]byte{0x01, 0x02}, 1600) // 0.1 秒
	wav := wrapWAV(pcm)
	if len(wav) != 44+len(pcm) {
		t.Fatalf("长度 = %d, want %d", len(wav), 44+len(pcm))
	}
	le := binary.LittleEndian
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" ||
		string(wav[12:16]) != "fmt " || string(wav[36:40]) != "data" {
		t.Fatalf("块标记不对: %q %q %q %q", wav[0:4], wav[8:12], wav[12:16], wav[36:40])
	}
	checks := []struct {
		name string
		off  int
		got  uint32
		want uint32
	}{
		{"riff size", 4, le.Uint32(wav[4:]), uint32(36 + len(pcm))},
		{"fmt size", 16, le.Uint32(wav[16:]), 16},
		{"format", 20, uint32(le.Uint16(wav[20:])), 1},
		{"channels", 22, uint32(le.Uint16(wav[22:])), 1},
		{"sample rate", 24, le.Uint32(wav[24:]), 16000},
		{"byte rate", 28, le.Uint32(wav[28:]), 32000},
		{"block align", 32, uint32(le.Uint16(wav[32:])), 2},
		{"bits", 34, uint32(le.Uint16(wav[34:])), 16},
		{"data size", 40, le.Uint32(wav[40:]), uint32(len(pcm))},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if !bytes.Equal(wav[44:], pcm) {
		t.Fatal("PCM 数据被改动")
	}
}
