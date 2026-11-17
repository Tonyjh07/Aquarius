package main

// 密钥库管理面装配根侧测试（S9-6b/D161）：uigui.SecretsAdmin 的列出 / 新增覆盖 /
// 删除——经真实 secretbin.Store（临时目录）跑，锁「写库即递增 Revision、掩码只出
// 尾 4 位、删不存在报错、未接线报错」。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// TestSecretsAdminRoundTrip 列出 / 覆盖 / 删除往返 + Revision 递增。
func TestSecretsAdminRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin, err := secretbin.New(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	w := &wiring{secretsBin: bin}
	sa := &secretsAdmin{w: w}

	// 空库：无条目、有路径、Revision 0。
	if s := sa.Snapshot(); len(s.Entries) != 0 || s.Path != bin.Path() {
		t.Fatalf("空库快照 = %+v", s)
	}

	if err := sa.Put("AQUARIUS_OPENAI_API_KEY", "sk-abcdefgh-9876"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if w.secretsRev.Load() != 1 {
		t.Fatalf("Put 后 Revision = %d, want 1", w.secretsRev.Load())
	}
	s := sa.Snapshot()
	if len(s.Entries) != 1 || s.Entries[0].Name != "AQUARIUS_OPENAI_API_KEY" ||
		s.Entries[0].Mask != "••••••9876" {
		t.Fatalf("快照 = %+v", s.Entries)
	}
	if strings.Contains(s.Entries[0].Mask, "abcdefgh") {
		t.Fatalf("掩码泄漏中段值：%q", s.Entries[0].Mask)
	}

	// 同名覆盖：条目数不变，Revision 递增。
	if err := sa.Put("AQUARIUS_OPENAI_API_KEY", "sk-zzzzzz-4321"); err != nil {
		t.Fatalf("覆盖: %v", err)
	}
	s = sa.Snapshot()
	if len(s.Entries) != 1 || s.Entries[0].Mask != "••••••4321" {
		t.Fatalf("覆盖后快照 = %+v", s.Entries)
	}
	if w.secretsRev.Load() != 2 {
		t.Fatalf("覆盖后 Revision = %d, want 2", w.secretsRev.Load())
	}

	// 删除：条目消失；不存在再删报错（不静默成功）。
	if err := sa.Delete("AQUARIUS_OPENAI_API_KEY"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if s = sa.Snapshot(); len(s.Entries) != 0 {
		t.Fatalf("删除后快照 = %+v", s.Entries)
	}
	if err := sa.Delete("AQUARIUS_OPENAI_API_KEY"); err == nil {
		t.Fatal("删不存在应报错")
	}
}

// TestSecretsAdminUnwired 未接线（secretsBin = nil）：写/删报错、快照为空不 panic。
func TestSecretsAdminUnwired(t *testing.T) {
	t.Parallel()
	sa := &secretsAdmin{w: &wiring{}}
	if s := sa.Snapshot(); len(s.Entries) != 0 || s.Path != "" {
		t.Fatalf("未接线快照 = %+v", s)
	}
	if err := sa.Put("K", "v"); err == nil {
		t.Fatal("未接线 Put 应报错")
	}
	if err := sa.Delete("K"); err == nil {
		t.Fatal("未接线 Delete 应报错")
	}
}

// TestSecretsAdminPutSurfacesStoreErrors 库自有校验（空值）透传：空值 → 报错、Revision 不动。
func TestSecretsAdminPutSurfacesStoreErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin, err := secretbin.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	w := &wiring{secretsBin: bin}
	sa := &secretsAdmin{w: w}
	if err := sa.Put("K", "   "); err == nil {
		t.Fatal("空值应报错（清一条请用删除）")
	}
	if w.secretsRev.Load() != 0 {
		t.Fatalf("失败不应递增 Revision，实为 %d", w.secretsRev.Load())
	}
}

// TestSecretsAdminDeleteNotFoundWrapsErrNotFound 删不存在时报错，文案含名字（不静默成功）。
func TestSecretsAdminDeleteNotFoundWrapsErrNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin, err := secretbin.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	sa := &secretsAdmin{w: &wiring{secretsBin: bin}}
	if err := sa.Delete("NOPE"); err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("删不存在 = %v, want 含名字", err)
	}
}

// writeImportConfig 一键导入用的配置模板：三条 provider 覆盖三种来源
// （明文 / 只存在于环境变量 / 库里已有），外加一条「两处都取不到」。
func writeImportConfig(t *testing.T) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{
  "model": {
    "providers": [
      {"name": "plain", "base_url": "http://a/v1", "keys": [{"api_key": "sk-plain-INLINE-7777"}], "models": [{"name": "m1"}]},
      {"name": "envonly", "base_url": "http://b/v1", "keys": [{"api_key": "secret:AQ_IMPORT_ENV"}], "models": [{"name": "m2"}]},
      {"name": "inbin", "base_url": "http://c/v1", "keys": [{"api_key": "secret:AQ_IMPORT_BIN"}], "models": [{"name": "m3"}]},
      {"name": "missing", "base_url": "http://d/v1", "keys": [{"api_key": "secret:AQ_IMPORT_MISSING"}], "models": [{"name": "m4"}]}
    ],
    "primary": "plain",
    "name": "m1"
  },
  "ui": {"kind": "repl"}
}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

// TestSecretsAdminImport 一键导入：明文入库并改写成引用、只存在于环境变量的引用值
// 抄进库、库里已有的条目不动、两处都取不到的列为未导入；值永不回显。
func TestSecretsAdminImport(t *testing.T) {
	cfgPath := writeImportConfig(t)
	bin, err := secretbin.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// 库已有 AQ_IMPORT_BIN（导入不应覆盖它），以及明文 provider 那一条派生名。
	if err := bin.Set(ctx, "AQ_IMPORT_BIN", "already-in-bin-1111"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AQ_IMPORT_ENV", "env-only-value-2222")
	t.Setenv("AQ_IMPORT_MISSING", "")

	w := &wiring{secretsBin: bin, cfgPath: cfgPath}
	sa := &secretsAdmin{w: w}
	res, err := sa.Import()
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// 明文那条：入库（键名按 provider 名派生）+ config 改写成引用。
	if got, err := bin.Get(ctx, "AQUARIUS_PLAIN_API_KEY"); err != nil || got != "sk-plain-INLINE-7777" {
		t.Fatalf("明文应入库: (%q, %v)", got, err)
	}
	// 环境变量那条：抄进库，值 = 环境里的那份。
	if got, err := bin.Get(ctx, "AQ_IMPORT_ENV"); err != nil || got != "env-only-value-2222" {
		t.Fatalf("环境变量应抄进库: (%q, %v)", got, err)
	}
	// 库里已有那条：不动（不覆盖）。
	if got, _ := bin.Get(ctx, "AQ_IMPORT_BIN"); got != "already-in-bin-1111" {
		t.Fatalf("库里已有条目被覆盖: %q", got)
	}
	// 结果只报名字（按名排序：'U' < '_'，故 AQUARIUS_… 在前）。
	if len(res.Imported) != 2 || res.Imported[0] != "AQUARIUS_PLAIN_API_KEY" || res.Imported[1] != "AQ_IMPORT_ENV" {
		t.Fatalf("Imported = %v", res.Imported)
	}
	if len(res.Rewritten) != 1 || res.Rewritten[0] != "AQUARIUS_PLAIN_API_KEY" {
		t.Fatalf("Rewritten = %v", res.Rewritten)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "AQ_IMPORT_MISSING" {
		t.Fatalf("Skipped = %v", res.Skipped)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("Failed = %v", res.Failed)
	}
	if w.secretsRev.Load() != 1 {
		t.Fatalf("导入后 Revision = %d, want 1", w.secretsRev.Load())
	}

	// config：明文那条已改引用，其余照旧；且**重读后 config 字节里搜不到明文**。
	raw, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(raw), "sk-plain-INLINE-7777") {
		t.Fatalf("config 里不该再出现明文密钥")
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	byName := map[string]string{}
	for _, p := range cfg.Model.Providers {
		byName[p.Name] = p.keyRefs()[0]
	}
	if byName["plain"] != "secret:AQUARIUS_PLAIN_API_KEY" {
		t.Fatalf("plain 的 api_key = %q, want 引用", byName["plain"])
	}
	if byName["envonly"] != "secret:AQ_IMPORT_ENV" {
		t.Fatalf("envonly 的 api_key 不该动: %q", byName["envonly"])
	}
	if byName["inbin"] != "secret:AQ_IMPORT_BIN" {
		t.Fatalf("inbin 的 api_key 不该动: %q", byName["inbin"])
	}
}

// TestSecretsAdminImportOtherSections 语音、决策、联网三处的明文也在导入范围内（D304）：
// 启动期的明文警告来自 warnPlaintext 的全部调用点，只收 provider 那一路的话，「导入完就
// 不再报警告」这句话对这几处不成立。空值与已经是引用的照旧不动。
func TestSecretsAdminImportOtherSections(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{
  "model": {
    "providers": [{"name": "a", "base_url": "http://a/v1", "keys": [{"api_key": "secret:AQ_KEEP"}], "models": [{"name": "m"}]}],
    "primary": "a", "name": "m"
  },
  "audio": {"asr": {"api_key": "sk-asr-1111"}, "tts": {"api_key": "secret:AQ_TTS"}},
  "decide": {"api_key": "sk-decide-3333"},
  "web": {
    "search": {"providers": [{"kind": "brave", "key": "sk-brave-4444"}]},
    "fetch": {"providers": [{"kind": "local"}]}
  },
  "ui": {"kind": "repl"}
}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	bin, err := secretbin.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &wiring{secretsBin: bin, cfgPath: cfgPath}
	res, err := (&secretsAdmin{w: w}).Import()
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	want := []string{"AQUARIUS_ASR_API_KEY", "AQUARIUS_BRAVE_API_KEY", "AQUARIUS_DECIDE_API_KEY"}
	if strings.Join(res.Imported, ",") != strings.Join(want, ",") {
		t.Fatalf("Imported = %v, want %v", res.Imported, want)
	}
	if strings.Join(res.Rewritten, ",") != strings.Join(want, ",") {
		t.Fatalf("Rewritten = %v, want %v", res.Rewritten, want)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("Failed = %v", res.Failed)
	}
	// 值原样进库（导入不改值）。
	for name, val := range map[string]string{
		"AQUARIUS_ASR_API_KEY":    "sk-asr-1111",
		"AQUARIUS_DECIDE_API_KEY": "sk-decide-3333",
		"AQUARIUS_BRAVE_API_KEY":  "sk-brave-4444",
	} {
		if got, err := bin.Get(context.Background(), name); err != nil || got != val {
			t.Fatalf("%s = (%q, %v), want %q", name, got, err, val)
		}
	}
	// 重读 config：三处都成了引用，已是引用与空值（本地抓取）都没被动过。
	raw, _ := os.ReadFile(cfgPath)
	for _, leaked := range []string{"sk-asr-1111", "sk-decide-3333", "sk-brave-4444"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("config 里仍有明文 %q", leaked)
		}
	}
	cfg2, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg2.Audio.ASR.APIKey != "secret:AQUARIUS_ASR_API_KEY" || cfg2.Audio.TTS.APIKey != "secret:AQ_TTS" {
		t.Fatalf("audio 段 = %q / %q", cfg2.Audio.ASR.APIKey, cfg2.Audio.TTS.APIKey)
	}
	if cfg2.Decide.APIKey != "secret:AQUARIUS_DECIDE_API_KEY" {
		t.Fatalf("decide = %q", cfg2.Decide.APIKey)
	}
	if got := cfg2.Web.Search.Providers[0].Key; got != "secret:AQUARIUS_BRAVE_API_KEY" {
		t.Fatalf("web.search key = %q", got)
	}
	if got := cfg2.Web.Fetch.Providers[0].Key; got != "" {
		t.Fatalf("local 抓取不该长密钥: %q", got)
	}
}

// TestSecretsAdminImportNothing 没有可导入的来源：不动库、Report 全空、Revision 不动。
func TestSecretsAdminImportNothing(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{"model":{"providers":[{"name":"a","base_url":"http://a/v1","keys":[{"api_key":"secret:AQ_NOPE_IMPORT"}],"models":[{"name":"m"}]}],"primary":"a"},"ui":{"kind":"repl"}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AQ_NOPE_IMPORT", "")
	bin, err := secretbin.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &wiring{secretsBin: bin, cfgPath: cfgPath}
	res, err := (&secretsAdmin{w: w}).Import()
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(res.Imported) != 0 || len(res.Rewritten) != 0 || len(res.Failed) != 0 {
		t.Fatalf("res = %+v, want 全空", res)
	}
	if w.secretsRev.Load() != 0 {
		t.Fatalf("无可导入时 Revision 不该动: %d", w.secretsRev.Load())
	}
}

// TestSecretsAdminImportRefusesUnreadableStore 库解不开时**拒绝导入**：普通写路径的
// 「从空表起步」在批量导入里等于丢掉已有条目，故这里响亮报错、文件保持原样。
func TestSecretsAdminImportRefusesUnreadableStore(t *testing.T) {
	t.Parallel()
	cfgPath := writeImportConfig(t)
	bin, err := secretbin.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin.Path(), []byte("broken-blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(cfgPath)
	sa := &secretsAdmin{w: &wiring{secretsBin: bin, cfgPath: cfgPath}}
	if _, err := sa.Import(); err == nil ||
		err.Error() != i18n.Tf("cmd.secrets.err.store_locked", "path", bin.Path()) {
		t.Fatalf("库解不开应拒绝导入: %v", err)
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Fatal("拒绝导入时 config 不该被改动")
	}
	if got, _ := os.ReadFile(bin.Path()); string(got) != "broken-blob" {
		t.Fatal("拒绝导入时库文件不该被改写")
	}
}

// TestSecretsAdminImportUnwired 未接线：报错（不静默「没得可导入」）。
func TestSecretsAdminImportUnwired(t *testing.T) {
	t.Parallel()
	if _, err := (&secretsAdmin{w: &wiring{}}).Import(); err == nil {
		t.Fatal("未接线 Import 应报错")
	}
}

// TestSecretsAdminImportDefaultsEmptyRef 空 api_key 与启动解析同口径回落默认引用：
// 该引用只存在于环境变量里时也应收进库（否则「启动能用、导入却没动」看着像漏了）。
func TestSecretsAdminImportDefaultsEmptyRef(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{"model":{"providers":[{"name":"a","base_url":"http://a/v1","keys":[{"api_key":""}],"models":[{"name":"m"}]}],"primary":"a"},"ui":{"kind":"repl"}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "default-ref-value-3333")
	bin, err := secretbin.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := (&secretsAdmin{w: &wiring{secretsBin: bin, cfgPath: cfgPath}}).Import()
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(res.Imported) != 1 || res.Imported[0] != "DEEPSEEK_API_KEY" {
		t.Fatalf("Imported = %v, want 回落默认引用", res.Imported)
	}
	if got, _ := bin.Get(context.Background(), "DEEPSEEK_API_KEY"); got != "default-ref-value-3333" {
		t.Fatalf("回落引用的值 = %q", got)
	}
}
