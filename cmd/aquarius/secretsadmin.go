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
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// secretsAdmin uigui.SecretsAdmin 的装配根实现。
//
// 快照修订号存在装配根全局 wiring 上（wiring.secretsRev，同 toolsRev 规则）：写/删后
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
		return errors.New(i18n.T("cmd.secrets.err.bin_unwired_write"))
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
		return errors.New(i18n.T("cmd.secrets.err.bin_unwired_delete"))
	}
	err := a.w.secretsBin.Delete(context.Background(), name)
	if errors.Is(err, secretbin.ErrNotFound) {
		return errors.New(i18n.Tf("cmd.secrets.err.entry_missing", "name", strconv.Quote(name)))
	}
	if err != nil {
		return err
	}
	a.w.secretsRev.Add(1)
	return nil
}

// Import 一键导入（S9-6b 追加；D304 扩到全部会报明文警告的地方）：把 config 里仍是
// 明文的密钥收进本机密钥库、config 改写成 `secret:<名>` 引用；对**已经写成引用**的槽位，
// 再把「库里没有、只存在于同名环境变量里」的值也收进来。
//
// 三条规则：
//   - **库解不开即拒绝**（不与 secretbin.Set 的「从空表起步」同路）：Set 的单条语义是
//     「向导窗首次填密钥必须能写入磁盘」（D161 落点②），那是从零起步；批量导入落在同一个
//     坑里就变成**把已有条目整份丢掉**。故先探一次库，解不开就报因不动文件。
//   - **先写进密钥库、后改 config**（同 D161 写路径顺序）：任何一步失败都留「库里有值、config
//     还是明文」这个**可用**的中间态，而不是「config 已是引用、库里没有值」。
//   - **值永不回显**：返回值与错误串都只带键名与位置（§9）。
//
// 空 api_key 与 resolveAPIKey 同规则回落默认引用 `secret:DEEPSEEK_API_KEY`——
// 不一致的话，「启动用环境变量、导入却跳过」会让用户以为导入漏了。这条只对 provider
// 生效：语音、决策、联网的空值是「借别处 / 还没配」，把它们补成库里一条新条目不是导入。
func (a *secretsAdmin) Import() (uigui.SecretsImportResult, error) {
	var res uigui.SecretsImportResult
	if a.w.secretsBin == nil {
		return res, errors.New(i18n.T("cmd.secrets.err.bin_unwired_import"))
	}
	ctx := context.Background()
	cfg, err := loadConfig(a.w.cfgPath)
	if err != nil {
		return res, fmt.Errorf("%s: %w", i18n.T("cmd.secrets.err.read_config"), err)
	}
	// 库解不开就拒绝：Set 会从空表起步，批量导入等于丢掉库里已有条目。
	if _, err := a.w.secretsBin.Entries(ctx); err != nil {
		if errors.Is(err, secretbin.ErrUnreadable) {
			return res, errors.New(i18n.Tf("cmd.secrets.err.store_locked",
				"path", a.w.secretsBin.Path()))
		}
		return res, err
	}

	var rewrites []importRewrite  // 明文槽位：库里写完、config 改成引用
	imported := map[string]bool{} // 已收进库的键名（多处引用同一把钥只处理一次）
	for _, t := range importTargets(cfg) {
		raw := strings.TrimSpace(t.ref)
		if raw == "" {
			raw = t.fallback
		}
		if raw == "" {
			continue // 空 = 借别处或还没配，不是可收的存量
		}
		if !strings.HasPrefix(raw, "secret:") {
			// 明文：写进库（键名按服务名与钥匙位派生），并登记 config 改写。
			name := keySlotName(t.service, "", t.slot)
			if imported[name] {
				res.Failed = append(res.Failed, i18n.Tf("cmd.secrets.import.dup_skipped",
					"where", t.where, "name", name))
				continue
			}
			if err := a.w.secretsBin.Set(ctx, name, raw); err != nil {
				res.Failed = append(res.Failed, i18n.Tf("cmd.secrets.import.at_store_failed",
					"where", t.where, "err", err))
				continue
			}
			imported[name] = true
			rewrites = append(rewrites, importRewrite{path: t.path, ref: keyStoreURI(name)})
			continue
		}
		name, nerr := secretName(raw)
		if nerr != nil {
			res.Failed = append(res.Failed, i18n.Tf("cmd.secrets.import.at_ref_invalid",
				"where", t.where, "err", nerr))
			continue
		}
		if imported[name] {
			continue // 同名引用已处理过（含多处共用一把钥的情形）
		}
		// 库里已有这条：无需搬运（也不覆盖——库里那份就是用户要用的）。
		if _, gerr := a.w.secretsBin.Get(ctx, name); gerr == nil {
			continue
		} else if !errors.Is(gerr, secretbin.ErrNotFound) {
			res.Failed = append(res.Failed, i18n.Tf("cmd.secrets.import.read_failed", "name", name, "err", gerr))
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
			res.Failed = append(res.Failed, i18n.Tf("cmd.secrets.import.store_failed",
				"name", strconv.Quote(name), "err", err))
			continue
		}
		imported[name] = true
	}
	// 明文那部分改 config：只在确有改写时动文件（无改写就一个字节都不碰）。
	if len(rewrites) > 0 {
		lost := 0 // 路径对不上（文件在读与写之间被改过）：那个槽位没改成
		if err := persistConfig(a.w.cfgPath, func(generic map[string]any) {
			for _, rw := range rewrites {
				if !setConfigPath(generic, rw.path, rw.ref) {
					lost++
				}
			}
		}); err != nil {
			// 值已在库里（读取链优先取库），只是 config 还留着明文——如实报出，别假装成功。
			res.Failed = append(res.Failed, i18n.Tf("cmd.secrets.import.rewrite_failed", "err", err))
		} else {
			if lost > 0 {
				res.Failed = append(res.Failed, i18n.Tf("cmd.secrets.import.path_lost", "n", lost))
			}
			for _, rw := range rewrites {
				res.Rewritten = append(res.Rewritten, strings.TrimPrefix(rw.ref, "secret:"))
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

// importRewrite 一处明文槽位的改写登记：写库成功后按 path 把值换成 `secret:<名>`。
type importRewrite struct {
	path []any  // config 里的位置（generic map 的键与数组下标）
	ref  string // 写进去的 `secret:<名>`
}

// importTarget 一个可能存着明文的密钥槽位。
type importTarget struct {
	where    string // 人话位置（报错与回执用；不含值）
	service  string // 派生键名用的服务名（keySlotName 的第一个参数）
	slot     int    // 密钥池下标（provider 的多把钥匙用；其余恒 0）
	ref      string // 当前值（明文 / `secret:<名>` / 空）
	fallback string // 空值时的缺省引用（只有 provider 有：与 resolveAPIKey 同一套规则）
	path     []any  // config 里的位置
}

// importTargets 收集全部槽位：provider 密钥池、语音两路、决策模型、联网两组提供商。
//
// 名单与「会在启动期报明文警告」的位置一一对应（warnPlaintext 的全部调用点）：漏一处，
// 「导入完就不报警告了」这句话就不成立；多收一处没人读的字段，只是白改一行配置。
func importTargets(cfg *fileConfig) []importTarget {
	var out []importTarget
	for pi := range cfg.Model.Providers {
		p := &cfg.Model.Providers[pi]
		for i, ref := range p.keyRefs() {
			out = append(out, importTarget{
				where:    i18n.Tf("cmd.secrets.import.where.provider", "provider", strconv.Quote(p.Name), "slot", i+1),
				service:  p.Name,
				slot:     i,
				ref:      ref,
				fallback: defaultSecretRef,
				path:     []any{"model", "providers", pi, "keys", i, "api_key"},
			})
		}
	}
	if k := strings.TrimSpace(cfg.Audio.ASR.APIKey); k != "" {
		out = append(out, importTarget{
			where: i18n.T("cmd.secrets.import.where.asr"), service: "asr",
			ref: k, path: []any{"audio", "asr", "api_key"},
		})
	}
	if k := strings.TrimSpace(cfg.Audio.TTS.APIKey); k != "" {
		out = append(out, importTarget{
			where: i18n.T("cmd.secrets.import.where.tts"), service: "tts",
			ref: k, path: []any{"audio", "tts", "api_key"},
		})
	}
	if k := strings.TrimSpace(cfg.Decide.APIKey); k != "" {
		out = append(out, importTarget{
			where: i18n.T("cmd.secrets.import.where.decide"), service: "decide",
			ref: k, path: []any{"decide", "api_key"},
		})
	}
	out = append(out, webImportTargets(cfg.Web.Search.Providers, webSideSearch, "search")...)
	out = append(out, webImportTargets(cfg.Web.Fetch.Providers, webSideFetch, "fetch")...)
	return out
}

// webImportTargets 一侧联网提供商的密钥槽位（local 这类不带凭据的 kind 与空 key 跳过）。
func webImportTargets(ps []webProvider, side webSide, key string) []importTarget {
	var out []importTarget
	for i := range ps {
		service, ok := webKeyService(ps[i].Kind, side)
		if !ok || strings.TrimSpace(ps[i].Key) == "" {
			continue
		}
		out = append(out, importTarget{
			where: i18n.Tf("cmd.secrets.import.where.web", "side", key, "kind", ps[i].Kind),
			// service 与界面填明文时的派生规则同一套（webKeyService）：同一个 kind 不会
			// 因为「从哪个入口进的库」而占两个名字。
			service: service,
			ref:     ps[i].Key,
			path:    []any{"web", key, "providers", i, "key"},
		})
	}
	return out
}

// setConfigPath 按路径把值写进 generic map，返回是否写成功。
//
// 只走**已经存在**的容器：能读到明文就说明这条路径在文件里存在，写不进去只可能是文件在
// 读与写之间被改过（另一个进程）。那种情况下宁可报「没改完」，也不在配置里凭空造一段。
func setConfigPath(root map[string]any, path []any, value string) bool {
	var cur any = root
	for i, seg := range path {
		last := i == len(path)-1
		switch k := seg.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				return false
			}
			if last {
				m[k] = value
				return true
			}
			cur = m[k]
		case int:
			a, ok := cur.([]any)
			if !ok || k < 0 || k >= len(a) || last {
				return false
			}
			cur = a[k]
		default:
			return false
		}
	}
	return false
}
