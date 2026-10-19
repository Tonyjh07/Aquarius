//go:build !windows

package secretbin

// 非 Windows 平台的默认档 = AES-256-GCM + 同目录 secrets.key（D151/D161）。
// DPAPI 是 Windows 独占（crypt32.dll），这里没有等价物可借，遂回落到「密钥文件
// 0600 + 整段加密」：挡住离线备份/误提交，不假装等同于绑定账户的 DPAPI（D151
// 评估表里已写明这档的边界）。

import "errors"

// platformCipher 非 Windows 默认档 = AES-GCM。
func platformCipher() string { return cipherAESGCM }

// DPAPI 是 Windows 独占：这里两个桩只为「编译得过来」而存在——密文档名带 dpapi 的
// 文件（从 Windows 拷来的 profile）在本平台一律报「本平台不支持」，再由上层包成
// ErrUnreadable → 警告 + 回落同名环境变量。**不能**在这里换成 AES 去解（钥匙不同，
// 只会把「解不开」变成「解出一堆垃圾」）。

func protLocal([]byte) ([]byte, error) {
	return nil, errors.New("DPAPI 密文档仅 Windows 可用")
}

func unprotLocal([]byte) ([]byte, error) {
	return nil, errors.New("DPAPI 密文档仅 Windows 可用（文件可能来自 Windows 机器）")
}
