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
| `Presenter` / `Prompter` | uitui（TUI）、repl | Tier-1（v1 不开放） | 收集器 / 脚本队列 |
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
| `/new` `/list` `/quit` `/exit` | 新会话 / 列会话 / 退出（`/exit` = `/quit` 别名） |
| `/title [文本]` | 查看 / 改写会话标题（会话元数据，即时落盘） |
| `/compact` | 触发上下文压缩：生成 system 摘要节点，水位上历史不再回传（§7.1 三轨之一） |
| `/permission [等级]` | 查看 / 切换权限等级（read-only/strict/permissive/full-access，写回 config） |
| `/goto <id>` | Head 移到任意节点（分支导航） |
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
- **时机**：仅启动恢复时回放一次；`/goto` 切换会话、`/new` 不回放（命令回显已足够，§14）。
- **repl 同权**：repl 前端按行打印同一语义（`> ` 输入行、正文、`[tool]` 行），保证
  e2e/管道输出与 TUI 信息一致。

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
  "ui": { "kind": "tui" },
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

---

## 13. 已决决策记录（ADR 摘要）

| # | 决策 | 否决的替代方案 |
|---|---|---|
| D1 | 节点不可变；用户"改"= 创建同级新节点 | 就地可变消息（v2 曾选）——放弃，换取崩溃安全与分支对比 |
| D2 | Revise `Carry` = **边转移** | 深拷贝子树（数据翻倍、身份分裂）；DAG 共享（复杂度不值） |
| D3 | 流式中间态不进领域，Turn 结束一次性 Commit | 节点随流变异（半节点/部分写入问题） |
| D4 | Tier-2 插件协议 = **MCP**（mcpgate 适配） | 自造 JSON-RPC（三方接入成本高、生态为零） |
| D5 | MCP sampling 反向调用 v1 拒绝 | —（有真实用例再议） |
| D6 | ~~文件工具默认沙箱 = 家目录，越界逐次 Confirm~~（**被 D22 取代**：权限等级矩阵 + sandbox 特权目录） | 全路径裸放（误伤面大）；严格 chroot（对个人助手过重） |
| D7 | `Prune` 硬删，storejson 写前留一代 `.bak` | 软删除/墓碑（个人数据不留坟墓） |
| D8 | Job 表 v1 内存态，日志落盘 | SQLite 任务表（量级不足，暂缓） |
| D9 | UI 插件 v1 只留契约不开放装载 | 开放多 UI 并存（焦点/路由复杂度不值） |
| D10 | Doc Part 文本提取 v1 简单截断内联 | 完整 PDF/Office 提取器（列为 Ingestor 插件面扩展） |
| D11 | 记忆 = markdown 文档 + 关键词检索 | embedding/RAG、自动记忆巩固（个人规模不需要） |
| D12 | 工具调用引用放宽为"存在性引用" | 严格祖先引用（与 D2 边转移冲突） |
| D13 | 内置能力与三方插件同走端口契约（内置不享特权） | 内核直连内置能力（内核腐化） |
| D14 | 横切能力用装饰器，不进插件 API | 插件中间件链（API 面失控） |
| D15 | 虚拟 Root 是唯一根，顶层消息可多条（`Head==""` = 游标在虚拟 Root）（**root 载体被 D19 修订为实节点**：多顶层语义保留，载体改为 Root 实节点、移除空串特例） | 严格单根消息——Revise 首条消息将被禁止，与"改任意消息"的招牌能力冲突 |
| D16 | Carry 边转移 = 改挂 `Children` 边 + 改写直接孩子的 `Parent` 边指针；Revise 的 Head 语义见 §4.1 | "边指针永不改写"（那就只能拷贝子树，回到 D2 否决项） |
| D17 | `Usage` / `BlobRef` 由 `domain/conversation` 持有，`port` 直接引用 | port 再造同型 DTO（双份定义易漂移，且 `Part.Ref` 本就要用） |
| D18 | tool 节点不可 Revise；`Prune` 连带清理失联 tool 结果节点 | 允许编辑机器生成的结果（破坏存在性引用不变量）；Prune 后留失联引用（同上） |
| D19 | Root 实节点化：ID 复用会话 ID（Root 即会话）、新增 `RoleRoot`、移除 `Head/Checkout` 空串特例、Prune/Revise 拒 Root、旧格式破坏性切换（修订 D15 载体） | 保留虚拟 Root + 空串特例（API 留魔法值、不变量两套表述）；Root 另造常量/ULID ID（同一棵树两个根 ID，易漂移） |
| D20 | persona 进树为会话首节点（system 角色，config `system_prompt` 快照，恒回传，可 Revise/Prune） | 只在装配期注入 config（人格随会话不可分叉/不可编辑、审计不到树上） |
| D21 | 上下文压缩三轨（手动 /compact 本轮、超阈值自动 70%、`context_compact`/Safe 自触发，后两轨 M2）；摘要 = system 水位节点，其上不回传（persona 除外），失败回退最旧裁剪 | 只裁剪不摘要（丢上下文）；摘要存记忆文档（与记忆系统混淆，§3 已排除）；旁路字段存摘要（与树两处存放、重启/回溯易失配） |
| D22 | 权限等级矩阵四档（默认 strict）+ sandbox 特权目录，两列分工（文件看路径格、执行看工具列）、矩阵外一律 ask、`/permission` 写回 config（取代 D6） | 家目录沙箱（D6：目录身份与等级正交，表达不了"特权/其他"两档）；矩阵外 deny（个人助手过严）；allow/ask 明细键与矩阵双事实源（判定顺序绕） |
| D23 | 记忆布局 = 全局单文件 `memories.md` + 会话级 `conversations/<id>.memory.md` | `memory/**/*.md` 目录树（个人单文件即可直读直编，目录树徒增组织成本）；会话记忆独立目录（与会话树分家，迁移/删除要同步两处） |
| D24 | `/memory` = 系统编辑器直开记忆文件（无子命令） | `list\|show\|edit\|rm` 子命令集（编辑器即最强编辑 UI，命令面保持极简） |
| D25 | ToolRunner 落位 `internal/adapter/toolrun`，文件类权限经可选接口 `FileTarget` 由工具**自申报**目标路径 | 按工具名前缀硬编码分类（内核腐化、三方工具无法参与）；往 `tool.Spec` 塞权限字段（污染模型可见的工具声明） |
| D26 | token 计数**三级链**：①服务端实测 usage（已发生的）→ ②适配器可选 `TokenCounter`（本地 tokenizer.json / count_tokens API，覆盖估算）→ ③通用字符估算 + 服务端 usage 自校准；tokenizer 经 `model.tokenizer` 指路径**启动时加载、错误 fail-fast** | 通用估算一刀切（已可拿到精确值时不拿）；词表 embed 进二进制（+数 MB 且换模型即失效）；实现 Jinja chat_template 渲染（要引模板引擎，且结构开销用常数已够准）；强推 count_tokens API（openai-compatible 普遍没有） |
| D27 | **M3 范围调整**：M3 落 JobManager + blobfs + Ingestor 管线 + notify 输出器；REPL 输入命令面（`/attach`/`/clip`/`/mic`）与 ASR/TTS/麦克风移入 §14 backlog，随 TUI 后续迭代或 GUI 落地（M4 TUI 为 MVP 不含，D33） | 硬凑"语音提问 → TTS 播报"验收（REPL 行式输入无拖拽/语音按钮；语音适配器选型未定，先定契约后装实现） |
| D28 | 输出器扇出点 = 装配根的 **Presenter 装饰器**：包住实际 UI，收到已完成的 assistant `CommittedEvent`（`Outcome=done`）后逐个调 `OutputAdapter.Deliver`，失败只记日志 | app 内直连输出器（内核直连具体实现违反 D13；扇出属横切，按 D14 走装配根装饰器） |
| D29 | **Tier-1 Go 插件后移出 M4**（`pluginapi/v1` + `adapter/plugingo` 移 §14，随后续里程碑落）；M4 只做 Tier-2 MCP | M4 双线并进（§12 验收只针对 MCP；Tier-1 会挤占 TUI、装饰器与审查遗留的容量） |
| D30 | MCP 客户端用官方 **`modelcontextprotocol/go-sdk`**（纯 Go，stdio `CommandTransport` + streamable HTTP `StreamableClientTransport` 双传输）；引入前先 spike 实测，API 不合则回退自写 stdio JSON-RPC + `net/http` SSE。**spike 已过（2026-09）**：双传输回环、工具调用、`IsError` 工具级错误语义均验证；sdk 要求 **go≥1.25**，go.mod 由 1.22 上调 | 长期自写协议栈（帧格式/能力协商/HTTP 流重连易踩 spec 细节）；cgo/Node 系客户端（违背纯 Go 优先） |
| D31 | 插件**启停与授权状态**统一存 config `plugins.<name> = {enabled, granted[]}`，两种发现源（`mcpServers` / `plugin.json`）共用；声明与状态分离 | 状态写回 `mcpServers` 条目（plugin.json 发现的插件无处安放）；状态存 plugin.json（本机授权态不该随分发文件走） |
| D32 | `/model <name>` = Agent 内热切换 + **写回 config**（同 `/permission` 模式，重启沿用） | 每会话独立模型（与"全局唯一 model 配置"冲突，切换语义碎片化）；只切不存（重启即失） |
| D33 | TUI **MVP** = 转写区 + 流式 + 输入框 + 命令历史 + Confirm 对话 + 状态行 + glamour 轻 markdown（committed 后渲染，流式阶段原样）；图片/音频仍占位；`ui.kind` 模板默认 `tui`、repl 保留；GUI 框架后移 §14 | 一步到位富 TUI（拖拽/语音/内联图——与后续 GUI 框架重复投入）；TUI 取代 REPL（e2e/CI 丢失无终端后端） |
| D34 | **思考控制面**：`/think [on\|off]` = 原生思考**总开关**（覆盖 `/effort`），`/effort [minimal\|low\|medium\|high\|off]` = `reasoning_effort` 档位；请求发 `reasoning_effort` 与 `enable_thinking`（dashscope 系布尔）两个字段，服务端点名不认 → **同请求剥离重试 + 记录进 config `model.unsupported_params`**（启动注入、以后直接省略）；思维链分片**只展示**（展示口径——入树与回传被 **D42 修订**：随节点入树、回传走 config）；`think` 草稿工具可见性走 config `model.think_tool`、**默认隐藏**（不做 /models 能力探测——兼容端几乎不返回能力信息） | 逐家私有布尔映射表（每家一个开关字段，维护面爆炸）；/models 能力探测后自动分叉（探测不可靠、分支形同虚设）；~~思维链入树（回传可能被服务端拒绝且占上下文）~~（**被 D42 取代**：入树且默认回传；拒收顾虑由剥离重试兜底、上下文顾虑交 config 关） |
| D35 | `model.api_key` **允许明文**：启动打印警告（不回显密钥）、`secret:` 引用仍走 `port.Secrets`；值为空时回落默认 `secret:AQUARIUS_OPENAI_KEY`；文件内值优先 | 维持明文一律拒绝（用户明确要简化接入）；明文静默启用（丢失风险告知） |
| D36 | **面向 agent 的文本一律英文**：进入模型上下文的字符串（system 提示、工具声明与参数描述、工具回填结果与错误）用英文；仅面向用户的界面、命令输出、启动警告与日志用中文 | 中英混杂（模型上下文语言口径漂移，回复语言只应由 system 提示约束）；全站改英文（用户界面跟着变，违背中文协作约定） |
| D37 | persona 创建时附加**运行环境块**：platform、UI 形态与 TERM、特权沙盒目录（`IsAbs` 才附）、缺省调用超时与 `timeout_sec` 说明；随 config 快照入树（D20 语义不变），字段为空整块跳过 | 装配期动态注入（与 D20 快照语义冲突，分叉/回溯后环境漂移）；不告知（模型不知道沙盒与平台，只能踩坑后学） |
| D38 | 单次工具调用超时 = `limits.tool_timeout_sec` 缺省，模型可经**保留参数** `timeout_sec`（整数 1–3600）逐次覆盖，**允许高于 config**；schema 自声明该参数的工具（`job_start`）不受保留参数约束；执行前从 Args 剥离（MCP 服务端不收未知参数）；值域外/非整数报错回填；发现性经 persona 环境块一行说明 | 每个工具各加超时参数（16×schema 重复、MCP 工具无法参与）；不可覆盖（长任务与 `sleep` 撞默认 60s）；静默夹取（掩盖模型传参错误，`0` 还会被误读为不限时）；完全无上限（失控面不可控） |
| D39 | 新增 `sleep` 内置工具（Safe：1–3600 秒、ctx 可中断、超缺省时长须配 `timeout_sec`） | 只靠 `job_start` + 轮询日志（重量级）；不提供（模型无法自然停顿 / 等待后台任务收尾） |
| D40 | 启动恢复会话经 **`HistoryEvent` 回放可见历史**（水位 → Head，见 §7.4）+ NoticeEvent 提示会话身份 | 复用 `CommittedEvent` 回放（会触发 D28 输出器重复通知/TTS，且 user/tool 节点在两前端的 Committed 语义是 no-op、渲染不出）；复用 Say 拼纯文本（丢角色样式、TUI 与 repl 各拼一遍易漂移） |
| D41 | 进程输出在 **jobproc 适配器内按行解码为 UTF-8**：UTF-8 合法则原样（ASCII 与显式 `chcp 65001` 输出），否则 GBK/CP936 解码；x/text 宽松解码器以 U+FFFD 兜底"两者都不是"，`term_exec` 同步输出与 `job_logs` 日志同口径 | 给子进程强灌 `chcp 65001`（改变命令运行环境，依赖 OEM 代码页的老程序反而乱码，且控制台代码页是共享状态）；调 `GetConsoleOutputCP`/`GetOEMCP` 精确解码（平台特定代码，子进程 stdout 是 pipe 时与控制台代码页未必一致——内容探测已覆盖真实两档 65001/936）；交 UI 层清洗（字节 → string 转换时 U+FFFD 已产生，事后不可恢复） |
| D42 | **思考过程入树 + config 控制回传**（修订 D34）：新增 `PartThinking` 分片（仅 assistant 可携带，节点形态校验把关；流内分片合并至多一段置于正文前，取消/出错终态的已生成思考同样入树）；回传走**独立承载** `PromptMessage.Reasoning` → openai 适配器序列化为 assistant 消息的 `reasoning_content` 字段（**2026-09 调研**：OpenAI 官方 Chat Completions 每轮丢弃推理、也不返回明文思维链——官方端点不触发回传；DeepSeek 等兼容端点带 `tools` 时**强制**回传、缺失即 400；`reasoning_content` 是兼容生态事实标准），端点点名不认则复用 D34 剥离重试管线（扩展到消息内字段）记入 `model.unsupported_params` 后省略；开关 config `model.echo_thinking`（`*bool`：**键缺失 = 回传**，显式 false 关，改后重启生效）；展示口径（实时暗块、启动回放）恒含思考，与回传开关解耦 | 维持"只展示不入树"（D34 原状：重启/回溯即丢、审计不到树上，违背"树是唯一事实源/用户主权"）；旁路字段或独立文件存思考（D21 否决同款理由：两处存放、回溯易失配）；独立 system/tool 节点承载思考（破坏一轮一 assistant 节点与工具配对语义）；`<thinking>` 文本拼进正文（DeepSeek 工具轮缺 `reasoning_content` 字段仍 400，且思考被当正文污染上下文）；默认不回传（对 DeepSeek 类端点是工具轮硬故障——调研后由"默认关"翻案为"默认回传"）；厂商私有思考块原样回传（Anthropic thinking block 等，列 §14 backlog） |

## 14. 暂缓事项（Backlog）

- MCP sampling（server 借用宿主模型）
- **厂商私有思考块回传**：Anthropic extended thinking 工具续跑须原样回传 thinking block；
  GPT-OSS/vLLM 系回传键名 `reasoning`（D42 现发 `reasoning_content`，vLLM 兼容两者）——
  接入此类提供商时由适配器做键名/块格式映射
- 非 GBK 的遗留代码页（CP437/latin-1 等）终端输出识别（D41 内容探测只有 UTF-8/GBK 两档，会误判成乱码中文）
- `file_read` 等文件文本入口的遗留编码解码（与 D41 同算法，终端之外的文本入口）
- `/goto`、`/new` 切换会话时的自动历史回放（当前仅启动恢复时回放一次，D40/§7.4）
- MCP 插件主动健康检查与按需懒加载（当前：启动即连接 + 被动 `Wait` 感知，§6.4 #4）
- **Tier-1 Go 插件（D29 由 M4 移入）**：`pluginapi/v1` 独立 go.mod 契约 + `adapter/plugingo`
  编译期装载器（范围随后续里程碑定稿；M4 只做 Tier-2 MCP）
- **GUI 框架接入（D33 移入）**：换掉 TUI 壳，复用 `port.Presenter + Prompter + Confirmer`
  契约与全部内核（TUI 薄壳即为该契约预留的换壳面）
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
- GUI / Web UI、移动端输入方式
- 向量检索记忆后端（作为 MemoryStore 插件）
