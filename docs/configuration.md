# 配置参考

> 本文衍生自 [DESIGN.md](../DESIGN.md) §8/§9；**冲突以 DESIGN.md 为准**。
> 配置文件：`~/.aquarius/config.json`（`-data` 可改数据目录）。

## 三级覆盖

```
CLI flags  >  环境变量（AQUARIUS_*）  >  config.json
```

| 来源 | 项 |
|---|---|
| flag | `-data`（数据目录）、`-model`（覆盖 model.name）、`-base-url`（覆盖 model.base_url） |
| 环境变量 | `AQUARIUS_MODEL`、`AQUARIUS_BASE_URL`；以及 `api_key` 引用的任意密钥变量 |
| config | 下表全部字段 |

首次运行会写入一份可运行模板并退出；改好后重新运行。

## 字段全表

### model（必填项）

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `model.provider` | string | `openai-compatible` | 预留标识，当前适配器只实现 OpenAI 兼容 |
| `model.name` | string | `gpt-4o-mini` | 模型名，空值启动报错 |
| `model.base_url` | string | `https://api.openai.com/v1` | Chat Completions 端点，空值启动报错 |
| `model.api_key` | string | `secret:AQUARIUS_OPENAI_KEY` | `secret:<环境变量名>` 引用（`port.Secrets` 按名取用）**或明文**（D35：启动打印警告、不回显密钥）；**值为空时回落默认引用** `secret:AQUARIUS_OPENAI_KEY`；文件内值优先 |
| `model.tokenizer` | string | `""` | 本地 tokenizer.json 路径（文件或目录）：启用**精确 token 计数**（三级计数链②，D26）；空 = 通用估算；路径错误启动即报因 |
| `model.think` | bool | `true`（键缺失） | 原生思考**总开关**（D34）：`off` 时 `reasoning_effort` 与 `enable_thinking` 一律不发；`/think` 切换写回本字段 |
| `model.reasoning_effort` | string | `""` | 推理档位（D34）：`minimal` / `low` / `medium` / `high`，空 = 不发送（交服务端默认）；`/effort` 切换写回本字段（`off` 即清除）；非法值启动报错 |
| `model.think_tool` | bool | `false` | `think` 草稿工具（DESIGN §4.3）是否列给模型——**默认隐藏**（D34），需要时配置启用（改后重启生效） |
| `model.unsupported_params` | []string | `[]` | 服务端已知不认的请求参数名单（D34 自动记录：400 点名 → 剥离重试成功 → 落盘；启动注入，之后直接省略）。可手工加字段名让某字段永久禁发 |

### ui

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `ui.kind` | string | `tui`（模板与键缺省） | `tui` = bubbletea TUI（D33 MVP：转写区/流式/历史/Confirm/状态行，轻 markdown）；`repl` = 行式（测试/e2e 后端）；其他值启动报错 |

### system_prompt（人格）

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `system_prompt` | string | `""`（内置默认） | 新建会话时**快照进 persona 首节点**（system 角色）；改它只影响之后新建的会话，已有会话的人格在其树内首节点上 |

### permissions

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `permissions.level` | string | `strict` | `read-only` / `strict` / `permissive` / `full-access`；非法值启动报错；`/permission` 切换会**写回**本字段（tmp+rename 原子换入，其余键保留） |

等级语义（矩阵格 = 免确认范围，矩阵外逐次确认）：

| 等级 | 特权目录 sandbox | 其他目录 | 工具调用 |
|---|---|---|---|
| `read-only` | r | r | Confirm 逐次 |
| `strict` | rw | r | Confirm 逐次 |
| `permissive` | rw | rw | Confirm 逐次 |
| `full-access` | rw | rw | 全免 |

两列分工、互不叠加：文件类看路径格（读全盘免确认），执行类看工具列（Safe 免、
Confirm 仅 full-access 免）。**执行接入 M2 起生效**（ToolRunner 每次执行现读等级）；
`network`/`secret` 授权流不随等级变化。

### limits

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `limits.max_turns` | int | 8 | 单次请求内"生成+工具"循环上限 |
| `limits.max_context_tokens` | int | 64000 | 上下文预算（硬保底由截断装饰器执行，D14，装配根叠加） |
| `limits.compact_threshold` | float | 0.7 | 自动压缩阈值（×max_context_tokens，M2 自动轨消费） |
| `limits.tool_output_chars` | int | 20000 | 工具结果截断（ToolRunner，M2 消费） |
| `limits.tool_timeout_sec` | int | 60 | 单次工具执行超时秒（ToolRunner，M2 消费） |

### output（M3 起）

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `output.notify` | bool | 模板 `true`；键缺失 = `false` | 已提交的回答触发系统通知（Windows PowerShell 气泡 / `notify-send` / `osascript`，发送失败只记日志）。扇出在装配根的 Presenter 装饰器上（D28），app 不感知输出器 |
| `output.tts` | bool | `false` | 语音播报：解析但不启用（ASR/TTS 移入 backlog，DESIGN D27） |

### mcpServers / plugins（M4 起）

`mcpServers` 声明 MCP server；`plugins` 存**启停与授权状态**（两种发现源共用，D31——
声明也可来自 `~/.aquarius/plugins/<name>/plugin.json`，状态一律在 config）。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `mcpServers.<name>.transport` | string | — | `stdio` 或 `streamable-http`（D30），缺省报错 |
| `mcpServers.<name>.command` / `args` / `env` | string / []string / object | — | stdio 形态：启动命令与环境（值可用 `secret:<环境变量名>`） |
| `mcpServers.<name>.url` / `headers` | string / object | — | streamable-http 形态：端点与请求头（值同样可用 `secret:` 引用，请求时注入不落明文） |
| `mcpServers.<name>.capabilities` | []string | `[]` | 所需能力声明（如 `network`），**首次使用弹确认**后写入下表 granted |
| `mcpServers.<name>.risk` | string | `safe` | `safe` / `confirm`：后者工具逐次走 `[y/N]` 确认（不随 granted 放行） |
| `plugins.<name>.enabled` | bool | `true` | 启停；`/plugin enable\|disable` 写回本字段（tmp+rename 原子换入），即时生效 |
| `plugins.<name>.granted` | []string | `[]` | capability allowlist（grant 确认的落盘处，D31） |

### 暂未消费的键（模板自带，随里程碑启用）

`input`（asr/mic）、`output.tts`。
未知键解析时忽略，**写回时原样保留**。

## 密钥规则

```json
"api_key": "secret:AQUARIUS_OPENAI_KEY"
```

- 推荐：配置里只有引用名 `secret:<环境变量名>`，真实值存于同名环境变量，进程内按名取用（`port.Secrets`）。
- 明文密钥（D35）：允许直接填进 `model.api_key`，**启动打印警告**（不回显密钥值）；建议此时确保 config 不被提交/同步。
- 值为空（或整项删除）→ 自动回落默认 `secret:AQUARIUS_OPENAI_KEY`（环境变量缺失时启动报因）。
- 无论哪种形态，密钥都不进会话树、附件、日志；stdio 插件子进程环境会剔除 `AQUARIUS_*` 变量。

## 完整模板（首次运行生成）

```json
{
  "model": {
    "provider": "openai-compatible",
    "name": "gpt-4o-mini",
    "base_url": "https://api.openai.com/v1",
    "api_key": "secret:AQUARIUS_OPENAI_KEY",
    "tokenizer": "",
    "think": true,
    "reasoning_effort": "",
    "think_tool": false,
    "unsupported_params": []
  },
  "ui": { "kind": "tui" },
  "system_prompt": "",
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
