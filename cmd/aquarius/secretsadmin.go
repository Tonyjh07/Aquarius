package main

// 密钥库管理面装配根侧（S9-6b/D161）：实现 uigui.SecretsAdmin（消费方定义接口）——
// 设置窗「密钥库」页的列出 / 新增覆盖 / 删除。动作由设置窗**同步**调用（本地小文件
// IO，微秒级；工具页那套旁路 goroutine + 结果通道是给 15s 拨号准备的可不必套用），
// 本文件方法只做「改库文件 → 递增 Revision」。库本体 secretsBin 由 bootstrap 填
//（profileDir 一就位就有，设置窗打开时必已就绪）。

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// secretsAdmin uigui.SecretsAdmin 的装配根实现。
//
// 快照修订号存在装配根全局 wiring 上（wiring.secretsRev，同 toolsRev 口径）：写/删后
// 递增，设置窗据此重建条目行；以后若出现别的写入路径，也一起递增同一计数器。
type secretsAdmin struct {
	w *wiring
}

var _ uigui.SecretsAdmin = (*secretsAdmin)(nil)

// Snapshot 原子快照：库条目（按名字排序，只给名字 + 尾 4 位掩码）+ 库文件路径。
// 库解不开（换机拷来 / 未接线）时不报错——返回空条目 + 路径，页面据此显示空态；
// 真正的读错误只影响取密钥链（那里警告 + 环境兜底，D161 落点②）。
func (a *secretsAdmin) Snapshot() uigui.SecretsSnapshot {
	s := uigui.SecretsSnapshot{Revision: a.w.secretsRev.Load()}
	if a.w.secretsBin == nil {
		return s
	}
	s.Path = a.w.secretsBin.Path()
	entries, err := a.w.secretsBin.Entries(context.Background())
	if err != nil {
		return s // 解不开：空条目（不回显错误，免得与「空库」混淆）
	}
	for _, e := range entries {
		s.Entries = append(s.Entries, uigui.SecretEntry{Name: e.Name, Mask: e.Mask})
	}
	return s
}

// Put 写入/覆盖一条密钥；值永不回显，同名即覆盖。写成功后递增 Revision。
func (a *secretsAdmin) Put(name, value string) error {
	if a.w.secretsBin == nil {
		return errors.New("本机密钥库未接线，无法写入")
	}
	if err := a.w.secretsBin.Set(context.Background(), name, value); err != nil {
		return err
	}
	a.w.secretsRev.Add(1)
	return nil
}

// Delete 删除一条；名字不存在报错（不静默成功——UI 据此报「已不在」）。
func (a *secretsAdmin) Delete(name string) error {
	if a.w.secretsBin == nil {
		return errors.New("本机密钥库未接线，无法删除")
	}
	err := a.w.secretsBin.Delete(context.Background(), name)
	if errors.Is(err, secretbin.ErrNotFound) {
		return fmt.Errorf("密钥库中已没有 %q（可能已被删除）", name)
	}
	if err != nil {
		return err
	}
	a.w.secretsRev.Add(1)
	return nil
}

// Import 一键导入（S9-6b 追加）：把 config 里仍是明文的 provider 密钥，以及
// 「库里没有、只存在于同名环境变量里」的引用值，一并收进本机密钥库；明文那部分
// 同时把 config 改写成 `secret:<名>` 引用。
//
// 三条口径：
//   - **库解不开即拒绝**（不与 secretbin.Set 的「从空表起步」同路）：Set 的单条语义是
//     「向导窗首次填密钥必须能落盘」（D161 落点②），那是从零起步；批量导入落在同一个
//     坑里就变成**把已有条目整份丢掉**。故先探一次库，解不开就报因不动文件。
//   - **先落库、后改 config**（同 D161 写路径顺序）：任何一步失败都留「库里有值、config
//     还是明文」这个**可用**的中间态，而不是「config 已是引用、库里没有值」。
//   - **值永不回显**：返回值与错误串都只带键名与 provider 名（§9）。
//
// 空 api_key 与 resolveAPIKey 同口径回落默认引用 `secret:AQUARIUS_OPENAI_KEY`——
// 不一致的话，「启动用环境变量、导入却跳过」会让用户以为导入漏了。
func (a *secretsAdmin) Import() (uigui.SecretsImportResult, error) {
	var res uigui.SecretsImportResult
	if a.w.secretsBin == nil {
		return res, errors.New("本机密钥库未接线，无法导入")
	}
	ctx := context.Background()
	cfg, err := loadConfig(a.w.cfgPath)
	if err != nil {
		return res, fmt.Errorf("读取配置失败: %w", err)
	}
	// 库解不开就拒绝：Set 会从空表起步，批量导入等于丢掉库里已有条目。
	if _, err := a.w.secretsBin.Entries(ctx); err != nil {
		if errors.Is(err, secretbin.ErrUnreadable) {
			return res, fmt.Errorf("本机密钥库解不开（原文件未被改写，已留一份 .bak）；"+
				"导入会从空表起步、把库里已有条目丢掉，故本次不做。请先修复 %s", a.w.secretsBin.Path())
		}
		return res, err
	}

	rewrite := map[string]string{} // provider 名 → 改写后的 `secret:<名>`
	imported := map[string]bool{}  // 已导入的键名（去重：多条 provider 可共享同一引用名）
	for _, p := range cfg.Model.Providers {
		raw := strings.TrimSpace(p.APIKey)
		if raw == "" {
			raw = defaultSecretRef // 与 resolveAPIKey 同口径回落
		}
		if !strings.HasPrefix(raw, "secret:") {
			// 明文：写进库（键名按 provider 名派生），并登记 config 改写。
			name := deriveKeyName(p.Name)
			if err := a.w.secretsBin.Set(ctx, name, raw); err != nil {
				res.Failed = append(res.Failed, fmt.Sprintf("provider %q 写入密钥库失败：%v", p.Name, err))
				continue
			}
			imported[name] = true
			rewrite[p.Name] = name
			continue
		}
		name, nerr := secretName(raw)
		if nerr != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("provider %q 的密钥引用非法：%v", p.Name, nerr))
			continue
		}
		if imported[name] {
			continue // 同名引用已处理过（含多处 provider 共用一把钥的情形）
		}
		// 库里已有这条：无需搬运（也不覆盖——库里那份就是用户要用的）。
		if _, gerr := a.w.secretsBin.Get(ctx, name); gerr == nil {
			continue
		} else if !errors.Is(gerr, secretbin.ErrNotFound) {
			res.Failed = append(res.Failed, fmt.Sprintf("读取密钥库失败（%s）：%v", name, gerr))
			continue
		}
		// 只读环境变量这一半：库这一半已在上面确认没有这条。直接走 envSecrets（而非
		// 整条读链）是为了让「库里没有、环境里有」这个判据与上面那次 Get 互不干扰。
		v, eerr := envSecrets{}.Get(ctx, name)
		if eerr != nil {
			res.Skipped = append(res.Skipped, name) // 两处都没有：没得可收，不是错误
			continue
		}
		if err := a.w.secretsBin.Set(ctx, name, v); err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("密钥 %q 写入密钥库失败：%v", name, err))
			continue
		}
		imported[name] = true
	}
	// 明文那部分改 config：只在确有改写时动文件（无改写就一个字节都不碰）。
	if len(rewrite) > 0 {
		if err := persistConfig(a.w.cfgPath, func(generic map[string]any) {
			model := modelSection(generic)
			list, _ := model["providers"].([]any)
			for _, pv := range list {
				pm, ok := pv.(map[string]any)
				if !ok {
					continue
				}
				n, _ := pm["name"].(string)
				if slot, ok := rewrite[n]; ok {
					pm["api_key"] = keyStoreURI(slot)
				}
			}
		}); err != nil {
			// 值已在库里（读取链优先取库），只是 config 还留着明文——如实报出，别假装成功。
			res.Failed = append(res.Failed, fmt.Sprintf("配置里的明文未能改写为引用（密钥已入库，明文仍在 config.json，请重试）：%v", err))
		} else {
			for _, name := range rewrite {
				res.Rewritten = append(res.Rewritten, name)
			}
		}
	}
	for name := range imported {
		res.Imported = append(res.Imported, name)
	}
	sort.Strings(res.Imported)
	sort.Strings(res.Rewritten)
	sort.Strings(res.Skipped)
	if len(imported) > 0 {
		a.w.secretsRev.Add(1)
	}
	return res, nil
}
