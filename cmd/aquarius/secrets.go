package main

// 本机密钥库在装配根的接线（D151/D161）：读链 + 写入口。
//
//   - 读：`secretChain` = **本机密钥库 → 同名环境变量**。命中即返回；两处都没有则报因
//     **点名查过哪两处**（绝不回显值）；库解不开时打一行警告后**回落同名环境变量**继续
//     用（把「profile 拷到新机」这个预期场景从硬故障变成可用启动，D161 落点②）。
//   - 写：设置窗 provider 编辑区 / 欢迎窗填的明文 → 写进库、config 只留 `secret:<名>`
//     （D161 落点①）；键名规则见 keyNameFor（D161 落点③）。

import (
	"context"
	"errors"
	"fmt"
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

// keyNameFor 库里键名规则（D161 落点③）：已有 `secret:<名>` 引用则沿用该名（改填明文
// 只在原地换值，引用不 churn）；否则按 provider 名生成 `AQUARIUS_<PROVIDER>_API_KEY`
// ——与「同名环境变量兜底」这条语义严丝合缝，多 provider 也不会互相覆盖同一个槽位。
func keyNameFor(providerName, origRef string) string {
	if ref := strings.TrimSpace(origRef); strings.HasPrefix(ref, "secret:") {
		if n, err := secretName(ref); err == nil {
			return n
		}
	}
	return deriveKeyName(providerName)
}

// deriveKeyName provider 名 → 环境变量味的键名：转大写、非 [A-Z0-9] 折成下划线、
// 连续下划线压成一个、首尾下划线去掉。
func deriveKeyName(providerName string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToUpper(strings.TrimSpace(providerName)) {
		switch {
		case r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		name = "PROVIDER"
	}
	return "AQUARIUS_" + name + "_API_KEY"
}

// storePlaintextKey 界面填了明文密钥时的写入口径（D161 落点①）：明文写进本机密钥库，
// 返回写进 config 的 `secret:<名>` 引用；填的本来就是引用则原样保留、不动库。
// **写库失败即报错**——绝不静默退回明文（那等于把用户以为存住的东西留在盘上明文）。
func storePlaintextKey(ctx context.Context, bin *secretbin.Store, providerName, origRef, typed string) (string, error) {
	ref := strings.TrimSpace(typed)
	if strings.HasPrefix(ref, "secret:") {
		return ref, nil // 用户填的就是引用：照写，不进库
	}
	if bin == nil {
		return "", fmt.Errorf("provider %q：本机密钥库未接线，无法保存明文密钥", providerName)
	}
	name := keyNameFor(providerName, origRef)
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
