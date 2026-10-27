package uigui

// history_exchange.go 会话进出的 GUI 出口（DESIGN §5.16）：左栏标题行两颗文字钮——
// 「导入」拿一个外部文件、「导出」拿一个落点；选完都只投一条内核命令（`/import <路径>`、
// `/export --path <路径>`），与键入同路径。
//
// 壳内不搬运：认格式、防覆盖、写半截删掉、回执怎么写，全在命令面（铁律 5）。这里唯一
// 多做的事是给保存框预填一个由标题派生的文件名——那是「界面该替用户填的默认值」，
// 与内核口径共用一个函数（port.FileStem），不会分叉。

import (
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

// exchangeSaveFilters 导出侧过滤器：**只列这一条路真正会写的格式**。
//
// 界面上的「导出」固定走缺省的无损归档（带附件字节、能原样导回）。若在这里列出
// `*.json` / `*.jsonl`，用户选了「ChatGPT 会话」却拿到一份 aqx 字节——过滤器就成了一句
// 不会兑现的承诺。换格式是命令行的事（`/export --format … --path …`），界面不替他挑。
func exchangeSaveFilters() []platform.FileFilter {
	return []platform.FileFilter{
		{Label: "Aquarius 归档 (*.aqx)", Pattern: "*.aqx"},
	}
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
// 预填 aqx 与缺省导出格式一致：用户直接点「保存」就得到一份能原样导回的归档；想换
// 格式就在框里改扩展名再配 `--format`（界面这条路固定走缺省格式，换格式走命令行）。
func (st *historyState) requestExport(u *UI) {
	name := port.FileStem(st.currentTitle())
	if name == "" {
		name = "aquarius-session"
	}
	u.plat.RequestSaveFile(platform.SaveRequest{
		Purpose: platform.PurposeExchangeExport,
		Title:   "导出会话",
		Name:    name + ".aqx",
		DefExt:  ".aqx",
		Filters: exchangeSaveFilters(),
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
				st.sendCmd(u, "import", p.path)
			case platform.PurposeExchangeExport:
				// 走 --path：用户在对话框里挑的就是最终落点，不该再被塞进导出目录。
				st.sendCmd(u, "export", "--path", p.path)
			}
		default:
			return
		}
	}
}

// sendCmd 投一条内核命令（与键入同路径：经 u.inCh；缓冲满丢弃）。
func (st *historyState) sendCmd(u *UI, name string, args ...string) {
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: name, Args: args}}:
	default:
	}
}
