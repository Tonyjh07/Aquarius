# pluginapi/v1 设计草稿（Tier-1 Go 插件）
<!-- docs-sync: 2026-11-22 @ 7f109c1 -->

> 定位：Tier-1 进程内 Go 插件（`pluginapi/v1`）的实现规格——接口面、UI 扩展面、宿主服务、
> 生命周期、版本纪律、能力边界。Tier-2（MCP server）的规格在 [contracts.md](contracts.md) §6.2–§6.4，
> 本文不重复也不改动 Tier-2 的规格；两级模型的总形状同样见那一节。决策动机与否决见
> [decisions.md](decisions.md) 的 D327。

## 1. 定位与范围

本文定 Tier-1 进程内 Go 插件的完整契约：接口面、UI 扩展面、宿主服务、生命周期、版本纪律。
Tier-2（MCP server）已是交付成品，见 contracts §6.2–§6.4；本文不改动 Tier-2 的任何结论。

三句边界，先立住：

1. **Tier-1 装载是编译期的**：main 显式注册、随二进制分发（D29）。运行期动态装载（.so）
   与无 cgo、单二进制两条交付规则冲突，不做（D29 否决理由，本文重申）。
2. **插件永远编译到 `pluginapi/v1` 上，不编译到内核类型上**：v1 类型与 `internal/port`
   完全独立，双向转换只在 `adapter/plugingo` 一处（硬性规则 4 的展开）。
3. **Tier-1 是同进程 Go 代码，没有沙箱**：安全边界是契约边界。插件技术上能做契约之外的
   事（自开网络、自读文件），契约对此的立场是「允许、不审计、不承诺」，见 §9。

## 2. 总形状：三块面，一个方向规则

```
pluginapi/v1/            （独立 go.mod，module path 带 /v1）
  manifest.go            清单与 API 版本
  plugin.go              生命周期（工厂 + 实例）与注册表
  host.go                Host：插件可用的内核服务
  value.go               稳定值类型层（防腐的公共词汇）
  llm.go                 供给面接口（按端口一个文件）
  tool.go
  memory.go
  ingest.go
  modality.go
  secrets.go
  uiext.go               UI 扩展面（设置页、面板、通知）
```

方向规则只有一条：接口里的每个类型都是 v1 自己定义的。v1 不 import `internal/port`，
`internal/port` 也不 import v1。

**落位事实**（P2 起生效）：独立模块在 `pluginapi/v1/`，module path
`github.com/Tonyjh07/Aquarius/pluginapi/v1`（包名 `v1`），零依赖、只许标准库。根 go.mod
以 `require` + `replace`（指向 `./pluginapi/v1`）引入——同仓多模块，不设 go.work；
外部插件仓库按 tag 依赖同一路径。内核侧的依赖规则由 arch 测试守着（`layerPluginAPI`
只许自引用，adapter 与 plugin 两层允许 import）；v1 侧的独立性（不 import
`internal/…`、不引三方）由包内 `independence_test.go` 自查。本版（P2）只落 go.mod、
manifest 与 value.go 三件；接口面（plugin / host / llm / tool / memory / ingest /
modality / secrets / uiext）随 P3 落盘。

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
| `tool.Spec` | `v1.ToolSpec` | `Schema`（json.RawMessage）按透传字节处理；内核侧 `Describe` 函数字段不进 v1，其对位是可选 `Describer` 接口（D320） |
| `tool.Call` / `tool.Result` | `v1.ToolCall` / `v1.ToolResult` | 调用 ID 随转换重编；`Images` 经 BlobStore 引用、`Fallback` 语义照抄 |
| `port.ConfirmRequest` / `ConfirmAnswer` | `v1.ConfirmRequest` / `v1.ConfirmAnswer` | 无任务标识字段（contracts §5.2 并发语义）；`Risk` / `Level` 档位词表冻结，两边常量值必须一致 |
| `port.MemoryDoc` / `MemoryIndexEntry` / `MemoryHit` | `v1.MemoryDoc` / `v1.MemoryIndexEntry` / `v1.MemoryHit` | `Name` 即文档 ID；`ErrMemoryNotFound` 哨兵的 v1 对位错误必须可 `errors.Is` |
| `port.SpeechSegment` | `v1.SpeechSegment` | PCM 原样字节，时间戳口径不变 |
| `port.RawInput` / `Attachment` / `IngestReport` / `OutputRequest` | `v1.RawInput` / `v1.Attachment` / `v1.IngestReport` / `v1.OutputRequest` | `BlobRef` 在 v1 是字符串别名（内容寻址键原样携带）；附件体积上限常量（`MaxAttachments` / `MaxImageBytes`）两边同步 |
| `port.Secrets`（`Get`） | `v1.SecretResolver`（Host 消费面） | 方向相反的两件分清：插件**供给**密钥走 `SecretProvider` 接口（P3），内核**解析给**插件走 `SecretResolver` |
| `port.TokenCounter` / `Prober` | `v1.TokenCounter` / `v1.Prober`（P3） | 可选接口，type assertion 探测；转换是纯值搬运，无易错点 |

新增 v1 类型时先在表里补一行再写代码；port 类型新增字段时按本表逐点过（核对清单在
[development.md](development.md)）。

## 4. 供给面：九个必选接口 + 三个可选扩展

### 必选面

| v1 接口 | 对应 port | 说明 |
|---|---|---|
| `LanguageModel` | `port.LLM` | 流式生成 + 模型清单。`Stream` / `Delta` 独立重述，七项语义（Text / ToolCall / Usage / Reasoning / ReasoningSignature / Title / ServerBlock）与 D42 / D175 / D309 / D310 对齐 |
| `ToolProvider` | `port.Tool` | 工具声明 + `Execute(ctx, Call) Result`。确认、超时、裁剪仍由内核 ToolRunner 包装，插件只拿到裁决后的调用 |
| `CommandProvider` | `app.CommandHandler` | 命令注册（含参数相）；`/help`、分派、补全归内核 |
| `MemoryStore` | `port.MemoryStore` | 文档记忆后端；向量检索插件的入口 |
| `Ingestor` | `port.Ingestor` | 输入摄取；OCR、音频直输等降级链扩展点 |
| `OutputAdapter` | `port.OutputAdapter` | 输出镜像；首个实现是导出文件输出器 |
| `Transcriber` / `Synthesizer` | `port.Modality` | 语音两端口，含分段器形态 |
| `SecretProvider` | `port.Secrets` | keychain 类实现；方向相反——内核调插件，防腐层单独说明 |

### 可选扩展面（同包独立接口，宿主按 type assertion 探测）

- `TokenCounter`：精确计数，覆盖估算链（D26）。
- `Prober`：能力探测三态（D174）。
- `Describer`：PTC 二段式 describe 的对位（D320）。

### 明确不进 v1 的（写死，防含糊）

`ConversationStore` 写面、树 / Revise / Carry、`Job` / `Delegate` 协作面、`watch` / `inject`、
`Presenter` / `Prompter` 装载（D143 收窄后维持，见 §6）、设置窗整体结构。这些是内核协作面，
不是插件供给面；插件要影响这些协作面，只能经 Tool / Command 的合法路径。

## 5. Host：消费面

Host 是插件可用的全部内核服务，单一结构分发，不裸暴露内核类型。首发六项：

```go
type Host interface {
    Logger() Logger            // 五段式日志的插件命名空间（D276 同一写出点）
    Secrets() SecretResolver   // 按名解析 secret:<name>，明文不落插件
    Blobs() BlobStore          // 内容寻址附件读写，与内核共享去重与 GC 语义
    WorkDir() string           // 插件私有临时目录（随 profile，关机清理）
    Confirm(req ConfirmRequest) (ConfirmAnswer, error)  // 走内核确认链
    Notify(level, text string) // 用户通知出口
}
```

每项的设计理由：

1. **Logger**：插件自开日志文件就破了「运行日志一处写出」（D276）。
2. **Secrets**：只给解析、不给存储；插件各存一份密钥是密钥安全规则（硬性规则 8）的反面。
3. **Blobs**：裸文件路径绕开内容寻址去重与 GC；面板展示附件内容也绕不开 BlobStore。
4. **WorkDir**：给插件一个有清理语义的落盘位置，不鼓励另开目录。
5. **Confirm**：插件不得自造确认 UI；权限矩阵、审计、无人值守语义（D285）才能罩住插件。
6. **Notify**：见 §6 的通知路径。

Host 的方法集是 v1 里最容易过度承诺的地方：每个方法都要过一遍「内核愿意为这个方法背多久的
兼容债」。首发宁可少给，靠只增不改补。

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

交互往返：插件 `Host.ShowView(spec)` → 用户点按钮或提交表单 → 内核回调
`OnViewAction(viewID, actionID, payload)` → 插件更新树 → 内核重渲染。**状态权威始终在
插件侧**，内核只持当前帧；回放、切会话、重启不产生 UI 状态同步问题。

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
2. **Validated**：宿主启动时逐项查 Manifest——provides 自检（声明 LLM 必须实现
   `LanguageModel`）、api 版本断言（v1 宿主只认 `api: 1`）、名字冲突（与内置工具、MCP
   端点撞名即拒）。校验失败停在 Stopped 并记因，不影响其他插件（软启动规则照抄 Tier-2）。
3. **Active**：`Init` 成功后可被调用；工具进注册表、模型进降级链、命令进 `/help`。
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

### 操作面

1. `/plugin enable|disable <name>` 与设置窗「插件」页开关，写同一个 config
   `plugins.<name>.enabled`（启停状态存 config，D31 语义复用），改动立即生效、不重启。
2. Tier-2 的启停沿用现有进程级 enable/disable；用户操作一致，内部机制各自。
3. 唯一需要重启的：换二进制本身（新增或删除编译进去的插件）。

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

1. `pluginapi/v1` 独立 go.mod；内核对 pluginapi/v1 的依赖是唯一入口，插件作者 `go get` 这一个包。
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

**能做**：接新厂商模型、精确计数、能力探测；任何工具形状的新工具与新命令；换记忆后端；
新输入摄取与新输出镜像；接新 ASR / TTS 引擎（外置服务形状的唤醒词、声纹、说话人分离）；
接 keychain；贡献设置页、面板、通知。

**契约明确排除**：动会话树（拿不到写面，看不到对话内容）；注入模型上下文（没有 system
prompt / 消息流注入接口）；替换或宿主 UI（不暴露 Gio、不改主题、不塞消息流）。

**技术上可行、契约不承诺（自行负责）**：自开网络与文件读写（不走审计、不走 Host 日志、
密钥自理）；后台 goroutine 长任务（生命周期只有 Init / Shutdown 两个锚点，无 Job 台账
语义，半截状态自理）；插件间直接调用（无寻址，除非经内核工具注册表绕行）。

**v1 先不做、登记重启条件**：面板词汇表扩展（每项要 GUI / TUI 两个渲染器跟上）；
状态栏挂件位；消息流装饰 API（等「MCP 富内容回传」排期交付时一并设计）；运行期装载（不做）。

## 11. 防腐层（plugingo）

1. 位置 `internal/adapter/plugingo`：v1 ⇄ port 双向转换器 + 宿主侧 Host 实现（把内核的
   logger、secretbin、blobfs 包成 v1 Host）。
2. 转换器逐字段手写；不反射、不 JSON round-trip（前者隐性耦合字段名，后者丢类型与
   性能）。每个转换函数配一个黄金往返测试。
3. port 侧重构时的核对清单（进 development.md）：改了某个 port 类型字段 → 按映射点
   列表逐点过 → 有语义变化就补决策记录。

## 12. 验收（P3，首个插件）

首个 Tier-1 插件定为**间隔重复记忆插件**（独立仓库 `aq-memory-plugin`，设计稿见其
`design.md`：`MemoryStore` 后端 + 三个检索工具 + 分层面板）。选型标准不随具体插件变：
一条路径同时覆盖四层——供给面实现 + `Init` 里解析 `secret:` + 落盘或附件经消费面
（WorkDir / Blobs）+ `/plugin` 与设置窗可见（管理面）+ Shutdown 收尾（生命周期）。
选中的插件同时是契约的验收测试与文档样例；导出文件输出器（`OutputAdapter`）留作
后续样例候选。

## 13. 决策索引

本文规格的动机与否决立为 [decisions.md](decisions.md) 的 D327，要点：

1. v1 类型独立重述，不暴露 `internal/port` 类型。
2. Host 首发六项（Logger / Secrets / Blobs / WorkDir / Confirm / Notify）。
3. UI 扩展面随 v1 开放；D143 收窄为「装载关、贡献开」。
4. 面板词汇首发六种 Node，降级行为写死。
5. 插件文案自带 zh / en，不进内核 i18n 表。
6. 生命周期工厂化；运行时启停走状态机，无暂停档。
7. capabilities 降为展示语义；确认链强制走 Host。
8. 运行期动态装载、消息流渲染 API、样式主题 API：不做（各有否决理由）。
