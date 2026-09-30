# Aquarius 初始设计方案（最终版）

> **状态：定稿（Initial Design, Final）** ｜ 技术栈 Go（单二进制）｜ 架构六边形 + 插件化
>
> 本文是 Aquarius 的**唯一权威设计文档**，覆盖并取代此前的 v1–v4 草案（DDD 全景 → 个人极简 →
> 不可变树/插件化 → MCP/多模态）。所有"待决事项"已在 §13 落定为决策；未尽事宜按 §14 暂缓处理。

---

## 1. 项目定位与设计原则

**Aquarius 是一个面向个人的、交互优先的极简 AI 助手。**

- 核心是**对话体验**：流式交互、消息修订分支、回溯对比；不是编码工作流产品。
- **极简内核**：无 subagent、无任务编排层、无消息总线。内核只有三件事：会话树、上下文装配（含压缩）、Turn 循环。
- **多模态出入**：文本 / 图片 / 文档 / 语音皆可输入，文本 / 语音 / 通知皆可输出；输入输出**方式**可插拔。
- **记忆 = 文档**：markdown 文件即记忆，用户可直接编辑；模型经工具读写；检索 = 关键词。
- **不可变节点 + 用户主权**：历史节点永不修改；"改"= 创建同级新节点，是否携带后续历史由用户选择。
- **通用能力内置**：后台任务、文件操作、终端执行。**没有工作区概念**（无项目根/索引/监听）。
- **内核极简、边缘可插**：工具、模型、记忆后端、命令、UI、输入/输出方式全部是插件面；
  进程外扩展统一走 **MCP**；内置实现与三方插件走同一契约（**内置不享特权**）。
- 交付物是**单个二进制**，数据全部在 `~/.aquarius/`。
- **面向 agent 的文本用英文**：进入模型上下文的字符串（system 提示、工具声明与参数描述、
  工具回填结果/错误）一律英文；面向用户的界面、命令输出与日志用中文（D36）。

架构原则：

1. 六边形架构：`domain` 纯 Go、零依赖；外部世界全部是端口（`port`）的适配器。
2. **两个稳定级**：`pluginapi/v1` 对外严格 semver；`internal/port` 是内核内部的缝、自由演进。二者类型独立。
3. 横切能力（重试/退避、审计日志、截断）用**端口装饰器**叠加，不进插件 API（限流由重试退避承担，暂无独立限流装饰器）。
4. 会话树是纯追加结构：一切变化 = 新增节点 / 增删边 / 边转移 / 移动 Head。
5. 插件**永远不能直接操作会话树**，一切经内核中转。

---

## 2. 统一语言

| 术语 | 英文 | 定义 |
|---|---|---|
| 会话树 | Conversation | 一棵不可变消息树 + 一个 Head 游标 |
| 节点 | Message | 树节点，创建后只读：角色、内容分片、工具调用/结果、终态、用量 |
| 内容分片 | Part | 消息内容的多态片段：Text / Image / Audio / Doc / Thinking |
| 附件 | Attachment | 被消息引用的二进制内容，sha256 内容寻址存储 |
| 根 | Root | 实节点空消息（ID=会话 ID、Role=root），唯一 `Parent==""` 的节点，仅作树管理、不进模型上下文 |
| 头 | Head | 当前游标（初始=Root，无空串特例），决定发给模型的线性路径 |
| 人格 | persona | 会话首节点（system 角色，config 快照），压缩水位之上也恒回传 |
| 压缩摘要 | Compact Summary | system 角色水位节点：其上历史（persona 除外）不再回传模型 |
| 权限等级 | Permission Level | read-only / strict / permissive / full-access 四档矩阵预设 |
| 路径 | Path | Root → Head 的节点序列 = 本次推理的上下文 |
| 轮次 | Turn | 一次"模型生成 + 0..n 次工具执行"的循环 |
| 提交 | Commit | 流式结束后把本轮产出作为不可变节点一次性挂入树 |
| 修订 | Revise | 创建同级新节点（同父）；`Fresh`=新分支重新开始，`Carry`=后续历史边转移过来 |
| 工具调用 | ToolCall / Result | 模型发起的调用与回填结果（tool 角色节点承载） |
| 后台任务 | Job | 异步执行的进程：可查状态、拉日志、终止 |
| 摄取器 | Ingestor | 输入方式适配器：原始输入（文本/文件/剪贴板/麦克风）→ Part 列表 |
| 输出器 | OutputAdapter | 输出方式适配器：TTS 播报 / 系统通知 / 导出文件 |
| 转写 / 合成 | Transcriber / Synthesizer | 语音模态端口：ASR（音频→文本）/ TTS（文本→音频） |
| 记忆文档 | MemoryDoc | 一篇 markdown 记忆（name + 文本） |
| MCP 网关 | mcpgate | 把 MCP server 适配为内核端口实现的适配器（Tier-2 插件面） |
| 增量 | Delta | 流式返回的文本/工具调用片段 |

---

## 3. 战略设计：限界上下文

```
┌────────────────────────────────────────────────────────────┐
│                    核心域：对话与交互（内核）                    │
│   会话树（Revise Fresh|Carry）· Part 内容 · 上下文装配 · Turn 循环 │
├──────────────┬──────────────┬──────────────┬────────────────┤
│ 支撑：工具执行  │ 支撑：记忆文档  │ 支撑：多模态 I/O │ 支撑：插件宿主    │
│ Job / fs / 终端│ markdown 文档  │ Ingestor/Out   │ MCP 网关 / 授权   │
├──────────────┴──────────────┴──────────────┴────────────────┤
│        通用域：LLM 接入 · ASR/TTS 接入（均经适配器/插件面）          │
└────────────────────────────────────────────────────────────┘
```

**不做**（明确排除）：subagent、任务/计划聚合、事件总线、Saga、多租户、计量计费、评测平台、
**工作区**（项目目录抽象/代码索引/文件监听）、**独立于文档的记忆系统**（记忆 = markdown 文档，一切一并写入；
无自动入库的记忆巩固/摘要记忆管线）、embedding 检索。

> **上下文压缩摘要**（§4.1 关键语义 / §7.1 三轨压缩）不是记忆系统：它管理的是**对话上下文**
> （agent 框架必备能力），转写结果以 system 节点入会话树、随会话走，与 `memory/*.md` 记忆文档无关。

---

## 4. 领域模型

### 4.1 会话树（`domain/conversation`）

```
            Root(实节点空消息，ID=会话ID)
               │
              m1 (user)
               │
              m2 (assistant)
             /                \
   m3 (user) ─ m4 ─ m5          m3' (user, Revise m3) ─ [继续]    ← Head
   ↑ 旧分支（Fresh 模式保留于此；Carry 模式下 m4/m5 转移到 m3'，m3 成为旧版本叶子）
```

```go
package conversation

type PartKind string

const (
    PartText     PartKind = "text"
    PartImage    PartKind = "image"
    PartAudio    PartKind = "audio"
    PartDoc      PartKind = "doc"
    PartThinking PartKind = "thinking" // 思考过程（D42）：仅 assistant 节点可携带
)

type Part struct {
    Kind       PartKind
    Text       string   // Kind=text；Kind=doc 时为提取文本（截断）；Kind=thinking 时为思考文本（D42）
    Ref        *BlobRef // Kind=image|audio|doc：附件引用
    Transcript string   // Kind=audio：ASR 转写文本（模型只见文本，音频留附件库回放）
}

type Message struct { // 创建后只读，值语义
    ID         MessageID
    Parent     MessageID // "" = Root 自身（仅 Root 为空；其余节点 Parent 恒非空）
    Role       Role      // root | user | assistant | system | tool
    Content    []Part
    ToolCalls  []tool.Call
    ToolResult *tool.Result
    Outcome    Outcome // done | cancelled | error（提交时确定）
    Model      string
    Usage      Usage
    CreatedAt  time.Time
}

type Conversation struct {
    ID           ID
    Title        string
    Nodes        map[MessageID]Message       // 只增
    Children     map[MessageID][]MessageID   // 结构边（Root 的孩子 = 顶层消息；树外键 "" 仅挂 Root 自身）
    Head         MessageID
    RevisedFrom  map[MessageID]MessageID     // 新→旧：版本链（纯结构元数据）
    CreatedAt, UpdatedAt time.Time
}

type KeepMode int

const (
    Fresh KeepMode = iota // 新节点空白开始；旧节点的子树原样留作历史分支
    Carry                 // 旧节点的子树边转移到新节点；旧节点成为"旧版本"叶子
)

func New(id ID, title string) *Conversation // 建 Root 空节点（ID=id、Role=root）并把 Head 置于 Root；persona 由应用层写为首孩子
func (c *Conversation) Append(role Role, content []Part) (Message, error) // 仅 user|assistant；root/system/tool 一律走 AppendCommitted
func (c *Conversation) AppendCommitted(m Message) error                   // 提交已组装好的不可变节点（system/tool），入树前校验不变量
func (c *Conversation) Revise(id MessageID, content []Part, mode KeepMode) (Message, error) // root/tool 不可 Revise；system 可
func (c *Conversation) Prune(id MessageID) error            // 剪掉 id 及整棵子树（连带清理失联 tool 节点；Root 不可剪）
func (c *Conversation) Checkout(id MessageID) error         // Head 移到任意节点（含 Root=回根；无空串特例）
func (c *Conversation) Path() []Message                     // Root→Head 线性序列（首元素为 Root，装配时滤掉）
func (c *Conversation) Branches(id MessageID) []Message     // 同级分叉（UI 对比新旧版本；id=Root 时为顶层消息）
func (c *Conversation) Find(id MessageID) (Message, bool)
func (c *Conversation) Validate() error                     // 三条不变量整体自检（加载后/测试用）
```

**不变量（3 条，性质测试守护）**：

1. **树合法**：以 **Root 实节点为唯一根**——Root 是空消息节点（`ID == 会话 ID`、`Role == root`、`Parent == ""`），
   `Parent == ""` 的节点仅 Root 一个；顶层消息 = Root 的孩子（允许多条，支撑 Revise 首条消息）；
   无环；`Parent/Children` 双向一致；`Head` 属于树（初始 = Root，**无空串特例**）。
2. **引用合法**：`tool` 节点的 `CallID` 匹配**树中存在**的某 assistant 节点的 `ToolCalls[i].ID`（存在性引用，
   以支撑 Carry 边转移；Path 上"失联"的 tool 节点在 Prompt 装配时按文本内联并标注 `〔历史工具结果〕`）。
   `Prune` 连带移除因此失联的 tool 结果节点，维持本不变量（D18）。
3. **节点不可变**：`Nodes` 只增不改（节点内容创建后只读）；一切变化 = 新增节点 / 增删边 / 边转移 / 移动 Head。

**关键语义**：

- **Revise Carry = 边转移**（否决深拷贝子树）：节点内容不依赖祖先指纹（不像 git commit），
  改写历史只需把 `Children[id]` 这批边改挂到新节点，树仍是一棵树，无 DAG。精确语义（D16）：
  被转移子树**零拷贝、身份不变**，`Children` 边与**直接孩子的 `Parent` 边指针**随之改写，
  孙代及更深节点字节级不动；节点内容（Content/ToolCalls/ToolResult/Usage/Model/Outcome/CreatedAt）永不改写。
- **Revise 的 Head 语义**（D15/D16）：`Fresh` → `Head` 移到新节点；`Carry` → 旧 `Head` 若是 id 的严格后代
  （随子树转移）则保持不变，否则移到新节点。Revise 首条（顶层）消息合法：新节点同为顶层消息。
- **流式中间态不进领域**：增量只流经 `Presenter`；Turn 结束（或取消）一次性 Commit 不可变节点
  （取消 = `Outcome: cancelled` + 已生成部分文本）。没有半个节点，崩溃恢复无部分写入问题。
  节点 ID 在 Turn 开始时预分配，作流事件关联 ID。
- **Root 即会话**（D19）：Root 实节点 ID 复用会话 ID（不另造第二个 ID），Role=root、空内容，仅作树管理；
  `Prune`/`Revise` 均拒 Root（删/改 Root 即破坏唯一根）。M0 的虚拟 Root 落盘格式**破坏性切换**——
  旧会话文件加载即报错，删除重建。
- **persona 进树**（D20）：会话首节点 = system 角色（值为 config `system_prompt` 快照 + 运行环境块
  D37：platform、UI 形态与 TERM、特权沙盒目录、缺省调用超时与 `timeout_sec` 说明，字段为空跳过；空则内置默认），
  压缩水位之上也**恒回传**；可 Revise（/edit 重写人格）、可 Prune（= 清空对话，走 /rm 二次确认；
  清空后装配回退 config 兜底注入）。
- **上下文压缩水位**（D21）：压缩摘要以 system 节点入树；装配 = [persona] + [最新摘要] + [摘要之后]，
  摘要之上（persona 除外）历史一律不回传；多次压缩链式吸收（只回传最新摘要）。
  `/compact` 手动触发、失败只报错树无损；压缩失败回退最旧裁剪、超预算硬保底由
  截断装饰器执行（三轨压缩与硬保底见 §7.1/D14）。
- **思考过程入树 + 回传开关**（D42）：assistant 节点的思维链以 `PartThinking` 分片承载（流内分片
  合并为至多一段、置于正文之前；取消/出错终态的已生成思考随节点一同入树）；节点形态校验限定
  **仅 assistant 可携带**（root/user/system/tool 一律拒）。**回传给提供商**走独立承载：
  装配层把思考放进 `PromptMessage.Reasoning`（与 Content 分离），openai 适配器序列化为
  assistant 消息的 **`reasoning_content` 字段**（兼容生态事实标准；DeepSeek 带 `tools` 时
  **强制**回传，缺失即 400），不混进正文；端点点名不认该字段时复用 D34 剥离重试管线
  （扩展到消息内字段）→ 记入 `model.unsupported_params` 后省略。是否回传由 config
  **`model.echo_thinking`** 控制（`*bool`：**键缺失 = 回传**，显式 `false` 才关，改后重启生效）。
  展示口径（实时暗块 + 启动回放）恒含思考，与模型口径（回传开关）解耦——
  回放展示树里有什么，装配决定发什么。
- `Prune` 是唯一破坏性操作；`storejson` 写文件前保留一代 `.bak` 防误删（§13-D7）。

### 4.2 附件与多模态承载

- 附件内容寻址（sha256）存 `~/.aquarius/attachments/<hash>`；同内容多处引用只存一份；
  GC 按引用集合清扫（启动时收集全量会话引用作 keep 集，收集不全则跳过；Prune 会话不立即删附件）。
- 模型能力差异：`ModelInfo{Vision, Audio}` 声明能力；不支持图片的模型遇到 Image Part →
  明确报因并提示（v1 不做自动 OCR/描述降级）。
- Audio Part 一律先经 `Transcriber` 得到 Transcript；模型只见文本。Doc Part 注入提取文本（截断），
  原文可经 `file_read` 工具取用。

### 4.3 内置工具（全部经 `port.Tool` 契约，与三方插件同权）

| 工具 | 说明 | Risk |
|---|---|---|
| `memory_list` / `memory_read` / `memory_search` | 记忆读取与检索（只见全局 + 当前会话两份，按文档名寻址） | Safe |
| `memory_write` | 写入记忆文档（`name`=全局/当前会话；`mode`=append 缺省 / overwrite） | Confirm |
| `think` | 显式整理思路（no-op）；可见性走 config `model.think_tool`，**默认隐藏**（D34：原生思考为主，需要草稿工具时配置启用） | Safe |
| `sleep` | 等待 N 秒（停顿或等后台任务；取消可中断；1–3600s，超缺省超时需在调用上配 `timeout_sec`，D38/D39） | Safe |
| `file_read` / `file_list` / `file_search` | 通用文件读取（路径按 §9 等级矩阵，读全盘免确认） | Safe |
| `file_write` / `file_delete` | 文件写入 / 删除 | Confirm |
| `term_exec` | 终端命令同步执行（超时返回，输出截断保头尾） | Confirm |
| `job_start` | 后台任务启动 | Confirm |
| `job_list` / `job_status` / `job_logs` / `job_kill` | 后台任务管理 | Safe |
| `context_compact` | 触发上下文压缩（等价 `/compact`，模型自我管理上下文） | Safe |

- 文件工具是**裸通用文件操作**：没有 cwd 工作区、项目根、索引、监听。路径权限按 **§9 权限等级矩阵**：
  读全盘免确认，写按等级格（`rw` 格免确认，其余逐次确认）——D22 取代 D6。
- 后台任务 = 独立进程 + 日志落盘 `~/.aquarius/jobs/<id>.log`；任务表 v1 内存态（§13-D8）。
- 工具声明与回填一律英文（D36）；任何调用可带**保留参数** `timeout_sec`（整数 1–3600）逐次覆盖缺省超时，
  schema 自声明该参数的工具（`job_start`）不受保留参数约束（D38）。
- `term_exec` 与 `job_*` 的**终端输出在 jobproc 适配器内按行解码为 UTF-8**（D41）：UTF-8 合法则原样
  （含显式 `chcp 65001` 的输出），否则按 GBK/CP936 解码；模型无需再手工 `chcp` 切换代码页。
- 三方工具经 MCP 网关加入，命名隔离：`mcp:<server>:<tool>`。

### 4.4 记忆文档

- **全局记忆**：`~/.aquarius/memories.md` 单文件；**会话记忆**：`~/.aquarius/conversations/<id>.memory.md`
  （随会话文件夹就近存放，D23）。用户可直接用编辑器改，`/memory` 即用系统编辑器打开（D24），下次读取即生效。
- `memory_*` 工具只操作**全局 + 当前会话**两份，按文档名寻址（`memories.md` / `<id>.memory.md`）。
- 无 embedding、无自动遗忘、无冲突消解；检索 = 关键词匹配。后端是插件面（`port.MemoryStore`）。

---

## 5. 端口设计（`internal/port`）

依赖方向：`adapter → port ← app → domain`；`domain` 不 import 任何端口；
port 以接口与 DTO 为主，允许无状态纯函数助手（如 `ResolveJobID`、`WithSessionID`——多层共用、
放任一层都会违反依赖方向）。

### 5.1 `llm.go` —— 生成端口

```go
type PromptPart struct {
    Kind string // "text" | "image"
    Text string
    MIME string
    Data []byte // 图片字节，由应用层经 AttachmentStore 解析后内联
}

type PromptMessage struct {
    Role      string // "system" | "user" | "assistant" | "tool"
    Content   []PromptPart
    Reasoning string // role=assistant：思维链回传承载（D42），适配器映射为 reasoning_content 等字段
    ToolCalls []tool.Call
    CallID    string // role=tool
}

type GenerateRequest struct {
    Model    string
    Messages []PromptMessage
    Tools    []tool.Spec
    Params   Sampling        // Temperature、MaxTokens、Stop、ReasoningEffort、Thinking（enable_thinking，D34）
    Budget   TokenBudget     // MaxOutputTokens、MaxCostUSD
}

type ModelInfo struct {
    Name           string
    Vision, Audio  bool
    CostIn, CostOutUSD float64 // 每千 token 单价
}

// Usage 见 domain/conversation.Usage（与 Message.Usage 同型，port 直接引用，不另造 DTO）
type Usage = conversation.Usage

type Delta struct {
    Text      string
    Reasoning bool            // 思维链分片（D34 展示 / D42 入树）：随节点提交为 PartThinking，回传走 PromptMessage.Reasoning
    ToolCalls []ToolCallDelta // Index 分片聚合（app 层 ToolCallAssembler）
    Usage     *Usage          // 流末尾
}

type Stream interface {
    Recv() (Delta, error) // io.EOF = 正常结束
    Close() error         // 中断生成（等价 ctx 取消）
}

type LLM interface {
    Generate(ctx context.Context, req GenerateRequest) (Stream, error)
    Models(ctx context.Context) ([]ModelInfo, error)
}

// TokenCounter 可选精确计数（三级计数链②，D26）：适配器按需实现——
// 本地 tokenizer（如 config `model.tokenizer` 指向的 tokenizer.json）或 count_tokens API。
// 未实现/报错时 app 回落通用估算（③），并以服务端实测 usage（①）自校准。
type TokenCounter interface {
    CountTokens(ctx context.Context, text string) (int, error)
}
```

### 5.2 `tool.go` —— 工具端口

```go
type Tool interface {
    Spec() tool.Spec
    Execute(ctx context.Context, call tool.Call) (tool.Result, error)
}

type ToolRunner interface { // 查找 + capability 校验 + Risk 确认 + 超时（可经保留参数 timeout_sec 逐次覆盖，D38）+ 结果裁剪
    Specs(ctx context.Context) ([]tool.Spec, error)
    Execute(ctx context.Context, call tool.Call) (tool.Result, error)
}

type Confirmer interface {
    Confirm(ctx context.Context, prompt string) (bool, error)
}

// FileTarget 可选能力（D25）：文件类工具申报本次调用的目标路径与读写操作，
// ToolRunner 据此查权限矩阵路径格（D22 两列分工）；未申报者走执行类（看工具列）。
type FileTarget interface {
    Target(ctx context.Context, call tool.Call) (path string, op perm.Op, ok bool)
}

// 会话上下文：Agent.Run 执行工具时注入当前会话 ID（值拷贝、只读），
// 供 memory_* 的 session 作用域等定位资源；不暴露会话树对象（插件不得直接操作树）。
func WithSessionID(ctx context.Context, id conversation.ID) context.Context
func SessionIDFrom(ctx context.Context) (conversation.ID, bool)
```

### 5.3 `store.go` —— 会话存储端口

```go
type ConversationSummary struct {
    ID conversation.ID; Title string; MessageN int; UpdatedAt time.Time
}

type ConversationStore interface { // 整树存取（一会话一 JSON），写前留一代 .bak
    Save(ctx context.Context, c *conversation.Conversation) error
    Load(ctx context.Context, id conversation.ID) (*conversation.Conversation, error)
    List(ctx context.Context) ([]ConversationSummary, error)
    Remove(ctx context.Context, id conversation.ID) error
}
```

### 5.4 `memory.go` —— 记忆端口

```go
type MemoryDoc struct{ Name, Content string; ModTime time.Time }
type MemoryIndexEntry struct{ Name, Summary string }
type MemoryHit struct{ Name string; Line int; Snippet string }

type MemoryStore interface {
    Index(ctx context.Context) ([]MemoryIndexEntry, error) // 注入 system prompt
    Read(ctx context.Context, name string) (MemoryDoc, error)
    Write(ctx context.Context, doc MemoryDoc) error
    Remove(ctx context.Context, name string) error
    Search(ctx context.Context, query string) ([]MemoryHit, error)
}
```

### 5.5 `blob.go` —— 附件端口

```go
// BlobRef 见 domain/conversation.BlobRef（Part.Ref 引用之，port 直接引用，不另造 DTO）
type BlobRef = conversation.BlobRef

type AttachmentStore interface {
    Put(ctx context.Context, r io.Reader, mime, name string) (BlobRef, error)
    Get(ctx context.Context, ref BlobRef) (io.ReadCloser, error)
    Stat(ctx context.Context, ref BlobRef) (bool, error)
    GC(ctx context.Context, keep map[string]bool) error
}
```

### 5.6 `modality.go` —— 语音模态端口

```go
type Transcriber interface { // ASR：whisper API / 本地引擎 / 系统听写
    Transcribe(ctx context.Context, audio BlobRef, lang string) (string, error)
}
type Synthesizer interface { // TTS：云 TTS / 系统朗读
    Synthesize(ctx context.Context, text, voice string) (BlobRef, error)
}
```

### 5.7 `ingest.go` —— 输入/输出方式端口

```go
type RawInput struct {
    Kind string // "text" | "file" | "clipboard" | "mic"
    Text string
    File string
    Blob *BlobRef // clipboard 图片 / mic 音频（已先入附件库）
}

type IngestReport struct {
    Parts []conversation.Part
    Note  string // "已由 ASR 转写" 等
}

type Ingestor interface {
    Accepts(raw RawInput) bool
    Ingest(ctx context.Context, raw RawInput) (IngestReport, error)
}

// Parts 可能含思考分片（PartThinking，D42）：输出器属"对外播报正文"，适配器须自行筛选。
type OutputRequest struct{ Message conversation.Message; Parts []conversation.Part }

type OutputAdapter interface {
    Name() string
    Deliver(ctx context.Context, req OutputRequest) error // 失败只记日志不打断主流程
}
```

### 5.8 `job.go` —— 后台任务端口

```go
type JobSpec struct {
    Command string; Args []string; Env map[string]string
    WorkDir string        // 进程 cwd（默认家目录），不是"工作区"
    Timeout time.Duration // 0 = 不限时
}

type Job struct {
    ID JobID; Spec JobSpec
    Status JobStatus // running | done | failed | killed
    PID, ExitCode int
    StartedAt, EndedAt time.Time
}

type JobManager interface {
    Start(ctx context.Context, spec JobSpec) (Job, error)         // 后台
    Run(ctx context.Context, spec JobSpec) (tool.Result, error)   // 同步（term_exec）
    List(ctx context.Context) ([]Job, error)
    Status(ctx context.Context, id JobID) (Job, error)
    Logs(ctx context.Context, id JobID, tail int) (string, error)
    Kill(ctx context.Context, id JobID) error
}
```

输出语义：`Run` 的 `tool.Result.Output` 与 `Logs` 返回值都是 **UTF-8 文本**——进程原始字节由
jobproc 在适配器内按行解码（D41），端口契约不暴露字节编码。

### 5.9 `ui.go` / `misc.go`

```go
type Event any // DeltaEvent{MessageID, Delta} | ToolCallEvent | ToolResultEvent
               // | CommittedEvent{Message} | ErrorEvent | NoticeEvent{Text}（裁剪/自动压缩等提示）
               // | HistoryEvent{Message}（启动恢复的历史回放，D40——不触发输出器扇出）
type Presenter interface{ Emit(ctx context.Context, ev Event) error }
type Prompter interface{ Next(ctx context.Context) (UserInput, error) }

type UserInput struct {
    Text    string      // "" = 命令
    Command *Command
    Raw     *RawInput   // 拖拽文件 / 粘贴图片 / 语音按钮走同一入口
}

type Command struct{ Name string; Args []string }

type Clock interface{ Now() time.Time }
type IDGen interface {
    ConversationID() conversation.ID
    MessageID() conversation.MessageID
    CallID() tool.CallID
}
type Secrets interface{ Get(ctx context.Context, name string) (string, error) }
```

### 5.10 端口 × 适配器矩阵

| 端口 | v1 内置适配器 | 三方接入 | 测试替身 |
|---|---|---|---|
| `LLM` | openai 兼容 / anthropic / ollama（+可选 `TokenCounter`） | Tier-1 Go 插件 | 脚本化 Stream |
| `Tool` | memory_*、file_*、term_*、job_*、think | **MCP server** | fake tool |
| `ToolRunner` | toolrun（查找/权限判定/确认/超时/裁剪，D25） | — | fake runner |
| `Confirmer` | TUI 确认 / `--yes` | — | 自动应答 |
| `ConversationStore` | storejson（一树一 JSON + .bak） | — | in-memory |
| `MemoryStore` | memoryfs（markdown） | MCP resources / Tier-1 | in-memory |
| `AttachmentStore` | blobfs（内容寻址） | — | in-memory |
| `Transcriber` / `Synthesizer` | whisper API / 系统朗读 | Tier-1 Go 插件 | 假转写 |
| `Ingestor` / `OutputAdapter` | 文本/文件/剪贴板/麦克风；通知/TTS/导出 | Tier-1 Go 插件 | 脚本 |
| `JobManager` | jobproc（本机进程 + 日志落盘） | — | 假任务 |
| `Presenter` / `Prompter` | uitui（TUI）、repl、uigui（GUI 悬浮球，D43/M5） | Tier-1（v1 不开放） | 收集器 / 脚本队列 |
| `Clock` / `IDGen` / `Secrets` | 系统时钟 / ULID / env | Tier-1（keychain） | 固定 / map |

---

## 6. 插件架构

### 6.1 扩展点清单

| # | 扩展点 | 内核侧契约 | 三方接入方式 |
|---|---|---|---|
| 1 | Tool | `port.Tool` | **MCP server**（tools） |
| 2 | LLM | `port.LLM` | Tier-1 Go 插件 |
| 3 | MemoryStore | `port.MemoryStore` | **MCP server**（resources，只读投影）/ Tier-1 |
| 4 | Command | `app.CommandHandler` | **MCP server**（prompts）/ Tier-1 |
| 5 | UI | `port.Presenter + Prompter` | Tier-1（契约就绪，v1 不开放装载） |
| 6 | Ingestor / OutputAdapter | `port.Ingestor/OutputAdapter` | Tier-1 |
| 7 | Transcriber / Synthesizer | `port.Transcriber/Synthesizer` | Tier-1 |
| 8 | Secrets | `port.Secrets` | Tier-1 |

### 6.2 两级插件

| | Tier-1 进程内（Go） | Tier-2 进程外 = **MCP server** |
|---|---|---|
| 契约 | `pluginapi/v1` Go 接口（独立 go.mod，严格 semver） | MCP 协议（stdio / streamable HTTP） |
| 内核侧接入 | `adapter/plugingo`（**D29：后移出 M4**，见 §14） | `adapter/mcpgate`（MCP client → port 实现，M4） |
| 发现 | main 显式注册 | config `mcpServers` + `~/.aquarius/plugins/*/plugin.json` |
| 装载 | 编译期 | 运行期 enable/disable，不重编 |
| 隔离 | 同进程 | 独立进程（崩溃不带崩内核） |

**双稳定级规则**：`internal/port` 可随内核重构；`pluginapi/v1` 只增不改，breaking change 走 `v2` 并存期。
两者类型独立，由 `plugingo`/`mcpgate` 做防腐转换——外部插件永远编译不到内核类型上。

### 6.3 MCP ⇄ Aquarius 映射（mcpgate 职责）

| MCP 概念 | Aquarius 概念 | 说明 |
|---|---|---|
| `tools/list` / `tools/call` | `port.Tool` 注册表 | 命名 `mcp:<server>:<tool>`；错误翻译为 `Result{OK:false}` |
| `resources/list` / `read` | `MemoryStore` 只读投影 | 资源进上下文索引；写仍走自有记忆 |
| `prompts/list` / `get` | `CommandHandler` | 暴露为 `/mcp:<server>:<prompt>` |
| `initialize` | 插件宿主校验 | 版本协商不兼容 → 拒载并报因 |
| sampling 反向调用 | **拒绝**（v1） | server 借用宿主模型的能力暂不支持 |

`plugin.json` = MCP server 启动描述：

```json
{
  "name": "web-search",
  "mcp": {
    "transport": "stdio",
    "command": "web-search-mcp",
    "args": ["--stdio"],
    "env": { "SERPER_API_KEY": "secret:SERPER_API_KEY" }
  },
  "capabilities": ["network"],
  "risk": "safe"
}
```

传输两种（M4，D30）：`stdio`（command/args/env）与 `streamable-http`（`url` + 可选 `headers`，
值同样允许 `secret:<环境变量名>` 引用，经 `port.Secrets` 在请求时注入、不落明文）；
config `mcpServers` 条目与 `plugin.json` 的 `mcp` 段字段一致。

### 6.4 插件宿主职责（`internal/plugin`）

1. **发现**：扫描 `plugins/*/plugin.json` + config 启停（启停与授权状态统一存 config `plugins.<name>`，两种发现源共用，D31）；Tier-1 由 main 注册。
2. **校验**：manifest schema、API 版本协商、provides 自检。
3. **授权（grant）**：`capabilities` 首次使用弹确认 → 写入 config `plugins.<name>.granted`（D31）；
   `risk=confirm` 工具逐次走 `Confirmer`；
   密钥经 `Secrets` 按名注入插件环境，不落明文配置。
   **stdio 子进程环境 = 父环境剔除 `AQUARIUS_*`（本项目密钥命名约定）+ 声明项**——
   宿主密钥不随继承外泄给插件；PATH/TEMP 等系统变量照常继承（审查修复）。
4. **生命周期**：启动即按状态连接（软启动，单插件失败只记状态）→ 被动健康
   （会话终止感知 `Wait`）→ 崩溃自动重启（限次 + 退避）→ 关机优雅 `shutdown`；
   主动健康检查与按需懒加载未实现，见 §14。
5. **可观测**：记录每个调用的 插件名/耗时/结果状态，供 `/plugin` 命令查看（同源数据进审计日志，§8）。

---

## 7. 应用层

### 7.1 Turn 循环（唯一的"编排"）

```go
func (a *Agent) Run(ctx context.Context, c *conversation.Conversation) error {
    for turn := 0; turn < a.limits.MaxTurns; turn++ {
        req := a.buildRequest(ctx, c)   // Path + 记忆索引 + 工具清单 + system 提示
        stream, err := a.llm.Generate(ctx, req)
        mid := a.ids.MessageID()        // 预分配关联 ID
        buf := newCommitBuffer(mid)     // 流式缓冲：只进 UI，不进领域
        calls, err := a.consume(ctx, stream, buf) // 边收边 Emit DeltaEvent
        c.AppendCommitted(buf.Commit(a.clock.Now(), outcomeOf(err))) // 一次性不可变提交
        if len(calls) == 0 { return nil }
        for i, call := range calls {
            res, err := a.tools.Execute(ctx, call) // 判定 + 确认 + 超时 + 裁剪
            if err != nil { // 装配级故障（§10）：补剩余失败结果后中止本轮
                abortToolCalls(calls[i:], err)
                return err
            }
            c.AppendCommitted(toolMessage(a.ids.MessageID(), res))
            _ = a.ui.Emit(ctx, port.ToolResultEvent{Result: res})
        }
    }
    return errMaxTurns
}
```

上下文装配规则（含压缩）：

```
装配顺序 = [persona（树内首节点，树内无则 config 兜底注入）]
         + [最新压缩摘要（若有）]
         + [摘要之后的历史]
—— Root 空节点不进上下文；摘要之上除 persona 外一律不回传（D21）。
其余承载：Image Part 内联字节、Audio 取 Transcript、Doc 取截断文本、
          失联 tool 节点文本内联标注〔历史工具结果〕、记忆索引与当前会话记忆文件内容、工具清单（含 mcp:*）；
          思考分片（PartThinking）按 `model.echo_thinking` 二态处理（D42）——
          回传（键缺失 = 开）= 置入该 assistant 消息的 `PromptMessage.Reasoning`，由适配器
          映射为 `reasoning_content` 字段；关（显式 false）= 装配时丢弃不发。
```

**三轨压缩【特色功能】**（D21）：

1. **手动**：`/compact` 调 `Agent.Compact`——把水位上（persona 之后）的历史交给当前模型转写为
   一条 system 摘要节点入树（记 Model/Usage）；失败只报错、树无损。
   摘要按**英文结构化模板**生成（借鉴 OpenCode 压缩提示词，措辞对任意 agent 通用接手）：
   Objective / Requirements / Decisions / Work State（Completed·Active·Blocked）/
   Next Move / Relevant Files / Important Context 七节；首次压缩与链式压缩（已有旧摘要）
   分两态提示词——后者合并更新、新历史优先、对齐 Work State 与 Next Move；
   persona 与系统设定恒回传、不入摘要；输出缺模板小节则带提醒重试一次（两次生成的
   Usage 累计入节点），仍不合格按失败处理。
2. **自动**：**三级 token 计数链**（D26）估算当前请求占用，达 `limits.compact_threshold`
   （默认 0.7 × max_context_tokens）自动触发——①已发生的用服务端实测 usage（自校准）；
   ②未发送的优先用适配器精确计数（`port.TokenCounter`：本地 tokenizer 或 count_tokens API）；
   ③适配器不支持时回退通用估算（ASCII÷4 + CJK÷1.5 + 其他÷2 + 结构开销）。
   每次 Run 至多自动压缩一次；压缩失败回退"最旧裁剪"（保 persona 与最近、丢中间，
   经 `NoticeEvent` 提示"已省略 k 条"）。
3. **自触发**：模型经 `context_compact` 工具（Safe）自行管理上下文。

压缩失败回退**最旧裁剪**（保 persona 与最近、丢中间，产出可发送的请求——
不得留下孤立 tool 结果，并在 UI 提示"已省略 k 条"）；
超预算的硬保底由截断装饰器执行（M4，D14：装饰器在装配根叠加，裁剪/估算逻辑由 `app`
导出注入，装饰器不反向依赖 app；裁到 `max_context_tokens − max(本轮 MaxOutputTokens, 1024)` 之下，
省略发生时发 `NoticeEvent`）。不做向量化、不进记忆文档。

### 7.2 摄取 / 输出管线

```
输入：RawInput → AttachmentStore.Put（二进制先落库）→ Ingestor.Ingest（mic → Transcriber）
      → []Part → Append(RoleUser) → Agent.Run

输出：CommittedEvent → Presenter.Emit（文本渲染 / 图片占位 / 音频播放器）
                  └→ 各 OutputAdapter.Deliver（TTS 播报 / 通知 / 导出），失败只记日志
      —— 扇出点在装配根的 Presenter 装饰器上（D28/D14）：包装实际 UI，仅对
      Outcome=done 的 assistant 提交文本 Deliver；app 与 UI 适配器均不感知输出器。
```

### 7.3 命令体系

| 命令 | 作用 |
|---|---|
| `/new` `/list` `/switch <id前缀>` `/quit` `/exit` | 新会话（清屏，D75）/ 列会话 / **切到既有会话**（唯一前缀解析，清屏并回放目标会话可见历史，D75）/ 退出（`/exit` = `/quit` 别名） |
| `/title [文本]` | 查看 / 改写会话标题（会话元数据，即时落盘） |
| `/compact` | 触发上下文压缩：生成 system 摘要节点，水位上历史不再回传（§7.1 三轨之一） |
| `/permission [等级]` | 查看 / 切换权限等级（read-only/strict/permissive/full-access，写回 config） |
| `/goto <id>` | Head 移到任意节点（分支导航）；**清屏 + 回放**新路径（D81，与 `/switch` 同口径） |
| `/edit <id> [--keep] <文本>` | Revise：默认 Fresh；`--keep` = Carry（保留后续历史） |
| `/branch [id]` | 展示同级分叉（新旧版本对比） |
| `/rm <id>` | Prune 剪子树（二次确认） |
| `/memory [会话id前缀]` | 用系统编辑器打开记忆文件（缺省全局 `memories.md`；带参开会话记忆，D24） |
| `/usage` | 用量查看：当前上下文占用（精确/≈估算）、上轮实测 prompt/completion、会话累计 |
| `/model [name]` | 无参：列 `LLM.Models()` 可用模型 + 当前模型/能力/单价；有参：Agent 内热切换并写回 config（D32，同 `/permission` 模式） |
| `/think [on\|off]` | 无参：显示原生思考开关（缺省开）；有参：切换并写回 config `model.think`（D34）。**总开关**：off 时 `reasoning_effort` 与 `enable_thinking` 一律不发 |
| `/effort [级别]` | 无参：显示当前 `reasoning_effort` 档位；有参：`minimal\|low\|medium\|high\|off`（off = 清除）写回 config `model.reasoning_effort`；**是否真发由 `/think` 决定**，on 且未设档则不发（交服务端默认，D34） |
| `/jobs [list\|logs\|kill]` | 后台任务管理 |
| `/plugin [list\|enable\|disable]` | MCP 插件管理（D31）：list 显示状态/能力/重启与调用统计；enable/disable 写回 config `plugins.<name>.enabled`，即时生效 |
| `/mcp:<server>:<prompt>` | MCP prompts 暴露的动态命令（随插件 enable/disable 注册/注销，§6.3；经 CommandHandler 扩展点） |
| `/help` | 帮助 |

### 7.4 启动历史回放（D40）

启动 `NewSession` 恢复最近会话后（`Store.List` 首条），装配根调用 `Session.ReplayHistory(ctx)`
把**当前分支的可见历史**重放进 UI 转写区，让用户知道"加载了哪个会话、此前聊了什么"：

- **回放范围** = `Path()` 上从水位到 Head：有压缩摘要时从**最新摘要节点**起（persona 之外的
  水位，D21），否则从**persona 之后的首条消息**起；Root 与 persona 不回放（persona 是配置
  快照，非对话内容）。水位裁剪与 `assemblePath` 同口径，但回放是**展示口径**：树内有什么显示什么
  （D42：**含思考分片**，与实时呈现一致），不套用 `model.echo_thinking` 回传开关——
  装配决定发什么，回放展示树里有什么。
- **事件形态**：新增 `port.HistoryEvent{Message}`——一次性呈现**已提交的历史节点**，非 Turn
  流程事件。前段不带 delta/ToolCall 过程，直接按节点角色定稿渲染（user 输入行、assistant
  思考暗块与正文、tool 调用/结果行、system 摘要块），与实时呈现同一套样式。
- **不扇出**：`HistoryEvent` 不是 `CommittedEvent`，输出器装饰器（D28）对它 no-op——
  回放不重复触发通知/TTS。
- **回放前提示**：发 `NoticeEvent`（`已恢复会话 <标题> (<id>)，回放 <n> 条历史`）。
- **时机**：启动恢复时回放一次；`/switch` 切到既有会话与 `/goto` 移 Head（§7.3）时同样
  **清屏（D75 `ClearEvent`）后回放**（D81：转写区须与 Head 一致，`/goto` 此前不回放是缺口）；
  `/new` 只清屏不回放（新会话无可回放）。
- **repl 同权**：repl 前端按行打印同一语义（`> ` 输入行、正文、`[tool]` 行），保证
  e2e/管道输出与 TUI 信息一致。

### 7.5 会话树只读视图（D80，前置 A）

UI 要画分叉按钮（§15.3/S1-1f）与会话树界面（S3）就得知道**树事实**（某节点的同级集合与
自身下标），但 Presenter 只发渲染事件、不给树；UI 又跑在独立 goroutine，直读
`Session.Current()` 会与装配根的树变更并发（`-race` 必红），铁律 5 亦不许 UI 直连 store。

- **端口**：`port.TreeView` —— 单一方法 `Branches(id) (BranchInfo, bool)`；
  `BranchInfo{IDs []conversation.MessageID; Index int}`：同父全部孩子**按创建序**（含自身）、
  `Index` 为自身下标；`id` 不在树中 → `ok=false`。
- **实现**：`*app.Session` 实现该端口（**首个 app 侧端口实现**；方向合法 —— adapter 与 app
  同依赖 port，本端口消费方是 UI 适配器、实现方是 app，与 llm/store 反向）。
- **并发**：Session 内维护**不可变快照**（整树 → `map[MessageID]BranchInfo`），树变更后
  整体重建并经 `atomic.Pointer` 发布；UI 侧无锁读，`-race` 干净。发布时机 = 构造完成 +
  每次 `Handle` 返回前（树变更只发生在 `Handle` 内：命令面与 Turn）。
- **口径**：快照是**展示口径**，不参与推理装配（装配走 `assemblePath`/水位，§7.1）；
  一次输入内的中间态对 UI 不可见（分支形状本就不会在 Turn 中途变化）。
- **不扇出**：只读视图不经 Presenter 事件面（同级数会随后续 Revise 变化，事件无法自我
  更新），也不给任何变更入口 —— 一切变更仍经内核命令（§6/§7.3）。

---

## 8. 存储布局与配置

```
~/.aquarius/
├── config.json                # 主配置（密钥默认存引用名，明文允许但启动警告 D35；permissions.level 权限等级）
├── sandbox/                   # Agent 特权目录（权限矩阵免确认读写，启动自动创建）
├── memories.md                # 全局记忆（markdown 单文件，D23）
├── plugins/<name>/plugin.json # MCP server 描述与可执行文件
├── jobs/<jobID>.log           # 后台任务日志
├── audit.log                  # 审计日志（JSONL：LLM/工具调用的耗时与结果状态，M4 装饰器写入，超限轮转一代）
├── attachments/<sha256>       # 内容寻址附件
├── conversations/<id>.json    # 会话树（写前留一代 <id>.json.bak）
└── conversations/<id>.memory.md # 会话记忆文件（随会话就近存放，D23）
```

```json
{
  "model": {
    "provider": "openai-compatible",
    "name": "gpt-4o-mini",
    "base_url": "https://api.openai.com/v1",
    "api_key": "secret:AQUARIUS_OPENAI_KEY", // 明文允许但启动警告；空值回落本默认引用（D35）
    "tokenizer": "",              // 可选：本地 tokenizer.json 路径（精确计数②，D26；空 = 通用估算）
    "think": true,                // 原生思考总开关（/think 写回；键缺失 = 开，D34）
    "reasoning_effort": "",       // 推理档位（/effort 写回；空 = 不发送，D34）
    "think_tool": false,          // think 草稿工具可见性（默认隐藏，D34）
    "echo_thinking": true,       // 树内思考是否回传给提供商（*bool：键缺失 = 回传，false = 不回传，改后重启生效，D42）
    "unsupported_params": []      // 服务端已知不认的字段名单（自动记录、启动注入省略；含消息级 reasoning_content，D34/D42）
  },
  "ui": { "kind": "gui", "hotkey": "", "theme": "system" }, // kind: gui | tui | repl（D51 默认 gui = 悬浮球前端，D43/§15；tui = D33 bubbletea，repl 为测试/e2e 后端）；hotkey: 全局呼出快捷键（空 = Alt+A，§15.1）；theme: system | light | dark（GUI 深浅，键缺失 = system，§15.4/D61）
  "system_prompt": "",           // 人格（进树为会话首节点的快照源；空 = 内置默认）
  "input": { "asr": "whisper-api", "mic": true },
  "output": { "tts": false, "notify": true },
  "mcpServers": {
    "web-search": {
      "transport": "stdio",
      "command": "web-search-mcp",
      "args": ["--stdio"],
      "env": { "SERPER_API_KEY": "secret:SERPER_API_KEY" },
      "capabilities": ["network"],
      "risk": "safe"
    },
    "docs": {                     // streamable HTTP 形态（M4，D30）
      "transport": "streamable-http",
      "url": "https://mcp.example.com",
      "headers": { "Authorization": "secret:DOCS_MCP_AUTH" },
      "capabilities": ["network"],
      "risk": "confirm"
    }
  },
  "plugins": {                    // 启停与授权状态，两种发现源共用（D31；声明仍在 mcpServers/plugin.json）
    "web-search": { "enabled": true, "granted": ["network"] }
  },
  "permissions": { "level": "strict" },
  "limits": {
    "max_turns": 8,
    "max_context_tokens": 64000,
    "compact_threshold": 0.7,
    "tool_output_chars": 20000,
    "tool_timeout_sec": 60         // 缺省单次调用超时；模型可经保留参数 timeout_sec 覆盖（1–3600，D38）
  }
}
```

配置三级覆盖：CLI flags > 环境变量（`AQUARIUS_*`）> `config.json`。

---

## 9. 权限与安全模型

**权限等级矩阵（D22，取代 D6）**：`config.permissions.level` 四档预设，默认 `strict`。
矩阵格 = **免确认范围**；矩阵外的操作一律逐次确认（`Confirmer`）。`~/.aquarius/sandbox`
（`<dataDir>/sandbox`，启动自动创建）是 Agent 特权目录。读操作全等级免确认（覆盖全盘），
写与工具执行按等级区分：

| 权限等级 | 特权目录 sandbox | 其他目录 | 工具调用 |
|---|---|---|---|
| `read-only` | r | r | （`Confirm` 逐次） |
| `strict` | **rw** | r | （`Confirm` 逐次） |
| `permissive` | **rw** | **rw** | （`Confirm` 逐次） |
| `full-access` | **rw** | **rw** | **√ 全免** |

- **两列分工、互不叠加**（D22）：
  - **文件类**（`file_*`、`memory_write`…）只看**路径格**：`rw` 格内免确认；`r` 格写入
    = 矩阵外 → 逐次确认。`Risk=Confirm` 不再叠加抬高（否则 strict 的 sandbox rw 名存实亡）。
  - **执行类**（`term_exec`、`job_start`…）只看**工具列**：`Safe` 免确认（全等级）；
    `Confirm` 仅 `full-access` 免、其余等级逐次确认。
- 判定顺序：**等级矩阵（免确认）→ 矩阵外 ask（`Confirmer`）**；deny 面由"矩阵不覆盖的能力
  不开放"体现（network/secret 见下表）。
- 策略是纯函数（等级 × 路径格 × Risk → Allow/Ask），落 `domain/perm`；**本轮落配置/策略/展示，
  执行接入在 M2 ToolRunner**。
- capability 表：`fs-*` 与 `exec` 由上述矩阵/工具列接管；`network`/`secret` 属插件与密钥授权流
  （§6.4、按名 allowlist），**不随等级变化**：

| capability | 含义 | 策略 | 备注 |
|---|---|---|---|
| `fs-read` | 文件读取 | 矩阵 r 格（全等级免确认） | `file_*`、Doc 提取 |
| `fs-write` | 文件写入/删除 | 矩阵 rw 格；`r` 格/矩阵外 ask | `file_write/delete`、`memory_write` |
| `exec` | 执行命令 | 工具列（`full-access` 免，其余 ask） | `term_exec`、`job_start` |
| `network` | 网络访问 | 首次 ask 进 allowlist（不变） | MCP 插件声明 |
| `secret` | 读取密钥 | 按名 allowlist（不变） | 仅经 `Secrets` 注入，不回显进上下文 |

- `/permission <level>` 切换**写回 config.json 的 `permissions.level`**（保留其余配置键、仅重排格式），
  立即生效、下次启动沿用；无参则展示当前等级 + 矩阵 + sandbox 路径。
- 输出安全：终端/任务输出视为**不可信数据**，只渲染不执行；工具结果统一截断（`tool_output_chars`）。
- 密钥永不进会话树、附件、日志；`Secrets` 返回值对 Prompt 侧不可见。
  `config.model.api_key` 明文是**显式例外**（D35）：仅常驻 config、启动打印警告（不回显），
  同样不进日志/会话树/附件；stdio 插件子进程环境仍剔除 `AQUARIUS_*`（§6.4 #3）。

---

## 10. 错误、取消与并发语义

- **取消**：`ctx` 取消 ≡ `Stream.Close()`；工具执行同受 `ctx` 约束；Job 后台运行不受会话取消影响（`job_kill` 显式终止）。
- **取消提交**：已生成文本以 `Outcome: cancelled` 提交为节点（可 `/edit` 重试），不留半节点。
- **工具失败**：`Result{OK:false, Err}` 照常回填给模型（让模型自行决定重试或改道），不中断 Turn。
- **装配级故障**：`ToolRunner.Execute` 返回 `error`（确认器缺失/报错、父 ctx 取消）——
  为剩余调用补 `OK=false` 中断结果（保持 tool_calls 一一配对、树仍可装配）后中止本轮，
  不回填让模型空转；取消按"取消提交"收场（返回 nil，主循环经 ctx 收尾）。
- **模型/网络错误**：装饰器层重试（幂等 Generate 的**瞬时错误**——适配器以 `port.ErrTransient`
  哨兵标注 429/5xx/连接中断，装饰器限次 + 指数退避，ctx 取消一律不重试）；仍失败则 `Outcome: error` 提交并上抛 UI。
- **并发**：单会话内 Turn 串行（Head 唯一）；多会话并行安全（一会话一文件）；Job 自管并发。

---

## 11. 测试策略

| 层 | 手段 |
|---|---|
| domain | 性质测试：随机 Append/Revise(Fresh\|Carry)/Prune/Checkout 序列 → 不变量 1–3 恒成立；Carry 边转移后被转移子树零拷贝、身份与内容字节级不变（仅直接孩子的 `Parent` 边指针改写）；节点形态校验含思考分片仅 assistant 可携带（D42） |
| app | 脚本流 LLM + 收集器 Presenter + 脚本队列 Prompter → 交互回放（golden）；摄取管线用假 Transcriber；思考入树（分片顺序）与回传开关两态装配（D42） |
| adapter | LLM 录制流回放；storejson/blobfs/jobproc/memoryfs 契约测试（临时目录） |
| MCP | 测试内起假 MCP server（stdio）跑 mcpgate 契约：发现/调用/超时/崩溃重启/授权拒绝 |
| e2e | 编译出二进制 + repl 适配器喂 stdin 断言 stdout；装配假 MCP server 验证 `mcp:` 工具全链路 |

质量门禁：`go vet` + `gofmt` + 全量 `go test`（含 `-race`）通过方可合入。

---

## 12. 里程碑与验收标准

| 里程碑 | 内容 | 验收标准 |
|---|---|---|
| **M0 骨架** | go.mod、`domain/conversation` + 性质测试、`port` 全量接口、storejson、repl UI、openai 兼容适配器 | 二进制跑通一轮纯文本对话；会话树存取正确；性质测试全绿 |
| **M1 树交互** | Revise(Fresh\|Carry)、Checkout/Branch/rm 命令、golden 回放测试框架 | 回放测试覆盖 Revise 两模式与分支导航；Carry 边转移后后代字节不变 |
| **M2 工具与记忆** | ToolRunner（确认/超时/裁剪）、memory_*、file_*、think、`context_compact`、权限矩阵执行接入、三级 token 计数链 + `/usage`、自动压缩轨、`/memory` 编辑器直开 | 模型可经工具读写记忆；Confirm 能拦截 `memory_write`；等级矩阵在工具链路生效；超阈值自动压缩跑通；`/usage` 展示精确/估算占用与实测累计；`/memory` 打开记忆文件 |
| **M3 任务与多模态** | JobManager + job_* + term_exec、blobfs、Ingestor（文本/文件/剪贴板，程序化入口，D27）、输出器 notify | `term_exec`/`job_start` 经 ToolRunner 确认链路跑通；job 后台跑 + `/jobs` 日志可查；文件/剪贴板输入 → 附件入库 → 装配内联字节端到端；notify 在提交时触发（语音链路见 D27/§14） |
| **M4 MCP 与 TUI** | mcpgate（**stdio + streamable HTTP** 双传输，D30）+ grant（D31）+ `/plugin`、`/model`（D32）、TUI MVP（bubbletea + glamour 轻 markdown，D33；repl 保留为测试/e2e 后端）、装饰器链（重试/硬保底截断/审计，D14/§10）；顺手清 §14 的 M2-P2 与 M3-P3 审查遗留。**Tier-1 不在本里程碑（D29）** | stdio 与 streamable HTTP **各接一个现成 MCP server** 全链路可用（发现→授权→调用→结果回填）；崩溃重启与授权拒绝行为符合 §6.4；TUI 完成一轮对话 + 工具 Confirm；重试/截断/审计在装配根生效；M2-P2/M3-P3 遗留清零后全门禁通过 |
| **M5 GUI 前端** | Gio 悬浮球 GUI（D43/§15）：单组件悬浮球（logo 即球）→ 展开输入栏 → 转写浮层；流式 + 思考暗块定稿折叠（D42）+ 工具折叠 chip + 完整 markdown；Confirm 输入栏确认态、命令补全、附件文件选择框、停止键/排队输入；托盘常驻 + 右键/托盘菜单 + 全局快捷键（默认 Alt+A 可配置）+ 拖拽位置记忆；主题 = 品牌色 `#00AEEF` + 深/浅两版跟随系统（§15.4 令牌 + `ui.theme`，D61）；**窗口管理**（§15.7/D60）：设置/会话历史/欢迎三窗 = 独立常规 OS 窗口——本轮基建 + 设置核心档（模型/权限/think/effort/hotkey/主题）+ 两空窗壳验证宿主。**spike 已过**（2026-09，§15.6：形裁 `SetWindowRgn` 悬浮胶囊） | 悬浮球展开输入栏完成一轮对话（流式 + 思考暗块折叠 + 完整 markdown + 工具 chip + 状态行）；停止键取消本轮、排队输入、Confirm 确认态拦截工具、命令补全含 `/mcp:*`；附件按钮 → 文件选择 → 入树内联展示；菜单切会话/主题/退出；**托盘/右键菜单可开三窗（历史/欢迎为占位壳）；设置窗改核心档写回 config 并热生效（模型/权限/think/effort/hotkey/主题），密钥只写不回显；主题深浅两版可切、`system` 跟随系统**；Alt+A 呼出 + 位置记忆；`go build ./cmd/aquarius` 仍单二进制（无 cgo）、全门禁通过、repl/tui 回归不受影响 |

---

## 13. 已决决策记录（ADR 摘要）

决策记录已拆分至 **[docs/decisions.md](docs/decisions.md)**（与本文同权威；编号 D1–D61 跨文件不变，
`§13` / `Dn` 引用仍有效）。新决策在该文件追加，本节不再维护。

## 14. 暂缓事项（Backlog）

- MCP sampling（server 借用宿主模型）
- **厂商私有思考块回传**：Anthropic extended thinking 工具续跑须原样回传 thinking block；
  GPT-OSS/vLLM 系回传键名 `reasoning`（D42 现发 `reasoning_content`，vLLM 兼容两者）——
  接入此类提供商时由适配器做键名/块格式映射
- 非 GBK 的遗留代码页（CP437/latin-1 等）终端输出识别（D41 内容探测只有 UTF-8/GBK 两档，会误判成乱码中文）
- `file_read` 等文件文本入口的遗留编码解码（与 D41 同算法，终端之外的文本入口）
- MCP 插件主动健康检查与按需懒加载（当前：启动即连接 + 被动 `Wait` 感知，§6.4 #4）
- **Tier-1 Go 插件（D29 由 M4 移入）**：`pluginapi/v1` 独立 go.mod 契约 + `adapter/plugingo`
  编译期装载器（范围随后续里程碑定稿；M4 只做 Tier-2 MCP）
- **GUI 后续（M5/D43 之外，§15 预留）**：悬浮球本体转常规窗口形态（D43 只预留设计
  不实现；功能窗用独立常规窗已入正册 §15.7/D60）、多颜色主题预设（M5 只做品牌色
  `#00AEEF` + 深/浅两版）、拖拽/粘贴/语音按钮（走同一
  `UserInput.Raw` 入口，摄取管线已就绪，见下方语音条目）
- **语音输入与播报（D27 移入）**：
  - ASR（`Transcriber`）/ TTS（`Synthesizer`）适配器选型与实现（whisper-api / 系统朗读 / 云 TTS）
  - 麦克风采集（`Kind=mic` 摄取）
  - `/attach` `/clip` `/mic` 输入命令与 TUI 拖拽/粘贴/语音按钮（M4 TUI 为 MVP 不含，D33——
    留 TUI 后续迭代或 GUI，走同一 `UserInput.Raw` 入口）
  - 导出文件输出器（`output.tts` 同批启用）
- **M2 审查遗留（P3，2026-09 评审；P2 的 trim 工具 schema 估算已修）**：
  - regexp2 `MatchTimeout` 与计数路径的 ctx 检查（模型可控输入的回溯爆炸防护）
  - Agent/Runner 共享可变状态的并发模型显式化（多会话共享实例时加锁；含 LLM 适配器
    `NoteUnsupported` → config 写回的并发竞争——D34 既有，非 D42 引入）
  - `think` 参数非空校验；压缩摘要流的 UI 标注（"正在生成摘要"以区别于回答流）；
    `/usage` "上轮实测"文案改"最近实测"
- 跨分支"摘抄"共享子树（DAG 化）
- Job 表持久化（SQLite）
- PDF/Office 等 Doc 提取器插件
- 图片 OCR/描述自动降级、音频直输模型（等模型能力普及）
- 长期记忆巩固（记忆 = 文档，无独立记忆系统；**上下文压缩摘要已入正册** §4.1/§7.1，与记忆无关）
- Web UI、移动端输入方式（GUI 桌面前端已入正册 §15/D43/M5）
- 向量检索记忆后端（作为 MemoryStore 插件）

---

## 15. GUI 前端（`internal/adapter/uigui`，D43 / M5）

GUI = **换壳不换核**：Gio 实现同一套 `uiFrontend`（`port.Presenter + Prompter + Confirmer` +
Say/Prompt/SetInterrupt/Close，装配面见 `cmd/aquarius`），`ui.kind=gui` 接入装配 switch；
repl（测试/e2e 后端）与 tui（默认）不动，D28 输出器装饰器自动继承。本节只描述**壳内**设计。

### 15.1 形态与窗口（悬浮球模型）

- **单组件悬浮球**：logo 圆钮是唯一常驻物，输入栏是它的展开态——悬浮球右侧展开输入栏，
  再次左键 logo 收起回球；**不存在球 + 胶囊两个独立组件**（两套焦点/生命周期）。
  logo **悬浮 tips** 承载启动提示（`Aquarius — 输入 /help 查看命令，/quit 退出；Ctrl+C
  取消当前生成`）——装配根对 `ui.kind=gui` 不再发 Say 启动行，转写区初始即空；
  悬浮提示为独立底板元素（随位图合成），悬停即现、移开即消（移开判定 = 事件态 × 光标
  直采 + 心跳复评，D53——分层窗按像素命中穿透，透明像素/窗外零事件收不到 Leave）。
- **展开/收起动画（D54；输入栏几何经 D76 分段导出）**：不再瞬时翻形，双通道**严格先后**——展开 700ms = 输入栏
  260ms `easeOutBack(c1=1.2)` 轻回弹 → 消息区 440ms CSS ease `cubic-bezier(.25,.1,.25,1)`；收起 540ms = 消息区 320ms → 输入栏 220ms `easeInSine`（消息区时长 ≈ 输入栏近两倍，揭示比控件归位更从容）。**输入栏几何 = send 行程为钟的分段导出（D76）**：
  logo 不动，`barP` 即 send 从 logo 矩形到终位的插值进度（回弹过冲可 >1、`clampRowX` 只平移收界）；
  胶囊按分界点 split（send.Min 抵达胶囊成圆处 ≈120/528）分两支——**缩/长段**（sendP ≥ split）左缘钉
  pillEnd.Min（与 logo 间隙恒 12dp）、右缘 = `send.Min − 12`（与 send 间隙恒 12dp，**send 全程同步
  移动**）；**平移/合球段**（sendP < split）胶囊已成 ⌀48 圆（宽 = 行高、停 pillEnd.Min），与 send 同步
  插值、**同时抵达** logo 合球（展开反向 = 圆先弹出到 split 再长宽）。时间线由原曲线导出：收起缩圆
  ≈0–188ms + 合球 ≈32ms，展开分开 ≈15ms + 生长至 260ms（≈118ms 满宽、≈182ms 过冲峰 = **回弹落在
  生长段**，过冲期双间隙仍恒 12；瞬发段为曲线起步/收尾极快的直接后果，拍板接受）；夹取**先 send 后
  胶囊**（贴边夹掉后双间隙仍恒 12）。内容按终宽排版随胶囊裁剪；logo 最后绘制、p=0 时与收起球一致。
  **消息揭示 = 动画淡出带**：内容静止，带顶从转写区底（全隐）升到 0
  （静息），带内 smoothstep 淡入、带顶以上不可见；带底夹在转写区内（不压状态行/输入行），
  升到 0 时与 §15.3 顶带逐像素重合（三处带机制由固定顶带泛化为动态带）。
  `collapsed` 仍即时翻转，layout 分支以 `expandAn.active` 区分（动画走全量 layout 插值）；
  进度在帧分支 `stepExpand` 现算（D50 ticker 底座，**置于 layout 之前**，headless 不起
  ticker）；中途再点 logo 从当前进度反向不跳变，动画期间 logo 可点、胶囊/send/tips 不可点。
- **胶囊内容显隐（D77，补 D54/D76）**：输入栏动画期**内容隐藏**、由独立 alpha 时间线显隐（Gio
  `paint.PushOpacity` 组透明层，仅 α<1 压层；胶囊底板不透明 → 命中/焦点/手势不变）——**淡入** =
  输入栏阶段完成（bar 260ms 到位进消息阶段）触发 180ms CSS ease，与消息揭示并行；**淡出** = 收起
  启动布防、延迟 140ms（320−180）开始，**与消息区收起同一刻结束**，bar 收缩段（320→540ms）胶囊
  已空（与展开「空胶囊长出」对称）。范围**仅胶囊内**（附件槽/占位文字与光标/展开槽/确认动作区），
  右圆 send 键与 logo 不参与（send 保持 D76 动画期可见移动）。时间线随 `stepExpand` 帧推（与动画
  同址、layout 之前），唤帧 ticker 存续到动画与时间线**都**结束；静止 alpha 由 `collapsed` 推导
  （expandProgress 同款），动画中无时间线时保持现值——收起中途反向先**取消进行中的淡出**（不闪隐），
  bar 完成触发淡入从现值续到 1。
- **悬浮形态实现 = 整窗 ULW 位图（D62；headless 底座 spike 实证 §15.6）**：Gio 窗口
  本质不透明（`gpu.Clear` 写死不透明白），"全悬空"经**单一像素通道**实现——headless
  同布局离屏重渲（D44 底座）升级为唯一像素源：内容 + 顶带/揭示带渐变 + 元素边缘羽化按
  `av = vis(形状) × g(y) × 内容alpha` 一次合成为整窗预乘 BGRA 位图，
  `UpdateLayeredWindow(ULW_ALPHA + AC_SRC_ALPHA)` 提交到**主窗 HWND**（分层窗）。
  位图 alpha 即形状（自带抗锯齿；代价同旧形裁：无 DWM 阴影）也即命中——alpha=0 的
  元素间隙与窗口边角**点击穿透到下层窗口**（分层窗逐像素命中）；羽化边带 alpha>0
  可命中，视觉与命中一致（优于旧形裁让位边带的"死区"）。**统一半透明** = ULW
  `SourceConstantAlpha`（每帧随 `u.alpha` 提交；D50 停靠淡化 `semiAlpha`→`dockAlpha`
  同帧跟随——旧 `LWA_ALPHA` 与形裁、overlay 一并退役）。**元素边缘羽化（D45–D48
  剖面保留）**：每像素 vis = 核心 1 → 边带 smoothstep 渐隐 → 真实轮廓处
  `featherEdgeMin`（0 = 淡到全透明），**沿元素真实轮廓**（未按视口/淡出带裁剪——
  不沿裁切线描边，跨带形状不留横缝）、**只在形状内落笔、不向外外扩**（旧向外环在
  `d=0` 有折点 →「饱和核心 + 外圈亮带」，已否决）；羽化宽全元素统一（D47/D48 响应式框架保留；D68 收细：
  ratio 0.03、上限 3dp——换壳后 A/B 观测羽化仍优于硬切；D70 修订参考边：按元素自身短边算会令气泡/chip/胶囊各得不同带宽，同屏边缘剖面软硬不一）：
  `fw = clamp(round(Dp(inputRowDp) × featherRatio), Dp(featherMinDp), Dp(featherMaxDp))`、
  且 ≤ 元素短边 1/3——取输入胶囊（48dp 短边）带宽为全元素统一值，随 DPI/缩放自适应，不引入固定 px。颜色取同帧
  headless 内容（文字/图标随渐隐自然淡出），headless 不可用（`src` 缺省或尺寸不符）
  才用元素底色兜底（`writePremulFill`，形状可见可点、无文字）。g(y) = 带渐变
  smoothstep（带顶 0 → 带底 1，D54 揭示带/§15.3 顶带同式）——带顶以上 av=0 不可见。
  降级：非 Windows / headless 失败 → 常规不透明窗渲染照常（win32 桩 no-op）。
- **同拍合成（D55 → D58 → D59 → D62 单通道收口）**：像素、形状、透明度、效果层全部
  在同一张 ULW 位图里生成、一次提交——主窗 swapchain 内容 / 形裁 / overlay 三通道并存
  时期的亚帧错位（D58/D59 三轮截图迭代实证：同一次序不同帧结果不一致 = 窗口线程队列与
  Gio ack→Present 的交错竞态，非次序可治）在结构上不再可能。帧次序固定为
  `stepExpand → stepAnim → layout → compose(全帧合成) → commit(移窗) → submit(e.Frame)
  → present(ULW)`：全帧合成是唯一慢段（离屏重渲 + 预乘，先跑完）；移窗一拍 flush
  （帧内只 `requestMove` 记账，D55 机制保留）；`e.Frame` 保留（事件路由/IME/vblank
  节奏零改动，其画面被 ULW 位图覆盖）；ULW 殿后提交（取实测窗口矩形定位）。
- **启动显隐时序（D78，补 D62）**：启动曾闪现「未定制窗口」（Q8/1d——Gio `Configure(ShowWindow)`
  早于 `Win32ViewEvent`，D62「分层窗首 ULW 前不显示」依赖**建窗时**挂样式的语义，后挂不成立）→
  改**显式状态机**（`revealPending`，事件循环/托盘线程 atomic）：`onHWND` 挂接即 `SW_HIDE`
  （最早可接管点；其前 Configure→挂钩的亚帧间隙无更早挂钩点，接受为限制），`WS_EX_LAYERED`
  保留（D62 双保险）；合成/提交门放行启动期（`presentable = mainVisible || revealPending`——
  隐藏是我方所为、须照常合成，否则永不首帧）；**首帧 ULW 提交成功即揭示**
  （`ShowWindow + SetForegroundWindow` 激活前台，与现状启动聚焦一致，首个可见帧带内容）；
  首帧前托盘/快捷键呼出**只置展开态、不 ShowWindow**（窗口由首帧自现，防提前显闪）；
  位置记忆/置顶/停靠恢复全在隐藏期完成（揭示即在记忆位、无跳动）。GPU 离屏失败永不首帧 →
  **保持隐藏**（D62 降级本无可显示像素；托盘在、可经菜单退出）。
- **右键 logo** = 菜单（D72 补全「菜单步」，项清单经 D73 调整）：**原生弹出菜单 = 新对话 /
  权限（二级菜单四档 = 设置窗同源 `permLevels`：read-only / strict / permissive /
  full-access，当前档打勾；选中注入 `/permission <档>` 与键入同路径——D22 写回 +
  热切换）/ 消息历史 /
  设置 / 置顶（勾选当前态）/ 隐藏 / ─分隔线 / 退出**——以用户显式清单为准（会话切换 /
  主题 / 欢迎入口不进右键菜单）。手势 = 区域内右键按下武装、**原位抬起**弹出（移出/取消
  不弹，右键拖走无效）；热区两态同源——展开 logo 钮 + 收起球（收起态注册整窗，事件
  本只落球像素，D62 逐像素命中），动画期不响应（D54）。检测在 Gio 事件循环，
  `PostMessage` 投**托盘线程**呈现 `TrackPopupMenu`（独立消息泵、不嵌 Gio 泵，
  TPM_RETURNCMD + 光标位）；命令分发与托盘菜单共享（`menuIt`/`runMenu`/`menuDispatch`）。
  **新对话 = 注入 `/new`**，与键入同路径同语义（`/new` 发 `ClearEvent` 清屏，D75——
  「转写视图不自动清」的旧口径作废）。
- **托盘常驻生命周期**：关窗（Alt+F4）= **隐藏**——子类化主窗过程吞 `WM_CLOSE`
  （Gio 无关闭拦截 API，`WM_CLOSE` 直落 `DefWindowProc` 即销毁），主窗隐藏即像素层
  一并消失（D62 后无独立 overlay）；**退出只经托盘菜单**（清托盘图标与快捷键 → **中断进行中轮次**（`interruptNow`，D64——退出即终止，不等待 Agent 完成）→ EOF 收尾）。主窗**不进任务栏
  与 Alt+Tab**（D51：`onHWND` 挂接句柄时一次性经窗口线程置 `WS_EX_TOOLWINDOW`、清
  `WS_EX_APPWINDOW`——托盘/快捷键是唯一入口，任务栏条目与悬浮球形态相斥）。
  托盘 = `Shell_NotifyIconW`（图标**内嵌** `assets/icon/aquarius.ico`，单二进制；
  Explorer 重启后图标重挂留后续），左键显隐、右键菜单。
- **全局快捷键**：默认 **`Alt+A`**（D43 修订 2026-09：原 `Alt+Space` 与输入法/开始
  菜单冲突面大），`ui.hotkey` 可改（设置面后补，先走配置文件），注册失败回退
  `Ctrl+Alt+A`，再失败仅托盘可用。语义 = **展开/收起互切**：窗口隐藏 → 呼出并展开
  输入栏（焦点入栏）；窗口可见 → 展开 ↔ 收起回球互切；**隐藏只经托盘**（左键显隐 /
  菜单）。
- **置顶开关（菜单调节）**：托盘菜单与右键 logo 菜单（D72，共享分发）均带「窗口置顶」勾选项，
  切换主窗 `HWND_TOPMOST/NOTOPMOST`——**本端 `SetWindowPos` 显式断言**（不依赖 Gio 的
  TopMost 应用路径：实测主窗置顶态会意外丢失、原因未明）并**持久化**（与位置记忆
  同文件，键缺失 = 缺省置顶）。**置顶即整窗置顶**（D62 后像素单窗、无 overlay 同步问题——
  旧「overlay 置顶态逐次对齐主窗」机制随 overlay 退役）。
- **位置**：拖拽移动（把手 = logo / 输入栏空隙 / 状态行；气泡区是滚动区、不拖窗）
  + 位置记忆（含多显示器，随 config/本地状态持久化）。**不出桌面（D50/D52）**：拖动中
  可见锚点（收起 = logo 球、展开 = **输入栏三段包围盒**——D52：转写消息区允许越出
  桌面上沿，只限制输入栏；整窗含形裁剔除的透明边距，实测把输入栏弹离桌面边缘约
  20px、展开态顶部不可近）实时夹进最近显示器工作区，位置恢复与拖动
  共用同一夹取口径（重启不跳位）；抬手时锚点距任一边 ≤ `snapDp`(12dp) 即贴齐——
  **四边吸附、两态皆可，不做拖动中磁吸**。**停靠隐藏（D50）**：**仅收起态**可停靠，
  且球贴齐**左右外侧边**（该边之外无相邻显示器——跨屏接缝不触发，"滑出"才藏得住）；
  触发 = **停在可停靠边 + 曾悬停 + 光标移开**（悬停 = 布防，启动恢复无悬停不自动滑出；
  召回移开、召回后拖回可停靠区移开走同一判定）——滑出屏幕留 `dockSliverDp`(8dp) 窄条 +
  整窗淡化（`semiAlpha`→`dockAlpha`，ULW `SourceConstantAlpha` 同帧跟随），`dockDurMs`(220ms)
  ease-out 插值（ticker 唤帧、进度事件循环现算）；**悬停窄条滑回复亮（召回）**，
  召回后无操作光标移开则重停靠；点击/呼出（Alt+A、托盘显示 = 召回 + 展开）立即脱离。
  **唤帧底座（D50 实测修订）**：悬停/移开判定 = 帧 + 光标直采，而位图窄条上的指针
  悬停**可能零帧**（实测：停靠窄条悬停 2s 无一帧 → 召回永不触发，点击却必有帧能唤出）
  ——故**收起态且球停在可停靠边（含停靠中）期间常驻 50ms 心跳 `Invalidate`** 主动唤帧；
  展开态、球不在可停靠边（无从停靠）则静默零帧。**纯点击不夹取不吸附**：位移 ≤
  `dragClickSlackPx` 的抬手跳过夹取/吸附——保住「点击脱离停靠」落下的半出屏贴边位，
  否则展开态整窗夹取把它推离边缘、收起后球离边超 `snapDp` → 布防/停靠周期断链（实测缺陷）。
  停靠态随位置记忆持久化（`docked` 键 = left/right，缺省未停靠；恢复按当前工作区重算
  停靠位、存的 X/Y 忽略，外侧边判定失效则落回贴边可见）。
- **转写浮层**：提交后在胶囊上方出现，**无底板——消息以双色气泡悬浮呈现**（§15.3），
  自动高度、可滚动、生成时长高；**每轮清空 + 可固定**（钉住后累计显示、可拖高）——
  历史在会话里，经菜单切会话查看。
- **状态行**：浮层底栏仅生成时显示「思考中/生成中 · 模型 · 权限档」（TUI 状态行等价物，
  数据源同 `Status` 回调：Model/Level/Effort 现取）；用量详情入口为待办（logo 菜单现为六项，见 §15.1/D72）。
- **悬浮球本体不转常规窗口形态**（只预留设计、不做实现，§14）；功能窗（设置/
  会话历史/欢迎）用**独立常规 OS 窗口**承载（D60/§15.7）。

### 15.2 输入栏与交互（视觉稿 = Figma 稿 `Untitled.fig` 的 canvas，本地稿未入库；D49：canvas 1:1 为几何基准，`temp/ui_design.png` 是截图、不作依据；idle 形态）

**三段式独立元素（无外层容器胶囊）**：**logo 圆钮**（⌀48、r24，兼展开/收起钮，圆钮**内嵌品牌图标** `assets/icon`）｜间隙 12｜**输入胶囊**（高 48、r24 全圆，`grow` 随窗口宽伸缩）｜间隙 12｜**发送键圆钮**（⌀48、r24）。三段等高、垂直居中、彼此分离——各自羽化边缘（位图合成），间隙透明且点击穿透；几何 1:1 取自 canvas（元素 48、间距 12、内边距 16、图标槽 20、文字 15sp），默认窗宽 608dp 下行宽 576 = 设计稿、输入区恰 456。

胶囊内（左右内边距 16、元素间距 12）：**附件图标槽**（20×20 灰占位、不可点，实现附件时启用）｜占位 *"Ask anything or type a command..."* 15sp（`grow`）｜**展开图标槽**（20×20 灰占位、不可点）｜状态动作区（确认态 = [允许/拒绝] 按钮组；生成中胶囊内无动作——停止在右圆）。

**发送键 = 主题色圆形钮 + 向上箭头图标**（悬停 tooltip「发送」）；生成中右圆变**停止键**（错误色 + 方块图标，`SetInterrupt`）；确认态右圆**置灰**不可点（几何不随状态变）。

| 交互 | 语义 |
|---|---|
| 展开按钮 | 输入栏变大（多行长文本编辑），可再收起 |
| Enter / Shift+Enter | 发送 / 换行（展开态多行编辑同规则） |
| 附件按钮 | 系统文件选择框 → `UserInput.Raw{Kind:"file"}` → ingestfile 既有管线（拖拽/粘贴/语音留 §14；槽位已按稿预留、当前为不可点灰占位） |
| 输入 `/` | 命令补全浮层（含 MCP 动态命令 `/mcp:*`，数据源 `Session.Handle` 命令面） |
| 生成中 | 发送键变**停止键**（`SetInterrupt` 取消本轮）；输入框保持可输入，新消息**排队**待本轮结束 |
| Confirm（工具确认） | 输入栏**切换为确认态**（胶囊内变 [允许/拒绝] 按钮组、右圆置灰），非模态弹窗 |

### 15.3 转写浮层与消息流呈现

| 元素 | 呈现 |
|---|---|
| 消息气泡 | **双色气泡**标示角色：user = 品牌色系气泡、assistant = 浅白气泡；思考/工具/notice 为非气泡文本行（气泡群内弱化样式） |
| 分叉切换（D81） | 有同级分叉的节点在气泡下方显示 `◀ i/n ▶`（`i` = 自身在兄弟创建序中的下标 +1）：点击经**输入通道**投递 `/goto <兄弟id>`（与键入同路径，壳内不旁路），内核移 Head + 清屏回放（§7.4）；按钮为独立点击热区、不挂行选（D63）；数据面 = `port.TreeView`（D80/§7.5，每帧无锁读快照） |
| 思考（D42） | 流式**实时暗块**（灰色小字 + "思考中…"）；定稿折叠为「已思考 · Ns」行可展开；启动回放同款——展示口径恒含思考，与 `model.echo_thinking` 回传开关解耦 |
| 工具调用/确认/结果（D67） | **单气泡合并 chip，默认折叠**：头部行 `▸ 🔧 name · 参数预览 · 状态`（…运行中/待确认/✓/✗），点击展开参数全文、权限问答与结果全文（可选可复制，不再截 120 字预览）；权限确认并入 chip（待确认自动展开；/rm 等非工具确认仍为文本行） |
| assistant 正文 | **完整 markdown**（D65 首增量 = 结构块：标题/代码块等宽卡/列表/引用/分隔线，行内剥标记保文本；语法高亮 + 复制按钮、表格、链接点击、行内富样式与行内图片为后续增量） |
| NoticeEvent / ErrorEvent | 流层内淡色提示行 / 错误块 |
| 流式 vs 定稿 | 对齐 D33 口径：流式阶段原样追加，`CommittedEvent` 后定稿渲染（思考折叠、markdown 全量） |
| 顶边淡出 | 转写区顶部固定高度**淡出带**：气泡滚出顶部走垂直 alpha 渐变消失（非硬切）；**滚动内容顶部预留一个带高的可滚空白（D74）**——滚到最上时首行完整落在带下可读，否则首行困在带内、`scrollPx` 不可为负而永远半透明（= 淡出遮挡内容）；实现见下 |
| 底边淡出（D79） | 转写区底部**常驻矮带**（`fadeBandBottomDp` = 12dp，远矮于顶带 56dp）：底缘内容渐隐、不再被视口下沿硬切；**滚动内容尾部垫一个等高的可滚空白**（同 D74 口径、随内容滚）——尾随/短内容贴底（D56）时末行底 = 视口底 − 带高，正好落在带外不受影响；上滚离底时下方内容延伸进带内渐隐。带止于转写区底；**带只乘在转写视口登记的形状上**（作用域 = 形状 clip 底 ≤ 带底）——状态行/输入栏在带之下、悬浮 tips 等 chrome 形状用整窗 clip 登记（几何上可跨进带内），一律不受淡化 |
| 滚动与贴底（D56） | 转写区是**滚轮滚动区**（把手只在输入栏/状态行，气泡区只滚不拖窗）：**底部锚定**——内容矮时贴底悬在输入栏上方，高过视口后旧消息上滚进顶带渐隐；**尾随贴底**（`d>0` = 向新内容）：流式新消息始终贴底，上滚离底即停跟随、滚回底部恢复跟随。滚轮增量按**垂直轴**夹取、边界由当帧钳制负责（D56）。**手势钉点（D71）**：滚轮真有增量即手势进行中——当帧位图把光标像素 alpha 顶到 ≥1 维持逐像素命中，光标不动跨间隙连续可滚（透明间隙不再吞滚轮），光标移位/收起/停靠即解除恢复穿透 |

**淡出实现（D44 起、D62 并入单通道，spike 已过 §15.6）**：淡出带不再挖空主窗、也没有
独立 overlay 窗——带渐变作为乘性因子 `g(y)` 并入整窗 ULW 位图（§15.1 单通道）：

1. **像素源** = `gioui.org/gpu/headless` 离屏渲染同一布局：清屏为透明 → alpha 通道 =
   内容覆盖（**免布局掩码同步**）；`Metric.PxPerDp` 对齐主窗 DPI；零值 `Source` 纯渲染
   已验证（`io/input`：zero-value = disabled）。
2. 全帧合成 `av = vis(形状) × g(y) × 内容alpha`（headless 像素 = **预乘线性 + sRGB
   存储**语义，按 decode/encode LUT 转为 ULW 所需的字节空间预乘；vis 剖面见 §15.1
   羽化）。g(y) = smoothstep——顶带**带顶 0 → 带底 1**（D54 揭示带/静息顶带同式，
   带顶以上 av=0 不可见）；**底带（D79）反向 1 → 0**，乘到转写区底收零、带下恢复 1
   （状态行/输入栏在带之下不受淡化）。
3. `UpdateLayeredWindow(ULW_ALPHA + AC_SRC_ALPHA)` 提交**主窗 HWND**，
   `SourceConstantAlpha` = `u.alpha`（统一半透明/停靠淡化，同帧跟随）。
4. **带/非带衔接**：同一位图内 g(y) 与 vis 连续相乘——顶带带底 g→1 与带下无缝、跨带
   形状无横缝（旧「region 方顶续接」机制随形裁退役，不再需要）；底带（D79）在转写区
   底收零后带下回 g=1 是断点，但转写形状裁剪上沿即带底、状态行/输入栏形状自带底起，
   无同形状跨越 → 亦无缝。

约束：淡出带内**不放交互控件**（零值 Source = 禁用态渲染、与主窗启用态有色差；带内
恒为文本气泡则无差异）；带内像素随 g(y) 半透明、命中按位图 alpha（不参与交互，滚动
从带下方起效）；headless 上下文随窗口尺寸/DPI 变化重建，内容/滚动/位置变化驱动重渲。

**§9 清洗**：所有不可信文本（流式 delta、思考、工具参数/结果、历史回放、命令输出）
出口统一剥控制序列（对齐 `sanitizeControl`）；渲染不执行任何标记语言的活动内容。

**文本选择与复制（D63）**：转写文本行挂 Gio `widget.Selectable`——拖选 / 双击选词 /
三击整行 / Ctrl+C 复制选区（经系统剪贴板）/ Ctrl+A 行内全选；选择状态按行序缓存
（markdown 复合行内为（行,块）双键，D66；行文本变化自动清选区，流式与会话切换
同路径）；适用行 = 用户/助手/普通/notice/错误/系统/思考正文/工具 chip **展开体**
（D67：chip 头部为点击热区不挂行选）；选区高亮随 widget 状态进 fade
位图（D62 单通道两遍渲染同帧同值）。配套：**拖层把手带收窄**为状态行 + 输入栏
（转写区不注册拖层——`gesture.Drag` 的 pointer.Grab 先到先得，否则拖层抢走行选
的 Drag 事件）；**编辑器焦点收口**为 `focusPending` / 自带点击取焦 / 初始焦点
（废除每帧常驻焦点——与行获焦同帧竞态会把选区焦点抢回）。

**markdown 渲染（D65/D66）**：定稿助手文本经 goldmark（CommonMark，依赖树既有）解析为
结构块（段落/标题/代码块/列表/引用/分隔线），`frameItems` 渲染期展开、**单回复单气泡**
——一条消息一行，行内垂直复合多块、共底板（D66 消息级复合行；代码块为气泡内嵌套
等宽小卡）；行选按（行,块）双键挂接，行机制/滚动/形状登记零特例，解析按原文缓存
（上限 256）；行内剥标记保文本（Label 单一样式，行内富样式留 richtext 后续增量）；
live 草稿不解析（D33 口径，流式原样、定稿渲染）；代码块用 Go Mono 等宽面（theme
集合补面，CJK 缺字自动回落）。详见 D65/D66。

### 15.4 主题

- **令牌化主题系统**（颜色/圆角/间距/字号为令牌）；预设 = 纯数据，加色不改代码。
- **MVP**：品牌色 `#00AEEF`（logo、发送键；取自 `assets/icon`）+ 输入框浅白/浅灰；
  深/浅两版，**默认跟随系统**。多颜色预设后补（§14）。
- **实施口径（D61，先于设置窗落地）**：v1 先落**颜色令牌**（背景/文字/气泡双色/思考
  暗块/状态行/错误色/禁用态等）**深浅两版纯数据预设**，主界面自绘路径与 material 主题
  同读令牌；几何令牌（圆角/间距/字号）只做结构、不随主题变体（多预设后补，§14）。
  **`ui.theme` = `system | light | dark`**（键缺失 = `system`，未识别值亦按 `system`，
  §8）：`system` 经 Win32 `AppsUseLightTheme` 注册表读取、主窗 `WM_SETTINGCHANGE`
  广播实时刷新；切换**热生效**（下一帧重绘），设置窗主题档同口径；次窗经原子快照
  同读同一预设（§15.7 并发模型）。

### 15.5 契约与并发

- `uigui` 实现 `uiFrontend` 六个面，语义与 uitui 一致：`Next/Confirm` 对调用方阻塞
  （channel 桥接），`Emit/Say/Confirm` 从装配根 goroutine 投递事件循环（线程安全）；
  EOF 与排队输入的优先级结构照抄 uitui 修复后的口径（先取尽排队输入再判 EOF），不重踩竞态。
- 渲染状态机对齐 `uitui/model.go`：`Delta.Reasoning` 分流进思考草稿、`CommittedEvent`
  定稿落块、`HistoryEvent` 回放（思考暗块先行）、`Say` 纯文本块。
- 测试：GUI 自身用**无窗口 headless 逻辑测试**（渲染状态机/桥接层抽纯逻辑）+ 投影收集器；
  门禁与 e2e 仍跑 repl 后端，GUI 不进 CI 图形路径。帧内提交次序是契约（D55/D58/D59
  历史次序被 **D62 单通道**取代）：抽 `frame(gtx, submit)` 方法 + `framePhase` 测试回执
  （生产恒 nil），无窗口断言「compose → commit → submit → present」（全帧合成先行、
  移窗 flush 在 `e.Frame` 之前、ULW 提交殿后），并断言 submit 时移窗**已** flush、
  帧尾已 flush；全帧合成（av = vis × g(y) × alpha 预乘）另有参考实现对照测试
  （内容不透明 / 间隙全零 / 带行渐变 / 羽化 ramp）。

### 15.6 前置 spike 结论（2026-09 已过，D30 同款记录）

| §15.6 清单项 | 实测结论 |
|---|---|
| 纯 Go 构建 | ✅ `CGO_ENABLED=0` 通过（Gio v0.10.2 Windows 纯 Go，无 cgo） |
| 悬浮胶囊形态 | ✅ **悬浮形态**（D44–D59 期 `SetWindowRgn` 形裁；D62 起整窗 ULW 位图——位图 alpha 即形状/命中/穿透，自带抗锯齿）；~~色键 `LWA_COLORKEY`~~ 与 D3D swapchain 不兼容（白屏，WinUI3#8469/SDL#15751 同类）；~~DWM 圆角~~ 多一圈窗口描边——两者排除 |
| 半透明 | ✅ `LWA_ALPHA` 常量透明可用（D44–D59 期与形裁叠加）；D62 起随整窗 ULW 位图走 `SourceConstantAlpha`（同帧提交） |
| 整窗 ULW 换壳（D62） | ✅ 主窗 `WS_EX_LAYERED` + 每帧 `UpdateLayeredWindow(ULW_ALPHA+AC_SRC_ALPHA)` 提交全帧合成位图：alpha 即形状/命中/穿透；内容 = headless 全帧重渲（单通道——形裁/overlay/swapchain 三通道并存的亚帧错位结构性根除） |
| 系统托盘 | ✅ `Shell_NotifyIconW` + 消息窗口 + 右键菜单（纯 Go syscall） |
| 全局快捷键 | ✅ `RegisterHotKey` 注册成功（spike 用 Alt+Space 验机制；默认值改 **Alt+A**——组合键与注册无关，Ctrl+Alt+A 回退逻辑备着） |
| 多显示器 + 位置记忆 | ✅ 显示器枚举/工作区夹取/`SetWindowPos` 定位 + JSON 位置记忆 + 重启恢复 |
| 中文字形 | ✅ 系统 `msyh.ttc` → `opentype.ParseCollection` 加载成功（gofont 无 CJK，M5 中文渲染走系统字体） |
| Gio 真透明 | ❌ 不存在（`gpu.Clear` 强制不透明白，仅 js 平台透明）；每像素透明需 `UpdateLayeredWindow` CPU 位图或 DirectComposition，与 Gio GPU 路径冲突——D44–D59 形裁绕开，D62 起经 headless 全帧重渲 + 主窗 ULW 承载 |
| 淡出像素源（headless 离屏渲染） | ✅ spike/headless：`gpu/headless` 清屏 = 透明（alpha 通道 = 内容覆盖，**免布局掩码**）；零值 `Source` 纯渲染不 panic（含 `gtx.Execute`/material 控件）；`PxPerDp` 可控（125% 缩放实测通过）；像素语义 = **预乘线性 + sRGB 存储**（ULW 前需 decode/encode 转字节预乘） |
| 主窗捕获（PrintWindow） | ❌ spike/printwin：`PW_RENDERFULLCONTENT` **连可见区都返回全零** → 捕获路线排除；淡出像素源改 headless 离屏渲染（D44） |
| 滚轮路由 / 手势钉点（D71） | ✅ spike/wheelpcap：`WM_MOUSEWHEEL` 按光标**逐像素命中**路由（alpha=0 间隙流失、与焦点无关）；**`SetCapture` 不改路由**（捕获持有、透明像素上仍 0 送达）→ 捕获方案排除；光标像素 **alpha≥1 即送达**（1/8/32/64/128/255 全档实测）→ 钉点方案成立。前置：`LockOSThread` 线程亲和（首跑 goroutine 迁移使泵/捕获失灵）+ 进程 DPI 感知（否则钉点落错物理像素、结论失真） |

**实现铁律（M5 必守，堆栈实证）**：

1. **修改性 Win32 调用一律经 `Window.Run` 送窗口线程执行**（`SetWindowPos`/`SetWindowLongPtr`/
   `SetLayeredWindowAttributes`/`SetWindowRgn` 等内部回投窗口过程；Gio runLoop 停在
   `deliverEvent` 的 select、不泵消息，跨线程调用死锁）。查询类（`GetWindowRect`/`GetCursorPos`）不受限。
2. **拖动定位用光标屏幕坐标绝对跟踪**（按下记「窗口左上角 + 光标位置」，按差值定位）；
   指针本地增量法与窗口移动互为反馈，会回弹。
3. **帧内提交次序（D55 提出、D58/D59 修订、D62 单通道收口）**：Gio `processFrame` 先
   ack 本帧再 `Present(1,0)`（阻塞到下一 vblank），ack 之后经 `Window.Run` 排队的
   Win32 调用要等 Present 返回才被窗口线程服务；而主窗是 bitblt 交换模型，内容同样要等
   Present 拷贝后的**下一次合成**才渲染——D44–D59 期主窗内容 / 形裁 / overlay **三通道
   并存**、采样点各异（`SetWindowRgn` 随下一次 Present 采样、`UpdateLayeredWindow` 立即
   生效），D59 的按通道分边混合次序仍留亚帧竞态（同一次序不同帧结果不一致，实测止损）；
   **D62 起像素单通道化**（整窗 ULW 位图承载形状/效果/透明度），按通道分边作废，次序
   仅剩工程约束：全帧合成（唯一慢段）→ 移窗一拍 flush（`requestMove` 记账，D55 保留）
   → `e.Frame`（事件路由/帧节奏，画面被 ULW 覆盖）→ ULW 提交殿后（取实测窗口矩形定位）。

### 15.7 窗口管理（多窗宿主，D60）

三类功能窗采用**独立常规 OS 窗口**（各自 Gio `app.Window`、有边框），与悬浮球形态解耦
——**悬浮球本体仍不转常规窗口形态**（§14 口径不变，卡的是球自己变普通窗，不是功能窗）。
本轮范围：**窗口管理基建 + 空窗壳**；设置窗随主题步（D61）一起落地；欢迎（首次运行）
与会话历史只落空窗壳验证宿主，数据面留后续步。

- **窗口清单**：**设置**（核心档：模型 provider/name/base_url、权限档、think/effort、
  `ui.hotkey`、`ui.theme`；密钥只写不回显、不回显明文——D35）｜**会话历史**（占位壳；
  列表数据面依赖未来切会话命令，§14）｜**欢迎/首次运行**（占位壳；接管「写模板即退出」
  启动流为后续步）。
- **形态**：`Decorated(true)` 常规窗——**不接**主窗专属机制：无 ULW 形状位图（常规
  不透明窗）、无位置记忆、不置顶、不拖拽把手；任务栏条目照常
  （`WS_EX_TOOLWINDOW` 只挂主窗 HWND，次窗不参与）。
- **并发模型**：每窗独立事件循环 goroutine、**自持状态**；跨窗只经消息（沿用 `uiMsg`
  模式）与线程安全回调（`Options` 下发，同 `Status` 口径），**不共享裸字段**（`-race`
  门禁兜底）。主窗专属全局态显式隔离、次窗一律不碰：`win32Run` 单槽（§15.6 铁律 1）、
  `mainHWND`、ULW 像素管线（fadeState/fadeBuf）、位置记忆/停靠、`WM_CLOSE → 隐藏` 子类化。
- **生命周期**：单实例防重开（同 kind 已开 → raise/focus）；次窗关闭 = **真关闭**
  （不走主窗「Alt+F4 = 隐藏」语义）；托盘隐藏 / Alt+A 显隐**不连带**次窗；退出只经
  托盘菜单 → 收编关闭全部次窗 → EOF 收尾。
- **入口**：托盘菜单带「设置 / 会话历史 / 欢迎」项，右键 logo 菜单带「消息历史 / 设置」项（§15.1/D72，
  两菜单项集合各自为集），项随各窗步启用、未落地者置灰。
- **设置窗读写契约**：读 = `Options` 快照回调（线程安全，同 `Status`；文本键以
  config 文件为事实源，权限/模型/思考/档位取运行态访问器）；写 = 单一 patch 回调
  **在装配根实现**（复用 config 泛键改写——`ui.*` 与 `model` 的 provider/base_url/
  api_key 一次落盘），并返回 diff 运行态得出的**内核命令计划**（`/permission`
  `/model` `/think` `/effort`）——计划经输入通道排队、与键入命令同路径串行执行、
  回执进转写区；模型/权限/思考/effort 的热生效与命令同口径——树/装配相关的语义仍走
  内核命令，壳内不旁路。壳侧热生效各归其位：快捷键 = 原子槽 + 托盘线程重注册
  （失败回退），主题 = `themeMsg` 事件循环应用。密钥只写不回显（快照结构无密钥字段，
  D35）。
- **测试（§15.5）**：窗口注册表与生命周期抽纯逻辑 + 假开窗器 headless 测（open/
  防重开/close/退出收编）；设置 patch 构造与回调序列 headless 测；GUI 不进 CI 图形路径。
