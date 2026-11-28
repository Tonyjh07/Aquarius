package pluginapi

import "context"

// secrets.go 密钥供给面（plugin-guide.md §4）：方向与 Host.Secrets() 相反——
// 这里是内核调插件取密钥（keychain 类实现），那边是插件请内核解析。
//
// 一个名字一次解析，不提供列举：能列出来的密钥清单本身就是一份泄密面。

// SecretProvider 密钥后端。
// 兼容承诺：改签名会破坏所有声明 provides: secrets 的插件与内核的密钥取用路径。
type SecretProvider interface {
	// Get 按名取明文。名字由配置里的 secret:<name> 引用给出；取不到要返回错误，
	// 不要用空串兼作失败（空串可能是合法的密钥值）。
	Get(ctx context.Context, name string) (string, error)
}
