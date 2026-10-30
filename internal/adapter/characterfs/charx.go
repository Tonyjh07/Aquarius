package characterfs

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// `.charx`（ZIP 容器，§5.17）：根目录的 `card.json` 是 V3 对象，图标取 `type=icon`
// 的资源。整个原包按字节留档（`source.charx`），表情 / 背景 / 语音这些本版不用的资产
// 不能悄悄丢——将来导出也有原料。

const (
	// maxZipEntries 压缩包条目上限（§5.17）。
	maxZipEntries = 2048
	// maxZipBytes 解压后总量上限。
	maxZipBytes = 128 << 20
	// maxIconBytes 图标资产的读取上限（图标本就小，超限基本是拿错了条目）。
	maxIconBytes = 32 << 20
)

// parseCharxCard 解析一个 `.charx`（或套着 `.charx` 的发行包）。
func parseCharxCard(data []byte) (ParsedCard, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ParsedCard{}, fmt.Errorf("压缩包读不出：%w", err)
	}
	if err := gateZip(zr); err != nil {
		return ParsedCard{}, err
	}
	files, byBase := indexZip(zr)
	cardFile := lookupZip(files, "card.json")
	if cardFile == nil {
		return ParsedCard{}, errors.New("压缩包根目录没有 card.json：这像是发行包，请导入其中的 .charx")
	}
	raw, err := readZipEntry(cardFile, maxCardJSONBytes)
	if err != nil {
		return ParsedCard{}, err
	}
	out, d, err := parseCardJSON(raw)
	if err != nil {
		return ParsedCard{}, err
	}
	out.Info.Source = "charx"
	out.RawJSON = raw
	out.Archive = data

	if uri, warn := pickIconURI(d.Assets); warn != "" {
		out.Info.Warnings = append(out.Info.Warnings, warn)
	} else if uri != "" {
		if resolved, warn := resolveAssetURI(uri, byBase); warn != "" {
			out.Info.Warnings = append(out.Info.Warnings, warn)
		} else if resolved != "" {
			if f := lookupZip(files, resolved); f != nil {
				icon, err := readZipEntry(f, maxIconBytes)
				if err != nil {
					out.Info.Warnings = append(out.Info.Warnings, "图标读不出："+err.Error())
				} else {
					out.Avatar = icon
				}
			} else {
				out.Info.Warnings = append(out.Info.Warnings, "图标 "+resolved+" 在包内找不到，已跳过")
			}
		}
	}
	return out, nil
}

// pickIconURI 选图标资产的 URI：`type=icon` 里优先 `name=main`，否则第一个。
func pickIconURI(assets []rawAsset) (uri, warn string) {
	first := ""
	for _, a := range assets {
		if !strings.EqualFold(a.Type, "icon") {
			continue
		}
		if strings.EqualFold(a.Name, "main") {
			return a.URI, ""
		}
		if first == "" {
			first = a.URI
		}
	}
	return first, ""
}

// resolveAssetURI 把资产 URI 解析成包内路径（§5.17 的三种写法 + `ccdefault:` + 网络地址）：
//   - `embeded://` / `embedded://` / `__asset:`：前缀之后即包内相对路径（规范里少一个 d）；
//   - `ccdefault:`：用默认图标（`.charx` 即无头像）；
//   - `http(s)://`：不下载，记警告；
//   - `/user/images/<卡名>/x.webp`：SillyTavern 私有路径，按 basename 映射到包内同名资产；
//   - 其余：当成包内相对路径。
func resolveAssetURI(uri string, byBase map[string][]string) (string, string) {
	u := strings.TrimSpace(uri)
	low := strings.ToLower(u)
	switch {
	case strings.HasPrefix(low, "embeded://"):
		return u[len("embeded://"):], ""
	case strings.HasPrefix(low, "embedded://"):
		return u[len("embedded://"):], ""
	case strings.HasPrefix(low, "__asset:"):
		return u[len("__asset:"):], ""
	case strings.HasPrefix(low, "ccdefault:"):
		return "", ""
	case strings.HasPrefix(low, "http://"), strings.HasPrefix(low, "https://"):
		return "", "资产指向网络地址，本版不下载：" + u
	case strings.HasPrefix(u, "/user/images/"):
		names := byBase[strings.ToLower(path.Base(u))]
		if len(names) == 0 {
			return "", "资产路径 " + u + " 在包内找不到同名文件，已跳过"
		}
		return names[0], ""
	default:
		return u, ""
	}
}

// gateZip 压缩包门禁（沿用技能安装口径，§5.15）：条目数与展开总量设上限；绝对路径、盘符、
// `..`、符号链接与加密条目一律拒绝。超限报错，不截断。
func gateZip(zr *zip.Reader) error {
	if len(zr.File) > maxZipEntries {
		return fmt.Errorf("压缩包条目数 %d 超过 %d 上限", len(zr.File), maxZipEntries)
	}
	var total uint64
	for _, f := range zr.File {
		if err := safeZipName(f.Name); err != nil {
			return err
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("压缩包里有符号链接 %q：拒绝", f.Name)
		}
		if f.Flags&0x1 != 0 { // bit 0 = 加密
			return fmt.Errorf("压缩包里有加密条目 %q：拒绝", f.Name)
		}
		total += f.UncompressedSize64
		if total > maxZipBytes {
			return fmt.Errorf("压缩包解压总量超过 %d MB 上限", maxZipBytes>>20)
		}
	}
	return nil
}

// safeZipName 校验压缩包条目路径（拒绝绝对路径、盘符与 `..`）。
func safeZipName(name string) error {
	n := strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(n, "/") {
		return fmt.Errorf("压缩包条目 %q 是绝对路径：拒绝", name)
	}
	if len(n) >= 2 && n[1] == ':' {
		return fmt.Errorf("压缩包条目 %q 带盘符：拒绝", name)
	}
	clean := path.Clean(n)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("压缩包条目 %q 越出目标目录：拒绝", name)
	}
	return nil
}

// indexZip 建两份索引：归一化路径 → 条目；basename → 归一化路径列表（私有路径映射用）。
func indexZip(zr *zip.Reader) (map[string]*zip.File, map[string][]string) {
	files := make(map[string]*zip.File, len(zr.File))
	byBase := map[string][]string{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		key := zipKey(f.Name)
		files[key] = f
		base := strings.ToLower(path.Base(strings.ReplaceAll(f.Name, `\`, "/")))
		byBase[base] = append(byBase[base], key)
	}
	return files, byBase
}

// zipKey 压缩包条目路径的归一化键（斜杠统一、大小写折叠）。
func zipKey(name string) string {
	return strings.ToLower(path.Clean(strings.ReplaceAll(name, `\`, "/")))
}

// lookupZip 按（大小写不敏感的）路径取条目。
func lookupZip(files map[string]*zip.File, name string) *zip.File { return files[zipKey(name)] }

// readZipEntry 读一个条目（按上限截断检测，避免谎报大小撑爆内存）。
func readZipEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("打开压缩包条目 %s: %w", f.Name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读压缩包条目 %s: %w", f.Name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("压缩包条目 %s 超过 %d MB 上限", f.Name, limit>>20)
	}
	return data, nil
}
