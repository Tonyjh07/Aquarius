# Aquarius 未来规划（docs/plan.md）
<!-- docs-sync: 2026-11-28 @ e3c8b45 -->

> 定位：**只写还没做、将要做的事**——范围、顺序、节奏、验收。**做过的、怎么演进的，一律不写**
> （那是 [roadmap.md](roadmap.md) 的档案与 [decisions.md](decisions.md) 的决策记录）。逐条决策看
> [decisions.md](decisions.md)；内核规格看 [contracts.md](contracts.md)，冲突以后两者为准。
>
> 规则只有一条——**没写进这里的未来工作，不进代码；做完的，从这里删掉。**
>
> **这份文件会被反复重排，所以别的文档不要引用本文的节号**（节号一挪，引用就错位）。要指代
> 这里的事，就写条目的名字；正文的章节顺序只服务本文自己的可读性，随时可改。
> 长期有效的工程准则与边界不属于待办，所以不在本文：见 [development.md](development.md)
> 的「工程准则与边界」。

- 规模记号：**S** = 半天~1 天，**M** = 2~4 天，**L** = 1 周+（含测试与文档）。
- 版本记号：一个版本 = 一组完整的、可验证的收口；不把半截工作留成版本边界。
- 批次记号：`P<n>-<m>` 是执行批次里的条目号，跨文档引用写批次号（如 `P2-4`），不要用节号。

---

## v1.0.0（在排）：正式版收口

> 下版发 `v1.0.0`。判断依据（2026-11-22 会话）：内核完成度与质量基建已够格，缺口在三笔
> 承诺——插件契约没冻结、隐含契约没显式化、稳定声明没写。后两笔已由 P1 立干净；剩插件
> 契约冻结，落在 P2 骨架 + P3 收尾，这也是本版唯一的关键路径。

- **P1 · 契约显式化**（S~M）· 已完成：四条全部落地——共享可变状态的并发模型（contracts
  §5.2 / §7.1.1 / §5.9）、稳定声明（[stability.md](stability.md)）、MCP 提示来源标记
  （`/mcp:<server>:<prompt>` 渲染结果首行注明来源，走 `withPromptSource`）、风险按来源
  取默认（D328，`Decl.ToolRisk()` + `TestToolRiskBySource`）。
- **P2 · pluginapi/v1 契约**（M）· **已完成（已冻结）**：独立 go.mod 模块（`pluginapi/`，
  零依赖、只许标准库，`independence_test.go` 守独立性）、manifest 与稳定值类型层（value.go，
  每个导出类型带兼容承诺句）、防腐映射点列表（plugin-design.md §1.2，UI/uienv 家族已补行）。
  接口面（供给面 + 可选扩展 + Host + 生命周期 + UI 声明面）随 P3 落盘，`interfaces_test.go`
  用一次性实现锁住形状。v1.0.0 收尾复核后冻结：D345（`MemoryRequest.ToolsAvailable`）与
  D355（迁移快照带文档名）两个缺口补齐、承诺句补齐、`Stream`/`UIEnviron` 编译期断言落位；
  之后只增不改，破坏性变更开 v2。
- **P3 · plugingo 装载器 + 首个 Tier-1 插件**（M~L，关键路径）· **已完成**：按 plugin-design.md §4/§5/§6
  与 plugin-guide.md §4–§7 实现全部四步。第 1 步（接口面）与第 2 步（`adapter/plugingo` 转换层 +
  Host 包装 + 供给面适配器、`plugin/tierone` 状态机与清单校验 + 独占记忆 + Draining）已落，第 3 步：
  注册表构造与 Host 接线（私有目录、当前语言、名字占用判定）、`/plugin` 并入 Tier-1
  （list/enable/disable、启动恢复、写回 `plugins.<name>.enabled`）、启用时挂插件工具源、
  内核侧记忆装配（D342：`port.MemorySource` 单挂载点 + 装配根路由器；接管期间 `memory_*`
  四工具与 `/memory` 退出、MCP resources 不再投影、失败还原、`Host` 迁移快照）。
  启停顺序按 D342 的修正版走：**撤内置记忆面 → 启用插件 → 挂插件工具源 → 接管注入**，
  停用反向。

  外部插件怎么进二进制已经定了（D343）：上游只留 `cmd/aquarius/pluginext.go` 一个中性
  接入口，构建期用 `-overlay` 换文件 + `-modfile` 换依赖视图注入，上游产物零痕迹。
  第 4 步的施工顺序：① `cmd/aqbuild` 构建器（已完成：参数见 plugin-guide.md §10.1，
  testdata 微型宿主/插件跑通端到端——含相对 replace 改写、宿主零改动两条断言）；② 样例插件的
  `store/` 与单测（独立仓库 `aq-memory-plugin` 的 `store` 包，落盘形状与四条实现口径
  记在其 design.md §9）；③ 工具面 + `Recall`（`memplug` 包，窗口/命名/同轮稳定三条口径；
  D345 落地后主路径只回引导、降级才注标题，偏离已收口记在其 design.md §10.4）；④ 命令面 + 面板 + 迁移（命令面与迁移已完成：
  插件侧 `/mem` 六个子命令与首次启用的内置记忆搬迁，宿主侧 Tier-1 命令并入会话动态命令表、
  与 MCP prompts 合流，口径见 plugin-guide.md §4.4 与插件 design.md §11；面板已完成：
  `/plugin view` 打开次窗（GUI 组件树渲染、TUI 渲染成只读文本），四条落地口径与「零 Kind =
  纯容器」的契约语义见 plugin-guide.md §6.2 与 D346）。
  验收：四层验收已落成真二进制端到端测试 `cmd/aquarius/plugin_e2e_test.go`（aqbuild 构建
  宿主+真 memplug，假 LLM 驱动 /plugin 管理面、工具声明与内置 memory_* 撤下、mem_write
  落盘、/mem 读回、重启恢复；`testing.Short` 与样例仓库缺席时跳过）；冻结复核与边界确认
  见 D355 与 `pluginboot_test.go`。

  **样例插件定为间隔重复记忆插件**（独立仓库，设计稿见该仓库
  design.md；`Memory`（含首次启用的内置记忆迁移）+ `ToolProvider` + 面板形态），验收标准
  （覆盖供给面、Host 消费面、管理面、生命周期四层）不变，另加两条记忆契约义务（接管期间
  `memory_*` 与 `/memory` 退出、内置记忆只读；两个 `provides: memory` 同时启用被拒）。
- **P4 · 杂项收口**（S）· 已完成：tokenizer 预切分正则加 `MatchTimeout` 且计数路径
  全程响应取消（报错回落通用估算）；`think` 空白参数运行时报失败；压缩/摘要生成流在
  转写里标注「正在生成摘要」（`DeltaEvent.Summary`）；`/usage` 与事实卡措辞改「最近实测」；
  DeepSeek anthropic 端点的真后端兼容回归用例（`anthropic_live_test.go`，环境变量给 key
  才跑）。
- **收口**：稳定声明复核 → 全门禁 → 发布说明（引 stability.md，写 1.0 的承诺与不承诺）→
  winres 1.0.0.0。

---

## 插件供给面消费接线（已排，v1.0.0 之后）

> `llm` / `ingest` / `output` / `transcribe` / `synthesize` / `secrets` 六个供给面目前只有
> 契约、防腐适配器与清单校验，装配根没有消费点（D356 定语义）。契约已冻结，本项只做
> 消费侧：按面分批接线，批间可独立回退。
>
> - **批 1 · 容器与槽位**：五个路由容器（llmRoute 按模型名路由、ingestRoute 尾位、
>   语音两槽位、密钥链兜底档）+ 输出扇出按名增删；装配点插入。
> - **批 2 · 启停挂载**：`setTierOne` 启用分支逐面挂载（type assertion 失败只 warn），
>   停用反序还原；语音独占（第二个同类 warn 不挂）；启动恢复自动走同一入口。
> - **批 3 · 测试**：容器单测 + 启停集成（pluginboot 模式）。

## 装配根抽包（已排，v1.0.0 发版收口之后）

> 把 `cmd/aquarius` 的装配根抽成可 import 的包。独立立项：它与多 Agent（下节）无关也迟早
> 要做，且是多 Agent 的唯一硬前置；同为装配根大改，按既有口径不进发版窗口，排 v1.0.0
> 发版收口之后（可与插件供给面接线同期或其后）。

- **抽包范围**（L）：`cmd/aquarius` 全是 `package main`（wiring 结构体、bootstrap 与
  profile、assemble_* 装配函数族、UI/命令/设置管理面、插件宿主接线，约 5-8k 行非测试
  代码，另有约 1.1 万行测试），另一个 cmd 入口无法 import。抽成 `internal/boot`：上述
  全部搬家，`cmd/aquarius` 改成薄壳调 `boot.Run(opts)`，剩下的代码只做 flag 解析与
  opts 组装。
- **验收**：行为不变——启动、对话、语音、插件、设置、退出删除数据目录逐项过一遍；
  全门禁绿；`boot` 包对外只暴露 Run 与 opts，装配细节不外泄。
- **先文档后码**：development.md 先记 `internal/boot` 的包职责与边界，再动码。

## 多 Agent 协作装配根（2.0 方向，backlog，未排期）

> 2026-11-28 可行性调研的结论与任务拆分。目标：保持 `cmd/aquarius` 行为不变，新建装配根，
> 每个 Agent 独立消息树、独立 goroutine，经注入通道互相沟通。
>
> 调研结论（2026-11-28）：**领域内核已经支持，外壳有三层要动**。`app.Agent` 把会话树当参数
> 传（`Run(ctx, c)`），prompt/压缩/est 全部 per-Run 无状态；storejson / memoryfs / blobfs
> 按会话 ID 或内容哈希寻址，多树并存零改动；`Session.injectCh` 是现成的会话注入通道，
> Agent 间沟通可在它上面扩。要动的只有：装配根抽包、Session 的「单一活动会话」外壳、
> 输出扇出与 UI 的会话寻址。

条目编号 `MA<n>`，执行顺序即编号序。硬前置是「装配根抽包」（独立条目，见上文），
其余在它之后按序推进、批间可独立回退。

- **MA1 · 多实例装配外壳**（L）：per-agent 一份 (Agent, Session, presenter) 的装配与生命周期。
  - `Agent.setPending` 收进实例（agent.go 现注释「一个进程只有一个在跑的 Session」，
    pendingList 由注入点改为构造参数）；逐个核查装配根注入的 `struct{ w *wiring }`
    适配器（约 30 个），单会话假设的收进实例、无状态的可共享。
  - 每 agent 一个 profile 子目录当配置与数据单元（借现成 profile 机制，几乎不改 config
    schema）；session 树、记忆、附件、job 台账、audit.log 全部 per-profile 天然隔离。
  - Agent 生命周期：配置启停（改 config 重载）、单 agent 崩溃隔离（一个 agent 的 panic
    不拖垮其他实例）与手动停启。主控实例管进程收尾（现有 runPendingWipe 口径照搬）。
- **MA2 · 会话寻址的输出扇出**（M）：现在 `port.Presenter.Emit` 事件不带目标会话 ID，
  路由无从谈起。事件补会话寻址（路由键 = 会话 ID → agent 的映射）、每 agent 一份
  presenter、聚合层按 UI 焦点分发；摄取分派同步补寻址（session.go 现状：Raw 输入只能进
  当前活动会话，多 agent 下要按输入路由到目标 agent 的 Session）。**验收**：两个 agent
  同时生成，各自增量只流进各自视图，互不串流。
- **MA3 · GUI 重新设计**（L，先设计稿后实现）：不是在现有悬浮球上加切换器，而是按多 agent
  工作台重做信息架构。
  - **MA3a · 设计稿**（M）：写进 [gui-design.md](gui-design.md) 新章 + 够格的决策进
    decisions.md。要覆盖的题：主窗形态（现有悬浮球/功能窗双形态在多 agent 下怎么收——
    候选方向：常驻主窗列出全部 agent 与各自会话，悬浮球降级为快捷入口）、agent 总览
    （名字/模型/状态/最近活动，谁在跑一眼可见）、焦点切换（点 agent 切消息树，TreeView
    快照路由按焦点切）、跨 agent 消息流的呈现（谁给谁发了什么、委托状态）、后台任务的
    聚合呈现（现在 job 观察者跟焦点会话走）、设置窗的多 agent 配置页、与单 agent 模式
    的关系（一个 agent 时退回现有形态还是同一套壳）。
  - **MA3b · 实现**（L）：按设计稿施工。UI 只经 `uiFrontend` 接口 + 三个 atomic 反向端口
    （`w.sessPtr/w.agentPtr`）对接，core 端口不动；改动集中在 uigui 与装配接线（MA2 的
    聚合层给 UI 供料）。TUI/repl 形态按同一接口各自适配，工作量显著小于 GUI，可另列小笔。
- **MA4 · 协作层：跨 agent 注入**（M）：跨会话注入端口，基于现成的 `Session.injectCh`
  机制扩（注入队列满重试、目标 agent 停用时的报因口径照 pending 现有语义）。第一版只做
  互发消息：agent A 的会话里以工具或命令把一句话注入 agent B 的当前树。**动手前拍板**
  深度：只互发消息，还是带委托/等待结果的工具面（后者要把请求-响应配对、超时、结果回传
  写进契约，见 MA5）。
- **MA5 · 委托与结果回传**（M~L，待 MA4 拍板后立项）：A 委托任务给 B、A 等待或轮询结果、
  结果以附件或引导消息回传进 A 的树。涉及请求-响应配对、超时与取消、委托深度上限
  （防 A→B→A 环）、权限口径（B 用自己的工具面跑，还是继承 A 的授权）——这些是契约级
  问题，先写 contracts.md 对应节 + 决策再动码。
- **MA6 · 端到端验收与文档**（M）：pluginboot 式多 agent 回放测试（脚本 LLM + 收集器
  presenter，禁真实网络——两 agent 互发消息、委托回传、崩溃隔离、输出不串流的 golden
  用例）；全量 `-race`；storage.md 记多 agent 的磁盘形状、configuration.md 记配置面、
  usage.md 记用户怎么用；插件面的行为变化（若插件宿主归主控）补 plugin-guide.md。

**边界与已拍板口径**：插件宿主共享一套（一个 Host、一套插件面，归主控实例管）——否则
同一插件拉两份 MCP 进程，secrets/语音槽「第一个赢」类独占面互相打架；附件不跨 agent 共享
（GC 按本 store 引用清扫，共库会误删，第一版绕开）；数据目录共用 `~/.aquarius`；入口形态
推荐独立 `cmd/xxx`（不动现有回归面）；jobproc / audit / mic / TTS 每实例一套。

**动手前待拍板**：① MA4 的协作深度（只互发消息 vs 带委托工具面）；② 入口形态最终确认
（独立 cmd 还是 `-mode=multi`）；③ MA3a 设计稿里的主窗形态与「单 agent 时退回哪个形态」；
④ 命令面要不要跨 agent 寻址（第一版建议不做——每 agent 的命令只打自己的树，跨控走 MA4）。

## Tier-3 WASM 插件（在排，v1.0.0 之后）

> 规格见 [wasm-plugin-design.md](wasm-plugin-design.md)，动机与否决见 D344。只做工具面；
> 沙箱边界（`/data` + 无网络 + 无密钥）已写死，施工时不再讨论。

- **T1 · guest SDK**（S）：`pluginapi/wasm/v1`，只依赖标准库——请求 / 响应类型、`Serve`
  辅助（挂住缓冲、导出 `aq_alloc` / `aq_invoke`、包 `aquarius.log`）。独立性测试照
  `pluginapi` 的既有做法加一条。
- **T2 · 装载器**（M）：`adapter/wasmplug`——wazero runtime（wasip1 + 宿主模块）、实例化
  （`WithStartFunctions("_initialize")`、`WithDirMount` 到 `/data`、64 MiB 内存上限）、
  `invoke`（写请求缓冲 → 读响应 → 解信封）、单实例串行化、30 秒超时即关模块。
- **T3 · 工具面**（S）：实例包成 `port.ToolSet`（名字 `w3:<插件>:<工具>`、`params` 直接
  转 `tool.Spec`、`risk` 映射到确认链）；故障（trap / 超时 / 坏 JSON）与业务失败分开报。
- **T4 · 发现与启停**（S）：`plugin.json` 的 `wasm` 段解析（与 `mcp` 段互斥、`module`
  禁绝对路径与 `..`、名字须等于目录名）；config `plugins.<name>.enabled` 启停、启动恢复、
  崩溃重建限次退避（沿用 Tier-2 的常量）；Tier-2 的发现扫描静默跳过 wasm 清单。
- **T5 · 管理面**（S）：`/plugin` 合并第三个来源（`source: plugins`、`transport: wasm`），
  list / enable / disable 与写回 config 一套；设置窗插件页只读展示。
- **T6 · 验收**（M）：测试内 `go build` 编 testdata guest（不依赖网络），覆盖 ABI 端到端、
  `/data` 越界、超时打断、崩溃重建、两插件同名工具不互相覆盖。

---

## 测试并行的剩余部分（backlog，未排期）

> v0.5.0 已把全量 `-race` 从 341.7 秒压到约 103 秒；逐包数字、串行判据与压测结论见
> [roadmap.md](roadmap.md) 的 `v0.5.0-E` 行。

**还能压什么**：其余小包，以及 `cmd/aquarius` 里剩下 98 个保持串行的顶层测试。

**先决条件**（都是产品代码改造，本版不做）：把语言槽从进程级收进实例、把 Windows 的
`consoleDetached` 改成原子量或移出 `run()`、把三个注入钩子改成实例字段。

**判据**：全量 `-race` 现停在约 103 秒，瓶颈是 `uigui` 的 62–64 秒与 `cmd/aquarius` 的
58–59 秒互抢 CPU；再降要动这两包的负载本身，不是再加 `t.Parallel()`。重启这一条前先量
一次当下墙钟，别沿用 11-05 的数字。

---

## 生态兼容与轻度角色扮演（backlog，未排期）

> 定位：个人 AI 助手 = 日常工作 + 轻度编码 + 轻度 RP + 兼容现有 AI 生态。
> 内核（会话树 / 压缩 / 记忆 / 工具 / MCP / 技能）已强于对照面，差距全在边缘。
> 动手前先按「先文档后代码」写成 [contracts.md](contracts.md) 对应节 + 决策编号。
>
> 下表的编号是原本的优先序，已完成项不再重复（角色卡批次见 [roadmap.md](roadmap.md) 的
> `v0.3.0-D`，世界书按关键词激活见 D294），跳过即可。

### 未排期

4. **多套用户人设**（M）：命名人设表 + 会话绑定，保持「首节点快照」语义。
5. **`cache_control` 断点**（M）：只在对应适配器做，不动配置面。
6. **会话全文搜索**（M）：扩会话列表端口的数据面，不改存储格式。
7. **MCP server 管理界面**（M）：设置窗「工具与 MCP」页可添加服务器（stdio 命令 / HTTP 地址 /
   环境变量 / 信任），现在只能手写配置。
8. **角色头像与会话背景**（M）：观感提升最大、成本最低；不做整套主题包（角色卡侧的头像
   读入与展示已随 `v0.3.0-D` 完成，这里只剩**会话背景**与主题化）。

---

## 语音前处理与说话人（backlog，未排期）

> VAD 已随 `v0.8.0` 交付（D295）：采集口改成推帧、`vadsilero` 逐帧判定并切段，
> 唤醒词与声纹的连续输入前置就此具备。本节只剩这两项，**各自还压着一条路径待拍板**。

### 唤醒词：待定，暂不排版本

**现状是两条路都不通**，原因不在代码而在分发许可与运行时：

1. **openWakeWord** 代码 Apache-2.0，但**预训练模型是 CC BY-NC-SA 4.0**（非商业 + 传染），
   不能随二进制分发，也不能靠用户「下载即用」绕过这条限制。
2. **microWakeWord**（OHF-Voice）代码与 `esphome/micro-wake-word-models` 都是 Apache-2.0，
   模型层面干净，但 microWakeWord 要 TFLite Micro 的 INT8 运行时；Go 侧只找到 cgo 的 `mattn/go-tflite`，
   没有纯 Go 的 INT8 TFLite 解释器。为唤醒词开 cgo 会破坏「无 cgo、单二进制」的交付规则。

**两条未解路径**（要先拍板走哪条，拍完再进本文排期）：

- **自训 Apache-2.0 模型**：用开放语料训 microWakeWord 形状的 INT8 模型，许可证可自行选定；
  代价是要补一个纯 Go 的 INT8 推理内核（只覆盖该模型用到的算子），属于「为一项功能写一个引擎」。
- **外置 openWakeWord 服务**：不进程内跑，唤醒词交给用户自己起的服务，内核只做 HTTP 客户端。
  与下文声纹同一形状，代价是「单二进制开箱可用」降级为「要另起一个服务」。

不进入任何版本的边界，也不列验收——**先定路径**。

### 声纹识别：外置服务 + 协议位，M

**模型选型**：3D-Speaker 的 CAM++（`speech_campplus_sv_zh_en_16k-common_advanced`，代码
Apache-2.0），80 维 fbank 输入、192 维 embedding，ONNX 体积 fp32 27MB / fp16 14MB / int8 8.2MB，
中文场景可用。WeSpeaker 代码同为 Apache-2.0，但 resnet34-voxceleb 等**权重**是 CC-BY-4.0，
若走 WeSpeaker 必须逐模型核对许可证。

**为什么不进程内跑**：三个纯 Go ONNX 引擎都不够格当生产依赖——`Kazuhito00/onnx-purego-interpreter`
自述比 ONNX Runtime 慢 5–30 倍且「不作为 ORT 替代」，`AdvancedClimateSystems/gonnx` 只到 opset 13
且 2024 年后未发版，`oramasearch/onnx-go` 走 Gorgonia、自述实验性；`shota3506/onnxruntime-purego`
虽免 cgo 但通过 purego 调原生共享库，仍需分发 `.so`/`.dll`。所以**结论是外置服务 + HTTP 适配器**，
不在进程里塞引擎。

**形状**：照既有语音端口的做法办——调用方（`app`）定义一个小端口（如「音频 → 说话人标签」），
配置面按**协议位**新增一个 kind（如 `audio.speaker.kind`，`campplus` / `http` 两档），适配器把 WAV
发给服务、换回 embedding 或「是否本人」的判定。配置取值仍沿用 `base_url` / `api_key` 显式字段优先、
缺的才借 `model.providers` 的既有顺序（D222）；分派仍是白名单 + 越界即报错（D223）。
**先补 [contracts.md](contracts.md) §5.6 与决策记录再动码。**

**用途**（决定验收怎么写，需产品上先定）：说话人分离（多段转写标注是谁）与「只认我的声音」的
入口门。**验收**取决于选哪个：分离版看同一段两人对话能否标对；门禁版看非本人声音是否被拒且
误拒率可接受。

### 与本仓工程准则的关系

- 不得引入工作区/文件索引之类的抽象，也不得让唤醒词或声纹进模型上下文——判定结果只作为
  「这段音频是谁 / 从哪开始」的元信息，附在附件或转写上。
- 唤醒词若最终选外置服务，`README` 的用户面措辞要写「需要额外跑一个服务」，不写内部编号。

---

## 程序化工具调用（PTC-3，backlog，未排期）

> PTC-1 与 PTC-2 已随 `v0.9.0` 交付（`tools.mode: native | ptc`、`run_code`、脚本内确认去重
> 与限额可配）；决策记录在 [decisions.md](decisions.md) 的 D319 / D320，规格在
> [contracts.md](contracts.md) §4.3，调研结论、引擎实测与首跑通过率在 [roadmap.md](roadmap.md)。

- **PTC-3**（S，带前置判据）：Anthropic 原生 `allowed_callers`——报文里真的出现 `caller`
  字段才做，否则登记不做。

---

## DeepSeek 全家桶：未排期的四项（backlog）

> 2026-11-18 按官方「构建 Chatbot」文档逐项对着本仓看过一遍。已排进 `v0.9.0` 的是端点能力声
> 明、用量分项、会话标题、阅读链接与文件输入（前四项已交付）；本节只留没排的，以及为什么
> 没排。

**服务端联网搜索与深度思考搜索**（L，未排期）
声明 `deepseek_web_search` 后，搜索与开页在模型推理内部完成，只有正文进上下文，回答带
`citations` 可展示来源；开思考时一轮最多约 20 次工具调用，这是本地工具循环给不了的体验。

不排的理由是服务端搜索跟树模型正面冲突三条：① 搜索要求把 `server_tool_use`、结果块与
`signature` 逐轮**原样回传**，而树可修订、可克隆、可压缩——压缩一次 `signature` 就没了（官
方说这不会让请求失败，只让读过的网页从上下文消失）；② 用过服务端搜索的回复**不能续写**
（400，得重发上次请求），而本仓有续写与修订；③ 声明搜索之后 `tool_choice` 不能强制，也不
能与其他服务端工具共存。

重启条件：先量一次「不接搜索多花了多少 token」，有数再评估（端点能力声明与用量分项都已就位，
发不发、省多少这两层不再挡路）。接之前还要处理两条语义差：开了这些能力，`temperature` / `top_p` /
`stop` 被服务端忽略（逐模型采样覆盖会静默失效，要能提示）；`max_tokens` 限的是**每一步推理**
而不是整轮，与 `TokenBudget.MaxOutputTokens` 的整轮语义对不上。

**内容安全 `safety_check`**（M，条件性）
`{"level": "high"}` 审输入与输出，输出被拒以 `stop_reason: refusal` 结束，输入命中直接返回
400。自用助手不需要；**对外提供服务时才是合规必需**，那时再排。接的话要处理三条：拒答后已经
流式下发的文本撤不回来（界面要能清掉已显示的回答，换成自己的拒答文案）；审核消耗计进输出，
并在 `usage` 里单列 `safety_tokens`；输入被拒仍按缓存命中价计费。

**TTS 的 WebSocket 流式合成**（M，未排期）
整段合成随 `v0.9.0` 的语音后端一起做；流式是第二步——`wss://api.deepseek.com/v1/tools/tts`
边发文字边收音频帧，于是长回答不必等全文合成完：第一句的音频就能先响。这一项与「暂缓事
项」里「语音后续」的 TTS 流式是同一条，动手时以本节为准。
前置是 `Synthesizer` 端口：现在返回一个 `BlobRef`，流式下要能交出音频流。`outputtts` 的播放
流程也要一起换——现在是落临时文件后异步播（D179），流式下要边收边放。

**三协议兼容回归**（S，未排期）
DeepSeek 现在同时提供 Messages（`/anthropic/v1/messages`）、Responses 与 Chat Completions
三套。可以把 DeepSeek 当第二家真后端跑 anthropic 协议的集成用例——不用 Anthropic 的 key
也能验证「Anthropic 适配器对非 Anthropic 厂商够不够兼容」。这是测试资产，不是功能，所以不
占版本位。

---

## 仍需人工拍板与已知未修项

### 待拍板（技术顺序已由本文定，只剩产品取舍）

- **唤醒词走哪条路**：两条都通不了现在这条道——开放模型的预训练权重不许随二进制分发，
  许可证干净的模型又缺纯 Go 的 INT8 运行时。要么自训模型并配套写一个极小的推理内核，
  要么做成外置服务（「开箱即用」降级为「另起一个服务」）。→ **唤醒词动手前拍板**，
  两条路径见上文「语音前处理与说话人」。
- **声纹识别的用途**：说话人分离（多段转写标注谁说的）与入口门（只认我的声音）是两种验收，
  先定要哪个再写代码。
- **破坏性窗口是否仍然开放**：应用已公开发布，用户可能已有 `~/.aquarius/` 数据。
  本文所有任务按「不破坏既有数据」设计；后续若必须改存储格式，先补迁移器并在本文标注。

### 已知未修项（留档，等条件成熟再排）

- **字体字距缺陷**（D156）：`T` 之后相邻字母被吃掉一个字距，根因在上游 Gio 的矢量字形路径计算，
  v0.10.3 与 main 均未修；本地绕不过（摘 `kern` 表无效、关系统字体才有效但会丢粗体）。
  等上游修，或评估自绘度量方案。
- **欢迎窗第 1 页 200% 字号下语言选择要滚一下**（2026-11-04 离屏走查，用户拍板保持现状）：大字号档
  已压过 logo 与间距、页面可滚，但首屏只放得下 logo + 名字 + 标语；要「第一眼就能切语言」得再省掉
  logo 与标语，那是品牌取舍，不随 0.5.0 改。
- **通知改走自家进程气泡**：弃 `powershell` 子进程方案，复用托盘基建在本进程发气泡，
  顺带升级为系统 toast。零子进程、无冷启，但要消息泵 + WinRT 组件，留在界面批次一并做。

---

## 暂缓事项（backlog，未排期）

> 登记但没排期的事：记下来是为了不再重复讨论，不是承诺。排期时从本节挑走，做完即删。

- MCP sampling（server 借用宿主模型）
- **厂商私有思考块回传**：GPT-OSS/vLLM 系回传键名 `reasoning`（D42 现发 `reasoning_content`，
  vLLM 兼容两者）——接入此类提供商时由适配器做键名映射
- 非 GBK 的遗留代码页（CP437/latin-1 等）终端输出识别（D41 内容探测只有 UTF-8/GBK 两档，会误判成乱码中文）
- `file_read` 等文件文本入口的遗留编码解码（与 D41 同算法，终端之外的文本入口）
- MCP 插件主动健康检查与按需懒加载（当前：启动即连接 + 被动 `Wait` 感知，[contracts.md](contracts.md) §6.4 #4）
- **MCP 富内容回传**：工具结果里的 `ImageContent` 落附件库并回填 `tool.Result.Images`、
  `EmbeddedResource` 的文本展开、`ResourceLink` 可读一次——当前一律降级为 `[image content omitted]` 之类的占位
  （规则落定后再编号）
- **语音后续**：本地 ASR 引擎（whisper.cpp 类，需 cgo 或外挂二进制，与单二进制规则冲突；
  TTS 流式合成见上文「DeepSeek 全家桶」一节）；**导出文件输出器**（把每条回答镜像到文件，[contracts.md](contracts.md) §7.2 的
  `OutputAdapter` 形态）——与 `/export` 的按次整树导出是两件事，后者见 [contracts.md](contracts.md) §5.16
- **备份的后续**：增量与差量备份、归档加密（密钥库那条是另一件事）、把备份做成定时任务
- **GUI 后续（M5/D43 之外，[gui-design.md](gui-design.md) 预留）**：悬浮球本体转常规窗口形态（D43 只预留设计
  不实现；功能窗用独立常规窗已入正册 D60）、多颜色主题预设（M5 只做品牌色
  `#00AEEF` + 深/浅两版）；转写气泡内嵌图片缩略图仍留在此处
- **语音输入与播报（D27 移入）**：TUI 侧拖拽/粘贴/语音按钮（M4 TUI 为 MVP 不含，D33）、
  导出文件输出器、本地 ASR 引擎与其他协议。
- **M2 审查遗留（P3，2026-09 评审；P2 的 trim 工具 schema 估算已修）**：
  - regexp2 `MatchTimeout` 与计数路径的 ctx 检查（模型可控输入的回溯爆炸防护）
  - Agent/Runner 共享可变状态的并发模型显式化（多路任务并存时共享实例的契约：`Confirmer`
    无任务标识、注入队列满时的重试、watcher 表；LLM 适配器 `NoteUnsupported` → config 写回
    那条 D34 竞争已由包级 `cfgWriteMu` 收口）
  - `think` 参数非空校验；压缩摘要流的 UI 标注（"正在生成摘要"以区别于回答流）；
    `/usage` "上轮实测"文案改"最近实测"
- 跨分支"摘抄"共享子树（DAG 化）
- Job 表持久化（SQLite）
- PDF/Office 等 Doc 提取器插件
- 图片 OCR/描述自动降级、音频直输模型（等模型能力普及）
- 长期记忆巩固（记忆 = 文档，无独立记忆系统；**上下文压缩摘要已入正册** [contracts.md](contracts.md) §4.1/§7.1，与记忆无关）
- Web UI、移动端输入方式（GUI 桌面前端已入正册 [gui-design.md](gui-design.md) / D43/M5）
- 向量检索记忆后端（作为 MemoryStore 插件）
- **生态兼容与轻度角色扮演**（2026-10-22 对照调查记录）：按「个人 AI 助手 = 日常工作 + 轻度编码 +
  轻度 RP + 兼容现有生态」的定位，与成熟角色扮演前端（角色卡 / 世界书 / 预设）的差距**全在边缘**，
  内核（会话树 / 压缩 / 记忆 / 工具 / MCP / 技能）已强于对照面。**只登记、未排期**，条目、做法要点与
  优先级见本文的「生态兼容与轻度角色扮演」清单（角色卡只读导入与会话导出**已完成**
  `v0.3.0-D` / `v0.3.0-A`，见 [contracts.md](contracts.md) §5.17/§5.16；**卡的常驻世界书条目已随 D247 进正册**，
  检索式激活已随 D294 交付）。
- **外部代理转交（ACP）的后续**（2026-10-27 调研记录，主线见 [contracts.md](contracts.md) §5.18/D244）：v1 只做
  全新会话、不写入磁盘、不转发 Aquarius 的 MCP server。登记但**未排期**的两件：① 会话内复用子代理
  （`session/resume` + `<profile>/delegate/` 句柄写入磁盘，端口复用位已留）；② 子代理转录按需写入磁盘
  供事后翻（不进上下文）。设置窗配置与 `fs` / `terminal` 申报已随 v0.6.0 发布
  （见 [roadmap.md](roadmap.md)）；**转接的并行**不单列——这项随后台化一起进主线（D261）。
  **明确不做**：内置代理预设、按 registry 自动安装与升级、ACP v2。
- **决策策略的两个阈值**（`auto` / `review`）：影子模式跑够样本后定，本文不预设。独立评测显示
  二值概率不能按 0.5 直接切（同一份数据上按训练集调阈值后 micro-F1 从 0.50 到 0.75），且零样本
  迁移到「工具调用审批」这类负载未被证明——有实测把正常工具输出判成注入、且错得最自信。
  **重启条件**：真机以 `decide.mode: "shadow"` 跑出一批样本再标定。
- **三条伪版本依赖**（2026-11-14 复核：都换不掉，等上游发 tag）：`go.mod` 里还剩三条直接依赖
  不是 tag——`lipgloss`、`ledongthuc/pdf`、`go-silerovad`。
  - `lipgloss`：上游最新 tag 是 `v1.1.0`，当前的 `v1.1.1-0.20250404…` 由 `glamour v1.0.0` 的
    `go.mod` 强制；换成 tag 会把 glamour 连带降到 `v0.9.1`，等于拿一个 tag 换另一个 tag 的降级。
  - `ledongthuc/pdf`：上游从无 tag。带 tag 的同 API 分支 `dslipak/pdf` 停在 `v0.0.2`（早年快照），
    换过去要回退近年修复；仍在维护的 `pdfcpu` 接口不同，得重写提取器。
  - `go-silerovad`（D295）：上游从无 tag，只能钉提交（`v0.0.0-20261009220311-dc87378735b7`）。
    升级要逐次读 diff——这个库承载语音分段的推理与权重，`Detector`/`Stream`/`Segmenter` 的行为变化
    会直接改分段结果，不能靠「跑一下还能编过」验收。
  - **判据**：见到任一条发 tag（或有安全通告）当天换上，并各跑一次绑定测试：lipgloss → TUI 观感，
    pdf → 文档转文本的 PDF 路径（选型见 [roadmap.md](roadmap.md) 的 Q17），
    silerovad → 分段回归（静音零段、单段、三段与停顿阈值）。
