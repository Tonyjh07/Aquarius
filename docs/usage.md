# 使用手册

> 本文衍生自 [DESIGN.md](../DESIGN.md) §7.1/§7.3/§9，并与当前实现对齐；
> **冲突以 DESIGN.md 为准**。配置字段见 [configuration.md](configuration.md)，数据与备份见 [storage.md](storage.md)。

## 快速上手

```bash
go build ./cmd/aquarius
./aquarius.exe        # 首次运行生成 ~/.aquarius/config.json 后退出
# 填 model.name / model.base_url，设置密钥环境变量，重新运行
```

进入 REPL 后直接输入文本即对话；`/help` 看命令；`/quit`（或 `/exit`）退出；Ctrl+C 取消当前生成
（已生成部分以 `cancelled` 终态入库，可 `/edit` 从该节点重试）。

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
| `/quit` `/exit` | 退出（`/exit` 为别名） |
| `/help` | 命令帮助 |

> 节点 ID 支持唯一前缀匹配；自身、同级与下级节点 ID 可用 `/branch` 查看（逐层下钻可达任意深度）。
> "修改"永不改写旧消息：Fresh 另起节点、Carry 边转移（后代本身不变），会话树始终保持不可变。

**尚未启用**（输入会提示对应里程碑）：`/model` `/jobs` `/plugin` —— 随 M3/M4。

## 内置工具（M2 起，模型自行调用）

模型可在回答过程中调用工具；**执行前一律过 ToolRunner**：权限矩阵判定 → 矩阵外逐次
`[y/N]` 确认（`-yes` 全免）→ 单次超时（`limits.tool_timeout_sec`）→ 结果按
`limits.tool_output_chars` 裁剪。拒绝/超时/失败都以 `OK=false` 回填给模型，不中断对话。

| 工具 | 说明 | Risk |
|---|---|---|
| `memory_list` / `memory_read` / `memory_search` | 记忆索引/读取/关键词检索（只见全局 + 当前会话两份） | Safe |
| `memory_write` | 写入记忆（`mode=append` 缺省 / `overwrite`；strict 下路径格外需确认） | Confirm |
| `file_read` / `file_list` / `file_search` | 读文件 / 列目录 / 按文件名递归搜索（**绝对路径**，读全盘免确认） | Safe |
| `file_write` / `file_delete` | 覆盖写 / 删除（写按权限矩阵路径格判定） | Confirm |
| `think` | 显式整理思路（no-op，内容随调用入树） | Safe |
| `context_compact` | 触发上下文压缩（等价 `/compact`，自我管理上下文） | Safe |

## 上下文压缩（三轨特色功能）

会话树不可变，压缩不删历史——它生成一条 **system 摘要节点**作为"水位"：此后装配
`[persona] + [最新摘要] + [摘要之后]`，摘要之上（persona 除外）的历史不再发给模型。
被裁掉的原文仍在树上，随时可回溯（`/goto` 回到压缩节点之前即可"撤销"压缩效果）。

- **手动轨（已可用）**：`/compact`
  - 摘要之上没有新历史 → 提示"无需压缩"，不花生成费用；
  - 成功 → `已压缩 N 条历史 → 1 条摘要（in=X out=Y tokens）`；
  - 取消/失败 → 会话树无损，可重试。
  - 多次压缩链式吸收：新摘要把旧摘要一并吞掉，上下文永远只带最新一条。
- **自动轨（M2 起可用）**：每轮生成前按**三级 token 计数链**估算上下文
  （①已发生的服务端实测 usage 自校准 → ②适配器本地 tokenizer 精确计数，
  配置 `model.tokenizer` 即启用 → ③通用估算 ASCII÷4 / CJK÷1.5 / 其他÷2），
  达 `limits.compact_threshold`（默认 0.7 × `max_context_tokens`）自动压缩——
  每次对话至多一次；失败回退"最旧裁剪"（保人格与最近、丢中间，`[notice]` 提示省略条数）。
- **自触发轨（M2 起可用）**：模型经 `context_compact` 工具（Safe）自我管理上下文。

persona（人格）恒回传：它是会话首节点（system 角色），源自 config 的 `system_prompt`
（空则用内置默认），可用 `/edit <persona-id> --keep <文本>` 改写（Carry 保留整棵对话树）。

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
- **启动报"model.api_key 必须是 secret:<环境变量名>"**：禁止明文密钥入配置，
  改成 `"api_key": "secret:AQUARIUS_OPENAI_KEY"` 并设置同名环境变量。
- **启动报未知权限等级**：`permissions.level` 只接受
  `read-only|strict|permissive|full-access`。
- **`ui.kind` 报未支持**：M0 只有 repl；bubbletea TUI 在 M4。
- **想换模型/端点**：改 config 的 `model.name`/`model.base_url`，或用
  `-model`/`-base-url` flag、`AQUARIUS_MODEL`/`AQUARIUS_BASE_URL` 环境变量临时覆盖。
