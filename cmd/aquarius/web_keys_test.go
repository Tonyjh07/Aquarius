package main

// 联网页密钥入库测试：与 provider 页同一口径——填明文 → 收进密钥库、config 只留引用。
//
// 出过的不一致：设置窗两个页面的密钥框都写着「填明文」，provider 页会转入密钥库并只留
// `secret:<名>`，联网页却把明文原样写进 config（外加一条启动警告）。同一个动作、两种
// 后果，用户无从预期。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// webWiring 搭一个带密钥库的装配根（只填联网保存用得到的字段）。
func webWiring(t *testing.T, cfgPath string) (*wiring, *secretbin.Store) {
	t.Helper()
	bin, err := secretbin.New(filepath.Dir(cfgPath))
	if err != nil {
		t.Fatalf("secretbin.New: %v", err)
	}
	return &wiring{cfgPath: cfgPath, secretsBin: bin, secrets: newSecretChain(bin, os.Stderr)}, bin
}

// TestStoreWebKeysBravePlaintext 联网页填明文 → 入密钥库 + config 只留引用，键名沿用原有
// 引用名（不 churn）。
func TestStoreWebKeysBravePlaintext(t *testing.T) {
	cfgPath := writeWebCfg(t)
	w, bin := webWiring(t, cfgPath)
	p := uigui.SettingsPatch{
		WebSearchProviders: []uigui.WebProviderPatch{{Kind: "brave", Key: "sk-brave-PLAIN-1234"}},
	}
	if err := w.storeWebKeys(&p); err != nil {
		t.Fatalf("storeWebKeys: %v", err)
	}
	if got := p.WebSearchProviders[0].Key; got != "secret:AQUARIUS_BRAVE_API_KEY" {
		t.Fatalf("patch 里的 key 应换成引用，得 %q", got)
	}
	if v, err := bin.Get(context.Background(), "AQUARIUS_BRAVE_API_KEY"); err != nil || v != "sk-brave-PLAIN-1234" {
		t.Fatalf("库里值 = (%q, %v)", v, err)
	}
	// 落盘后 config 里不得出现明文。
	if err := persistSettingsTextKeys(cfgPath, p); err != nil {
		t.Fatalf("persistSettingsTextKeys: %v", err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-brave-PLAIN-1234") {
		t.Fatalf("明文落进了 config:\n%s", data)
	}
	if !strings.Contains(string(data), "secret:AQUARIUS_BRAVE_API_KEY") {
		t.Fatalf("config 应留引用:\n%s", data)
	}
}

// TestStoreWebKeysSkipsBorrowedKinds 借用对话 provider 的 kind 没有自己的密钥槽：填了也
// 不往库里塞（否则会占掉对话 provider 那一格）。
func TestStoreWebKeysSkipsBorrowedKinds(t *testing.T) {
	cfgPath := writeWebCfg(t)
	w, bin := webWiring(t, cfgPath)
	p := uigui.SettingsPatch{
		WebSearchProviders: []uigui.WebProviderPatch{{Kind: "deepseek", Key: "sk-should-not-be-stored"}},
	}
	if err := w.storeWebKeys(&p); err != nil {
		t.Fatalf("storeWebKeys: %v", err)
	}
	if got := p.WebSearchProviders[0].Key; got != "sk-should-not-be-stored" {
		t.Fatalf("借用型不该改写 key，得 %q", got)
	}
	entries, err := bin.Entries(context.Background())
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("不该写库，却有了 %d 条", len(entries))
	}
}

// TestStoreWebKeysKeepsExistingRefs 已填引用 / 留空 = 不动库（引用原样保留）。
func TestStoreWebKeysKeepsExistingRefs(t *testing.T) {
	cfgPath := writeWebCfg(t)
	w, bin := webWiring(t, cfgPath)
	p := uigui.SettingsPatch{
		WebSearchProviders: []uigui.WebProviderPatch{
			{Kind: "brave", Key: "secret:CUSTOM_BRAVE"}, // 引用：照写
			{Kind: "brave"}, // 留空：保持
		},
	}
	if err := w.storeWebKeys(&p); err != nil {
		t.Fatalf("storeWebKeys: %v", err)
	}
	if got := p.WebSearchProviders[0].Key; got != "secret:CUSTOM_BRAVE" {
		t.Fatalf("引用应原样保留，得 %q", got)
	}
	if got := p.WebSearchProviders[1].Key; got != "" {
		t.Fatalf("留空应保持空，得 %q", got)
	}
	entries, _ := bin.Entries(context.Background())
	if len(entries) != 0 {
		t.Fatalf("引用/留空都不该写库，却有了 %d 条", len(entries))
	}
}

// TestStoreWebKeysUnwiredKeepsPlaintext 库未接线：不报错、保持旧行为（明文照写），
// 保存不因「存不进库」而失败。
func TestStoreWebKeysUnwiredKeepsPlaintext(t *testing.T) {
	cfgPath := writeWebCfg(t)
	w := &wiring{cfgPath: cfgPath}
	p := uigui.SettingsPatch{
		WebSearchProviders: []uigui.WebProviderPatch{{Kind: "brave", Key: "sk-plain-keep"}},
	}
	if err := w.storeWebKeys(&p); err != nil {
		t.Fatalf("库未接线不该报错: %v", err)
	}
	if got := p.WebSearchProviders[0].Key; got != "sk-plain-keep" {
		t.Fatalf("库未接线应保持明文，得 %q", got)
	}
}
