<p align="center">
  <img src="assets/icon/icon-128.png" alt="Aquarius 图标" width="128">
</p>

# Aquarius
<!-- docs-sync: 2026-10-22 @ 4dc469b -->

面向个人的 AI 助手。对话优先，支持图片、文档与语音输入，长期记忆以 markdown 文档存放，模型与工具均可替换。Go 编写，交付单个二进制。

> 项目使用 AI 辅助开发

## 功能

- **会话树**：消息创建后即不可变，修改一条消息会从该节点分叉，新旧分支并存，可随时回溯、切换。
- **上下文压缩**：`/compact` 手动、超阈值自动、模型经工具自触发三轨；压缩失败时回退到裁剪最旧消息。
- **多模态输入**：文件与剪贴板摄取，pdf/docx/xlsx/html 自动转文本，图片直接进入对话。
- **记忆即文档**：markdown 文件就是记忆。用户可直接编辑，模型经工具读写。
- **内置工具**：文件读写与精确替换编辑、命令执行、后台任务、技能载入、网页抓取。
- **技能**：Agent Skills 开放格式（目录 + `SKILL.md`），与 Claude Code、OpenCode 等现成技能目录通用。正文按需载入。
- **MCP 扩展**：stdio 与 streamable HTTP 两种传输，接入现成 server 即得新工具，与内置工具同权。
- **本地优先**：数据全在 `~/.aquarius/`，profile 整目录隔离。卸载即删程序与该目录。
- **三种界面**：Gio 桌面窗口（默认）、bubbletea 终端界面、行式 repl。
- **平台支持**：Windows 为完整形态。Linux 与 macOS 可构建运行，界面为降级实现；屏幕捕获与语音采集仅 Windows。
- **规划中**：Go 级进程内插件（Tier-1）。

## 构建

需要 Go ≥ 1.27.3。Linux 下的 GUI 依赖 cgo 与 X11/Wayland 开发库。

```bash
go build ./cmd/aquarius     # Windows 上产物为 aquarius.exe
./aquarius -version         # 版本、平台与 Go 版本
./aquarius                  # 首次运行生成 ~/.aquarius/
```

发版构建与质量门禁见[开发指南](docs/development.md)。

## 使用

1. 首次启动会打开引导窗，填写服务地址、模型名与密钥。也可稍后在设置窗「模型服务」页，或直接编辑 `~/.aquarius/profiles/default/config.json`。
2. 密钥可写成 `secret:<名字>`，真实值存本机加密密钥库或同名环境变量，配置文件里只留引用。也接受明文直填，启动时会打印警告。
3. `/help` 列出全部命令。日常用法见[用户手册](docs/guide.md)，命令与参数逐条见[参考手册](docs/usage.md)。

数据在 `~/.aquarius/`（`-data` 可改位置）。根目录只存当前 profile 指针，主配置、会话、记忆与附件都在 `profiles/<name>/` 下。`--profile` 临时切换，或在设置窗改后重启生效。

## 文档

| 文档 | 内容 |
|---|---|
| [DESIGN.md](DESIGN.md) | **权威设计文档**：定位、领域模型、端口设计、权限模型、平台边界与里程碑 |
| [docs/decisions.md](docs/decisions.md) | **决策记录**：逐条决策、动机与被否决的方案（编号见文内索引表） |
| [docs/roadmap.md](docs/roadmap.md) | **进度与规划**：待办盘点、分步路线与落地状态（进度以此为准） |
| [docs/overview.md](docs/overview.md) | 架构导读：内核职责、依赖方向、目录与端口矩阵 |
| [docs/guide.md](docs/guide.md) | **用户手册**：安装、对话、附件、语音、分支、设置窗逐页、故障排查 |
| [docs/usage.md](docs/usage.md) | 参考手册：命令与参数、内置工具、压缩、权限等级、常见问题 |
| [docs/configuration.md](docs/configuration.md) | 配置参考：`config.json` 逐字段、覆盖顺序、密钥规则 |
| [docs/storage.md](docs/storage.md) | 数据布局与备份：目录树、会话文件、`.bak` 恢复 |
| [docs/development.md](docs/development.md) | 开发指南：质量门禁、测试手段、新增工具与命令的分步说明 |
| [AGENTS.md](AGENTS.md) | AI 编码代理协作指南：硬性规则、命令与提交要求 |
| [docs/releases/v0.1.0.md](docs/releases/v0.1.0.md) | v0.1.0 发布说明：功能、安装、已知限制 |

`docs/` 各篇均由 DESIGN.md 派生，`decisions.md` 为权威决策记录；有冲突时以 DESIGN.md 与决策记录为准。

## 状态

开发进行中，里程碑进度见 [docs/roadmap.md](docs/roadmap.md)。

## 许可证

[Mozilla Public License 2.0](LICENSE)
E)
