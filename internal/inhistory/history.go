package inhistory

// inhistory 输入历史的落盘形式：`<profile>/input_history.jsonl`，一行一条已提交的输入。
//
// 为什么单独成包：GUI（adapter/uigui）与 TUI（adapter/uitui）各有自己的翻历史逻辑，
// 两份都被锁在进程内存里，重启即归零，而且互相看不见对方输入的句子。落盘之后两侧
// 读到的是同一份文件——这不是新功能，是把「已经记住 50 条」这件事真的记住。
//
// 三条口径：
//   - 追加写、不重写历史（压实除外）：崩在半路也不会丢已有的句子；
//   - 读时去相邻重复、只留最近 Max 条：去重放在读侧，写侧才能保持廉价；
//   - 坏行跳过、文件不可读写即退化为内存：历史不值得挡住启动。
//
// 每行是 JSON 字符串而不是裸文本：一句话里可以有多行（Shift+Enter），裸文本没法表达
// 「这是一条」；另外裸文本里的 `\n` 会让行号与条数对不上，压实时的计数跟着错。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Max 保留的条数（与 GUI / TUI 的既有上限一致）。
const Max = 50

// compactAt 文件行数超过这个值就压实一次：追加写让文件无限长，而读侧只取最后 Max 条，
// 上千行之后的旧行没有任何读者。压实的代价是重写一次小文件，频率低到可以忽略。
const compactAt = 400

// Store 一份落盘的输入历史。路径为空时只在内存里（测试与禁用落盘的场景）。
//
// 并发安全：GUI 的提交与 TUI 的提交各在自己的 goroutine，压实又要在写之间插入，
// 一把 mutex 比「谁先谁后」的推理便宜。
type Store struct {
	mu    sync.Mutex
	path  string
	lines []string // 已去相邻重复、最多 Max 条
	disk  int      // 文件当前行数（压实阈值用；读失败时按内存条数估）
}

// Open 读入既有历史。路径不存在、读不出、全是坏行都得到空历史——不报错、不挡启动。
func Open(path string) *Store {
	s := &Store{path: path}
	if path == "" {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		s.disk++
		var text string
		if err := json.Unmarshal([]byte(ln), &text); err != nil {
			continue // 坏行（截断、手工编辑）跳过：一条历史不值得让整份作废
		}
		s.append(text)
	}
	return s
}

// Lines 最近 Max 条的副本（调用方改它不该改到历史）。
func (s *Store) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.lines))
	copy(out, s.lines)
	return out
}

// Push 记一条已提交的输入：空行与「与上一条相同」都跳过（与两侧既有口径一致）。
func (s *Store) Push(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.append(line) {
		return
	}
	s.persist()
}

// append 更新内存并返回是否真的入了一条（调用方持锁）。
func (s *Store) append(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if n := len(s.lines); n > 0 && s.lines[n-1] == line {
		return false
	}
	s.lines = append(s.lines, line)
	if len(s.lines) > Max {
		s.lines = s.lines[len(s.lines)-Max:]
	}
	return true
}

// persist 追加一行；超阈值先压实。写失败静默——历史不该打断对话。
func (s *Store) persist() {
	if s.path == "" || len(s.lines) == 0 {
		return
	}
	data, err := json.Marshal(s.lines[len(s.lines)-1])
	if err != nil {
		return
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, werr := f.Write(append(data, '\n'))
	_ = f.Close()
	if werr != nil {
		return
	}
	s.disk++
	if s.disk > compactAt {
		s.compact()
	}
}

// compact 用内存里的最后 Max 条重写文件。失败就保持原样：文件长一点不影响读。
func (s *Store) compact() {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	var b strings.Builder
	for _, line := range s.lines {
		data, err := json.Marshal(line)
		if err != nil {
			continue
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(s.path, []byte(b.String()), 0o600); err != nil {
		return
	}
	s.disk = len(s.lines)
}
