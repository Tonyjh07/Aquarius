# 配置参考

> 本文衍生自 [DESIGN.md](../DESIGN.md) §8/§9；**冲突以 DESIGN.md 为准**。
> D110（破坏性改造）起配置**以 profile 为隔离单位**：
>
> - `<data>/config.json` = 当前 profile **指针**（`{"profile": "<name>"}`）；
> - `<data>/profiles/<name>/config.json` = **主配置**（本表全部字段）；
> - 会话/记忆/附件/任务/审计/沙盒/位置记忆全部随 profile 整目录隔离。
>
> `<data>` 默认 `~/.aquarius`（`-data` 可改）。

## profile 选择（D110①③）

```
--profile flag  >  <data>/config.json 指针        （两者只决定「读哪个 profile」）
```

- `--profile <name>`：进程级覆盖指针（**不写回**）；指向不存在的 profile 启动即报错并列出可用名，不自动创建。
- 指针文件不存在 = 首次运行：写指针（指向 `default`）+ `profiles/default/config.json` 模板后退出；某 profile 的 `config.json` 缺失时同样只写该 profile 的模板。
- **切换 = 重启生效**（Q1）：进程内不热切。设置窗「Profile」区可新建 / 复制当前 / 切换（切换 = 原子写指针）；「复制」仅复制配置（新 profile 以当前 profile 的 config.json 为底，**不带会话/记忆/附件数据**）。
- 旧单文件 config（D110 前布局）启动即显式报 `ErrLegacyConfig` 并给出手工迁移步骤——**不做自动迁移**（无生产数据，D110④），沿 storejson `ErrLegacyFormat` 口径。

## 三级覆盖

profile 选择在覆盖链之外；**profile 内配置值**的覆盖链为：

```
CLI flags  >  环境变量（AQUARIUS_*）  >  profiles/<name>/config.json
```

| 来源 | 项 |
|---|---|
| flag | `-data`（数据目录）、`-profile`（profile 选择）、`-model`（覆盖 model.name）、`-base-url`（覆盖 **primary provider** 的 base_url——调试逃生口，D110 修订②） |
| 环境变量 | `AQUARIUS_MODEL`（覆盖 model.name）、`AQUARIUS_BASE_URL`（同 `-base-url`）；以及各 provider `api_key` 引用的任意密钥变量 |
| config | 下表全部字段 |

首次运行会写入一份可运行模板并退出；改好后重新运行。

## 字段全表

### model（D110②：provider 列表化，原平铺字段 provider/base_url/api_key/tokenizer/unsupported_params 已退役）

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `model.providers` | []object | — | provider 列表，至少一条，`name` 唯一。每条：`name`（唯一标识，primary/fallback 按名引用）、`base_url`（OpenAI 兼容端点，非空）、`api_key`（见密钥规则，逐 provider 独立）、`models`（模型清单，**首个 = 该 provider 缺省请求模型**——fallback 降级时使用，D110 修订③）、`vision`（该 provider 的模型是否接受图片输入；缺省 `false`——服务端 `/models` 不返回模态，只能由用户声明，见 D169）、`tokenizer`（本地 tokenizer.json 路径，精确计数② D26；空 = 通用估算）、`unsupported_params`（服务端不认的请求参数，D34/D42 自动记录、启动省略） |
| `model.primary` | string | 首个 provider | 当前生效 provider；不在列表内启动报错 |
| `model.fallback` | []string | `[]` | 有序降级顺序（Q5）：**仅网络类错误**（429/408/5xx/连接/超时，`port.ErrTransient` 口径）在内层同 provider 重试耗尽后触发；401/400 等配置错误不降级直接暴露；降级请求模型 = 目标 provider 的 `models[0]`；无粘态（每次调用仍从 primary 起试）；流中途断连不降级（既有 retry 边界）；降级经状态行一次性提示。名字须在 providers 内、不含 primary、无重复，否则启动报错 |
| `model.name` | string | primary 的 `models[0]` | 当前模型名（在 primary 的 models 内）；`/model` 热切换并写回本字段（provider/base_url 重启生效） |
| `model.think` | bool | `true`（键缺失） | 原生思考**总开关**（D34）：`off` 时 `reasoning_effort` 与 `enable_thinking` 一律不发；`/think` 切换写回本字段 |
| `model.reasoning_effort` | string | `""` | 推理档位（D34）：`minimal` / `low` / `medium` / `high`，空 = 不发送；`/effort` 切换写回（`off` 即清除）；非法值启动报错 |
| `model.think_tool` | bool | `false` | `think` 草稿工具是否列给模型——默认隐藏（D34），改后重启生效 |
| `model.echo_thinking` | bool | `true`（键缺失） | **思考回传开关**（D42）：思维链是否回传给提供商。键缺失 = 回传（DeepSeek 等兼容端点带 `tools` 时强制回传，缺失会 400）；显式 `false` = 不发。端点点名不认时自动剥离重试并记入该 provider 的 `unsupported_params`。改后重启生效 |

### ui

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `ui.kind` | string | `gui`（D51 模板与键缺省） | `gui` = Gio 悬浮球 GUI（D43/§15，仅 Windows）；`tui` = bubbletea TUI（D33）；`repl` = 行式（测试/e2e 后端）；其他值启动报错 |
| `ui.hotkey` | string | `""`（= `Alt+A`） | **GUI** 全局呼出/收起快捷键（§15.1）：`修饰键+键`（如 `Alt+A`）；注册失败回退 `Ctrl+Alt+A`；仅 gui 生效 |
| `ui.theme` | string | `system`（键缺失） | **GUI** 深浅主题（§15.4/D61）：`system`/`light`/`dark`，切换热生效；仅 gui 生效 |
| `ui.scale` | number | `1.0` | **GUI** 元素缩放倍率（§15.8/D90）：夹 [0.75, 2.5]；设置窗百分比档；热生效；仅 gui 生效 |
| `ui.font_size` | number | `15` | **GUI** 正文字号 sp（§15.8/D90）：最终字号 = font_size × scale；夹 [10, 28]；热生效；仅 gui 生效 |
| `ui.window_width` / `ui.window_height` | int | `0`（= 缺省 608×460dp） | **GUI** 主窗像素尺寸（§15.8/D90）；UI 侧按地板与粗界夹取；热生效；仅 gui 生效 |
| `ui.welcomed` | bool | `false`（键缺失） | **GUI** 欢迎 / 首次运行引导是否已看（D141/§15.7）：键缺失 = 未看——**首跑或标记缺失**时自动打开欢迎窗一次，完成或显式关闭即置位；入口另在设置窗 |

### prompts（人格模块树，S7/D144）

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `prompts.modules` | object | `{}` | **模块树**：`name → { text?, children?[] }`。叶 = 仅 `text`（命名片段）；组 = `children`（子模块名有序列表，可多级嵌套），亦可带 `text` 作组头 |
| `prompts.persona` | string[] | `[]` | **人格** = 有序顶层引用列表；空 = 默认 `["@default","@env"]` |

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

结果在**新建会话**时快照进 persona 首节点（Q4/D20：改动只影响新建会话，存量会话的 persona 已在
树内、可 `/edit`）。

### system_prompt（人格兜底单串）

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `system_prompt` | string | `""`（内置默认） | 作为内置模块 `@default` 的正文来源（兼容旧配置）；`persona` 不引用 `@default` 时不影响组合结果 |

### permissions

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `permissions.level` | string | `strict` | `read-only` / `strict` / `permissive` / `full-access`；非法值启动报错；`/permission` 切换**写回**本字段 |

等级语义（矩阵格 = 免确认范围，矩阵外逐次确认）：

| 等级 | 特权目录 sandbox | 其他目录 | 工具调用 |
|---|---|---|---|
| `read-only` | r | r | Confirm 逐次 |
| `strict` | rw | r | Confirm 逐次 |
| `permissive` | rw | rw | Confirm 逐次 |
| `full-access` | rw | rw | 全免 |

两列分工、互不叠加：文件类看路径格（读全盘免确认），执行类看工具列（Safe 免、
Confirm 仅 full-access 免）。**执行接入 M2 起生效**；`network`/`secret` 授权流不随等级变化。

### limits

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `limits.max_turns` | int | 8 | 单次请求内"生成+工具"循环上限 |
| `limits.max_context_tokens` | int | 64000 | 上下文预算（硬保底由截断装饰器执行，D14） |
| `limits.compact_threshold` | float | 0.7 | 自动压缩阈值（×max_context_tokens） |
| `limits.tool_output_chars` | int | 20000 | 工具结果截断 |
| `limits.tool_timeout_sec` | int | 60 | 单次工具执行超时秒（模型可经保留参数 `timeout_sec` 逐次覆盖，D38） |

### output（M3 起）

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `output.notify` | bool | 模板 `true`；键缺失 = `false` | 已提交的回答触发系统通知（发送失败只记日志，D28 扇出） |
| `output.tts` | bool | `false` | 语音播报：解析但不启用（ASR/TTS 移入 backlog，D27） |

### mcpServers / plugins（M4 起）

`mcpServers` 声明 MCP server；`plugins` 存**启停与授权状态**（声明也可来自
`profiles/<name>/plugins/<name>/plugin.json`，状态一律在 profile config）。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `mcpServers.<name>.transport` | string | — | `stdio` 或 `streamable-http`（D30），缺省报错 |
| `mcpServers.<name>.command` / `args` / `env` | string / []string / object | — | stdio 形态：启动命令与环境（值可用 `secret:<环境变量名>`） |
| `mcpServers.<name>.url` / `headers` | string / object | — | streamable-http 形态：端点与请求头（值同样可用 `secret:` 引用） |
| `mcpServers.<name>.capabilities` | []string | `[]` | 所需能力声明，**首次使用弹确认**后写入 granted |
| `mcpServers.<name>.risk` | string | `safe` | `safe` / `confirm`：后者工具逐次确认 |
| `plugins.<name>.enabled` | bool | `true` | 启停；`/plugin enable\|disable` 写回，即时生效 |
| `plugins.<name>.granted` | []string | `[]` | capability allowlist（D31） |

设置窗「MCP 与插件」为只读清单；结构化字段的编辑走「高级」页 raw JSON（Q10）。

### 暂未消费的键（模板自带，随里程碑启用）

`input`（asr/mic）、`output.tts`。
未知键解析时忽略，**写回时原样保留**（provider 条目内的无关键同样保留——编辑器按
原名合并，Q10 口径）。

## 密钥规则（逐 provider，D35/D110②/D151）

```json
"api_key": "secret:AQUARIUS_OPENAI_KEY"
```

- 推荐：配置里只有引用名 `secret:<名>`，真实值按名取用——**先查本机密钥库、未命中再查同名环境变量**（D151）。
- **本机密钥库**落 `<profile>/secrets.bin`（随 profile 隔离）：Windows 走 DPAPI（绑定当前用户账户，
  无口令、无额外密钥文件），其它平台走 AES-256-GCM + 同目录 `secrets.key`（0600）。整库加密，
  键名与值都在密文内；写前留一代 `.bak`。
- **界面填的明文会转入库**（D161）：设置窗 provider 编辑区 / 欢迎窗填明文 → 写进密钥库，
  config 里只落 `secret:<名>`（名沿用原引用名，否则按 provider 名生成 `AQUARIUS_<PROVIDER>_API_KEY`）。
  密钥库不可写时直接报错，不会静默把明文留在盘上。
- 明文密钥（D35）：允许直接填或手改 config，**启动打印警告**（不回显密钥值）；建议确保 config
  不被提交/同步，或到设置窗重填一次转入密钥库。
- 值为空（或整项删除）→ 该 provider 回落默认 `secret:AQUARIUS_OPENAI_KEY`。
- **库解不开时**（profile 拷到别的机器、换了账户、文件损坏）：打一行警告后**回落同名环境变量**继续
  启动（不阻断）；原文件原地保留，不会被改写。
- 备份 `secrets.bin` 到另一台机器**解不开**——这是绑定本机/本账户的预期行为。
- 无论哪种形态，密钥都不进会话树、附件、日志；stdio 插件子进程环境剔除 `AQUARIUS_*` 变量。
- 设置窗 provider 编辑区密钥**只写不回显**：留空 = 保持原值（按编辑前原名匹配保留，改名不丢密钥）。
- MCP 声明的 `env`/`headers` 值同样可用 `secret:<名>` 引用；任意名字可在设置窗「密钥库」页新增/
  覆盖/删除，列表只显示名字与尾 4 位掩码。

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
      { "name": "openai", "base_url": "https://api.openai.com/v1",
        "api_key": "secret:AQUARIUS_OPENAI_KEY",
        "models": ["gpt-4o-mini"], "vision": false, "tokenizer": "",
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
  "ui": { "kind": "gui", "theme": "system", "scale": 1.0, "font_size": 15, "window_width": 0, "window_height": 0 },
  "system_prompt": "",
  "prompts": { "modules": {}, "persona": [] },
  "input": { "asr": "whisper-api", "mic": true },
  "output": { "tts": false, "notify": true },
  "mcpServers": {},
  "plugins": {},
  "permissions": { "level": "strict" },
  "limits": {
    "max_turns": 8,
    "max_context_tokens": 64000,
    "compact_threshold": 0.7,
    "tool_output_chars": 20000,
    "tool_timeout_sec": 60
  }
}
```

## 设置窗与 config 的关系（S4/D110③）

- 「Profile」区：当前 profile 展示 + 新建 / 复制当前 / 切换（重启生效）。
- 「模型服务」区：provider 列表**全量编辑**（名称/接口地址/密钥/模型列表/tokenizer/不认参数）+ 设为主 + 删除 + 连通性「测试」（真实最小请求 `/models`，8s 超时）；「应用 provider 变更」整体写回，重启生效。
- 「权限与思考」「外观与呼出」：热生效项照旧；模型名经 `/model` 热切换。
- 「输出」「限额」「人格模块树（S7/D144）」：保存即写回；人格组合**只影响之后新建的会话**，且**重启后生效**（与 `system_prompt` 同口径）。
- 「MCP 与插件」：只读清单；「高级」页 = config.json **原文整文件编辑**（保存前 JSON 校验，解析失败原文件不动；未知键天然保留，Q10）。
