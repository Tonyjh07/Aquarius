package secretbin

// 密钥库契约测试（D151/D161）：对**每个可用密文档**跑同一套断言（往返、覆盖、删除、
// 不存在、篡改、掩码、备份、键名与值不落明文）。Windows 上 DPAPI 是平台档、AES 是
// 可覆盖档，两档都跑；非 Windows 只跑 AES（DPAPI 文件带 windows build tag，编不进来）。

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ciphersUnderTest 本平台要覆盖的密文档（DPAPI 只在 Windows 可用）。
func ciphersUnderTest() []struct {
	name string
	set  func() string
} {
	out := []struct {
		name string
		set  func() string
	}{{"aesgcm", func() string { return cipherAESGCM }}}
	if runtime.GOOS == "windows" {
		out = append(out, struct {
			name string
			set  func() string
		}{"dpapi", func() string { return cipherDPAPI }})
	}
	return out
}

// withCipher 临时切换密文档并还原（Store 每次写盘现取 cipherFor，故切换即时生效）。
func withCipher(t *testing.T, set func() string) {
	t.Helper()
	prev := cipherFor
	cipherFor = set
	t.Cleanup(func() { cipherFor = prev })
}

// newStore 临时目录里的空库。
func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestStoreContract(t *testing.T) {
	for _, c := range ciphersUnderTest() {
		t.Run(c.name, func(t *testing.T) {
			withCipher(t, c.set)
			ctx := context.Background()
			s := newStore(t)

			// 空库：取不存在的名字 = ErrNotFound（**不是**解不开——调用方据此继续查环境变量）。
			if _, err := s.Get(ctx, "NOPE"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("空库取不存在的名字应 ErrNotFound，得 %v", err)
			}
			if _, err := s.Entries(ctx); err != nil {
				t.Fatalf("空库列举: %v", err)
			}

			// 写入 + 读回 + 列举。
			if err := s.Set(ctx, "MY_TEST_KEY", "sk-live-DEADBEEF"); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := s.Get(ctx, "MY_TEST_KEY")
			if err != nil || got != "sk-live-DEADBEEF" {
				t.Fatalf("Get = (%q, %v)，want 原值", got, err)
			}
			ents, err := s.Entries(ctx)
			if err != nil || len(ents) != 1 || ents[0].Name != "MY_TEST_KEY" {
				t.Fatalf("Entries = %+v (%v)", ents, err)
			}

			// 覆盖：同名字再写 = 换值，不新增条目。
			if err := s.Set(ctx, "MY_TEST_KEY", "sk-second-value-x"); err != nil {
				t.Fatalf("覆盖 Set: %v", err)
			}
			if got, _ := s.Get(ctx, "MY_TEST_KEY"); got != "sk-second-value-x" {
				t.Fatalf("覆盖后 = %q", got)
			}
			if ents, _ := s.Entries(ctx); len(ents) != 1 {
				t.Fatalf("覆盖不应新增条目: %+v", ents)
			}
			// 换代留了 .bak（同 storejson 口径）。
			if _, err := os.Stat(s.Path() + ".bak"); err != nil {
				t.Fatalf("覆盖后应有一代 .bak: %v", err)
			}

			// 第二条 + 排序。
			if err := s.Set(ctx, "ANOTHER_ONE", "another-12345678"); err != nil {
				t.Fatalf("Set 第二条: %v", err)
			}
			if ents, _ := s.Entries(ctx); len(ents) != 2 || ents[0].Name != "ANOTHER_ONE" {
				t.Fatalf("列举应按名排序: %+v", ents)
			}

			// 删除；再删 = ErrNotFound（不静默成功）。
			if err := s.Delete(ctx, "ANOTHER_ONE"); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if _, err := s.Get(ctx, "ANOTHER_ONE"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("删除后应 ErrNotFound，得 %v", err)
			}
			if err := s.Delete(ctx, "ANOTHER_ONE"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("重复删除应 ErrNotFound，得 %v", err)
			}
			// 删到空表：库文件仍在（重置场景下不应把文件凭空消掉）。
			if err := s.Delete(ctx, "MY_TEST_KEY"); err != nil {
				t.Fatalf("清空最后一条: %v", err)
			}
			if ents, _ := s.Entries(ctx); len(ents) != 0 {
				t.Fatalf("清空后应无条目: %+v", ents)
			}

			// 跨实例读（同目录新开一个 Store）：密文落在盘上，不是内存态。
			s2, err := New(filepath.Dir(s.Path()))
			if err != nil {
				t.Fatalf("重开 Store: %v", err)
			}
			if err := s2.Set(ctx, "REOPEN_KEY", "reopen-value-99"); err != nil {
				t.Fatalf("重开 Set: %v", err)
			}
			if got, err := s.Get(ctx, "REOPEN_KEY"); err != nil || got != "reopen-value-99" {
				t.Fatalf("旧实例读到新值 = (%q, %v)", got, err)
			}
		})
	}
}

// TestFileKeepsNoPlaintext 文件字节里搜不到键名与值：连键名都在密文内（D161 格式约定）。
func TestFileKeepsNoPlaintext(t *testing.T) {
	for _, c := range ciphersUnderTest() {
		t.Run(c.name, func(t *testing.T) {
			withCipher(t, c.set)
			ctx := context.Background()
			s := newStore(t)
			const (
				name  = "LEAKCANARY_NAME"
				value = "sk-leakcanary-VALUE"
			)
			if err := s.Set(ctx, name, value); err != nil {
				t.Fatalf("Set: %v", err)
			}
			data, err := os.ReadFile(s.Path())
			if err != nil {
				t.Fatalf("读库文件: %v", err)
			}
			if strings.Contains(string(data), name) {
				t.Fatalf("库文件里出现了键名（应全在密文内）")
			}
			if strings.Contains(string(data), value) {
				t.Fatalf("库文件里出现了密钥值")
			}
			// 密钥文件（AES 档）同样不该含明文——它是一串随机字节。
			if key, err := os.ReadFile(s.keyPath); err == nil && strings.Contains(string(key), value) {
				t.Fatalf("密钥文件里出现了密钥值")
			}
		})
	}
}

// TestUnreadableFallsBackToBak 主文件坏 → 用一代 .bak；两代都坏 → ErrUnreadable。
func TestUnreadableFallsBackToBak(t *testing.T) {
	for _, c := range ciphersUnderTest() {
		t.Run(c.name, func(t *testing.T) {
			withCipher(t, c.set)
			ctx := context.Background()
			s := newStore(t)
			if err := s.Set(ctx, "K1", "first-value-11"); err != nil {
				t.Fatalf("Set: %v", err)
			}
			if err := s.Set(ctx, "K1", "second-value-22"); err != nil { // 这一代写前把第一代留成 .bak
				t.Fatalf("覆盖 Set: %v", err)
			}
			// 主文件截断（模拟写坏/被改动）。
			if err := os.WriteFile(s.Path(), []byte("not json at all"), 0o600); err != nil {
				t.Fatalf("写坏主文件: %v", err)
			}
			if got, err := s.Get(ctx, "K1"); err != nil || got != "first-value-11" {
				t.Fatalf("应回落到 .bak 第一代值 = (%q, %v)", got, err)
			}
			// 两代都坏 → ErrUnreadable（调用方据此警告 + 回落环境变量）。
			if err := os.WriteFile(s.Path()+".bak", []byte("also broken"), 0o600); err != nil {
				t.Fatalf("写坏 .bak: %v", err)
			}
			if _, err := s.Get(ctx, "K1"); !errors.Is(err, ErrUnreadable) {
				t.Fatalf("两代都坏应 ErrUnreadable，得 %v", err)
			}
		})
	}
}

// TestTamperedCiphertextRejected 密文被改动 → 校验失败（ErrUnreadable），绝不返回垃圾。
func TestTamperedCiphertextRejected(t *testing.T) {
	for _, c := range ciphersUnderTest() {
		t.Run(c.name, func(t *testing.T) {
			withCipher(t, c.set)
			ctx := context.Background()
			s := newStore(t)
			if err := s.Set(ctx, "K1", "value-to-tamper-1"); err != nil {
				t.Fatalf("Set: %v", err)
			}
			data, err := os.ReadFile(s.Path())
			if err != nil {
				t.Fatalf("读库文件: %v", err)
			}
			// 改一位载荷字符（不动 JSON 结构，只坏密文本身）。
			var env envelope
			if err := json.Unmarshal(data, &env); err != nil {
				t.Fatalf("解信封: %v", err)
			}
			payload := []byte(env.Payload)
			if len(payload) < 4 {
				t.Fatalf("载荷过短，无法构造篡改样本")
			}
			if payload[0] == 'A' {
				payload[0] = 'B'
			} else {
				payload[0] = 'A'
			}
			env.Payload = string(payload)
			tampered, err := json.Marshal(env)
			if err != nil {
				t.Fatalf("重组信封: %v", err)
			}
			if err := os.WriteFile(s.Path(), tampered, 0o600); err != nil {
				t.Fatalf("写回篡改文件: %v", err)
			}
			if _, err := s.Get(ctx, "K1"); !errors.Is(err, ErrUnreadable) {
				t.Fatalf("篡改应 ErrUnreadable，得 %v", err)
			}
			_ = fs.ErrNotExist // 保持 fs 导入（与 os.ErrNotExist 同义，读路径口径提示）
		})
	}
}

// TestUnreadableStoreIsWritable 解不开的库**仍能写入**（换机后首次填密钥必须落得下）：
// 从空表起步，旧密文整份留给 .bak（D161 落点②）。
func TestUnreadableStoreIsWritable(t *testing.T) {
	for _, c := range ciphersUnderTest() {
		t.Run(c.name, func(t *testing.T) {
			withCipher(t, c.set)
			ctx := context.Background()
			s := newStore(t)
			const broken = "opaque-broken-blob"
			if err := os.WriteFile(s.Path(), []byte(broken), 0o600); err != nil {
				t.Fatalf("埋坏文件: %v", err)
			}
			if err := s.Set(ctx, "NEW_KEY", "fresh-value-777"); err != nil {
				t.Fatalf("解不开的库应仍可写入: %v", err)
			}
			if got, err := s.Get(ctx, "NEW_KEY"); err != nil || got != "fresh-value-777" {
				t.Fatalf("写入后读回 = (%q, %v)", got, err)
			}
			bak, err := os.ReadFile(s.Path() + ".bak")
			if err != nil || string(bak) != broken {
				t.Fatalf("旧密文应整份留在 .bak（%q, %v）", string(bak), err)
			}
		})
	}
}

// TestAESMissingKeyFile 解不开：AES 档缺 secrets.key（密文被单独拷走/同步的场景）。
func TestAESMissingKeyFile(t *testing.T) {
	withCipher(t, func() string { return cipherAESGCM })
	ctx := context.Background()
	s := newStore(t)
	if err := s.Set(ctx, "K1", "value-needs-key-1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := os.Remove(s.keyPath); err != nil {
		t.Fatalf("移除密钥文件: %v", err)
	}
	if _, err := s.Get(ctx, "K1"); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("缺密钥文件应 ErrUnreadable，得 %v", err)
	}
	// 且**不得**顺手现生成一把新钥匙（那只会把「解不开」变成「解出垃圾」）。
	if _, err := os.Stat(s.keyPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("解不开时不应现生成密钥文件")
	}
}

// TestAESKeyFilePermission 新建的 secrets.key 是 0600（同目录密文才谈得上「挡离线备份」）。
func TestAESKeyFilePermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上权限位由 ACL 决定，0600 不具此语义")
	}
	withCipher(t, func() string { return cipherAESGCM })
	ctx := context.Background()
	s := newStore(t)
	if err := s.Set(ctx, "K1", "value-perm-1234"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	fi, err := os.Stat(s.keyPath)
	if err != nil {
		t.Fatalf("stat 密钥文件: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("密钥文件权限 = %o，want 600", perm)
	}
}

// TestNameAndValueValidation 写入口径：非法名字/空值被拦（不落盘、不静默）。
func TestNameAndValueValidation(t *testing.T) {
	withCipher(t, func() string { return cipherAESGCM })
	ctx := context.Background()
	s := newStore(t)
	for _, bad := range []string{"", " ", " has space", "has:colon", "trail "} {
		if err := s.Set(ctx, bad, "v-12345678"); err == nil {
			t.Fatalf("非法名字 %q 应被拒", bad)
		}
	}
	if err := s.Set(ctx, "OK_NAME", "   "); err == nil {
		t.Fatalf("空值应被拒（清条目走 Delete）")
	}
	if err := s.Set(ctx, "OK_NAME", "v-12345678"); err != nil {
		t.Fatalf("合法写入: %v", err)
	}
}

// TestMask 掩码：够长留尾 4 位，短值整条打码（不泄可用密钥）。
func TestMask(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "••••••"},
		{"abc", "••••••"},
		{"1234567", "••••••"}, // 7 位：留尾 4 位等于漏掉一半以上，仍整条打码
		{"12345678", "••••••5678"},
		{"sk-live-DEADBEEF", "••••••BEEF"},
	}
	for _, c := range cases {
		if got := maskOf(c.in); got != c.want {
			t.Errorf("maskOf(%q) = %q，want %q", c.in, got, c.want)
		}
	}
	// 掩码本身不得含全值（回归：尾 4 位以外的字符一律不出现）。
	for _, v := range []string{"sk-live-DEADBEEF", "0123456789"} {
		m := maskOf(v)
		if strings.Contains(m, v[:len(v)-4]) {
			t.Errorf("掩码泄漏了尾 4 位以外的内容: %q", m)
		}
	}
}

// TestNewRejectsBadDir 目录不存在即报因（调用方负责 MkdirAll，同 atomicfile 口径）。
func TestNewRejectsBadDir(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "no-such-dir")); err == nil {
		t.Fatalf("不存在的目录应报错")
	}
	if _, err := New("  "); err == nil {
		t.Fatalf("空目录名应报错")
	}
}
