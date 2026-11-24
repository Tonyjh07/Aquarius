package plugingo

import (
	"bytes"

	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// convert_speech.go 语音两侧的转换（plugin-design.md §3.1）：PCM 与原时间戳原样搬，
// 音色与风格是配置项，不进模型上下文、不进会话树——这里也不需要它们进任何状态。

// toV1SpeechRequest 合成请求。
func toV1SpeechRequest(r port.SpeechRequest) v1.SpeechRequest {
	return v1.SpeechRequest{
		Text:   r.Text,
		Voice:  r.Voice,
		Design: r.Design,
		Style:  r.Style,
		Tags:   r.Tags,
	}
}

// toV1SpeechSegment 一段语音。PCM 复制一份：分段器交出的字节是它自己的缓冲，
// 不让两个组件共享底层数组。
func toV1SpeechSegment(s port.SpeechSegment) v1.SpeechSegment {
	return v1.SpeechSegment{PCM: bytes.Clone(s.PCM), At: s.At}
}

// toPortSpeechSegment 反向，用于插件的分段器交给采集链。
func toPortSpeechSegment(s v1.SpeechSegment) port.SpeechSegment {
	return port.SpeechSegment{PCM: bytes.Clone(s.PCM), At: s.At}
}

// toPortSpeechSegments 一批分段。
func toPortSpeechSegments(in []v1.SpeechSegment) []port.SpeechSegment {
	if len(in) == 0 {
		return nil
	}
	out := make([]port.SpeechSegment, 0, len(in))
	for _, s := range in {
		out = append(out, toPortSpeechSegment(s))
	}
	return out
}
