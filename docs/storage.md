# 数据布局与备份

> 本文衍生自 [DESIGN.md](../DESIGN.md) §8/§4，结合 storejson 实现；
> **冲突以 DESIGN.md 为准**。原则：**本地优先、用户数据主权**——全部明文、
> 卸载 = 删二进制 + 删目录，任何文件都可用编辑器直接查看修改。

## 目录布局

```
~/.aquarius/                     # -data 可整体改指
├── config.json                  # 主配置（写回 = tmp+rename 原子换入）
├── sandbox/                     # Agent 特权目录（权限矩阵 rw 格，启动自动创建）
├── memories.md                  # 全局记忆（markdown 单文件，D23；/memory 直开）
├── conversations/               # 会话树，一会话一 JSON
│   ├── <id>.json                # 当前代
│   ├── <id>.json.bak            # 上一代（Save 换代前留一代，D7）
│   ├── <id>.json.<随机>.tmp     # 写入中的临时文件（唯一随机名，正常结束不存在）
│   └── <id>.memory.md           # 该会话的会话记忆（随会话就近存放，D23）
├── jobs/<jobID>.log             # 后台任务日志（M3：命令头 + 输出 + 退出标记行；任务表内存态 D8）
├── audit.log                    # 审计日志（M4：JSONL，装饰器写入 LLM/工具调用的耗时与结果状态；超限轮转一代）
├── attachments/<sha256>         # 内容寻址附件（M3：同内容去重；启动时按全量会话引用 GC）
└── plugins/<name>/plugin.json   # MCP server 描述（M4 起）
```

## 会话 JSON 结构

一树一文件，明文缩进。核心形态（节选）：

```json
{
  "id": "01JX...AAA",
  "title": "讲个笑话",
  "nodes": {
    "01JX...AAA": { "id": "01JX...AAA", "parent": "", "role": "root",
                     "created_at": "2026-09-24T10:00:00Z" },
    "01JX...BBB": { "id": "01JX...BBB", "parent": "01JX...AAA", "role": "system",
                     "content": [{ "kind": "text", "text": "你是 Aquarius…" }] },
    "01JX...CCC": { "id": "01JX...CCC", "parent": "01JX...BBB", "role": "user",
                     "content": [{ "kind": "text", "text": "讲个笑话" }] },
    "01JX...DDD": { "id": "01JX...DDD", "parent": "01JX...CCC", "role": "assistant",
                     "content": [{ "kind": "text", "text": "…" }],
                     "outcome": "done", "model": "gpt-4o-mini",
                     "usage": { "input_tokens": 12, "output_tokens": 34, "cost_usd": 0 } }
  },
  "children": { "": ["01JX...AAA"], "01JX...AAA": ["01JX...BBB"], "…": ["…"] },
  "head": "01JX...DDD",
  "revised_from": {},
  "created_at": "…", "updated_at": "…"
}
```

要点（详见 DESIGN §4.1 / D19–D21）：

- **role=`root`**：唯一根，`id` = 会话 ID、空内容、`parent` 为空；
  `children` 里键 `""` 只挂 Root 自己。
- **role=`system`**：会话首节点 = persona（人格）；水位处的 = `/compact` 摘要。
- **role=`user|assistant|tool`**：普通消息 / 工具调用与结果。
- `head`：游标（初始 = Root）；`revised_from`：修订版本链（新→旧）。
- 节点创建后内容只读；一切变化 = 新增节点 / 增删边 / 移动 head。

## .bak 机制与恢复

`Save` 在覆盖前把现有文件挪成 `<id>.json.bak`（保留一代）：

- **主文件损坏/丢失时** `Load` 自动回退读 `.bak`；
- **误删/误改主文件**手动恢复：

```powershell
# PowerShell：把上一代恢复为当前代
Move-Item ~/.aquarius/conversations/<id>.json.bak ~/.aquarius/conversations/<id>.json -Force
```

- `Remove`（未来 `/rm` 会话级）与手工删除会连 `.bak` 一起删——想留底先拷走；
  `<id>.memory.md` 不随 JSON 删除联动（会话级删除落地时再一并处理，当前无此入口）。

## D19 旧格式（M0 早期数据）处置

实 Root 切换是**破坏性**的：虚拟 Root 时代的文件（无 root 节点、`head` 为空）
加载会报：

```
storejson: 发现旧格式会话（虚拟 Root，D19）: <id>, <id>——请删除对应 .json 文件后重试
```

按报因点名逐个删除**两个文件**（只删 `.json` 会被 `.bak` 回退"复活"）：

```powershell
Remove-Item ~/.aquarius/conversations/<id>.json, ~/.aquarius/conversations/<id>.json.bak -ErrorAction SilentlyContinue
```

没有自动迁移（已拍板破坏性切换）；旧数据如需保留，手动誊写内容到新会话。

## 备份与迁移

- **全量备份**：整个 `~/.aquarius/` 拷走即可（全明文、无数据库、无锁文件）。
- **单会话迁移**：拷 `<id>.json` 到目标机 `conversations/`；`head`/`nodes` 自洽，
  加载时会跑不变量自检，坏树直接报因。
- **换机续聊**：连同 `config.json` 一起拷；密钥不在这——记得设环境变量。
- **会话树自检**：加载即 `Validate`（唯一实根、引用合法、无环、Head 在树），
  被手工改坏的文件会拒绝加载并报具体不变量。
