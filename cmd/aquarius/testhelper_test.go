package main

// testhelper_test.go 测试里读回「刚被二进制落盘的那棵树」。
//
// 为什么不直接 json.Unmarshal 到 conversation.Conversation：会话树早已私有化，
// 磁盘格式是 storejson 自己的 DTO。测试若自己解 JSON，就等于把适配器的格式再实现
// 一遍——格式一变，测试和实现一起改，验证不到真实读回路径。
// 这里走真正的 storejson：既拿到树，也顺带验证落盘文件能被产品代码读回。

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// loadStored 从 -data 目录下的 profile 会话库读回唯一那棵树，并自检不变量。
func loadStored(t *testing.T, dir string) *conversation.Conversation {
	t.Helper()
	ctx := context.Background()
	store, err := storejson.New(filepath.Join(dir, "profiles", "default", "conversations"))
	if err != nil {
		t.Fatalf("storejson: %v", err)
	}
	sums, err := store.List(ctx)
	if err != nil {
		t.Fatalf("列出会话: %v", err)
	}
	if len(sums) != 1 {
		t.Fatalf("会话数 = %d, want 1", len(sums))
	}
	c, err := store.Load(ctx, sums[0].ID)
	if err != nil {
		t.Fatalf("读回会话: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("落盘会话破坏不变量: %v", err)
	}
	return c
}
