<p align="center">
  <img src="assets/icon/icon-128.png" alt="Aquarius 图标" width="128">
</p>

# Aquarius
<!-- docs-sync: 2026-10-22 @ 2b073f8 -->

面向个人的极简 AI 助手：对话优先，支持图片、文档、语音，基于文本的长期记忆，自由选择模型与工具。Go 编写，发布单个二进制。

> 本项目使用 AI 辅助开发。

## 特性

- [x] **会话树** — 改一条消息就开一条新分支，旧回答都还在，随时切回对比
- [x] **上下文压缩** — 接近上限时自动总结前文，也能随时手动 `/compact`，长对话不断线
- [x] **图片与文档** — 拖文件或粘贴即可，PDF/Word/Excel/HTML 自动转文本
- [x] **系统通知** — 回答完成时弹一条提醒，切到别的窗口也不会漏
- [x] **语音**（Windows）— 发送键变麦克风，录完转写回输入框（不自动发送）；回答可自动朗读
- [x] **视觉输入**（Windows）— 截图、整个屏幕或被挡住的窗口，都能直接问
- [x] **记忆 = 文档** — markdown 文件就是记忆，你自己能改，模型也能读写
- [x] **技能** — 一个目录 + `SKILL.md` 就是一套做法，用到才载入，平时不占上下文
- [x] **内置工具** — 读写文件、执行命令、联网搜索、跑后台任务
- [x] **MCP 扩展** — 接上现成的 [server](https://modelcontextprotocol.io) 就多一批工具，和内置工具用法一样
- [ ] **进程内插件** — Go 级扩展，规划中
- [x] **本地优先** — 数据全在 `~/.aquarius/`，卸载 = 删程序 + 删这个目录
- [x] **两个界面** — 默认是桌面窗口（Windows 上为悬浮球，可托盘常驻、全局快捷键呼出），也可以切到终端界面
- [x] **跨平台** — Windows 功能最全；其它平台可运行，界面更简、部分功能不可用

## 快速开始

**运行要求**：Windows 10/11 x64 为完整形态；其它平台能跑，但功能不全。**源码构建需 Go ≥ 1.27.3**。

```bash
go build ./cmd/aquarius        # 开发构建（Windows 上产物是 aquarius.exe）
./aquarius.exe -version        # 看版本、平台与 Go 版本
./aquarius.exe                 # 首次运行会生成 ~/.aquarius/ 下的配置
```

1. 首次启动（默认的桌面界面）会打开引导窗，填上服务地址、模型名与密钥即可（也可以稍后在设置窗
   「模型服务」页或 `~/.aquarius/profiles/default/config.json` 里改）；
2. 密钥建议写成 `secret:<名字>`：真实值放本机密钥库或同名环境变量，配置文件里只留引用（也接受明文，
   但启动会警告）；
3. 之后直接对话，`/help` 看命令。第一次用建议先读[用户手册](docs/guide.md)。

数据都在 `~/.aquarius/`（`-data` 可换位置）：根目录只存当前用哪个 profile，主配置、会话、记忆与附件
都在 `profiles/<name>/` 里，互不干扰（`--profile` 临时切换，或在设置窗里改、重启生效）。

## 文档

| 文档 | 内容 |
|---|---|
| [DESIGN.md](DESIGN.md) | **权威设计文档**：项目定位、核心模型、架构与权限、里程碑 |
| [docs/decisions.md](docs/decisions.md) | **决策记录**：每条决策与否决方案（编号见文内索引） |
| [docs/roadmap.md](docs/roadmap.md) | **进度与规划**：待办、分步路线与落地状态（进度以此为准） |
| [docs/overview.md](docs/overview.md) | 架构导读：十分钟看懂内核做什么、代码怎么分层 |
| [docs/guide.md](docs/guide.md) | **用户手册**：安装、聊天、附件、语音、分支、设置窗逐页、故障排查（不需要懂编程） |
| [docs/usage.md](docs/usage.md) | 参考手册：命令与参数、内置工具、压缩、权限等级、常见问题 |
| [docs/configuration.md](docs/configuration.md) | 配置参考：`config.json` 逐字段、覆盖顺序、密钥规则 |
| [docs/storage.md](docs/storage.md) | 数据布局与备份：目录树、会话文件、`.bak` 恢复 |
| [docs/development.md](docs/development.md) | 开发指南：检查项、测试手段、新增工具/命令的分步说明 |
| [AGENTS.md](AGENTS.md) | AI 编码代理协作指南：硬性规则、命令与提交要求 |
| [docs/releases/v0.1.0.md](docs/releases/v0.1.0.md) | v0.1.0 发布说明：功能、安装、已知限制 |

> 开发进行中，进度见 [docs/roadmap.md](docs/roadmap.md)。`docs/` 各篇都由 DESIGN.md 派生，
> 有冲突时以 DESIGN.md 与决策记录为准。

## 许可证

[Mozilla Public License 2.0](LICENSE)
