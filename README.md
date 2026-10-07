<p align="center">
  <img src="assets/icon/icon-128.png" alt="Aquarius 图标" width="128">
</p>

# Aquarius

面向个人的极简 AI 助手 —— 对话优先、多模态出入、文档记忆、可插拔工具与模型。Go 编写，交付单个二进制。

## 特性

- [x] **会话树** — 消息不可变，修改即分叉新节点，随时回溯、分支、对比
- [x] **上下文压缩** — 手动 `/compact`、超阈值自动、工具自触发三轨
- [x] **图片与文档输入** — 文件/剪贴板摄取，pdf/docx/xlsx/html 自动转文本
- [x] **通知输出** — 回答提交后触发系统通知
- [ ] **语音输入与播报** — ASR/TTS 接入，规划中
- [x] **记忆 = 文档** — markdown 文件即记忆，可直接编辑，模型经工具读写
- [x] **内置工具** — 文件操作、终端执行、后台任务
- [x] **MCP 扩展** — [stdio + streamable HTTP](https://modelcontextprotocol.io) 双传输，接入现成 server 即得新工具，与内置工具同权
- [ ] **进程内插件** — Go 级进程内扩展，规划中
- [x] **本地优先** — 数据全部在 `~/.aquarius/`（profile 整目录隔离），卸载 = 删二进制 + 删目录
- [x] **TUI + GUI** — bubbletea 终端界面；Gio 悬浮球为默认前端（托盘常驻、全局快捷键、深浅主题跟随）
- [x] **跨平台（功能分级）** — GUI 平台层已抽出（D111/S4b）：Windows 为完整形态（整窗 ULW 半透明/羽化、托盘、全局热键、原生菜单）；**非 Windows 为可用降级**——常规不透明窗 + 输入/转写/设置窗可用、深浅色与文件选择框走系统命令，**无**半透明/羽化、托盘、全局热键与原生右键菜单（缺失项在转写区提示一次）。详见 [DESIGN §15.6](DESIGN.md)

## 快速开始

```bash
go build ./cmd/aquarius
./aquarius.exe      # 首次运行生成 ~/.aquarius/config.json 指针 + profiles/default/ 模板
```

1. 编辑 `~/.aquarius/profiles/default/config.json`：在 `model.providers` 填端点与模型
   （`base_url` / `models` / `api_key`；多 provider + 降级顺序 `model.fallback`，见[配置参考](docs/configuration.md)）；
2. 密钥推荐写 `secret:AQUARIUS_OPENAI_KEY` 并存入同名环境变量（也允许明文填入 `api_key`，启动会打印警告）；
3. 重新运行即可直接对话；`/help` 查看命令，完整命令参考见[使用手册](docs/usage.md)。

数据全部在 `~/.aquarius/`（`-data` 可指定目录）：根 `config.json` 仅存当前 profile 指针，
主配置与会话/记忆/附件都在 `profiles/<name>/` 整目录隔离（`--profile` 或设置窗切换，重启生效）；
会话树为可直接查看的一树一 JSON。

## 文档

| 文档 | 内容 |
|---|---|
| [DESIGN.md](DESIGN.md) | **权威设计文档**：定位、领域模型（会话树/Revise）、端口设计、插件架构（MCP）、权限模型与里程碑 |
| [docs/decisions.md](docs/decisions.md) | **决策记录 ADR**：逐条决策与否决方案（编号范围见该文索引表） |
| [docs/roadmap.md](docs/roadmap.md) | **进度与阶段规划**：TODO 盘点、S1–S8 分步路线（含 S1b/S1c/S2b/S4b 半步）与落地状态（里程碑进度以此为准） |
| [AGENTS.md](AGENTS.md) | AI 编码代理协作指南：硬性规则、命令、测试与提交要求 |
| [docs/overview.md](docs/overview.md) | 架构导读：10 分钟地图——内核三事、依赖铁律、目录与端口矩阵 |
| [docs/usage.md](docs/usage.md) | 使用手册：命令参考、三轨压缩、权限等级、常见问题 |
| [docs/configuration.md](docs/configuration.md) | 配置参考：config.json 逐字段、三级覆盖、密钥规则 |
| [docs/storage.md](docs/storage.md) | 数据布局与备份：目录树、会话 JSON、.bak 恢复、旧格式处置 |
| [docs/development.md](docs/development.md) | 开发指南：门禁、测试手段、新增适配器/工具/命令分步 how-to |

> 开发进行中，进度与路线见 [docs/roadmap.md](docs/roadmap.md)。`docs/` 各篇均为 DESIGN.md 的衍生视图（`decisions.md` 为权威决策记录），**冲突以 DESIGN.md / docs/decisions.md 为准**。

## 许可证

[Mozilla Public License 2.0](LICENSE)
