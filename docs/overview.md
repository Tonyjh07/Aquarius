# 架构导读
<!-- docs-sync: 2026-10-29 @ 8c1f643 -->

> **定位、统一语言与限界上下文以本文为准**，同时是 **10 分钟建立索引的地图**：内核做什么、
> 目录怎么分、端口对哪个适配器。内核的逐条规格（领域不变量、端口接口、应用层语义、
> 安全与并发）见 [contracts.md](contracts.md)；理由与否决方案见 [decisions.md](decisions.md)。

## Aquarius 是什么

面向个人的**极简 AI 助手**：对话优先、多模态出入、文档记忆、可插拔工具与模型，
Go 单二进制交付，数据全在 `~/.aquarius/`。不是编码工作流产品——没有 subagent、
任务编排、事件总线、工作区概念。

- 核心是**对话体验**：流式交互、消息修订分支、回溯对比。
- **极简内核**：无 subagent、无任务编排层、无消息总线。内核只有三件事：会话树、上下文装配
  （含压缩）、Turn 循环。
- **多模态出入**：文本 / 图片 / 文档 / 语音皆可输入，文本 / 语音 / 通知皆可输出；
  输入输出**方式**可插拔。
- **记忆 = 文档**：markdown 文件即记忆，用户可直接编辑；模型经工具读写；检索 = 关键词。
- **不可变节点 + 用户主权**：历史节点永不修改；「改」= 创建同级新节点，是否携带后续历史由用户选择。
- **通用能力内置**：后台任务、文件操作、终端执行。**没有工作区概念**（无项目根/索引/监听）。
- **内核极简、边缘可插**：工具、模型、记忆后端、命令、UI、输入/输出方式全部是插件面；
  进程外扩展统一走 **MCP**；内置实现与三方插件走同一契约（**内置不享特权**）。
- **面向 agent 的文本用英文**：进入模型上下文的字符串（system 提示、工具声明与参数描述、
  工具回填结果/错误）一律英文；面向用户的界面、命令输出与日志用中文（D36）。

架构原则：

1. 六边形架构：`domain` 纯 Go、零依赖；外部世界全部是端口（`port`）的适配器。
2. **两个稳定级**：`pluginapi/v1` 对外严格 semver；`internal/port` 是内核内部的缝、自由演进。
   二者类型独立。
3. 横切能力（重试/退避、审计日志、截断）用**端口装饰器**叠加，不进插件 API（限流由重试退避承担，
   暂无独立限流装饰器）。
4. 会话树是纯追加结构：一切变化 = 新增节点 / 增删边 / 边转移 / 移动 Head。
5. 插件**永远不能直接操作会话树**，一切经内核中转。

## 统一语言

| 术语 | 英文 | 定义 |
|---|---|---|
| 会话树 | Conversation | 一棵不可变消息树 + 一个 Head 游标 |
| 节点 | Message | 树节点，创建后只读：角色、内容分片（工具调用/结果内嵌于 assistant 分片，D95）、终态、用量 |
| 内容分片 | Part | 消息内容的多态片段：Text / Image / Audio / Doc / Thinking / Tool（D95） |
| 附件 | Attachment | 被消息引用的二进制内容，sha256 内容寻址存储 |
| 根 | Root | 实节点空消息（ID=会话 ID、Role=root），唯一 `Parent==""` 的节点，仅作树管理、不进模型上下文 |
| 头 | Head | 当前游标（初始=Root，无空串特例），决定发给模型的线性路径 |
| 人格 | persona | 会话首节点（system 角色，config 快照），压缩水位之上也恒回传 |
| 压缩摘要 | Compact Summary | system 角色水位节点：其上历史（persona 除外）不再回传模型 |
| 权限等级 | Permission Level | read-only / strict / permissive / full-access 四档矩阵预设 |
| 路径 | Path | Root → Head 的节点序列 = 本次推理的上下文 |
| 轮次 | Turn | 一次「模型生成 + 0..n 次工具执行」的循环 |
| 提交 | Commit | 流式结束后把本轮产出作为不可变节点一次性挂入树 |
| 修订 | Revise | 创建同级新节点（同父）；`Fresh`=新分支重新开始，`Carry`=后续历史边转移过来，`Clone`=深拷贝后续历史为独立副本（D96） |
| 工具调用 | ToolCall / Result | 模型发起的调用与回填结果（内嵌为 assistant 节点的 Tool 分片，D95） |
| 后台任务 | Job | 异步执行的进程：可查状态、拉日志、终止 |
| 摄取器 | Ingestor | 输入方式适配器：原始输入（文本/文件/剪贴板/麦克风）→ Part 列表 |
| 输出器 | OutputAdapter | 输出方式适配器：TTS 播报 / 系统通知 / 导出文件 |
| 转写 / 合成 | Transcriber / Synthesizer | 语音模态端口：ASR（音频→文本）/ TTS（文本→音频） |
| 记忆文档 | MemoryDoc | 一篇 markdown 记忆（name + 文本） |
| 技能 | Skill | 一个目录 + `SKILL.md`：按需载入的指令包（发现只读 name/description，命中才载入正文，D201） |
| 外部代理 | Delegate Agent | 经 ACP 接进来的外部程序：我们把一件复杂任务整个交给它跑，只收回报告（D244） |
| MCP 网关 | mcpgate | 把 MCP server 适配为内核端口实现的适配器（Tier-2 插件面） |
| ACP 网关 | acpgate | 把外部 ACP 代理适配为 `delegate` 工具实现的适配器（与 MCP 网关对称） |
| 增量 | Delta | 流式返回的文本/工具调用片段 |

## 战略设计：限界上下文

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
**工作区**（项目目录抽象/代码索引/文件监听）、**独立于文档的记忆系统**（记忆 = markdown 文档，
一切一并写入；无自动入库的记忆巩固/摘要记忆管线）、embedding 检索。

> **上下文压缩摘要**（[contracts.md](contracts.md) §4.1 的关键语义与 §7.1 的三轨压缩）不是记忆系统：
> 它管理的是**对话上下文**（agent 框架必备能力），转写结果以 system 节点入会话树、随会话走，
> 与 `memory/*.md` 记忆文档无关。

## 内核只有三件事

1. **会话树** —— 不可变消息树 + Head 游标（`internal/domain/conversation`）
2. **上下文装配** —— Path → 模型消息，含压缩水位（`internal/app/prompt.go`）
3. **Turn 循环** —— 一次"模型生成 + 0..n 次工具执行"（`internal/app/agent.go`）

## 六边形与依赖铁律

```
              ┌────────────── 内核 ──────────────┐
   适配器 ───▶│ internal/port   （接口 + DTO）    │◀── 应用层
 (llm/repl/   │      ▲                │         │   (internal/app:
  storejson…) │      │                ▼         │    agent/prompt/session)
              │ internal/domain ← internal/app  │
              └──────────────────────────────────┘
                       ▲
              cmd/aquarius（装配根：config → 端口 → 会话 → REPL）
```

内核另含 `internal/plugin`（插件宿主：发现、grant、崩溃限次重启）——它只依赖 `domain`/`port`，
和 `app` 同层；`mcpgate` 是它的协议适配器（`mcpgate → plugin` 方向正确）。

铁律（AGENTS.md 全文为准）：

- **依赖方向**：`adapter → port ← app → domain`；`domain` 零依赖（不 import 端口/适配器/SDK）。
- **内置不享特权**：内置工具/存储/UI 一律经端口契约接入，app/domain 不直连具体实现。
- **流式不进领域**：增量只到 Presenter；Turn 结束一次性 Commit 不可变节点。
- **横切走装饰器**：重试（含 429 退避）/截断/审计在装配根叠加，不进业务代码与插件 API。

## 会话树一图

```
Root(实节点, ID=会话ID, role=root)
 └── persona (role=system, 恒回传)          ← 会话首节点，config 快照
      └── m1(user) ── m2(assistant) ── …     ← 普通链，Head 游标在此移动
           └── 摘要 (role=system, 水位)      ← /compact 生成；其上不再回传
                └── m3(user) ── m4 …
```

- 一切变化 = 新增节点 / 增删边 / 移动 Head；修订 = 同级新节点（Fresh / Carry 边转移）。
- 两条不变量（树合法 / 节点不可变）由性质测试守护（D95 后引用合法性由工具分片同片承载结构性保证）。

## 目录地图

| 目录 | 职责 |
|---|---|
| `cmd/aquarius` | 装配根：config 解析、端口注入、权限/密钥、REPL 主循环 |
| `internal/domain/conversation` | 会话树：Root/persona/Revise/Prune/水位语义 |
| `internal/domain/tool` | 工具值对象：Spec / Call / Result / Risk |
| `internal/domain/perm` | 权限等级与免确认矩阵（纯函数，D22） |
| `internal/port` | 全部端口：llm/tool/store/memory/blob/modality/ingest/job/ui/misc |
| `internal/app` | 应用层：agent（Turn 循环）、prompt（装配/水位）、session（命令） |
| `internal/adapter/llm` | OpenAI 兼容流式适配器（SSE）+ `llm/tokenizer`（HF BPE 精确计数，D26②） |
| `internal/adapter/repl` | REPL Presenter + Prompter（标准输入输出） |
| `internal/adapter/storejson` | 一会话一 JSON + 一代 .bak |
| `internal/adapter/memoryfs` | 记忆存储：全局 memories.md + 会话 `<id>.memory.md`（D23） |
| `internal/adapter/toolbuiltin` | 内置工具：memory_* / file_* / think / term_exec / job_*（经端口契约接入，D13） |
| `internal/adapter/toolrun` | ToolRunner 门面：权限判定 → 确认 → 超时 → 裁剪（D25） |
| `pluginapi/v1` | 对外稳定契约（独立 go.mod；**D29 后移出 M4，尚未建目录**；与 port 类型互不引用） |
| `docs/` | 文档（本目录）：地图见 [README.md](README.md) |

已落位适配器（M3）：`blobfs`（附件库+启动 GC）/ `jobproc`（后台任务）/
`ingestfile`/`ingestclip`（摄取管线）/ `notify`（通知输出器）/ `atomicfile`（原子写入原语）；
（M4）：`decorate`（横切装饰器）/ `mcpgate`（MCP → port 投影）/ `uitui`（bubbletea TUI）+
`internal/plugin` 宿主（发现/grant/崩溃限次重启）；
（M5 主体已落位）：`uigui`（Gio 悬浮球 GUI，D43/§15——悬浮球/托盘/快捷键/主题/设置核心档与
S1/S1b/S1c/S2/S2b 增强均已落地，已落地步骤见 [roadmap.md](roadmap.md)，后续计划见 [plan.md](plan.md)）；
待落位：`plugingo` + `pluginapi/v1`（D29 后移）；语音侧适配器（`asrchat`/`asrwhisper`/`asropenai`/
`asrfunasr`/`mimotts`/`vitstts`/`gptsovitstts`/`zhconv`/`micwin`/`audioplay`/`outputtts`）与屏幕
捕获（`screencap`）已随 M11 落位。

## 端口 × 内置适配器（落地里程碑）

| 端口 | 内置适配器 | 落地 | 测试替身 |
|---|---|---|---|
| `LLM` | openai 兼容（+可选本地 tokenizer 精确计数，D26） | **M0 ✓** | 脚本化 Stream |
| `ConversationStore` | storejson | **M0 ✓** | in-memory |
| `Presenter` / `Prompter` | repl（行式）+ uitui（TUI，D33）+ uigui（GUI 悬浮球，D43/§15） | repl **M0 ✓** / uitui **M4 ✓** / uigui M5（主体 ✓） | 收集器 / 脚本队列 |
| `Clock` / `IDGen` / `Secrets` | 系统时钟 / ULID / env | **M0 ✓** | 固定 / 递增 / map |
| `Tool` | toolbuiltin（memory_*/file_*/think/sleep + M3 的 term_exec/job_*）+ app 的 context_compact + mcpgate 的 `mcp:<server>:<tool>` | **M2 ✓**（M3 扩充，M4+MCP） | fake tool |
| `ToolRunner` | toolrun（权限判定/确认/超时/裁剪，D25） | **M2 ✓** | fake runner |
| `Confirmer` | repl 确认（读行 y/N；`-yes` 全免）；uitui 确认对话（D33）；uigui 输入栏确认态（D43） | **M1 ✓** | 脚本应答 |
| `MemoryStore` | memoryfs（全局 memories.md + 会话记忆，D23） | **M2 ✓** | in-memory |
| `AttachmentStore` | blobfs（sha256 寻址 + 启动 GC） | **M3 ✓** | in-memory |
| `Transcriber` / `Synthesizer` | （选型待定） | backlog（D27） | 假转写 |
| `Ingestor` / `OutputAdapter` | ingestfile/ingestclip（文本/文件/剪贴板）；notify | **M3 ✓** | 脚本 |
| `JobManager` | jobproc（日志落盘，D8 内存表；进程输出按行解码为 UTF-8，D41） | **M3 ✓** | 假任务 |

## 两个稳定级

- **`internal/port`**：内核内部的缝，可随内核自由重构；
- **`pluginapi/v1`**（D29 后移出 M4，见 [plan.md](plan.md) 的 Tier-1 条目）：对外严格 semver，只增不改，breaking 走 v2；
  两侧类型互不引用，由 `adapter/plugingo`（后移）与 `adapter/mcpgate`（M4 ✓）做防腐转换——
  外部插件永远编译不到内核类型。

## 延伸阅读

- 逐条决策（编号范围见该文索引表）：[decisions.md](decisions.md)
- 使用与命令：[用户手册](guide.md)（日常使用）｜[参考手册](usage.md)（命令与参数）｜ 配置：[configuration.md](configuration.md)
- 数据与备份：[storage.md](storage.md) ｜ 动手开发：[development.md](development.md)
