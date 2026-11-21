package llm

// wire_file_test.go 服务端文件引用块（D315）的 wire 映射：openai-chat 发
// `{"type":"file","file_id":…}`；无 file 分片时 content 保持纯字符串（兼容面不变）。

import (
	"encoding/json"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestBuildContentFileRef 含 file 分片 → content 数组，引用块带 file_id；文本分片照常。
func TestBuildContentFileRef(t *testing.T) {
	parts := []port.PromptPart{
		{Kind: port.PromptText, Text: "看看这份扫描件"},
		{Kind: port.PromptFile, FileID: "file-api-abc", Text: "本地提取文本"},
	}
	got, err := buildContent(parts)
	if err != nil {
		t.Fatalf("buildContent: %v", err)
	}
	arr, ok := got.([]chatContentPart)
	if !ok {
		t.Fatalf("content 类型 = %T, want 分片数组", got)
	}
	if len(arr) != 2 {
		t.Fatalf("分片数 = %d, want 2", len(arr))
	}
	if arr[0].Type != "text" || arr[0].Text != "看看这份扫描件" {
		t.Fatalf("第 0 片 = %+v", arr[0])
	}
	if arr[1].Type != "file" || arr[1].FileID != "file-api-abc" {
		t.Fatalf("第 1 片 = %+v, want file 引用", arr[1])
	}
	data, err := json.Marshal(arr[1])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe struct {
		Type   string `json:"type"`
		FileID string `json:"file_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.Type != "file" || probe.FileID != "file-api-abc" || probe.Text != "" {
		t.Fatalf("引用块 JSON = %s", data)
	}
}

// TestBuildContentPlainStringUnchanged 没有图片也没有 file 分片 → 纯字符串（与从前逐字节相同）。
func TestBuildContentPlainStringUnchanged(t *testing.T) {
	got, err := buildContent([]port.PromptPart{{Kind: port.PromptText, Text: "你好"}})
	if err != nil {
		t.Fatalf("buildContent: %v", err)
	}
	if s, ok := got.(string); !ok || s != "你好" {
		t.Fatalf("content = %T %v, want 纯字符串", got, got)
	}
}
