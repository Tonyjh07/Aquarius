# Tier-1 Go 插件契约（pluginapi，v1）设计规格
<!-- docs-sync: 2026-11-26 @ 8efa632 -->

> 定位：Tier-1 进程内 Go 插件契约包（`pluginapi`，本代为 v1）的实现规格——接口面、UI 扩展面、宿主服务、
> 生命周期、版本纪律、能力边界。Tier-2（MCP server）的规格在 [contracts.md](contracts.md) §6.2–§6.4，
> 本文不重复也不改动 Tier-2 / Tier-3 的规格；三级模型的总形状同样见那一节。决策动机与否决见
> [decisions.md](decisions.md) 的 D327。

## 1. 定位与范围

本文定 Tier-1 进程内 Go 插件的完整契约：接口面、UI 扩展面、宿主服务、生命周期、版本纪律。
Tier-2（MCP server）已是交付成品，见 contracts §6.2–§6.4；本文不改动 Tier-2 的任何结论。

三句边界，先立住：

1. **Tier-1 装载是编译期的**：main 显式注册、随二进制分发（D29）。运行期动态装载（.so）
   与无 cgo、单二进制两条交付规则冲突，不做（D29 否决理由，本文重申）；要「装了就能用」
   的插件走 Tier-3（wasm 沙箱，见 [wasm-plugin-design.md](wasm-plugin-design.md)）。
2. **插件永远编译到 v1 契约包（`pluginapi`）上，不编译到内核类型上**：v1 类型与 `internal/port`
   完全独立，双向转换只在 `adapter/plugingo` 一处（硬性规则 4 的展开）。
3. **Tier-1 是同进程 Go 代码，没有沙箱**：安全边界是契约边界。插件技术上能做契约之外的
   事（自开网络、自读文件），契约对此的立场是「允许、不审计、不承诺」，见 §9。

## 2. 总形状：三块面，一个方向规则

```
pluginapi/              （独立 go.mod；目录名不带版本——Go 规则 /v1 后缀非法）
  manifest.go            清单与 API 版本
  plugin.go              生命周期（工厂 + 实例）
  host.go                Host：插件可用的内核服务
  value.go               稳定值类型层（防腐的公共词汇）
  llm.go                 供给面接口（按端口一个文件）
  tool.go
  command.go
  memory.go
  ingest.go
  modality.go
  secrets.go
  uiext.go               UI 扩展面（设置页、面板、通知）
```

注册表不在本包：清单由宿主侧的 main 显式列出（见 §7「注册表归宿主」）。

方向规则只有一条：接口里的每个类型都是 v1 自己定义的。v1 不 import `internal/port`，
`internal/port` 也不 import v1。

**落位事实**（P2 起生效）：独立模块在仓库 `pluginapi/` 目录，module path
`github.com/Tonyjh07/Aquarius/pluginapi`（包名 `pluginapi`），零依赖、只许标准库。
module path **不带** `/v1` 后缀——Go 的规则是 v1 隐式、v2 起才要后缀，`…/v1` 是非法
module path；「v1」由包文档与 `APIVersion` 常量承载，将来破坏性变更开 `pluginapi/v2`
独立模块并存。根 go.mod 以 `require` + `replace`（指向 `./pluginapi`）引入——同仓
多模块，不设 go.work；外部插件仓库按 tag 依赖同一路径。内核侧的依赖规则由 arch 测试守着（`layerPluginAPI`
只许自引用，adapter 与 plugin 两层允许 import）；v1 侧的独立性（不 import
`internal/…`、不引三方）由包内 `independence_test.go` 自查。落盘节奏：P2 落 go.mod、
manifest 与 value.go 三件；P3 第 1 步落接口面（plugin / host / llm / tool / command /
memory / ingest / modality / secrets / uiext），转换器与装载器随后。

## 3. 稳定值类型层（value.go）

v1 最重要的资产是一层语义等价、字段冻结的稳定词汇。现在 `internal/port` 里的值对象
（`Usage`、`Attachment`、`ModelInfo` 等）散在各文件，v1 按三个规则重述：

1. **按对外语义定义，不按内核内部语义**。例：v1 的 `Usage.InputTokens` 写成「总输入
   （含缓存两段）」——这是 D308 已统一的对外口径，内核以后内部怎么改不影响 v1。
2. **三类字段三种处理**：
   - 语义稳定且对外有意义的 → 进 v1（`Usage`、`ModelInfo`、`ConfirmRequest`、
     `SpeechSegment` 等）；
   - 内部实现细节 → 排除（ctx key、goroutine 协作细节、watch/inject 的内部面）；
   - 透传不解读的 → 定义成 `[]byte`，注释写明义务是「原样保真、逐轮回传」，不是理解
     （`ServerBlock.Raw`、`ReasoningSignature`）。
3. **每个类型注释带一条兼容承诺句**：改这个类型会破坏谁的二进制。

每个 v1 类型配一条防腐映射点列表，`plugingo` 转换器照单核对；port 侧改字段时按列表
逐点过。

### 3.1 防腐映射点列表（plugingo 转换器照单核对）

| port 侧 | v1 侧 | 语义口径与易错点 |
|---|---|---|
| `conversation.Usage` | `v1.Usage` | `InputTokens` = 总输入（含缓存读写两段，D308 对外口径）；`CostUSD` 不进 v1——成本折算归内核，模型插件只报 token |
| `port.ModelInfo` / `ModelCaps` | `v1.ModelInfo` / `v1.ModelCaps` | caps 逐位对齐（会什么，D173）；单价字段单位随内核口径（每百万 token） |
| `llm.Delta` | `v1.Delta` | 七项语义对齐（Text / ToolCall / Usage / Reasoning / ReasoningSignature / Title / ServerBlock）；`ReasoningSignature` 是透传字节，原样保真逐轮回传 |
| `llm.ServerBlock` | `v1.ServerBlock` | `Raw` 原样保真、逐轮回传——这是「透传不解读」义务的样板字段 |
| `tool.Spec` | `v1.ToolSpec` | `Schema`（json.RawMessage）按透传字节处理；内核侧 `Describe` 函数字段不进 v1，其对位是可选 `Describer` 接口（D320）。名字文法 `[A-Za-z0-9_-]{1,64}`：`plugingo` 在每轮装配时逐条校验，不合文法或同轮重名整份拒掉（模型端点会 400，早报好读） |
| `port.ToolSet` | `v1.ToolProvider`（`Tools(ctx)`） | 清单不物化进注册表：装配时向源要、执行时向源找。名字在**会话可见集合内**唯一（不同会话可同名），撞内置时以内置为准；工具源的端点不受 `tools.disabled` 约束——逐名禁用对逐轮变化的名字没有意义，开关是插件启停（D340） |
| `tool.Call` / `tool.Result` | `v1.ToolCall` / `v1.ToolResult` | 调用 ID 随转换重编；`Images` 经 BlobStore 引用、`Fallback` 语义照抄 |
| `port.ConfirmRequest` / `ConfirmAnswer` | `v1.ConfirmRequest` / `v1.ConfirmAnswer` | 无任务标识字段（contracts §5.2 并发语义）；`Risk` / `Level` 档位词表冻结，两边常量值必须一致；`perm.Op` 是 int 枚举（iota 顺序不是契约），v1 侧投影为字符串（`"read"` / `"write"` / 空） |
| `port.MemoryDoc` / `MemoryIndexEntry` / `MemoryHit` / `MemoryStore` | **不进 v1** | 文档方法集是 memoryfs 的形状，不是记忆的契约。插件面只留注入挂载点 `Memory.Recall`，值类型只有 `MemoryRequest` / `MemoryContext` / `LegacyMemorySnapshot`；存储、组织、检索、管理全归插件（见 §4 记忆所有权）。接管期间 `memory_*` 四工具与 `/memory` 退出，内置记忆转只读、只作迁移素材 |
| `port.WithSessionID` / `SessionIDFrom` | `v1.WithSession` / `v1.SessionFrom` | 同一个 ctx 值，换一层包名。会话身份不进任何 v1 值类型（§5「会话身份」）；空串 = 不注入；宿主每个插件入口都要 `WithSession` 一次，漏注入不报错、只让插件退回全局 |
| `port.SpeechSegment` | `v1.SpeechSegment` | PCM 原样字节，时间戳口径不变 |
| `port.RawInput` / `Attachment` / `IngestReport` / `OutputRequest` | `v1.RawInput` / `v1.Attachment` / `v1.IngestReport` / `v1.OutputRequest` | `conversation.BlobRef` 结构体（Hash/MIME/Name/Size）在 v1 原样重述，不是字符串别名；`Part` 按**正文投影**重述（Kind/Text/Ref/Transcript/Signature/服务端块原文）——`Tool` 分片不进 v1（对外播报与摄取只用正文，工具往返是内核协作面）；附件体积上限常量（`MaxAttachments` / `MaxImageBytes`）两边同步 |
| `port.Secrets`（`Get`） | `v1.SecretResolver`（Host 消费面） | 方向相反的两件分清：插件**供给**密钥走 `SecretProvider` 接口（P3），内核**解析给**插件走 `SecretResolver` |
| `port.TokenCounter` / `Prober` | `v1.TokenCounter` / `v1.Prober`（P3） | 可选接口，type assertion 探测；转换是纯值搬运，无易错点 |
| `port.GenerateRequest` / `PromptMessage` / `PromptPart` / `Sampling` / `TokenBudget` | `v1.GenerateRequest` / `v1.PromptMessage` / `v1.PromptPart` / `v1.Sampling` / `v1.TokenBudget` | 装配后的请求形状。`PromptPart.Data` 是已解析的图片字节（不是路径）；`PromptMessage.ServerBlocks` 与 `Delta` 的 `ServerBlock.Raw` 同一条透传义务；`Sampling` 的浮点参数用指针（nil = 不发送，0 是有意义取值）；`ServerTools` 非空时 `Tools` 必须为空——协议里两者互斥 |
| `port.CommandInfo` / `CommandParam` / `ParamKind` | `v1.CommandSpec` / `v1.CommandParam` / `v1.ParamKind` | 名字给主名 + 别名（首名为主名）；`ParamKind` 三档词表冻结（text / choice / flag）；`Values` 是 choice 的可选值，flag 带枚举值时也放这里 |
| `port.SpeechRequest` | `v1.SpeechRequest` | 字段一一对应（Text / Voice / Design / Style / Tags）；音色与风格是配置项，不进模型上下文、不进会话树 |
| `port.SpeechSegmenter` | `v1.SpeechSegmenter` | 逐帧喂入、按说话切段；`Flush` 收尾。返回的段即 `v1.SpeechSegment`，PCM 原样字节 |
| `port.ProbeResult` / `ProbeVerdict` | `v1.ProbeResult` / `v1.ProbeVerdict` | 三态判定（无结论 / 支持 / 不支持）；`Reason` 是给用户看的一句话，可能夹带服务端原文片段——那是数据不是文案 |
| `port.Log` | `v1.Logger` | 五段式日志的插件命名空间：级别由插件给，上下文段由宿主填成 `plugin:<name>`，事件 / 行动两句由插件自写（插件文案不进内核文案表），kv 是「名, 值」且名固定英文；插件不自开日志文件（D276 一处写出） |
| `port.AttachmentStore` | `v1.BlobStore` | 内容寻址三方法（Put / Get / Stat）。**GC 不进 v1**：keep 集来自会话树，插件交上来的 keep 会把别人引用的字节删掉 |

新增 v1 类型时先在表里补一行再写代码；port 类型新增字段时按本表逐点过（核对清单在
[development.md](development.md)）。

## 4. 供给面：九个必选接口 + 四个可选扩展（其中一个在 Host 侧）

### 必选面

| v1 接口 | 对应 port | 说明 |
|---|---|---|
| `LanguageModel` | `port.LLM` | 流式生成 + 模型清单。`Stream` / `Delta` 独立重述，七项语义（Text / ToolCall / Usage / Reasoning / ReasoningSignature / Title / ServerBlock）与 D42 / D175 / D309 / D310 对齐 |
| `ToolProvider` | `port.Tool` + `port.ToolSet` | 工具声明（`Tools(ctx)`，**每轮问一次**）+ `Execute(ctx, Call) Result`。确认、超时、裁剪仍由内核 ToolRunner 包装，插件只拿到裁决后的调用。清单可变、名字按会话解析（见下「工具面」） |
| `CommandProvider` | `app.CommandHandler` | 命令注册（含参数相）；`/help`、分派、补全归内核 |
| `Memory` | `port.MemoryStore`（**不对应，见下**） | 记忆所有权。插件只实现一个注入方法 `Recall`，存哪、怎么组织、怎么管理全归插件——内置的文档方法集不进契约 |
| `Ingestor` | `port.Ingestor` | 输入摄取；OCR、音频直输等降级链扩展点 |
| `OutputAdapter` | `port.OutputAdapter` | 输出镜像；首个实现是导出文件输出器 |
| `Transcriber` / `Synthesizer` | `port.Modality` | 语音两端口，含分段器形态 |
| `SecretProvider` | `port.Secrets` | keychain 类实现；方向相反——内核调插件，防腐层单独说明 |

### 可选扩展面（同包独立接口，宿主按 type assertion 探测）

- `TokenCounter`：精确计数，覆盖估算链（D26）。
- `Prober`：能力探测三态（D174）。
- `Describer`：PTC 二段式 describe 的对位（D320）。
- `LegacyMemoryHost`：内置记忆的一次性只读快照，只有记忆插件拿得到（见下）。

### 工具面：按轮给、按会话解析（D340）

工具声明不是「注册一次、长期有效」的条目。宿主在**每次装配请求时**调用 `Tools(ctx)`——包括
同一 Turn 内工具循环的每一轮——所以清单可以随插件状态变。这条能力是「标题即工具」形态的
地基：记忆点、目录节点各自是一个工具，调用它就是下钻一层，返回全文或子记忆。

内核侧的落法是 `port.ToolSet`（不是把清单塞进 `Runner.byName`）：

| 时机 | 动作 |
|---|---|
| 插件启用 | `Runner.AddSet(源)`：只挂一个源，不展开清单 |
| 每次装配请求 | `Specs(ctx)` = 静态工具 + 各源按 ctx 的会话要来的声明；同名胜出顺序：内置优先 |
| 每次执行 | 静态表未命中时按 ctx 向源查（`Lookup`），后查到的名字按「未知工具」回填 |
| 插件停用 | `Runner.RemoveSet(源)` |

为什么不能进全局表：**同一个名字在不同会话可以指向不同的东西**（会话 A 打开「部署」，会话 B
打开「英语」，两边的第一个工具都叫 `mem_1`）。全局 `byName` 装不下按会话解析的名字，而
`Execute` 本来就带 ctx，让查找走 ctx 是唯一不需要在名字里编码会话的做法。

三条约束写进契约：

1. 名字文法 `[A-Za-z0-9_-]{1,64}`，同会话内唯一；中文标题要转成 ASCII 名，标题全文进
   `Description`（模型选工具读的是 name + description）。
2. **同一轮内必须稳定**：模型这一轮看到的清单与本次执行的查找要一致。
3. 上一轮声明过、这一轮消失的名字不再是合法调用，宿主按未知工具回填。

**零工具不再拒载**：声明 `provides: tool` 但这一轮一件都没有是正常状态（空库、游标停在根、
刚装完还没迁移）。拿启用时的一次采样当拒载理由，会把首次启用的记忆插件误杀。

PTC 模式下模型面只出 `run_code`，工具源不参与说明——脚本里用不上按轮导航的清单。

### 命令面：并入动态命令表

命令不是「注册一次、长期有效」，也不是一张独立的表：Tier-1 插件的 `CommandProvider` 与
Tier-2 的 `/mcp:<server>:<prompt>` 落在**同一张会话动态命令表**里（`Session.SetDynamicCommands`），
`/help`、补全浮层与分派读的都是它。

这张表只有**整体替换**一个写入口，所以两个来源必须先合流再提交（`cmd/aquarius/plugincmd.go`）：

| 优先级 | 来源 | 何时重写 |
|---|---|---|
| 高 | 内置静态表 | 编译期定死；同名动态命令永远分派不到 |
| 中 | MCP prompts | 每次服务器清单刷新整体重算 |
| 低 | Tier-1 `CommandProvider` | 每次插件启停按「当前 Active 集合」整体重建 |

三条实现口径：

1. **撞名只丢那一条，不拒载插件**。命令名冲突不影响工具面，为一个名字把一个能用的插件整个
   关掉太重；丢的时候记一条 warn（`/plugin` 的日志能解释「为什么我的 `/mem` 没出现」）。
2. **每次调用重新取实例**。命令表在启用时建好，而插件可能在两次调用之间被停用又启用（拿到
   新实例）；取到后用 `Guard` 记在途，停用流程会等这次调用跑完再收尾。
3. **会话从 ctx 取**。`execCommand` 在分派前把当前会话放进 ctx（已有会话的不覆盖），插件
   与工具面看到的是同一个来源。

### `Memory`：插件完全拥有记忆

内置记忆在端口侧是五个方法（索引 / 读 / 写 / 删 / 关键词检索），那是 **memoryfs 的形状**，
不是「记忆」的契约。把它原样端给插件会逼出两条烂路：分层 + 向量 + 图的实现要假装成文档
（丢掉相似度、分层、预算裁剪），或者在工具里另开一套（于是 `MemoryStore` 那两篇文档与
插件自己的库成为两套并存的记忆，模型看到两个自己）。因此 v1 只保留**一个挂载点**：

```go
type Memory interface {
    Recall(ctx context.Context, req MemoryRequest) (MemoryContext, error)
}
```

1. **存哪**归插件：`DataDir()` 落盘、`Secrets()` 取远端凭据、或接外部服务，内核不问。
2. **怎么组织**归插件：文档、图层、向量、时序图都行；`Recall` 返回的就是本轮注入的
   markdown 原文，宿主原样放进去，不解读、不重排、不加标题。
3. **怎么管理**归插件：写入工具、`/memory` 类命令、面板全走 `ToolProvider` /
   `CommandProvider` / `ViewProvider`，不再有内核提供的 `memory_write`。
4. **独占**：`provides: memory` 至多一个插件启用；两个同时启用由宿主拒绝并报错，
   不给「先声明的赢」这种隐式裁决。
5. **内核退出项**：接管期间 `memory_list` / `memory_read` / `memory_search` /
   `memory_write` 四件工具与 `/memory` 命令退出，MCP resources 不再投影记忆；内置记忆
   转只读，不再被模型或用户改写。
6. **预算**：宿主给 `BudgetTokens` 软预算并按它硬截断，正常情况由插件自己裁——截断点由
   宿主挑，截不出一条完整的记忆。软预算 = 窗口上限减已装配部分与输出预留、再以窗口的
   四分之一封顶（记忆是召回材料，不该把本轮对话挤出去）；截断按 token 估算取最长前缀，
   截过之后在 `/context` 的记忆段细目里标 `truncated`——用户看得见记忆被砍，比翻日志快。
7. **迁移**：`LegacyMemoryHost` 给一次性快照（全局 + 每个会话各一份）。搬不搬、搬之前
   要不要 `Confirm` 由插件决定，内核不自动搬。这是用户接受的那笔成本：迁移一次，之后
   内置记忆只读。
8. **不承诺的**：轮末 / 会话结束的巩固钩子不进 v1——那要求内核往插件手里递会话内容，
   是新的数据出境面与生命周期锚点，得先设计再给。热路径写入走插件自己的工具；离线巩固
   只能挂在 `Init`（扫库）上。
9. **会话身份**经 ctx 给（`SessionFrom`），不进任何值类型——理由见 §5「会话身份」。

#### 内核侧怎么接

Agent 只认一个入口 `port.MemorySource`（`Recall(ctx, 会话 ID, 预算)`），因此「谁回答」可以
在运行期切换：

- **内置回答者** `memoryfs.Source`：两篇白名单（全局 + 当前会话）拼成一块，排版属于内置实现
  ——插件接管后换掉的就是它，内核不背这套形状。
- **路由器**（装配根的 `memoryRoute`）：`owner == ""` 时问内置，否则问接管插件的
  `plugingo.NewMemory`。`/context` 里这一段因此显示 `plugin:<名字>`。
- **切换点**只有两个：`/plugin enable|disable` 与启动期按 config 恢复。

启停的先后是这段实现里最容易写错的地方：

1. **启用**：先撤内置面（四工具并进 Runner 的禁用并集、`/memory` 进 Session 的隐藏集），
   再启用插件，最后挂工具源并接管注入。撤必须早于启用——内置工具还在，插件沿用
   `memory_*` 的名字就会被内置顶掉（Runner 合并时内置优先），而模型根本看不到插件那份。
2. **停用**：先摘工具源、停实例（Draining 等在途调用跑完），再交还内置面。反过来做，
   内置记忆会在插件还活着的窗口里与它同时存在。
3. **失败还原**：启用失败必须把内置面放回去，并保住已有接管者——一次被拒的启用不该让
   别人的接管或用户的记忆消失。

其余两条接法：MCP resources 在接管期间不再投影（插件是唯一所有者）；迁移素材只给声明了
`provides: memory` 的插件，判据取清单快照而不是反查注册表——造 Host 时注册表正锁在启用
流程里，回调再查它就死锁。

### 明确不进 v1 的（写死，防含糊）

`ConversationStore` 写面、树 / Revise / Carry、`Job` / `Delegate` 协作面、`watch` / `inject`、
`Presenter` / `Prompter` 装载（D143 收窄后维持，见 §6）、设置窗整体结构。这些是内核协作面，
不是插件供给面；插件要影响这些协作面，只能经 Tool / Command 的合法路径。

## 5. Host：消费面

Host 是插件可用的全部内核服务，单一结构分发，不裸暴露内核类型。首发八项：

```go
type Host interface {
    Logger() Logger            // 五段式日志的插件命名空间（D276 同一写出点）
    Secrets() SecretResolver   // 按名解析 secret:<name>，明文不落插件
    Blobs() BlobStore          // 内容寻址附件读写，与内核共享去重与 GC 语义
    DataDir() string           // 插件私有持久目录（随 profile，跨重启存活）
    WorkDir() string           // 插件私有临时目录（随 profile，关机清理）
    Locale() string            // 内核当前界面语言 "zh" / "en"（与 Manifest.Locales 同词表）
    Confirm(ctx, req ConfirmRequest) (ConfirmAnswer, error)  // 走内核确认链
    Notify(level, text string) // 用户通知出口
}
```

每项的设计理由：

1. **Logger**：插件自开日志文件就破了「运行日志一处写出」（D276）。
2. **Secrets**：只给解析、不给存储；插件各存一份密钥是密钥安全规则（硬性规则 8）的反面。
3. **Blobs**：裸文件路径绕开内容寻址去重与 GC；面板展示附件内容也绕不开 BlobStore。
4. **DataDir**：要活过重启的东西（库、索引、游标、迁移快照）得有落盘处——记忆插件的第一
   条需求就在这。内核只给位置：不清理、不读内容、不做备份，布局与迁移归插件。`Blobs`
   顶不上这个位：它是内容寻址的，没有元数据写面，写不了索引和游标（D341）。
5. **WorkDir**：临时产物的落盘位置（解包目录、中间文件）。与 DataDir 分两个方法而不是加
   个参数，是因为「这个目录会不会被清」不该让插件去猜。
6. **Locale**：`Manifest.Locales` 声称「宿主按此选插件文案」，那就得有通道告诉插件现在是
   哪种语言，否则那条承诺不成立。语言整次运行内不变（D275），所以给值不给访问器；空串
   = 宿主没给，插件退回自己的缺省。插件文案本身仍不进内核 i18n 表（§6 纪律 2）。
7. **Confirm**：插件不得自造确认 UI；权限矩阵、审计、无人值守语义（D285）才能罩住插件。
   带 ctx 而不是无参：取消时确认链随轮次一并收工，插件不自己拼超时。
8. **Notify**：见 §6 的通知路径。

**两个目录落在哪**（装配根的唯一口径）：`DataDir` = `<profile>/plugin-data/<name>`，
`WorkDir` = `<profile>/plugin-tmp/<name>`。临时目录在**每次新建实例前**清空——关机时清靠
退出钩子，而崩溃退出没有钩子，启动时清是唯一不依赖退出路径的做法。不放在 Tier-2 的
`plugins/<name>/` 下：那个目录装的是用户丢进来的声明，删声明是「不用这个 server 了」，
数据是插件自己的库，混在一处会让人以为删声明就该连数据一起删。目录由内核独占，插件不受限。
两个目录都建不出来时给空串（插件按契约降级成不留存），不因此拒载。名字要拼进路径，所以
清单校验除了工具/命令名的限制，还拒绝带路径分隔符与 `.` / `..` 的名字。

Host 的方法集是 v1 里最容易过度承诺的地方：每个方法都要过一遍「内核愿意为这个方法背多久的
兼容债」。首发宁可少给，靠只增不改补。

**可选扩展按 type assertion 探测，不扩 Host 本体**：插件对 `Host` 句柄做
`h.(pluginapi.NoticePoster)` 断言，命中才用——首发两项：「内联提示」（把轻量提示放进消息流，
未命中时退回 `Notify` 的语义）与 `LegacyMemoryHost`（内置记忆快照，只有记忆插件用得上，
见 §4）。这条同时是将来加 Host 能力的唯一方式：扩接口就破坏实现方，加独立接口只破坏
「想用这一项的那一个插件」。

### 会话身份：经 ctx，不进值类型

需要会话身份的面至少有四个：工具执行、命令执行、面板渲染与动作、记忆注入。每个面各带一个
`Session` 字段就是四个来源，迟早在某个入口漏填或填错（2026-11-24 拍板，D338）。

- 身份只走 ctx：宿主在**每个**插件入口用 `WithSession(ctx, id)` 注入，插件在入口用
  `SessionFrom(ctx)` 读取。空串等于不注入；`ok=false` 表示这次调用没有会话上下文
  （会话列表预览、启动探针、非会话触发的命令）。
- 值类型保持干净：`ToolCall` / `ViewAction` / `MemoryRequest` 都不带会话字段。
- 这不是安全边界（Tier-1 无进程隔离），是数据形状纪律；键类型不导出，两边不会撞键。
- 宿主侧的义务是硬的：漏注入不会报错，只会让插件悄悄退回全局。所以 plugingo 的每个入口
  适配函数都要经过 `WithSession`，并在其测试里逐个断言——「漏一个入口」是这一条唯一
  真实的失败方式。

## 6. UI 扩展面（uiext.go）

### 总原则：插件交声明，不交像素

v1 不出现任何 Gio 类型、不出现回调式绘制。插件产出一棵语义组件树，`uigui` 用 Gio 渲染、
`uitui` 用降级文本渲染。同一份数据、两个消费者，与设置窗现状（port/settings.go 的 DTO
形态）同构。这条同时守住：平台抽象不破、TUI 不是二等公民、将来换 UI 技术栈插件不动。

### 三个贡献点

**1. 设置页（`SettingsSectionProvider`）**。插件声明一组设置行（标题、说明、字段：
文本 / 开关 / 下拉 / 数字），设置窗在「插件」区渲染，保存时插件收到自己那段 patch。
语义规则全在插件（哪个值落哪个 config 键、留空什么意思），内核只管表单渲染与校验框。

**2. 面板（`ViewProvider`）**。插件注册具名视图（ID + 标题），用户从命令或菜单打开为
次窗。内容是组件树，首发词汇六种：

```go
type Node struct {
    Kind     NodeKind    // text / markdown / table / kv / list / form / buttons
    Text     string      // 主内容；markdown 走内核渲染管线，表格复用 D325
    Table    *TableSpec
    Items    []string
    Fields   []FormField // 单行 / 多行 / 开关 / 下拉，各带 label 与当前值
    Actions  []Action    // ID + 标签；点击后回传插件
    Children []Node
}
```

交互往返走**拉模式**，插件没有推送入口：内核打开视图时调 `ViewProvider.Render(ctx, viewID)`
取当前帧；用户点按钮或提交表单时调 `ViewProvider.OnAction(ctx, ViewAction)`，插件按自己的状态
算出新树并返回，内核拿到就重渲染。**状态权威始终在插件侧，内核只持当前帧**。选的不是推模式
（`Host.ShowView(spec)`）——推模式要求宿主接受任意时刻的界面变更，等于为每个插件背一条跨线程
UI 契约，而回放、切会话、重启都会留下要同步的 UI 状态。

**3. 通知与内联提示**。`Host.Notify` 兜底；`NoticePoster` 作为可选接口提供消息流内的
轻量内联提示，TUI 降级为状态行。暂缓清单「MCP 富内容回传」的图片落地将来走这条路。

### 两条配套纪律

1. **降级有定义**：词汇表里每种 Node 在 TUI 的渲染行为写死（table → 对齐文本，
   form → 逐行问答，buttons → 数字选择）。词汇表扩一项，两份渲染器各配一段。
2. **文案归插件**：插件 UI 文案不进内核 i18n 表，插件自带 zh / en（Manifest 里声明）；
   内核只翻译自己渲染的壳（窗口标题模板、按钮语义字）。

### 刻意不做

1. **消息流渲染 API**：不给「插件渲染消息」的接口，避免插件获得任意注入对话流的口子。
   工具结果的展示提示已经在 `tool.Result` 里带语义，够用。
2. **样式 / 主题 API**：内核还没有多主题，先不承诺。
3. **状态栏挂件位**：等面板词汇表稳定后再评估。

### D143 的修订口径

原判「v1 不开放 UI」收窄为两句：**替换 Presenter / Prompter 的装载面继续关**（插件是
内容贡献者，不是界面宿主）；**声明式贡献面随 v1 开放**（设置页 + 面板 + 通知）。
否决：任意绘制（绑死 Gio、破平台抽象）、消息流任意注入（安全面失控）。

## 7. 生命周期与运行时启停

### 状态机（每个插件实例）

```
Registered ──> Validated ──> Stopped ──Init──> Active ──禁用──> Draining ──> Stopped
 (编译进二进制)  (启动校验)   (enabled=false      │  ▲            (等在途调用)
                            或禁用态)            │  └────启用────┘
                                                 │
                                       Init 失败: 拒载（状态 + 报因进 /plugin，
                                       不半装载；重新启用 = 重新 Init）
```

1. **Registered**：编译期注册（main 里 import + `Register`）。只是候选，不占运行时资源。
2. **Validated**：宿主启动时逐项查 Manifest——api 版本断言（v1 宿主只认 `api: 1`）、
   名字冲突（与内置工具、MCP 端点撞名即拒）、名字能否当目录名（私有目录按名字拼）、
   provides 词表。校验失败停在 Stopped 并记因，
   不影响其他插件（软启动规则照抄 Tier-2）。
   **provides 自检（声明 LLM 必须实现 `LanguageModel`）推迟到 Create 之后、Start 之前**：
   接口实现与否只有在拿到实例后才验得了，而为一个禁用状态的插件造实例、跑一遍再丢掉，
   等于让「禁用」也付出初始化副作用。落点变了、结论没变——声明与实现不符的插件照样
   进不了 Active，只是报因发生在启用那一刻（`plugin/tierone` 的 `checkProvides`）。
3. **Active**：`Init` 成功后可被调用；工具源挂进注册表（`AddSet`，清单仍按轮给）、模型进
   降级链、命令进 `/help`。
4. **Draining**：禁用的第一阶段。进入瞬间做三件原子事：
   - 工具注册表摘除该插件全部端点；
   - 降级链快照按轮生效——进行中的一轮用旧链跑完（半轮换模型 = 同轮两家回答拼在一起，
     语义不成立）；
   - 该插件打开的面板次窗关闭，显示「插件已停用」。
   然后在途调用跑完（内核限时，超时按失败结果补齐）。
5. **Stopped**：`Shutdown(ctx)` 返回后的终态。契约义务：Shutdown 返回后插件不得再有
   任何副作用（goroutine 收净、文件句柄落定）；Host 句柄由宿主置失效，再调直接报错。

### 接口形态：工厂，不是单例

```go
type Factory interface {
    Manifest() Manifest
    Create(h Host) (Instance, error)   // 每次启用造新实例
}
type Instance interface {
    Start() error
    Shutdown(ctx context.Context) error
    // 各供给面方法
}
```

重新启用永远是干净的新实例，插件不用满足「Init 可重入」；代价是插件作者要理解全局
状态随实例丢弃，这正是启停的用户预期。

### 注册表归宿主

编译期注册由 main 显式列出工厂（一个 `[]pluginapi.Factory` 交给宿主装配），v1 包里不放
全局 `Register`：全局注册表会把 init 顺序与并发写进要冻结的契约面，而名字冲突校验、状态机
与启停本来就都在宿主（本节），v1 里再存一份等于两处权威。

### 插件怎么进到二进制里（构建期注入）

Tier-1 是编译期链接（Go 没有可用的运行期代码加载），所以「装一个插件」在事实层面就是
「拿一份含它的工厂列表重新构建」。三方各有一条边界：

| 谁 | 要什么 | 不该被迫做什么 |
|---|---|---|
| 上游（宿主仓库） | 发布产物里没有任何第三方插件 | 收留插件的代码、模块路径、名字 |
| 插件作者 | 在自己仓库里改代码、跑测试、出二进制 | fork 上游、往上游提 PR 才能试跑 |
| 构建者 | 一个带某个插件的可执行文件 | 手工编辑上游的任何文件 |

上游只留一个中性接入口（`cmd/aquarius/pluginext.go`）：

```go
//go:build !tier1sample
func externalFactories() []v1.Factory { return nil }
```

注入靠两条只影响本次编译的 flag，由构建器（`cmd/aqbuild`，协议归上游、参数化到不认识
任何具体插件；见 plan P3 第 4 步）执行：

1. `-overlay=overlay.json`：把上面这个文件在编译单元里换成插件带来的一份实现（必须
   `package main`、带 `tier1sample` 约束、定义同名函数）。被换掉的原文件不参与编译，
   两份实现不会同时在场；换上去的那份因为带 tag，在插件仓库自己的 `go build ./...` 里
   也不参与（否则「`package main` 没有 `func main`」会挡住插件作者跑测试）。
2. `-modfile=<插件侧>.mod`：让依赖解析读插件仓库里那份 go.mod 副本（宿主原 go.mod 加
   `require` + `replace` 插件模块）。没有它，注入进来的一段代码一旦 import 插件自己的包
   就找不到模块。

三条实测事实（2026-11-26 用探针插件验证，见 D343）：`-modfile` 指向仓库目录之外合法；
要带 `-mod=mod`，构建过程才能把 `require` 规整进那份副本（不加会报「updates to go.mod
needed」）；副本、go.sum、overlay.json 与可执行文件全部落在插件仓库或缓存目录，上游
`git status` 一个字节都不变。

三个被否的做法：

1. **`replace` 写进上游 go.mod**：本地立刻生效，但它进了版本库——发布产物与下游构建都
   被绑上插件，正好是「远端看不到示例插件」的反面。
2. **`go.work`**：干净，但作用域是目录，每个新 clone 与 CI 都得自己 `go work init`，
   忘了就报「找不着模块」，把一次性配置成本摊给所有人。
3. **`-buildmode=plugin`**：Windows 不支持；Linux 上要求宿主与插件的 Go 版本、依赖版本、
   构建标志完全一致，拿它做产品级插件系统是在造事故。

兼容性由编译期天然兜住：注入的代码拿到的 `Host` / `Instance` 就是宿主这份 pluginapi 的
类型；插件若 require 了另一份 pluginapi，`Create(Host)` 签名对不上，编译直接失败，不会
等到运行期才发现两份契约不是一回事。构建器因此有一条硬要求：**必须把 pluginapi 指到宿主
自己的那份**，而不是让 MVS 去挑一个版本。

#### 构建器参数（`cmd/aqbuild`）

参数化到不认识任何具体插件：只认路径、包名与 tag，不认插件名。

| 参数 | 缺省 | 作用 |
|---|---|---|
| `-plugin` | 必填 | 插件仓库根（含 go.mod） |
| `-hook` | `pluginext.go` | 注入用的实现文件（相对 `-plugin`）：`package main`、带 `-tag` 那个约束、定义与 `-target` 同名的函数 |
| `-host` | `.` | 宿主仓库根 |
| `-target` | `cmd/aquarius/pluginext.go` | 被替换的中性接入口（相对 `-host`）；文件不存在即报错，不猜 |
| `-pkg` | `./cmd/aquarius` | 要构建的包（相对 `-host`） |
| `-tag` | `tier1sample` | 互斥开关；与 `-target` 那份文件里的约束成对 |
| `-o` | `<plugin>/<宿主模块名末段>-with-plugin[.exe]` | 产物路径；允许落在宿主之外 |
| `-work` | 缓存目录下 `aquarius/aqbuild/<宿主>-<插件>-<hash>` | modfile、go.sum 副本、overlay.json 的落点 |
| `-v` | 关 | 打每条 `go` 命令原文（排障用） |

执行顺序固定，四步各自可单测：

1. **建工作区**：复制宿主 `go.mod` 为 `<work>/build.mod`、`go.sum` 为 `<work>/build.sum`
   （有才复制）。副本放在工作区而不是宿主目录，宿主不产生任何新文件。
2. **改依赖视图**：`go mod edit -require=<插件模块>@v0.0.0 -replace=<插件模块>=<插件目录>`；
   同时把宿主原有的**相对路径 replace** 改写成绝对路径——modfile 移到宿主之外以后，
   `./pluginapi` 会按工作区解析，不改写就找不到模块。版本形式的 replace 原样保留。
3. **写 overlay**：`{"Replace": {"<host>/<target>": "<plugin>/<hook>"}}`；两边都必须是
   绝对路径（overlay 不认相对路径）。
4. **构建**：`go build -modfile=<work>/build.mod -mod=mod -overlay=<work>/overlay.json
   -tags=<tag> -o <out> <pkg>`，工作目录是宿主根，子进程 `GOFLAGS` 清空——用户环境里的
   `-mod=readonly` 之类会让这一步失败。任何一步失败即停，报出是哪一步、哪条命令。

`-mod=mod` 不是可选项：没有它，`require` 规整不进副本，构建直接报「updates to go.mod
needed」。产物与工作区都在宿主之外，用完宿主 `git status` 不变。

### 操作面

1. `/plugin enable|disable <name>` 与设置窗「插件」页开关，写同一个 config
   `plugins.<name>.enabled`（启停状态存 config，D31 语义复用），改动立即生效、不重启。
2. Tier-2 的启停沿用现有进程级 enable/disable；用户操作一致，内部机制各自。`/plugin list`
   把两边合成一张表，Tier-1 显示的来源是 `binary`、传输是 `in-process`，另列供给面。
3. 唯一需要重启的：换二进制本身（新增或删除编译进去的插件）。
4. **启动恢复**：启动期按 config 逐个启用（键缺失 = 启用，同 Tier-2 语义），时机在工具源与
   会话都就绪之后——启用即挂工具源，挂不上的启用等于「有插件没有工具面」。启动期启用失败
   只记一行、继续下一个，且**不写盘**：一次失败的启动不该把用户的启用意图改成停用。
5. `/plugin logs <Tier-1 名>` 暂不按插件过滤：调用记在审计日志的工具名下，而工具名由插件
   自定，反查不到所属插件。给一句说明，不猜一份看起来像日志的数据。

### 装配顺序（记忆接管专用，D340）

接管记忆的插件若沿用内置工具名（`memory_read` / `memory_search` / …），装配顺序必须是
**先撤内置四工具 → 再启用插件 → 再挂插件工具源 → 接管注入面**。

- 撤必须排在最前：内置工具还在时挂源，Runner 合并按「内置优先」，插件那份根本进不了
  模型面，而且不会有任何报错。
- 挂源只能排在启用之后：工具源要从**已 Active 的实例**上取清单，没有实例就没有源。
- 启停之间不留空窗：这一串在同一个调用里同步走完，中间不跑 Turn。

腾出名字是有意为之：模型侧的提示词、习惯与既有对话里对 `memory_search` 的引用都不用改，
迁移成本几乎为零。停用按相反顺序走：摘源 → 停实例 → 交还内置面。

### 契约义务（插件作者侧）

1. `Start` 要快（同步调用）；重活异步做或首次调用时懒做。
2. 任何时刻可能收到 `Shutdown`，包括调用进行中——供给面方法都要认 ctx。
3. `Shutdown` 返回即死透，不接「复活后补一笔」。
4. Host 句柄不缓存跨实例使用。

### 不设「暂停」档

Draining → Stopped 是唯一出路。Go 同进程里「挂起」没有可执行语义（goroutine 收不收、
状态冻在哪都没有答案），挂起态只会催生「禁用后还有反应」的幽灵问题。临时静默 =
禁用再启用。

## 8. 版本纪律（只增不改的执行细则）

1. 独立 go.mod；内核对 pluginapi 的依赖是唯一入口，插件作者 `go get` 这一个包。
2. 不改已有字段的语义与类型；新字段只加在结构体末尾，且必须注释「零值语义」。
3. 新方法只加在新的独立接口上（断言探测），不扩已发布接口。
4. 接口方法签名冻结；要改就开 v2 包并存，宿主按 major 各自转换，v1 插件不必动。
5. 每个类型注释带兼容承诺句（见 §3 规则 3）。

## 9. 安全与授权语义

1. **capabilities 降为展示语义**：Tier-1 没有进程隔离，`capabilities` 的作用收敛为
   对外展示与审计归组（`/plugin` 可见、审计日志按名归组），不走授权弹窗。信任由「编译
   进来」承载。这条写死，防止把 Tier-2 信任门错套到 Tier-1。
2. **密钥例外**：`Secrets()` 按名解析本身是最小授权面。
3. **契约外行为**：插件自开网络、自读写文件系统——允许、不审计、不承诺。规格明写，
   不假装拦得住。
4. **确认链强制**：需要用户确认的动作必须走 `Host.Confirm`，插件自造 UI 不算数。

## 10. 能力边界清单

**能做**：接新厂商模型、精确计数、能力探测；任何工具形状的新工具与新命令；接管记忆
（换存储、换组织、换检索，见 §4）；新输入摄取与新输出镜像；接新 ASR / TTS 引擎（外置服务
形状的唤醒词、声纹、说话人分离）；接 keychain；贡献设置页、面板、通知。

**契约明确排除**：动会话树（拿不到写面，看不到对话内容）；注入模型上下文（唯一注入点是
记忆段的 `Memory.Recall`，没有 system prompt / 消息流注入接口）；替换或宿主 UI（不暴露 Gio、
不改主题、不塞消息流）。

**技术上可行、契约不承诺（自行负责）**：自开网络与文件读写（不走审计、不走 Host 日志、
密钥自理）；后台 goroutine 长任务（生命周期只有 Init / Shutdown 两个锚点，无 Job 台账
语义，半截状态自理）；插件间直接调用（无寻址，除非经内核工具注册表绕行）。

**v1 先不做、登记重启条件**：面板词汇表扩展（每项要 GUI / TUI 两个渲染器跟上）；
状态栏挂件位；消息流装饰 API（等「MCP 富内容回传」排期交付时一并设计）；Tier-1 的运行期
装载（不做；运行期装载由 Tier-3 承担，但那条路只给工具面，见
[wasm-plugin-design.md](wasm-plugin-design.md)）。

## 11. 防腐层（plugingo）

1. 位置 `internal/adapter/plugingo`：v1 ⇄ port 双向转换器 + 宿主侧 Host 实现（把内核的
   logger、secretbin、blobfs 包成 v1 Host）。
2. 转换器逐字段手写；不反射、不 JSON round-trip（前者隐性耦合字段名，后者丢类型与
   性能）。每个转换函数配一个黄金往返测试。
3. port 侧重构时的核对清单（进 development.md）：改了某个 port 类型字段 → 按映射点
   列表逐点过 → 有语义变化就补决策记录。

落盘形状（P3 第 2 步）：

| 文件 | 内容 |
|---|---|
| `convert.go` | 风险/操作档的词表投影、会话身份换键、附件引用 |
| `convert_llm.go` | 请求出去（含「服务端工具与本地 tools 互斥」的守门）、增量/用量/模型清单回来 |
| `convert_tool.go` | 调用进去、结果/确认应答回来、确认请求出去 |
| `convert_io.go` | 原始输入、摄取结果、正文分片、输出请求（工具分片不进 v1） |
| `convert_speech.go` `convert_command.go` | 语音段/合成请求、命令声明 |
| `host.go` | v1 Host 八项转发；可选扩展按构造时是否给实现决定断言命中 |
| `providers.go` | 九个供给面的插件实现 → port 接口（`NewLLM` / `Tools` / `NewIngestor` / …） |

两条实现约定：**每个入口都单独调一次 `toV1Session`**（不做「顺手带上」的公共包装，漏了
就是漏了）；**插件交回的错误不分类**——v1 没有错误词表，内核按不可重试处理，要表达瞬时
错误得先在 v1 加哨兵。

## 12. 验收（P3，首个插件）

首个 Tier-1 插件定为**间隔重复记忆插件**（独立仓库 `aq-memory-plugin`，设计稿见其
`design.md`：`Memory` 注入 + **标题即工具**（每个记忆点一个工具，调用即下钻并计数）+
分层面板 + 首次启用时迁移内置记忆）。选型标准
不随具体插件变：一条路径同时覆盖四层——供给面实现 + `Init` 里解析 `secret:` 与迁移快照 +
落盘或附件经消费面（DataDir / WorkDir / Blobs）+ `/plugin` 与设置窗可见（管理面）+ Shutdown 收尾
（生命周期）。记忆插件额外验两条契约义务：接管期间 `memory_*` 与 `/memory` 确实退出、
内置记忆确实转只读；两个 `provides: memory` 同时启用确实被拒。
选中的插件同时是契约的验收测试与文档样例；导出文件输出器（`OutputAdapter`）留作
后续样例候选。

验收的形态是「在插件仓库跑构建器，得到一个带该插件的二进制」——不是一条 `go test`。
主仓的默认构建、全量测试与发布产物都不含它（`pluginext_test.go` 守着默认构建的工厂列表
为空），这也正是构建期注入（§7）要保证的事。

## 13. 决策索引

本文规格的动机与否决立为 [decisions.md](decisions.md) 的 D327，要点：

1. v1 类型独立重述，不暴露 `internal/port` 类型。
2. Host 首发八项（Logger / Secrets / Blobs / DataDir / WorkDir / Locale / Confirm / Notify，
   其中 DataDir 与 Locale 由 D341 补入）。
3. UI 扩展面随 v1 开放；D143 收窄为「装载关、贡献开」。
4. 面板词汇首发六种 Node，交互走拉模式（插件不推送界面），降级行为写死。
5. 插件文案自带 zh / en，不进内核 i18n 表。
6. 生命周期工厂化；运行时启停走状态机，无暂停档。
7. capabilities 降为展示语义；确认链强制走 Host。
8. Tier-1 的运行期动态装载、消息流渲染 API、样式主题 API：不做（各有否决理由）；外部插件
   经构建期注入进入二进制（§7，D343）。运行期装载单独立为 Tier-3（wasm 沙箱、只有工具面）：
   [wasm-plugin-design.md](wasm-plugin-design.md)，D344。
9. 记忆由插件完全拥有：契约只留 `Memory.Recall` 注入挂载点，`provides: memory` 独占，
   迁移走 `LegacyMemoryHost` 一次性快照（D337）。
