//go:build !windows

package i18n

import (
	"os"
	"strings"
)

// systemLanguage 按 POSIX 约定读 locale 环境变量（D275）：LC_ALL > LC_MESSAGES > LANG，
// 取语言段（`zh_CN.UTF-8` → `zh`）。认不出返回空串，由调用方回落 zh。
func systemLanguage() Lang {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if lang := langFromLocale(os.Getenv(name)); lang != "" {
			return lang
		}
	}
	return ""
}

// langFromLocale locale 串 → Lang：`zh*` 中文、`en*` 英文，其余（含 C / POSIX）返回空串。
// 只取语言段：`.` 之后是编码、`@` 之后是修饰（`zh_CN.UTF-8@cjk`），都不影响选哪种语言。
func langFromLocale(loc string) Lang {
	if i := strings.IndexAny(loc, "._@"); i >= 0 {
		loc = loc[:i]
	}
	switch strings.ToLower(strings.TrimSpace(loc)) {
	case "zh":
		return LangZh
	case "en":
		return LangEn
	}
	return ""
}
