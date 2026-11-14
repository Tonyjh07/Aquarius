package vadsilero

// 分段器的测试用两套素材：
//
//   - testdata/speech-16k.pcm：上游 Silero VAD 的真人语音样本（MIT，见 THIRD-PARTY-NOTICES），
//     它的预期分段写在上游的 ExampleSegments 里——「三段话」这件事是真值，不是我自己说了算。
//   - 合成静音：只用来验证「纯静音不出段」这一条，不需要真值。
//
// 合成正弦信号在这里没有用：上游的模型把稳态正弦判成噪声（它的测试里就有一条
// TestModelRejectsSilenceAndTone），用合成波形断言「这是语音」只会写出一个假绿测试。

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// loadSpeech 读入真人语音样本（16kHz 单声道 16bit 裸 PCM，约 6 秒）。
func loadSpeech(t testing.TB) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/speech-16k.pcm")
	if err != nil {
		t.Fatalf("读样本: %v", err)
	}
	return raw
}

// silencePCM 静音（留一点本底噪声，全零对检测器是另一种输入）。
func silencePCM(d time.Duration) []byte {
	n := int(bytesOfDuration(d)) / 2
	out := make([]byte, n*2)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(i%7-3)))
	}
	return out
}

// feedAll 按帧长切块喂进去，收齐所有段。
func feedAll(t testing.TB, seg port.SpeechSegmenter, pcm []byte) []port.SpeechSegment {
	t.Helper()
	var out []port.SpeechSegment
	for off := 0; off+chunkBytes <= len(pcm); off += chunkBytes {
		got, err := seg.Feed(pcm[off : off+chunkBytes])
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		out = append(out, got...)
	}
	tail, err := seg.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return append(out, tail...)
}

// TestSilenceYieldsNoSegment 纯静音一段都不出：这是分段最实在的收益——
// 开着麦半小时不该产生任何附件，也不该发起任何转写。
func TestSilenceYieldsNoSegment(t *testing.T) {
	t.Parallel()
	seg, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := feedAll(t, seg, silencePCM(3*time.Second)); len(got) != 0 {
		t.Fatalf("纯静音应零段，得到 %d 段", len(got))
	}
}

// TestRealSpeechSplitsAtPauses 真人样本（含多段话与停顿）切成多段，且每段都有内容。
//
// 真值见上游 ExampleSegments：5.984 秒的样本里有五段语音、彼此隔开；本仓的缺省参数
// 比上游示例严（静音门槛 100ms 而不是 300ms），段数可能更细，所以这里断言的是
// 「切成了多段」与「段都不空、按序不重叠」，而不是钉死一个数字。
func TestRealSpeechSplitsAtPauses(t *testing.T) {
	t.Parallel()
	seg, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pcm := loadSpeech(t)
	got := feedAll(t, seg, pcm)
	if len(got) < 2 {
		t.Fatalf("含多段话的样本应切成多段，得到 %d 段", len(got))
	}
	total := durationOfBytes(int64(len(pcm)))
	var prevEnd time.Duration
	for i, s := range got {
		if len(s.PCM) == 0 {
			t.Fatalf("第 %d 段是空的", i)
		}
		if s.At < 0 || s.At >= total {
			t.Fatalf("第 %d 段起点越界：%v（总长 %v）", i, s.At, total)
		}
		if s.At < prevEnd {
			t.Fatalf("第 %d 段与前一段重叠：起点 %v < 前段终点 %v", i, s.At, prevEnd)
		}
		prevEnd = s.At + durationOfBytes(int64(len(s.PCM)))
		if prevEnd > total {
			t.Fatalf("第 %d 段越过录音终点：%v > %v", i, prevEnd, total)
		}
	}
}

// TestSegmentsDoNotOverlap 段之间不重叠，且都在总时长之内（含静音拼接的用例）。
func TestSegmentsDoNotOverlap(t *testing.T) {
	t.Parallel()
	seg, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pcm := append(silencePCM(time.Second), loadSpeech(t)...)
	total := durationOfBytes(int64(len(pcm)))
	var prevEnd time.Duration
	for i, s := range feedAll(t, seg, pcm) {
		if s.At < prevEnd {
			t.Fatalf("第 %d 段与前一段重叠：起点 %v < 前段终点 %v", i, s.At, prevEnd)
		}
		prevEnd = s.At + durationOfBytes(int64(len(s.PCM)))
		if prevEnd > total {
			t.Fatalf("第 %d 段越过录音终点：%v > %v", i, prevEnd, total)
		}
	}
}

// TestShortBlipDropped 太短的瞬态（咳嗽、敲键盘）按 MinSpeech 丢弃：
// 把门槛抬到整个样本都够不着，就该一段都不出。
func TestShortBlipDropped(t *testing.T) {
	t.Parallel()
	seg, err := New(Options{MinSpeech: time.Hour})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := feedAll(t, seg, loadSpeech(t)); len(got) != 0 {
		t.Fatalf("门槛抬到一小时应丢弃全部段，得到 %d 段", len(got))
	}
}

// TestWholeSegmenterYieldsOneSegment 关掉分段时的退路：整次录音一段、字节原样。
func TestWholeSegmenterYieldsOneSegment(t *testing.T) {
	t.Parallel()
	pcm := loadSpeech(t)
	got := feedAll(t, NewWhole(), pcm)
	if len(got) != 1 {
		t.Fatalf("整段模式应得一段，得到 %d 段", len(got))
	}
	if len(got[0].PCM) != len(pcm) {
		t.Fatalf("整段模式应原样交出全部音频：得到 %d 字节，喂了 %d 字节", len(got[0].PCM), len(pcm))
	}
	if got[0].At != 0 {
		t.Fatalf("整段模式的起点应为 0，得到 %v", got[0].At)
	}
}

// TestWholeSegmenterEmptyStream 一帧都没喂：零段（空录音不产生空附件）。
func TestWholeSegmenterEmptyStream(t *testing.T) {
	t.Parallel()
	if got, err := NewWhole().Flush(); err != nil || len(got) != 0 {
		t.Fatalf("空流应零段，得到 %d 段（err=%v）", len(got), err)
	}
}

// TestBadOptions 参数越界当场报因，不留到运行期。
func TestBadOptions(t *testing.T) {
	t.Parallel()
	if _, err := New(Options{Threshold: 1.5}); err == nil {
		t.Fatal("阈值 1.5 应报错")
	}
	if _, err := New(Options{Threshold: 0.5, NegThreshold: 0.6}); err == nil {
		t.Fatal("收尾阈值不低于开始阈值应报错")
	}
}
