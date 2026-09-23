package conversation

import (
	"crypto/rand"
	"sync"
	"time"
)

// ID 会话标识。
type ID string

// MessageID 消息标识。"" 保留给虚拟 Root（DESIGN §4.1 不变量 1 / D15）。
type MessageID string

// NewID 生成新的会话 ID（ULID）。
func NewID() ID { return ID(newULID()) }

// NewMessageID 生成新的消息 ID（ULID，进程内严格单调，可按创建顺序排序）。
func NewMessageID() MessageID { return MessageID(newULID()) }

const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	ulidMu     sync.Mutex
	ulidLastMs uint64
	ulidEnt    [10]byte
)

// newULID 生成 26 字符 ULID：48 位毫秒时间戳 + 80 位熵；同毫秒内熵递增，进程内严格单调。
func newULID() string {
	ulidMu.Lock()
	defer ulidMu.Unlock()

	ms := uint64(time.Now().UnixMilli())
	if ms <= ulidLastMs {
		// 同毫秒或时钟回拨：沿用上次时间戳并递增熵，保证单调。
		ms = ulidLastMs
		incBigEndian(ulidEnt[:])
	} else {
		ulidLastMs = ms
		// crypto/rand.Read 文档保证不失败且整段填满，此处错误可安全忽略。
		_, _ = rand.Read(ulidEnt[:])
	}
	return encodeULID(ms, ulidEnt)
}

// incBigEndian 将大端字节数组加一；溢出（全 1）时重新随机播种。
func incBigEndian(b []byte) {
	for i := len(b) - 1; i >= 0; i-- {
		b[i]++
		if b[i] != 0 {
			return
		}
	}
	_, _ = rand.Read(b)
}

// encodeULID 编码为 26 字符 Crockford Base32：时间戳 48 位 → 10 字符，熵 80 位 → 16 字符。
func encodeULID(ms uint64, ent [10]byte) string {
	var out [26]byte
	v := ms
	for i := 9; i >= 0; i-- {
		out[i] = crockfordAlphabet[v&31]
		v >>= 5
	}
	for i := 0; i < 16; i++ {
		var x byte
		for j := 0; j < 5; j++ {
			bit := i*5 + j
			x = x<<1 | (ent[bit/8]>>(7-bit%8))&1
		}
		out[10+i] = crockfordAlphabet[x]
	}
	return string(out[:])
}
