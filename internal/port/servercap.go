package port

import "strings"

// 端点侧的服务端能力（D307）：这类功能要在请求里显式声明，具体做事的是服务端。
//
// 与 ModelCaps 不是一回事：那个说的是「这个模型会不会」，由协议与模型决定；这个说的是
// 「这个端点认不认」，同一个模型挂在官方端点上认、挂在只做转发的网关上就可能不认。
// 名字是配置 `server_caps` 数组的取值，也是自动记录里 `unsupported_server_caps` 的键。
const (
	ServerCapTitle     = "title"      // 会话标题由模型顺带生成
	ServerCapReadURL   = "read_url"   // 服务端抓取并阅读用户贴的链接
	ServerCapFiles     = "files"      // 文件上传后由服务端解析（扫描件 OCR、富版式）
	ServerCapWebSearch = "web_search" // 服务端联网搜索
	ServerCapSafety    = "safety"     // 服务端内容审核
)

// ServerCaps 一组服务端能力位。零值 = 一项都不认。
type ServerCaps struct {
	Title, ReadURL, Files, WebSearch, Safety bool
}

// ServerCapNames 全部合法名字，顺序固定（配置写回、报错文案与界面共用同一份顺序）。
func ServerCapNames() []string {
	return []string{ServerCapTitle, ServerCapReadURL, ServerCapFiles, ServerCapWebSearch, ServerCapSafety}
}

// IsServerCap 名字是否在词表内（大小写不敏感，两侧空白忽略）。
func IsServerCap(name string) bool {
	switch normalizeCapName(name) {
	case ServerCapTitle, ServerCapReadURL, ServerCapFiles, ServerCapWebSearch, ServerCapSafety:
		return true
	}
	return false
}

// ServerCapsFromNames 名字集合 → 能力位；认不出的名字忽略（调用方在校验期已拦过，
// 这里是运行期读文件时的兜底：手改配置写错一个词，不该让整个进程起不来）。
func ServerCapsFromNames(names []string) ServerCaps {
	var c ServerCaps
	for _, n := range names {
		c = c.With(n)
	}
	return c
}

// With 加上一项（名字认不出时原样返回）。
func (c ServerCaps) With(name string) ServerCaps {
	switch normalizeCapName(name) {
	case ServerCapTitle:
		c.Title = true
	case ServerCapReadURL:
		c.ReadURL = true
	case ServerCapFiles:
		c.Files = true
	case ServerCapWebSearch:
		c.WebSearch = true
	case ServerCapSafety:
		c.Safety = true
	}
	return c
}

// And 两项都认才算认（降级链取交用，D173 的 chainCaps 同规则）。
func (c ServerCaps) And(o ServerCaps) ServerCaps {
	return ServerCaps{
		Title:     c.Title && o.Title,
		ReadURL:   c.ReadURL && o.ReadURL,
		Files:     c.Files && o.Files,
		WebSearch: c.WebSearch && o.WebSearch,
		Safety:    c.Safety && o.Safety,
	}
}

// Drop 去掉一项（名字认不出时原样返回）。自动记录下来的不认项靠这个方法剔除。
func (c ServerCaps) Drop(name string) ServerCaps {
	switch normalizeCapName(name) {
	case ServerCapTitle:
		c.Title = false
	case ServerCapReadURL:
		c.ReadURL = false
	case ServerCapFiles:
		c.Files = false
	case ServerCapWebSearch:
		c.WebSearch = false
	case ServerCapSafety:
		c.Safety = false
	}
	return c
}

// Has 是否认这一项（消费方发字段前问这一句）。
func (c ServerCaps) Has(name string) bool {
	switch normalizeCapName(name) {
	case ServerCapTitle:
		return c.Title
	case ServerCapReadURL:
		return c.ReadURL
	case ServerCapFiles:
		return c.Files
	case ServerCapWebSearch:
		return c.WebSearch
	case ServerCapSafety:
		return c.Safety
	}
	return false
}

// Names 已开启的名字，顺序与 ServerCapNames 一致（写回与展示共用，避免两种顺序）。
func (c ServerCaps) Names() []string {
	out := make([]string, 0, 5)
	for _, n := range ServerCapNames() {
		if c.Has(n) {
			out = append(out, n)
		}
	}
	return out
}

// normalizeCapName 名字归一：小写、去两侧空白。
func normalizeCapName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
