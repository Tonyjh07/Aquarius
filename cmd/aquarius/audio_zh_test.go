package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/zhconv"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// scriptedTranscriber 固定返回一段文本的转写器（验包装层，不碰引擎）。
type scriptedTranscriber struct {
	text string
	err  error
}

func (s scriptedTranscriber) Transcribe(context.Context, conversation.BlobRef, string) (string, error) {
	return s.text, s.err
}

// TestZhSimplifierConverts 本地识别的繁体输出过一道繁转简再交给上层。
func TestZhSimplifierConverts(t *testing.T) {
	conv, err := zhconv.New(zhconv.ModeTW2SP)
	if err != nil {
		t.Fatalf("zhconv.New: %v", err)
	}
	z := &zhSimplifier{inner: scriptedTranscriber{text: "今天天氣不錯，我們討論新版本的發布計畫。"}, conv: conv}
	got, err := z.Transcribe(context.Background(), conversation.BlobRef{}, "zh")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got != "今天天气不错，我们讨论新版本的发布计划。" {
		t.Fatalf("转换结果 = %q", got)
	}
}

// TestZhSimplifierKeepsError 转写失败时原样透传，不把错误吞成空文本。
func TestZhSimplifierKeepsError(t *testing.T) {
	conv, err := zhconv.New(zhconv.ModeTW2SP)
	if err != nil {
		t.Fatalf("zhconv.New: %v", err)
	}
	z := &zhSimplifier{inner: scriptedTranscriber{err: errors.New("模型未加载")}, conv: conv}
	if _, err := z.Transcribe(context.Background(), conversation.BlobRef{}, "zh"); err == nil ||
		!strings.Contains(err.Error(), "模型未加载") {
		t.Fatalf("err = %v", err)
	}
}

// TestZhSimplifierDegradesOnConvertFailure 转换本身失败时保留原文并记一笔——用户宁可
// 看见繁体，也不想因为转换出错而丢掉识别结果。
func TestZhSimplifierDegradesOnConvertFailure(t *testing.T) {
	var logged error
	z := &zhSimplifier{
		inner: scriptedTranscriber{text: "今天天氣不錯"},
		conv:  failingConverter{},
		logf:  func(err error) { logged = err },
	}
	got, err := z.Transcribe(context.Background(), conversation.BlobRef{}, "zh")
	if err != nil {
		t.Fatalf("转换失败不该让转写失败: %v", err)
	}
	if got != "今天天氣不錯" {
		t.Fatalf("降级应保留原文，得 %q", got)
	}
	if logged == nil {
		t.Fatal("转换失败应记一笔")
	}
}

// failingConverter 永远失败的转换器（模拟词典加载不了）。
type failingConverter struct{}

func (failingConverter) Convert(text string) (string, error) {
	return text, errors.New("词典加载失败")
}
