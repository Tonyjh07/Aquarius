package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// grep.go `file_grep`：按内容搜文件（D301）。
//
// 为什么需要它：找「哪一行写了这个」现在只能 term_exec（Risk=Confirm，每次点一遍确认），
// 而这是最典型的只读动作。file_search 只按文件名找，替代不了。一个 Safe 的只读搜索把
// 每天几十次确认降到一个都没有。
//
// 三道上限是为了「不因一个巨大目录失控」：命中数、扫描文件数、单文件大小。都取保守值——
// 搜不全是可接受的（有提示），拖死一次对话不可接受。

const (
	// maxGrepHits 命中上限（每行一条，比 file_search 的 200 少：一行内容比一个路径长得多）。
	maxGrepHits = 100
	// maxGrepFiles 扫描文件数上限（命中为 0 也不许无限走）。
	maxGrepFiles = 5000
	// maxGrepFileBytes 单文件上限：更大的文件跳过并计数，不读进内存。
	maxGrepFileBytes = 1 << 20
	// maxGrepLineChars 单行展示长度：命中行只在「是不是这一行」上有用，长行截断。
	maxGrepLineChars = 240
	// maxGrepPatternChars 模式长度上限：固定串搜索里长模式没有额外价值，正则则不值得赌。
	maxGrepPatternChars = 512
)

// grepSkipDirs 默认跳过的目录名：版本库内部与依赖目录（搜它们几乎总是噪音）。
var grepSkipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
}

// fileGrep 按内容递归搜索（Risk=Safe：只读，不申报写目标）。
type fileGrep struct{ sandbox string }

func (t *fileGrep) Spec() tool.Spec {
	return tool.Spec{
		Name: "file_grep",
		Description: fmt.Sprintf("Search file contents under a directory and return matching lines as path:line:text. "+
			"query is a literal substring by default, or a regular expression with regex=true; ignore_case folds case. "+
			"Skips .git/.hg/.svn/node_modules, binary files and files larger than %s. "+
			"Stops at %d matches or %d files scanned and says which limit was hit. "+
			"Start from a narrow directory: a huge tree still costs time even with the caps. Never needs confirmation.",
			humanBytes(maxGrepFileBytes), maxGrepHits, maxGrepFiles),
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "absolute path of the directory to search"},
				"query": {"type": "string", "description": "literal substring, or a regular expression when regex is true"},
				"regex": {"type": "boolean", "description": "treat query as a regular expression (RE2 syntax)"},
				"ignore_case": {"type": "boolean", "description": "case-insensitive matching"}
			},
			"required": ["path", "query"]
		}`),
		Risk: tool.Safe,
	}
}

func (t *fileGrep) Target(_ context.Context, call tool.Call) (string, perm.Op, bool) {
	p, ok := targetPath(call, t.sandbox)
	if !ok {
		return "", 0, false
	}
	return p, perm.OpRead, true
}

func (t *fileGrep) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	var a struct {
		Path       string `json:"path"`
		Query      string `json:"query"`
		Regex      bool   `json:"regex"`
		IgnoreCase bool   `json:"ignore_case"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	root, err := requireAbs(t.sandbox, a.Path)
	if err != nil {
		return tool.Result{}, err
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return tool.Result{}, errors.New("missing query")
	}
	if len(query) > maxGrepPatternChars {
		return tool.Result{}, fmt.Errorf("query is longer than %d characters", maxGrepPatternChars)
	}
	match, err := newGrepMatcher(query, a.Regex, a.IgnoreCase)
	if err != nil {
		return tool.Result{}, err
	}

	var (
		hits, scanned, skippedLarge int
		out                         strings.Builder
		hitLimit                    bool
	)
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return nil // 无权限/悬空项跳过，不中断整体搜索
		}
		if d.IsDir() {
			if path != root && grepSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if scanned >= maxGrepFiles {
			return fs.SkipAll
		}
		scanned++
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.Size() > maxGrepFileBytes {
			skippedLarge++
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if isBinary(data) {
			return nil
		}
		hits += grepLines(&out, path, string(data), match, maxGrepHits-hits)
		if hits >= maxGrepHits {
			hitLimit = true
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return tool.Result{}, fmt.Errorf("grep: %w", walkErr)
	}

	if hits == 0 {
		if scanned >= maxGrepFiles {
			return okResult(fmt.Sprintf("no matches in the first %d files scanned (file limit reached)", maxGrepFiles)), nil
		}
		return okResult(emptyMatches), nil
	}
	msg := out.String()
	switch {
	case hitLimit:
		msg += fmt.Sprintf("\n...[hit the limit of %d matches]", maxGrepHits)
	case scanned >= maxGrepFiles:
		msg += fmt.Sprintf("\n...[stopped after %d files scanned]", maxGrepFiles)
	}
	if skippedLarge > 0 {
		msg += fmt.Sprintf("\n...[skipped %d file(s) larger than %s]", skippedLarge, humanBytes(maxGrepFileBytes))
	}
	return okResult(msg), nil
}

// grepLines 把一个文件的命中行写进 out，返回本文件的命中数。
//
// 行号从 1 起，与 file_read 的前缀规则一致（同一个人在两处读同一个文件，编号不该跳）。
func grepLines(out *strings.Builder, path, body string, match func(string) bool, room int) (found int) {
	for i, line := range strings.Split(body, "\n") {
		if room <= 0 {
			return found
		}
		line = strings.TrimSuffix(line, "\r")
		if !match(line) {
			continue
		}
		if found > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(path)
		out.WriteByte(':')
		out.WriteString(fmt.Sprint(i + 1))
		out.WriteByte(':')
		out.WriteString(clipLine(line))
		found++
		room--
	}
	return found
}

// clipLine 命中行的展示长度：超长行截断，但保留尾部提示（长行中间往往才有差别）。
func clipLine(line string) string {
	r := []rune(line)
	if len(r) <= maxGrepLineChars {
		return line
	}
	return string(r[:maxGrepLineChars]) + "…"
}

// newGrepMatcher 建匹配函数：固定串或正则，可折叠大小写。
//
// 固定串走 strings.Contains 而不是把 query 塞进正则——含 `(`、`[` 的字面量搜索必须能搜，
// 而用户不写 regex=true 就是在说「这些字符按字面算」。
func newGrepMatcher(query string, regex, ignoreCase bool) (func(string) bool, error) {
	if regex {
		pat := query
		if ignoreCase {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression: %w", err)
		}
		return re.MatchString, nil
	}
	if ignoreCase {
		q := strings.ToLower(query)
		return func(line string) bool { return strings.Contains(strings.ToLower(line), q) }, nil
	}
	return func(line string) bool { return strings.Contains(line, query) }, nil
}
