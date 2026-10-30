package port

import (
	"context"
	"io"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// ExchangeFormat 一种可导出/导入的会话格式（§5.16，D242）。
//
// 命令面的 `--format` 取值、建议文件名后缀、回执与帮助文本都读这里，不另抄一份。
type ExchangeFormat struct {
	// Name 命令面标识：aqx | chatgpt | openai | sillytavern | openwebui。
	Name string
	// Ext 建议扩展名（含点；既有 `.aqx` 也有 `.json`/`.jsonl`）。
	Ext string
	// Label 用户面名称（回执与用法提示里给用户看的那个词）。
	Label string
	// Lossy 是否丢弃树事实：无损格式必须能原样往返，有损格式按别人的规范如实有损，
	// 两者共用一层解析但绝不互相冒充。
	Lossy bool
	// LossNote 丢什么（一句话；帮助与回执共用，用户据此知道这次搬运少了什么）。
	LossNote string
	// NoExport 只认不写：openwebui 与 chatgpt 同族（都是树形），读进来不难，但我们只维护
	// 一份树形出口，不产出第二套同义格式。
	NoExport bool
}

// MultiExportStem 多会话导出的缺省文件名（没有单一会话标题可用）。
//
// 放在端口：命令面 `--all` 的缺省落点与界面批量导出的预填名是同一个名字，两处各写一份
// 迟早会分叉（界面改了一档，命令面照旧）。
const MultiExportStem = "aquarius-backup"

// ExchangeEntry 归档里的一个会话（**索引条目**：只给展示与选择用，不装树）。
type ExchangeEntry struct {
	// Index 清单序号，**1 起**（--pick 用它；顺序 = manifest 里的顺序）。
	Index int
	// ID 源会话 ID（回执溯源；导入时全部重新分配）。
	ID string
	// Title 清单里记的标题：只用于在勾选面板上认人，导入以 tree.json 为准
	// （改了树的标题不会回头改归档——这是备份，不是活文档）。
	Title string
	// Nodes 节点数（不含 Root），只供展示。
	Nodes int
}

// ExchangeReport 一次进出的回执：数量、字节、丢了什么。
//
// Dropped 里是逐项可读的事实行（如「附件 3 个（源文件不含字节）」），不是错误——错误走返回的
// error。回执的作用是让用户一眼看出少了什么，而不是把残缺装成完整。
type ExchangeReport struct {
	Format string
	// Nodes 写出的节点数 / 读入的节点数（多会话时是总和）。
	Nodes int
	// Bytes 容器或文件字节数；流式写出的格式由实现累计写入量。
	Bytes int64
	// Dropped 逐项可读的事实行（没有带过去 / 没有带进来的东西）。
	Dropped []string
	// Entries 这次涉及的会话清单：**多会话容器才填**（单会话与线性格式留空）。
	// 回执据此决定用「一份会话」还是「N 个会话 + 逐条标题」的版式，不靠数量反猜。
	Entries []ExchangeEntry
}

// SessionExporter `/export`：把**一组**会话按指定格式写进 w（§5.16）。
//
// 消费方 = app.Session。为什么不复用 OutputAdapter：那个端口的语义是「每提交一条回答就投递
// 一次」（§7.2 的扇出），而导出是用户点一次、作用于整棵树的动作；塞进同一个接口只会让
// 两边各自长出「什么时候不该触发」的分支。§14 的「导出文件输出器」指的是前者（把回答镜像到
// 文件），与 `/export` 是两件事，各自保留。
type SessionExporter interface {
	// Formats 全部可写格式（表序即用法提示与帮助文本的顺序）。
	Formats() []ExchangeFormat
	// Export 把 cs 按 format 写出。单会话路径就是 len(cs) == 1。
	//
	// **多会话只对 aqx 成立**：chatgpt / openai / sillytavern 的容器按别人的规范只能装一条
	// 会话链，收到多于一个时由实现报错——硬塞进去只会让别人的工具读到一份半成品。
	Export(ctx context.Context, cs []*conversation.Conversation, format string, w io.Writer) (ExchangeReport, error)
}

// SessionImporter `/import`：读一个外部文件，变成一组**待入库**的树。
//
// 外部文件是不可信输入：实现先解析成 DTO，再走 conversation.Restore 的统一自检（重复 ID、
// 悬空父、Head 不在树一律拒绝，不「尽力修复」）。落成新会话与节点 ID 重分配是消费方的事，
// 不在端口里做——端口只负责「文件 → 树」。
type SessionImporter interface {
	// Detect 按文件名与文件头判断这是谁的格式；认不出返回 false（调用方报「认不出这份文件」
	// 并列出可接受的格式）。
	Detect(name string, head []byte) (ExchangeFormat, bool)
	// Import 读入并自检。pick 是 **1 起的清单序号**（空 = 全部）；非容器格式只有一项，
	// pick 只接受空或 {1}。返回顺序 = 清单顺序（升序），不按 pick 的书写顺序。
	//
	// 读侧的上限（每会话节点数、正文总字节、容器条目数与展开总量）由实现自己守，
	// 超限报错而不是静默截断。
	Import(ctx context.Context, r io.Reader, f ExchangeFormat, pick []int) ([]*conversation.Conversation, ExchangeReport, error)
}

// ExchangeIndexer 列出一份**容器**里的会话清单（界面勾选面板与批量入口用）。
//
// 单独一个接口而不是塞进 SessionImporter：界面要的是「按路径先看一眼有什么」，拿不到
// 那一步就只能自己解 ZIP——壳里再写一遍容器解析，正是铁律 5 要挡的那类分叉。
type ExchangeIndexer interface {
	// Index 只读容器的清单（aqx 的 manifest.json），不解析树。
	//
	// 不是容器、版本更高、旧单树形状、清单坏掉都返回错误：调用方（界面）据此退回普通导入，
	// 由导入侧给出真正的格式原因，不在这里猜。
	Index(ctx context.Context, path string) ([]ExchangeEntry, error)
}

// FileStem 会话标题 → 可用作文件名的词干：路径分隔符与 Windows 保留字符换成 '-'、压掉首尾
// 空白与点、截到 60 字（超长标题在文件管理器里只剩一截，也躲开路径长度限制）。
//
// 放在端口而不是命令面：命令面的缺省落点与 GUI 保存框的建议文件名是同一个名字，两处各写一遍
// 迟早会分叉（一处换了保留字符表，另一处照旧）。
func FileStem(title string) string {
	const maxRunes = 60
	repl := func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t', 0:
			return '-'
		}
		return r
	}
	s := strings.Map(repl, strings.TrimSpace(title))
	s = strings.Trim(s, " .-")
	if r := []rune(s); len(r) > maxRunes {
		s = strings.Trim(string(r[:maxRunes]), " .-")
	}
	return s
}

// ExchangeFormats 四种格式的静态事实（§5.16 的格式表，D242）。
//
// 放在端口而不在适配器：命令面的用法提示、导入识别失败时的「支持这几种」文案都要用它，
// 而那时可能连适配器都没装配。适配器的 Formats() 由这张表派生，两处不会分叉。
func ExchangeFormats() []ExchangeFormat {
	return []ExchangeFormat{
		{Name: "aqx", Ext: ".aqx", Label: "Aquarius 归档"},
		{
			Name: "chatgpt", Ext: ".json", Label: "ChatGPT 会话", Lossy: true,
			LossNote: "附件只留一条说明、思考与工具调用不带、注入节点降级为 system、消息的模型/用量等元数据不带",
		},
		{
			Name: "openai", Ext: ".json", Label: "OpenAI 消息列表", Lossy: true,
			LossNote: "只取当前那条链，旁支、思考、工具与附件不带；注入节点降级为 system",
		},
		{
			Name: "sillytavern", Ext: ".jsonl", Label: "SillyTavern 对话", Lossy: true,
			LossNote: "只取当前那条链，旁支、思考、工具与附件不带；注入节点降级为 system；对方的世界书/正则/作者注不装",
		},
		{
			Name: "openwebui", Ext: ".json", Label: "Open WebUI 会话", Lossy: true, NoExport: true,
			LossNote: "只认不写：要喂 Open WebUI 用 chatgpt 那份",
		},
	}
}
