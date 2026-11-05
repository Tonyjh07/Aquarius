package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// Level 日志级别（D276）：四档，没有别的。
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// levelNames 级别名即 JSON 字段值，也是文本里那一列——三处出口共用这一份，改名就是改契约。
var levelNames = [...]string{"debug", "info", "warn", "error"}

// String 小写级别名（`--log-format json` 的 level 字段、文本那一列、`ParseLevel` 的反向）。
func (lv Level) String() string {
	if lv < LevelDebug || lv > LevelError {
		return "unknown"
	}
	return levelNames[lv]
}

// ParseLevel 解析级别写法。空串按 info（键缺失 = 模板值）；非法值报错——静默回落等于
// 用户在配置里写的 `wran` 永远不生效，而他会以为自己在看 warn 以上的日志。
func ParseLevel(raw string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return LevelInfo, nil
	case "debug":
		return LevelDebug, nil
	case "warn":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	}
	return LevelInfo, fmt.Errorf("日志级别 %q 仅支持 debug | info | warn | error", raw)
}

// Format 两种形态：人读文本、机读 JSON。字段相同，只是渲染不同。
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// ParseFormat 解析 `--log-format`。空串按 text。
func ParseFormat(raw string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(FormatText):
		return FormatText, nil
	case string(FormatJSON):
		return FormatJSON, nil
	}
	return FormatText, fmt.Errorf("日志形态 %q 仅支持 text | json", raw)
}

// Context 子系统标识（contracts §7.9）。英文枚举：它是分类不是文案，不进文案表。
type Context string

const (
	Boot      Context = "boot"
	Attach    Context = "attach"
	LLM       Context = "llm"
	Audio     Context = "audio"
	Job       Context = "job"
	Delegate  Context = "delegate"
	Plugin    Context = "plugin"
	Skill     Context = "skill"
	Character Context = "character"
	Session   Context = "session"
	Perm      Context = "perm"
	UI        Context = "ui"
	Web       Context = "web"
)

// Contexts 全部已登记的上下文（测试按它校验枚举没被随手加宽）。
var Contexts = []Context{Boot, Attach, LLM, Audio, Job, Delegate, Plugin, Skill, Character, Session, Perm, UI, Web}

const (
	// defaultCap 单文件上限，超出轮转一代（口径同 audit.log）。
	defaultCap = 4 << 20 // 4 MiB
	// defaultKeepDays 保留天数：超期文件在下次 Open 时清掉。
	defaultKeepDays = 7
	// filePrefix 落盘文件名前缀：`aquarius-YYYY-MM-DD.log`。
	filePrefix = "aquarius-"
	// timeLayoutText 文本形态的时间列（本地时区到毫秒）。
	timeLayoutText = "2006-01-02 15:04:05.000"
	// timeLayoutJSON JSON 形态的时间字段（带时区偏移，便于跨机器对齐）。
	timeLayoutJSON = "2006-01-02T15:04:05.000Z07:00"
	// stderrLevel stderr 的固定门槛：warn 以上（GUI 双击没有控制台，日志主体在文件里）。
	stderrLevel = LevelWarn
)

// Options 构造参数。零值可用：不落文件、只写 Stderr（除 Stderr 之外全取缺省）。
type Options struct {
	Dir      string    // 落盘目录；空 = 不落文件
	Level    Level     // 文件门槛（低于它的行不写）
	Format   Format    // 渲染形态
	Stderr   io.Writer // stderr 出口；nil = 不写 stderr
	Now      func() time.Time
	CapBytes int64 // 单文件上限；<=0 用 4 MiB
	KeepDays int   // 保留天数；<=0 用 7
}

// Logger 运行日志句柄。并发安全：文件、轮转与 stderr 都在同一把锁下。
type Logger struct {
	mu     sync.Mutex
	dir    string
	level  Level
	format Format
	stderr io.Writer
	now    func() time.Time
	cap    int64
	keep   int

	f    *os.File
	day  string // 当前打开文件的日期（YYYY-MM-DD），跨零点换文件
	size int64
}

// Open 打开日志：建目录、清超期文件、开今天的文件。
//
// **不返回错误**：日志失败不得拦住启动（D276）。开不出文件就退化成只写 stderr，并就此记一条
// warn——用户至少能在控制台看见「本次只有 stderr」。
func Open(opts Options) *Logger {
	l := &Logger{
		dir:    opts.Dir,
		level:  opts.Level,
		format: opts.Format,
		stderr: opts.Stderr,
		now:    opts.Now,
		cap:    opts.CapBytes,
		keep:   opts.KeepDays,
	}
	if l.now == nil {
		l.now = time.Now
	}
	if l.cap <= 0 {
		l.cap = defaultCap
	}
	if l.keep <= 0 {
		l.keep = defaultKeepDays
	}
	if l.format == "" {
		l.format = FormatText
	}
	if l.dir == "" {
		return l
	}
	l.mu.Lock()
	err := l.openLocked(l.now())
	l.mu.Unlock()
	if err != nil {
		l.Warn(Boot, "log.boot.file_unavailable", "log.act.stderr_only", "path", l.dir, "err", err)
		return l
	}
	l.purgeOld()
	return l
}

// Close 关闭文件（幂等）。
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Debug / Info / Warn / Error 四个入口。event 与 action 都是 i18n key，kv 是 `名, 值` 成对
// （与 i18n.Tf 同一种给法，同一份 kv 同时喂给事件与行动）。
func (l *Logger) Debug(ctx Context, event, action string, kv ...any) {
	l.write(LevelDebug, ctx, event, action, "", kv)
}

func (l *Logger) Info(ctx Context, event, action string, kv ...any) {
	l.write(LevelInfo, ctx, event, action, "", kv)
}

func (l *Logger) Warn(ctx Context, event, action string, kv ...any) {
	l.write(LevelWarn, ctx, event, action, "", kv)
}

func (l *Logger) Error(ctx Context, event, action string, kv ...any) {
	l.write(LevelError, ctx, event, action, "", kv)
}

// Log 按变量级别写一条（级别在装配期才定得下来时用；四个具名入口是它的简写）。
func (l *Logger) Log(lv Level, ctx Context, event, action string, kv ...any) {
	l.write(lv, ctx, event, action, "", kv)
}

// Write 写一条**整句已由调用方给出**的日志：msg 非空就不再按 event 取词，event 只留在
// JSON 侧供聚合。给的是**整句**而不是 key——装配根只剩少数几处（降级说明、回放失败、明文
// 密钥）由上游函数把句子拼好了，拆回 key 得连那边的措辞一起改，收益不成比例。事件 key 仍是
// 聚合标识，`log.*` 段的占位句只为让它能被取词表覆盖。
func (l *Logger) Write(lv Level, ctx Context, event, action, msg string, kv ...any) {
	l.write(lv, ctx, event, action, msg, kv)
}

// write 过滤后写一个出口或两个：文件要过级别门槛，stderr 固定 warn 以上。
func (l *Logger) write(lv Level, ctx Context, event, action, msg string, kv []any) {
	if l == nil {
		return
	}
	toFile := l.dir != "" && lv >= l.level
	toStderr := l.stderr != nil && lv >= stderrLevel
	if !toFile && !toStderr {
		return
	}
	now := l.now()
	line := l.render(lv, ctx, event, action, msg, kv, now)

	l.mu.Lock()
	defer l.mu.Unlock()
	if toFile {
		l.writeFileLocked(line, now)
	}
	if toStderr {
		_, _ = io.WriteString(l.stderr, line+"\n")
	}
}

// render 一条日志的最终字节（不含换行）。文本与 JSON 共用同一份取值，不各拼一遍。
func (l *Logger) render(lv Level, ctx Context, event, action, msg string, kv []any, now time.Time) string {
	if msg == "" {
		msg = i18n.Tf(event, kv...)
	}
	act := ""
	if action != "" {
		act = i18n.Tf(action, kv...)
	}
	if l.format == FormatJSON {
		rec := jsonRecord{
			TS:     now.Format(timeLayoutJSON),
			Level:  lv.String(),
			Ctx:    string(ctx),
			Event:  event,
			Action: action,
			Msg:    msg,
		}
		if act != "" {
			rec.Act = act
		}
		if len(kv) > 0 {
			rec.KV = kvMap(kv)
		}
		data, err := json.Marshal(rec)
		if err != nil { // map 里只有字符串，走不到；宁可给一行能看的降级文本
			return fmt.Sprintf("%s %s %s %s", now.Format(timeLayoutJSON), lv, ctx, msg)
		}
		return string(data)
	}
	var b strings.Builder
	b.WriteString(now.Format(timeLayoutText))
	b.WriteByte(' ')
	b.WriteString(pad(strings.ToUpper(lv.String()), 5))
	b.WriteByte(' ')
	b.WriteString(pad(string(ctx), 8))
	b.WriteByte(' ')
	b.WriteString(lineEscaper.Replace(msg))
	if act != "" {
		b.WriteString("  → ")
		b.WriteString(lineEscaper.Replace(act))
	}
	if len(kv) > 0 {
		b.WriteString("  ")
		b.WriteString(renderKV(kv))
	}
	return b.String()
}

// jsonRecord JSON 形态的一条。ctx / event / action 给 key（不随语言变），msg / act 给译文。
type jsonRecord struct {
	TS     string            `json:"ts"`
	Level  string            `json:"level"`
	Ctx    string            `json:"ctx"`
	Event  string            `json:"event"`
	Action string            `json:"action,omitempty"`
	Msg    string            `json:"msg"`
	Act    string            `json:"act,omitempty"`
	KV     map[string]string `json:"kv,omitempty"`
}

// kvMap 把 `名, 值` 成对转成 map（JSON 侧靠 marshal 的键序保证稳定）。
func kvMap(kv []any) map[string]string {
	out := make(map[string]string, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		name, ok := kv[i].(string)
		if !ok || name == "" {
			continue
		}
		out[name] = fmt.Sprint(kv[i+1])
	}
	return out
}

// lineEscaper 文本形态的单行化：换行、回车、制表符转成字面量，包裹符本身也转义。
//
// 文本形态的约定是「一行一条」，而调用方给的 err 经常自带换行（yaml 解析、子进程输出、
// stderr 尾巴）。原样写下去，一条记录会劈成两行：后半行没有时间、级别与上下文，按级别
// 或 ctx 过滤时既会漏也会误计。JSON 形态不走这里——那边由 marshal 负责转义。
var lineEscaper = strings.NewReplacer(
	"\r\n", `\n`,
	"\n", `\n`,
	"\r", `\r`,
	"\t", `\t`,
	"«", `\«`,
	"»", `\»`,
)

// renderKV 文本侧的 `名=值` 尾巴，保持调用方给的顺序（排查时人按自己写日志的顺序读）。
func renderKV(kv []any) string {
	var b strings.Builder
	first := true
	for i := 0; i+1 < len(kv); i += 2 {
		name, ok := kv[i].(string)
		if !ok || name == "" {
			continue
		}
		if !first {
			b.WriteByte(' ')
		}
		first = false
		b.WriteString(name)
		b.WriteByte('=')
		raw := fmt.Sprint(kv[i+1])
		v := lineEscaper.Replace(raw)
		// 原文里有空白、引号或包裹符就圈起来：圈内是转义后的一行，圈外仍是 `名=值`。
		if raw == "" || strings.ContainsAny(raw, " \t\r\n\"«»") {
			b.WriteString("«" + v + "»")
			continue
		}
		b.WriteString(v)
	}
	return b.String()
}

// pad 左对齐定宽；超宽原样返回（宁可错位也不截断标识）。
func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// writeFileLocked 写一行到当天的文件：先过天，再过容量，最后写。调用方持锁。
func (l *Logger) writeFileLocked(line string, now time.Time) {
	day := now.Format("2006-01-02")
	if l.f == nil || l.day != day {
		if err := l.openLocked(now); err != nil {
			l.f = nil // 退化：这一行只留在 stderr（若够格）
			return
		}
	}
	data := line + "\n"
	if l.size+int64(len(data)) > l.cap {
		l.rotateLocked(now)
		if l.f == nil {
			return
		}
	}
	n, _ := l.f.WriteString(data)
	l.size += int64(n)
}

// openLocked 打开（或换到）当天文件。已有别的日期的文件开着就先关掉。
func (l *Logger) openLocked(now time.Time) error {
	day := now.Format("2006-01-02")
	if l.f != nil && l.day == day {
		return nil
	}
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.pathLocked(day), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	var size int64
	if st, err := f.Stat(); err == nil {
		size = st.Size()
	}
	l.f, l.day, l.size = f, day, size
	return nil
}

// rotateLocked 轮转一代：<path> → <path>.1（覆盖旧代），然后新开空文件继续。
func (l *Logger) rotateLocked(now time.Time) {
	path := l.pathLocked(l.day)
	_ = l.f.Close()
	l.f = nil
	_ = os.Remove(path + ".1") // Windows 的 rename 不覆盖既有目标
	_ = os.Rename(path, path+".1")
	_ = l.openLocked(now) // 失败则 l.f 为 nil，调用方退回 stderr
}

// pathLocked 当天文件的完整路径。
func (l *Logger) pathLocked(day string) string {
	return filepath.Join(l.dir, filePrefix+day+".log")
}

// purgeOld 清掉超期文件（含轮转出来的 `.1`）：看修改时间，不解析文件名——手改过名字的文件
// 也该按「最后写过」算年龄。失败只忽略：清不掉不影响记日志。
func (l *Logger) purgeOld() {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	cutoff := l.now().Add(-time.Duration(l.keep) * 24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), filePrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(l.dir, e.Name()))
	}
}
