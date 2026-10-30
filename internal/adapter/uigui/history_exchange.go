package uigui

// history_exchange.go 会话进出的 GUI 出口（§5.16）：左栏标题行两颗文字钮——
// 「导入」拿一个外部文件、「导出」拿一个落点；选完都只投一条内核命令（`/import <路径>`、
// `/export --path <路径>`），与键入同路径。
//
// 壳内不搬运：认格式、防覆盖、写半截删掉、回执怎么写，全在命令面（铁律 5）。这里唯一
// 多做的事是给保存框预填一个由标题派生的文件名——那是「界面该替用户填的默认值」，
// 与内核口径共用一个函数（port.FileStem），不会分叉。

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// exchangeOpenFilters 导入侧文件框的过滤器：五档格式的扩展名。认不认得出由命令面按
// 内容判，过滤器只帮用户少翻文件，不当作格式保证（`.json` 既可能是 chatgpt 也可能是
// openai 或 openwebui）。
func exchangeOpenFilters() []platform.FileFilter {
	return []platform.FileFilter{
		{Label: "会话文件 (*.aqx;*.json;*.jsonl)", Pattern: "*.aqx;*.json;*.jsonl"},
		{Label: "Aquarius 归档 (*.aqx)", Pattern: "*.aqx"},
		{Label: "JSON / JSONL (*.json;*.jsonl)", Pattern: "*.json;*.jsonl"},
		{Label: "所有文件 (*.*)", Pattern: "*.*"},
	}
}

// exchangeSaveFilters 导出侧过滤器：**按端口格式表列出全部可写格式**，一档一种。
//
// 这份列表就是「保存类型」下拉框的内容，档位与格式一一对应：用户选中哪一档，导出就写哪
// 种格式（§5.16）。所以它必须与 `port.ExchangeFormats()` 里 `NoExport == false` 的那些
// 条目严格同序、同数——序号错一位就是「选了 SillyTavern、拿到的却是 aqx 字节」。
//
// 只读格式（`openwebui`）不列：它没有第二套同义出口，列出来就是一句不会兑现的承诺。
func exchangeSaveFilters() []platform.FileFilter {
	out := make([]platform.FileFilter, 0, len(exportFormats()))
	for _, f := range exportFormats() {
		out = append(out, platform.FileFilter{
			Label:   f.Label + " (*" + f.Ext + ")",
			Pattern: "*" + f.Ext,
		})
	}
	return out
}

// exportFormats 可写格式（表序即保存框档序）。
func exportFormats() []port.ExchangeFormat {
	var out []port.ExchangeFormat
	for _, f := range port.ExchangeFormats() {
		if !f.NoExport {
			out = append(out, f)
		}
	}
	return out
}

// exportFormatAt 保存框档位 → 格式（越界 = 认不出，交调用方回落）。
func exportFormatAt(idx int) (port.ExchangeFormat, bool) {
	formats := exportFormats()
	if idx < 0 || idx >= len(formats) {
		return port.ExchangeFormat{}, false
	}
	return formats[idx], true
}

// exportFormatByExt 按落点扩展名反推格式（非 Windows 的降级依据：那里的对话框只回路径）。
//
// `.json` 有多档（chatgpt 与 openai 同扩展名），取**表里第一个**匹配的那档——想精确选
// 走命令行 `/export --format openai`。反推不出来（扩展名不认识）给 aqx：那是缺省，也是
// 唯一无损的一档，猜错的代价最小。
func exportFormatByExt(path string) port.ExchangeFormat {
	ext := strings.ToLower(filepath.Ext(path))
	for _, f := range exportFormats() {
		if strings.EqualFold(f.Ext, ext) {
			return f
		}
	}
	if f, ok := exportFormatAt(0); ok {
		return f
	}
	return port.ExchangeFormat{Name: "aqx", Ext: ".aqx"}
}

// withFormatExt 让落点扩展名与所选格式一致。
//
// Win32 的 lpstrDefExt 是静态的：用户选了「SillyTavern 对话」却只打了文件名，对话框补上
// 的仍是 .aqx——格式与扩展名对不上，文件就是在骗人。所以这里按格式改正：
//   - 旧的扩展名**是某个已知格式的**（.aqx/.json/.jsonl）→ 换成本档的；
//   - 不是（`2026.10.28` 这种点名、或无扩展名）→ 直接补上，不切掉用户写的东西。
func withFormatExt(path string, f port.ExchangeFormat) string {
	if f.Ext == "" {
		return path
	}
	ext := filepath.Ext(path)
	if strings.EqualFold(ext, f.Ext) {
		return path
	}
	if ext != "" && knownFormatExt(ext) {
		return strings.TrimSuffix(path, ext) + f.Ext
	}
	return path + f.Ext
}

// knownFormatExt 这个扩展名是不是格式表里的某一档（含只读的 openwebui，它也可能出现在
// 用户手打的落点里）。
func knownFormatExt(ext string) bool {
	for _, f := range port.ExchangeFormats() {
		if strings.EqualFold(f.Ext, ext) {
			return true
		}
	}
	return false
}

// requestImport 打开单选文件框（导入侧）。
func (st *historyState) requestImport(u *UI) {
	u.plat.RequestOpenFile(platform.OpenRequest{
		Purpose: platform.PurposeExchangeImport,
		Title:   "导入会话",
		Filters: exchangeOpenFilters(),
	})
}

// requestExport 打开保存框（导出侧），预填由当前会话标题派生的文件名。
//
// 预填 aqx 与缺省格式一致：用户直接点「保存」就得到一份能原样导回的归档；想换格式就在
// 「保存类型」里换一档——档位随结果回来，命令面照它写 `--format`（§5.16）。
func (st *historyState) requestExport(u *UI) {
	name := port.FileStem(st.currentTitle())
	if name == "" {
		name = "aquarius-session"
	}
	def, _ := exportFormatAt(0)
	u.plat.RequestSaveFile(platform.SaveRequest{
		Purpose:     platform.PurposeExchangeExport,
		Title:       "导出会话",
		Name:        name + def.Ext,
		DefExt:      strings.TrimPrefix(def.Ext, "."),
		Filters:     exchangeSaveFilters(),
		FilterIndex: 0,
	})
}

// drainFilePicks 帧首取走文件框结果并投命令（通道满即丢，同其余图内命令口径）。
// u.filePicks 为 nil（headless 测试的瘦 UI）时什么都不做。
func (st *historyState) drainFilePicks(u *UI) {
	if u.filePicks == nil {
		return
	}
	for {
		select {
		case p := <-u.filePicks:
			switch p.purpose {
			case platform.PurposeExchangeImport:
				st.startImport(u, p.path)
			case platform.PurposeExchangeExport:
				// 走 --path：用户在对话框里挑的就是最终落点，不该再被塞进导出目录。
				// 格式按档位定（Windows 报得出），报不出才按扩展名回落。
				f, ok := exportFormatAt(p.filterIndex)
				if !ok {
					f = exportFormatByExt(p.path)
				}
				st.sendCmd(u, "export",
					"--format", f.Name,
					"--path", withFormatExt(p.path, f))
			case platform.PurposeExchangeBatchExport:
				// 落点恒定写 aqx（对话框只一档），ID 取按下「导出」那刻的快照：对话框是异步
				// 的，这期间用户可能已经退出选择态，sel 那时已经清空。
				if ids := st.selPending; len(ids) > 0 {
					st.sendCmd(u, "export",
						"--ids", strings.Join(ids, ","),
						"--path", p.path)
				}
				st.selPending = nil
				st.exitSelMode()
			}
		default:
			return
		}
	}
}

// startImport 选完导入文件后的分流：先向内核要清单，多于一个会话才弹勾选面板。
//
// 只有一项（非 aqx，或只装了一个会话的 aqx）直接投——弹一个只有一行的勾选框只是多一次
// 点击。取清单失败（文件损坏 / 不是归档）也直接投：命令面会把原因说清楚，界面不替它
// 编一个「读不出来」的说法（壳内不认格式，铁律 5）。
func (st *historyState) startImport(u *UI, path string) {
	if u.opts.Indexer != nil {
		if entries, err := u.opts.Indexer.Index(context.Background(), path); err == nil && len(entries) > 1 {
			st.openImportPick(path, entries)
			return
		}
	}
	st.sendCmd(u, "import", path)
}

// sendCmd 投一条内核命令（与键入同路径：经 u.inCh；缓冲满丢弃）。
func (st *historyState) sendCmd(u *UI, name string, args ...string) {
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: name, Args: args}}:
	default:
	}
}
