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
| `model.api_key` | string | `secret:AQUARIUS_OPENAI_KEY` | **只允许 `secret:<环境变量名>`**；明文启动即拒（密钥不落配置/日志/会话树） |

### ui

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `ui.kind` | string | `repl` | M0 仅 `repl`；`tui` 待 M4，其他值启动报错 |

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
Confirm 仅 full-access 免）。执行接入在 M2；`network`/`secret` 授权流不随等级变化。

### limits

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `limits.max_turns` | int | 8 | 单次请求内"生成+工具"循环上限 |
| `limits.max_context_tokens` | int | 64000 | 上下文预算（硬保底由截断装饰器执行，M4） |
| `limits.compact_threshold` | float | 0.7 | 自动压缩阈值（×max_context_tokens）；**自动轨 M2 消费**，当前仅登记 |
| `limits.tool_output_chars` | int | 20000 | 工具结果截断（ToolRunner M2 消费） |
| `limits.tool_timeout_sec` | int | 60 | 工具超时（同上） |

### 暂未消费的键（模板自带，随里程碑启用）

`memory.dir`、`input`（asr/mic）、`output`（tts/notify）、`mcpServers`、
`permissions` 之外的授权细节。未知键解析时忽略，**写回时原样保留**。

## 密钥规则

```json
"api_key": "secret:AQUARIUS_OPENAI_KEY"
```

- 配置里只有引用名；真实值存于同名环境变量，进程内按名取用（`port.Secrets`）。
- 禁止把明文密钥写进 config——启动时校验，非 `secret:` 前缀直接报错退出。
- 密钥不进会话树、附件、日志。

## 完整模板（首次运行生成）

```json
{
  "model": {
    "provider": "openai-compatible",
    "name": "gpt-4o-mini",
    "base_url": "https://api.openai.com/v1",
    "api_key": "secret:AQUARIUS_OPENAI_KEY"
  },
  "ui": { "kind": "repl" },
  "system_prompt": "",
  "memory": { "dir": "~/.aquarius/memory" },
  "input": { "asr": "whisper-api", "mic": true },
  "output": { "tts": false, "notify": true },
  "mcpServers": {},
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
