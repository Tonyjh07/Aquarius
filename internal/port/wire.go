package port

import "time"

// WireTrace 一次生成请求的原始响应留档（§7.1，`/raw`）：Raw 就是响应体字节本身，
// 没有经过解码。
//
// 它存在的理由是不可替代：端口上的 Delta 是「已解析、已映射成增量」的产物，服务端多发
// 一个字段、少发一帧、把思考链写在另一帧、用错分隔符、或者压根没发结束标记，在 Delta
// 上都看不见。报错路径同理——错误行只带一小段摘要，真实错误体常比这长。
//
// 留档只在内存里、只留最近几次，不落树、不进日志。落盘由命令显式触发（`/raw full`）。
type WireTrace struct {
	Meta WireMeta
	Raw  []byte
}

// WireMeta 一次交换的元信息（不含正文）。字段都是可空的事实，不表达判断。
type WireMeta struct {
	Proto   string // 协议形状：openai-chat / openai-responses / anthropic
	URL     string // 端点；查询串只留键名（防 `?key=` 型端点把密钥带进视图）
	Model   string
	Status  int      // HTTP 状态码
	Headers []string // 白名单响应头，形如 "Content-Type: text/event-stream"
	// Started / Elapsed 收流耗时（从拿到响应头到收流结束）。首字节慢还是流慢，
	// 看 FirstByte 与 Elapsed 的对比。
	Started   time.Time
	FirstByte time.Duration
	Elapsed   time.Duration
	// Bytes 实收字节总数；超过留存上限时 Raw 只留前一段，Truncated 置位。
	Bytes     int
	Truncated bool
	// Notes 适配器侧的事实（剥离重试、兼容回退等）——这些只有解码器知道，
	// 不写在这里就无处可见。
	Notes []string
	End   string // 收流结束原因，取 WireEnd* 之一
}

// 收流结束原因（WireMeta.End）：区分「协议宣告结束」与「连接直接断了」是排查半截流的
// 第一步——两者都表现为正常的 io.EOF。
const (
	WireEndDone      = "done"      // 收到协议结束标记（[DONE] / response.completed / message_stop）
	WireEndEOF       = "eof"       // 连接关闭，没有结束标记
	WireEndCancelled = "cancelled" // 调用方中断
	WireEndError     = "error"     // 收流中解析失败或服务端在流内报错
	WireEndStatus    = "status"    // 非 200，没有流
)
