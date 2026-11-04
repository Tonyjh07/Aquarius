package i18n

// I6 的锁：测试期望值跟 key 走。
//
// 前三把锁都只扫**非测试**源码；测试侧留了一个同构的盲区——期望值按中文写死。默认语言是
// zh 时全绿，但「跟 key 走」的契约只对生产代码成立：一旦有人在 `AQUARIUS_UI_LANG=en` 下跑
// 回归（或以后加第三种语言），成片断言会碎，且碎因看着像文案改坏了。
//
// 判据（plan.md I6，写死、不留裁量）——`_test.go` 里**四类期望值位置**的中文字面量必须为 0：
//
//  1. `strings.Contains/ContainsAny/ContainsRune/Count/HasPrefix/HasSuffix/EqualFold/Compare`
//     与名为 `Equal` 的判定调用的实参（子串断言改成「从表里拼出期望值再比」）；
//  2. `==` / `!=` 的字符串字面量操作数；
//  3. 赋值/声明目标名以 `want` 开头（大小写不敏感）的字面量（`want := "…"` / `var want = "…"`）；
//  4. 复合字面量里键名以 `want` 开头的字段值（表驱动测试的 `want: "…"`，含 `wantErr` 一类）。
//
// 三类**不在锁内**：`t.Fatalf/Errorf` 等失败消息（开发面文案，语言约定就是中文，plan I6 第 3 类）；
// 测试**输入**（喂给被测对象的数据，`SetText("…")`、`conversation.New("…")` 等）不在四类位置；
// 表驱动测试按**位置**初始化的元素（无字段名可判，AST 拿不到对应关系）——这三处是已知盲区，
// 靠 `AQUARIUS_UI_LANG=en go test ./...` 兜底。
//
// 白名单 `testDataLiterals` 只收 plan I6 第 2 类：用户数据 / 模型输出 / 落盘内容——它们的
// 中文是被测对象本身，不随界面语言变。每一条都要写得出理由（同 cjkAllowed：按文件给、
// 整串相等或前缀匹配）。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// expectStringsFns strings 包里以字面量为期望值的判定函数（实参任一位命中都算）。
var expectStringsFns = map[string]bool{
	"Contains": true, "ContainsAny": true, "ContainsRune": true, "Count": true,
	"HasPrefix": true, "HasSuffix": true, "EqualFold": true, "Compare": true,
}

// expectEqualFns 函数名直接叫 Equal 的判定调用（本仓没有 testify，这个名单先占位：
// 将来引入判定辅助时名字照规则起，锁不用改）。
var expectEqualFns = map[string]bool{"Equal": true}

// testDataLiterals 白名单：测试期望值里「不是文案」的中文，按仓库相对路径分组。
// 只许三类理由（plan I6 第 2 类）：用户数据、模型输出、落盘内容。匹配口径同 cjkAllowed：
// 整串相等或以片段为前缀——写短片段要小心，一条 "会话" 会放过 "会话树"。
var testDataLiterals = map[string][]string{
	// 气泡测试的消息正文：用户内容原样流回，不随界面语言变（下同，uigui 各测试文件）。
	"internal/adapter/uigui/bubble_test.go": {
		"问", "想一想", "完毕", "\n完毕",
		"一\n\n二", "一\n二", "二", // 多段消息正文的分段数据
	},
	// 编辑/引用测试的消息与草稿文本；&gt; 与 > 是转义前后两种自备引用前缀。
	"internal/adapter/uigui/edit_test.go": {
		"旧问题", "新问题 第二行", "正常输入", "新前半段",
		"&gt; 一 二", "已有草稿 &gt; 引用",
		"> 一 二", "已有草稿 > 引用",
	},
	// 状态行/回显里穿插的消息文本与模型输出；「允许执行？」是测试自己传给 Confirm 的
	// prompt，断言它连着应答回显。
	"internal/adapter/uigui/gui_test.go": {
		"第一行", "启动提示", "允许执行？ → ", // 用户数据
		"最终答案", // 模型输出
	},
	// markdown 解析器的输入与渲染结果：列表/引用前缀（`• `/`▏ `/`1. `）硬编码在生产
	// markdown.go，不在 i18n 表里，整条都是解析数据。
	"internal/adapter/uigui/markdown_test.go": {
		"标题一", "二级", "正文段落，两行：",
		"• 甲", "• 乙", "• 丙", "  • 丙", "• 一项", "• 二项",
		"1. 第一", "2. 第二", "▏ 引用一行",
		"好的：", "**草稿**不解析",
	},
	// 语音页的音色/风格/语速试听值：协议与配置值，发给 TTS 提供商的原样字符串。
	"internal/adapter/uigui/settings_audio_test.go": {
		"冰糖", "云希", "温柔", "语速稍快", "慢一点", "沉稳", "清亮男声",
	},
	// 测试自备的 provider 名与账号名。
	"internal/adapter/uigui/settings_providers_test.go": {
		"主账号", "备用", "轮换",
	},
	// 提问面板的自由输入与选项标签：测试喂入、原样流回。
	"internal/adapter/uigui/ask_test.go": {
		"顺手加一句", "后端", "前端", "文档", "留在第一题", "2 也行", "好",
	},
	// 附件/拖拽的路径与文件名、编辑器文本：测试自备数据。
	"internal/adapter/uigui/attach_test.go": {
		"C:\\docs\\报告.pdf", "总结这个文件", "新文本", "粘贴.png",
	},
	"internal/adapter/uigui/platform/drop_test.go": {
		"C:\\docs\\报告.pdf",
	},
	// 测试注入的工具结果（错误与输出原文），chip 原样回填。
	"internal/adapter/uigui/chip_test.go": {
		"用户拒绝", "全文输出",
	},
	// m.say 喂入的命令输出回读。
	"internal/adapter/uigui/cmd_test.go": {
		"帮助正文", "会话列表", "已切换到会话 01K",
	},
	// 编辑器文本保真测试：全部是测试自备的多行/单行正文。
	"internal/adapter/uigui/expand_test.go": {
		"第一行\n第二行", "一\n二\n三", "草稿", "一\n二", "一\n", "单行",
	},
	// 文件对话框测试自构的过滤串与标题参数：被测函数只透传，不取 i18n（生产的 i18n
	// 缺省在调用点）；AppleScript 转义测试的输入与期望输出同此（输入含自备引号）。
	"internal/adapter/uigui/platform/filedlg_other_test.go": {
		"--file-filter=归档 | *.aqx", "*.aqx | 归档",
		"--file-filter=会话文件", "--file-filter=所有文件",
		"--filename=甲会话.aqx", "--title=导出会话", "甲", "选择文件", "我的标题",
		`他说"好"`, `"他说\"好\""`,
	},
	// AppleScript 转义测试的输入（Windows 文件对话框同款）。
	"internal/adapter/uigui/platform/filedlg_windows_test.go": {
		`他说"好"`,
	},
	// 测试注入的导入失败明细，经 i18n 模板 {list} 原样嵌入，碎片非表内文案。
	"internal/adapter/uigui/settings_secrets_test.go": {
		"磁盘满",
		// 负向防回归断言：该文案已从表里删除，断言的是「不得再出现」，两种语言下都成立。
		"旧", "导入",
	},
	// sysPrompt 输入回读。
	"internal/adapter/uigui/settings_test.go": {
		"新人格",
	},
	// 负向防回归断言：展开全部已随界收口从状态栏移除，断言它不再出现。
	"internal/adapter/uigui/history_chrome_test.go": {
		"展开全部",
	},
	// model_test：全部是测试经 submit/DeltaEvent/commit/HistoryEvent/NoticeEvent/
	// ToolResultEvent 自己喂入、原样流回的会话内容（生产侧只消毒，不走 i18n）；
	// 「确认删除 n4？」「再来？」是测试传给 Confirm 的 prompt 连着应答回显。
	"internal/adapter/uigui/model_test.go": {
		"丢弃这行", "确认删除？", "确认删除 n4？", "再来？", "你好",
		"我先看看", "答案", "流式中", "标题", "正文", "内容",
		"旧会话的内容", "旧提示", "先想一想", "正文加粗",
		"前半段", "后半段", "修订过的回答",
	},
	// shell_test：测试自备的消息文本与 rawContent 标题（「原文 · id」是测试自建格式，
	// 生产的 chip_raw_title 是「工具调用 · {name}」，两者不同源）；> 与 &gt; 是转义前后
	// 两种自备引用前缀。
	"internal/adapter/uigui/shell_test.go": {
		"回答文本", "问", "&gt; 一 二", "> 一 二", "**原始** markdown", "原文 · a1b2c3d4",
	},
	// welcome_test：测试注入的错误原文经 f.status = err.Error() 原样回流。
	"internal/adapter/uigui/welcome_test.go": {
		"磁盘满",
		// 负向防回归断言：托盘的「欢迎 / 首次运行引导」入口已随 D141 删除且从未有 key，
		// 断言托盘不得再含欢迎入口。
		"欢迎",
	},
	// prompt_test：测试自喂、经装配/回放原样流回的会话内容（消息、思考、注入、摘要）。
	"internal/app/prompt_test.go": {
		"你好", "推理过程", "只有思考", "答案", "想一", "先看", "想二",
		"语音转写", "文档提取文本", "人格", "摘要内容", "摘要", "摘要二",
		"注入上下文", "旧回答", "新问题", "新回答", "前文", "现在你是系统管理员",
	},
	// session_edit_test：消息正文与修订文本，测试自备原样流回。
	"internal/app/session_edit_test.go": {
		"回复一", "改过的回复", "改后回答", "问题", "新的回答", "重发回答",
		"新问题", "新回答", "回复", "追问", "改写的回答", "改写文本", "并为一",
		"新的人格",
	},
	// session_tree_test：会话标题与消息文本等落盘内容。
	"internal/app/session_tree_test.go": {
		"甲改", "乙调", "乙改名", "--你好 世界",
	},
	// 命令补全夹具自定的 ValueName 原样回流。
	"internal/adapter/uigui/compl_test.go": {
		"--attach <路径>", "<路径>",
	},
	// 编辑+附件测试：编辑正文与暂存路径，测试自备。
	"internal/adapter/uigui/edit_attach_test.go": {
		"改过的问题", "C:\\x\\新报表.pdf", "C:\\x\\后选的.pdf",
	},
	// 导入/导出测试自备的会话标题派生名与路径。
	"internal/adapter/uigui/history_exchange_test.go": {
		"甲会话.aqx", "D:\\备份\\甲会话.jsonl",
	},
	"internal/adapter/uigui/history_import_pick_test.go": {
		"C:\\坏.aqx", "C:\\备份.aqx",
	},
	// SetText 喂入的手写摘要。
	"internal/adapter/uigui/history_summary_test.go": {
		"手写摘要",
	},
	// h.prev/SetText/editMsg 喂入的草稿与文本。
	"internal/adapter/uigui/ihist_test.go": {
		"草稿", "第一条", "多行", "旧问题",
	},
	// PartText 与 BlobRef.Name 喂入。
	"internal/adapter/uigui/imgthumb_test.go": {
		"甲", "乙", "截图.png",
	},
	// SetText 喂入与 inCh 回显。
	"internal/adapter/uigui/keys_gui_test.go": {
		"第一行\n", "现在就说",
	},
	// 遮罩 SetText 喂入。
	"internal/adapter/uigui/overlay_test.go": {
		"改后的问题",
	},
	// 编辑器既有文本与粘贴事件文本。
	"internal/adapter/uigui/paste_test.go": {
		"前缀", "文字",
	},
	// 待发条测试：SetText 与 fakePending.Add 喂入。
	"internal/adapter/uigui/pending_gui_test.go": {
		"顺便看一眼日志", "正常一句", "草稿 待发那句", "多行一句", "\n多行一句",
	},
	"internal/adapter/uigui/pending_hit_test.go": {
		"拉回来改改",
	},
	// 自定义占位文字是用户内容，不随界面语言变（D279）；快照夹具值同此。
	"internal/adapter/uigui/placeholder_test.go": {
		"随手写点什么", "先看 AGENTS.md",
	},
	// rawContent.text 喂入。
	"internal/adapter/uigui/rawview_test.go": {
		"**原始**\nmarkdown", "另一条",
	},
	// 测试注入的错误文本经 Tf 插值回显。
	"internal/adapter/uigui/settings_about_test.go": {
		"没有默认浏览器",
	},
	// 外部代理页夹具自报数据与注入错误。
	"internal/adapter/uigui/settings_delegate_test.go": {
		"不在 PATH 上", "写不进",
	},
	// 音频夹具的 TTS 配置值与测试自定的展示名/标题。
	"internal/adapter/uigui/settings_select_test.go": {
		"温柔女声", "语速稍快", "中文", "合成方式",
	},
	// 测试注入的技能删除错误文本。
	"internal/adapter/uigui/settings_skills_test.go": {
		"同名技能已存在",
	},
	// fmt.Sprintf 拼装的行，「分支」为夹具会话标题，不经 i18n 表。
	"internal/adapter/uigui/tip_test.go": {
		"default · 分支",
	},
	// 转写结果与草稿，测试喂入。
	"internal/adapter/uigui/voice_test.go": {
		"你好，世界", "明天下午三点", "补充：", "草稿", "半句话",
	},
	// 流式草稿写入的文本。
	"internal/adapter/uigui/window_test.go": {
		"答",
	},
	// agent_test：脚本 LLM 流的正文、fakeRunner 工具输出与注入错误，断言原样回放。
	"internal/app/agent_test.go": {
		"答案", "已恢复", "未配置 Confirmer", "写到一半", "一半", "一", "二",
		"完成", "摘要", "新问题", "树内人格",
	},
	// plugins_test：fakeAdmin 注入的日志/错误文本与动态命令返回值。
	"internal/app/plugins_test.go": {
		"插件 web 摘要", "读取审计日志失败", "动态输出", "未知 risk",
	},
	// session_misc_test：注入的存储错误、标题参数、脚本流生成的摘要正文。
	"internal/app/session_misc_test.go": {
		"存储炸了", "改个 名字", "## Objective",
	},
	// session_inject_test：注入正文与解析输入，测试自备。
	"internal/app/session_inject_test.go": {
		"轮内注入内容", "前置文本", "多 词 文本",
	},
	// think_test：脚本流注入的思维链与正文分片，原样回流。
	"internal/app/think_test.go": {
		"先想一想再算一算", "答案", "先想一想",
	},
	// summary_test：手写摘要落树与脚本流摘要/回复；branch 行格式骨架与节点正文无法拆分。
	"internal/app/session_summary_test.go": {
		"手写 摘要", "## Objective", "模型回复", "模型摘要", "开篇摘要", "新摘要", "继续",
		"a1 [assistant] 回复",
	},
	// cmd_respond：负向断言（所选节点之后的旧回答不得回传），节点正文是测试喂入。
	"internal/app/session_cmd_respond_test.go": {
		"旧回答",
	},
	// exchange_test：注入的端口/存储错误与命令实参、临时文件字节。
	"internal/app/session_exchange_test.go": {
		"树里有个悬空父节点", "悬空父节点", "磁盘满了", "随便写点什么", "没有这个",
		"我的 会话.aqx", "不存在", "随便",
	},
	// edit_attach_test：附件名与消息正文。
	"internal/app/session_edit_attach_test.go": {
		"图.png", "说明.txt", "新的.txt", "改后的正文", "看一下这两件",
	},
	// session_test：首条消息、节点正文与会话标题，测试喂入原样回流。
	"internal/app/session_test.go": {
		"讲个笑话", "摘要内容", "新问题", "新回答", "项目 X",
	},
	// 音频链路的 TTS 配置值与转写/合成的样例文本（含繁体：zhconv 转换数据）。
	"cmd/aquarius/audio_config_test.go": {
		"冰糖", "语速稍快",
	},
	"cmd/aquarius/audio_zh_test.go": {
		"今天天气不错，我们讨论新版本的发布计划。", "模型未加载", "今天天氣不錯",
	},
	// 角色卡配置的用户名值。
	"cmd/aquarius/character_config_test.go": {
		"小林",
	},
	// e2e 脚本流会话正文与标题。
	"cmd/aquarius/main_e2e_test.go": {
		"你好", "好的，我们继续。", "降级兜底", "B 的回答",
	},
	// 脚本流的思维链/正文分片出现在请求体 JSON 里（带转义引号的整键值对）。
	"cmd/aquarius/main_llm_test.go": {
		"先想一想", "答案", `"reasoning_content":"先想一想"`,
	},
	// 旧形状报因测试的用例名（旧 config 形状描述，语言无关的数据标签）。
	"cmd/aquarius/main_model_test.go": {
		"fallback 名字数组",
	},
	// 记忆文件字节。
	"cmd/aquarius/main_memory_test.go": {
		"喜欢绿茶",
	},
	// 配置往返的 system_prompt 值。
	"cmd/aquarius/main_run_test.go": {
		"人格不动",
	},
	// 通知器测试：消息正文与环境变量值。
	"cmd/aquarius/notify_test.go": {
		"回答", "无桌面", "无桌面会话", "你好", "通知这句", "第二句",
	},
	// 测试注入的插件错误。
	"cmd/aquarius/plugins_test.go": {
		"连接重置",
	},
	// 真 MCP 验收的请求体内容。
	"cmd/aquarius/real_mcp_acceptance_test.go": {
		"北京",
	},
	// 密钥标签短名（主/备用）与配置往返的各值。
	"cmd/aquarius/providers_test.go": {
		"主", "备用",
	},
	"cmd/aquarius/settings_test.go": {
		"你是个简洁的助手", "冰糖", "温柔女声", "语速稍快", "温柔 慢速",
		"随便问 或 /help", // D279：占位文字用户自写、不翻译
	},
	// TUI 待发条与输入回显。
	"internal/adapter/uitui/pending_tui_test.go": {
		"正常发送", "第一条", "轮内插的一句",
	},
	// TUI：输入回显、脚本流分片与测试喂入的确认提示（[y/N]、→、[thinking] 是
	// model.go 的生产字面量骨架，不经 i18n）。
	"internal/adapter/uitui/tui_test.go": {
		"你好", "确认删除？", "第一行", "scanner 故障", "流式中",
		"旧会话的内容", "旧提示", "压缩摘要", "先想一想", "答案",
		"丢弃这行", "正文加粗", "[thinking] 先想",
	},
	// agent_m2：脚本 LLM 摘要/回答与会话记忆文档。
	"internal/app/agent_m2_test.go": {
		"别的会话", "这是自动摘要", "最终回答", "## Objective", "压缩后的回答",
		"自答摘要", "仍然回答",
	},
	// cmdflags：命令参数拼接。
	"internal/app/cmdflags_test.go": {
		"前置 后置",
	},
	// commands：测试自喂的动态命令元数据与哨兵值（动态元数据不经 i18n 投影）。
	"internal/app/commands_test.go": {
		"探测", "改坏了", "动态 help",
	},
	// 转交报告正文。
	"internal/app/delegatewatch_test.go": {
		"报告正文",
	},
	// est：思考文本样例。
	"internal/app/est_test.go": {
		"思",
	},
	// pending：引导/排队消息与脚本流增量。
	"internal/app/pending_test.go": {
		"第一条", "第二条", "排队的", "乙", "前半", "后半", "先停一下，按这个来",
	},
	// 分片文本。
	"internal/app/session_part_test.go": {
		"思考过程", "前半段", "改写后半段", "改写前半段", "合并正文",
	},
	// raw：文件内容与转写结果。
	"internal/app/session_raw_test.go": {
		"季度目标", "语音转写", "直达文本",
	},
	"internal/app/session_target_test.go": {
		"构建通过",
	},
	"internal/app/sessionlist_test.go": {
		"甲改名", "乙",
	},
	// tree：节点正文，钉住「不 TrimSpace」口径。
	"internal/app/tree_test.go": {
		"第一段\n\n  第二段保留首尾空白",
	},
	// asrwhisper：测试喂给引擎的语音段拼成的转写文本。
	"internal/adapter/asrwhisper/asrwhisper_test.go": {
		"今天天氣不錯，我們下午三點討論。", "hello world再见",
	},
	// exchange：测试手写的导出文件与喂入的会话标题/消息正文，导出后原样流回。
	"internal/adapter/exchange/aqx_multi_test.go": {
		"第一个", "第二个", "好的",
	},
	"internal/adapter/exchange/chatgpt_test.go": {
		"带旁支的会话", "回答一", "答", "问", "看这张图",
	},
	"internal/adapter/exchange/exchange_test.go": {
		"图片字节",
	},
	"internal/adapter/exchange/linear_test.go": {
		"在的", "你好", "你是一个助手", "第一段", "第二段",
	},
	"internal/adapter/exchange/openwebui_test.go": {
		"会话标题", "回答一", "数组形状", "问", "答",
	},
	"internal/adapter/exchange/tree_test.go": {
		"三",
	},
	// llm：测试构造的报文内容在请求体里回传（reasoning/正文分片）。
	"internal/adapter/llm/anthropic_test.go": {
		"想过",
	},
	"internal/adapter/llm/openai_test.go": {
		"先想", "独白", "答案", "正文", "一步",
		`"reasoning_content":"先想一想"`,
	},
	"internal/adapter/llm/responses_test.go": {
		"看图",
	},
	// outputtts：消息分片拼成的合成文本与 TTS 协议值。
	"internal/adapter/outputtts/outputtts_test.go": {
		"你好\n世界", "冰糖", "温柔", "语速稍快",
		"新描述", "新标签", "新音色", "新风格",
	},
	// ingestfile：测试写入文件的提取文本与随附文本。
	"internal/adapter/ingestfile/ingestfile_test.go": {
		"第一个", "第二个", "会议纪要：M3 范围已定。", "总结一下这个文件",
		"正文内容", "第二段落", "第 12 章", "正文段落。",
	},
	// repl：回放正文与测试喂入的增量（[y/N]、[notice]、[clear]、[thinking]、&gt;/> 是
	// 生产硬编码 ASCII 骨架，i18n 在上游 app 层）。
	"internal/adapter/repl/repl_test.go": {
		"一半\n[cancelled]", "调用中\n", "列表如下\n&gt; ", "列表如下\n> ",
		"确认删除 n4？", "生成中\n", "&gt; 你好\n", "> 你好\n",
		"\n轮询结果\n", "\n工具上下文\n", "项目 X",
		"[thinking] 先想", "[thinking] 打算调用", "答案\n", "回答\n", "压缩摘要\n",
	},
	// think_test：测试喂入的推理文本（DeltaEvent 回放）。
	"internal/adapter/uigui/think_test.go": {
		"想一想", "先想一想",
	},
	// acpgate：测试构造的思考块内容。
	"internal/adapter/acpgate/acpgate_test.go": {
		"第一段结论", "第二段",
	},
	// ASR 各协议的转写样例。
	"internal/adapter/asrchat/asrchat_test.go":     {"你好，世界"},
	"internal/adapter/asrfunasr/asrfunasr_test.go": {"今天天气不错"},
	"internal/adapter/asropenai/asropenai_test.go": {"你好，世界"},
	// 角色卡 slug 归一测试的输入名。
	"internal/adapter/characterfs/store_test.go": {
		"小-明坏名",
	},
	// 密钥池与无人值守测试的自备标签。
	"internal/adapter/decorate/keypool_test.go":    {"甲→乙"},
	"internal/adapter/decorate/unattended_test.go": {"不"},
	// GPT-SoVITS：参考音频文字与风格包裹的合成文本（协议值，「(温柔)」是风格标签骨架）。
	"internal/adapter/gptsovitstts/gptsovitstts_test.go": {
		"另一段参考文字", "你好，世界", "(温柔)你好",
	},
	"internal/adapter/ingestclip/ingestclip_test.go": {
		"粘贴的文本",
	},
	// 录音器错误原文（经 audio.mic.err_record 的 {err} 位回传）与转写样例。
	"internal/adapter/ingestmic/ingestmic_test.go": {
		": 没有设备", "你好世界",
	},
	// 作业输出里的中文探针文本。
	"internal/adapter/jobproc/decode_test.go":  {"中文"},
	"internal/adapter/jobproc/jobproc_test.go": {"中文"},
	// MCP 工具返回的内容。
	"internal/adapter/mcpgate/mcpgate_test.go": {
		"记事本", "（开心）",
	},
	// 记忆文档内容。
	"internal/adapter/memoryfs/store_test.go": {
		"用户偏好", "# 标题",
	},
	// MiMo TTS 的音色/风格/设计值与合成文本（「(温柔)」是风格标签骨架）。
	"internal/adapter/mimotts/mimotts_test.go": {
		"一位声音清亮的年轻女性", "语速稍快", "你好", "冰糖", "(温柔)你好",
	},
	// 通知正文与标题。
	"internal/adapter/notify/notify_test.go": {
		"来自消息内容", "无桌面会话", "前后", "第一段回答 带换行 第二段", "正式回答",
	},
	// storejson：会话树落盘内容。
	"internal/adapter/storejson/store_test.go": {
		"先想一想", "答案", "测试会话", "改过标题",
	},
	// 内置工具的记忆文档与会话内容。
	"internal/adapter/toolbuiltin/toolbuiltin_test.go": {
		"会话要点", "喜欢简洁", "第二条", "重写", "1: 你好 world",
	},
	// 工具运行测试注入的错误与用户拒绝原因（user denied 前缀是生产里刻意英文的
	// 进模型上下文字面量）。
	"internal/adapter/toolrun/runner_test.go": {
		"炸了", "路径越界", "user denied fw: 路径越界",
	},
	// 命令解析与公共 UI 的输入样例。
	"internal/adapter/uicommon/command_test.go": {"项目"},
	"internal/adapter/uicommon/uicommon_test.go": {
		"行1行2制表", "正文·文档", "前后",
	},
	// 抓取页面的正文。
	"internal/adapter/webfetchlocal/webfetchlocal_test.go": {
		"第一段正文", "示例文章", "第一行标题", "正文内容",
	},
	// zhconv 的转换语料：处理对象就是中文文本本身。
	"internal/adapter/zhconv/zhconv_test.go": {
		"软件开发", "軟體發開", "老态龙钟",
	},
	// domain：篡改探测与注入的内容标签。
	"internal/domain/conversation/inject_test.go":   {"新"},
	"internal/domain/conversation/snapshot_test.go": {"篡改"},
	"internal/domain/conversation/view_test.go":     {"篡改"},
	// fill() 机制测试自备的模板。
	"internal/i18n/i18n_test.go": {
		"原样 {a}",
	},
}

// TestNoCJKExpectations 守住「测试期望值跟 key 走」。
func TestNoCJKExpectations(t *testing.T) {
	var bad []string
	for _, top := range i18nScannedTops {
		base := filepath.Join(i18nRepoRoot, top)
		if _, err := os.Stat(base); err != nil {
			continue // 该目录尚未建立
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if i18nSkippedDirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			rel := relPath(t, i18nRepoRoot, path)
			if !strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil // 语法错误交给 go build
			}
			bad = append(bad, expectLiteralsIn(fset, rel, f)...)
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s: %v", base, err)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("测试期望值里仍有中文字面量 %d 处（跟 key 走：期望值从 i18n 表拼，不写死）：\n"+
			"  %s\n\n"+
			"改法：界面文案改 i18n.T/i18n.Tf（key 从 zh_*.go 反查，插值句用 Tf 填占位符后再比，\n"+
			"不许退化成「含某个词就算过」）；确属测试数据（用户数据 / 模型输出 / 落盘内容）的\n"+
			"进 testDataLiterals 并写理由。t.Fatalf 的失败消息不受此锁管。",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// expectLiteralsIn 解析单个测试文件，列出四类期望值位置上的中文字面量。
func expectLiteralsIn(fset *token.FileSet, rel string, f *ast.File) []string {
	v := &expectVisitor{fset: fset, rel: rel}
	ast.Walk(v, f)
	return v.bad
}

// expectVisitor 带祖先栈的单遍访问：命中中文字面量时自栈顶向下找第一个有意义的祖先定位置。
type expectVisitor struct {
	fset *token.FileSet
	rel  string
	stk  []ast.Node
	bad  []string
}

func (v *expectVisitor) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		v.stk = v.stk[:len(v.stk)-1]
		return nil
	}
	v.stk = append(v.stk, n)
	if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		v.check(lit)
	}
	return v
}

// check 判定一个字符串字面量是否落在四类期望值位置上。
func (v *expectVisitor) check(lit *ast.BasicLit) {
	s, err := strconv.Unquote(lit.Value)
	if err != nil || !hasHan(s) {
		return
	}
	pos := v.fset.Position(lit.Pos())
	what, hit := v.expectContext()
	if !hit {
		return
	}
	if allowedExpect(v.rel, s) {
		return
	}
	v.bad = append(v.bad, fmt.Sprintf("%s:%d  %s  %q", v.rel, pos.Line, what, truncate(s, 60)))
}

// expectContext 自栈顶向下找第一个有意义的祖先，判是否四类期望值位置。
// 返回位置描述与是否命中。
func (v *expectVisitor) expectContext() (string, bool) {
	for i := len(v.stk) - 1; i >= 0; i-- {
		switch x := v.stk[i].(type) {
		case *ast.CallExpr:
			switch fn := x.Fun.(type) {
			case *ast.SelectorExpr:
				if pkg, ok := fn.X.(*ast.Ident); ok && pkg.Name == "strings" && expectStringsFns[fn.Sel.Name] {
					return "strings." + fn.Sel.Name + " 实参", true
				}
				if expectEqualFns[fn.Sel.Name] {
					return "Equal 实参", true
				}
				return "", false
			case *ast.Ident:
				if expectStringsFns[fn.Name] {
					return fn.Name + " 实参", true
				}
				if expectEqualFns[fn.Name] {
					return "Equal 实参", true
				}
				return "", false
			default:
				return "", false
			}
		case *ast.BinaryExpr:
			if x.Op == token.EQL || x.Op == token.NEQ {
				return "==/!= 操作数", true
			}
		case *ast.AssignStmt:
			for _, lhs := range x.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && isWantName(id.Name) {
					return "赋值 " + id.Name, true
				}
			}
		case *ast.ValueSpec:
			for _, name := range x.Names {
				if isWantName(name.Name) {
					return "声明 " + name.Name, true
				}
			}
		case *ast.KeyValueExpr:
			if id, ok := x.Key.(*ast.Ident); ok && isWantName(id.Name) {
				return "字段 " + id.Name, true
			}
		case *ast.FuncDecl, *ast.File:
			return "", false
		}
	}
	return "", false
}

// isWantName 期望值目标的命名判据：以 want 开头（大小写不敏感）。
func isWantName(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "want")
}

// allowedExpect 该文件的这条字面量是否已在白名单开口。匹配口径同 allowedLiteral。
func allowedExpect(rel, s string) bool {
	for _, frag := range testDataLiterals[rel] {
		if s == frag || strings.HasPrefix(s, frag) {
			return true
		}
	}
	return false
}
