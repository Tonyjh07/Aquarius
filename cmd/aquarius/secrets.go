package main

// 本机密钥库在装配根的接线（D151/D161）：读链 + 写入口。
//
//   - 读：`secretChain` = **本机密钥库 → 同名环境变量**。命中即返回；两处都没有则报因
//     **点名查过哪两处**（绝不回显值）；库解不开时打一行警告后**回落同名环境变量**继续
//     用（把「profile 拷到新机」这个预期场景从硬故障变成可用启动，D161 落点②）。
//   - 写：设置窗 provider 编辑区 / 欢迎窗填的明文 → 写进库、config 只留 `secret:<名>`
//     （D161 落点①）；键名规则见 keySlotName——库里每个键名的**唯一**产生处。

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"strings"
	"sync/atomic"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// secretChain port.Secrets 的装配实现：库优先、环境变量兜底。
type secretChain struct {
	bin   *secretbin.Store // nil = 本机库未接线（只剩环境变量这一档）
	env   port.Secrets     // 同名环境变量（envSecrets）
	warnf func(string, ...any)
	// warned 库解不开只警告一次：启动期每个 provider 都会探一次密钥，逐个都打会把
	// 一行警告刷成一屏（同一份坏文件，说一遍就够）。
	warned atomic.Bool
}

var _ port.Secrets = (*secretChain)(nil)

// Get 按名取用：先库、后同名环境变量；两处都没有时报因点名两处（不回显值）。
func (c *secretChain) Get(ctx context.Context, name string) (string, error) {
	if c.bin != nil {
		v, err := c.bin.Get(ctx, name)
		switch {
		case err == nil:
			return v, nil
		case errors.Is(err, secretbin.ErrNotFound):
			// 库里没有这条：继续查同名环境变量（正常路径，不是错误）。
		default:
			// 解不开/结构坏：警告一次，继续以环境变量兜底（不致命）。
			if c.warnf != nil && c.warned.CompareAndSwap(false, true) {
				c.warnf("警告：本机密钥库 %s 读不出（换机拷来、账户不同或文件损坏），已忽略；"+
					"本次启动只用同名环境变量取密钥。原文件仍在，未做任何改写。\n", c.bin.Path())
			}
		}
	}
	if c.env != nil {
		if v, err := c.env.Get(ctx, name); err == nil {
			return v, nil
		}
	}
	where := "同名环境变量"
	if c.bin != nil {
		where = "本机密钥库与同名环境变量"
	}
	return "", fmt.Errorf("密钥 %q 取不到：%s 都没有这个名字（库文件 %s）", name, where, c.binPathOr("-"))
}

// binPathOr 库路径展示（未接线时给占位）。
func (c *secretChain) binPathOr(fallback string) string {
	if c.bin == nil {
		return fallback
	}
	return c.bin.Path()
}

// newSecretChain 构造读链：库 + 环境变量兜底。bin 可为 nil（库未接线）。
func newSecretChain(bin *secretbin.Store, stderr io.Writer) *secretChain {
	return &secretChain{
		bin:   bin,
		env:   envSecrets{},
		warnf: func(format string, args ...any) { fmt.Fprintf(stderr, format, args...) },
	}
}

// keySlotName 密钥池第 i 把钥匙的库键名——**库里键名的唯一规则**（各写入路径都走它，
// 见下「两条」）。键名只决定「值存在哪一格」，与引用无关；config 里永远只写
// `secret:<名>`。
//
// 两条：
//   - **已经有引用就沿用**：该槽原本是 `secret:<名>` 就继续用这个名，重填明文只换值、
//     不改名。这条对**每一把**都成立——早先只有第一把这么做，第 N 把重填时会把已经带
//     `_N` 的引用再拼一次后缀（`CUSTOM_2` → `CUSTOM_2_2` → …），库里堆出一串没人引用的
//     旧条目。同理，provider 改名也不让引用 churn（按槽位下标读原引用）。
//   - **没有引用就派生**：`AQUARIUS_<PROVIDER>_API_KEY`，第 N 把追 `_<N>`（N = 下标+1）
//     ——同一 provider 的多账号各占独立槽位，且名字读得懂、能手写进 config。
func keySlotName(providerName, origRef string, i int) string {
	if n, ok := secretRefName(origRef); ok {
		return n
	}
	base := deriveKeyName(providerName)
	if i <= 0 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, i+1)
}

// secretRefName 从 `secret:<名>` 里取出名字；不是引用、或名字非法 → false（走派生）。
func secretRefName(ref string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(ref), "secret:") {
		return "", false
	}
	n, err := secretName(ref)
	if err != nil {
		return "", false
	}
	return n, true
}

// deriveKeyName provider 名 → 环境变量味的键名：转大写、非 [A-Z0-9] 折成下划线、
// 连续下划线压成一个、首尾下划线去掉。
//
// 纯 ASCII 名字（`deepseek`、`My-Provider.v2`）结果就是 `AQUARIUS_<名>_API_KEY`，与
// 「同名环境变量兜底」严丝合缝。**名字里出现 ASCII 之外的内容字符**（中文、全角、重音
// 字母）时，规范化会把它整段吃掉：`深度求索` 与 `月之暗面` 都变成
// `AQUARIUS_PROVIDER_API_KEY`，两家 provider 静默共用一个槽位、后写的覆盖先写的，
// 先那家从此拿错密钥（报 401 且看不出原因）。这类名字补 4 位指纹区分。
//
// 分隔符（空格 `-` `.` `_` `/` `\` `+` `:` `@` `,` `(` `)` `&` `'`）不算丢信息——它们
// 本来就只是分隔，`My-Provider.v2` 不该为此多一截指纹。
func deriveKeyName(providerName string) string {
	name := strings.TrimSpace(providerName)
	stem, lossy := normalizeStem(name)
	if stem == "" {
		stem = "PROVIDER"
	}
	out := "AQUARIUS_" + stem
	if lossy {
		out += "_" + nameFingerprint(name)
	}
	return out + "_API_KEY"
}

// normalizeStem 名字 → 大写下划线形态；lossy = 是否丢掉了分隔符之外的内容字符。
func normalizeStem(name string) (string, bool) {
	var b strings.Builder
	lossy, prevUnderscore := false, false
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		case isNameSeparator(r):
			if !prevUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevUnderscore = true
			}
		default:
			lossy = true // 中文/全角/重音字母：折成下划线就等于丢掉
			if !prevUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_"), lossy
}

// isNameSeparator 只是分隔作用的字符（去掉它们不丢信息）。
func isNameSeparator(r rune) bool {
	return strings.ContainsRune(" -_./\\:+@,()&'", r)
}

// nameFingerprint 名字的 4 位十六进制指纹（FNV-1a 取低 16 位）：同名恒同值，跨进程
// 稳定，所以键名可预测、可手写；不同名撞同一指纹的概率 1/65536，够用且名字仍读得懂。
func nameFingerprint(name string) string {
	h := fnv.New32a()
	_, _ = io.WriteString(h, name)
	return fmt.Sprintf("%04X", h.Sum32()&0xFFFF)
}

// storePlaintextKey 界面填了明文密钥时的写入口径（D161 落点①）：明文写进本机密钥库，
// 返回写进 config 的 `secret:<名>` 引用；填的本来就是引用则原样保留、不动库。
// **写库失败即报错**——绝不静默退回明文（那等于把用户以为存住的东西留在盘上明文）。
func storePlaintextKey(ctx context.Context, bin *secretbin.Store, providerName, origRef, typed string) (string, error) {
	return storePlaintextKeyAt(ctx, bin, providerName, origRef, typed, 0)
}

// storePlaintextKeyAt 密钥池第 slot 把的明文入库（D173）：键名一律由 keySlotName 定
// （有引用沿用、没有就派生）——重填明文只换值，引用名稳定，库里不会攒出孤儿条目。
func storePlaintextKeyAt(ctx context.Context, bin *secretbin.Store, providerName, origRef, typed string, slot int) (string, error) {
	ref := strings.TrimSpace(typed)
	if strings.HasPrefix(ref, "secret:") {
		return ref, nil // 用户填的就是引用：照写，不进库
	}
	if bin == nil {
		return "", fmt.Errorf("provider %q：本机密钥库未接线，无法保存明文密钥", providerName)
	}
	name := keySlotName(providerName, origRef, slot)
	if err := bin.Set(ctx, name, ref); err != nil {
		return "", fmt.Errorf("provider %q：写入本机密钥库失败: %w", providerName, err)
	}
	return "secret:" + name, nil
}

// keyStoreURI 库里键名 → 设置页展示用的引用写法（不含值）。
func keyStoreURI(name string) string { return "secret:" + name }

// secretBoundWarn 界面填的明文已改为入库 + 引用后，启动期明文警告的措辞更新（D161）：
// 不再推荐「改用 secret:<环境变量名> 引用环境变量」，而是指路设置窗（要么填明文由界面
// 转入库，要么直接填引用）。
func secretBoundWarn(stderr io.Writer, provider string) {
	fmt.Fprintf(stderr, "警告：provider %q 的 api_key 为明文——密钥直接落在 config.json，"+
		"本机任意可读进程、备份与同步都可能取用；到设置窗重填一次即转入本机加密密钥库"+
		"（config 里只留引用），也可手工改写为 \"secret:<名>\"。\n", provider)
}
