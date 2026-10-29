# 配置参考
<!-- docs-sync: 2026-10-29 @ 8c1f643 -->

> 本文与当前实现对齐；**字段语义以本文为准**，端口与权限规则见 [contracts.md](contracts.md)。
> 配置**以 profile 为隔离单位**：
>
> - `<data>/config.json` = 当前 profile **指针**（`{"profile": "<name>"}`）；
> - `<data>/profiles/<name>/config.json` = **主配置**（本表全部字段）；
> - 会话/记忆/附件/任务/审计/沙盒/位置记忆全部随 profile 整目录隔离。
>
> `<data>` 默认 `~/.aquarius`（`-data` 可改）。

## profile 选择

```
--profile flag  >  <data>/config.json 指针        （两者只决定「读哪个 profile」）
```

- `--profile <name>`：进程级覆盖指针（**不写回**）；指向不存在的 profile 启动即报错并列出可用名，不自动创建。
- 指针文件不存在 = 首次运行：写指针（指向 `default`）+ `profiles/default/config.json` 模板后退出；某 profile 的 `config.json` 缺失时同样只写该 profile 的模板。
- **切换 = 重启生效**（Q1）：进程内不热切。设置窗「Profile」区可新建 / 复制当前 / 切换（切换 = 原子写指针）；「复制」仅复制配置（新 profile 以当前 profile 的 config.json 为底，**不带会话/记忆/附件数据**）。
- 旧单文件 config（目录化之前的布局）启动即显式报 `ErrLegacyConfig` 并给出手工迁移步骤——**不做自动迁移**，沿 storejson `ErrLegacyFormat` 口径。

## 三级覆盖

profile 选择在覆盖链之外；**profile 内配置值**的覆盖链为：

```
CLI flags  >  环境变量（AQUARIUS_*）  >  profiles/<name>/config.json
```

| 来源 | 项 |
|---|---|
| flag | `-data`（数据目录）、`-profile`（profile 选择）、`-model`（覆盖 model.name）、`-base-url`（覆盖 **primary provider** 的 base_url——调试逃生口） |
| 环境变量 | `AQUARIUS_MODEL`（覆盖 model.name）、`AQUARIUS_BASE_URL`（同 `-base-url`）；以及各把密钥引用（`secret:<名>`）指向的同名环境变量 |
| config | 下表全部字段 |

首次运行会写入一份可运行模板并退出；改好后重新运行。

## 字段全表

### model（v2：provider 承载「怎么连」，模型承载「能做什么」）

> **改版提示**：`api_key` 单值、`models` 字符串数组、provider 上的 `vision`、`fallback` 名字数组
> 都已是旧形状——启动会**显式报错并打印改写示例**，不会自动迁移（见文末「从旧形状迁移」）。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `model.providers` | []object | — | provider 列表，至少一条，`name` 唯一。每条：`name`（唯一标识，primary/fallback 按名引用）、`kind`（协议：`openai-chat`（空 = 该档，端点 `/chat/completions`）/ `openai-responses`（端点 `/responses`）/ `anthropic`（端点 `/messages`）、`base_url`（**路径前缀**，如 `https://api.openai.com/v1`；适配器在其后追加 `/chat/completions`、`/responses`、`/messages`）、`keys`（密钥池，见下）、`models`（模型条目数组，**首个启用的条目 = 该 provider 缺省请求模型**）、`tokenizer`（本地 tokenizer.json 路径，精确计数；空 = 通用估算）、`unsupported_params`（服务端不认的请求参数，自动记录、启动省略；**只对 `openai-chat` 生效**） |
| `model.providers[].keys` | []object | `[{ "api_key": "" }]` | **密钥池**：`{ "label": "主账号", "api_key": "secret:…" }`。`label` 只用于界面展示；`api_key` 规则同下「密钥规则」。按序轮换：某把被限流（429/配额）即换成下一把，被拒的钥匙冷却一段时间（读 `Retry-After`，缺省 60s）。空数组 = 一条空引用（回落默认引用） |
| `model.providers[].models` | []object | — | 模型条目 `{ "name": "…", "caps": ["tools","reasoning","vision"], "context_window": 128000, "enabled": false, "cost_in": 0.15, "cost_out": 0.6 }`。`name` 唯一非空；`caps` 三项能力的集合，**省略 = 按协议给缺省，写了（含 `[]`）就以声明为准**（见下表）；`context_window` = 该模型的上下文窗口 tokens，**省略/0 = 不声明**（预算只看全局）；`enabled` 省略 = 启用，**只有 `false` 才参与落键**——未启用的条目保留声明但不参与选型（不进降级链候选、不进 `/model` 清单、不作为该 provider 的缺省模型）；每家的模型**至少要有一个启用**，否则启动与设置窗应用都明确报错；`cost_in`/`cost_out` = 单价（**每百万 token**，与厂商报价同口径），省略 = 未声明（界面不显示费用，不当免费）；`sampling` = 该模型的采样覆盖，见下「采样参数」 |
| `model.primary` | string | 首个 provider | 当前生效 provider；不在列表内启动报错 |
| `model.fallback` | []object | `[]` | 有序降级链：`{ "provider": "mimo", "model": "mimo-v2.6-flash" }`；`model` 省略 = 该 provider 首个**启用**的模型。同一 provider 可重复出现（同家不同型号兜底），但 `{provider, model}` 二元组不得重复。**仅网络类错误**（429/408/5xx/连接/超时）在内层同 provider 重试耗尽后触发（重试在内、降级在外）；401/400 等配置错误不降级直接暴露；无粘态（每次调用仍从 primary 起试）；流中途断连既不重试也不降级 |
| `model.name` | string | primary 首个**启用**的模型 | 当前模型名；`/model` 热切换并写回本字段（provider/base_url 重启生效）。切到**没在 `models` 里声明**的名字也允许——按协议缺省给能力 |
| `model.think` | bool | `true`（键缺失） | 原生思考**总开关**：`off` 时 `reasoning_effort` 与 `enable_thinking` 一律不发；`/think` 切换写回本字段。当前链的模型没有 `reasoning` 能力时同样不发 |
| `model.reasoning_effort` | string | `""` | 推理档位：`minimal` / `low` / `medium` / `high`，空 = 不发送；`/effort` 切换写回（`off` 即清除）；非法值启动报错 |
| `model.think_tool` | bool | `false` | `think` 草稿工具是否列给模型——默认隐藏，改后重启生效 |
| `model.echo_thinking` | bool | `true`（键缺失） | **思考回传开关**：思维链是否回传给提供商。键缺失 = 回传（DeepSeek 等兼容端点带 `tools` 时强制回传，缺失会 400）；显式 `false` = 不发。端点点名不认时自动剥离重试并记入该 provider 的 `unsupported_params`。改后重启生效 |
| `model.sampling` | object | — | **采样参数**（整链缺省）：`{ "temperature": 0.7, "top_p": 0.9, "max_tokens": 1024, "stop": ["END"] }`。四项都可省；**浮点项用「写了才发」语义**：键缺失 = 不发送（交服务端默认），`0` = 真的发 0。`stop` 是字符串数组（可含换行），空串非法 |

**能力位与协议缺省**（省略 `caps` 时生效）：

| kind | tools | reasoning | vision | 说明 |
|---|---|---|---|---|
| `openai-chat` | 开 | 开 | **关** | 兼容生态里多数端点支持工具调用；思考参数不认时有「剥离重试」兜底。图片端点差异太大，不猜 |
| `openai-responses` | 开 | 开 | 开 | `input_image` 是标准字段 |
| `anthropic` | 开 | **关** | 开 | 图片与工具全系支持；extended thinking 要显式给 `budget_tokens`，旧模型不接受 |

**生效能力 = 整条链取与**：primary 当前模型与每个降级项的模型，三项各自取与。链上任一模型缺某项，
整轮就按缺该项处理（不发工具清单 / 不发思考参数 / 图片降级为文字占位）。加一条弱模型会削掉整链的
能力，所以启动时若链窄于当前模型会打印一行警告，`/model` 也会列出生效能力。

**能力探测**：能力位是声明，写错了要到用的时候才炸。`/model probe [模型名]`（省略 = 当前模型）
发三条最小请求试 工具调用 / 图片输入 / 思考，**只把判定明确的**写回 `caps`——网络失败、超时、
参数被静默忽略这类「无结论」不会清掉已有声明。图片输入那一项是**给一张纯色图让它说颜色**：
说对了才算支持（说明图被真正解码了，而不只是收下了字段），说成别的颜色或明说看不到算不支持。
探测之后该模型的能力位就是**显式声明**了（无结论的项保留探测前的生效值），重启后生效；
探测会真实消耗少量 token，故不自动运行。设置窗「模型服务」页的模型行也提供同一操作。
思考参数有 `reasoning_effort` 与 `enable_thinking` 两套（各家认的不一样），探测会**分别各试一次**
再合并——任意一套能出思考就算支持，两套都被拒才算不支持。

**上下文窗口**（`models[].context_window`）：装配预算 = **min(当前模型的窗口, `limits.max_context_tokens`)**。全局那项是"我想用多少"（省钱就调小），模型窗口是"服务端最多收多少"（超了直接拒），两者都生效、谁小听谁的。
模型没写窗口（或写 0）= 只看全局。窗口**不随降级链取小**——装配发生在换模型之前，按当前模型算；
降级到窗口更小的模型属于自己配的链，可能被服务端拒。`/model` 会显示生效值与「是谁在压」。
**模型启用位**（`models[].enabled`）：键缺失 = 启用，只有 `false` 会落键。未启用的条目**保留声明
但不参与选型**——不进降级链候选、不进 `/model` 清单（当前模型例外：它正在跑）、不作为该 provider 的
缺省请求模型。日常用途是「把服务端列出来的模型都登记着，只勾现在要用的那几个」：设置窗「模型服务」页
切进去时自动拉一遍各家的 `/models`，新补进来的行默认未启用，勾选后随「应用 provider 变更」写回。
每家的模型**至少有一个启用**，否则启动与应用都会明确报错。

```json
"models": [
  { "name": "mimo-v2.6-flash" },                    // 键缺失 = 启用
  { "name": "mimo-v2.6-pro" },
  { "name": "mimo-v2.6-preview", "enabled": false }  // 登记着，但先不用
]
```

**采样参数**（`model.sampling` 与 `models[].sampling`）：温度 / top_p / 输出上限 / 停止串四项。
`model.sampling` 是**整链缺省**（primary 与每个降级目标都按它），模型条目里的 `sampling` 只写「想改的那几项」，
其余继承缺省——降级换模型时跟着换成目标自己那份。

```json
"model": {
  "sampling": { "temperature": 0.7, "top_p": 0.9, "max_tokens": 1024 },
  "providers": [{ "name": "a", "models": [
    { "name": "want-code", "sampling": { "temperature": 0 } },
    { "name": "want-long", "sampling": { "max_tokens": 8192, "stop": ["\\n\\n---"] } }
  ]}]
}
```

- **留空 ≠ 0**：浮点项（`temperature` / `top_p`）键缺失 = 这个参数根本不发，交服务端默认；写得 `0` 就是真的发 0。
  想「什么都不指定」就整段删掉，而不是填 0；
- **范围**：`temperature` 0–2（`anthropic` 只接 0–1），`top_p` 0–1，`max_tokens` 正整数；超范围启动即报因（不静默截断）；
- **思考开着时不发采样参数**（`anthropic` 的扩展思考不接受温度 / top_p），此时配了也不生效；
- 输出上限不压过「思考预算」：开了思考且给了 `budget_tokens` 时以预算为准；
- 改完**保存即生效**（设置窗「模型」页保存就换这份快照），不必重启；降级目标那份随重启装配。
  入口只有设置窗「模型」页与 config 两处。

**单价与费用**（`models[].cost_in` / `cost_out`）：单位是**每百万 token**（与厂商报价同口径），
填的是美元。两个都填了才折算：每轮结束后 `Usage.CostUSD` 按当轮用量累加，`/usage` 汇总显示；
缺任意一项就不给数（宁可没有，也不给一个猜的）。`/model` 列表里带单价的模型会标注 `$入/$出 每百万token`。
设置窗「模型服务」页的模型行可直接填这两格，留空 = 未声明。

### ui

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `ui.kind` | string | `gui`（模板与键缺省） | `gui` = Gio 桌面窗口（Windows 为完整形态，其它平台为降级实现）；`tui` = 终端界面；`repl` = 行式（测试/e2e 后端）；其他值启动报错 |
| `ui.hotkey` | string | `""`（= `Alt+A`） | **GUI** 全局呼出/收起快捷键：`修饰键+键`（如 `Alt+A`）；注册失败回退 `Ctrl+Alt+A`；仅 gui 生效 |
| `ui.theme` | string | `system`（键缺失） | **GUI** 深浅主题：`system`/`light`/`dark`，切换热生效；仅 gui 生效 |
| `ui.scale` | number | `1.0` | **GUI** 元素缩放倍率：夹 [0.75, 2.5]；设置窗百分比档；热生效；仅 gui 生效 |
| `ui.font_size` | number | `15` | **GUI** 正文字号 sp：最终字号 = font_size × scale；夹 [10, 28]；热生效；仅 gui 生效 |
| `ui.window_width` / `ui.window_height` | int | `0`（= 缺省 608×460dp） | **GUI** 主窗像素尺寸；UI 侧按地板与粗界夹取；热生效；仅 gui 生效 |
| `ui.welcomed` | bool | `false`（键缺失） | **GUI** 欢迎 / 首次运行引导是否已看：键缺失 = 未看——**首跑必走装配前向导**（该次引导发生在主窗之前，保存即置位，主窗不再弹欢迎窗）；**标记缺失的升级用户**在启动后自动打开欢迎窗一次；完成或显式关闭即置位；入口另在设置窗 |

### prompts（人格模块树）

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `prompts.modules` | object | `{}` | **模块树**：`name → { text?, children?[] }`。叶 = 仅 `text`（命名片段）；组 = `children`（子模块名有序列表，可多级嵌套），亦可带 `text` 作组头 |
| `prompts.persona` | string[] | `[]` | **人格** = 有序顶层引用列表；空 = 默认 `["@default","@env"]` |
| `prompts.user_name` | string | `User` | `{{user}}` / `<USER>` 的取值（角色卡投影用）：你希望在角色卡里被怎么称呼。空 = 回落 `User`；改动只影响之后新建的角色会话，已在树里的人格不会被改写 |

**展开（组装最终 system 人格）**：深度优先——组自身 `text` 在前、子模块文本换行接续（子模块间以空行
连接），顶层引用间以空行连接；**空组不出**（有子模块但全部无文本 ⇒ 整组不输出）；缺名跳过、不阻断。
**内置只读模块**（`@` 保留前缀，用户不可占用，可在 `persona` 引用/排序/移除）：

| 内置模块 | 展开内容 |
|---|---|
| `@default` | 默认人格正文 = 顶层 `system_prompt` 或内置默认 |
| `@env` | **环境信息组**（组头 `Runtime environment:`），子模块见下 |
| `@env.platform` | `- Platform: <os/arch>` |
| `@env.terminal` | `- Terminal: <ui 形态> (TERM=… \| TERM unset)` |
| `@env.sandbox` | `- Privileged sandbox directory: …`（仅绝对路径时出） |
| `@env.timeout` | `- Tool timeout: …`（仅配置了超时时出） |
| `@env.date` | `- Current date: YYYY-MM-DD`（**会话创建当天**） |
| `@char` | **角色卡组**（组头无文本），子模块见下；**只在角色会话里有内容** |
| `@char.identity` | `You are <角色显示名>.` |
| `@char.description` / `@char.personality` / `@char.scenario` | 卡的对应字段，各带一个英文标签 |
| `@char.system` | 卡里的 `system_prompt`（`{{original}}` 在此展开） |
| `@char.book` | 卡的**常驻**世界书条目（`constant` 且 `enabled`，按卡内顺序） |

`@char.*` 是**条件内置**：非角色会话、或卡里对应字段为空时它是空模块、**不计「缺失模块」**
警告。所以把它留在 `prompts.persona` 里不会改变任何普通会话的输出——你可以一直带着它，
按自己的顺序摆在用户人格之前或之后。

结果在**新建会话**时快照进 persona 首节点（改动只影响新建会话，存量会话的 persona 已在
树内、可 `/edit`）。角色的 `@char.*` 与卡目录**脱钩**：换卡版本、删卡、改配置都不会改写
已开始的会话，要按新卡重来就再 `/character start`。

### system_prompt（人格兜底单串）

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `system_prompt` | string | `""`（内置默认） | 作为内置模块 `@default` 的正文来源（兼容旧配置）；`persona` 不引用 `@default` 时不影响组合结果 |

### permissions

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `permissions.level` | string | `strict` | `read-only` / `strict` / `permissive` / `full-access`；非法值启动报错；`/permission` 切换**写回**本字段 |
| `permissions.unattended` | bool | `false` | 无人值守：模型发起的越界操作**当场拒绝**并说明，不再弹确认等人；权限等级本身不变。`/unattended [on|off]`、球/托盘菜单「无人值守」、设置窗「常规」→「权限」勾选都会**写回**本字段并热生效。你自己键入的命令确认（删除会话等）与插件授权照常询问 |

等级语义（矩阵格 = 免确认范围，矩阵外逐次确认）：

| 等级 | 特权目录 sandbox | 其他目录 | 工具调用 |
|---|---|---|---|
| `read-only` | r | r | Confirm 逐次 |
| `strict` | rw | r | Confirm 逐次 |
| `permissive` | rw | rw | Confirm 逐次 |
| `full-access` | rw | rw | 全免 |

两列分工、互不叠加：文件类看路径格（读全盘免确认），执行类看工具列（Safe 免、
Confirm 仅 full-access 免）。**执行接入已生效**；`network`/`secret` 授权流不随等级变化。

无人值守（`unattended`）与等级是两件事，别混着理解：

- **等级**决定「哪些操作免确认」；**无人值守**决定「需要确认时怎么办」——不弹窗问人，直接
  拒绝并告诉模型原因（没人能批准、别重试、在允许范围内继续、把待批事项列出来）。
- 它不是提权：越界操作**不会**被执行，也不会被悄悄跳过；模型会改道去用权限内的手段。
- 与 `-yes` 相反：`-yes` 是自动放行（越界操作照做）。两者同时出现时 `-yes` 优先（命令行显式
  意图压过配置），启动会打一条警告说明本次没启用无人值守。
- 开启期间状态行常驻「无人值守」标记；回来看到一串「已拒绝」时，看状态行就知道原因。
- 模型提问（`ask_user`）同款：没人可问时直接回「用户不在」，不让轮次卡在提问面板上。

### limits

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `limits.max_turns` | int | 32 | 单次请求内"生成+工具"循环上限（终端类多步任务常见 8 轮以上） |
| `limits.max_context_tokens` | int | 64000 | 上下文预算**上限**：实际预算 = min(本值, 当前模型声明的 `context_window`)，见上文「上下文窗口」。硬保底由截断装饰器执行 |
| `limits.compact_threshold` | float | 0.7 | 自动压缩阈值（×max_context_tokens） |
| `limits.tool_output_chars` | int | 20000 | 工具结果截断 |
| `limits.tool_timeout_sec` | int | 60 | 单次工具执行超时秒（模型可经保留参数 `timeout_sec` 逐次覆盖） |

### input / audio（语音）

语音识别与合成各是一份**完整**的服务配置：端点与密钥可以自己填，也可以只写 `provider`
借 `model.providers` 里那一条的端点与第一把密钥。借来的东西只有 `base_url` 与密钥，
不继承协议与能力位，也不进降级链（音频模型不是对话模型的替补）。

三种形态：

- 什么都不填 = 该能力不可用（界面隐藏入口，执行时报告原因，不影响对话）；
- 只写 `provider` = 借那一条的端点与密钥（老配置的形状，行为不变）；
- 写了 `base_url` / `api_key` = 独立配置，写了的那项覆盖借来的（只补缺的一项）。

`provider` 指不到条目时，只有「端点也没自己填」才算配置错误；否则只是这一家取不到密钥，
启动打一行提示，按钮与命令明确报因。整路三样都不填 = 该能力不可用，不算错误。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `input.language` | string | `auto` | 录音语种提示：`auto` / `zh` / `en`，只喂给识别 |
| `audio.asr.provider` | string | — | 借 `model.providers` 里的 name（可省略，见上） |
| `audio.asr.base_url` | string | — | 自己的端点（如 `https://api.example.com/v1`）；空 = 借 `provider` 的 |
| `audio.asr.api_key` | string | `secret:AQUARIUS_ASR_API_KEY` | 自己的密钥（`secret:` 引用或明文）；空 = 借 `provider` 的，都不借则回落此名 |
| `audio.asr.model` | string | — | 转写模型名（`chat-audio` / `openai` 必填），如 `mimo-v2.5-asr` / `whisper-1` |
| `audio.asr.kind` | string | `chat-audio` | 识别协议：`chat-audio` / `whisper` / `openai` / `funasr`，见下 |
| `audio.asr.model_path` | string | — | 本地识别模型文件绝对路径，`kind=whisper` 必填 |
| `audio.tts.provider` | string | — | 同上；缺省 = 不能播报 |
| `audio.tts.base_url` | string | — | 同上（合成侧） |
| `audio.tts.api_key` | string | `secret:AQUARIUS_TTS_API_KEY` | 同上（合成侧） |
| `audio.tts.kind` | string | `chat-audio` | 合成协议：`chat-audio` / `vits` / `gptsovits` |
| `audio.tts.model` | string | — | `mimo-v2.5-tts`（预置音色）/ `-voicedesign`（文本设计）/ `-voiceclone`（样本复刻）；仅 `chat-audio` 用 |
| `audio.tts.voice` | string | — | 预置音色名（如 `冰糖`）/ VITS 的说话人 ID / GPT-SoVITS 的参考音频路径 |
| `audio.tts.design` | string | — | voicedesign 模式的音色描述；`kind=gptsovits` 时是参考音频对应的文字 |
| `audio.tts.style` | string | — | 自然语言风格指令（如 `语速稍快，语气轻松`） |
| `audio.tts.tags` | string | — | 风格标签，非空时拼成 `(标签)` 前缀置于正文开头 |

`kind` 决定打哪个端点、字段怎么摆——**不是靠模型名猜的**，同一个模型名可以挂在不同的服务端后面。

| 侧 | `kind` | 打哪 | 必填 |
|---|---|---|---|
| 识别 | `chat-audio`（缺省） | `POST {base}/chat/completions`，音频进 `input_audio` 内容块 | `model` |
| 识别 | `whisper` | 进程内本地引擎，不连网 | `model_path` |
| 识别 | `openai` | `POST {base}/audio/transcriptions`，multipart `file` + `model` + `language` | `model` |
| 识别 | `funasr` | `POST {base}/recognition`，multipart `audio`（响应带 `code`） | 端点 |
| 合成 | `chat-audio`（缺省） | `POST {base}/chat/completions`，Base64 音频在 `choices[0]` | `model` |
| 合成 | `vits` | `GET {base}/voice/vits?text&id&format=wav` | 端点 |
| 合成 | `gptsovits` | `POST {base}/tts`，音色 = 参考音频路径 + 其文字 | 端点、`voice` |

`openai` 这一档覆盖一大片：OpenAI 官方、FunASR 的 `examples/openai_api` 服务端、whisper.cpp 的
`whisper-server`、各类自建网关——它们的差别只有端点与模型名。

**录音时长有硬顶**：线上限由厂商的音频体积上限反推（16kHz 单声道 16bit ≈ 32KB/s，Base64 再膨胀
4/3）——MiMo 当前 10MB 折合约 4 分钟。产品侧默认 60 秒，命令可带秒数覆盖，但不得超过这个硬顶。

**本地识别**（`kind=whisper`）不下载模型：自己取 `ggml-base-q5_1.bin`（约 60MB，
`huggingface.co/ggerganov/whisper.cpp`）后把绝对路径填进 `model_path`。模型文件读不到时启动会
警告并让识别不可用，不影响对话。这一档要求构建时带 `GOEXPERIMENT=simd`，否则慢 4 倍左右。
本地引擎对中文默认输出繁体，装配处会过一道繁转简（OpenCC 的 `tw2sp` 词典）；转换失败保留原文。

`kind` 留空等同 `chat-audio` 且视为「未配置」；写成非缺省值即表示明确要用该协议，此时必须填
端点（`whisper` 除外）。白名单外的值在启动时直接报错。

密钥槽按**能力**分（`_ASR` / `_TTS`），不按 provider：同一家的对话、识别、合成通常是三把
不同的钥匙，共用一个槽位会让后填的覆盖先填的。界面里填明文会先收进本机密钥库，config 只留
`secret:<名>`。

### skills（技能）

技能 = 一个目录 + `SKILL.md`（YAML frontmatter 必填 `name` / `description`，正文是给模型的
指令）。助手只在**要用的时候**才载入正文：常驻上下文里的只有名字与一句说明。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `skills.dirs` | []string | `[]` | 追加技能根（**绝对路径**，按声明序优先）；空 = 只用 `<profile>/skills` |

- 每个技能根下最多找两层（根自身 / 一级子目录 / 二级子目录）的 `SKILL.md`；**目录名必须与
  frontmatter 的 `name` 相同**（小写字母、数字、连字符，≤64 字符）。
- 多个根出现同名技能时**先声明的生效**，后面的跳过并记一条警告（启动日志里能看到）。
- 目录可以放 `scripts/`、`references/`、`assets/`：模型载入技能后会拿到技能目录的绝对路径，
  需要时用 `file_read` / `term_exec` 自己深入（脚本执行照常走权限矩阵与逐次确认）。
- 装坏的技能（缺字段、名字不合规、YAML 解析失败）不会让助手起不来：跳过它并记警告。
- 改动 `skills.dirs` 或增删技能目录后**重启生效**（不做文件监听）；GUI 侧将来有「重新扫描」。

### characters（角色卡）

角色卡只读导入（PNG / JSON / `.charx`），投影成一次会话的开头；卡的落位与解析口径属于设计，
见 [contracts.md](contracts.md) 的领域模型与角色卡端口一节。这里只列会影响行为的四个开关与用户名的取值。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `characters.prefer_system_prompt` | bool | `false` | `false` = **你的人设在前，卡的系统提示作为附加段**（推荐：卡不该悄悄替换你的规则与安全约束）；`true` = 卡里的 `system_prompt` 替换主提示，其中的 `{{original}}` 展开成你的人格，卡的角色设定段始终保留。**两种模式下运行环境信息都保留**——那是事实，不是人设 |
| `characters.apply_post_history` | bool | `true` | 每轮把卡的 `post_history_instructions` 追加在**历史之后**（语义就是「最后再说一句」）；内容随会话落盘，关掉只是不再发送，不会删掉 |
| `characters.inject_constant_book` | bool | `true` | 注入卡的**常驻**世界书条目（`constant: true` 且未禁用），按卡内顺序进人格段。默认开是因为不少卡把人设整个写在常驻条目里，不注入就是个空壳。**代价**：它在人格段里，每轮都付 token、**不会被上下文压缩挤掉**；觉得占地方就关掉它再重开一次会话 |
| `characters.inject_examples` | bool | `true` | 注入卡的示例对话（`mes_example`）。它落在人格之后的注入节点里，**会随历史增长被挤掉**；关掉只是不再发送，卡文件照留 |

- 你希望在卡里被怎么称呼，用 `prompts.user_name`（默认 `User`）指定，见上文 prompts 段。
- 四个开关**只影响之后新建的角色会话**，已开始的会话保持建会话时的口径（人格与尾部指令
  都已快照/落盘）；只有 `apply_post_history` 对已有会话是**立即**生效的（它决定发不发，
  不改内容）。
- 卡的 `creator_notes`（作者留言）、`tags`、`creator`、`character_version` 只展示，**不进上下文**；
  卡的**非常驻**世界书条目、非图标资产，以及正则脚本、扩展脚本一类的机制**保留但不执行**——
  导入回执会逐类说清哪样读了、哪样没读。
- 卡目录是唯一事实来源：坏掉的卡在 `/character list` 里带警告跳过，不阻断其余角色。

### delegate（外部代理转交）

把复杂任务整个交给一个外部代理程序（ACP 协议）。适配器与协议面属于设计，见
[contracts.md](contracts.md) 的 delegate 端口一节；这里只列会改变行为的配置。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `delegate.agents.<名字>.command` | string | — | 启动命令（按 `PATH` 探测）；名字就是 `delegate` 工具的 `agent` 取值与 `/delegate list` 里看到的那个 |
| `delegate.agents.<名字>.args` | []string | `[]` | 启动参数（各家代理自己定；registry 里能查到） |
| `delegate.agents.<名字>.env` | object | `{}` | 子进程环境变量，值可用 `secret:<名>` 引用密钥库/环境变量 |
| `delegate.agents.<名字>.workdir` | string | 空 | 子进程起点目录；空 = 家目录。**它是起点不是围栏**，那个程序按它自己的策略访问文件 |
| `delegate.agents.<名字>.enabled` | bool | `true` | `false` = 留着配置但不探测、不注册 |
| `delegate.agents.<名字>.default` | bool | 见下 | `delegate` 不指定 `agent` 时用哪个；多个都标时取配置里第一个，都不标时取第一个可用的 |
| `delegate.timeout_sec` | int | `1800` | 单次转交总闸（防进程挂住）；`0` = 不限，最大 86400 |

- **键缺失不等于没配**：`agents` 为空或全不可用时，`delegate` 工具不注册（模型压根看不到它），
  `/delegate list` 会给出「还没配任何代理」与一段配置指引。
- **不发预设、不自动安装**：各家代理的命令名与启动参数由它们自己决定，我们抄一份只会过时；
  装什么、装哪版由你决定。想要「一键装了就能用」，去它们各自的官方安装说明。
- **每次转交都是全新会话**：进程跑完即关，`sessionId` 不跨次使用；追问要重述背景。
- **权限真话**：批准一次转交 = 批准那个程序在它自己判断下动手。我们的权限等级矩阵只管我们
  自己的工具链，管不到它；它愿意反问的权限请求会回落到你的确认框，不愿意问的我们知道不了。
- 转交的转录**不进会话树**，也不落盘——只看最终报告与一行摘要。

### output

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `output.notify` | bool | 模板 `true`；键缺失 = `false` | 已提交的回答触发系统通知（发送失败只记日志） |
| `output.tts` | bool | `false` | 语音播报**总开关**：开启后已提交的回答自动合成并播放（需 `audio.tts` 配好） |

### web（联网搜索与抓取）

两组列表都是**有序 = 优先级**：先试第一家，失败换下一家（网络/超时/401/402/403/429/5xx 换，
400 不换）。回填里会注明实际应答的提供商与之前的失败，模型据此说明来源。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `web.search.providers` | []object | `[]` | 搜索提供商（有序）；空 = 搜索不可用 |
| `web.search.providers[].kind` | string | — | `deepseek` / `brave` |
| `web.search.providers[].from` | string | 同 `kind` | `deepseek` 借哪个 `model.providers` 的 `base_url` 与密钥（只补没填的那项） |
| `web.search.providers[].key` | string | 见下 | 自己的密钥（`secret:` 引用或明文）；空 = 借 `from` 的，都不借则按侧回落 |
| `web.search.providers[].base_url` | string | 见下 | 自己的端点（自建网关/镜像）；空 = 借 `from` 的 |
| `web.search.providers[].country` / `lang` | string | 服务端缺省 | 仅 `brave`：结果国家（`CN`）与语言（`zh-hans`） |
| `web.search.count` | int | `8` | 缺省条数（1–20，工具参数可逐次覆盖） |
| `web.fetch.providers` | []object | 模板带 `[{"kind":"local"}]` | 抓取提供商（有序）：`deepseek` / `local` |
| `web.fetch.max_chars` | int | `16000` | 单次回填正文上限（500–20000） |
| `web.fetch.proxy` | string | 空 | 本地抓取代理（如 `http://127.0.0.1:7897`）；空 = 读 `HTTP(S)_PROXY` |
| `web.fetch.user_agent` | string | 浏览器形态 | 本地抓取 UA（很多站点拒绝非浏览器 UA） |
| `web.fetch.respect_robots` | bool | `true` | 本地抓取遵守 `robots.txt`（键缺失 = 遵守） |
| `web.fetch.allow_private_ips` | bool | `false` | 放开回环/私网目标（只用于调试本机服务） |
| `web.fetch.timeout_sec` | int | `30` | 本地抓取单次超时 |

`deepseek` 用的是它自己的 Search / Fetch REST 端点（与模型无关，按成功次数计费）；
`local` 是纯 Go 抓取（正文提取 + Markdown 转换），不需要密钥，缺省就开。某一家装不起来
（缺密钥、`base_url` 为空）只打一行警告并跳过，其余提供商继续可用；一家都没配起来时，该工具
执行时明确报「未配置提供商」，启动不失败。

计费只发生在「HTTP 200 且结果非空」时：空结果换下一家再试一次，全链都空就如实回空结果（不是错误）。

`deepseek` 的端点与密钥**优先用本条目自己填的**，没填的才按 `from` 去 `model.providers`
借（`from` 缺省 = 同名的 `deepseek`）。所以 `{"kind":"deepseek"}` 这种老写法照常工作，
而想给联网单配一把钥匙时只写 `key` / `base_url` 即可，不必动对话 provider。密钥槽按**侧**
区分——搜索 `AQUARIUS_DEEPSEEK_SEARCH_API_KEY`、抓取 `AQUARIUS_DEEPSEEK_FETCH_API_KEY`
（`AQUARIUS_DEEPSEEK_API_KEY` 归对话 provider），两侧互不覆盖。

以上键都可以在 GUI 设置窗的「联网」页编辑（两组提供商是有序列表，可增删与调序；密钥框
只写不回显，留空 = 保持原引用）。「借用」下拉选中一条后点「填入」，会把那条的端点与
**密钥引用名**抄进本条目（永不抄值），之后照常可以改。设置窗保存前按与启动同一口径校验，
写坏的组合进不了盘。

### mcpServers / plugins

`mcpServers` 声明 MCP server；`plugins` 存**启停与授权状态**（声明也可来自
`profiles/<name>/plugins/<name>/plugin.json`，状态一律在 profile config）。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `mcpServers.<name>.transport` | string | — | `stdio` 或 `streamable-http`，缺省报错 |
| `mcpServers.<name>.command` / `args` / `env` | string / []string / object | — | stdio 形态：启动命令与环境（值可用 `secret:<环境变量名>`） |
| `mcpServers.<name>.url` / `headers` | string / object | — | streamable-http 形态：端点与请求头（值同样可用 `secret:` 引用） |
| `mcpServers.<name>.capabilities` | []string | `[]` | 所需能力声明，**首次使用弹确认**后写入 granted |
| `mcpServers.<name>.risk` | string | `safe` | `safe` / `confirm`：后者工具逐次确认 |
| `plugins.<name>.enabled` | bool | `true` | 启停；`/plugin enable\|disable` 写回，即时生效 |
| `plugins.<name>.granted` | []string | `[]` | capability allowlist |

设置窗「MCP 与插件」为只读清单；结构化字段的编辑走「高级」页 raw JSON（Q10）。

### 暂未消费的键（模板自带，随里程碑启用）

未知键解析时忽略，**写回时原样保留**（provider 条目内的无关键同样保留——编辑器按
原名合并，Q10 口径）。GUI 语音键与 `/mic` 已可用；TUI 尚无语音入口（用 `/mic`）。
`delegate.agents` 为空时照常启动（工具不注册），填了才探测；设置窗「外部代理」页尚未落地。

## 密钥规则（逐 provider、逐把钥匙）

```json
"keys": [
  { "label": "主账号", "api_key": "secret:AQUARIUS_OPENAI_KEY" },
  { "label": "备用",   "api_key": "secret:AQUARIUS_OPENAI_KEY_2" }
]
```

- 推荐：配置里只有引用名 `secret:<名>`，真实值按名取用——**先查本机密钥库、未命中再查同名环境变量**。
- **本机密钥库**落 `<profile>/secrets.bin`（随 profile 隔离）：Windows 走 DPAPI（绑定当前用户账户，
  无口令、无额外密钥文件），其它平台走 AES-256-GCM + 同目录 `secrets.key`（0600）。整库加密，
  键名与值都在密文内；写前留一代 `.bak`。
- **界面填的明文会转入库**：设置窗（provider 页、联网页）与欢迎窗填明文 → 写进密钥库，
  config 里只落 `secret:<名>`。**键名规则只有一条**：
  - 该槽原本就有 `secret:<名>` 引用 → **沿用原名**（重填明文只换值，provider 改名也不换名）；
  - 没有引用 → 按 provider 名生成 `AQUARIUS_<PROVIDER>_API_KEY`，第 N 把追加 `_<N>`；
  - provider 名里含中文、全角或重音字母时，规范化会把它们吃掉（`深度求索` 与 `月之暗面`
    都变 `AQUARIUS_PROVIDER_API_KEY`、互相覆盖），这类名字补 4 位指纹区分，
    如 `AQUARIUS_OPENAI_03F5_API_KEY`。
  联网页的 brave 密钥同口径入库（沿用 `AQUARIUS_BRAVE_API_KEY` 或自定义引用名）；deepseek
  这类可独立配置的条目按**侧**分槽（`AQUARIUS_DEEPSEEK_SEARCH_API_KEY` /
  `AQUARIUS_DEEPSEEK_FETCH_API_KEY`，不占对话 provider 的 `AQUARIUS_DEEPSEEK_API_KEY`）；
  语音页按**能力**分槽（`AQUARIUS_ASR_API_KEY` / `AQUARIUS_TTS_API_KEY`）。密钥库不可写时
  直接报错，不会静默把明文留在盘上。
- 明文密钥：允许直接填或手改 config，**启动打印警告**（不回显密钥值）；建议确保 config
  不被提交/同步，或到设置窗重填一次转入密钥库。
- 值为空（或整项删除）→ 该 provider 回落默认 `secret:AQUARIUS_OPENAI_KEY`。
- **密钥池**：同一 provider 配多把，按序轮换。某把返回 429 / 配额耗尽即换成下一把并立刻重试；
  被拒的那把进入**进程内冷却**（读响应头的 `Retry-After`，缺省 60s），冷却期内直接跳过。全都在冷却
  = 该 provider 这一轮失败，交给降级链。顺序固定「先换钥匙、再换 provider」。冷却状态不落盘，重启清空。
  换钥匙与「全在冷却」都会在界面上给一行提示。只有一把钥匙时不启用轮换（没有别的可换）；某一把
  引用取不到时跳过它继续启动（其余钥匙照用），全部取不到才报因。5xx/网络类错误**不换钥匙**——
  端点故障换钥匙没用。
- **库解不开时**（profile 拷到别的机器、换了账户、文件损坏）：打一行警告后**回落同名环境变量**继续
  启动（不阻断）；原文件原地保留，不会被改写。
- 备份 `secrets.bin` 到另一台机器**解不开**——这是绑定本机/本账户的预期行为。
- **导入旧密钥**（能力保留，界面上已不提供入口）：把存量密钥从旧形态收进库——config 里仍是明文
  直填的那把（收库后退化为 `secret:<名>` 引用），以及「config 只写引用、真值只在同名环境变量里」
  的那些（值抄进库、config 不动）。只处理 **config 真正引用到的名字**，不扫全部 `AQUARIUS_*`
  环境变量（那会把无关变量也固化下来）。库解不开时**拒绝导入**，不套用普通写路径的「从空表起步」
  ——批量导入会因此丢掉库里已有条目。顺序是先落库、后改 config，失败留「库里有值、config 还是
  明文」这个可用中间态。值永不回显，反馈只报条数。
- 无论哪种形态，密钥都不进会话树、附件、日志；stdio 插件子进程环境剔除 `AQUARIUS_*` 变量。
- 设置窗密钥输入**只写不回显**：留空 = 保持原值（按编辑前原名匹配保留，改名不丢密钥）。
- MCP 声明的 `env`/`headers` 值同样可用 `secret:<名>` 引用；任意名字可在设置窗「密钥库」页新增/
  覆盖/删除，列表只显示名字与尾 4 位掩码。

**为什么不是明文文件或环境变量**：三者真正的差别是「谁能读到」与「会不会被顺带带走」两件事。

| 形态 | 同用户进程 | 同用户子进程 | 备份 / 同步 / 换机拷文件 |
|---|---|---|---|
| 环境变量 | 可读 | **会继承**——`term_exec`、`job_start` 起的命令直接看见 | 否（但 Windows 的用户环境变量明文存在注册表里） |
| 明文文件 | 可读 | 不继承 | **会带走** |
| 本机加密库 | 可读 | 不继承 | **不带走**（密文绑当前账户；非 Windows 的密钥另存同目录，只挡住离线备份） |

助手的 `term_exec` 会把环境整份交给子进程，所以环境变量在这里是最差的一档；明文文件只解决了继承
问题；本机加密库额外挡住「备份 / 同步 / 换机」这条最常见的泄露路径。三者都挡不住「以同一用户身份
运行的恶意程序」——这一点不夸大。

## 完整模板（首次运行生成）

根指针 `<data>/config.json`：

```json
{
  "profile": "default"
}
```

profile 配置 `<data>/profiles/default/config.json`：

```json
{
  "model": {
    "providers": [
      { "name": "openai",
        "kind": "openai-chat",
        "base_url": "https://api.openai.com/v1",
        "keys": [ { "label": "主账号", "api_key": "secret:AQUARIUS_OPENAI_KEY" } ],
        "models": [
          { "name": "gpt-4o-mini", "context_window": 128000 },
          { "name": "gpt-4o", "caps": ["tools", "reasoning", "vision"], "context_window": 128000 }
        ],
        "tokenizer": "",
        "unsupported_params": [] }
    ],
    "primary": "openai",
    "fallback": [],
    "name": "gpt-4o-mini",
    "think": true,
    "reasoning_effort": "",
    "think_tool": false,
    "echo_thinking": true
  },
  "tools": { "disabled": ["think"] },
  "ui": { "kind": "gui", "theme": "system", "scale": 1.0, "font_size": 15, "window_width": 0, "window_height": 0, "welcomed": false },
  "system_prompt": "",
  "prompts": { "modules": {}, "persona": [], "user_name": "User" },
  "input": { "language": "auto" },
  "skills": { "dirs": [] },
  "characters": { "prefer_system_prompt": false, "apply_post_history": true,
                  "inject_constant_book": true, "inject_examples": true },
  "delegate": { "agents": {}, "timeout_sec": 1800 },
  "output": { "tts": false, "notify": true },
  "audio": {
    "asr": { "provider": "openai", "model": "mimo-v2.5-asr" },
    "tts": { "provider": "openai", "model": "mimo-v2.5-tts", "voice": "冰糖" }
  },
  "web": {
    "search": {
      "providers": [
        { "kind": "deepseek" },
        { "kind": "brave", "key": "secret:AQUARIUS_BRAVE_API_KEY", "country": "CN", "lang": "zh-hans" }
      ],
      "count": 8
    },
    "fetch": {
      "providers": [ { "kind": "deepseek" }, { "kind": "local" } ],
      "max_chars": 16000,
      "proxy": "",
      "respect_robots": true
    }
  },
  "mcpServers": {},
  "plugins": {},
  "permissions": { "level": "strict", "unattended": false },
  "limits": {
    "max_turns": 32,
    "max_context_tokens": 64000,
    "compact_threshold": 0.7,
    "tool_output_chars": 20000,
    "tool_timeout_sec": 60
  }
}
```

## 设置窗与 config 的关系

- 「Profile」区：当前 profile 展示 + 新建 / 复制当前 / 切换（重启生效）。
- 「模型」页：当前模型名 + 原生思考与推理档位 + 有序降级链（每项 `provider / model`，可增删与上移下移；链窄于当前模型能力时上方给一行警示）。
- 「模型服务」页：provider 管理（**折叠卡片**，同时只展开一张）——名称/协议/接口地址/密钥池/**模型列表**/tokenizer/不认参数，+ 设为主 + 删除 + 连通性「测试」（真实最小请求，8s 超时）。模型列表每行 = **启用**勾选 + 名字 + 上下文窗口 + 三能力勾选 +「探测能力」；表头有「共 N 个 · 已启用 M 个」与「获取模型」，下面一行是本卡片的拉取反馈，尾部「＋ 添加模型」。切进本页时会**自动拉一轮**各家的 `/models`（每窗一次），结果全部并进列表、新增行默认未启用；行数不设上限，超过 8 行分页。密钥值**只写不回显**（留空 = 保持原值）；能力勾选**动过才固化成显式声明**（未动 = 保持原形态），「探测能力」以**编辑区现值**探（模型还没应用也能探，结论先回填勾选）。「应用 provider 变更」整体写回，重启生效。
- 「权限与思考」「外观与呼出」：热生效项照旧；模型名经 `/model` 热切换。
- 「输出」「限额」「人格模块树」：保存即写回；人格组合**只影响之后新建的会话**，且**重启后生效**（与 `system_prompt` 同口径）。
- 「MCP 与插件」：只读清单；「联网」页 = 搜索与抓取两组提供商（有序列表，可增删与上下移）+ 本地抓取参数（条数、正文上限、代理、UA、robots、超时）。每条可自己填端点与密钥，也可以选「借用」再点「填入」把某个 `model.providers` 条目的端点与密钥引用抄过来（永不抄值）。密钥框只写不回显（留空 = 保持原引用）；保存前按启动同一口径校验，写坏的组合进不了盘，原文件不动。
- 「语音」页 = 识别与合成两份独立配置（服务、端点、密钥、模型）+ 音色/音色描述/风格/标签 + 试听。同样支持「借用」+「填入」；密钥框只写不回显，填明文会先收进本机密钥库（识别存 `AQUARIUS_ASR_API_KEY`、合成存 `AQUARIUS_TTS_API_KEY`）。服务与模型的改动重启生效，音色与风格对此后的播报立即生效。
- 「高级」页 = config.json **原文整文件编辑**（保存前 JSON 校验，解析失败原文件不动；未知键天然保留，Q10）。编辑区随窗口高度撑满，不设固定高度。

## 从旧形状迁移

启动时若检测到下列任一形状，会**报错退出并打印改写示例**（不做自动迁移，避免程序偷偷改你的配置）：

| 旧形状 | 改成 |
|---|---|
| `"api_key": "secret:K"`（provider 上单值） | `"keys": [ { "api_key": "secret:K" } ]`（多把即密钥池） |
| `"models": ["m1","m2"]`（字符串数组） | `"models": [ { "name": "m1" }, { "name": "m2" } ]` |
| `"vision": true`（provider 上） | 挪到对应模型：`"models": [ { "name": "m1", "caps": ["tools","vision"] } ]` |
| `"fallback": ["b"]`（provider 名字数组） | `"fallback": [ { "provider": "b", "model": "mb" } ]`（省略 `model` = 该 provider 首个启用的模型） |

上下文窗口是**新**能力，没有旧形状要迁：想知道模型能吃多少就加 `"context_window": 128000`，
不加就仍按 `limits.max_context_tokens` 走。
