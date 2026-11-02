package i18n

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// Lang 是界面语言。两种：zh 是母版，en 与它同 key（D275）。
type Lang string

const (
	LangZh Lang = "zh"
	LangEn Lang = "en"
)

// Selection 是 `ui.lang` 的取值：auto | zh | en。空串按 auto 认。
type Selection string

const (
	SelectionAuto Selection = "auto"
	SelectionZh   Selection = "zh"
	SelectionEn   Selection = "en"
)

// catalog 一张语言的表。语言在启动时定一次，之后整支进程只读。
type catalog struct {
	lang  Lang
	texts map[string]string
}

var (
	zhCatalog = &catalog{lang: LangZh, texts: zhTexts}
	enCatalog = &catalog{lang: LangEn, texts: enTexts}

	// current 当前生效的表。GUI 在渲染、设置、托盘几个 goroutine 里取词，所以走原子槽
	// 而不是裸变量——与 hotkeyCfg 同口径（D275：启动定一次，之后只读）。
	current atomic.Pointer[catalog]
)

// 缺省 zh：没选过语言（内部包的单测、库式调用）时按母版说话，系统语言只在 cmd 装配根
// 经 Use(SelectionAuto) 参与判断。
func init() { current.Store(zhCatalog) }

// Parse 校验 `ui.lang` 的写法：空与 auto 等价，其余只认 zh / en。非法值报错——与 ui.kind
// 同口径：等界面出来了才发现语言没生效，比启动失败难查得多。
func Parse(raw string) (Selection, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(SelectionAuto):
		return SelectionAuto, nil
	case string(SelectionZh):
		return SelectionZh, nil
	case string(SelectionEn):
		return SelectionEn, nil
	}
	return "", fmt.Errorf("ui.lang=%q 仅支持 auto | zh | en", raw)
}

// Use 选定界面语言并返回真正生效的那种：auto 读系统界面语言，认不出回落 zh（D275）。
//
// 启动时调一次，之后进程内不变——文案在构造期就散进了各视图与测量，热切换等于重建整个壳。
func Use(sel Selection) Lang {
	var lang Lang
	switch sel {
	case SelectionZh:
		lang = LangZh
	case SelectionEn:
		lang = LangEn
	default:
		if lang = systemLanguage(); lang == "" {
			lang = LangZh
		}
	}
	current.Store(catalogFor(lang))
	return lang
}

// catalogFor 按语言取表；未知语言给母版（宁可口径旧，也不给一张空表）。
func catalogFor(lang Lang) *catalog {
	if lang == LangEn {
		return enCatalog
	}
	return zhCatalog
}

// Current 当前生效的界面语言（诊断与测试用）。
func Current() Lang { return current.Load().lang }

// T 取词。缺 en 的 key 回落 zh（界面不会空着），但那条同时是缺陷，同构测试要红；两边
// 都没有就把 key 原样露出来——空串在界面里看不出哪里坏了，key 至少指得准。
func T(key string) string {
	c := current.Load()
	if s, ok := c.texts[key]; ok {
		return s
	}
	if s, ok := zhTexts[key]; ok {
		return s
	}
	return key
}

// Tf 取词并填占位符，参数按「名, 值」成对给：
//
//	i18n.Tf("boot.config_written", "path", p, "profile", name, "pointer", ptr)
//
// 具名而不是顺序位，因为英文语序常与中文相反，两个以上参数时顺序写反编译期查不出来。
// 缺参时占位符原样留在输出里：看得见比悄悄少一个字好。
func Tf(key string, kv ...any) string { return fill(T(key), kv) }

// fill 把 {name} 换成对应的值。只认成对给的字符串名字，其余花括号原样保留——模板里没有
// 别的花括号语法，所以这里也不做转义。
func fill(tmpl string, kv []any) string {
	if len(kv) == 0 || !strings.ContainsRune(tmpl, '{') {
		return tmpl
	}
	vals := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		if name, ok := kv[i].(string); ok && name != "" {
			vals[name] = kv[i+1]
		}
	}
	var b strings.Builder
	b.Grow(len(tmpl))
	for {
		open := strings.IndexByte(tmpl, '{')
		if open < 0 {
			b.WriteString(tmpl)
			return b.String()
		}
		width := strings.IndexByte(tmpl[open:], '}')
		if width < 0 {
			b.WriteString(tmpl)
			return b.String()
		}
		name := tmpl[open+1 : open+width]
		b.WriteString(tmpl[:open])
		if v, ok := vals[name]; ok {
			b.WriteString(fmt.Sprint(v))
		} else {
			b.WriteString(tmpl[open : open+width+1])
		}
		tmpl = tmpl[open+width+1:]
	}
}

// PlaceholderNames 列出模板里的 {name}（去重、升序）。
//
// 两份表的同构判据用它：key 集合相等之外，同一条文案的占位符名集合也必须相等——否则
// 切了语言就少一个值，而少值不会报错，只会印出半句话。
func PlaceholderNames(tmpl string) []string {
	var out []string
	seen := make(map[string]bool)
	for {
		open := strings.IndexByte(tmpl, '{')
		if open < 0 {
			break
		}
		width := strings.IndexByte(tmpl[open:], '}')
		if width < 0 {
			break
		}
		if name := tmpl[open+1 : open+width]; name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		tmpl = tmpl[open+width+1:]
	}
	sort.Strings(out)
	return out
}

// mergeTables 把按区域拆开的 zh_*.go / en_*.go 合成一张。
//
// 重复 key 直接 panic：后写的会静默盖掉先写的，而盖掉之后两处都还在仓里，谁也看不出哪条
// 才是真正在用的文案。启动即炸比界面上一句来路不明的话好查。
func mergeTables(tables ...map[string]string) map[string]string {
	n := 0
	for _, t := range tables {
		n += len(t)
	}
	out := make(map[string]string, n)
	for _, t := range tables {
		for k, v := range t {
			if _, dup := out[k]; dup {
				panic("i18n: 文案 key 重复: " + k)
			}
			out[k] = v
		}
	}
	return out
}

// Table 某一种语言的表（只读；测试与扫描用：同构检查要同时看两份）。
func Table(lang Lang) map[string]string { return catalogFor(lang).texts }
