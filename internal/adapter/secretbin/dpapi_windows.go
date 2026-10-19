//go:build windows

package secretbin

// DPAPI 档（D151/D161）：Windows 上默认用它——CryptProtectData 把整表绑到**当前用户
// 账户**（无口令、无需额外密钥文件）。纯 syscall（crypt32.dll / kernel32.dll），
// **不引入 cgo**，守住单二进制交付。
//
// 挂 CRYPTPROTECT_UI_FORBIDDEN：本机密钥库是后台路径，绝不允许弹系统对话框（无
// UI 的启动期一旦弹窗就等于挂死）。还原出的 blob 由系统分配（LocalAlloc），
// 用 LocalFree 释放——漏释放就是每次读泄漏一份密钥内存。

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// platformCipher Windows 默认档 = DPAPI（AES 档仍编译在内，测试可覆盖 cipherFor 跑到）。
func platformCipher() string { return cipherDPAPI }

// cryptprotectUIForbidden 调用不得弹 UI（CRYPTPROTECT_UI_FORBIDDEN）。
const cryptprotectUIForbidden = 0x1

var (
	crypt32   = syscall.NewLazyDLL("crypt32.dll")
	kernel32  = syscall.NewLazyDLL("kernel32.dll")
	procPrtod = crypt32.NewProc("CryptProtectData")
	procUnprt = crypt32.NewProc("CryptUnprotectData")
	procFree  = kernel32.NewProc("LocalFree")
)

// dataBlob DATA_BLOB（与 C 布局一致：长度 + 指针）。
type dataBlob struct {
	cbData uint32
	pbData *byte
}

// protLocal 加密为 DPAPI blob。
func protLocal(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, fmt.Errorf("DPAPI: 空明文不可加密")
	}
	in := dataBlob{cbData: uint32(len(plain)), pbData: &plain[0]}
	var out dataBlob
	// 参数：pDataIn, szDataDescr(NULL), pOptionalEntropy(NULL), pvReserved(0),
	// pPromptStruct(NULL), dwFlags, pDataOut。
	r, _, err := procPrtod.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0,
		cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)),
	)
	runtime.KeepAlive(plain)
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData 失败: %w", err)
	}
	return takeOutBlob(out)
}

// unprotLocal 还原 DPAPI blob。
func unprotLocal(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, fmt.Errorf("DPAPI: 空密文不可还原")
	}
	in := dataBlob{cbData: uint32(len(blob)), pbData: &blob[0]}
	var out dataBlob
	// 参数：pDataIn, ppszDataDescr(NULL), pOptionalEntropy(NULL), pvReserved(0),
	// pPromptStruct(NULL), dwFlags, pDataOut。
	r, _, err := procUnprt.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0,
		cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)),
	)
	runtime.KeepAlive(blob)
	if r == 0 {
		// 换机拷 profile / 换了账户的典型症状就落在这里：具体原因交给上层统一措辞
		// （调用方按 ErrUnreadable 警告 + 回落同名环境变量）。
		return nil, fmt.Errorf("CryptUnprotectData 失败（本机账户与写入时不是同一个，或文件已损坏）: %w", err)
	}
	return takeOutBlob(out)
}

// takeOutBlob 拷出系统分配的输出 blob 并立即释放（不把密钥留在 LocalAlloc 内存里）。
func takeOutBlob(out dataBlob) ([]byte, error) {
	if out.pbData == nil || out.cbData == 0 {
		return nil, fmt.Errorf("DPAPI 返回空结果")
	}
	defer func() { _, _, _ = procFree.Call(uintptr(unsafe.Pointer(out.pbData))) }()
	buf := make([]byte, int(out.cbData))
	copy(buf, unsafe.Slice(out.pbData, int(out.cbData)))
	return buf, nil
}
