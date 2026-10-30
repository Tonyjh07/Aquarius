package main

// 角色卡配置（§5.17/D247）：`prompts.user_name` 与 `characters` 段的缺省解析。
// 三处默认开的开关键（apply_post_history / inject_constant_book / inject_examples）用
// 指针区分「键缺失 = 开」与「显式 false」——老配置没有这一段时不该把它们一并关掉。

import (
	"encoding/json"
	"testing"
)

// TestCharacterOptionsDefaults 缺键按文档默认解析：常驻世界书与示例默认开、替换态默认关、
// 尾部指令默认发；显式值覆盖默认。
func TestCharacterOptionsDefaults(t *testing.T) {
	var cfg fileConfig
	opts := cfg.characterOptions()
	if opts.PreferSystemPrompt || !opts.InjectConstantBook || !opts.InjectExamples {
		t.Fatalf("缺键解析 = %+v, want {Prefer:false Book:true Examples:true}", opts)
	}
	if !cfg.applyPostHistory() {
		t.Fatal("apply_post_history 缺键应默认发")
	}

	off := false
	cfg.Characters = charactersConfig{
		PreferSystemPrompt: true,
		ApplyPostHistory:   &off,
		InjectConstantBook: &off,
		InjectExamples:     &off,
	}
	opts = cfg.characterOptions()
	if !opts.PreferSystemPrompt || opts.InjectConstantBook || opts.InjectExamples {
		t.Fatalf("显式解析 = %+v, want {Prefer:true Book:false Examples:false}", opts)
	}
	if cfg.applyPostHistory() {
		t.Fatal("apply_post_history 显式 false 应关")
	}
}

// TestPromptSpecCarriesUserName prompts.user_name 进装配规格（{{user}} 取值）；空 = 由
// 内核回落 "User"。
func TestPromptSpecCarriesUserName(t *testing.T) {
	if got := (&fileConfig{}).promptSpec().UserName; got != "" {
		t.Fatalf("空配置 UserName = %q, want 空（回落 User）", got)
	}
	cfg := &fileConfig{Prompts: promptsConfig{UserName: "小林"}}
	if got := cfg.promptSpec().UserName; got != "小林" {
		t.Fatalf("UserName = %q, want 小林", got)
	}
}

// TestDefaultConfigCarriesCharacterKeys 首跑模板带上 user_name 与 characters 段，且解析后
// 与文档默认一致（否则新用户开箱即与文档记载不符）。
func TestDefaultConfigCarriesCharacterKeys(t *testing.T) {
	var cfg fileConfig
	if err := json.Unmarshal([]byte(defaultConfig), &cfg); err != nil {
		t.Fatalf("默认模板解析失败: %v", err)
	}
	if cfg.Prompts.UserName != "User" {
		t.Fatalf("模板 user_name = %q, want User", cfg.Prompts.UserName)
	}
	if got := cfg.promptSpec().UserName; got != "User" {
		t.Fatalf("模板 promptSpec.UserName = %q, want User", got)
	}
	opts := cfg.characterOptions()
	if !opts.InjectConstantBook || !opts.InjectExamples || opts.PreferSystemPrompt {
		t.Fatalf("模板投影开关 = %+v, want {常驻开 示例开 替换关}", opts)
	}
	if !cfg.applyPostHistory() {
		t.Fatal("模板 apply_post_history 应为 true")
	}
}
