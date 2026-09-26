<p align="center">
  <img src="assets/icon/icon-128.png" alt="Aquarius 图标" width="128">
</p>

# Aquarius

面向个人的极简 AI 助手 —— 对话优先、多模态出入、文档记忆、可插拔工具与模型。Go 编写，交付单个二进制。

## 特性

- **会话树**：消息不可变，"修改"即创建同级新节点（`/edit --keep` 可保留后续历史），随时分支、回溯、对比。
- **多模态**：文本 / 图片 / 文档 / 语音输入，文本 / 语音 / 通知输出；输入输出方式可插拔（如语音 = ASR 适配器）。
- **记忆 = 文档**：markdown 文件即记忆，你可直接编辑；模型经工具读写。
- **内置通用能力**：文件操作、终端执行、后台任务。没有工作区之类的重抽象。
- **可扩展**：进程外扩展统一走 [MCP](https://modelcontextprotocol.io)，接入现成 MCP server 即得新工具；
  内置能力与三方插件同权（内置不享特权）。
- **本地优先**：数据全部在 `~/.aquarius/`，卸载 = 删二进制 + 删目录。

## 状态

开发中，里程碑 **M0 骨架 + M1 树交互 + M2 工具与记忆 + M3 任务与多模态 + M4 MCP 与 TUI**已落地（DESIGN §12）：实 Root 会话树 +
性质测试、persona 首节点、**三轨上下文压缩**（`/compact` 手动轨、超阈值自动轨、
`context_compact` 自触发轨；失败回退最旧裁剪）、**权限等级矩阵**
（read-only/strict/permissive/full-access + `/permission`，且经 ToolRunner 在工具链路生效）、
全量端口、storejson 持久化、OpenAI 兼容流式适配器、Turn 循环、repl 界面与单二进制装配——
`go build ./cmd/aquarius` 即可跑通纯文本对话。
M1 提供 **Revise 两模式与分支导航**：`/goto <id>`（唯一前缀匹配回溯）、
`/edit <id> [--keep] <文本>`（缺省 Fresh 另起节点，`--keep` Carry 边转移保留后续历史）、
`/branch [id]`（同级分叉与下级视图，Root 显示顶层消息）、`/rm <id>`（二次确认后剪枝，`-yes` 跳过确认），
并有 golden 回放测试覆盖。
M2 提供**工具与记忆**：内置工具 memory_*/file_*/think/context_compact 经 **ToolRunner 门面**
（权限矩阵判定 → 逐次确认 → 超时 → 结果裁剪）执行；记忆 = 全局 `memories.md` + 会话
`conversations/<id>.memory.md`（模型经工具读写，`/memory` 用系统编辑器直开）；
**三级 token 计数链**（服务端实测 usage → 适配器本地 tokenizer 精确计数 → 通用估算+自校准）
支撑自动压缩与 `/usage` 用量查看。
M3 提供**任务与多模态**：`term_exec` 同步执行与 `job_start` 后台任务（独立进程、日志落盘
`~/.aquarius/jobs/`，`/jobs` 列表/查日志/终止）、**附件库**（sha256 内容寻址 + 启动 GC）、
文件/剪贴板**摄取管线**（RawInput → Part 入树 → 装配内联图片字节与文档提取文本；
触发命令面按 D27/D33 留 §14）、`notify` 输出器（已提交回答触发系统通知，`output.notify`）。
命令：`/new /list /title /goto /edit /branch /rm /compact /permission /memory /usage /jobs /model /plugin /quit /exit /help`（外加 MCP prompts 的 `/mcp:<server>:<prompt>` 动态命令）。
M4 提供**MCP 接入与 TUI**：Tier-2 MCP server（**stdio + streamable HTTP** 双传输，D30 官方
go-sdk）经 `mcpgate` 投影为 `mcp:<server>:<tool>` 工具（与内置工具同权过权限矩阵）、
resources 只读并入记忆索引、prompts 暴露为动态命令；插件宿主负责发现（config `mcpServers` +
`plugin.json`，D31）、capability 首用授权（grant 写回 `plugins.<name>.granted`）、
崩溃限次重启与 `/plugin` 管理；`/model` 查看/热切换模型并写回 config（D32）。
**TUI**（bubbletea，D33）为默认前端（`ui.kind=tui`）：转写区 + 流式 + 命令历史 + Confirm
对话 + 状态行（模型/权限/用量）+ 轻 markdown（glamour），repl 保留为测试/e2e 后端，
GUI 换壳契约已定稿（§15/D43：Gio 悬浮球前端，M5）。横切**装饰器链**（重试/硬保底截断/审计 JSONL，
D14）在装配根叠加。验收：stdio 与 streamable HTTP 各接现成 MCP server 全链路
（`AQUARIUS_E2E_REAL_MCP=1 go test ./cmd/aquarius -run RealMCP` 可重跑）。
后续：**M5 GUI 前端**（D43/§15：spike 已过——形裁悬浮窗/托盘/快捷键/定位实测见 §15.6；
`uigui` 骨架（uiFrontend 六面 + 桥接 + 渲染状态机 + 窗口壳，`ui.kind=gui`）已落位，
悬浮球形态/托盘常驻/完整 markdown 消息面按 §15 继续推进）；Tier-1 插件
（`pluginapi/v1`，D29）与语音输入/播报（D27）按 §14 推进。

## 文档

| 文档 | 内容 |
|---|---|
| [DESIGN.md](DESIGN.md) | **权威设计文档**：定位、领域模型（会话树/Revise）、端口设计、插件架构（MCP）、权限模型、里程碑与决策记录 |
| [AGENTS.md](AGENTS.md) | AI 编码代理协作指南：硬性规则、命令、测试与提交要求 |
| [docs/overview.md](docs/overview.md) | 架构导读：10 分钟地图——内核三事、依赖铁律、目录与端口矩阵 |
| [docs/usage.md](docs/usage.md) | 使用手册：命令参考、三轨压缩、权限等级、常见问题 |
| [docs/configuration.md](docs/configuration.md) | 配置参考：config.json 逐字段、三级覆盖、密钥规则 |
| [docs/storage.md](docs/storage.md) | 数据布局与备份：目录树、会话 JSON、.bak 恢复、旧格式处置 |
| [docs/development.md](docs/development.md) | 开发指南：门禁、测试手段、新增适配器/工具/命令分步 how-to |

> `docs/` 均为衍生视图，**冲突以 DESIGN.md 为准**；里程碑进度只看本页「状态」。

## 快速开始

```bash
go build ./cmd/aquarius
./aquarius.exe      # 首次运行生成 ~/.aquarius/config.json
```

1. 编辑 `~/.aquarius/config.json`：`model.name` / `model.base_url`（可选 `system_prompt` 人格、
   `permissions.level` 权限等级，默认 `strict`，特权目录 `~/.aquarius/sandbox`）；
2. 密钥推荐走环境变量引用：配置里写 `secret:AQUARIUS_OPENAI_KEY`，同名环境变量存真实密钥；
   也允许直接把密钥明文填进 `model.api_key`（**启动会打印警告**，值留空则自动回落上述默认引用）；
3. 重新运行，直接对话；`/help` 查看命令（含 `/compact` 上下文压缩、`/permission` 权限等级），
   `/quit`（或 `/exit`）退出。

数据全部在 `~/.aquarius/`（`-data` 可指定目录），会话树为可直接查看的一树一 JSON。

## 许可证

[Mozilla Public License 2.0](LICENSE)
