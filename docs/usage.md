# 使用手册

> 本文衍生自 [DESIGN.md](../DESIGN.md) §7.1/§7.3/§9，并与当前实现对齐；
> **冲突以 DESIGN.md 为准**。配置字段见 [configuration.md](configuration.md)，数据与备份见 [storage.md](storage.md)。

## 快速上手

```bash
go build ./cmd/aquarius
./aquarius.exe        # 首次运行生成 ~/.aquarius/config.json 后退出
# 填 model.name / model.base_url，设置密钥环境变量，重新运行
```

默认 **TUI**（`ui.kind=tui`，D33）：转写区 + 输入框 + 状态行（模型/权限/用量），
上下键历史、PgUp/PgDn 滚动转写、Ctrl+C 取消当前生成、committed 回答带轻 markdown 渲染；
`ui.kind=repl` 为行式 REPL（测试/e2e 后端）。直接输入文本即对话；`/help` 看命令；
`/quit`（或 `/exit`）退出。
（Ctrl+C 语义：TUI 取消本轮后保持待命；repl 取消本轮后进程随即退出。）

## 命令参考

| 命令 | 说明 |
|---|---|
| `/new [标题]` | 新建会话（立即落盘；首条消息后默认标题自动改为消息摘要） |
| `/list` | 列出会话：最新在前，当前会话行首标 `*`，`N条` 为消息数（不含 Root） |
| `/title` | 无参：显示当前标题；有参：改写标题并即时落盘（会话元数据，不动消息树） |
| `/goto <id>` | 回溯到任意节点并把它设为新起点（支持唯一前缀；输入会话 ID 或其前缀回到根） |
| `/edit <id> [--keep] <文本>` | 修改某节点：缺省 **Fresh** 另起同级新节点（原分支保留）；`--keep`（须紧跟 id）**Carry** 把该节点的后续历史边转移过来 |
| `/branch [id]` | 分支视图：该节点自身 + **同级分叉**（新旧版本对比）+ **下级**，Root 显示顶层消息；缺省当前起点 |
| `/rm <id>` | 删除节点及其后代（Root 不可删；二次确认后执行，剪枝即落盘，落盘失败自动回滚；`-yes` 启动可跳过确认） |
| `/compact` | 触发上下文压缩（见下节） |
| `/permission [等级]` | 无参：当前等级 + 免确认矩阵 + 特权目录；有参：切换等级并写回 config，工具链路即时生效 |
| `/memory [会话id]` | 用系统编辑器打开记忆文件（缺省全局 `memories.md`，带参会话记忆；保存后下次读取生效） |
| `/usage` | 用量查看：当前上下文占用（精确/≈估算）、上轮实测 prompt/completion、会话累计 |
| `/jobs [list\|logs <id> [行数]\|kill <id>]` | 后台任务管理（M3）：缺省 `list`；`logs` 取末尾行（缺省 50）；`kill` 终止（连同子进程）。任务由模型经 `job_start` 启动，ID 支持唯一前缀 |
| `/model [name]` | 无参：当前模型 + 可用清单（`LLM.Models()`，能力/单价标注）；有参：先写回 config `model.name` 再 Agent 内热切换（D32，同 `/permission` 模式），下一轮即生效 |
| `/think [on\|off]` | 无参：原生思考开关（缺省开）；有参：切换并写回 config `model.think`（D34）。**总开关**：off 时 `reasoning_effort` 与 `enable_thinking` 一律不发（覆盖 `/effort`） |
| `/effort [级别]` | 无参：当前 `reasoning_effort` 档位；有参：`minimal\|low\|medium\|high\|off`（off 清除）写回 config `model.reasoning_effort`。是否真发由 `/think` 决定；on 且未设档 = 不发（交服务端默认） |
| `/plugin [list\|enable <name>\|disable <name>]` | MCP 插件管理（M4，D31）：list 显示状态/来源/传输/能力授权/调用统计/重启与报因，并列出可用动态命令；enable 走 capability 首用确认并写回 config；disable 即时摘除其工具与命令 |
| `/mcp:<server>:<prompt> [args]` | MCP prompts 动态命令（随插件启停注册/注销，`/plugin list` 查看可用项）；渲染结果作为用户消息走完整一轮 |
| `/quit` `/exit` | 退出（`/exit` 为别名） |
| `/help` | 命令帮助 |

> 节点 ID 支持唯一前缀匹配；自身、同级与下级节点 ID 可用 `/branch` 查看（逐层下钻可达任意深度）。
> "修改"永不改写旧消息：Fresh 另起节点、Carry 边转移（后代本身不变），会话树始终保持不可变。

## 内置工具（M2/M3 起，模型自行调用）

模型可在回答过程中调用工具；**执行前一律过 ToolRunner**：权限矩阵判定 → 矩阵外逐次
`[y/N]` 确认（`-yes` 全免）→ 单次超时（`limits.tool_timeout_sec`）→ 结果按
`limits.tool_output_chars` 裁剪。拒绝/超时/工具自身失败都以 `OK=false` 回填给模型，
不中断对话；**基础设施故障**（确认器缺失/报错、Ctrl+C 取消）则快速中止本轮，不空转。

| 工具 | 说明 | Risk |
|---|---|---|
| `memory_list` / `memory_read` / `memory_search` | 记忆索引/读取/关键词检索（只见全局 + 当前会话两份） | Safe |
| `memory_write` | 写入记忆（`mode=append` 缺省 / `overwrite`；strict 下路径格外需确认） | Confirm |
| `file_read` / `file_list` / `file_search` | 读文件 / 列目录 / 按文件名递归搜索（**绝对路径**，读全盘免确认） | Safe |
| `file_write` / `file_delete` | 覆盖写 / 删除（写按权限矩阵路径格判定） | Confirm |
| `think` | 显式整理思路（no-op，内容随调用入树） | Safe |
| `context_compact` | 触发上下文压缩（等价 `/compact`，自我管理上下文） | Safe |
| `term_exec` | 同步执行终端命令行（`cmd /c` / `sh -c`；超时统一控制，输出保头尾截断；执行类看工具列） | Confirm |
| `job_start` | 启动后台任务（独立进程，日志落盘 `~/.aquarius/jobs/<id>.log`，不随对话取消） | Confirm |
| `job_list` / `job_status` / `job_logs` / `job_kill` | 后台任务管理（列表/状态/日志尾部/终止） | Safe |

## 上下文压缩（三轨特色功能）

会话树不可变，压缩不删历史——它生成一条 **system 摘要节点**作为"水位"：此后装配
`[persona] + [最新摘要] + [摘要之后]`，摘要之上（persona 除外）的历史不再发给模型。
被裁掉的原文仍在树上，随时可回溯（`/goto` 回到压缩节点之前即可"撤销"压缩效果）。

- **手动轨（已可用）**：`/compact`
  - 摘要之上没有新历史 → 提示"无需压缩"，不花生成费用；
  - 成功 → `已压缩 N 条历史 → 1 条摘要（in=X out=Y tokens）`；
  - 摘要按**英文结构化模板**生成（Objective/Requirements/Decisions/Work State/
    Next Move/Relevant Files/Important Context，对任意 agent 通用接手），
    已有旧摘要时合并更新（新历史优先）；输出缺小节带提醒重试一次，仍不合格按失败处理；
  - 取消/失败 → 会话树无损，可重试。
  - 多次压缩链式吸收：新摘要把旧摘要一并吞掉，上下文永远只带最新一条。
- **自动轨（M2 起可用）**：每轮生成前按**三级 token 计数链**估算上下文
  （①已发生的服务端实测 usage 自校准 → ②适配器本地 tokenizer 精确计数，
  配置 `model.tokenizer` 即启用 → ③通用估算 ASCII÷4 / CJK÷1.5 / 其他÷2），
  达 `limits.compact_threshold`（默认 0.7 × `max_context_tokens`）自动压缩——
  每次 Run（单条输入触发的整轮工具循环）至多一次；失败回退"最旧裁剪"
  （保人格与最近、丢中间，`[notice]` 提示省略条数，绝不留下孤立 tool 消息）。
- **自触发轨（M2 起可用）**：模型经 `context_compact` 工具（Safe）自我管理上下文。

persona（人格）恒回传：它是会话首节点（system 角色），源自 config 的 `system_prompt`
（空则用内置默认），可用 `/edit <persona-id> --keep <文本>` 改写（Carry 保留整棵对话树）。

## MCP 插件（M4）

Tier-2 = 任意 MCP server（stdio 或 streamable HTTP 双传输，D30 官方 go-sdk）：

- **声明**在 config `mcpServers.<name>`（stdio：`command`/`args`/`env`；http：`url`/`headers`，
  值可用 `secret:<环境变量名>` 引用）或 `~/.aquarius/plugins/<name>/plugin.json`；
  **启停与授权状态**在 config `plugins.<name>`（D31，同名声明以 config 为准）。
- **授权**：声明的 `capabilities` 首次启用逐项 `[y/N]` 确认，通过即写入 `granted`；
  未配置确认器时 fail-closed 不启动；`risk=confirm` 的工具仍逐次确认（不随授权放行）。
- **工具**：`tools/list` 注册为 `mcp:<server>:<tool>`，与内置工具同权过 ToolRunner 权限矩阵；
  server 崩溃自动重启（1s/2s/4s 退避、限 3 次），超限置 `crashed`；重启与调用统计见 `/plugin`。
- **resources** 只读并入记忆索引与 `memory_*` 读取面（写/删仍走自有记忆，§6.3）；
  **prompts** 暴露为 `/mcp:<server>:<prompt>` 动态命令。
- `/plugin list` 看状态，`enable`/`disable` 即时启停（写回 config，重启沿用）。

## 权限等级

四档预设，`sandbox`（`~/.aquarius/sandbox`，启动自动创建）是 Agent 特权目录。
矩阵格 = **免确认范围**，矩阵外一律逐次确认：

| 等级 | 特权目录 | 其他目录 | 工具调用 |
|---|---|---|---|
| `read-only` | r | r | Confirm 逐次 |
| `strict`（默认） | **rw** | r | Confirm 逐次 |
| `permissive` | **rw** | **rw** | Confirm 逐次 |
| `full-access` | **rw** | **rw** | 全免 |

- 两列分工不叠加：**文件类**只看路径格（读全盘免确认；写看该格是否 rw）；
  **执行类**（term_exec 等）只看工具列（Safe 免、Confirm 仅 full-access 免）。
- `/permission permissive` 切换后写回 config（保留其余键），下次启动沿用；
  **切换即时生效**——ToolRunner 每次执行现读等级。
- **执行接入（M2 起）**：权限判定在 ToolRunner 内、工具执行前完成；
  `network`/`secret` 属插件与密钥授权流，不随等级变化。

## 常见问题

- **启动报"发现旧格式会话（虚拟 Root，D19）"**：M0 早期数据不兼容，按报因点名的
  ID 删除 `~/.aquarius/conversations/<id>.json`（含 `.bak`）后重试，详见 [storage.md](storage.md)。
- **启动警告"model.api_key 为明文"**（D35）：明文密钥直接写在 config.json 里，任何能读
  该文件的进程都可取用——建议改为 `"api_key": "secret:AQUARIUS_OPENAI_KEY"` + 同名环境变量；
  该项留空也会自动回落默认引用（环境变量缺失时才报错退出）。
- **思考链路（D34）**：`/think off` 关闭原生思考（不发 `reasoning_effort`/`enable_thinking`）；
  `/effort high` 只存档位、配合 `/think on` 才发送；`reasoning_effort` 非法值启动即报因
  （`minimal|low|medium|high`）；服务端不认的参数会被自动剥离并记入
  `model.unsupported_params`（要恢复发送就从该数组删掉对应字段）。
- **启动报未知权限等级**：`permissions.level` 只接受
  `read-only|strict|permissive|full-access`。
- **`ui.kind` 报未支持**：只接受 `repl`（行式，测试/e2e 后端）与 `tui`（默认，D33）。
- **想换模型/端点**：改 config 的 `model.name`/`model.base_url`，或用
  `-model`/`-base-url` flag、`AQUARIUS_MODEL`/`AQUARIUS_BASE_URL` 环境变量临时覆盖。
