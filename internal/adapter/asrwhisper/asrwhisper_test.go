package asrwhisper

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	whisper "github.com/mpyw/whisper.go"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// fakeModel 替身：记录收到的样本与选项，返回脚本化的结果（真模型 60MB 不进仓库，
// 契约测试只验「适配器把什么喂给了引擎、拿回来的东西怎么拼」）。
type fakeModel struct {
	mu       sync.Mutex
	calls    int
	gotPCM   []float32
	gotOpts  *whisper.TranscribeOptions
	result   *whisper.Result
	err      error
	closed   bool
	closeErr error
}

func (f *fakeModel) Transcribe(_ context.Context, pcm []float32, opts *whisper.TranscribeOptions) (*whisper.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.gotPCM = pcm
	f.gotOpts = opts
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func (f *fakeModel) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.closeErr
}

// wavBytes 造一段 16bit 单声道 16kHz 的 WAV，samples 是样本值。
func wavBytes(samples []int16) []byte {
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+len(samples)*2))
	buf.WriteString("WAVEfmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))    // fmt 块长度
	binary.Write(&buf, binary.LittleEndian, uint16(1))     // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(1))     // 单声道
	binary.Write(&buf, binary.LittleEndian, uint32(16000)) // 采样率
	binary.Write(&buf, binary.LittleEndian, uint32(32000)) // 字节率
	binary.Write(&buf, binary.LittleEndian, uint16(2))     // 块对齐
	binary.Write(&buf, binary.LittleEndian, uint16(16))    // 位深
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(samples)*2))
	for _, s := range samples {
		binary.Write(&buf, binary.LittleEndian, s)
	}
	return buf.Bytes()
}

// newClient 造一个带假模型的客户端。
func newClient(t *testing.T, m Model) (*Client, *blobfs.Store) {
	t.Helper()
	blobs, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	c, err := New(Config{Model: m, Blobs: blobs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, blobs
}

// putWAV 把 WAV 塞进附件库。
func putWAV(t *testing.T, b *blobfs.Store, data []byte) conversation.BlobRef {
	t.Helper()
	ref, err := b.Put(context.Background(), bytes.NewReader(data), "audio/wav", "mic.wav")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	return ref
}

// TestTranscribeFeedsSamplesAndJoinsSegments 正常路径：WAV 解成 [-1,1] 样本喂给引擎，
// 分段拼成一段话。
func TestTranscribeFeedsSamplesAndJoinsSegments(t *testing.T) {
	m := &fakeModel{result: &whisper.Result{
		Language: "zh",
		Segments: []whisper.Segment{
			{Text: " 今天天氣不錯，"}, {Text: "我們下午三點討論。"},
		},
	}}
	c, blobs := newClient(t, m)
	samples := make([]int16, 2000)
	copy(samples, []int16{0, 16384, -16384, 32767})
	ref := putWAV(t, blobs, wavBytes(samples))

	text, err := c.Transcribe(context.Background(), ref, "zh")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "今天天氣不錯，我們下午三點討論。" {
		t.Fatalf("拼段 = %q", text)
	}
	if m.gotOpts == nil || m.gotOpts.Language != "zh" {
		t.Fatalf("语种选项 = %+v", m.gotOpts)
	}
	want := []float32{0, 0.5, -0.5, 32767.0 / 32768}
	if len(m.gotPCM) < len(want) {
		t.Fatalf("样本数 = %d, want 至少 %d", len(m.gotPCM), len(want))
	}
	for i := range want {
		if diff := m.gotPCM[i] - want[i]; diff > 1e-6 || diff < -1e-6 {
			t.Errorf("样本[%d] = %v, want %v", i, m.gotPCM[i], want[i])
		}
	}
}

// TestTranscribeEmptyLangIsAuto 空语种 = auto（引擎自己判断）。
func TestTranscribeEmptyLangIsAuto(t *testing.T) {
	m := &fakeModel{result: &whisper.Result{Segments: []whisper.Segment{{Text: "hi"}}}}
	c, blobs := newClient(t, m)
	ref := putWAV(t, blobs, wavBytes(make([]int16, 2000)))

	if _, err := c.Transcribe(context.Background(), ref, "  "); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if m.gotOpts.Language != "auto" {
		t.Fatalf("空语种应转 auto，得 %q", m.gotOpts.Language)
	}
}

// TestTranscribeRejectsShortAudio 太短的录音提前判掉，不白跑一遍引擎。
func TestTranscribeRejectsShortAudio(t *testing.T) {
	m := &fakeModel{}
	c, blobs := newClient(t, m)
	ref := putWAV(t, blobs, wavBytes(make([]int16, 100)))

	_, err := c.Transcribe(context.Background(), ref, "zh")
	if err == nil || !strings.Contains(err.Error(), "太短") {
		t.Fatalf("err = %v", err)
	}
	if m.calls != 0 {
		t.Fatalf("短音频不该调引擎，实调 %d 次", m.calls)
	}
}

// TestTranscribeEmptyResult 引擎没出文字时报可读原因。
func TestTranscribeEmptyResult(t *testing.T) {
	m := &fakeModel{result: &whisper.Result{}}
	c, blobs := newClient(t, m)
	ref := putWAV(t, blobs, wavBytes(make([]int16, 2000)))

	_, err := c.Transcribe(context.Background(), ref, "zh")
	if err == nil || !strings.Contains(err.Error(), "没有识别出文字") {
		t.Fatalf("err = %v", err)
	}
}

// TestTranscribeEngineError 引擎报错带上原因。
func TestTranscribeEngineError(t *testing.T) {
	m := &fakeModel{err: errors.New("trap")}
	c, blobs := newClient(t, m)
	ref := putWAV(t, blobs, wavBytes(make([]int16, 2000)))

	_, err := c.Transcribe(context.Background(), ref, "zh")
	if err == nil || !strings.Contains(err.Error(), "trap") {
		t.Fatalf("err = %v", err)
	}
}

// TestDecodeWAVRejectsWrongFormat 只认 16bit 单声道 16kHz：别的格式喂进去只会出乱码。
func TestDecodeWAVRejectsWrongFormat(t *testing.T) {
	good := wavBytes(make([]int16, 2000))
	if _, err := decodeWAV(good); err != nil {
		t.Fatalf("正常 WAV 应能解: %v", err)
	}
	// 44.1kHz
	bad := bytes.Clone(good)
	binary.LittleEndian.PutUint32(bad[24:], 44100)
	if _, err := decodeWAV(bad); err == nil || !strings.Contains(err.Error(), "44100") {
		t.Fatalf("错采样率应报错，得到 %v", err)
	}
	// 双声道
	bad = bytes.Clone(good)
	binary.LittleEndian.PutUint16(bad[22:], 2)
	if _, err := decodeWAV(bad); err == nil || !strings.Contains(err.Error(), "2 声道") {
		t.Fatalf("双声道应报错，得到 %v", err)
	}
	// 8bit
	bad = bytes.Clone(good)
	binary.LittleEndian.PutUint16(bad[34:], 8)
	if _, err := decodeWAV(bad); err == nil || !strings.Contains(err.Error(), "8bit") {
		t.Fatalf("8bit 应报错，得到 %v", err)
	}
	// 非 WAV
	if _, err := decodeWAV([]byte("not a wav file")); err == nil || !strings.Contains(err.Error(), "WAV") {
		t.Fatalf("非 WAV 应报错，得到 %v", err)
	}
}

// TestDecodeWAVToleratesStreamedLength 管道写出来的 WAV 会把 data 长度留成 0xFFFFFFFF，
// 要按实际字节读而不是报错。
func TestDecodeWAVToleratesStreamedLength(t *testing.T) {
	good := wavBytes(make([]int16, 2000))
	binary.LittleEndian.PutUint32(good[40:], 0xFFFFFFFF)
	pcm, err := decodeWAV(good)
	if err != nil {
		t.Fatalf("流式 WAV 应能解: %v", err)
	}
	if len(pcm) != 2000 {
		t.Fatalf("样本数 = %d, want 2000", len(pcm))
	}
}

// TestCloseReleasesModel 关闭后释放模型，重复关闭幂等，之后的转写报错。
func TestCloseReleasesModel(t *testing.T) {
	m := &fakeModel{result: &whisper.Result{Segments: []whisper.Segment{{Text: "x"}}}}
	c, blobs := newClient(t, m)
	ref := putWAV(t, blobs, wavBytes(make([]int16, 2000)))

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !m.closed {
		t.Fatal("模型未释放")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("重复 Close 应幂等: %v", err)
	}
	if _, err := c.Transcribe(context.Background(), ref, "zh"); err == nil || !strings.Contains(err.Error(), "已关闭") {
		t.Fatalf("关闭后转写应报错，得到 %v", err)
	}
}

// TestJoinSegmentsSpacing 中文段间不插空格，英文段间补一个。
func TestJoinSegmentsSpacing(t *testing.T) {
	res := &whisper.Result{Segments: []whisper.Segment{
		{Text: "hello"}, {Text: "world"}, {Text: "再见"},
	}}
	if got := joinSegments(res); got != "hello world再见" {
		t.Fatalf("joinSegments = %q", got)
	}
	if got := joinSegments(nil); got != "" {
		t.Fatalf("nil 结果 = %q", got)
	}
	if got := joinSegments(&whisper.Result{Segments: []whisper.Segment{{Text: "  "}, {Text: "a"}}}); got != "a" {
		t.Fatalf("空段应跳过，得 %q", got)
	}
}

// TestNewRequiresModelPath 没给模型文件路径、也没注入模型时，装配期就报，并把下载
// 指引带出来（用户最容易卡住的一步）。
func TestNewRequiresModelPath(t *testing.T) {
	blobs, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	_, err = New(Config{Blobs: blobs})
	if err == nil || !strings.Contains(err.Error(), "模型") {
		t.Fatalf("err = %v", err)
	}
	_, err = New(Config{Blobs: blobs, ModelPath: filepath.Join(t.TempDir(), "nope.bin")})
	if err == nil || !strings.Contains(err.Error(), "huggingface") {
		t.Fatalf("缺文件应带下载指引，得到 %v", err)
	}
	if _, err := New(Config{Model: &fakeModel{}}); err == nil || !strings.Contains(err.Error(), "附件库") {
		t.Fatalf("缺附件库应报错，得到 %v", err)
	}
}

// TestNewRejectsEmptyModelFile 空文件不交给引擎（引擎会报一句看不懂的话）。
func TestNewRejectsEmptyModelFile(t *testing.T) {
	blobs, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "empty.bin")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("写空模型: %v", err)
	}
	_, err = New(Config{Blobs: blobs, ModelPath: path})
	if err == nil || !strings.Contains(err.Error(), "为空") {
		t.Fatalf("err = %v", err)
	}
}

// TestTranscribeHonoursContext 上下文取消时不白跑一遍引擎。
func TestTranscribeHonoursContext(t *testing.T) {
	m := &fakeModel{result: &whisper.Result{Segments: []whisper.Segment{{Text: "x"}}}}
	c, blobs := newClient(t, m)
	ref := putWAV(t, blobs, wavBytes(make([]int16, 2000)))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Transcribe(ctx, ref, "zh"); err == nil {
		t.Fatal("已取消的上下文应报错")
	}
	if m.calls != 0 {
		t.Fatalf("取消后不该调引擎，实调 %d 次", m.calls)
	}
}
