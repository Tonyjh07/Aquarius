# 已决决策记录（ADR）

> **权威设计文档之一**：原 [DESIGN.md](../DESIGN.md) §13 于 2026-09 拆出，与 DESIGN.md 同级同权威
> （`docs/` 其余篇目为衍生导读）。编号 **D1–D67 跨文件不变**，各处 `§13` / `Dn` 引用均指本文件。
>
> **维护规则**
>
> 1. **先改文档再改代码**：新决策在此追加，编号只增不改、不重排；被取代者保留原文并在 **状态** 指向取代者。
> 2. 字段：**决策**（必）、**否决**（必）；**动机/实测**、**验证** 仅在原文有显式段落时单列——历史决策
>    （D1–D41）设计期定案，动机写在正文、验证即 DESIGN §12 里程碑验收，不为凑字段补造内容。
> 3. 本文件与 DESIGN §12（里程碑）、§14（Backlog）交叉对账，随每次增补复查。

## 索引

| # | 决策 | 状态 |
|---|---|---|
| D1 | 节点不可变；用户"改"= 创建同级新节点 | 生效 |
| D2 | Revise `Carry` = 边转移 | 生效 |
| D3 | 流式中间态不进领域，Turn 结束一次性 Commit | 生效 |
| D4 | Tier-2 插件协议 = MCP（mcpgate 适配） | 生效 |
| D5 | MCP sampling 反向调用 v1 拒绝 | 生效 |
| D6 | 文件工具默认沙箱 = 家目录，越界逐次 Confirm（被 D22 取代：权限等级矩阵 + sandbox 特权目录） | 被 D22 取代 |
| D7 | `Prune` 硬删，storejson 写前留一代 `.bak` | 生效 |
| D8 | Job 表 v1 内存态，日志落盘 | 生效 |
| D9 | UI 插件 v1 只留契约不开放装载 | 生效 |
| D10 | Doc Part 文本提取 v1 简单截断内联 | 生效 |
| D11 | 记忆 = markdown 文档 + 关键词检索 | 生效 |
| D12 | 工具调用引用放宽为"存在性引用" | 生效 |
| D13 | 内置能力与三方插件同走端口契约（内置不享特权） | 生效 |
| D14 | 横切能力用装饰器，不进插件 API | 生效 |
| D15 | 虚拟 Root 是唯一根，顶层消息可多条（`Head==""` = 游标在虚拟 Root）（root 载体被 D19 修订为实节点） | 部分被 D19 修订 |
| D16 | Carry 边转移 = 改挂 `Children` 边 + 改写直接孩子的 `Parent` 边指针；Revise 的 Head 语义见 §4.1 | 生效 |
| D17 | `Usage` / `BlobRef` 由 `domain/conversation` 持有，`port` 直接引用 | 生效 |
| D18 | tool 节点不可 Revise；`Prune` 连带清理失联 tool 结果节点 | 生效 |
| D19 | Root 实节点化 | 生效 |
| D20 | persona 进树为会话首节点（system 角色，config `system_prompt` 快照，恒回传，可 Revise/Prune） | 生效 |
| D21 | 上下文压缩三轨（手动 /compact 本轮、超阈值自动 70%、`context_compact`/Safe 自触发，后两轨 M2） | 生效 |
| D22 | 权限等级矩阵四档（默认 strict）+ sandbox 特权目录，两列分工（文件看路径格…） | 生效 |
| D23 | 记忆布局 = 全局单文件 `memories.md` + 会话级 `conversations/<id>.memory.md` | 生效 |
| D24 | `/memory` = 系统编辑器直开记忆文件（无子命令） | 生效 |
| D25 | ToolRunner 落位 `internal/adapter/toolrun`，文件类权限经可选接口 `FileTarget` 由工具自申报目标路径 | 生效 |
| D26 | token 计数三级链 | 生效 |
| D27 | M3 范围调整 | 生效 |
| D28 | 输出器扇出点 = 装配根的 Presenter 装饰器 | 生效 |
| D29 | Tier-1 Go 插件后移出 M4 | 生效 |
| D30 | MCP 客户端用官方 `modelcontextprotocol/go-sdk`（纯 Go…） | 生效 |
| D31 | 插件启停与授权状态统一存 config… | 生效 |
| D32 | `/model <name>` = Agent 内热切换 + 写回 config（同 `/permission` 模式，重启沿用） | 生效 |
| D33 | TUI MVP = 转写区 + 流式 + 输入框 + 命令历史 + Confirm 对话… | 部分被 D43 接续 |
| D34 | 思考控制面 | 部分被 D42 修订 |
| D35 | `model.api_key` 允许明文 | 生效 |
| D36 | 面向 agent 的文本一律英文 | 生效 |
| D37 | persona 创建时附加运行环境块 | 生效 |
| D38 | 单次工具调用超时 = `limits.tool_timeout_sec` 缺省，模型可经保… | 生效 |
| D39 | 新增 `sleep` 内置工具（Safe：1–3600 秒、ctx 可中断、超缺省时长须配 `timeout_sec`） | 生效 |
| D40 | 启动恢复会话经 `HistoryEvent` 回放可见历史（水位 → Head，见 §7.4）+ NoticeEvent 提示会话身份 | 生效 |
| D41 | 进程输出在 jobproc 适配器内按行解码为 UTF-8 | 生效 |
| D42 | 思考过程入树 + config 控制回传 | 生效 |
| D43 | GUI 前端 = Gio 悬浮球换壳 | 生效 |
| D44 | GUI 悬浮渲染架构 = 逐元素形裁 + headless/ULW 淡出 | 部分被 D62 改判 |
| D45 | 元素边缘羽化 = 淡出 overlay 整窗效果层（D44 扩展，§15.1/§15.3）：`SetWindowRgn` 二值掩码致圆角锯齿——headless 同帧渲染已存在（D44），overlay 从"仅带内"扩为整窗 | 部分被 D62 改判 |
| D46 | 边缘羽化推广 = region 内缩 + 环沿真轮廓 | 部分被 D62 改判 |
| D47 | 羽化环去采样 + 环宽响应式 | 生效 |
| D48 | 边缘羽化改「内容自身向内渐隐」+ 取色采样 + 羽化宽收敛 | 生效 |
| D49 | 输入栏几何 = 设计稿 canvas 1:1 三段式 | 生效 |
| D50 | 悬浮球位置行为 = 锚点夹取 + 四边吸附 + 左右停靠隐藏（D43 形态行为细化，§15.1） | 部分被 D52 修订 |
| D51 | 二进制默认启动 GUI + 主窗不进任务栏/Alt+Tab | 生效 |
| D52 | 展开态夹取/吸附锚点 = 输入栏三段包围盒 | 生效 |
| D53 | tips 显隐 = 事件态 × 光标直采 + 心跳唤帧 | 生效 |
| D54 | 展开/收起动画 = 输入栏插值 + 消息揭示带双通道，严格先后 | 生效 |
| D55 | 帧提交次序 = 屏幕态与效果层先于绘制提交（D44/D45/D50/D54 合成时序收口，§15.1/§15.5/§15.6；次序段后被 D58 改判、D59 再改为混合次序，见 D58/D59） | 部分被 D58 改判 |
| D56 | 转写区滚动手势 = 按垂直轴夹取 + 边界由当帧钳制负责（D44/§15.1/§15.3 滚动行为落地） | 生效 |
| D57 | 输入栏过冲几何夹回窗口边界（D54 过冲段修订，§15.1/§15.2） | 生效 |
| D58 | 帧提交次序反转 = 绘制先提交，屏幕态与效果层随后同拍落地 | 部分被 D59 改判 |
| D59 | 帧提交次序再改判 = 形裁回 `e.Frame` 之前、overlay 留在之后（混合次序，修订 D58 的次序段，§15.1/§15.5/§15.6） | 被 D62 收口 |
| D60 | 三类功能窗 = 独立常规 OS 窗口（设置/会话历史/欢迎；窗口管理与生命周期，§15.7） | 生效 |
| D61 | 主题令牌系统先行于设置窗（颜色令牌深浅两版 + `ui.theme` + 主界面全量接入，§15.4） | 生效 |
| D62 | 整窗 ULW 换壳 = 单通道像素根治亚帧通道错位（形裁/LWA/overlay 退役，D46–D48 保留选项启用，§15.1/§15.5/§15.6） | 生效 |
| D63 | 转写文本可选可复制 = 行级 `widget.Selectable` + 拖层把手带收窄 + 编辑器焦点收口（§15.3） | 生效 |
| D64 | GUI 退出 = 立即中断进行中轮次（不等待 Agent 完成，§15.1） | 生效 |
| D65 | 助手正文 markdown 渲染 = goldmark 定稿解析为结构块、行级展开（§15.3 首增量） | 部分被 D66 改判 |
| D66 | 单回复单气泡 = 消息级复合行（块不拆行、共底板，修订 D65 呈现粒度，§15.3） | 生效 |
| D67 | 工具调用单气泡 = 调用/确认/结果合并 chip，默认折叠点击展开（§15.3） | 生效 |

## 记录

### D1 — 节点不可变；用户"改"= 创建同级新节点

- **决策**：节点不可变；用户"改"= 创建同级新节点
- **否决**：就地可变消息（v2 曾选）——放弃，换取崩溃安全与分支对比
- **状态**：生效

### D2 — Revise `Carry` = 边转移

- **决策**：Revise `Carry` = **边转移**
- **否决**：深拷贝子树（数据翻倍、身份分裂）；DAG 共享（复杂度不值）
- **状态**：生效

### D3 — 流式中间态不进领域，Turn 结束一次性 Commit

- **决策**：流式中间态不进领域，Turn 结束一次性 Commit
- **否决**：节点随流变异（半节点/部分写入问题）
- **状态**：生效

### D4 — Tier-2 插件协议 = MCP（mcpgate 适配）

- **决策**：Tier-2 插件协议 = **MCP**（mcpgate 适配）
- **否决**：自造 JSON-RPC（三方接入成本高、生态为零）
- **状态**：生效

### D5 — MCP sampling 反向调用 v1 拒绝

- **决策**：MCP sampling 反向调用 v1 拒绝
- **否决**：—（有真实用例再议）
- **状态**：生效

### D6 — 文件工具默认沙箱 = 家目录，越界逐次 Confirm（被 D22 取代：权限等级矩阵 + sandbox 特权目录）

- **决策**：~~文件工具默认沙箱 = 家目录，越界逐次 Confirm~~（**被 D22 取代**：权限等级矩阵 + sandbox 特权目录）
- **否决**：全路径裸放（误伤面大）；严格 chroot（对个人助手过重）
- **状态**：被 D22 取代

### D7 — `Prune` 硬删，storejson 写前留一代 `.bak`

- **决策**：`Prune` 硬删，storejson 写前留一代 `.bak`
- **否决**：软删除/墓碑（个人数据不留坟墓）
- **状态**：生效

### D8 — Job 表 v1 内存态，日志落盘

- **决策**：Job 表 v1 内存态，日志落盘
- **否决**：SQLite 任务表（量级不足，暂缓）
- **状态**：生效

### D9 — UI 插件 v1 只留契约不开放装载

- **决策**：UI 插件 v1 只留契约不开放装载
- **否决**：开放多 UI 并存（焦点/路由复杂度不值）
- **状态**：生效

### D10 — Doc Part 文本提取 v1 简单截断内联

- **决策**：Doc Part 文本提取 v1 简单截断内联
- **否决**：完整 PDF/Office 提取器（列为 Ingestor 插件面扩展）
- **状态**：生效

### D11 — 记忆 = markdown 文档 + 关键词检索

- **决策**：记忆 = markdown 文档 + 关键词检索
- **否决**：embedding/RAG、自动记忆巩固（个人规模不需要）
- **状态**：生效

### D12 — 工具调用引用放宽为"存在性引用"

- **决策**：工具调用引用放宽为"存在性引用"
- **否决**：严格祖先引用（与 D2 边转移冲突）
- **状态**：生效

### D13 — 内置能力与三方插件同走端口契约（内置不享特权）

- **决策**：内置能力与三方插件同走端口契约（内置不享特权）
- **否决**：内核直连内置能力（内核腐化）
- **状态**：生效

### D14 — 横切能力用装饰器，不进插件 API

- **决策**：横切能力用装饰器，不进插件 API
- **否决**：插件中间件链（API 面失控）
- **状态**：生效

### D15 — 虚拟 Root 是唯一根，顶层消息可多条（`Head==""` = 游标在虚拟 Root）（root 载体被 D19 修订为实节点）

- **决策**：虚拟 Root 是唯一根，顶层消息可多条（`Head==""` = 游标在虚拟 Root）（**root 载体被 D19 修订为实节点**：多顶层语义保留，载体改为 Root 实节点、移除空串特例）
- **否决**：严格单根消息——Revise 首条消息将被禁止，与"改任意消息"的招牌能力冲突
- **状态**：部分被 D19 修订

### D16 — Carry 边转移 = 改挂 `Children` 边 + 改写直接孩子的 `Parent` 边指针；Revise 的 Head 语义见 §4.1

- **决策**：Carry 边转移 = 改挂 `Children` 边 + 改写直接孩子的 `Parent` 边指针；Revise 的 Head 语义见 §4.1
- **否决**："边指针永不改写"（那就只能拷贝子树，回到 D2 否决项）
- **状态**：生效

### D17 — `Usage` / `BlobRef` 由 `domain/conversation` 持有，`port` 直接引用

- **决策**：`Usage` / `BlobRef` 由 `domain/conversation` 持有，`port` 直接引用
- **否决**：port 再造同型 DTO（双份定义易漂移，且 `Part.Ref` 本就要用）
- **状态**：生效

### D18 — tool 节点不可 Revise；`Prune` 连带清理失联 tool 结果节点

- **决策**：tool 节点不可 Revise；`Prune` 连带清理失联 tool 结果节点
- **否决**：允许编辑机器生成的结果（破坏存在性引用不变量）；Prune 后留失联引用（同上）
- **状态**：生效

### D19 — Root 实节点化

- **决策**：Root 实节点化：ID 复用会话 ID（Root 即会话）、新增 `RoleRoot`、移除 `Head/Checkout` 空串特例、Prune/Revise 拒 Root、旧格式破坏性切换（修订 D15 载体）
- **否决**：保留虚拟 Root + 空串特例（API 留魔法值、不变量两套表述）；Root 另造常量/ULID ID（同一棵树两个根 ID，易漂移）
- **状态**：生效

### D20 — persona 进树为会话首节点（system 角色，config `system_prompt` 快照，恒回传，可 Revise/Prune）

- **决策**：persona 进树为会话首节点（system 角色，config `system_prompt` 快照，恒回传，可 Revise/Prune）
- **否决**：只在装配期注入 config（人格随会话不可分叉/不可编辑、审计不到树上）
- **状态**：生效

### D21 — 上下文压缩三轨（手动 /compact 本轮、超阈值自动 70%、`context_compact`/Safe 自触发，后两轨 M2）

- **决策**：上下文压缩三轨（手动 /compact 本轮、超阈值自动 70%、`context_compact`/Safe 自触发，后两轨 M2）；摘要 = system 水位节点，其上不回传（persona 除外），失败回退最旧裁剪
- **否决**：只裁剪不摘要（丢上下文）；摘要存记忆文档（与记忆系统混淆，§3 已排除）；旁路字段存摘要（与树两处存放、重启/回溯易失配）
- **状态**：生效

### D22 — 权限等级矩阵四档（默认 strict）+ sandbox 特权目录，两列分工（文件看路径格…）

- **决策**：权限等级矩阵四档（默认 strict）+ sandbox 特权目录，两列分工（文件看路径格、执行看工具列）、矩阵外一律 ask、`/permission` 写回 config（取代 D6）
- **否决**：家目录沙箱（D6：目录身份与等级正交，表达不了"特权/其他"两档）；矩阵外 deny（个人助手过严）；allow/ask 明细键与矩阵双事实源（判定顺序绕）
- **状态**：生效

### D23 — 记忆布局 = 全局单文件 `memories.md` + 会话级 `conversations/<id>.memory.md`

- **决策**：记忆布局 = 全局单文件 `memories.md` + 会话级 `conversations/<id>.memory.md`
- **否决**：`memory/**/*.md` 目录树（个人单文件即可直读直编，目录树徒增组织成本）；会话记忆独立目录（与会话树分家，迁移/删除要同步两处）
- **状态**：生效

### D24 — `/memory` = 系统编辑器直开记忆文件（无子命令）

- **决策**：`/memory` = 系统编辑器直开记忆文件（无子命令）
- **否决**：`list\|show\|edit\|rm` 子命令集（编辑器即最强编辑 UI，命令面保持极简）
- **状态**：生效

### D25 — ToolRunner 落位 `internal/adapter/toolrun`，文件类权限经可选接口 `FileTarget` 由工具自申报目标路径

- **决策**：ToolRunner 落位 `internal/adapter/toolrun`，文件类权限经可选接口 `FileTarget` 由工具**自申报**目标路径
- **否决**：按工具名前缀硬编码分类（内核腐化、三方工具无法参与）；往 `tool.Spec` 塞权限字段（污染模型可见的工具声明）
- **状态**：生效

### D26 — token 计数三级链

- **决策**：token 计数**三级链**：①服务端实测 usage（已发生的）→ ②适配器可选 `TokenCounter`（本地 tokenizer.json / count_tokens API，覆盖估算）→ ③通用字符估算 + 服务端 usage 自校准；tokenizer 经 `model.tokenizer` 指路径**启动时加载、错误 fail-fast**
- **否决**：通用估算一刀切（已可拿到精确值时不拿）；词表 embed 进二进制（+数 MB 且换模型即失效）；实现 Jinja chat_template 渲染（要引模板引擎，且结构开销用常数已够准）；强推 count_tokens API（openai-compatible 普遍没有）
- **状态**：生效

### D27 — M3 范围调整

- **决策**：M3 落 JobManager + blobfs + Ingestor 管线 + notify 输出器；REPL 输入命令面（`/attach`/`/clip`/`/mic`）与 ASR/TTS/麦克风移入 §14 backlog，随 TUI 后续迭代或 GUI 落地（M4 TUI 为 MVP 不含，D33）
- **否决**：硬凑"语音提问 → TTS 播报"验收（REPL 行式输入无拖拽/语音按钮；语音适配器选型未定，先定契约后装实现）
- **状态**：生效

### D28 — 输出器扇出点 = 装配根的 Presenter 装饰器

- **决策**：输出器扇出点 = 装配根的 **Presenter 装饰器**：包住实际 UI，收到已完成的 assistant `CommittedEvent`（`Outcome=done`）后逐个调 `OutputAdapter.Deliver`，失败只记日志
- **否决**：app 内直连输出器（内核直连具体实现违反 D13；扇出属横切，按 D14 走装配根装饰器）
- **状态**：生效

### D29 — Tier-1 Go 插件后移出 M4

- **决策**：（`pluginapi/v1` + `adapter/plugingo` 移 §14，随后续里程碑落）；M4 只做 Tier-2 MCP
- **否决**：M4 双线并进（§12 验收只针对 MCP；Tier-1 会挤占 TUI、装饰器与审查遗留的容量）
- **状态**：生效

### D30 — MCP 客户端用官方 `modelcontextprotocol/go-sdk`（纯 Go…）

- **决策**：MCP 客户端用官方 **`modelcontextprotocol/go-sdk`**（纯 Go，stdio `CommandTransport` + streamable HTTP `StreamableClientTransport` 双传输）；引入前先 spike 实测，API 不合则回退自写 stdio JSON-RPC + `net/http` SSE。**spike 已过（2026-09）**：双传输回环、工具调用、`IsError` 工具级错误语义均验证；sdk 要求 **go≥1.25**，go.mod 由 1.22 上调
- **否决**：长期自写协议栈（帧格式/能力协商/HTTP 流重连易踩 spec 细节）；cgo/Node 系客户端（违背纯 Go 优先）
- **状态**：生效

### D31 — 插件启停与授权状态统一存 config…

- **决策**：插件**启停与授权状态**统一存 config `plugins.<name> = {enabled, granted[]}`，两种发现源（`mcpServers` / `plugin.json`）共用；声明与状态分离
- **否决**：状态写回 `mcpServers` 条目（plugin.json 发现的插件无处安放）；状态存 plugin.json（本机授权态不该随分发文件走）
- **状态**：生效

### D32 — `/model <name>` = Agent 内热切换 + 写回 config（同 `/permission` 模式，重启沿用）

- **决策**：`/model <name>` = Agent 内热切换 + **写回 config**（同 `/permission` 模式，重启沿用）
- **否决**：每会话独立模型（与"全局唯一 model 配置"冲突，切换语义碎片化）；只切不存（重启即失）
- **状态**：生效

### D33 — TUI MVP = 转写区 + 流式 + 输入框 + 命令历史 + Confirm 对话…

- **决策**：TUI **MVP** = 转写区 + 流式 + 输入框 + 命令历史 + Confirm 对话 + 状态行 + glamour 轻 markdown（committed 后渲染，流式阶段原样）；图片/音频仍占位；`ui.kind` 模板默认 `tui`、repl 保留；GUI 框架后移 §14（**被 D43 接续**：Gio 悬浮球 GUI 入正册 §15/M5）
- **否决**：一步到位富 TUI（拖拽/语音/内联图——与后续 GUI 框架重复投入）；TUI 取代 REPL（e2e/CI 丢失无终端后端）
- **状态**：部分被 D43 接续

### D34 — 思考控制面

- **决策**：`/think [on\|off]` = 原生思考**总开关**（覆盖 `/effort`），`/effort [minimal\|low\|medium\|high\|off]` = `reasoning_effort` 档位；请求发 `reasoning_effort` 与 `enable_thinking`（dashscope 系布尔）两个字段，服务端点名不认 → **同请求剥离重试 + 记录进 config `model.unsupported_params`**（启动注入、以后直接省略）；思维链分片**只展示**（展示口径——入树与回传被 **D42 修订**：随节点入树、回传走 config）；`think` 草稿工具可见性走 config `model.think_tool`、**默认隐藏**（不做 /models 能力探测——兼容端几乎不返回能力信息）
- **否决**：逐家私有布尔映射表（每家一个开关字段，维护面爆炸）；/models 能力探测后自动分叉（探测不可靠、分支形同虚设）；~~思维链入树（回传可能被服务端拒绝且占上下文）~~（**被 D42 取代**：入树且默认回传；拒收顾虑由剥离重试兜底、上下文顾虑交 config 关）
- **状态**：部分被 D42 修订

### D35 — `model.api_key` 允许明文

- **决策**：`model.api_key` **允许明文**：启动打印警告（不回显密钥）、`secret:` 引用仍走 `port.Secrets`；值为空时回落默认 `secret:AQUARIUS_OPENAI_KEY`；文件内值优先
- **否决**：维持明文一律拒绝（用户明确要简化接入）；明文静默启用（丢失风险告知）
- **状态**：生效

### D36 — 面向 agent 的文本一律英文

- **决策**：进入模型上下文的字符串（system 提示、工具声明与参数描述、工具回填结果与错误）用英文；仅面向用户的界面、命令输出、启动警告与日志用中文
- **否决**：中英混杂（模型上下文语言口径漂移，回复语言只应由 system 提示约束）；全站改英文（用户界面跟着变，违背中文协作约定）
- **状态**：生效

### D37 — persona 创建时附加运行环境块

- **决策**：persona 创建时附加**运行环境块**：platform、UI 形态与 TERM、特权沙盒目录（`IsAbs` 才附）、缺省调用超时与 `timeout_sec` 说明；随 config 快照入树（D20 语义不变），字段为空整块跳过
- **否决**：装配期动态注入（与 D20 快照语义冲突，分叉/回溯后环境漂移）；不告知（模型不知道沙盒与平台，只能踩坑后学）
- **状态**：生效

### D38 — 单次工具调用超时 = `limits.tool_timeout_sec` 缺省，模型可经保…

- **决策**：单次工具调用超时 = `limits.tool_timeout_sec` 缺省，模型可经**保留参数** `timeout_sec`（整数 1–3600）逐次覆盖，**允许高于 config**；schema 自声明该参数的工具（`job_start`）不受保留参数约束；执行前从 Args 剥离（MCP 服务端不收未知参数）；值域外/非整数报错回填；发现性经 persona 环境块一行说明
- **否决**：每个工具各加超时参数（16×schema 重复、MCP 工具无法参与）；不可覆盖（长任务与 `sleep` 撞默认 60s）；静默夹取（掩盖模型传参错误，`0` 还会被误读为不限时）；完全无上限（失控面不可控）
- **状态**：生效

### D39 — 新增 `sleep` 内置工具（Safe：1–3600 秒、ctx 可中断、超缺省时长须配 `timeout_sec`）

- **决策**：新增 `sleep` 内置工具（Safe：1–3600 秒、ctx 可中断、超缺省时长须配 `timeout_sec`）
- **否决**：只靠 `job_start` + 轮询日志（重量级）；不提供（模型无法自然停顿 / 等待后台任务收尾）
- **状态**：生效

### D40 — 启动恢复会话经 `HistoryEvent` 回放可见历史（水位 → Head，见 §7.4）+ NoticeEvent 提示会话身份

- **决策**：启动恢复会话经 **`HistoryEvent` 回放可见历史**（水位 → Head，见 §7.4）+ NoticeEvent 提示会话身份
- **否决**：复用 `CommittedEvent` 回放（会触发 D28 输出器重复通知/TTS，且 user/tool 节点在两前端的 Committed 语义是 no-op、渲染不出）；复用 Say 拼纯文本（丢角色样式、TUI 与 repl 各拼一遍易漂移）
- **状态**：生效

### D41 — 进程输出在 jobproc 适配器内按行解码为 UTF-8

- **决策**：进程输出在 **jobproc 适配器内按行解码为 UTF-8**：UTF-8 合法则原样（ASCII 与显式 `chcp 65001` 输出），否则 GBK/CP936 解码；x/text 宽松解码器以 U+FFFD 兜底"两者都不是"，`term_exec` 同步输出与 `job_logs` 日志同口径
- **否决**：给子进程强灌 `chcp 65001`（改变命令运行环境，依赖 OEM 代码页的老程序反而乱码，且控制台代码页是共享状态）；调 `GetConsoleOutputCP`/`GetOEMCP` 精确解码（平台特定代码，子进程 stdout 是 pipe 时与控制台代码页未必一致——内容探测已覆盖真实两档 65001/936）；交 UI 层清洗（字节 → string 转换时 U+FFFD 已产生，事后不可恢复）
- **状态**：生效

### D42 — 思考过程入树 + config 控制回传

- **决策**：（修订 D34）：新增 `PartThinking` 分片（仅 assistant 可携带，节点形态校验把关；流内分片合并至多一段置于正文前，取消/出错终态的已生成思考同样入树）；回传走**独立承载** `PromptMessage.Reasoning` → openai 适配器序列化为 assistant 消息的 `reasoning_content` 字段（**2026-09 调研**：OpenAI 官方 Chat Completions 每轮丢弃推理、也不返回明文思维链——官方端点不触发回传；DeepSeek 等兼容端点带 `tools` 时**强制**回传、缺失即 400；`reasoning_content` 是兼容生态事实标准），端点点名不认则复用 D34 剥离重试管线（扩展到消息内字段）记入 `model.unsupported_params` 后省略；开关 config `model.echo_thinking`（`*bool`：**键缺失 = 回传**，显式 false 关，改后重启生效）；展示口径（实时暗块、启动回放）恒含思考，与回传开关解耦
- **否决**：维持"只展示不入树"（D34 原状：重启/回溯即丢、审计不到树上，违背"树是唯一事实源/用户主权"）；旁路字段或独立文件存思考（D21 否决同款理由：两处存放、回溯易失配）；独立 system/tool 节点承载思考（破坏一轮一 assistant 节点与工具配对语义）；`<thinking>` 文本拼进正文（DeepSeek 工具轮缺 `reasoning_content` 字段仍 400，且思考被当正文污染上下文）；默认不回传（对 DeepSeek 类端点是工具轮硬故障——调研后由"默认关"翻案为"默认回传"）；厂商私有思考块原样回传（Anthropic thinking block 等，列 §14 backlog）
- **状态**：生效

### D43 — GUI 前端 = Gio 悬浮球换壳

- **决策**：（§15/M5）：`internal/adapter/uigui` 同权实现 `uiFrontend`（`port.Presenter + Prompter + Confirmer` + Say/Prompt/SetInterrupt/Close），`ui.kind` 新增 `gui` 接入装配 switch（repl/tui 不动；默认 tui → D51 改默认 gui）；形态 = **单组件悬浮球**（logo 即球，左键展开/收起输入栏、右键菜单）→ 提交后上方转写浮层（每轮清空 + 可固定钉住累计）；托盘常驻生命周期（关窗隐藏、退出经菜单）、全局快捷键（默认 Alt+A，`ui.hotkey` 可改）、拖拽 + 位置记忆；消息流 = 思考暗块（流式实时、定稿折叠为「已思考」行，D42 展示口径恒含）+ 工具折叠 chip 可展开 + 完整 markdown；Confirm = 输入栏切换确认态（非模态）；附件按钮 = 系统文件选择框走 `UserInput.Raw{Kind=file}` 既有摄取管线；主题令牌化（MVP 品牌色 `#00AEEF` + 输入框浅白/浅灰 + 深/浅跟随系统，多颜色预设留数据后补）。**spike 已过（2026-09，§15.6）**：悬浮胶囊用**形裁 `SetWindowRgn`**（Gio 无真透明、色键与 D3D 不兼容白屏、DWM 圆角有描边副作用，均排除）；托盘/Alt+Space 快捷键/多显示器定位/位置记忆/系统中文字形全通过；实现铁律两条见 §15.6（修改性 Win32 调用走 `Window.Run`、拖动用光标绝对跟踪）
- **否决**：Fyne（cgo/OpenGL 违「无 cgo」硬约束、控件样式僵硬做不出透明胶囊）；Wails/Web UI（前端构建链 + webview 依赖，富文本强但与纯 Go 单二进制张力大）；独立 GUI 进程经 IPC（交付变双程序）；悬浮球 + 独立胶囊双组件（两套焦点/生命周期，合并为单组件展开态）；模态弹窗做 Confirm（打断输入流，输入栏确认态更贴极简）；MVP 上多主题预设（先令牌化留一色，加色只改数据）；~~色键透明~~/~~DWM 圆角~~/~~每像素透明~~（spike 实测不可行或被形裁取代，§15.6）
- **状态**：生效

### D44 — GUI 悬浮渲染架构 = 逐元素形裁 + headless/ULW 淡出

- **决策**：（细化 D43 形裁口径，§15.1/§15.3）：无背景"全悬空" = 每帧布局记录可见元素矩形（气泡/输入栏/状态行），`SetWindowRgn` 并集**逐元素挖空**（间隙/边角透明 + 点击穿透；物理 px = 逻辑 × PxPerDp，经 `Window.Run` 重建）；统一半透明 = `LWA_ALPHA` 整窗常量（与形裁正交）；顶边渐变淡出 = 淡出带整带挖空 + 独立 `UpdateLayeredWindow` overlay——像素源取 **`gpu/headless` 离屏渲染同布局**（透明清屏 → alpha=覆盖免掩码、`PxPerDp` 对齐主窗、零值 Source 纯渲染），预乘线性+sRGB 语义转字节预乘后 `ULW_ALPHA+AC_SRC_ALPHA` 提交，`SourceConstantAlpha` = 主窗 LWA_ALPHA × 渐变无缝衔接。**spike 已过（2026-09，§15.6）**：headless 四点全过；PrintWindow 捕获全零已排除
- **否决**：主窗直出 alpha（`gpu.Clear` 写死不透明白 + HWND swapchain alpha 被合成器忽略，per-pixel 仅 DirectComposition / ULW 两条系统路径，Gio 都不走）；色键 `LWA_COLORKEY`（D3D 白屏，§15.6）；屏幕 BitBlt 捕获淡出带（带内已挖空、取不到气泡内容）；`PrintWindow` 捕获（spike 实测连可见区全零）；扫描线抖动近似淡出（观感降级，留作 fallback）；`LWA_ALPHA` 做渐变（仅整窗常量）；接管 swapchain 走 DirectComposition（放弃 Gio 渲染 = 换架构，违背 D43 换壳不换核）
- **状态**：部分被 D62 改判（形裁/overlay 双通道被整窗 ULW 单通道取代；headless 像素源保留并升级为唯一像素源）

### D45 — 元素边缘羽化 = 淡出 overlay 整窗效果层（D44 扩展，§15.1/§15.3）：`SetWindowRgn` 二值掩码致圆角锯齿——headless 同帧渲染已存在（D44），overlay 从"仅带内"扩为整窗

- **决策**：带内渐变照旧，带外对每个可见元素画**羽化环**（几何边界内 alpha=1 盖住二值切口，向外 2px smoothstep 衰减到 0；颜色取同帧内侧像素；`SourceConstantAlpha` 与主窗 LWA 同值衔接），带线以上不画环、被带裁切形状**方顶续接**（region 上两角填方）。输入/caret/形裁语义全不动；z 序仍锚定主窗
- **否决**：~~整窗 ULW 换壳~~（治本但打字 caret 消失需 spike、D44 架构级重构、ULW 取代常规自绘需运行时实证——风险高）；~~纯参数缓解~~（降 semiAlpha/加大圆角/同色描边，治标不显真过渡）
- **状态**：部分被 D62 改判（羽化剖面算法保留、改合成进主窗 ULW 位图；独立 overlay 窗退役）

### D46 — 边缘羽化推广 = region 内缩 + 环沿真轮廓

- **决策**：（D45 修补，§15.1/§15.3）：D45 环实测两缺陷——① 环在边界**内侧** 1px 以 alpha=1 重绘同色像素，叠在主窗同色像素上把不透明度从 0.92 抬到 ~0.99 → 亮边；② 环的 SDF 用**视口/带裁剪后**矩形，跨带形状的环沿带底裁切线描边 → 横缝。修：**region 沿元素真实边内缩 `featherInDp`(2dp)**（裁切边不缩，否则露出环不覆盖的洞；圆角同步收窄），**环改沿真轮廓**（未按视口/带裁剪）+ 只落在可见裁剪区 + 带底以上不落笔（带内由带单绘，避免带/环重复叠加），向外衰减加宽到 `featherOutDp`(6dp)。"band 模型"（region 让位、overlay 单绘、alpha 斜坡）由此推广到所有边缘；`drawShape` 由「裁后矩形」改为「真轮廓 + 裁剪区」两点。输入/caret/形裁语义仍不动
- **否决**：~~D45 原样只调参数~~（内侧重绘亮边与带底横缝是结构性问题，调参只能缓解）；~~整窗 ULW 换壳~~（用户已表态"有效即可考虑"，但 caret 消失仍需 spike 与运行时实证——本修先以现有架构达成同观感，ULW 作为后续独立选项保留）
- **状态**：部分被 D62 改判（region 内缩机制随形裁退役；真轮廓 SDF + 渐隐剖面保留、合成进主窗 ULW 位图）

### D47 — 羽化环去采样 + 环宽响应式

- **决策**：（D46 修补，§15.1/§15.3）：D46 环实测两缺陷——① 环的颜色取 headless 同帧「夹进形状外接矩形内 1px」的采样点，在**圆角/窄条**处该采样点落在形状外的透明区 → `writePremul` 直接丢弃、该处无环像素：直边有环而四角/上下端没有 → 观感成"十字/阶梯"伪影（合成复现已证）；② 环宽固定 `featherInDp/OutDp`(2/6dp)，不随元素尺寸与窗口缩放变化。修：**元素登记时携带自身底色**（气泡/胶囊/chip/tips/球各带 bg），环**不再采样**、直接用底色画（内侧覆盖 `d > -(in+0.5)`、向外到 `out`）的 alpha 斜坡——处处均匀、形状无关（圆角/窄条同样有环；内边界取 `in+0.5` 使环与 GDI 形裁区块逐像素互补，既无叠加亮线也无栅格空洞）；**环宽响应式** `out = clamp(min(w,h) × featherRatio, Dp(featherMinDp), Dp(featherMaxDp))`、`in = clamp(out / featherInRatio, Dp(featherInMinDp), Dp(featherInMaxDp))`，region 内缩与环内侧同源，窗口缩放/自定义宽高/DPI 自动跟随（**不再有固定 px 羽化宽**）。带（顶部渐变）为参照、不动
- **否决**：~~继续调 D46 固定参数~~（不解决圆角缺口与不响应尺寸）；~~整窗 ULW 换壳~~（治本但 headless 无焦点 → caret 需自绘、无 pointer → hover 需自注入事件，工作量/风险明显更大——先以本修达成同观感，ULW 作为后续独立选项保留）
- **状态**：生效

### D48 — 边缘羽化改「内容自身向内渐隐」+ 取色采样 + 羽化宽收敛

- **决策**：（D45–D47 修补，§15.1/§15.3）：D45–D47 的向外环在轮廓线 `d=0` 处剖面**有折点**（内侧 alpha=1 平坦、向外才衰减）→ 观感成「饱和核心 + 外圈亮带」，与顶部淡出带「内容向内渐隐」割裂（用户比对两处边缘的直接结论）。修：① **region 沿真轮廓内缩 `featherWidth`**（裁切边不缩），overlay 在让位出的边带内画「内容自身由内向外渐隐」——region 边界首像素 alpha=1（与主窗像素同不透明度、无缝）→ smoothstep → 轮廓处 `featherEdgeMin`（当前 0 = 淡到全透明），**只在形状内落笔、不向外外扩**（删掉 D45–D47 的 1px 抗锯齿尾与外向公式）；② **取色部分回滚 D47**：形状内**采样同帧 headless 内容**（文字/图标随渐隐自然淡出；采样点只在形状内 → 保住 D46/D47 的圆角「十字/阶梯」回归），headless 不可用（`src` 缺省或尺寸不符）才用元素底色兜底；③ **带内额外乘 `g(y)`**（与带渐变同式）：跨带元素左右边在带底与带下连续，消除带底横缝；④ **羽化宽收敛为单参数族** `fw = clamp(round(min(w,h) × featherRatio), Dp(featherMinDp), Dp(featherMaxDp))` 且 ≤ 短边 1/3（region 不退化、元素不被整体吃掉），当前调参 `0.05 / 0 / 5` → 发丝级 1–5px，region 内缩与渐隐带同源取**真轮廓**短边（裁剪后短边会变，按裁剪尺寸算会与渐隐带错位）。互补口径不变：region 覆盖 `d ≤ -(fw+0.5)`、渐隐覆盖其余（`+0` 圆角留 1px 洞、`+1` 每边叠 1px 亮线，均实测复现）
- **否决**：~~继续用 D47 向外环只调参数~~（折点长在剖面结构里，调参改不掉「核心饱和/外圈亮带」的割裂）；~~固定 px 羽化宽~~（D47 已否决，DPI/窗口缩放下失配）；~~整窗 ULW 换壳~~（caret/hover 需 spike，仍留作后续独立选项）
- **状态**：生效

### D49 — 输入栏几何 = 设计稿 canvas 1:1 三段式

- **决策**：（D43 形态细化，§15.2）：输入栏由「单一胶囊内嵌 logo/send」改为 **[logo ⌀48] 12 [输入胶囊 h48/r24] 12 [send ⌀48]** 三段等高独立元素（各自形裁 + 羽化、间隙透明且点击穿透），尺寸 1:1 取自 Figma 稿 `Untitled.fig` 的 canvas（本地未入库：元素 48、间距 12、内边距 16、图标槽 20、文字 15sp），默认窗宽 560→**608dp** 使默认态行宽 576 = 设计稿、输入区恰 456；**响应式口径**：全部 dp（DPI/系统缩放自适应）+ 中间胶囊 `grow` 随窗口宽伸缩（未来窗口可调只伸缩胶囊，字号不随窗口变）；胶囊内保留两个 20dp 图标槽（附件/展开，灰占位不可点、实现时启用）；右圆钮状态 = idle 发送 / 生成中停止（错误色 + 方块图标）/ 确认态置灰——**几何不随状态变**（收起球位 = 展开态 logo 位，换形不跳动）
- **否决**：白色外层容器胶囊（canvas 原有，用户明确不要——忽略）；以 `temp/ui_design.png` 截图为几何依据（系截图、与 canvas 不符：61/14/483 vs 48/12/456，已确认不准确）；整套随窗口宽等比缩放含字号（字号随窗口变、可读性差）；维持 72dp 现尺寸不改（不遵循设计稿）
- **状态**：生效

### D50 — 悬浮球位置行为 = 锚点夹取 + 四边吸附 + 左右停靠隐藏（D43 形态行为细化，§15.1）

- **决策**：① **夹取**：拖动/恢复共用 `clampAnchor`——收起态锚点 = logo 球（可见物不出桌面即可，透明窗体允许越界，保住"球贴右/上边"的停法）、展开态锚点 = 整窗（转写浮层不丢；D52 改输入栏包围盒，见 D52），最近显示器工作区口径；② **吸附**：抬手距任一边 ≤ `snapDp`(12dp) 贴齐，四边、两态皆可；③ **停靠**：仅收起态 + 球贴齐左右**外侧边**（接缝边外还有屏 → 不触发），触发统一为「停在可停靠边 + 曾悬停 + 光标移开」（按帧直采光标与球矩形的包含关系判定，不依赖 Hover 进出事件时序；悬停 = 布防，启动恢复无悬停不自动滑出；召回移开、召回后拖回可停靠区移开同一判定）——收起态球在可停靠边（含停靠中）常驻 50ms 心跳唤帧（形裁窄区指针悬停可零帧，实测缺陷：窄条悬停 2s 无帧召回不触发、点击必有帧）；纯点击（位移 ≤ `dragClickSlackPx`）抬手不夹取不吸附（防展开态整窗夹取推离「点击脱离停靠」的贴边位、收起后断链，实测缺陷）——滑出留 `dockSliverDp`(8dp) 窄条 + 整窗淡化（LWA_ALPHA `semiAlpha`→`dockAlpha`，overlay `SourceConstantAlpha` 同帧跟随），悬停窄条召回（动画进行中可反向 retarget），点击/按下/呼出立即脱离；④ **动画底座**：`easeOutCubic` p(t) 纯函数 + 16ms ticker → `Invalidate` 唤帧，进度与插值全在事件循环（`layout` 被 headless 二次调用，位置/alpha 前推只放帧分支 `stepAnim`），ticker goroutine 只读自有 channel → 无共享可变状态；⑤ **记忆**：`posRec.docked`（缺省/旧文件 = 未停靠），停靠态恢复按当前工作区重算停靠位（存的 X/Y 忽略），跨线程保存（托盘置顶开关）经 `dockHint` 原子镜像
- **否决**：整窗硬夹取（透明窗体拖累右侧停球/贴边判定，可见锚点更贴语义）；拖动中磁吸（与夹取互相拉扯、指针手感差）；底部停靠（窄条被任务栏遮挡、召回失灵）；展开态可停靠（转写浮层/确认态交互复杂，停靠 = 球的待命语义，用户拍板"仅悬浮球形态可停靠"）；跨屏接缝停靠（滑进相邻显示器藏不住）；原地淡出热区召回（不可见仍占点击、需全局光标轮询）；悬停事件驱动的停靠判定（Enter/Leave 与按下拖动的时序耦合不可靠，改光标直采）
- **状态**：部分被 D52 修订

### D51 — 二进制默认启动 GUI + 主窗不进任务栏/Alt+Tab

- **决策**：（D43 落地后默认形态收口，§8/§15.1）：① `ui.kind` 键缺省与首跑模板默认 = `gui`（D33 起的 `tui` 默认退役——交付形态是悬浮球；`tui`/`repl` 仍可显式指定，测试/e2e 走 `repl` 不受影响；首跑生成配置即退出、不启前端）；② 主窗 `onHWND` 挂接句柄时一次性置 `WS_EX_TOOLWINDOW` + 清 `WS_EX_APPWINDOW`（经窗口线程，铁律 1）：悬浮球托盘常驻、关窗即隐藏，任务栏条目与形态相斥，Alt+Tab 同步剔除
- **否决**：按平台条件选默认（交付与文档以 Windows 为准，非 Windows GUI 未适配时显式 `ui.kind=tui` 即可，不引入分支默认）；保留 `WS_EX_APPWINDOW`（会在任务栏强行显形，与 ② 相斥）；窗口消息钩子动态藏任务栏按钮（时机与可靠性差于建窗即挂样式）
- **状态**：生效

### D52 — 展开态夹取/吸附锚点 = 输入栏三段包围盒

- **决策**：（D50 ① 修订，§15.1）：收起态不变（logo 球），展开态锚点从整窗改为输入栏（logo/胶囊/send）包围盒——**转写消息区允许越出桌面上沿，只限制输入栏不离工作区**（用户拍板）。整窗口径含形裁剔除的透明边距，实测两缺陷：贴边拖到底输入栏被弹离桌面边缘约 20px（左缘要让出整窗透明边距）、展开态顶部无法近（最上只能到透明上边距处）；`anchorFor` 展开分支与普通位置恢复夹取共用同式（恢复期 frameMetric 未就绪 → 沿用 `restoreDock` 的「窗高/`winHeightDp`」比例口径，抽出 `restorePx` 共用）
- **否决**：展开态完全不夹取（输入栏可被拖出屏幕、交互面丢失）；锚点 = 全部可见形状包围盒（转写区参与仍顶住上沿，且内容高度变化使夹取边界漂移）；锚点 = 整窗（实测两缺陷）
- **状态**：生效

### D53 — tips 显隐 = 事件态 × 光标直采 + 心跳唤帧

- **决策**：（D50 光标直采口径推广到 tips，§15.1）：分层窗按像素 alpha 命中穿透——光标移到透明像素或窗外后**零 pointer 事件**，`gesture.Hover`/`widget.Clickable` 永远等不到 `Leave`（实测老 bug：悬停启动/发送/停止 tips 移开后不消失）。修：三处 tips 显示条件叠「`cursorPos` 直采在钮上」（`inputBtnRects` 由 `rowAnchor` 派生圆钮窗口矩形，与停靠悬停同一口径）；任一 tips 在显 → `tipShown` 并入 `heartbeatNeed`（复用 D50 50ms 心跳唤帧复评），光标离钮即熄、心跳随之收敛
- **否决**：`TrackMouseEvent`/`WM_MOUSELEAVE` 自管进出（穿行透明像素仍不可见、需钩 WndProc）；光标全局轮询驱帧（无 tips 时白耗电）；直接清 `Hover` 事件态（事件静默时无帧可跑，判定跑不到）；整窗 ULW 换壳（D46 否决理由同）
- **状态**：生效

### D54 — 展开/收起动画 = 输入栏插值 + 消息揭示带双通道，严格先后

- **决策**：（D43 形态行为细化，§15.1）：左键 logo / 快捷键 / 托盘呼出的展开收起不再瞬时翻形，改为双通道时间线——**展开 700ms = 输入栏 260ms `easeOutBack(c1=1.2)`（轻回弹 ≈6%，send 过冲回落）→ 消息区 440ms CSS ease `cubic-bezier(.25,.1,.25,1)`；收起 540ms = 消息区 320ms CSS ease → 输入栏 220ms `easeInSine`，两阶段严格先后不并行**（消息区时长 = 输入栏的近两倍：揭示刻意比控件归位更慢，让内容浮现更从容）。① **输入栏**：logo 恒定不动（D49 换形不跳动），胶囊/send 从 logo 矩形插值到终位（p=0 二者 = logo 同尺寸圆、被 logo 盖住，p=1 = D49 终位几何），胶囊内容按**终宽排版、按当前胶囊矩形裁剪**（生长即揭示，不挤压重排）；绘制顺序改 **胶囊 → send → logo 最后**（p=0 时 logo 盖住二者，与收起态球逐像素一致 → 收尾切 `layoutCollapsed` 无缝）。② **消息揭示 = 动画淡出带**：内容静止不位移——带顶 `Y = (1-msgP)×transH` 从 transH（全隐）降到 0（静息），`y < Y` 不可见（形裁裁掉 + overlay 不写）、`[Y, min(Y+bandPx, transH))` smoothstep 淡入、带底以下全可见；**带底夹在 transH 内**（展开中途的带绝不压状态行/刚弹出的输入行），`Y = 0` 时退化为现有顶带 `[0, bandPx)` 与静息**无缝重合**——三处带机制（`record` 跳过条件、`regionShapes` band 裁切线、`fadePremultiplyBand` 写行范围 × `fadeFeatherShapes` 带因子 `g(y)`）从"恒为顶带"泛化为动态 `(bandTop, bandBottom)`。③ **状态机与驱动**：`collapsed` 仍即时翻转（逻辑态），layout 分支 = `collapsed && !expandAn.active → layoutCollapsed`（动画期间走全量 layout + barP 插值，收尾 barP=0 时几何 == 收起球）；进度全在帧分支 `stepExpand` 现算、且在 `layout` **之前**（D50 底座复用：16ms ticker → `Invalidate`；headless 二次 layout 须与主窗同帧同进度，进度放 `layout` 内会双倍推进），headless 不起 ticker、只落状态；中途再点 logo **从当前进度反向不跳变**（无距离的阶段瞬时跳过），动画期间 logo 可点、胶囊/send/tips 不可点、停靠评估让位
- **否决**：整体位移淡入（内容会动，与拍板的"内容静止"相悖）；硬边裁剪揭示（割裂横缝，顶带机制本可复用）；两通道并行（输入栏与消息争抢注意力，交互焦点不清）；带底不夹在 transH（展开到一半把刚弹出的输入行顶部扫淡）；进度放 `layout` 内（headless 二次调用双倍推进，D50 已实证）；动画期间胶囊可点（半程几何点击落点错位）
- **状态**：生效

### D55 — 帧提交次序 = 屏幕态与效果层先于绘制提交（D44/D45/D50/D54 合成时序收口，§15.1/§15.5/§15.6；次序段后被 D58 改判、D59 再改为混合次序，见 D58/D59）

- **动机/实测**：展开/收起/停靠动画与拖动中，**元素边缘（羽化带 + 淡出带，全在 ULW overlay 上）恒慢元素一帧**。根因（堆栈实证）：Gio `processFrame` **先 ack 后 `Present(1,0)`**（`app/os.go`："Let the client continue as soon as possible, in particular before a potentially blocking Present"），`Present` 阻塞到下一 vblank；客户端 ack 之后经 `Window.Run`（铁律 1）排队的移窗 / `LWA_ALPHA` / 形裁 / ULW 要等 Present 返回才被窗口线程 select 服务 → 一律落在下一 vblank 之后合成，而主窗绘制已在当前 vblank 上屏 → 效果层恒比主窗晚一帧。旧帧次序 `layout → e.Frame → stepAnim → fadeFrame` 恰好把 `stepAnim`（移窗+alpha）与 `fadeFrame`（ULW）全排在 `e.Frame` **之后**
- **决策**：帧次序固定为 **`stepExpand → stepAnim → layout → fadeCompose → commitWinGeom → fadePresent → e.Frame`**（**该序已由 D58 反转为 `… → fadeCompose → submit(e.Frame) → commitWinGeom → fadePresent`，D59 再改为混合次序 `… → fadeCompose → commitWinGeom → submit(e.Frame) → fadePresent`（形裁回 `e.Frame` 前、overlay 留后）**；以下 ①–④ 仍有效）——① `fadeFrame` 拆成 **`fadeCompose`**（headless 同布局重渲 + 渐变/羽化预乘，唯一慢段、不改屏幕态）与 **`fadePresent`**（ULW 提交或隐藏，须在移窗之后——取实测窗口矩形定位）；② 新增 **`commitWinGeom`**：形裁 + 移窗 + `LWA_ALPHA` **一拍 flush**——帧内 `moveDrag`/`endDrag`/`undockInstant`/`stepAnim` 一律只记账（`requestMove`/`requestAlpha`），避免多次 `Window.Run` 各占一拍、与 overlay 拍点错开；启动路径 `onHWND`/`restoreDock` 不在帧内，仍直接调用；③ `applyRegion` 从 `layout` 移出（headless 二次调用与主遍共用同一提交点；`mainHWND==0` 直接返回、不刷失败日志）；④ 两套动画进度（`stepExpand`/`stepAnim`）都提到 `layout` **之前**，两遍 layout 同帧同进度（顺带修掉 `stepAnim` 夹在两遍 layout 之间、停靠进度两遍不一致的隐患）。次序是契约：抽 `frame(gtx, submit)` 方法 + `framePhase` 测试回执（§15.5），无窗口断言「compose → commit → present → submit」且提交时挂起标记已清（**次序一段后被 D58 修订、D59 再改为混合次序**：见 D58/D59——本行对 `fadeCompose`/`fadePresent` 拆分、进度前置、`commitWinGeom` 一拍 flush 三项仍有效）
- **否决**：`e.Frame` 之后再提交（**D58 已改判**：本条只推到"排队项要等 Present 返回"，未推 bitblt 模型下内容同样要等 Present 拷贝后的下一次合成才渲染——真实后果是屏幕态**先于**内容上屏、实测黑缝 + 月牙，详见 D58）；每帧无条件 `SetWindowPos`/`SetLayeredWindowAttributes`（无变化也发 = 冗余 GDI 调用与闪烁，故只在 pending 时发）；给 overlay 换 DWM / DirectComposition（架构级，D45/D46 已否决）
- **状态**：部分被 D58 改判

### D56 — 转写区滚动手势 = 按垂直轴夹取 + 边界由当帧钳制负责（D44/§15.1/§15.3 滚动行为落地）

- **动机/实测**：转写区的**手工滚动从未生效**（滚轮完全无反应，只靠流式尾随自动贴底）。根因（Gio 源码实证）：`gesture.Scroll.Update(..., scrollx, scrolly)` 两参分别喂 `ScrollX/ScrollY` 两路累计器，而垂直手势累加的是 `e.Scroll.Y`；`updateScroll` 却把可滚范围当 `scrollx` 传了进去 → Y 侧过滤器是零范围 → 每格滚轮都被夹成 0（Gio 自家 `layout/list.go` 就是按轴向把 bounds 换到 Y 的，`Axis==Vertical` 时喂 `scrolly`）
- **决策**：**① 范围按轴向绑定**——横向 `pointer.ScrollRange{}`（X 夹到 0，不横滚），纵向 `pointer.ScrollRange{Min: -overflow, Max: overflow}`（`overflow = total - viewH`，含 fling 溢出余量）；**② 过滤器范围不随 `scrollPx` 走**——过滤器是**上一帧**登记、事件到来时才用于夹取，随位置走会滞后一帧：边界处反向的首格被旧边界误夹成 0 丢失（下滚回去的第一格被吃）；边界反正由 `updateScroll` **当帧钳制**负责（含 fling 溢出），过滤器只求够宽——单格增量不可能超过 `overflow`。语义：`d>0` = 向新内容，**尾随贴底**（流式新消息恒贴底；上滚离底停跟随，滚回 `overflow` 恢复）；内容矮于视口时贴底锚定、`overflow=0` 不滚。无窗口测试经 `input.Router.Queue` 注入滚轮事件覆盖三态（上滚停尾随 / 下滚半程不恢复 / 回底恢复），手势区与事件投递走真实 hit 树 + 过滤器
- **否决**：把边界交给 `ScrollRange` 钳（滞后一帧误夹反向首格，与当帧钳制重复）；手势改走 pointer.Drag 翻页滚动（手感生硬，与气泡区"只滚不拖"的定位冲突）；把夹取范围直接依赖 `scrollPx`（即上述滞后缺陷的形态）
- **状态**：生效

### D57 — 输入栏过冲几何夹回窗口边界（D54 过冲段修订，§15.1/§15.2）

- **动机/实测**：展开回弹段右钮**被自家窗边切平**（"右侧空间不够动画出框"）：`lerpRowRects` 对 `p>1` 沿同一式外推（D54 口径），`send.Max.X = logo.Max.X + (sendEnd.Max.X-logo.Max.X)×p`，在 `easeOutBack` 峰值 `barP≈1.053` 处 ≈ `64+528×1.053 = 620 > 窗宽 608`（无窗口复现实测：`send=(572,396)-(620,444)`，形裁被窗界夹成 `w=34` 而非 44、overlay 越界提交）。根因：行右缘距窗边只剩 `sideMarginDp`(16dp)，而回弹位移 = `(p-1)×(终位−原点)` ≈ `0.053×528 ≈ 28px > 16px`——D54 的外推式对**贴边元素没有护栏**
- **决策**：新增纯函数 **`clampRowX(r, w)`**，在 `inputBar` 插值后把外推矩形**平移收界**（保尺寸只移位；夹到**窗口边界**而非行边距/终位——窗内那 16px 边距正好留给回弹，D54 的"越出终位再回落"在窗内保留可见）。只夹 X：行 Y 是行内局部坐标（真实位置经 `absY` 另加）、且 D49 行高行位不随动画变，不会越界
- **否决**：夹到行边距/终位（回弹在右钮上完全消失，D54"轻回弹"行为落空）；调小 `easeOutBack` 的 `c1`（全局削回弹，且窗宽是变量、窄窗仍越界）；给过冲段单独换一套无过冲缓动（两套缓动口径分叉，几何测试跟着分叉）；扩窗或把右钮静息位往里挪（D49 终位几何与视觉稿固定，动画期才出的事不该改静息位）
- **状态**：生效

### D58 — 帧提交次序反转 = 绘制先提交，屏幕态与效果层随后同拍落地

- **动机/实测**：展开动画**运动最快的段落**，胶囊/右钮右端出现**黑月牙**（内容边缘外露底色、外侧另有一条羽化环），而定格在完全相同的几何位置时画面**干净**（实心 → 4px 羽化 → 4px 黑 → 圆键）→ 排除几何算错，是时序；逐帧反推得「内容 + 形裁在第 N−1 帧、overlay 在第 N 帧」，错位 ≈8px 且仅 2/8 动画帧可见（错位量 = 速度 × 时间差，慢速段 < 羽化宽本就不可见）。根因（Gio v0.10.2 源码 + MSDN 实证）：① `processFrame` **先 ack 后 `Present(1,0)`**（阻塞到下一 vblank），ack 之后经 `Window.Run` 排队的移窗/`LWA_ALPHA`/形裁/ULW 只能等 Present 返回才被窗口线程服务；② 主窗是 **bitblt 交换模型**（`DXGI_SWAP_EFFECT_DISCARD`、`BufferCount=1`）——Present 把 back buffer 拷进 redirection surface，DWM 要到**拷贝之后的下一次合成**才渲染它。于是：屏幕态排在 `e.Frame` **之前**（D55 旧序）= 形裁/羽化环先上屏、内容等下一次合成 → 不匹配窗口暴露「**旧内容 + 新形裁/新环**」= 内容边缘外露底色（黑缝）+ 外侧新环（月牙），与实测像素逐点吻合；排在 **之后**（D58）= 内容拷贝完立即落地 → 三者并入同一次合成，即便跨过 latch 也只剩「新内容 + 旧形裁/旧环」，而旧形裁与旧环是**同帧产物、彼此一致**（内容被裁掉的羽化宽恰由旧环补上）→ 视觉连续
- **决策**：（修订 D55 的次序段，§15.1/§15.5/§15.6；**次序段后被 D59 再改判为混合次序**——`fadePresent` 留在 `e.Frame` 之后、`commitWinGeom` 回之前）：帧次序固定为 **`stepExpand → stepAnim → layout → fadeCompose → submit(e.Frame) → commitWinGeom → fadePresent`**——D55 的另两半（`fadeCompose`/`fadePresent` 拆分、两套进度提到 `layout` 之前）不动，只把「屏幕态 + overlay」整体挪到 `e.Frame` 之后；`commitWinGeom` 与 `fadePresent` 仍紧邻成对（形裁与环必须同拍）。次序契约测试改为断言 `compose → submit → commit → present`，并断言 submit 时屏幕态尚未 flush、帧尾已 flush（**两项断言后被 D59 改判**：现断言 `compose → commit → submit → present`、submit 时屏幕态已 flush；根因分析与 `fadeCompose`/`fadePresent` 拆分仍有效）
- **否决**：保持 D55 旧序（伪影形态与该序推得的「旧内容 + 新形裁/新环」逐点吻合，即实测根因）；给 overlay 换 DirectComposition 或并入单窗（D45/D46 已否决）；靠放慢动画让错位量 < 羽化宽规避（掩盖而非修复，快滚/快拖仍会露）
- **状态**：部分被 D59 改判（次序机制整体被 D62 退役）

### D59 — 帧提交次序再改判 = 形裁回 `e.Frame` 之前、overlay 留在之后（混合次序，修订 D58 的次序段，§15.1/§15.5/§15.6）

- **动机/实测**：D58 后月牙与出框已消失，但**运动最快段**残留三件套（实测截图像素）：① 胶囊体内 14px **黑沙漏**（上/下排有、中排无：`x=[485,498]` 黑）；② 胶囊右端多出一个**同色灰圆盘**（`x=[499,545]` 与胶囊同为 #e7e7e9）；③ 发送键只剩**空心蓝环**、内部露台布（内容蓝盘整块被裁、只剩不受形裁约束的 overlay 环）。三者同源 = **形裁慢内容一帧**：胶囊体内的洞只能来自「region 的 pill 形与 send 形停在上一帧位置」——两形之间的缝露台布；旧 region 的 send 形窗口把新胶囊白底放行 = 灰圆盘；内容的新 send 盘不在旧 region 覆盖内被整块裁掉 = 空心键。四次观察（D55 序 × D58 序，各两通道）用一个模型全解释——**两类机制的采样点不同**：`SetWindowRgn` 的形状要到**下一次 Present** 才被 DWM 采样（排在 Present **前** = 与该 Present 的内容同拍；排在**后** = 慢一帧），`UpdateLayeredWindow` **立即**在下一次合成生效（排在 Present **后** = 与刚拷贝完的内容同拍；排在**前** = 提前于内容上屏）。故 D55 序 = 形裁同步 ✓ / ULW 超前 ✗（月牙），D58 序 = ULW 同步 ✓ / 形裁慢一帧 ✗（三件套）——**各对一半、各错一半**
- **决策**：帧次序固定为 **`stepExpand → stepAnim → layout → fadeCompose → commitWinGeom → submit(e.Frame) → fadePresent`**（窗口态取 D55 位、overlay 取 D58 位，各取对的一半；移窗/`LWA_ALPHA` 与形裁同拍提交——二者不产生形状-内容错位，D55 期无实测异议）。次序契约测试改断言 `compose → commit → submit → present`，并断言 submit 时屏幕态**已** flush
- **否决**：整体退回 D55 旧序（ULW 超前 = 黑缝 + 月牙，实测）；整体留在 D58 序（形裁慢一帧 = 三件套，实测）；同帧内把形裁写两次补延迟（`SetWindowRgn` 只有"下一次 Present"一次采样，多写无效且多一次窗口线程往返）；继续按帧挪次序试错（同一次序实测两种结果 = 竞态非次序，三轮迭代后止损）；给 overlay 换 DirectComposition 或并入单窗（D45/D46 已否决）
- **验证**：验证记录与已知缺陷（三轮截图迭代后止损）：落地后实测三件套、月牙、出框全部消失（发送键实心带箭头、胶囊连续）；**残余 = 最快段的亚帧级通道错位**——形裁/overlay 偶发慢内容约 1 帧（错位量 = 速度 × 1 帧时，慢速段 < 羽化宽不可见），表现为胶囊右端 3–5px 白/青细环横穿、峰值帧旧 send 形窗口放行新胶囊白底（发送键左半盖白块）。同一次序在不同帧表现不一致（一组截图形裁同步、另一组慢一帧）→ 根因是**窗口线程队列与 ack→Present 的交错竞态**而非固定次序，继续挪次序治不净；根治需 DirectComposition/单窗（D45/D46 已否决）→ 记为已知限制，不再迭代
- **状态**：被 D62 收口（通道采样点分析与混合次序随之作废——形裁/overlay 双通道退役、单通道 ULW 根除亚帧错位，见 D62）

### D60 — 三类功能窗 = 独立常规 OS 窗口（窗口管理，§15.7）

- **动机**：设置表单、会话列表、首次引导需要独立尺寸/生命周期/任务栏语义，而悬浮球是无边框置顶形裁窗（§15.1），塞进主窗既无常规窗体验、也永远验证不了多窗宿主；「常规窗口形态只预留设计」卡的是**悬浮球本体**转常规窗，不是功能窗——功能窗用常规窗不违背 D43/§14。独立 Gio `app.Window` 复用 material 表单生态，纯 Go 无 cgo 照旧。
- **决策**：设置、会话历史、欢迎（首次运行）三窗各起独立 Gio `app.Window`（`Decorated(true)` 常规窗），**不接**主窗专属机制（形裁 `SetWindowRgn`、`LWA_ALPHA`、淡出 overlay、位置记忆、置顶、`win32Run` 单槽、`WM_CLOSE→隐藏` 子类化）；每窗独立事件循环自持状态，跨窗只经消息（`uiMsg` 模式）与线程安全回调（`Options` 下发）；次窗关闭 = 真关闭、单实例防重开、退出经托盘收编全部次窗（§15.7）。本轮 = 基建 + 设置窗（核心档）+ 两空窗壳验证宿主；欢迎接管首次运行流、会话历史数据面（依赖未来切会话命令）留后续步。
- **否决**：
  - **主窗内浮层面板**——复用形裁/overlay 机制改动最小、最贴悬浮球范式，但窗仍是无边框置顶无任务栏，设置表单体验差，且永远做不了常规窗语义（等于把 §14 的「常规窗口形态」继续欠着）；
  - **裸 Win32 二窗**——丢 Gio 渲染与 material 控件，自造布局/输入法轮子，违背纯 Go 依赖优先；
  - **WebView 面板**——引入运行时依赖，破坏单二进制（无 cgo）交付。
- **状态**：生效

### D61 — 主题令牌系统先行于设置窗（§15.4 实施口径）

- **动机**：核查发现 uigui 只有单套固定主题（`newTheme` 仅装 CJK 字体，无深浅双色板、无系统深浅检测），M5 口径的「深/浅跟随系统」（§15.4/§12）**尚未实现**；设置窗的主题档依赖它——先落 `ui.theme` 键后做系统会留下置灰死档，反之先做主题、主界面全量接入后设置档才有可切对象。
- **决策**：设置窗落地前先做 §15.4 实施口径：**颜色令牌**深浅两版纯数据预设（背景/文字/气泡双色/思考暗块/状态行/错误色/禁用态等），主界面自绘路径与 material 主题**同读令牌**、主界面一并接入；几何令牌（圆角/间距/字号）只做结构、不随主题变体；`ui.theme = system | light | dark`（键缺失 = `system`，§8），`system` 经系统深浅检测（已定实现口径：Win32 `AppsUseLightTheme` 注册表 + 主窗 `WM_SETTINGCHANGE` 广播刷新；未识别主题值亦按 `system`），切换热生效；设置 v1 核心档 = 模型/权限/think/effort/`ui.hotkey`/`ui.theme`，密钥只写不回显（D35）。
- **否决**：
  - **设置 v1 砍掉主题**——「深/浅跟随系统」本就在 M5 验收里，欠账越滚越大；
  - **`ui.theme` 键先落、UI 置灰占位**——置灰死档比没有更困惑，且键一旦发布难撤；
  - **连多颜色预设一起做**——§14 明确后补，范围失控；
  - **主题项留空窗壳内后补**——主界面不接入则设置改了也不生效，自欺验收。
- **状态**：生效

### D62 — 整窗 ULW 换壳 = 单通道像素根治亚帧通道错位（形裁/LWA/overlay 退役，D46–D48 保留选项启用，§15.1/§15.5/§15.6）

- **动机/实测**：D59 落地后残余伪影仍在（`temp/anim-bug/final/z1–z3`：胶囊右端旧 send 形灰盘残影 + 青色羽化环横穿 + 发送键左半白块），且同一次序不同帧表现不一致。源码复核 Gio v0.10.2（app/window.go `validateAndProcess`）：`e.Frame` 在 `queue.Frame` 之后、`Present(1,0)`（阻塞到 vblank）**之前**先 signal 放行客户端——此后经 `Window.Run` 排队的 Win32 调用与 Present 拷贝、DWM 合成 latch 的交错点不受代码次序控制。主窗内容（bitblt swapchain）、形裁（`SetWindowRgn`）、效果层（overlay `UpdateLayeredWindow`）**三通道并存，亚帧错位即结构必然**，挪次序已被三轮截图迭代判死（D58/D59 验证记录）。
- **决策**：启用 D46/D47/D48 三次保留的后续选项**「整窗 ULW 换壳」**，像素出口收敛为**单通道**：① 每帧 headless 同布局重渲（D44 底座不动）升级为**唯一像素源**——内容 + 淡出带 + 元素羽化按 `av = vis(形状) × g(y) × 内容alpha` 一次合成为整窗预乘 BGRA 位图（`fadeFrame`；vis = 形状集覆盖度：核心 1 / 边带 smoothstep 渐隐 / 轮廓外 0，SDF 与剖面常数沿用 D45–D48，region 内缩/方顶续接机制随形裁失去承载对象而退役）；② 位图经 `UpdateLayeredWindow(ULW_ALPHA+AC_SRC_ALPHA)` 直提**主窗 HWND**（主窗本就 `WS_EX_LAYERED`），`SourceConstantAlpha = u.alpha` 取代 `LWA_ALPHA`（D50 停靠淡化同帧跟随语义不变）；③ **形裁整条退役**：位图 alpha 即形状（自带抗锯齿）、即点击穿透（分层窗逐像素命中：alpha=0 间隙穿透到任意下层窗口，语义等价旧形裁；羽化边带 alpha>0 变为可命中——比旧 region 内缩 fw+0.5 的「边带死区」更贴近视觉）；④ overlay 独立窗口整体退役（少一窗、少一套 z 序/置顶/显隐同步）；⑤ `e.Frame` 与 Gio GPU 路径保留（事件路由/IME/光标/vblank 节奏零改动，其画面被 ULW 位图覆盖）；帧次序 `compose(全帧合成) → commit(移窗) → submit(e.Frame) → present(ULW)`——单通道后「按通道分边」（D59/铁律 3）整体作废，移窗先于 ULW 仅为定位取实测矩形。降级：headless 失败 → 元素底色兜底（`writePremulFill`，形状可见可点、无文字）。
- **否决**：参数缓解（加宽羽化/降速掩盖——D58 已否决「掩盖而非修复」，快滚/快拖仍露）；DirectComposition 接管（D44 已否决：放弃 Gio 渲染 = 换架构，违背「换壳不换核」）；独立全帧 overlay 窗（内容/效果仍双通道，错位照旧）；`WM_NCHITTEST` 补穿透（`HTTRANSPARENT` 只对同线程窗口生效，跨线程穿透只能靠位图 alpha，无增量）；`app.CustomRenderer(true)` 关 GPU 路径（省一遍渲染 + vblank 阻塞，但改 Gio 驱动语义面大——留作后续独立优化，先零改动落地）。
- **后果**：① 启动首帧前分层窗不显示（首次 ULW 即现）——优于旧「白底闪现」；② 点击语义变化如上（间隙穿透不变、羽化边带由穿透变可命中）；③ `e.Frame` 的 swapchain 渲染成为不可见冗余（与今天同成本；headless 全帧本就每帧在跑）；④ D55/D58/D59 的通道采样点分析与混合次序成为历史（机制退役），测试契约改断言 `compose → commit → submit → present` 且 commit 时移窗已 flush。
- **状态**：生效


### D63 — 转写文本可选可复制 = 行级 `widget.Selectable` + 拖层把手带收窄 + 编辑器焦点收口（§15.3）

- **动机/实测**：转写区文本为自绘（`material` Label 录宏重放），不可选、不可复制（用户实测反馈 + 截图）；会话内容复制是 IM 形态刚需。§15.3 此前只设计了 markdown 代码块「复制按钮」（未实现），文本行级选择无设计——补决策。（首版实现两处交互缺陷实测：悬浮出 I-beam 却选不中/复制不了——见决策段 ③④。）
- **决策**：转写文本行挂 Gio `widget.Selectable`（`material.LabelStyle.State`，v0.10.2 内建）：**鼠标拖选 / 双击选词 / 三击整行 / Ctrl+C 复制选区（`clipboard.WriteCmd` → 系统剪贴板）/ Ctrl+A 行内全选**；选区高亮用 LabelStyle 默认 `SelectionColor`（主题 ContrastBg 半透明）。选择状态按**行序号**缓存于 UI（`selRows` 切片，get-or-create）：行文本变化经 `SetText` 幂等更新并自动清选区——流式行每帧变化、会话切换内容更替走同一条路径，无需显式失效。适用行 = 用户/助手/普通/notice/错误/系统/思考正文/工具 chip 文本行；思考头部（元信息）与状态行不选。配套两处交互收口（首版缺陷的根修）：③ **拖层把手带收窄**——整窗背景拖层改为只注册**状态行 + 输入栏带**（`[transH, 窗底)`），转写区不注册拖层：`gesture.Drag` 超出 slop 即 `pointer.GrabCmd`（gesture.go 实证）且**先到先得**，拖层覆盖气泡时会在行选 dragger 之前抢走 Drag/Release（实测「悬浮出 I-beam 却选不中」的根因）；收窄后气泡区指针归行选独占，行间隙本就随位图 alpha=0 穿透。④ **编辑器焦点收口**——废除每帧无条件回投 `FocusCmd{Tag: &u.editor}` 的「常驻焦点」（与行获焦同帧竞态：`Selectable.Focused()` 滞后一帧，编辑器把刚点选行的焦点抢回 → 选区隐没、Ctrl+C 失效）；焦点来源收口为 `focusPending`（呼出/展开，anim.go 既有）、编辑器自带点击取焦、newUI 初始焦点。
- **否决**：整块自造选区渲染（hit 测试/位移/取词/选区矩形轮子，Selectable 已内建且与 IME 键盘导航同源）；点击气泡即复制全文（与「选择」诉求不符、无操作反馈位）；拖层内按行矩形过滤按下（`posInRow` 首版尝试——只挡住 `beginDrag`，挡不住 gesture.Drag 内部的 grab 抢占，治标不治本）；行级状态按块 ID 键控（model 块无稳定 ID，序号 + SetText 幂等已覆盖全部失效场景，不为此引入标识符）。
- **后果/限制**：① 流式行文本变化即清该行选区（内容变了选择失效，合理）；② 工具 chip 未来落地折叠点击（§15.3）时需与行选手势调和（同区双手势）；③ markdown 行内富样式落地后按 span 细分选择留后续；④ 选区高亮绘制进 fade 位图（状态在 widget 上，两遍渲染同帧同值——与 D62 单通道兼容）；⑤ 转写区空白（含行间隙）不再拖窗——对齐 §15.3「气泡区只滚不拖窗」原意（位图透明处本就穿透，此前仅气泡不透明像素上可拖，属未察觉的设计偏差）。
- **状态**：生效

### D64 — GUI 退出 = 立即中断进行中轮次（不等待 Agent 完成，§15.1）

- **动机/实测**：托盘「退出」只 `signalEOF`——而装配根此刻阻塞在 `sess.Handle`（Turn 内），`Next` 的 EOF 要等本轮自然结束才被看到 → 退出悬挂数十秒（用户实测：Agent 工作中点退出，窗口隐藏但进程滞留）。设计意图 = **退出即终止**。
- **决策**：`exitViaShell` 在 `signalEOF` **之前**调用 `interruptNow()`（复用停止键的每轮 `hcancel` 注入，§10 取消语义）：`hctx` 取消 → 流式/工具立即解卷 → 根循环回到 `Next` → 见 EOF → 正常收尾退出（退出码 0）。工具确认挂起场景无需额外处理（`Confirm` 的 `eofCh` 广播本就立即解卷）。`interruptNow` 无轮进行时为 no-op（`SetInterrupt(nil)` 存 nop），空闲退出不受影响。
- **否决**：等 Turn 完成再退（即本 bug）；绕过装配根强杀/直接 return（跳过 `ui.Close` 收尾——事件循环滞留、退出码错乱）；改为取消外层 signal ctx（会连带取消 `ReplayHistory` 等外层路径且改变退出码语义，单轮取消已足够解卷）。
- **状态**：生效

### D65 — 助手正文 markdown 渲染 = goldmark 定稿解析为结构块、行级展开（§15.3 首增量）

- **动机**：§15.3 设计目标「assistant 正文 = 完整 markdown」至今未落地（`model.go` 骨架注释「渲染期 markdown，先纯文本」）；模型回复大量使用 markdown（代码块/列表/标题），GUI 呈现为原文符号（`**`、``` 围栏原样可见），可读性差（用户提出）。
- **决策**：① **解析**：`github.com/yuin/goldmark`（已在依赖树——TUI glamour 的传递依赖，随本决策升为直接依赖；纯 Go、CommonMark 规范、自身零依赖）Parse→AST **顶层遍历**为中间块 `mdBlock{kind, text, level, order, lang}`：段落 / 标题(1–6) / 围栏+缩进代码块(带语言标签) / 列表项(无序 `•`、有序编号、按嵌套深度缩进) / 引用 / 分隔线；行内内容**剥标记保文本**（emphasis/链接取文字、图片取 alt、code span 去反引号、软换行保 `\n`、转义还原）。② **渲染**：`frameItems` 渲染期把定稿 `blockAssistant` 块展开为多个 `blockView`——每块一行，D63 行选/滚动手势/淡出合成（D62 单通道）等全部行机制**零改动**复用；展开结果按原文缓存于 UI（上限 256 条防长会话膨胀，超限整表重建）。行样式：段落 = 助手气泡原样；标题 = 同气泡大字（h1 20sp → h3 15sp，Weight 求粗，CJK 粗体面缺省时回落常规）；代码块 = Go Mono 等宽 + 深底卡（theme 集合补 Go Mono 面，typesetting FontMap 对 CJK 缺字自动回落）；列表/引用 = 前缀文本（`• ` / `1. ` / `▏ `）；分隔线 = 弱化短行。③ **口径**：仅定稿 `blockAssistant` 文本参与解析——live 草稿不解析（对齐 D33「流式原样、定稿渲染」，TUI glamour 同款）；用户输入/思考/工具行/系统行原样。解析异常回退单行原文（对齐 TUI「失败回退原文」）。
- **否决**：glamour 直用（ANSI 终端输出，Gio 不适用——TUI 专用）；hand-rolled 解析器（CommonMark 边角是 bug 农场，goldmark 已在树上）；gioui.org/x richtext（行内富样式的正解，但替换 D63 行选体系、引入 Gio 版本耦合——留作后续独立增量，届时按 span 细分选择即 D63 后果③）；表格/GFM 扩展（不启用扩展时管道表以原文段落呈现、可读；表格组件渲染留后续）；行内图片（文本面无图源，§4.2 占位符已覆盖）。
- **后果/限制**：① 流式阶段原文可见、`CommittedEvent` 后跳变为格式化（TUI 同款）；② 一条消息拆多行 → `selRows` 行序缓存条目更多（既有机制，无行为变化）；③ 语法高亮、代码块复制按钮、表格、链接点击、行内富样式（bold/italic 视觉化）与行内图片 = §15.3 表中目标的**后续增量**（本决策只落结构块）；④ 解析失败不致命（回退原文）。
- **状态**：部分被 D66 改判（呈现粒度「行级展开」改为「消息级复合行」——解析/口径/字体/缓存与后续增量清单不变，见 D66）

### D66 — 单回复单气泡 = 消息级复合行（块不拆行、共底板，修订 D65 呈现粒度，§15.3）

- **动机/实测**：D65 落地后一条助手消息按块拆为多行，每行经 `measureRow/paintRow` 各绘独立底板——多块消息（段落+列表+代码）呈「气泡堆叠」（用户实测反馈：一次回复应在同一个气泡里）。
- **决策**：呈现粒度从块收回**消息**——助手消息仍是 transcript 的**一行**，行内容 = 垂直 Flex 的多块复合 widget，共用一个 `pillBg` 气泡底板（思考块「头部+正文」共底板为既有先例）：① `frameItems` 对定稿助手块产出**单条目**携预解析 `md []mdBlock`（live 草稿 `md==nil` 单气泡原样，D33 口径不变）；② 块分派在 `rowStyle` 内：段落/列表项 = Body2、标题 = 大字求粗、引用 = 暗色 Body2、代码块 = 等宽 `cardTool` 小卡（录宏→画底→重放，同 `paintRow` 次序，卡在气泡内嵌套）、分隔线 = 暗点行；块间 Spacer 4dp；③ 行选挂接改**双键**——`measureRow/rowStyle` 的单 `sel` 参数改 `sels func(int) *widget.Selectable`，transcript 按条目 `selCount()`（助手 = 块数，其余 = 1）累计线性序号，`sels(k) = selFor(base+k)`：`selRows` 缓存结构与 SetText 清选区语义不变；定稿块只增不改 → 既有消息序号 base 稳定（漂移只发生在尾部 live 思考/草稿，瞬时态无选择价值）。D65 的 `blockHeading/blockCode/blockRule` 行种类随拆行机制退役（blockKind 不为 markdown 膨胀）。
- **否决**：组级布局（transcript 主循环按消息分组——动间隙/量高/绘制三处主循环，相邻底板矩形拼接 per-corner RRect 且内部边羽化要靠 painter's-algorithm 覆盖缝合，滚动贴底主循环是 D56/D62 精调过的敏感路径）；继续多气泡（即用户反馈的问题本身）；richtext 整块替换（行内富样式正解但不解决气泡粒度，仍留后续）。
- **后果/限制**：① 气泡宽 = 消息内最宽块（贴满宽块后短段落左侧对齐留白——标准聊天气泡形态）；② `record()` 每消息一个矩形（D65 实现期为每块一个），fade 形状数下降；③ 块内选择仍是逐块 Selectable（跨块拖选高亮分段，与拆行期等价）；④ 单块消息（纯文本回复）渲染不变。
- **状态**：生效

### D67 — 工具调用单气泡 = 调用/确认/结果合并 chip，默认折叠点击展开（§15.3）

- **动机/实测**：一次工具调用在转写里现呈 **4 条独立行**（调用 chip + 权限问句 plain + 应答 plain + 结果 chip），噪音大且参数/结果只留 120 字预览；§15.3 设计目标「折叠 chip（`🔧 file_read config.json ✓`）点击展开参数/结果明细」一直未落地（用户提出：合并 + 默认折叠 + 点击展开）。
- **决策**：① **合并**：`block` 增 `chip *toolChip`（id/name/args/confirmQ/confirmA/done/ok/result）——`ToolCallEvent` 开 chip；`ToolResultEvent` 按 `Result.CallID` 回填（实时与回放同路径；找不到则新开结果 chip 兜底）；权限确认按「最新未完成 chip 且问句含工具名」归属（`confirmPrompt` 含 `call.Name`；/rm 等非工具确认仍走 plain 行），确认问句与应答写入 chip、不再加 plain 行；commit 时仍未完成的 chip 标记「未返回——本轮已结束」（取消/异常收尾没有结果事件）。② **折叠**：默认只渲染头部行 `▸ 🔧 name · 参数预览 · 状态`，状态机 `…`（运行中）/`待确认`/`✓`/`✗`；点击头部切换展开（`widget.Clickable` 按块序缓存于 UI，块只增不减 → 序稳定）；展开体 = 参数（等宽全文）+ 权限问答（暗色 caption）+ 结果全文——**全文不再截 120 字**，且可选可复制。③ **可见性特例**：待确认 chip **强制展开**渲染（折叠是视图态，权限问句不可折叠隐藏）；应答后回落用户上次的开合选择。④ 头部为点击热区、不再挂行选（D63 的「工具 chip 文本行可选」收窄为展开体内容可选）；行选键数恒 2（参数/结果各一）——开合切换不漂移后续行的选择序号。
- **否决**：FIFO 配对（`CallID` 现成，FIFO 在并行调用交错时错配）；折叠态仍渲染全文（单行噪音不减，违背折叠目的）；权限确认保留独立 plain 行（即合并诉求本体）；头部挂行选（与点击手势打架，头部是元信息）；uitui 同步改（终端无点击折叠收益，§15.5 对齐的是事件面而非呈现层）。
- **后果/限制**：① 展开体长结果（≤ `tool_output_chars`）一次成型无内部分段/滚动，超长 chip 很高——后续增量；② 展开状态按块序缓存在 UI（视图态不进模型，会话重建后复位折叠）；③ 头部参数预览仅首行截断，完整参数看展开体。
- **状态**：生效
