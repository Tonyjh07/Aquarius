# 进程内 Go 插件：内核侧实现规格
<!-- docs-sync: 2026-11-28 @ d106c4f -->

> **本文写给改宿主的人与 AI 编码代理。** 插件作者要看的写法、接口语义、宿主能力与打包
> 步骤在 [plugin-guide.md](plugin-guide.md)——作者面的事实以那一篇为准，本文不复述。
>
> 本文管插件在内核侧的实现：类型边界与防腐、装配与接管、界面事件在宿主侧的落地、
> 构建器里插件怎么被链进来。末尾是验收与决策索引。
>
> 三级模型的总形状在 [contracts.md](contracts.md) §6.2；Tier-3 的规格见
> [wasm-plugin-design.md](wasm-plugin-design.md)。决策动机与否决见
> [decisions.md](decisions.md) 的 D327。

## 1. v1 与内核类型的边界

`pluginapi` 是独立模块（仓库 `pluginapi/` 目录），module path
`github.com/Tonyjh07/Aquarius/pluginapi`，包名 `pluginapi`，零依赖、只许标准库。
module path **不带** `/v1` 后缀——Go 的规则是 v1 隐式、v2 起才要后缀，`…/v1` 是非法
module path；「v1」由包文档与 `APIVersion` 常量承载，将来的破坏性变更开 `pluginapi/v2`
独立模块并存。根 go.mod 以 `require` + `replace`（指向 `./pluginapi`）引入——同仓多模块，
不设 go.work；外部插件仓库按 tag 依赖同一路径（tag 还没打，过渡期用 `replace` 指到
本地仓库）。

方向规则只有一条：接口里的每个类型都是 v1 自己定义的。v1 不 import `internal/port`，
`internal/port` 也不 import v1。内核侧的依赖规则由 arch 测试守着（`layerPluginAPI`
只许自引用，adapter 与 plugin 两层允许 import）；v1 侧的独立性（不 import
`internal/…`、不引三方）由包内 `independence_test.go` 自查。

### 1.1 值类型层的三条重述规则

v1 最重要的资产是一层语义等价、字段冻结的稳定词汇。`internal/port` 里的值对象
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

### 1.2 防腐映射点列表（`plugingo` 转换器照单核对）

| port 侧 | v1 侧 | 语义口径与易错点 |
|---|---|---|
| `conversation.Usage` | `v1.Usage` | `InputTokens` = 总输入（含缓存读写两段，D308 对外口径）；`CostUSD` 不进 v1——成本折算归内核，模型插件只报 token |
| `port.ModelInfo` / `ModelCaps` | `v1.ModelInfo` / `v1.ModelCaps` | caps 逐位对齐（会什么，D173）；单价字段单位随内核口径（每百万 token） |
| `llm.Delta` | `v1.Delta` | 七项语义对齐（Text / ToolCall / Usage / Reasoning / ReasoningSignature / Title / ServerBlock）；`ReasoningSignature` 是透传字节，原样保真逐轮回传 |
| `llm.ServerBlock` | `v1.ServerBlock` | `Raw` 原样保真、逐轮回传——这是「透传不解读」义务的样板字段 |
| `tool.Spec` | `v1.ToolSpec` | `Schema`（json.RawMessage）按透传字节处理；内核侧 `Describe` 函数字段不进 v1，其对位是可选 `Describer` 接口（D320）。名字文法 `[A-Za-z0-9_-]{1,64}`：`plugingo` 在每轮装配时逐条校验，不合文法或同轮重名整份拒掉（模型端点会 400，早报好读） |
| `port.ToolSet` | `v1.ToolProvider`（`Tools(ctx)`） | 清单不物化进注册表：装配时向源要、执行时向源找。名字只在**会话可见集合内**唯一，不同会话可以同名；撞内置时以内置为准；工具源的端点不受 `tools.disabled` 约束——逐名禁用对逐轮变化的名字没有意义，开关是插件启停（D340） |
| `tool.Call` / `tool.Result` | `v1.ToolCall` / `v1.ToolResult` | 调用 ID 随转换重编；`Images` 经 BlobStore 引用、`Fallback` 语义照抄 |
| `port.ConfirmRequest` / `ConfirmAnswer` | `v1.ConfirmRequest` / `v1.ConfirmAnswer` | 无任务标识字段（contracts §5.2 并发语义）；`Risk` / `Level` 档位词表冻结，两边常量值必须一致；`perm.Op` 是 int 枚举（iota 顺序不是契约），v1 侧投影为字符串（`"read"` / `"write"` / 空） |
| `port.MemoryDoc` / `MemoryIndexEntry` / `MemoryHit` / `MemoryStore` | **不进 v1** | 文档方法集是 memoryfs 的形状，不是记忆的契约。插件面只留注入挂载点 `Memory.Recall`，值类型只有 `MemoryRequest` / `MemoryContext` / `LegacyMemorySnapshot`；存储、组织、检索、管理全归插件（见 §2）。接管期间 `memory_*` 四工具与 `/memory` 退出，内置记忆转只读、只作迁移素材 |
| `port.WithSessionID` / `SessionIDFrom` | `v1.WithSession` / `v1.SessionFrom` | 同一个 ctx 值，换一层包名。会话身份不进任何 v1 值类型；空串 = 不注入；宿主每个插件入口都要 `WithSession` 一次，漏注入不报错、只让插件退回全局 |
| `port.SpeechSegment` | `v1.SpeechSegment` | PCM 原样字节，时间戳口径不变 |
| `port.RawInput` / `Attachment` / `IngestReport` / `OutputRequest` | `v1.RawInput` / `v1.Attachment` / `v1.IngestReport` / `v1.OutputRequest` | `conversation.BlobRef` 结构体（Hash/MIME/Name/Size）在 v1 原样重述，不是字符串别名；`Part` 按**正文投影**重述（Kind/Text/Ref/Transcript/Signature/服务端块原文）——`Tool` 分片不进 v1（对外播报与摄取只用正文，工具往返是内核协作面）；附件体积上限常量（`MaxAttachments` / `MaxImageBytes`）两边同步 |
| `port.Secrets`（`Get`） | `v1.SecretResolver`（Host 消费面） | 方向相反的两件分清：插件**供给**密钥走 `SecretProvider` 接口，内核**解析给**插件走 `SecretResolver` |
| `port.TokenCounter` / `Prober` | `v1.TokenCounter` / `v1.Prober` | 可选接口，type assertion 探测；转换是纯值搬运，无易错点 |
| `port.GenerateRequest` / `PromptMessage` / `PromptPart` / `Sampling` / `TokenBudget` | `v1.GenerateRequest` / `v1.PromptMessage` / `v1.PromptPart` / `v1.Sampling` / `v1.TokenBudget` | 装配后的请求形状。`PromptPart.Data` 是已解析的图片字节，不是路径；`PromptMessage.ServerBlocks` 与 `Delta` 的 `ServerBlock.Raw` 同一条透传义务；`Sampling` 的浮点参数用指针（nil = 不发送，0 是有意义取值）；`ServerTools` 非空时 `Tools` 必须为空——协议里两者互斥 |
| `port.CommandInfo` / `CommandParam` / `ParamKind` | `v1.CommandSpec` / `v1.CommandParam` / `v1.ParamKind` | 名字给主名 + 别名（首名为主名）；`ParamKind` 三档词表冻结（text / choice / flag）；`Values` 是 choice 的可选值，flag 带枚举值时也放这里 |
| `port.SpeechRequest` | `v1.SpeechRequest` | 字段一一对应（Text / Voice / Design / Style / Tags）；音色与风格是配置项，不进模型上下文、不进会话树 |
| `port.SpeechSegmenter` | `v1.SpeechSegmenter` | 逐帧喂入、按说话切段；`Flush` 收尾。返回的段即 `v1.SpeechSegment`，PCM 原样字节 |
| `port.ProbeResult` / `ProbeVerdict` | `v1.ProbeResult` / `v1.ProbeVerdict` | 三态判定（无结论 / 支持 / 不支持）；`Reason` 是给用户看的一句话，可能夹带服务端原文片段——那是数据不是文案 |
| `port.Log` | `v1.Logger` | 五段式日志的插件命名空间：级别由插件给，上下文段由宿主填成 `plugin:<name>`，事件 / 行动两句由插件自写（插件文案不进内核文案表），kv 是「名, 值」且名固定英文；插件不自开日志文件（D276 一处写出） |
| `port.AttachmentStore` | `v1.BlobStore` | 内容寻址三方法（Put / Get / Stat）。**GC 不进 v1**：keep 集来自会话树，插件交上来的 keep 会把别人引用的字节删掉 |

新增 v1 类型时先在表里补一行再写代码；port 类型新增字段时按本表逐点过（核对清单在
[development.md](development.md)）。

## 2. 内核侧接入：记忆接管

Agent 只认一个入口 `port.MemorySource`（`Recall(ctx, 会话 ID, 预算)`），因此「谁回答」可以
在运行期切换：

- **内置回答者** `memoryfs.Source`：两篇白名单（全局 + 当前会话）拼成一块，排版属于内置实现
  ——插件接管后换掉的正是这一份，内核不背这套形状。
- **路由器**（装配根的 `memoryRoute`）：`owner == ""` 时问内置，否则问接管插件的
  `plugingo.NewMemory`。`/context` 里这一段因此显示 `plugin:<名字>`。
- **切换点**只有两个：`/plugin enable|disable` 与启动期按 config 恢复。

### 2.1 顺序（D340）

接管记忆的插件若沿用内置工具名（`memory_read` / `memory_search` / …），装配顺序必须是
**先撤内置四工具 → 再启用插件 → 再挂插件工具源 → 接管注入面**。

1. **启用**：先撤内置面（四工具并进 Runner 的禁用并集、`/memory` 进 Session 的隐藏集），
   再启用插件，最后挂工具源并接管注入。撤必须早于启用——内置工具还在，插件沿用
   `memory_*` 的名字就会被内置顶掉（Runner 合并时内置优先），而模型根本看不到插件那份。
2. **停用**：先摘工具源、停实例（Draining 等在途调用跑完），再交还内置面。反过来做，
   内置记忆会在插件还活着的窗口里与插件那份同时存在。
3. **失败还原**：启用失败必须把内置面放回去，并保住已有接管者——一次被拒的启用不该让
   别人的接管或用户的记忆消失。
4. **挂源排在启用之后**：工具源要从已 Active 的实例上取清单，没有实例就没有源。
5. **启停之间不留空窗**：这一串在同一个调用里同步走完，中间不跑 Turn。

腾出名字是有意为之：模型侧的提示词、习惯与既有对话里对 `memory_search` 的引用都不用改，
迁移成本几乎为零。

其余两条接法：MCP resources 在接管期间不再投影（插件是唯一所有者）；迁移素材只给声明了
`provides: memory` 的插件，判据取清单快照而不是反查注册表——造 Host 时注册表正锁在启用
流程里，回调再查注册表就死锁。

## 3. 界面事件在宿主侧的落地

作者面的词汇与行为在 [plugin-guide.md](plugin-guide.md) §6。宿主侧定下来的几条：

1. **入口只有一条命令**：`/plugin view <插件> [<面板ID>]`。省略 ID 而插件只有一个面板就
   直接打开，多个则列出来让人挑——猜一个打开，用户看到的是自己没要的那块。事件
   （`port.OpenViewEvent`）里只有目标，内容由前端现取（拉模式的直接结果）。
2. **一窗一 `(plugin, view)`**（D351）：同一插件的两个面板可以同时开着，同一视图重开只是
   聚焦并重取一帧（`winHandle.reload`），没有「原地换目标」的全局原子槽。窗标题取
   `ViewSpec.Title`，尺寸按 `ViewSpec.Window` 叠在缺省上并夹取（`applyWinSpec` /
   `clampDp`：`[320,4096]`，最小尺寸不超过终值且不小于 240×200），几何在 spawn 时一次
   算定（`windowShape`），帧内不再查插件。窗口表 `winHost.live` 的键是
   `winKey{kind, plugin, view}`。
3. **停用即关窗**（D351）：停用插件时收编**该插件的全部**面板窗，而不是只关屏幕上看得到
   的那一个。留着上一帧等于让用户对着一个已经死掉的插件点按钮。
4. **设置页的落位判定在宿主**（D350）：`SettingsSectionProvider` 声明 `Placement`，宿主
   按插件聚合——任意一段声明 `page`，该插件全部参数进独立页，清单页不再留参数；独立页
   不渲染清单，顶部是「只看「x」的设置」与返回。开关仍由宿主渲染，走 `setTierOne`。
   开关的旁路 goroutine 用 `atomic.Bool` 标记，回到本窗时重建。
5. **值永远现取**：开窗与每次保存成功后各取一次快照，控件按行表重建。插件自己写的值拷贝
   会在「先构造界面、后启用插件」的顺序下被烤成空态——`PluginAdmin` / 注册表一律存函数
   现取，不存值。
6. **TUI 的面板是只读的**：`form` / `buttons` 照渲染（形状、当前值、编号），但不接交互，
   末行明说「操作请用命令」。设计稿的降级表原写「form → 逐行问答、buttons → 数字选择」，
   那条要求把面板接进输入循环，与命令面重复；收窄到只读是决定，不是没做完。
7. **降级有定义**：词汇表每扩一项，两份渲染器（GUI 与文本）各配一段降级行为，且都先画
   该 `Kind` 自身、再按序画 `Children`。词汇表与容器语义在作者面文档里，这里只管两处渲染
   都得跟上。

## 4. 构建期注入的机制

进程内插件是编译期链接，「装一个插件」在事实层面就是「拿一份含该插件的工厂列表重新构建」。
三方各有一条边界：

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

注入靠两条只影响本次编译的 flag，由 `cmd/aqbuild` 执行（参数表在作者面文档里）：

1. `-overlay=overlay.json`：把这个文件在编译单元里换成插件带来的一份实现。被换掉的原文件
   不参与编译，两份实现不会同时在场；换上去的那份带 `tier1sample` 约束，在插件仓库自己的
   `go build ./...` 里也不参与。
2. `-modfile=<插件侧>.mod`：让依赖解析读插件仓库里那份 go.mod 副本（宿主原 go.mod 加
   `require` + `replace` 插件模块）。没有这份 modfile，注入进来的一段代码一旦 import
   插件自己的包就找不到模块。

三条实测事实（2026-11-26 用探针插件验证，见 D343）：`-modfile` 指向仓库目录之外合法；
要带 `-mod=mod`，构建过程才能把 `require` 规整进那份副本（不加会报「updates to go.mod
needed」）；副本、go.sum、overlay.json 与可执行文件全部落在插件仓库或缓存目录，上游
`git status` 一个字节都不变。

三个被否的做法：

1. **`replace` 写进上游 go.mod**：本地立刻生效，但这条 replace 进了版本库——发布产物与
   下游构建都被绑上插件，正好是「远端看不到示例插件」的反面。
2. **`go.work`**：干净，但作用域是目录，每个新 clone 与 CI 都得自己 `go work init`，
   忘了就报「找不着模块」，把一次性配置成本摊给所有人。
3. **`-buildmode=plugin`**：Windows 不支持；Linux 上要求宿主与插件的 Go 版本、依赖版本、
   构建标志完全一致，拿 `-buildmode=plugin` 做产品级插件系统是在造事故。

兼容性由编译期天然兜住：注入的代码拿到的 `Host` / `Instance` 就是宿主这份 pluginapi 的
类型；插件若 require 了另一份 pluginapi，`Create(Host)` 签名对不上，编译直接失败。构建器
因此有一条硬要求：**必须把 pluginapi 指到宿主自己的那份**，而不是让 MVS 去挑一个版本。

主仓的默认构建、全量测试与发布产物都不含任何示例插件（`pluginext_test.go` 守着默认构建
的工厂列表为空）。

## 5. 防腐层（`plugingo`）

1. 位置 `internal/adapter/plugingo`：v1 ⇄ port 双向转换器 + 宿主侧 Host 实现（把内核的
   logger、secretbin、blobfs 包成 v1 Host）。
2. 转换器逐字段手写；不反射、不 JSON round-trip（前者隐性耦合字段名，后者丢类型与
   性能）。每个转换函数配一个黄金往返测试。
3. port 侧重构时的核对清单（进 [development.md](development.md)）：改了某个 port 类型字段
   → 按 §1.2 的映射点列表逐点过 → 有语义变化就补决策记录。

落盘形状：

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

## 6. 验收（首个插件）

首个进程内插件是**间隔重复记忆插件**（独立仓库 `aq-memory-plugin`，设计稿见其
`design.md`：记忆注入 + 标题即工具（每个记忆点一个工具，调用即下钻并计数）+ 分层面板 +
首次启用时迁移内置记忆）。这个插件同时是契约的验收测试与文档样例。选型标准不随具体插件变：
一条路径同时覆盖五层——供给面实现 + `Init` 里解析 `secret:` 与迁移快照 + 落盘或附件经
消费面（DataDir / WorkDir / Blobs）+ `/plugin` 与设置窗可见（管理面）+ Shutdown 收尾
（生命周期）。记忆插件额外验两条契约义务：接管期间 `memory_*` 与 `/memory` 确实退出、
内置记忆确实转只读；两个 `provides: memory` 同时启用确实被拒。

验收的形态是「在插件仓库跑构建器，得到一个带该插件的二进制」——不是一条 `go test`。
导出文件输出器（`OutputAdapter`）留作后续样例候选。

## 7. 决策索引

本文规格的动机与否决立为 [decisions.md](decisions.md) 的 D327，要点：

1. v1 类型独立重述，不暴露 `internal/port` 类型。
2. Host 首发八项（Logger / Secrets / Blobs / DataDir / WorkDir / Locale / Confirm / Notify，
   其中 DataDir 与 Locale 由 D341 补入）。
3. UI 扩展面随 v1 开放；D143 收窄为「装载关、贡献开」。
4. 面板词汇首发六种 Node，交互走拉模式（插件不推送界面），降级行为写死。
5. 插件文案自带 zh / en，不进内核 i18n 表。
6. 生命周期工厂化；运行时启停走状态机，无暂停档。
7. capabilities 降为展示语义；确认链强制走 Host。
8. 进程内插件的运行期动态装载、消息流渲染 API、样式主题 API：不做（各有否决理由）；
   外部插件经构建期注入进入二进制（§4，D343）。运行期装载单独立为 Tier-3（wasm 沙箱、
   只有工具面）：[wasm-plugin-design.md](wasm-plugin-design.md)，D344。
9. 记忆由插件完全拥有：契约只留 `Memory.Recall` 注入挂载点，`provides: memory` 独占，
   迁移走 `LegacyMemoryHost` 一次性快照（D337）。
