# 架构导读

> 本文衍生自 [DESIGN.md](../DESIGN.md) §1/§3/§5/§6，定位是 **10 分钟建立索引的地图**；
> **冲突以 DESIGN.md 为准**（决策全集在 §13，D1–D28）。

## Aquarius 是什么

面向个人的**极简 AI 助手**：对话优先、多模态出入、文档记忆、可插拔工具与模型，
Go 单二进制交付，数据全在 `~/.aquarius/`。不是编码工作流产品——没有 subagent、
任务编排、事件总线、工作区概念。

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

铁律（AGENTS.md 全文为准）：

- **依赖方向**：`adapter → port ← app → domain`；`domain` 零依赖（不 import 端口/适配器/SDK）。
- **内置不享特权**：内置工具/存储/UI 一律经端口契约接入，app/domain 不直连具体实现。
- **流式不进领域**：增量只到 Presenter；Turn 结束一次性 Commit 不可变节点。
- **横切走装饰器**：重试/限流/截断/审计在装配根叠加，不进业务代码与插件 API。

## 会话树一图

```
Root(实节点, ID=会话ID, role=root)
 └── persona (role=system, 恒回传)          ← 会话首节点，config 快照
      └── m1(user) ── m2(assistant) ── …     ← 普通链，Head 游标在此移动
           └── 摘要 (role=system, 水位)      ← /compact 生成；其上不再回传
                └── m3(user) ── m4 …
```

- 一切变化 = 新增节点 / 增删边 / 移动 Head；修订 = 同级新节点（Fresh / Carry 边转移）。
- 三条不变量（树合法 / 引用合法 / 节点不可变）由性质测试守护。

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
| `pluginapi/v1` | 对外稳定契约（独立 go.mod，M4 起用；与 port 类型互不引用） |
| `docs/` | 衍生文档（本目录）；权威是 DESIGN.md |

已落位适配器（M3）：`blobfs`（附件库+启动 GC）/ `jobproc`（后台任务）/
`ingestfile`/`ingestclip`（摄取管线）/ `notify`（通知输出器）/ `atomicfile`（原子写入原语）；
待落位：`mcpgate`/`uitui`/`plugingo`（M4）、`asr`/`tts`（backlog，D27）。

## 端口 × 内置适配器（落地里程碑）

| 端口 | 内置适配器 | 落地 | 测试替身 |
|---|---|---|---|
| `LLM` | openai 兼容（+可选本地 tokenizer 精确计数，D26） | **M0 ✓** | 脚本化 Stream |
| `ConversationStore` | storejson | **M0 ✓** | in-memory |
| `Presenter` / `Prompter` | repl（TUI → uitui） | repl **M0 ✓** | 收集器 / 脚本队列 |
| `Clock` / `IDGen` / `Secrets` | 系统时钟 / ULID / env | **M0 ✓** | 固定 / 递增 / map |
| `Tool` | toolbuiltin（memory_*/file_*/think + M3 的 term_exec/job_*）+ app 的 context_compact | **M2 ✓**（M3 扩充） | fake tool |
| `ToolRunner` | toolrun（权限判定/确认/超时/裁剪，D25） | **M2 ✓** | fake runner |
| `Confirmer` | repl 确认（读行 y/N；`-yes` 全免） | **M1 ✓** | 脚本应答 |
| `MemoryStore` | memoryfs（全局 memories.md + 会话记忆，D23） | **M2 ✓** | in-memory |
| `AttachmentStore` | blobfs（sha256 寻址 + 启动 GC） | **M3 ✓** | in-memory |
| `Transcriber` / `Synthesizer` | （选型待定） | backlog（D27） | 假转写 |
| `Ingestor` / `OutputAdapter` | ingestfile/ingestclip（文本/文件/剪贴板）；notify | **M3 ✓** | 脚本 |
| `JobManager` | jobproc（日志落盘，D8 内存表） | **M3 ✓** | 假任务 |

## 两个稳定级

- **`internal/port`**：内核内部的缝，可随内核自由重构；
- **`pluginapi/v1`**（M4 起）：对外严格 semver，只增不改，breaking 走 v2；
  两侧类型互不引用，由 `adapter/plugingo` 与 `adapter/mcpgate` 做防腐转换——
  外部插件永远编译不到内核类型。

## 延伸阅读

- 逐条决策（D1–D28）：[DESIGN.md §13](../DESIGN.md)
- 使用与命令：[usage.md](usage.md) ｜ 配置：[configuration.md](configuration.md)
- 数据与备份：[storage.md](storage.md) ｜ 动手开发：[development.md](development.md)
