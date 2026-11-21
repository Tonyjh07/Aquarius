package app

// session_cmd_search.go `/search`：跨会话搜索（D322）。
//
// 逐树扫、不建索引（决策否决全文索引：当前量级逐树扫即可）：标题与节点正文（text/doc
// 分片）作命中来源，思考分片不搜、工具分片不作命中来源——与 nodeSummary 的正文规则同一
// 套口径。命中行给会话 ID + 节点 ID，跳转走 `/switch <会话ID>`；上限 100 行防刷屏，单个
// 会话载入失败只计数继续。GUI 左栏的过滤框是另一件事（对已发布快照的客户端过滤），不走这里。

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// searchHitLimit 命中行上限（D322）：搜全库不能把回执写成另一堵墙，超出截断并记数。
const searchHitLimit = 100

// searchFlags /search 认识的 flag：--tag <名>（可重复，与 /list --tag 同一交集口径）、
// --regex（把查询当正则）。选项写在查询前后任意位置（flagsAnywhere）。
var searchFlags = []flagDecl{
	{name: "--tag", value: true},
	{name: "--regex"},
}

// searchMatcher 按旗标组出匹配函数：缺省子串、不分大小写；--regex 编成正则（不分大小写），
// 非法正则报用法错、不搜。
func searchMatcher(flags []cmdFlag, query string) (func(string) bool, error) {
	for _, f := range flags {
		if f.name == "--regex" {
			re, err := regexp.Compile("(?i)" + query)
			if err != nil {
				return nil, errors.New(i18n.T("usage.search"))
			}
			return re.MatchString, nil
		}
	}
	q := strings.ToLower(query)
	return func(s string) bool { return strings.Contains(strings.ToLower(s), q) }, nil
}

// searchNodeText 节点参与匹配的文本：正文（text/doc）分片拼起来；思考不搜、工具分片不搜
// （D322 否决项：工具输出是运行痕迹，正文命中已够定位）。
func searchNodeText(m conversation.Message) string {
	var parts []string
	for _, p := range m.Content {
		if p.Kind == conversation.PartText || p.Kind == conversation.PartDoc {
			if s := strings.TrimSpace(p.Text); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// execSearch 实现 /search：逐会话扫标题与正文，命中行 = 会话 ID · 标题 · 节点 ID · 预览。
func (s *Session) execSearch(ctx context.Context, cmd port.Command) (string, error) {
	flags, rest, err := scanFlags(cmd.Args, flagScan{pos: flagsAnywhere}, searchFlags)
	if err != nil || len(rest) == 0 {
		return "", errors.New(i18n.T("usage.search"))
	}
	match, err := searchMatcher(flags, strings.Join(rest, " "))
	if err != nil {
		return "", err
	}
	sums, err := s.store.List(ctx)
	if err != nil {
		return "", wrapSession("err.list_conversations", err)
	}
	// --tag 的取值单独收（tagFilter 收全部 flag 的值，会把不带值的 --regex 收成空串）。
	var want []string
	for _, f := range flags {
		if f.name == "--tag" {
			want = append(want, f.value)
		}
	}
	if len(want) > 0 {
		filtered := sums[:0]
		for _, sm := range sums {
			if hasAllTags(sm.Tags, want) {
				filtered = append(filtered, sm)
			}
		}
		sums = filtered
	}
	var (
		b        strings.Builder
		hits     int
		failed   int
		more     int
		scanning = true
	)
	emit := func(line string) bool {
		if hits >= searchHitLimit {
			more++
			return false
		}
		b.WriteString(line + "\n")
		hits++
		return true
	}
	for _, sm := range sums {
		if !scanning {
			break
		}
		c := s.cur
		if sm.ID != s.cur.ID {
			var err error
			c, err = s.store.Load(ctx, sm.ID)
			if err != nil {
				failed++ // 单个会话坏了不中断：坏了多少行要说，剩下的照搜
				continue
			}
		}
		if match(sm.Title) {
			if scanning = emit(fmt.Sprintf("%s  %s", sm.ID, sm.Title)); !scanning {
				break
			}
		}
		for _, m := range c.Messages() {
			if m.Role == conversation.RoleRoot {
				continue
			}
			if match(searchNodeText(m)) {
				if scanning = emit(fmt.Sprintf("%s  %s  %s [%s] %s",
					sm.ID, sm.Title, m.ID, m.Role, nodeSummary(m))); !scanning {
					break
				}
			}
		}
	}
	if hits == 0 && failed == 0 {
		return i18n.T("tree.search.no_match"), nil
	}
	if more > 0 {
		fmt.Fprintf(&b, "%s\n", i18n.Tf("tree.search.truncated", "n", more))
	}
	if failed > 0 {
		fmt.Fprintf(&b, "%s\n", i18n.Tf("tree.search.failed", "n", failed))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
