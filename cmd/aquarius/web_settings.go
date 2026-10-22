package main

// web_settings.go 设置窗「联网」页的装配根侧（D200）：config `web` 段与设置窗快照/
// 提交之间的换算与保存前校验。
//
// 口径与启动期 normalizeWeb 一致——那边是最后一道闸（写坏了启动就报错），这边把它
// 提前到落盘之前，用户看到的是「设置窗保存失败」，而不是下次启动才炸。密钥只写不回显：
// 快照只带形态（引用 / 明文），提交里留空 = 保持原引用。

import (
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// webProviderViewsOf config 里的提供商列表 → 设置窗视图（密钥只带形态，D35）。
func webProviderViewsOf(ps []webProvider) []uigui.WebProviderView {
	out := make([]uigui.WebProviderView, 0, len(ps))
	for _, p := range ps {
		v := uigui.WebProviderView{
			Kind:    p.Kind,
			From:    p.From,
			BaseURL: p.BaseURL,
			Country: p.Country,
			Lang:    p.Lang,
			Count:   p.Count,
		}
		key := strings.TrimSpace(p.Key)
		switch {
		case key == "":
			// 空 = 回落同名 secret（brave 的既有口径），界面按「留空」呈现。
		case strings.HasPrefix(key, "secret:"):
			v.KeySaved = true
			v.KeyRef = key
		default:
			v.KeyPlain = true
		}
		out = append(out, v)
	}
	return out
}

// validateWebSettings 联网配置保存前校验（D200）：与 normalizeWeb 同口径——未知 kind、
// 抓取侧写了只做搜索的 kind、deepseek 引用了不存在的对话 provider、数值越界。
// 密钥缺失不在这里拦（启动时警告并跳过该家，其余提供商照常可用）。
func validateWebSettings(cfgPath string, p uigui.SettingsPatch) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, pc := range cfg.Model.Providers {
		known[pc.Name] = true
	}
	for i, wp := range p.WebSearchProviders {
		kind := strings.TrimSpace(wp.Kind)
		switch kind {
		case webKindDeepSeek:
			from := strings.TrimSpace(wp.From)
			if from == "" {
				from = webKindDeepSeek
			}
			if !known[from] {
				return fmt.Errorf("搜索提供商第 %d 条借用的服务 %q 不在 provider 列表内"+
					"（先在「模型服务」页添加，或填一个已有的名字）", i+1, from)
			}
		case webKindBrave:
		default:
			return fmt.Errorf("搜索提供商第 %d 条的类型 %q 不认识（支持 %s / %s）",
				i+1, kind, webKindDeepSeek, webKindBrave)
		}
		if n := wp.Count; n < 0 || n > 20 {
			return fmt.Errorf("搜索提供商第 %d 条的条数 %d 超出范围（1–20；0 = 用缺省）", i+1, n)
		}
	}
	for i, wp := range p.WebFetchProviders {
		kind := strings.TrimSpace(wp.Kind)
		switch kind {
		case webKindDeepSeek:
			from := strings.TrimSpace(wp.From)
			if from == "" {
				from = webKindDeepSeek
			}
			if !known[from] {
				return fmt.Errorf("抓取提供商第 %d 条借用的服务 %q 不在 provider 列表内"+
					"（先在「模型服务」页添加，或填一个已有的名字）", i+1, from)
			}
		case webKindLocal:
		default:
			return fmt.Errorf("抓取提供商第 %d 条的类型 %q 不认识（支持 %s / %s）",
				i+1, kind, webKindDeepSeek, webKindLocal)
		}
	}
	if n := p.WebSearchCount; n < 0 || n > 20 {
		return fmt.Errorf("搜索条数 %d 超出范围（1–20；0 = 缺省 8）", n)
	}
	if n := p.WebFetchMaxChars; n != 0 && (n < 500 || n > 20000) {
		return fmt.Errorf("抓取正文上限 %d 超出范围（500–20000；0 = 缺省 16000）", n)
	}
	if n := p.WebFetchTimeoutSec; n < 0 || n > 600 {
		return fmt.Errorf("抓取超时 %d 超出范围（1–600 秒；0 = 缺省 30）", n)
	}
	return nil
}

// writeWebSection 把设置窗提交写进 config 的 `web` 段（泛键改写，未知键保留）。
// 提供商列表**恒全量写**（空数组 = 清空 = 该能力不可用）；列表项的未知键按**同类型**的
// 旧条目保留，`key` 留空 = 沿用旧引用（值永不回显，D35）。
//
// 判「这条写路径管不管联网」用**列表是否 nil**（与降级链同口径）：设置窗保存总会给
// 两个非 nil 列表（空表单 = 空切片），而窄写回（人格模板等）给的是零值 —— 后者不该
// 顺手把用户的联网配置抹平。同一侧的其他键也只在该侧列表非 nil 时写：只改搜索的
// patch 不该把抓取的代理与开关按零值覆盖。
func writeWebSection(generic map[string]any, p uigui.SettingsPatch) {
	if p.WebSearchProviders == nil && p.WebFetchProviders == nil {
		return
	}
	webSec, _ := generic["web"].(map[string]any)
	if webSec == nil {
		webSec = map[string]any{}
		generic["web"] = webSec
	}
	if p.WebSearchProviders != nil {
		searchSec, _ := webSec["search"].(map[string]any)
		if searchSec == nil {
			searchSec = map[string]any{}
			webSec["search"] = searchSec
		}
		searchSec["providers"] = webProviderEntries(searchSec["providers"], p.WebSearchProviders)
		if p.WebSearchCount > 0 {
			searchSec["count"] = p.WebSearchCount
		}
	}
	if p.WebFetchProviders != nil {
		fetchSec, _ := webSec["fetch"].(map[string]any)
		if fetchSec == nil {
			fetchSec = map[string]any{}
			webSec["fetch"] = fetchSec
		}
		fetchSec["providers"] = webProviderEntries(fetchSec["providers"], p.WebFetchProviders)
		if p.WebFetchMaxChars > 0 {
			fetchSec["max_chars"] = p.WebFetchMaxChars
		}
		fetchSec["proxy"] = p.WebFetchProxy
		fetchSec["user_agent"] = p.WebFetchUserAgent
		fetchSec["respect_robots"] = p.WebFetchRobots
		fetchSec["allow_private_ips"] = p.WebFetchPrivate
		if p.WebFetchTimeoutSec > 0 {
			fetchSec["timeout_sec"] = p.WebFetchTimeoutSec
		}
	}
}

// webProviderEntries 提供商列表写回：逐条以**同类型的旧条目**为底（未知键保留），再覆盖
// 本页管的键；空值一律省略（config 里不落空串，读侧「空 = 缺省」的语义才成立）。
//
// 旧条目按 kind 对齐而不是按下标：用户在前面插一条、或删掉一条，下标整体错位，硬套会把
// brave 的密钥引用贴到新插入的 deepseek 条目上（那条根本不用自己的密钥）。同类型多条时
// 按出现顺序依次认领，认领过的旧条目不再复用。改类型视为换了一家，旧条目的私有字段不再
// 跟着走。
func webProviderEntries(oldVal any, ps []uigui.WebProviderPatch) []any {
	old, _ := oldVal.([]any)
	used := make([]bool, len(old))
	out := make([]any, 0, len(ps))
	for _, p := range ps {
		entry := map[string]any{}
		kind := strings.TrimSpace(p.Kind)
		for i, raw := range old {
			if used[i] {
				continue
			}
			prev, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			prevKind, _ := prev["kind"].(string)
			if strings.TrimSpace(prevKind) != kind {
				continue
			}
			used[i] = true
			for k, v := range prev {
				entry[k] = v // 未知键保留（Q10 口径：用户手写的字段不丢）
			}
			break
		}
		setStr := func(key, val string) {
			if v := strings.TrimSpace(val); v != "" {
				entry[key] = v
			} else {
				delete(entry, key)
			}
		}
		setStr("kind", p.Kind)
		setStr("from", p.From)
		setStr("base_url", p.BaseURL)
		setStr("country", p.Country)
		setStr("lang", p.Lang)
		if p.Count > 0 {
			entry["count"] = p.Count
		} else {
			delete(entry, "count")
		}
		if k := strings.TrimSpace(p.Key); k != "" {
			entry["key"] = k // 留空 = 沿用旧条目里的 key（上面已拷进来）
		}
		out = append(out, entry)
	}
	return out
}
