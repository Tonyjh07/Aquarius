# Aquarius 初始设计方案（最终版）

> **状态：定稿（Initial Design, Final）** ｜ 技术栈 Go（单二进制）｜ 架构六边形 + 插件化
>
> 本文是 Aquarius 的**唯一权威设计文档**，覆盖并取代此前的 v1–v4 草案（DDD 全景 → 个人极简 →
> 不可变树/插件化 → MCP/多模态）。所有"待决事项"已在 §13 落定为决策；未尽事宜按 §14 暂缓处理。

---

## 1. 项目定位与设计原则

**Aquarius 是一个面向个人的、交互优先的极简 AI 助手。**

- 核心是**对话体验**：流式交互、消息修订分支、回溯对比；不是编码工作流产品。
- **极简内核**：无 subagent、无任务编排层、无消息总线。内核只有三件事：会话树、上下文装配、Turn 循环。
- **多模态出入**：文本 / 图片 / 文档 / 语音皆可输入，文本 / 语音 / 通知皆可输出；输入输出**方式**可插拔。
- **记忆 = 文档**：markdown 文件即记忆，用户可直接编辑；模型经工具读写；检索 = 关键词。
- **不可变节点 + 用户主权**：历史节点永不修改；"改"= 创建同级新节点，是否携带后续历史由用户选择。
- **通用能力内置**：后台任务、文件操作、终端执行。**没有工作区概念**（无项目根/索引/监听）。
- **内核极简、边缘可插**：工具、模型、记忆后端、命令、UI、输入/输出方式全部是插件面；
  进程外扩展统一走 **MCP**；内置实现与三方插件走同一契约（**内置不享特权**）。
- 交付物是**单个二进制**，数据全部在 `~/.aquarius/`。

架构原则：

1. 六边形架构：`domain` 纯 Go、零依赖；外部世界全部是端口（`port`）的适配器。
2. **两个稳定级**：`pluginapi/v1` 对外严格 semver；`internal/port` 是内核内部的缝、自由演进。二者类型独立。
3. 横切能力（重试、限流、审计日志、截断）用**端口装饰器**叠加，不进插件 API。
4. 会话树是纯追加结构：一切变化 = 新增节点 / 增删边 / 边转移 / 移动 Head。
5. 插件**永远不能直接操作会话树**，一切经内核中转。

---

## 2. 统一语言

| 术语 | 英文 | 定义 |
|---|---|---|
| 会话树 | Conversation | 一棵不可变消息树 + 一个 Head 游标 |
| 节点 | Message | 树节点，创建后只读：角色、内容分片、工具调用/结果、终态、用量 |
| 内容分片 | Part | 消息内容的多态片段：Text / Image / Audio / Doc |
| 附件 | Attachment | 被消息引用的二进制内容，sha256 内容寻址存储 |
| 根 / 头 | Root / Head | 虚拟起点 / 当前游标，决定发给模型的线性路径 |
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
**工作区**（项目目录抽象/代码索引/文件监听）、自动摘要记忆、embedding 检索。

---

## 4. 领域模型

### 4.1 会话树（`domain/conversation`）

```
            Root(虚拟)
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
    PartText  PartKind = "text"
    PartImage PartKind = "image"
    PartAudio PartKind = "audio"
    PartDoc   PartKind = "doc"
)

type Part struct {
    Kind       PartKind
    Text       string   // Kind=text；Kind=doc 时为提取文本（截断）
    Ref        *BlobRef // Kind=image|audio|doc：附件引用
    Transcript string   // Kind=audio：ASR 转写文本（模型只见文本，音频留附件库回放）
}

type Message struct { // 创建后只读，值语义
    ID         MessageID
    Parent     MessageID // "" = 根消息
    Role       Role      // user | assistant | tool
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
    Children     map[MessageID][]MessageID   // 结构边
    Head         MessageID
    RevisedFrom  map[MessageID]MessageID     // 新→旧：版本链（纯结构元数据）
    CreatedAt, UpdatedAt time.Time
}

type KeepMode int

const (
    Fresh KeepMode = iota // 新节点空白开始；旧节点的子树原样留作历史分支
    Carry                 // 旧节点的子树边转移到新节点；旧节点成为"旧版本"叶子
)

func New(id ID, title string) *Conversation
func (c *Conversation) Append(role Role, content []Part) Message
func (c *Conversation) Revise(id MessageID, content []Part, mode KeepMode) (Message, error)
func (c *Conversation) Prune(id MessageID) error            // 剪掉 id 及整棵子树
func (c *Conversation) Checkout(id MessageID) error         // Head 移到任意节点
func (c *Conversation) Path() []Message                     // Root→Head 线性序列
func (c *Conversation) Branches(id MessageID) []Message     // 同级分叉（UI 对比新旧版本）
func (c *Conversation) Find(id MessageID) (Message, bool)
```

**不变量（3 条，性质测试守护）**：

1. **树合法**：至多一个根；无环；`Parent/Children` 双向一致；`Head` 属于树。
2. **引用合法**：`tool` 节点的 `CallID` 匹配**树中存在**的某 assistant 节点的 `ToolCalls[i].ID`（存在性引用，
   以支撑 Carry 边转移；Path 上"失联"的 tool 节点在 Prompt 装配时按文本内联并标注 `〔历史工具结果〕`）。
3. **节点不可变**：`Nodes` 只增不改；一切变化 = 新增节点 / 增删边 / 边转移 / 移动 Head。

**关键语义**：

- **Revise Carry = 边转移**（否决深拷贝子树）：节点内容不依赖祖先指纹（不像 git commit），
  改写历史只需把 `Children[id]` 这批边改挂到新节点，后代节点字节级不动。树仍是一棵树，无 DAG。
- **流式中间态不进领域**：增量只流经 `Presenter`；Turn 结束（或取消）一次性 Commit 不可变节点
  （取消 = `Outcome: cancelled` + 已生成部分文本）。没有半个节点，崩溃恢复无部分写入问题。
  节点 ID 在 Turn 开始时预分配，作流事件关联 ID。
- `Prune` 是唯一破坏性操作；`storejson` 写文件前保留一代 `.bak` 防误删（§13-D7）。

### 4.2 附件与多模态承载

- 附件内容寻址（sha256）存 `~/.aquarius/attachments/<hash>`；同内容多处引用只存一份；
  GC 按引用计数清扫（Prune 会话不立即删附件）。
- 模型能力差异：`ModelInfo{Vision, Audio}` 声明能力；不支持图片的模型遇到 Image Part →
  明确报因并提示（v1 不做自动 OCR/描述降级）。
- Audio Part 一律先经 `Transcriber` 得到 Transcript；模型只见文本。Doc Part 注入提取文本（截断），
  原文可经 `file_read` 工具取用。

### 4.3 内置工具（全部经 `port.Tool` 契约，与三方插件同权）

| 工具 | 说明 | Risk |
|---|---|---|
| `memory_list` / `memory_read` / `memory_search` | 记忆文档读取与检索 | Safe |
| `memory_write` | 写入/覆盖记忆文档 | Confirm |
| `think` | 显式整理思路（no-op） | Safe |
| `file_read` / `file_list` / `file_search` | 通用文件读取（默认限家目录） | Safe（越界=Confirm） |
| `file_write` / `file_delete` | 文件写入 / 删除 | Confirm |
| `term_exec` | 终端命令同步执行（超时返回，输出截断保头尾） | Confirm |
| `job_start` | 后台任务启动 | Confirm |
| `job_list` / `job_status` / `job_logs` / `job_kill` | 后台任务管理 | Safe |

- 文件工具是**裸通用文件操作**：没有 cwd 工作区、项目根、索引、监听。默认沙箱 = 用户家目录，
  越界路径逐次 Confirm（§13-D6）。
- 后台任务 = 独立进程 + 日志落盘 `~/.aquarius/jobs/<id>.log`；任务表 v1 内存态（§13-D8）。
- 三方工具经 MCP 网关加入，命名隔离：`mcp:<server>:<tool>`。

### 4.4 记忆文档

- `~/.aquarius/memory/**/*.md`，用户可直接用编辑器改，下次读取即生效。
- 无 embedding、无自动遗忘、无冲突消解；检索 = 关键词匹配。后端是插件面（`port.MemoryStore`）。

---

## 5. 端口设计（`internal/port`）

依赖方向：`adapter → port ← app → domain`；`domain` 不 import 任何端口；port 只含接口与 DTO。

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
    ToolCalls []tool.Call
    CallID    string // role=tool
}

type GenerateRequest struct {
    Model    string
    Messages []PromptMessage
    Tools    []tool.Spec
    Params   Sampling        // Temperature、MaxTokens、Stop
    Budget   TokenBudget     // MaxOutputTokens、MaxCostUSD
}

type ModelInfo struct {
    Name           string
    Vision, Audio  bool
    CostIn, CostOutUSD float64 // 每千 token 单价
}

type Usage struct{ InputTokens, OutputTokens int; CostUSD float64 }

type Delta struct {
    Text      string
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
```

### 5.2 `tool.go` —— 工具端口

```go
type Tool interface {
    Spec() tool.Spec
    Execute(ctx context.Context, call tool.Call) (tool.Result, error)
}

type ToolRunner interface { // 查找 + capability 校验 + Risk 确认 + 超时 + 结果裁剪
    Specs(ctx context.Context) ([]tool.Spec, error)
    Execute(ctx context.Context, call tool.Call) (tool.Result, error)
}

type Confirmer interface {
    Confirm(ctx context.Context, prompt string) (bool, error)
}
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
type BlobRef struct{ Hash, MIME, Name string; Size int64 }

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

### 5.9 `ui.go` / `misc.go`

```go
type Event any // DeltaEvent{MessageID, Delta} | ToolCallEvent | ToolResultEvent
               // | CommittedEvent{Message} | ErrorEvent
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
| `LLM` | openai 兼容 / anthropic / ollama | Tier-1 Go 插件 | 脚本化 Stream |
| `Tool` | memory_*、file_*、term_*、job_*、think | **MCP server** | fake tool |
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
| 内核侧接入 | `adapter/plugingo` | `adapter/mcpgate`（MCP client → port 实现） |
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

### 6.4 插件宿主职责（`internal/plugin`）

1. **发现**：扫描 `plugins/*/plugin.json` + config 启停；Tier-1 由 main 注册。
2. **校验**：manifest schema、API 版本协商、provides 自检。
3. **授权（grant）**：`capabilities` 首次使用弹确认 → 写入 config allowlist；`risk=confirm` 工具逐次走 `Confirmer`；
   密钥经 `Secrets` 按名注入插件环境，不落明文配置。
4. **生命周期**：按需懒加载 → 健康检查 → 崩溃自动重启（限次）→ 关机优雅 `shutdown`。
5. **可观测**：记录每个调用的 插件名/耗时/结果状态，供 `/plugin` 命令查看。

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
        for _, call := range calls {
            res := a.tools.Execute(ctx, call)     // capability 校验 + Risk 确认 + 超时 + 裁剪
            c.AppendCommitted(toolMessage(a.ids.MessageID(), res))
            _ = a.ui.Emit(ctx, port.ToolResultEvent{Result: res})
        }
    }
    return errMaxTurns
}
```

上下文装配规则（v1 故意简单）：`Path()` 全量（Image Part 内联字节、Audio 取 Transcript、Doc 取截断文本、
失联 tool 节点文本内联标注）+ 记忆索引 + 工具清单（含 `mcp:*`）+ 一条 system 提示；
超预算从最旧裁剪（保 root 与最近 N 条）并在 UI 提示"已省略 k 条"。不做自动摘要。

### 7.2 摄取 / 输出管线

```
输入：RawInput → AttachmentStore.Put（二进制先落库）→ Ingestor.Ingest（mic → Transcriber）
      → []Part → Append(RoleUser) → Agent.Run

输出：CommittedEvent → Presenter.Emit（文本渲染 / 图片占位 / 音频播放器）
                  └→ 各 OutputAdapter.Deliver（TTS 播报 / 通知 / 导出），失败只记日志
```

### 7.3 命令体系

| 命令 | 作用 |
|---|---|
| `/new` `/list` `/quit` | 新会话 / 列会话 / 退出 |
| `/goto <id>` | Head 移到任意节点（分支导航） |
| `/edit <id> [--keep] <文本>` | Revise：默认 Fresh；`--keep` = Carry（保留后续历史） |
| `/branch [id]` | 展示同级分叉（新旧版本对比） |
| `/rm <id>` | Prune 剪子树（二次确认） |
| `/memory [list\|show\|edit\|rm]` | 记忆文档管理 |
| `/model [name]` | 查看/切换模型 |
| `/jobs [list\|logs\|kill]` | 后台任务管理 |
| `/plugin [list\|enable\|disable]` | 插件管理 |
| `/help` | 帮助 |

---

## 8. 存储布局与配置

```
~/.aquarius/
├── config.json                # 主配置（密钥只存引用名）
├── memory/**/*.md             # 记忆文档
├── plugins/<name>/plugin.json # MCP server 描述与可执行文件
├── jobs/<jobID>.log           # 后台任务日志
├── attachments/<sha256>       # 内容寻址附件
└── conversations/<id>.json    # 会话树（写前留一代 <id>.json.bak）
```

```json
{
  "model": {
    "provider": "openai-compatible",
    "name": "gpt-4o-mini",
    "base_url": "https://api.openai.com/v1",
    "api_key": "secret:AQUARIUS_OPENAI_KEY"
  },
  "ui": { "kind": "tui" },
  "memory": { "dir": "~/.aquarius/memory" },
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
    }
  },
  "permissions": {
    "allow": ["fs-read:~/**"],
    "ask": ["fs-read", "fs-write", "exec", "network", "secret"]
  },
  "limits": {
    "max_turns": 8,
    "max_context_tokens": 64000,
    "tool_output_chars": 20000,
    "tool_timeout_sec": 60
  }
}
```

配置三级覆盖：CLI flags > 环境变量（`AQUARIUS_*`）> `config.json`。

---

## 9. 权限与安全模型

| capability | 含义 | 默认策略 | 备注 |
|---|---|---|---|
| `fs-read` | 文件读取 | 家目录 allow，越界 ask | `file_*`、Doc 提取 |
| `fs-write` | 文件写入/删除 | ask（逐次 Confirm） | `file_write/delete`、`memory_write` |
| `exec` | 执行命令 | ask | `term_exec`、`job_start` |
| `network` | 网络访问 | 首次 ask 进 allowlist | MCP 插件声明 |
| `secret` | 读取密钥 | 按名 allowlist | 仅经 `Secrets` 注入，不回显进上下文 |

- 权限判定顺序：config allow → ask（`Confirmer`）→ deny。工具级 `Risk` 与 capability 取更严者。
- 输出安全：终端/任务输出视为**不可信数据**，只渲染不执行；工具结果统一截断（`tool_output_chars`）。
- 密钥永不进会话树、附件、日志；`Secrets` 返回值对 Prompt 侧不可见。

---

## 10. 错误、取消与并发语义

- **取消**：`ctx` 取消 ≡ `Stream.Close()`；工具执行同受 `ctx` 约束；Job 后台运行不受会话取消影响（`job_kill` 显式终止）。
- **取消提交**：已生成文本以 `Outcome: cancelled` 提交为节点（可 `/edit` 重试），不留半节点。
- **工具失败**：`Result{OK:false, Err}` 照常回填给模型（让模型自行决定重试或改道），不中断 Turn。
- **模型/网络错误**：装饰器层重试（幂等 Generate 的瞬时错误，限次 + 退避）；仍失败则 `Outcome: error` 提交并上抛 UI。
- **并发**：单会话内 Turn 串行（Head 唯一）；多会话并行安全（一会话一文件）；Job 自管并发。

---

## 11. 测试策略

| 层 | 手段 |
|---|---|
| domain | 性质测试：随机 Append/Revise(Fresh\|Carry)/Prune/Checkout 序列 → 不变量 1–3 恒成立；Carry 后后代节点字节级不变 |
| app | 脚本流 LLM + 收集器 Presenter + 脚本队列 Prompter → 交互回放（golden）；摄取管线用假 Transcriber |
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
| **M2 工具与记忆** | ToolRunner（确认/超时/裁剪）、memory_*、file_*、think、权限 allow/ask | 模型可经工具读写记忆；Confirm 能拦截 `memory_write`；家目录沙箱生效 |
| **M3 任务与多模态** | JobManager + job_* + term_exec、blobfs、Ingestor（文本/文件/剪贴板/麦克风）、ASR/TTS 适配器、输出器 | "语音提问 → 文本回答 → TTS 播报"链路端到端跑通；job 后台跑 + 日志可查 |
| **M4 MCP 与 TUI** | mcpgate + grant + `/plugin`、bubbletea TUI（多模态呈现）、装饰器链（重试/截断/审计） | 接入任一现成 MCP server 全链路可用；崩溃重启与授权拒绝行为符合 §6.4 |

---

## 13. 已决决策记录（ADR 摘要）

| # | 决策 | 否决的替代方案 |
|---|---|---|
| D1 | 节点不可变；用户"改"= 创建同级新节点 | 就地可变消息（v2 曾选）——放弃，换取崩溃安全与分支对比 |
| D2 | Revise `Carry` = **边转移** | 深拷贝子树（数据翻倍、身份分裂）；DAG 共享（复杂度不值） |
| D3 | 流式中间态不进领域，Turn 结束一次性 Commit | 节点随流变异（半节点/部分写入问题） |
| D4 | Tier-2 插件协议 = **MCP**（mcpgate 适配） | 自造 JSON-RPC（三方接入成本高、生态为零） |
| D5 | MCP sampling 反向调用 v1 拒绝 | —（有真实用例再议） |
| D6 | 文件工具默认沙箱 = 家目录，越界逐次 Confirm | 全路径裸放（误伤面大）；严格 chroot（对个人助手过重） |
| D7 | `Prune` 硬删，storejson 写前留一代 `.bak` | 软删除/墓碑（个人数据不留坟墓） |
| D8 | Job 表 v1 内存态，日志落盘 | SQLite 任务表（量级不足，暂缓） |
| D9 | UI 插件 v1 只留契约不开放装载 | 开放多 UI 并存（焦点/路由复杂度不值） |
| D10 | Doc Part 文本提取 v1 简单截断内联 | 完整 PDF/Office 提取器（列为 Ingestor 插件面扩展） |
| D11 | 记忆 = markdown 文档 + 关键词检索 | embedding/RAG、自动记忆巩固（个人规模不需要） |
| D12 | 工具调用引用放宽为"存在性引用" | 严格祖先引用（与 D2 边转移冲突） |
| D13 | 内置能力与三方插件同走端口契约（内置不享特权） | 内核直连内置能力（内核腐化） |
| D14 | 横切能力用装饰器，不进插件 API | 插件中间件链（API 面失控） |

## 14. 暂缓事项（Backlog）

- MCP sampling（server 借用宿主模型）
- 跨分支"摘抄"共享子树（DAG 化）
- Job 表持久化（SQLite）
- PDF/Office 等 Doc 提取器插件
- 图片 OCR/描述自动降级、音频直输模型（等模型能力普及）
- 自动摘要压缩上下文、长期记忆巩固
- GUI / Web UI、移动端输入方式
- 向量检索记忆后端（作为 MemoryStore 插件）
