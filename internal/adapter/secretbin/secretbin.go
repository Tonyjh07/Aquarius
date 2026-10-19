// Package secretbin 实现本机加密密钥库（D151/D161）：把一张 JSON 键值表整段加密后
// 落在 <profile>/secrets.bin。
//
// 密文档按平台选：Windows 走 DPAPI（绑定当前用户账户，无口令、无额外密钥文件）；
// 其它平台回落 AES-256-GCM + 同目录 secrets.key（0600，首次写入才生成）。两档都只
// 是「整段加密」，文件头（版本 + 档名）明文，**键名与值全在密文内**——文件字节里
// 搜不到任何一条键名或值（测试据此断言）。
//
// 写盘经 atomicfile 原子换入，换入前把旧文件留一代 .bak（同 storejson 口径）；
// 读失败（解不开 / 结构坏）先试 .bak，两代都解不开才报错——调用方（装配根）据此
// **警告 + 回落同名环境变量**继续启动，而不是把「profile 拷到新机」变成硬故障
// （D161 落点②）。
//
// 明文不落日志、不进会话树：本包的错误串只带名字与路径，绝不回显值（§9）。
package secretbin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
)

// 密文档名（写进文件头，读时按它分派；跨平台文件只在同档可读）。
const (
	cipherDPAPI  = "dpapi"
	cipherAESGCM = "aesgcm"
)

// fileVersion 文件格式版本（换代时旧版本显式报错，不静默当零值解析）。
const fileVersion = 1

// 键名/值约束（写入口径：名字要能被 `secret:<名>` 引用，空值没有意义——清掉一条
// 用 Delete，不是写个空串）。
const (
	maxNameLen  = 128
	maxValueLen = 16 << 10
)

// ErrNotFound 库里没有这个名字（**不是**错误态：调用方继续查同名环境变量）。
var ErrNotFound = errors.New("secretbin: 密钥不存在")

// ErrUnreadable 库解不开或结构坏（换机拷了 profile、DPAPI 绑了别的账户、文件损坏、
// 密文被篡改）。调用方据此警告 + 回落环境变量；写路径据此从空表起步（旧文件留 .bak）。
var ErrUnreadable = errors.New("secretbin: 密钥库解不开")

// Entry 一条密钥的展示项（名字 + 掩码）：值永不回显（D161 落点④）。
type Entry struct {
	Name string // 键名（`secret:<名>` 里的 <名>）
	Mask string // 掩码：•••••• + 尾 4 位（值短于 8 位则整条打码）
}

// cipherFor 写盘时用的密文档（默认为平台档）。**可覆盖**——AES 档因此在 Windows 上
// 也能被测试直接跑到，不至于成为「只在 Linux 编过、本机从未执行」的死代码（D161）。
var cipherFor = platformCipher

// Store 本机加密密钥库（<dir>/secrets.bin + <dir>/secrets.key）。
// 方法只做本地文件 IO，不做进程内缓存：每次现读，永远新鲜（文件很小，代价可忽略）。
type Store struct {
	path    string // secrets.bin
	keyPath string // secrets.key（仅 AES 档用）
}

// New 绑定密钥库文件路径（父目录须已存在，由调用方 MkdirAll——同 atomicfile 口径）。
// 文件本身**不在此创建**：没有密钥时不落任何文件，首次 Set 才出现。
func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("secretbin: 目录不能为空")
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("secretbin: 目录 %s 不可用: %w", dir, err)
	}
	return &Store{
		path:    filepath.Join(dir, "secrets.bin"),
		keyPath: filepath.Join(dir, "secrets.key"),
	}, nil
}

// Path 密钥库文件路径（报错文案与设置页展示用；不含任何值）。
func (s *Store) Path() string { return s.path }

// Get 按名取值。名字不在库里返回 ErrNotFound（调用方继续查环境变量）；
// 库解不开返回 ErrUnreadable（调用方警告 + 回落）。
func (s *Store) Get(ctx context.Context, name string) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	m, err := s.load(ctx)
	if err != nil {
		return "", err
	}
	v, ok := m[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return v, nil
}

// Set 写入/覆盖一条密钥。库解不开时**从空表起步**（旧文件留一代 .bak）——换机后
// 首次填密钥必须能落盘，否则向导窗无路可走（D161 落点②）。
func (s *Store) Set(ctx context.Context, name, value string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("secretbin: %q 的值不可为空（要清掉这一条请用删除）", name)
	}
	if len(value) > maxValueLen {
		return fmt.Errorf("secretbin: %q 的值过长（上限 %d 字节）", name, maxValueLen)
	}
	m, err := s.load(ctx)
	if err != nil && !errors.Is(err, ErrUnreadable) {
		return err // 真 IO 错（权限等）：不动文件，报因
	}
	if errors.Is(err, ErrUnreadable) {
		m = map[string]string{} // 解不开：从空表起步，旧密文整份留给 .bak
	}
	m[name] = value
	return s.save(ctx, m)
}

// Delete 删除一条。名字不存在返回 ErrNotFound（不静默成功——UI 据此报「已不在」）。
func (s *Store) Delete(ctx context.Context, name string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	m, err := s.load(ctx)
	if err != nil && !errors.Is(err, ErrUnreadable) {
		return err
	}
	if errors.Is(err, ErrUnreadable) {
		return err // 解不开时删不了（也不知道有什么可删）——报因，不动文件
	}
	if _, ok := m[name]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	delete(m, name)
	return s.save(ctx, m)
}

// Entries 列出全部条目（按名字排序，只给名字 + 掩码）。
func (s *Store) Entries(ctx context.Context) ([]Entry, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	m, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(m))
	for n, v := range m {
		out = append(out, Entry{Name: n, Mask: maskOf(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// load 读并解密整表。文件不存在 = 空表（不是错误）；解不开/结构坏/旧版本 = ErrUnreadable，
// 此时先试一代 .bak——两代都救不回来才报 ErrUnreadable（同 storejson 的主/备口径）。
func (s *Store) load(ctx context.Context) (map[string]string, error) {
	m, err := s.decodeFile(ctx, s.path)
	if err == nil {
		return m, nil
	}
	if !errors.Is(err, ErrUnreadable) {
		return nil, err
	}
	// 注意：.bak 不存在时**不能**把「空表」当成回退成功（decodeFile 对缺失文件返回空表）
	// ——那会让「密文坏 + 无备份」静默读成「库里没有这条」，把真错误吞掉。
	if _, serr := os.Stat(s.path + ".bak"); serr == nil {
		if bak, berr := s.decodeFile(ctx, s.path+".bak"); berr == nil {
			return bak, nil
		}
	}
	return nil, err
}

// decodeFile 单个文件的读取路径。
func (s *Store) decodeFile(ctx context.Context, path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil // 还没存过任何密钥：空表
	}
	if err != nil {
		return nil, fmt.Errorf("secretbin: 读取 %s: %w", path, err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("%w: %s 结构坏: %v", ErrUnreadable, path, err)
	}
	if env.Version != fileVersion {
		return nil, fmt.Errorf("%w: %s 版本 %d 不被支持（本版认 %d）", ErrUnreadable, path, env.Version, fileVersion)
	}
	blob, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %s 载荷不是合法 base64: %v", ErrUnreadable, path, err)
	}
	plain, err := decrypt(ctx, env.Cipher, s.keyPath, blob)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUnreadable, path, err)
	}
	var m map[string]string
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, fmt.Errorf("%w: %s 明文结构坏: %v", ErrUnreadable, path, err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

// save 加密整表落盘：旧文件先留一代 .bak，再经 atomicfile 原子换入（0600）。
func (s *Store) save(ctx context.Context, m map[string]string) error {
	plain, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("secretbin: 序列化: %w", err)
	}
	cipher := cipherFor()
	blob, err := encrypt(ctx, cipher, s.keyPath, plain)
	if err != nil {
		return err
	}
	env := envelope{Version: fileVersion, Cipher: cipher, Payload: base64.StdEncoding.EncodeToString(blob)}
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("secretbin: 序列化信封: %w", err)
	}
	// 旧文件留一代 .bak：先原样搬走，再换入新文件（两处都是整份密文，不留明文退路）。
	if old, rerr := os.ReadFile(s.path); rerr == nil {
		if werr := atomicfile.WriteFile(s.path+".bak", old, 0o600); werr != nil {
			return fmt.Errorf("secretbin: 留备份 %s: %w", s.path+".bak", werr)
		}
	}
	if err := atomicfile.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("secretbin: 写入 %s: %w", s.path, err)
	}
	return nil
}

// envelope 文件外壳：只有版本与档名是明文（键名与值都在密文里）。
type envelope struct {
	Version int    `json:"version"`
	Cipher  string `json:"cipher"`
	Payload string `json:"payload"` // base64(密文)
}

// encrypt 按档加密（keyPath 仅 AES 档用）。
func encrypt(ctx context.Context, cipher, keyPath string, plain []byte) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	switch cipher {
	case cipherDPAPI:
		return protLocal(plain)
	case cipherAESGCM:
		return sealAES(keyPath, plain)
	}
	return nil, fmt.Errorf("secretbin: 未知密文档 %q（文件可能来自更新版本）", cipher)
}

// decrypt 按档解密（keyPath 仅 AES 档用）。
func decrypt(ctx context.Context, cipher, keyPath string, blob []byte) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	switch cipher {
	case cipherDPAPI:
		return unprotLocal(blob)
	case cipherAESGCM:
		return openAES(keyPath, blob)
	}
	return nil, fmt.Errorf("secretbin: 未知密文档 %q（文件可能来自更新版本）", cipher)
}

// maskOf 掩码：值短于 8 位整条打码（尾 4 位对短值等于全暴露）；够长才留尾 4 位，
// 让用户认出「这条是不是我要的那把」而不泄出可用密钥。
func maskOf(v string) string {
	r := []rune(v)
	if len(r) < 8 {
		return strings.Repeat("•", 6)
	}
	return strings.Repeat("•", 6) + string(r[len(r)-4:])
}

// validName 键名约束：非空、无空白与控制字符、无 `:`（会和 `secret:` 前缀读混）。
func validName(name string) error {
	switch {
	case name == "":
		return errors.New("secretbin: 密钥名不可为空")
	case name != strings.TrimSpace(name):
		return fmt.Errorf("secretbin: 密钥名 %q 首尾不可含空白", name)
	case len(name) > maxNameLen:
		return fmt.Errorf("secretbin: 密钥名过长（上限 %d 字节）", maxNameLen)
	}
	if strings.ContainsAny(name, ": \t\r\n") {
		return fmt.Errorf("secretbin: 密钥名 %q 不可含空白或冒号（引用写作 secret:<名>）", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("secretbin: 密钥名 %q 不可含控制字符", name)
		}
	}
	return nil
}

// ctxErr 取消传播（本地小文件 IO 很快，但契约上仍尊重调用方 ctx）。
func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return errors.New("secretbin: nil context")
	}
	return ctx.Err()
}

// randBytes 随机字节（AES 密钥与 nonce 共用；错误不吞）。
func randBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("secretbin: 取随机数: %w", err)
	}
	return b, nil
}
