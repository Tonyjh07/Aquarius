package secretbin

// AES-256-GCM 档（D151）：密钥是**同目录 secrets.key** 里 32 字节随机串（0600，
// 首次写入才生成）。密文档在**任何平台都编译**——平台选择走 secretbin.cipherFor，
// Windows 上测试可覆盖到本档，AES 代码不会成为「只在 Linux 编过、本机从未执行」
// 的死代码（D161）。
//
// 防的是什么、不防什么：密文文件被拷走/同步/提交时，没有同目录的 key 解不开；
// **但 key 与密文同目录** ⇒ 只挡「离线备份」这一档，挡不住能读到同目录的进程。
// 这是 D151 评估表里已写明并接受的边界，不夸大到「与 DPAPI 同档」。

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// aesKeyLen AES-256 密钥长度（字节）；nonceLen GCM 标准 nonce 长度。
const (
	aesKeyLen = 32
	nonceLen  = 12
)

// sealAES 加密：[nonce 12B][密文+GCM tag]。密钥文件不存在则现生成（首次写入）。
func sealAES(keyPath string, plain []byte) ([]byte, error) {
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	aead, err := aeadOf(key)
	if err != nil {
		return nil, err
	}
	nonce, err := randBytes(nonceLen)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, nonceLen+len(plain)+aead.Overhead())
	out = append(out, nonce...)
	return aead.Seal(out, nonce, plain, nil), nil
}

// openAES 解密。密钥文件缺失 = 解不开（**绝不现生成**——那会拿一把新钥匙去开旧锁，
// 只会把「解不开」变成「解出一堆垃圾」）。
func openAES(keyPath string, blob []byte) ([]byte, error) {
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("缺少密钥文件 %s（本机换过 profile、或文件被单独拷走）", keyPath)
	}
	if err != nil {
		return nil, fmt.Errorf("读取密钥文件 %s: %w", keyPath, err)
	}
	if len(key) != aesKeyLen {
		return nil, fmt.Errorf("密钥文件 %s 长度异常（%d 字节，应为 %d）", keyPath, len(key), aesKeyLen)
	}
	aead, err := aeadOf(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < nonceLen+aead.Overhead() {
		return nil, errors.New("密文长度不足（文件被截断或不是本档密文）")
	}
	nonce, ct := blob[:nonceLen], blob[nonceLen:]
	plain, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("密文校验失败（密钥不对或内容被改动）: %w", err)
	}
	return plain, nil
}

// loadOrCreateKey 读密钥文件；不存在则生成 32 随机字节并以 0600 **独占创建**落地。
// 并发首建时后到者读走先到者写下的那把（O_EXCL 的 EEXIST 即此意）。
func loadOrCreateKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) != aesKeyLen {
			return nil, fmt.Errorf("密钥文件 %s 长度异常（%d 字节，应为 %d）", path, len(b), aesKeyLen)
		}
		return b, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("读取密钥文件 %s: %w", path, err)
	}
	key, err := randBytes(aesKeyLen)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) { // 并发首建：用别人写下的那把
		other, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, fmt.Errorf("读取密钥文件 %s: %w", path, rerr)
		}
		if len(other) != aesKeyLen {
			return nil, fmt.Errorf("密钥文件 %s 长度异常", path)
		}
		return other, nil
	}
	if err != nil {
		return nil, fmt.Errorf("创建密钥文件 %s: %w", path, err)
	}
	if _, werr := f.Write(key); werr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("写入密钥文件 %s: %w", path, werr)
	}
	if cerr := f.Close(); cerr != nil {
		return nil, fmt.Errorf("关闭密钥文件 %s: %w", path, cerr)
	}
	return key, nil
}

// aeadOf 由 32 字节密钥构造 GCM AEAD。
func aeadOf(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("构造 AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("构造 GCM: %w", err)
	}
	return aead, nil
}
