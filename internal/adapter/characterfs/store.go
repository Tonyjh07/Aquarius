package characterfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// avatarName 头像固定文件名（§5.17 落位：`characters/<slug>/avatar.png`）。
const avatarName = "avatar.png"

// maxSlugRunes slug 截断前的字符数上限（§5.17）。
const maxSlugRunes = 40

// Store 角色卡目录（§5.17）：`<profile>/characters/<slug>/` 下的卡目录。实现
// port.CharacterCatalog（只读 List/Load），外加用户面的 Import / Remove（装配根把它接到
// app.CharacterAdmin）。目录是唯一事实来源：坏条目跳过不阻断其余。
type Store struct {
	root string
	logf func(format string, args ...any)
	now  func() time.Time
}

// Options Store 的装配参数。
type Options struct {
	// Root `<profile>/characters` 绝对路径，不存在则按需创建。
	Root string
	// Logf 用户面日志（坏条目跳过、meta 重建失败）；nil 时静默。
	Logf func(format string, args ...any)
	// Now 导入时间来源；nil 时用 time.Now（测试注入固定时钟）。
	Now func() time.Time
}

var _ port.CharacterCatalog = (*Store)(nil)

// New 组装角色卡目录；Root 必须是绝对路径（不猜工作目录，与技能安装同口径）。
func New(opts Options) (*Store, error) {
	if opts.Root == "" || !filepath.IsAbs(opts.Root) {
		return nil, fmt.Errorf("角色卡根必须是绝对路径（得到 %q）", opts.Root)
	}
	s := &Store{root: filepath.Clean(opts.Root), logf: opts.Logf, now: opts.Now}
	if s.logf == nil {
		s.logf = func(string, ...any) {}
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// List 已导入角色卡（按显示名排序，同名以 slug 定序），实现 §5.17 的 List。
//
// 坏条目（card.json 读不出且 meta 也没有）带警告跳过——一个坏目录不该让整张清单消失。
func (s *Store) List(ctx context.Context) ([]port.CharacterInfo, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读角色卡目录: %w", err)
	}
	out := make([]port.CharacterInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.root, e.Name())
		info, err := s.loadInfo(dir, e.Name())
		if err != nil {
			s.logf("跳过角色卡目录 %s：%v", e.Name(), err)
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Display != out[j].Display {
			return out[i].Display < out[j].Display
		}
		return out[i].Slug < out[j].Slug
	})
	return out, nil
}

// Load 按 slug 取一张卡（§5.17）：meta 缺就顺手从 card.json 重建，card.json 读不出即报错。
func (s *Store) Load(ctx context.Context, slug string) (port.CharacterCard, error) {
	dir, err := s.dirOf(slug)
	if err != nil {
		return port.CharacterCard{}, err
	}
	info, err := s.loadInfo(dir, slug)
	if err != nil {
		return port.CharacterCard{}, fmt.Errorf("角色卡 %s 读不出: %w", slug, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "card.json"))
	if err != nil {
		return port.CharacterCard{}, fmt.Errorf("角色卡 %s 的 card.json 读不出: %w", slug, err)
	}
	parsed, _, err := parseCardJSON(raw)
	if err != nil {
		return port.CharacterCard{}, fmt.Errorf("角色卡 %s 解析失败: %w", slug, err)
	}
	card := port.CharacterCard{Info: info, Profile: parsed.Profile}
	if ap := existingAvatar(dir); ap != "" {
		card.AvatarPath = ap
		card.Info.HasAvatar = true
	}
	return card, nil
}

// Import 导入一个卡文件（PNG / JSON / .charx）：解析 → 归一 slug → 落位 → 写 meta（§5.17）。
//
// 同名卡不覆盖：再导入同一张卡是新 slug（卡可能在对话里正被引用）；要换版本先 Remove。
// 只读导入，不授予任何能力——卡文件里的脚本、路径、URL 都不执行、不访问。
func (s *Store) Import(ctx context.Context, source string) (port.CharacterCard, error) {
	if strings.TrimSpace(source) == "" {
		return port.CharacterCard{}, errors.New("请给卡文件路径（PNG / JSON / .charx）")
	}
	data, err := readFileLimited(source, maxCardFileBytes)
	if err != nil {
		return port.CharacterCard{}, err
	}
	parsed, err := parseCard(filepath.Base(source), data)
	if err != nil {
		return port.CharacterCard{}, err
	}
	slug, err := s.uniqueSlug(parsed.Info.Display)
	if err != nil {
		return port.CharacterCard{}, err
	}
	dir := filepath.Join(s.root, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return port.CharacterCard{}, fmt.Errorf("建角色目录 %s: %w", dir, err)
	}
	info := parsed.Info
	info.Slug = slug
	info.ImportedAt = s.now()

	if err := atomicfile.WriteFile(filepath.Join(dir, "card.json"), parsed.RawJSON, 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return port.CharacterCard{}, fmt.Errorf("写 card.json: %w", err)
	}

	if avatar := s.writeAvatar(dir, parsed, &info); avatar != "" {
		info.HasAvatar = true
	}
	if len(parsed.Archive) > 0 {
		if err := atomicfile.WriteFile(filepath.Join(dir, "source.charx"), parsed.Archive, 0o644); err != nil {
			info.Warnings = append(info.Warnings, "原压缩包留档失败："+err.Error())
		}
	}
	if err := writeMeta(dir, info, avatarFile(dir)); err != nil {
		_ = os.RemoveAll(dir)
		return port.CharacterCard{}, fmt.Errorf("写 meta.json: %w", err)
	}

	card := port.CharacterCard{Info: info, Profile: parsed.Profile}
	if ap := existingAvatar(dir); ap != "" {
		card.AvatarPath = ap
	}
	return card, nil
}

// Remove 删除一个角色目录（只读导入下的「删除」就是删目录）。未命中返回 ErrCharacterNotFound。
func (s *Store) Remove(ctx context.Context, slug string) error {
	dir, err := s.dirOf(slug)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("删除角色卡 %s: %w", slug, err)
	}
	return nil
}

// writeAvatar 重编码并落 avatar.png（§5.17：只留像素）。头像解码失败只记警告、不阻断导入。
func (s *Store) writeAvatar(dir string, parsed ParsedCard, info *port.CharacterInfo) string {
	if len(parsed.Avatar) == 0 {
		return ""
	}
	png, err := encodeAvatar(parsed.Avatar)
	if err != nil {
		info.Warnings = append(info.Warnings, "头像跳过："+err.Error())
		return ""
	}
	if err := atomicfile.WriteFile(filepath.Join(dir, avatarName), png, 0o644); err != nil {
		info.Warnings = append(info.Warnings, "头像写盘失败："+err.Error())
		return ""
	}
	return avatarName
}

// loadInfo 读一张卡的事实（§5.17）：meta 在就用 meta，缺/坏则从 card.json 重建并记警告。
func (s *Store) loadInfo(dir, dirName string) (port.CharacterInfo, error) {
	if m, err := readMeta(dir); err == nil {
		info := m.toInfo(dirName)
		info.HasAvatar = existingAvatar(dir) != ""
		return info, nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "card.json"))
	if err != nil {
		return port.CharacterInfo{}, fmt.Errorf("card.json 读不出: %w", err)
	}
	parsed, _, err := parseCardJSON(raw)
	if err != nil {
		return port.CharacterInfo{}, fmt.Errorf("card.json 解析失败: %w", err)
	}
	info := parsed.Info
	info.Slug = dirName
	info.Source = sourceFromFiles(dir)
	info.Warnings = append(info.Warnings, "meta.json 缺失或损坏，已从卡重建")
	info.HasAvatar = existingAvatar(dir) != ""
	if err := writeMeta(dir, info, avatarFile(dir)); err != nil {
		s.logf("重建 %s 的 meta.json 失败：%v", dirName, err)
	}
	return info, nil
}

// dirOf 把 slug 解析成目录绝对路径（拒绝空、分隔符与越界的 slug）。
func (s *Store) dirOf(slug string) (string, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" || slug != filepath.Base(slug) || strings.ContainsAny(slug, `/\`) {
		return "", fmt.Errorf("%w: %q", port.ErrCharacterNotFound, slug)
	}
	dir := filepath.Join(s.root, slug)
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%w: %s", port.ErrCharacterNotFound, slug)
	}
	return dir, nil
}

// uniqueSlug 由显示名派生 slug，与已有目录**大小写不敏感**比较，重名追加 `-2`、`-3`。
func (s *Store) uniqueSlug(display string) (string, error) {
	base, err := slugBase(display)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	if entries, err := os.ReadDir(s.root); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				taken[strings.ToLower(e.Name())] = true
			}
		}
	}
	slug := base
	for i := 2; taken[strings.ToLower(slug)]; i++ {
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	return slug, nil
}

// slugBase 显示名归一（§5.17）：去控制字符与路径分隔符、空白折叠为 `-`、去首尾点、截 40 字符，
// 保留中文。归一后为空、`.`/`..`、Windows 设备名一律拒绝。
func slugBase(display string) (string, error) {
	var b strings.Builder
	prevDash := false
	for _, r := range display {
		switch {
		case r < 0x20 || r == 0x7f:
			continue
		case r == '/' || r == '\\' || strings.ContainsRune(`<>:"|?*`, r):
			continue
		case unicode.IsSpace(r):
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			b.WriteRune(r)
			prevDash = false
		}
	}
	slug := strings.Trim(b.String(), ".-")
	if utf8.RuneCountInString(slug) > maxSlugRunes {
		slug = strings.Trim(string([]rune(slug)[:maxSlugRunes]), ".-")
	}
	if slug == "" {
		return "", fmt.Errorf("显示名 %q 归一后为空，无法作目录名", display)
	}
	if isReservedDevice(slug) {
		return "", fmt.Errorf("显示名 %q 归一后是系统保留名 %q，请改名后再导入", display, slug)
	}
	return slug, nil
}

// isReservedDevice 是否是 Windows 保留设备名（CON / PRN / AUX / NUL / COM1-9 / LPT1-9）。
func isReservedDevice(slug string) bool {
	name := strings.ToUpper(slug)
	if i := strings.IndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	switch name {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) &&
		name[3] >= '1' && name[3] <= '9' {
		return true
	}
	return false
}

// meta.json 的形状（§5.17 落位）：列表与详情直接读它，避免每次解析整张卡（卡 JSON 可 17 MB）。
type metaJSON struct {
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Display     string    `json:"display"`
	Creator     string    `json:"creator,omitempty"`
	Version     string    `json:"version,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	Source      string    `json:"source"`
	Spec        string    `json:"spec"`
	Greetings   int       `json:"greetings"`
	BookEntries int       `json:"book_entries"`
	Avatar      string    `json:"avatar,omitempty"`
	ImportedAt  time.Time `json:"imported_at"`
	Warnings    []string  `json:"warnings,omitempty"`
}

func writeMeta(dir string, info port.CharacterInfo, avatar string) error {
	m := metaJSON{
		Slug: info.Slug, Name: info.Name, Display: info.Display,
		Creator: info.Creator, Version: info.Version, Tags: info.Tags,
		Source: info.Source, Spec: info.Spec, Greetings: info.Greetings,
		BookEntries: info.BookEntries, Avatar: avatar,
		ImportedAt: info.ImportedAt, Warnings: info.Warnings,
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(dir, "meta.json"), append(b, '\n'), 0o644)
}

func readMeta(dir string) (metaJSON, error) {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return metaJSON{}, err
	}
	var m metaJSON
	if err := json.Unmarshal(b, &m); err != nil {
		return metaJSON{}, err
	}
	if strings.TrimSpace(m.Display) == "" {
		return metaJSON{}, errors.New("meta.json 缺显示名")
	}
	return m, nil
}

func (m metaJSON) toInfo(dirName string) port.CharacterInfo {
	slug := m.Slug
	if slug == "" {
		slug = dirName
	}
	return port.CharacterInfo{
		Slug: slug, Name: m.Name, Display: m.Display, Creator: m.Creator,
		Version: m.Version, Tags: m.Tags, Source: m.Source, Spec: m.Spec,
		Greetings: m.Greetings, BookEntries: m.BookEntries,
		ImportedAt: m.ImportedAt, Warnings: m.Warnings, HasAvatar: m.Avatar != "",
	}
}

// existingAvatar 目录里头像文件的可读路径（无则空）。
func existingAvatar(dir string) string {
	p := filepath.Join(dir, avatarName)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p
	}
	return ""
}

// avatarFile 头像文件名（写 meta 用）。
func avatarFile(dir string) string {
	if existingAvatar(dir) != "" {
		return avatarName
	}
	return ""
}

// sourceFromFiles 从落位文件反推来源（meta 丢失时用）：source.charx 在即 charx，否则 json。
func sourceFromFiles(dir string) string {
	if fi, err := os.Stat(filepath.Join(dir, "source.charx")); err == nil && !fi.IsDir() {
		return "charx"
	}
	return "json"
}

// readFileLimited 读文件（按上限截断检测，避免一次性把超大文件读进内存）。
func readFileLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", path, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("卡文件超过 %d MB 上限", limit>>20)
	}
	return data, nil
}
