# 数据布局与备份
<!-- docs-sync: 2026-10-27 @ e378a9a -->

> 本文与 `storejson` 的落盘实现对齐；**设计上有冲突以 [DESIGN.md](../DESIGN.md) 为准**。
> 原则：**本地优先、用户数据主权**——全部明文，卸载 = 删二进制 + 删目录，
> 任何文件都可用编辑器直接查看修改。配置字段的逐项语义见 [configuration.md](configuration.md)，
> 本文只管「磁盘上长什么样」。

## 目录布局

```
~/.aquarius/                     # -data 可整体改指
├── config.json                  # 当前 profile 指针：{"profile": "<name>"}
└── profiles/<name>/             # profile 整目录隔离（会话/记忆/附件/密钥/技能都在里面）
    ├── config.json              # 主配置（写回 = 随机临时文件 + 原子换入；字段见 configuration.md）
    ├── sandbox/                 # Agent 特权目录（写这里免确认，启动自动创建）
    ├── memories.md              # 全局记忆（markdown 单文件；/memory 直开）
    ├── conversations/           # 会话树，一会话一 JSON
    │   ├── <id>.json            # 当前代
    │   ├── <id>.json.bak        # 上一代（Save 换代前留一代）
    │   ├── <id>.json.tmp        # 写入中的临时文件（正常结束不存在）
    │   └── <id>.memory.md       # 该会话的会话记忆（随会话就近存放）
    ├── jobs/<任务号>.log        # 后台任务日志（命令头 + 输出 + 退出标记行；任务表只在内存）
    ├── audit.log                # 审计日志（JSONL：每次模型调用/工具调用一行）
    ├── audit.log.1              # 上一代审计日志（超过 4 MB 轮转一代）
    ├── attachments/<sha256>     # 内容寻址附件（同内容去重；启动按全量会话引用清扫）
    ├── gui_pos.json             # 主窗位置记忆
    ├── secrets.bin              # 本机密钥库（密文；另留一代 secrets.bin.bak）
    ├── secrets.key              # 密钥库的加密密钥（仅非 Windows 平台；0600）
    ├── skills/                  # 技能目录（放这儿即被认出，启动自动创建）
    ├── skill-cache/             # 技能安装的下载缓存
    ├── skill-installs.json      # 技能安装台账（装了哪些、从哪儿装的）
    ├── characters/              # 角色卡（只读导入；一个角色一个目录）
    │   └── <slug>/
    │       ├── card.json        # 原卡 JSON 逐字节（导入后不再改写）
    │       ├── meta.json        # 显示名 / slug / 来源 / spec / 导入时间 / 警告 / 头像文件名
    │       ├── avatar.png       # 卡内嵌头像（没有则无此文件）
    │       └── source.charx     # 仅 .charx 来源：原压缩包整份留档
    └── plugins/<name>/plugin.json  # MCP server 声明
```

- **profile 切换**：`--profile <name>` 进程级覆盖，或设置窗切换（原子写根指针）——均重启生效。
  切换 = 换整目录，会话 / 记忆 / 附件 / 密钥互相隔离。
- **旧单文件 config**（目录化之前的布局）启动即显式报 `ErrLegacyConfig` 并给手工迁移步骤，
  **不做自动迁移**。
- `conversations/` 是唯一会被 `List` 扫描的目录：只认 `.json` 后缀，`.bak` / `.tmp` 不参与列表。
- 除 `config.json` / `memories.md` / `secrets.*` 外，其余按需创建（没有后台任务时就可能没有 `jobs/`）。

## 会话 JSON 结构

一树一文件，明文缩进。核心形态（节选，实际含完整节点）：

```json
{
  "id": "01JX...AAA",
  "title": "讲个笑话",
  "nodes": {
    "01JX...AAA": { "id": "01JX...AAA", "parent": "", "role": "root",
                     "usage": { "input_tokens": 0, "output_tokens": 0, "cost_usd": 0 },
                     "created_at": "2026-09-24T10:00:00Z" },
    "01JX...BBB": { "id": "01JX...BBB", "parent": "01JX...AAA", "role": "system",
                     "content": [{ "kind": "text", "text": "你是 Aquarius…" }] },
    "01JX...CCC": { "id": "01JX...CCC", "parent": "01JX...BBB", "role": "user",
                     "content": [
                       { "kind": "text", "text": "这张图里有啥" },
                       { "kind": "image", "ref": { "hash": "3f9c…", "mime": "image/png",
                                                   "name": "shot.png", "size": 118374 } }
                     ] },
    "01JX...DDD": { "id": "01JX...DDD", "parent": "01JX...CCC", "role": "assistant",
                     "content": [
                       { "kind": "thinking", "text": "先看图。", "signature": "anthropic:…" },
                       { "kind": "tool", "tool": {
                           "call_id": "call_1", "name": "file_read",
                           "args": { "path": "C:\\…\\shot.png" },
                           "result": { "call_id": "call_1", "ok": true, "output": "…" } } },
                       { "kind": "text", "text": "图里是一只猫。" }
                     ],
                     "outcome": "done", "model": "gpt-4o-mini",
                     "usage": { "input_tokens": 12, "output_tokens": 34, "cost_usd": 0 } }
  },
  "children": { "": ["01JX...AAA"], "01JX...AAA": ["01JX...BBB"], "…": ["…"] },
  "head": "01JX...DDD",
  "character": "yuki",          // 角色卡 slug（键缺失/空 = 普通会话）
  "tail": "Stay in character.", // 历史之后追加的 system 文本（键缺失/空 = 无）
  "revised_from": { "01JX...DDD": "01JX...CC1" },
  "created_at": "…", "updated_at": "…"
}
```

要点：

- **`role` 五种**：
  - `root`：唯一根，`id` = 会话 ID、空内容、`parent` 为空；`children` 里键 `""` 只挂它。
  - `system`：会话首节点 = 人格（persona）；水位处的 = `/compact` 摘要（都会切断其上历史）。
  - `user` / `assistant`：普通发言；`assistant` 可携带思考分片与工具分片。
  - `inject`：注入节点（`/inject`、工具与轮询器写入的上下文），不是用户发言。
    比 `system` 多三个字段：`source`（来源，如 `user` / `job:<id>` / `tool:<name>` /
    `poller:<id>` / `plugin:<name>`）、`trigger`（落地时是否真的起了一轮响应）、
    `trust`（`""` = 外部内容，装配时套围栏；`host` = 宿主事实）。三者只用于展示、
    审计与装配，不进模型上下文。
- **`content` 分片 `kind`**：`text`（正文）、`thinking`（思考，可带 `signature` 供原样回传）、
  `tool`（调用与结果同片）、`image` / `audio` / `doc`（带 `ref` 指向附件库；
  `audio` 另带 `transcript` 转写文本）。
- **`outcome`**：`done` / `cancelled` / `error`（提交时一次定死）。
- **`head`**：游标（初始 = Root）；**`revised_from`**：修订版本链（新 → 旧）。
- **`character` / `tail`**：会话级元数据，**不是节点**——`character` 是这张会话用了哪张角色卡
  （删卡后只是查不到，键仍保留），`tail` 是每轮追加在历史之后的 system 文本。两者都不进水位、
  不受压缩与分支影响；键缺失即空。
- 节点创建后内容只读；一切变化 = 新增节点 / 增删边 / 移动 head。改「用户消息」永远产生
  新节点，旧节点留在文件里（这就是分叉能切回去的原因）。
- 写入是**整树重写**：`<id>.json.tmp` 写满 → 现有文件挪成 `.bak` → 临时文件原子换入。

## .bak 机制与恢复

`Save` 在覆盖前把现有文件挪成 `<id>.json.bak`（保留一代）：

- **主文件损坏/丢失时** `Load` 自动回退读 `.bak`；
- **误删/误改主文件**手动恢复：

```powershell
# PowerShell：把上一代恢复为当前代
Move-Item ~/.aquarius/profiles/<name>/conversations/<id>.json.bak `
          ~/.aquarius/profiles/<name>/conversations/<id>.json -Force
```

- `/rmconv`（删除整个会话）会连 `.bak` / `.tmp` 一起删——想留底先拷走；
  它**不动** `<id>.memory.md`，删完会话那份会话记忆还在，需要时手工删。
- `<id>.memory.md` 与会话 JSON 是两份独立文件：会话记忆只有 `/memory` 与 `memory_*`
  工具会写它，删会话不联动。

## 早期格式数据处置

现在的格式要求每个会话都有真实根节点。更早的原型数据（无 root 节点、`head` 为空，
或工具调用还是独立的 `tool` 节点）加载会报：

```
storejson: 发现旧格式会话: <id>, <id>——请删除对应 .json 文件后重试
```

按报因点名逐个删除**两个文件**（只删 `.json` 会被 `.bak` 回退"复活"）：

```powershell
Remove-Item ~/.aquarius/profiles/<name>/conversations/<id>.json,
            ~/.aquarius/profiles/<name>/conversations/<id>.json.bak -ErrorAction SilentlyContinue
```

没有自动迁移（已拍板破坏性切换）；旧数据如需保留，手动誊写内容到新会话。
单个坏文件不影响其余会话——`/list` 会跳过它并继续。

## 备份与迁移

- **全量备份**：整个 `~/.aquarius/` 拷走即可（全明文、无数据库、无锁文件）；
  单 profile 备份 = 拷 `profiles/<name>/` + 根指针。
- **单会话迁移**：拷 `<id>.json` 到目标机 `conversations/`；`head` / `nodes` 自洽，
  加载时会跑不变量自检，坏树直接报因。
- **换机续聊**：连同 `config.json` 一起拷；密钥不在这——本机密钥库（`secrets.bin`）
  跟着 profile 走，换机后解不开（Windows 的密文绑当前用户），配环境变量同名密钥即可
  （见 [configuration.md](configuration.md) 的密钥规则）。
- **附件**：`attachments/` 按会话里的 `ref.hash` 引用；只拷会话 JSON 不拷附件库，
  图片/文档会变成"缺失引用"。整目录备份最省事。
- **会话树自检**：加载即校验（唯一实根、引用合法、无环、Head 在树），
  被手工改坏的文件会拒绝加载并报具体不变量。
- **角色卡**：`characters/` 整个目录拷走即带走全部角色；会话只记 `character` 这个 slug，
  所以只拷 `conversations/` 的话会话照常能打开（头像回默认、`tail` 仍在文件里）。
  卡目录里的 `card.json` 是导入时的原样字节，改它等于自己造一张卡——没有校验，也不推荐。
