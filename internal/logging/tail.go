package logging

// tail.go 日志目录的读侧（v0.9.0「图形界面看运行日志」）：列文件 + 读末尾几行。
//
// 只做这两件事，不做过滤、不做解析——查看口要看到的是文件里真实的那几行，加了筛选就会
// 出现「界面里没有、文件里有」的分歧，而排查时人信的是文件。读末尾按窗口逐步放大，
// **不把整份读进内存**：4 MiB 的当日文件与几天的旧文件都在同一个目录里。

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// File 日志目录里的一份文件：按天切出的正本，或轮转出来的 `.1`。
type File struct {
	Name string    // 文件名（不含目录）
	Size int64     // 字节数
	Mod  time.Time // 最后写入时刻（列表按该时刻倒序）
}

// List 列出 dir 里由本包写出的文件，新的在前。
//
// 只认 `aquarius-*` 前缀：同目录还可能躺着 `audit.log`、`crash-*.txt`、`jobs/`，这些是
// 别的面（审计、崩溃报告、后台任务输出），混进来看起来像一回事，实际格式各不相同。
// 目录不存在按「还没有日志」处理，返回空表而不是错误——首次运行就是这个状态。
func List(dir string) ([]File, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]File, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), filePrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // 读目录与读属性之间被清掉：跳过这一份，不因此整表失败
		}
		out = append(out, File{Name: e.Name(), Size: info.Size(), Mod: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Mod.Equal(out[j].Mod) {
			return out[i].Mod.After(out[j].Mod)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

// tailWindows 逐步放大的读取窗口：先 64 KiB，不够行数就放大一档，到 1 MiB 为止。
//
// 上限定在 1 MiB 而不是无限放大：文本形态一条几十到几百字节，1 MiB 足够放下几千行；
// 再大的文件说明有人在看一份不是「最近发生了什么」的东西，那该用编辑器，不该让界面
// 把一个 4 MiB 的文件整个搬进内存。
var tailWindows = []int64{64 << 10, 256 << 10, 1 << 20}

// Tail 读 path 末尾至多 maxLines 行（返回的行不带行尾符，末尾不留空行）。
//
// 窗口内的第一行可能是半行（从中间切进去的），一律丢掉：半行看起来像完整的一行，
// 读日志的人会照着这半行去猜一条不存在的事件。maxLines <= 0 时只按最小窗口读。
func Tail(path string, maxLines int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := st.Size()
	var last []string
	for _, win := range tailWindows {
		if win > size {
			win = size
		}
		buf := make([]byte, win)
		if _, err := f.ReadAt(buf, size-win); err != nil && err != io.EOF {
			return "", err
		}
		text := string(buf)
		// 从中间切进去时首行不完整，丢掉；整份读进来时首行是完整的，不能丢。
		if win < size {
			if i := strings.IndexByte(text, '\n'); i >= 0 {
				text = text[i+1:]
			} else {
				text = ""
			}
		}
		lines := splitLines(text)
		last = lines
		// 行数够了就定这一档；不够且还有更大的窗口就再放大一次。
		if maxLines <= 0 || len(lines) >= maxLines || win == size {
			return joinTail(lines, maxLines), nil
		}
	}
	// 最大窗口也装不满 maxLines（1 MiB 里都没那么多行）：给窗口内全部，不再往上加。
	return joinTail(last, maxLines), nil
}

// splitLines 去尾空行后按行切（`\r\n` 与 `\n` 都认：Windows 上文本形态的换行是 CRLF）。
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// joinTail 取最后 maxLines 行并接回一个字符串。
func joinTail(lines []string, maxLines int) string {
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

// LogFileName 当日文件名（`aquarius-YYYY-MM-DD.log`）。查看口要标出「今天这份」，
// 与写侧同一份命名规则——两边各拼一次就会出现某天之后的静默错位。
func LogFileName(day time.Time) string {
	return filePrefix + day.Format("2006-01-02") + ".log"
}

// Dir 日志目录在 profile 下的位置（`<profile>/logs`）。写侧在装配根拼路径，读侧
// 也需要同一条规则；放在这里是为了两边只留一处。
func Dir(profileDir string) string { return filepath.Join(profileDir, "logs") }
