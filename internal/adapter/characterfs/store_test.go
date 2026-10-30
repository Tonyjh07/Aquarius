package characterfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newStore 建一个临时根上的 Store（固定时钟，日志收集）。
func newStore(t *testing.T) (*Store, string, *[]string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "characters")
	logs := &[]string{}
	s, err := New(Options{
		Root: root,
		Logf: func(format string, args ...any) { *logs = append(*logs, format) },
		Now:  func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, root, logs
}

// writeCard 把一个卡文件写进临时目录并返回路径。
func writeCard(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write card: %v", err)
	}
	return p
}

// TestStoreImportLoadList 导入 → 加载 → 列清单走通，字段与落位文件齐。
func TestStoreImportLoadList(t *testing.T) {
	s, root, _ := newStore(t)
	ctx := context.Background()
	path := writeCard(t, "aria.json", []byte(`{"spec_version":"2.0","data":{"name":"Aria","nickname":"Ari","description":"desc","post_history_instructions":"tail"}}`))

	card, err := s.Import(ctx, path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if card.Info.Slug != "Ari" || card.Info.Name != "Aria" || card.Info.Display != "Ari" {
		t.Fatalf("info = %+v", card.Info)
	}
	if card.Info.ImportedAt.IsZero() {
		t.Fatalf("ImportedAt must be stamped")
	}
	// 落位文件：card.json 与 meta.json（JSON 卡无头像、无 source.charx）。
	dir := filepath.Join(root, "Ari")
	for _, f := range []string{"card.json", "meta.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s missing: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "source.charx")); err == nil {
		t.Fatalf("JSON card must not leave source.charx")
	}

	loaded, err := s.Load(ctx, "Ari")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Profile.Description != "desc" || loaded.Profile.PostHistory != "tail" {
		t.Fatalf("loaded profile = %+v", loaded.Profile)
	}
	if loaded.AvatarPath != "" {
		t.Fatalf("avatar path = %q, want empty", loaded.AvatarPath)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Slug != "Ari" {
		t.Fatalf("list = %+v", list)
	}
}

// TestStoreListSorted 清单按显示名排序，同名以 slug 定序。
func TestStoreListSorted(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	for _, n := range []string{"Zoe", "ann", "Bob"} {
		if _, err := s.Import(ctx, writeCard(t, "c.json", cardJSONNamed(n))); err != nil {
			t.Fatalf("import %s: %v", n, err)
		}
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := make([]string, len(list))
	for i, c := range list {
		got[i] = c.Display
	}
	want := []string{"Bob", "Zoe", "ann"} // 字节序：大写字母排在前面
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// TestSlugBase KeepsChinese 中文名归一保留汉字，分隔符去掉。
func TestSlugBaseKeepsChinese(t *testing.T) {
	got, err := slugBase("  小 明/坏:名  ")
	if err != nil {
		t.Fatalf("slugBase: %v", err)
	}
	if got != "小-明坏名" {
		t.Fatalf("slug = %q, want %q", got, "小-明坏名")
	}
}

// TestSlugBaseTrimsAndFolds 首尾点与横线去掉，空白折叠为单个横线，超长截断。
func TestSlugBaseTrimsAndFolds(t *testing.T) {
	got, err := slugBase("..a   b..")
	if err != nil {
		t.Fatalf("slugBase: %v", err)
	}
	if got != "a-b" {
		t.Fatalf("slug = %q, want %q", got, "a-b")
	}
	long := strings.Repeat("x", maxSlugRunes+10)
	got, err = slugBase(long)
	if err != nil {
		t.Fatalf("slugBase: %v", err)
	}
	if len([]rune(got)) != maxSlugRunes {
		t.Fatalf("slug runes = %d, want %d", len([]rune(got)), maxSlugRunes)
	}
}

// TestSlugBaseRejects 归一后为空、保留设备名都拒绝。
func TestSlugBaseRejects(t *testing.T) {
	if _, err := slugBase("///"); err == nil {
		t.Fatalf("empty-after-normalisation must error")
	}
	for _, name := range []string{"CON", "nul", "Com1", "LPT9"} {
		if _, err := slugBase(name); err == nil {
			t.Fatalf("reserved device %q must be rejected", name)
		}
	}
	// 设备名带扩展名也算（Windows 只看点之前那段）。
	if _, err := slugBase("aux.txt"); err == nil {
		t.Fatalf("reserved device with extension must be rejected")
	}
	// 只是前缀，不是保留名。
	if _, err := slugBase("console"); err != nil {
		t.Fatalf("console must be allowed: %v", err)
	}
}

// TestStoreSlugDedup 同名卡不覆盖：第二次导入得到 `-2` 后缀，两张都在。
func TestStoreSlugDedup(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	first, err := s.Import(ctx, writeCard(t, "c.json", cardJSONNamed("Aria")))
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	second, err := s.Import(ctx, writeCard(t, "c.json", cardJSONNamed("Aria")))
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if first.Info.Slug != "Aria" || second.Info.Slug != "Aria-2" {
		t.Fatalf("slugs = %q / %q, want Aria / Aria-2", first.Info.Slug, second.Info.Slug)
	}
	list, _ := s.List(ctx)
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
}

// TestStoreSlugDedupCaseInsensitive 大小写不同的目录名也算占用（Windows 上同名）。
func TestStoreSlugDedupCaseInsensitive(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	if _, err := s.Import(ctx, writeCard(t, "c.json", cardJSONNamed("Aria"))); err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := s.Import(ctx, writeCard(t, "c.json", cardJSONNamed("aria")))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.Info.Slug != "aria-2" {
		t.Fatalf("slug = %q, want aria-2", second.Info.Slug)
	}
}

// TestStoreImportPNGStripsText PNG 卡导入：头像重编码为纯像素（无 tEXt），HasAvatar 置起。
func TestStoreImportPNGStripsText(t *testing.T) {
	s, root, _ := newStore(t)
	ctx := context.Background()
	js := `{"spec_version":"2.0","data":{"name":"Pixel"}}`
	base := validPNG(t)
	cardPNG := injectText(t, base, "chara", b64(js))
	// 卡 JSON 塞在 tEXt 里，parseCard 走 PNG 分支。
	path := writeCard(t, "pixel.png", cardPNG)

	card, err := s.Import(ctx, path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !card.Info.HasAvatar || card.AvatarPath == "" {
		t.Fatalf("avatar must be set: %+v", card.Info)
	}
	avatar, err := os.ReadFile(card.AvatarPath)
	if err != nil {
		t.Fatalf("read avatar: %v", err)
	}
	if strings.Contains(string(avatar), "tEXt") {
		t.Fatalf("avatar kept a tEXt chunk")
	}
	// 原卡 JSON 里的名字仍应从 card.json 读到（卡的事实来源）。
	if card.Info.Name != "Pixel" {
		t.Fatalf("name = %q", card.Info.Name)
	}
	_ = root
}

// TestStoreImportCharxKeepsArchive .charx 导入留 source.charx，meta.Source 记 charx。
func TestStoreImportCharxKeepsArchive(t *testing.T) {
	s, root, _ := newStore(t)
	ctx := context.Background()
	card := v3Card("Aria", "")
	archive := zipWith(zf{"card.json", card})
	path := writeCard(t, "aria.charx", archive)

	card0, err := s.Import(ctx, path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if card0.Info.Source != "charx" {
		t.Fatalf("source = %q, want charx", card0.Info.Source)
	}
	kept, err := os.ReadFile(filepath.Join(root, card0.Info.Slug, "source.charx"))
	if err != nil {
		t.Fatalf("source.charx missing: %v", err)
	}
	if string(kept) != string(archive) {
		t.Fatalf("source.charx not verbatim")
	}
}

// TestStoreListSkipsBadDir 坏目录（card.json 读不出）带警告跳过，其余仍在。
func TestStoreListSkipsBadDir(t *testing.T) {
	s, root, logs := newStore(t)
	ctx := context.Background()
	if _, err := s.Import(ctx, writeCard(t, "c.json", cardJSONNamed("Good"))); err != nil {
		t.Fatalf("import: %v", err)
	}
	bad := filepath.Join(root, "broken")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bad, "card.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write bad card: %v", err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Display != "Good" {
		t.Fatalf("list = %+v, want only Good", list)
	}
	if len(*logs) == 0 {
		t.Fatalf("a skipped dir must be logged")
	}
}

// TestStoreLoadRebuildsMeta meta.json 缺失时从 card.json 重建并记警告。
func TestStoreLoadRebuildsMeta(t *testing.T) {
	s, root, _ := newStore(t)
	ctx := context.Background()
	card, err := s.Import(ctx, writeCard(t, "c.json", cardJSONNamed("Rebuild")))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	metaPath := filepath.Join(root, card.Info.Slug, "meta.json")
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("remove meta: %v", err)
	}
	loaded, err := s.Load(ctx, card.Info.Slug)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Info.Display != "Rebuild" {
		t.Fatalf("display = %q", loaded.Info.Display)
	}
	if !warnsContain(loaded.Info.Warnings, "meta.json") {
		t.Fatalf("warnings = %v, want a rebuild note", loaded.Info.Warnings)
	}
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("Load must rewrite meta.json: %v", err)
	}
}

// TestStoreLoadNotFound 未命中返回包装了 ErrCharacterNotFound 的错误。
func TestStoreLoadNotFound(t *testing.T) {
	s, _, _ := newStore(t)
	_, err := s.Load(context.Background(), "nope")
	if !errors.Is(err, port.ErrCharacterNotFound) {
		t.Fatalf("err = %v, want ErrCharacterNotFound", err)
	}
	// 越界 slug 也归到未命中，不去摸目录。
	if _, err := s.Load(context.Background(), "../escape"); !errors.Is(err, port.ErrCharacterNotFound) {
		t.Fatalf("traversal err = %v, want ErrCharacterNotFound", err)
	}
}

// TestStoreImportRejectsReservedDevice 设备名拒绝导入（不落盘）。
func TestStoreImportRejectsReservedDevice(t *testing.T) {
	s, root, _ := newStore(t)
	_, err := s.Import(context.Background(), writeCard(t, "c.json", cardJSONNamed("CON")))
	if err == nil {
		t.Fatalf("reserved device import must error")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatalf("nothing should land on a rejected import: %v", entries)
	}
}

// TestStoreImportMissingFile 文件不存在即报错（不静默）。
func TestStoreImportMissingFile(t *testing.T) {
	s, _, _ := newStore(t)
	if _, err := s.Import(context.Background(), filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatalf("missing source must error")
	}
	if _, err := s.Import(context.Background(), "  "); err == nil {
		t.Fatalf("blank source must error")
	}
}

// TestStoreRemove Remove 删目录；再 Load 即未命中。
func TestStoreRemove(t *testing.T) {
	s, root, _ := newStore(t)
	ctx := context.Background()
	card, err := s.Import(ctx, writeCard(t, "c.json", cardJSONNamed("Gone")))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if err := s.Remove(ctx, card.Info.Slug); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, card.Info.Slug)); !os.IsNotExist(err) {
		t.Fatalf("dir still there: %v", err)
	}
	if _, err := s.Load(ctx, card.Info.Slug); !errors.Is(err, port.ErrCharacterNotFound) {
		t.Fatalf("Load after remove = %v, want ErrCharacterNotFound", err)
	}
}

// TestStoreNewRequiresAbsRoot 根必须是绝对路径。
func TestStoreNewRequiresAbsRoot(t *testing.T) {
	if _, err := New(Options{Root: "relative"}); err == nil {
		t.Fatalf("relative root must be rejected")
	}
	if _, err := New(Options{Root: ""}); err == nil {
		t.Fatalf("empty root must be rejected")
	}
}

// TestStoreListMissingRoot 根还不存在时 List 返回空而非报错（首次使用）。
func TestStoreListMissingRoot(t *testing.T) {
	s, _, _ := newStore(t)
	list, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list = %+v, want empty", list)
	}
}

// validPNG 一张最小的、可解码的 PNG（头像重编码需要真图）。
func validPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// cardJSONNamed 一张最小 V2 卡，name=display=n。
func cardJSONNamed(n string) []byte {
	b, _ := json.Marshal(map[string]any{
		"spec_version": "2.0",
		"data":         map[string]any{"name": n},
	})
	return b
}
