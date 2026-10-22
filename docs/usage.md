# 参考手册
<!-- docs-sync: 2026-10-22 @ 4bb7226 -->

> **第一次使用请先看[用户手册](guide.md)**——那本按「怎么用起来」写，不要求懂编程；
> 本文是逐条命令与参数的速查，写给要抠细节的人。
>
> 本文衍生自 [DESIGN.md](../DESIGN.md) §4.3/§7.1/§7.3/§7.4/§9，并与当前实现对齐；
> **冲突以 DESIGN.md 为准**。配置字段见 [configuration.md](configuration.md)，数据与备份见 [storage.md](storage.md)。

## 快速上手

```bash
go build ./cmd/aquarius
./aquarius.exe        # 首次运行生成根指针 + profiles/default/config.json 模板后退出（D110）
# 填 model.providers（base_url / models / api_key），设置密钥环境变量，重新运行
```

默认 **GUI 悬浮球**（`ui.kind=gui`，D51/§15：托盘常驻、Alt+A 呼出、不进任务栏）。
输入栏左侧的**回形针**可选文件随消息发出（文件框支持多选，一条消息最多 8 个，见
[用户手册](guide.md) 第 6 节）。
**输入框为空时，右侧圆钮是语音键**（麦克风图标）：点一下开始录音、再点一下结束，
转写文字回填输入框（不自动发送，可先改错字）；录音中圆钮变停止键。有输入时恢复为
发送键。语音在设置窗「语音」页配置（识别/合成各自的服务与模型、音色与风格、试听；
服务与模型改动保存后重启生效），也可直接改 `audio.asr` / `audio.tts`
（见 [configuration.md](configuration.md)）；**识别服务未配置时保持发送键不变**（合成配没配
不影响麦克风——录完要转写，缺的是识别）。
**双击启动不弹控制台**（D108）：仅本次启动独占新建控制台（双击/资源管理器/开始菜单）
时窗口即隐，日志与启动警告不可见；需看日志（复现问题、明文 api_key 警告）请**从终端启动**。
`ui.kind=tui` 起 **TUI**（D33）：转写区 + 输入框 + 状态行（模型/权限/用量），
上下键历史、PgUp/PgDn 滚动转写、Ctrl+C 取消当前生成、committed 回答带轻 markdown 渲染；
`ui.kind=repl` 为行式 REPL（测试/e2e 后端）。TUI/repl 中直接输入文本即对话；`/help` 看命令；
`/quit`（或 `/exit`）退出。
（Ctrl+C 语义：TUI 取消本轮后保持待命；repl 取消本轮后进程随即退出。）

启动会自动恢复最近一次会话，并把该分支**模型可见的历史**（上个压缩摘要 → 最新；
没有摘要则从首条消息起）回放进转写区，附一条 `已恢复会话 <标题> (<id>)，回放 n 条历史`
提示——persona 配置快照与摘要水位之上的旧内容不回放（D40/DESIGN §7.4）。

## 命令参考

| 命令 | 说明 |
|---|---|
| `/new [标题]` | 新建会话（立即落盘；首条消息后默认标题自动改为消息摘要） |
| `/list` | 列出会话：最新在前，当前会话行首标 `*`，`N条` 为消息数（不含 Root） |
| `/title` | 无参：显示当前标题；有参：改写标题并即时落盘（会话元数据，不动消息树） |
| `/goto <id>` | 回溯到任意节点并把它设为新起点（支持唯一前缀；输入会话 ID 或其前缀回到根） |
| `/edit <id> [--keep\|--copy] [--part N] [--attach <路径>]... [--drop N]... [--drop-all] [<文本>]` | 修改某节点：缺省 **Fresh** 另起同级新节点（原分支保留）；`--keep`（须紧跟 id）**Carry** 把该节点的后续历史边转移过来；`--copy`（须紧跟 id）**Clone** 深拷贝后续历史为独立副本（新旧两份互不影响，D96/D97）；`--part N` 只替换第 N 个正文分片（0 起，思考/工具/附件分片原样保留——GUI 气泡编辑自动携带，D107）；`--attach <路径>` 追加附件（可重复，按序接在末尾，须绝对路径）、`--drop N` 移除第 N 个附件（0 起，按附件在消息里的出现序）、`--drop-all` 移除全部附件——三者只影响新修订出的节点，不动附件库，且只对用户消息开放；带附件操作时文本可省 = 只改附件，但改完至少留一段文字或一个附件（D188）；修订后清屏回放新路径（D92）；修订**用户消息**（Fresh）即重新生成回答（D93：编辑即重发）；Carry/Clone 与助手/系统消息修订一律不触发生成（D97） |
| `/regen <id>` | 重新生成（id 可为用户或助手消息）：取上游最近一条用户消息重发并生成新回答——已有回答时另起同级新节点（旧回答保留为历史分支，分叉条可切回）；尚无回答时直接在其上生成（D93） |
| `/respond <id>` | 从所选节点**继续生成一条响应**（id 可为任意**非根**节点，含人格/系统/工具节点）：把它设为起点后跑一轮，发往模型的就是「水位 → 该节点」这段上下文；新回答作为该节点的子节点，已有子节点时开一条新分支（旧分支保留）。会话根不可作为起点 |
| `/inject [--source <名>] [--no-trigger] <文本>` | 手动注入一条上下文（D150）：入树为**注入节点**，默认起一轮响应（`--no-trigger` 只塞进上下文、等下一次输入）；`--source` 标注来源（任意名，缺省按「手动」处理，任务来源默认起响应、工具/插件来源默认只补上下文；它只改展示与触发默认，**不影响这条内容被当作可信宿主事实**——那是你亲手写的）。工具/MCP 走同一内核入口，不经此命令 |
| `/branch [id]` | 分支视图：该节点自身 + **同级分叉**（新旧版本对比）+ **下级**，Root 显示顶层消息；缺省当前起点 |
| `/rm <id>` | 删除节点及其后代（Root 不可删；二次确认后执行，剪枝即落盘，落盘失败自动回滚；`-yes` 启动可跳过确认） |
| `/compact` | 触发上下文压缩（见下节） |
| `/permission [等级]` | 无参：当前等级 + 免确认矩阵 + 特权目录；有参：切换等级并写回 config，工具链路即时生效 |
| `/unattended [on\|off]` | 无人值守：**越界操作当场拒绝并说明**，不再弹确认等人（权限等级本身不变）；无参 = 切换，有参 = 显式设置；写回 config `permissions.unattended` 并热生效。与 `-yes` 相反——那个是自动放行，两者同时出现时 `-yes` 优先 |
| `/memory [会话id]` | 用系统编辑器打开记忆文件（缺省全局 `memories.md`，带参会话记忆；保存后下次读取生效） |
| `/usage` | 用量查看：当前上下文占用（精确/≈估算）、上轮实测 prompt/completion、会话累计 |
| `/jobs [list\|logs <id> [行数]\|kill <id>]` | 后台任务管理（M3）：缺省 `list`；`logs` 取末尾行（缺省 50）；`kill` 终止（连同子进程）。任务由模型经 `job_start` 启动，ID 支持唯一前缀 |
| `/watch [list\|stop <id>]` | 观察器（D190）：模型声明的定时观察（看一条命令的输出、一个文件、一个网页，命中什么就回报）。缺省 `list` 看它在盯什么、已查几次、什么时候结束；`stop` 叫停。声明由模型经 `watch_start` 发起，用户不在这里写探针 |
| `/mic [秒数]` | 录一段话（缺省 60 秒，上限由服务端体积反推）→ 转写 → 作为一条用户输入提交；录音中再触发会提示正在录音。需配好 `audio.asr`，非 Windows 平台明确报不支持 |
| `/model [name]` | 无参：当前 provider + 当前模型 + **生效能力**（工具/思考/图片，整条降级链取与，并指名哪一环在拉低）+ **上下文预算**（生效值，以及是被模型窗口还是全局上限压住的）+ 降级链逐环 + 可用清单（单价标注）；有参：先写回 config `model.name` 再 Agent 内热切换（D32，同 `/permission` 模式），下一轮即生效；provider/端点/降级链/窗口改设置窗或 config（重启生效） |
| `/model probe [模型名]` | 实测某模型到底收不收得下工具调用、思考、图片：三条最小请求、逐项三态结论（支持/不支持/无结论，附依据与耗时用量）。图片那一项给一张纯色图让它说颜色——说对才算支持。**只把判定明确的几位写回** `models[].caps`（无结论不动现有声明），重启后生效。省略模型名 = 当前模型；探测是真请求、真花钱，故只在手动发起时跑 |
| `/think [on\|off]` | 无参：原生思考开关（缺省开）；有参：切换并写回 config `model.think`（D34）。**总开关**：off 时 `reasoning_effort` 与 `enable_thinking` 一律不发（覆盖 `/effort`）；当前链的模型未声明思考能力时同样不发 |
| `/effort [级别]` | 无参：当前 `reasoning_effort` 档位；有参：`minimal\|low\|medium\|high\|off`（off 清除）写回 config `model.reasoning_effort`。是否真发由 `/think` 决定；on 且未设档 = 不发（交服务端默认） |
| `/plugin [list\|enable <name>\|disable <name>]` | MCP 插件管理（M4，D31）：list 显示状态/来源/传输/能力授权/调用统计/重启与报因，并列出可用动态命令；enable 走 capability 首用确认并写回 config；disable 即时摘除其工具与命令 |
| `/mcp:<server>:<prompt> [args]` | MCP prompts 动态命令（随插件启停注册/注销，`/plugin list` 查看可用项）；渲染结果作为用户消息走完整一轮 |
| `/quit` `/exit` | 退出（`/exit` 为别名） |
| `/help` | 命令帮助 |

> 节点 ID 支持唯一前缀匹配；自身、同级与下级节点 ID 可用 `/branch` 查看（逐层下钻可达任意深度）。
> "修改"永不改写旧消息：Fresh 另起节点、Carry 边转移（后代本身不变），会话树始终保持不可变。

## 内置工具（M2/M3 起，模型自行调用）

模型可在回答过程中调用工具；**执行前一律过 ToolRunner**：权限矩阵判定 → 矩阵外逐次
`[y/N]` 确认（`-yes` 全免）→ 单次超时（`limits.tool_timeout_sec` 缺省，任何调用可带
保留参数 `timeout_sec`（整数 1–3600）逐次覆盖，允许高于 config，D38）→ 结果按
`limits.tool_output_chars` 裁剪。拒绝/超时/工具自身失败都以 `OK=false` 回填给模型，
不中断对话；**基础设施故障**（确认器缺失/报错、Ctrl+C 取消）则快速中止本轮，不空转。

| 工具 | 说明 | Risk |
|---|---|---|
| `memory_list` / `memory_read` / `memory_search` | 记忆索引/读取/关键词检索（只见全局 + 当前会话两份） | Safe |
| `memory_write` | 写入记忆（`mode=append` 缺省 / `overwrite`；strict 下路径格外需确认） | Confirm |
| `file_read` / `file_list` / `file_search` | 读文件（可分页：`offset`/`limit`，带行号与续读标注；图片直接给模型看，需当前降级链上的模型都声明了 `vision` 能力）/ 列目录 / 按文件名递归搜索（**绝对路径**，读全盘免确认） | Safe |
| `file_edit` | 精确子串替换（`old_string` 须唯一，`replace_all` 放开；保留原换行与 BOM） | Confirm |
| `file_write` / `file_delete` | 覆盖写 / 删除（写按权限矩阵路径格判定） | Confirm |
| `think` | 显式整理思路（no-op，内容随调用入树）——**默认不列给模型**（D34），`model.think_tool: true` 启用 | Safe |
| `sleep` | 等待 N 秒（停顿或等后台任务；Ctrl+C 可中断；1–3600s，超缺省超时须在该次调用上带 `timeout_sec`，D38/D39） | Safe |
| `context_compact` | 触发上下文压缩（等价 `/compact`，自我管理上下文） | Safe |
| `term_exec` | 同步执行终端命令行（`cmd /c` / `sh -c`；超时统一控制，输出保头尾截断；输出自动转 UTF-8，无须 `chcp`；执行类看工具列） | Confirm |
| `job_start` | 启动后台任务（独立进程，日志落盘 `profiles/<name>/jobs/<id>.log`，不随对话取消；**任务结束时自动汇报**，无需反复查） | Confirm |
| `job_wait` | 等后台任务结束并直接拿结果（状态/退出码/耗时/日志尾部）：编译、跑测试这类确定会结束的任务用它，不必等下一轮汇报。不传 `timeout_sec` 表示一直等（随时可中断）；等超时只回「还在跑」，任务继续 | Safe |
| `job_list` / `job_status` / `job_logs` / `job_kill` | 后台任务管理（列表/状态/日志尾部/终止） | Safe |
| `watch_start` | 声明一条观察器：`kind`（command/file/http）+ `target` + `match`（正则，省略 = 输出有变化）+ `notify`（match 报一次即结束 / change 每次重新命中都报）+ `interval_sec`（5–3600，缺省 30，固定间隔）+ `max_checks` / `deadline_sec`。**命中才回报**，未命中不打扰；等 CI、日志里出现某行、文件出现用它 | Confirm |
| `watch_list` / `watch_stop` | 观察器列表 / 停止 | Safe |
| `window_list` | 列出可抓取的顶层窗口（标题、程序名、屏幕矩形、是否前台，按 z 序自顶向下）；`handle` 传给 `screen_capture` | Safe |
| `screen_capture` | 截屏给模型看：`target` = `screen`（整个虚拟桌面）或窗口 `handle`（抓客户区，**被遮挡、非顶层的窗口也抓得到**；最小化窗口经系统合成器的预览取整窗，不还原窗口、不抢焦点；纯 GPU 自绘窗口取不到会明确报因）；打开着的菜单/下拉会合进主图。PNG 进附件库并随结果内联（长边超 1600 会缩小）。仅 **Windows** 支持；当前链上的模型未声明图片能力时调用即报因 | Confirm |
| `ask_user` | 向你提问（一次 1–4 题，每题最多 9 个选项，可多选，也可以自己输入）：GUI 在输入栏原地展开问答面板，命令行前端逐题打印编号选单。**不吃默认工具超时**——想多久都行；无人值守或 `-yes` 时立即回「用户不在」，不阻塞轮次 | Safe |
| `web_search` | 联网搜索：`query`（1–128 字符，支持 `site:` / `"短语"` / `-词` / `lang:zh,en` / `after:2025-01-01` 等算符）+ `count`（1–20）+ `freshness`（day/week/month/year）。按 `web.search.providers` 的顺序换家，回填里注明这次实际用了哪家、之前哪家失败了 | Safe |
| `web_fetch` | 抓一个网页并转成 Markdown（`url` + `max_chars` 500–20000）：导航与脚本被剔除，长文按预算截断并提示还能继续读。本地抓取遵守 `robots.txt`，拒绝内网/回环目标 | Safe |

联网能力由配置决定，两件工具**始终注册**（没配就调用会明确告诉你缺哪一段）：

- 抓取开箱可用：模板默认带本地实现（纯 Go，无需密钥）。
- 搜索需要先配一家提供商，例如在 profile 的 `config.json` 里写
  `"web": { "search": { "providers": [ { "kind": "deepseek" } ] } }`——`deepseek` 借
  `model.providers` 里同名 provider 的地址与密钥；也可以用 `{ "kind": "brave", "key": "secret:AQUARIUS_BRAVE_API_KEY" }`。
  两家的顺序就是优先级，前面那家挂了会自动换后面那家（详见 [configuration.md](configuration.md)）。

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
  值可用 `secret:<环境变量名>` 引用）或 `profiles/<name>/plugins/<m>/plugin.json`；
  **启停与授权状态**在 config `plugins.<name>`（D31，同名声明以 config 为准）。
- **授权**：声明的 `capabilities` 首次启用逐项 `[y/N]` 确认，通过即写入 `granted`；
  未配置确认器时 fail-closed 不启动；`risk=confirm` 的工具仍逐次确认（不随授权放行）。
- **工具**：`tools/list` 注册为 `mcp:<server>:<tool>`，与内置工具同权过 ToolRunner 权限矩阵；
  出站到模型时换成端点安全别名 `mcp_<server>_<tool>`（严格端点不接受冒号），会话树与日志仍是冒号形态；
  server 崩溃自动重启（1s/2s/4s 退避、限 3 次），超限置 `crashed`；重启与调用统计见 `/plugin`。
- **resources** 只读并入记忆索引与 `memory_*` 读取面（写/删仍走自有记忆，§6.3）；
  **prompts** 暴露为 `/mcp:<server>:<prompt>` 动态命令。
- `/plugin list` 看状态，`enable`/`disable` 即时启停（写回 config，重启沿用）。

## 权限等级

四档预设，`sandbox`（`profiles/<name>/sandbox`，启动自动创建）是 Agent 特权目录。
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
- **无人值守**（`/unattended`、球/托盘菜单「无人值守」、设置窗「常规」→「权限」）：走开一会儿
  时用。开启后**权限等级一字不改**，只是模型发起的越界调用不再弹确认，而是当场拒绝并回填原因
  （没人能批准 / 别重试 / 在允许范围内继续 / 把待批事项列出来）——模型会改道，轮次不会被卡住。
  你自己键入的命令确认（`/rm`、删会话）与插件授权照常询问；`ask_user` 也直接回「用户不在」。
  状态行会常驻「无人值守」标记。注意它与 `-yes` **方向相反**（自动放行），两者同时出现时
  `-yes` 优先且启动会警告。

## 常见问题

- **启动报"发现旧格式会话（虚拟 Root，D19）"**：M0 早期数据不兼容，按报因点名的
  ID 删除 `profiles/<name>/conversations/<id>.json`（含 `.bak`）后重试，详见 [storage.md](storage.md)。
- **启动警告"api_key 为明文"**（D35）：明文密钥直接写在 config.json 里，任何能读
  该文件的进程都可取用——到设置窗「模型服务」页（或欢迎窗）重填一次即转入本机加密密钥库，
  config 里只留 `secret:<名>` 引用；也可手工改成 `"api_key": "secret:名"` 再配同名环境变量。
  该项留空也会自动回落默认引用（两处都取不到时才报错退出）。
- **密钥库**：真实值存 `<profile>/secrets.bin`（本机加密；Windows 用系统账户加密，其它平台用
  同目录 `secrets.key`）。取用顺序 = **先查库、再查同名环境变量**。设置窗「密钥库」页可列出/
  新增/覆盖/删除任意名字（列表只显示名字与尾 4 位掩码）。页内「导入旧密钥」可一键把**存量**收进库：
  config 里仍然直填的明文 `api_key`（收库后退化为 `secret:<名>` 引用）、以及只在同名环境变量里的
  引用值（值抄进库、config 不动）；库解不开时该按钮会**拒绝**而不是清库重来。**备份/同步/换机拷走的
  `secrets.bin` 解不开**，这是预期行为；那种情况下启动只打一行警告、继续用同名环境变量兜底，不会阻断。
- **思考链路（D34/D42）**：`/think off` 关闭原生思考（不发 `reasoning_effort`/`enable_thinking`）；
  `/effort high` 只存档位、配合 `/think on` 才发送；`reasoning_effort` 非法值启动即报因
  （`minimal|low|medium|high`）；服务端不认的参数会被自动剥离并记入
  `model.unsupported_params`（要恢复发送就从该数组删掉对应字段）。
  思考过程**随节点入树**（`PartThinking` 分片，实时暗块与启动回放都看得到）；实时阶段正文
  一直铺开，**定稿后折叠成一行「▸ 已思考 N 秒」**，点一下这一行展开正文、再点收起（开合只
  影响显示，不改变会话树）；
  是否**回传给提供商**由 config `model.echo_thinking` 控制——**键缺失即回传**（DeepSeek 等
  兼容端点带 `tools` 时强制，缺失会 400），设为 `false` 可关；回传走 assistant 消息的
  `reasoning_content` 字段，服务端不认时自动剥离并记入 `model.unsupported_params`。
- **启动报未知权限等级**：`permissions.level` 只接受
  `read-only|strict|permissive|full-access`。
- **`ui.kind` 报未支持**：只接受 `gui`（默认，D51 悬浮球）、`tui`（D33）与 `repl`（行式，测试/e2e 后端）。
- **想换模型/端点**：模型名 `/model`（热切）或 config `model.name`；provider/端点改
  `model.providers`（设置窗「模型服务」页可全量编辑：协议 / 端点 / 密钥池 / 模型列表，含连通性
  测试与逐模型「探测能力」；切到该页会自动拉一遍各家服务端的模型清单，新拉到的行默认未启用，
  勾上「启用」再把这次变更应用出去，重启生效）；或用
  `-model`/`-base-url` flag、`AQUARIUS_MODEL`/`AQUARIUS_BASE_URL` 环境变量临时覆盖。
