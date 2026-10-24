<p align="center">
  <img src="assets/icon/icon-128.png" alt="Aquarius 图标" width="128">
</p>

# Aquarius
<!-- docs-sync: 2026-10-24 @ 4ab55ce -->

面向个人的 AI 助手。对话优先，支持图片、文档与语音输入，长期记忆以 markdown 文档存放，模型与工具均可替换。Go 编写，交付单个二进制。

> 项目使用 AI 辅助开发

## 功能

- **会话树**：消息创建后即不可变，修改一条消息会从该节点分叉，新旧分支并存，可随时回溯、切换。
- **上下文压缩**：`/compact` 手动、超阈值自动、模型经工具自触发三轨；压缩失败时回退到裁剪最旧消息。
- **多模态输入**：文件与剪贴板摄取，pdf/docx/xlsx/html 自动转文本，图片直接进入对话。
- **语音输入与朗读**（Windows）：空输入时发送键变麦克风，录完把转写结果回填输入框而不自动发送；回答可整段合成后朗读，音色、风格与试听在设置窗「语音」页。识别与合成各有多种接法：云端对话接口、本地 whisper（录音不出本机）、OpenAI 转写形状的任一端点、FunASR 原生服务，以及 VITS / GPT-SoVITS 合成服务。
- **截图问答**（Windows）：内置 `window_list` 与 `screen_capture`，能列窗口、抓全屏，也能抓被其它窗口遮挡的窗口，图片直接进对话。
- **完成通知**：回答提交后弹系统通知，Windows 走 PowerShell、Linux 走 `notify-send`、macOS 走 `osascript`。
- **记忆即文档**：markdown 文件就是记忆。用户可直接编辑，模型经工具读写。
- **内置工具**：文件读写与精确替换编辑、命令执行、后台任务、联网搜索与网页抓取、技能载入。
- **技能**：Agent Skills 开放格式（目录 + `SKILL.md`），与 Claude Code、OpenCode 等现成技能目录通用；单文件、文件夹、压缩包或 GitHub 链接都能导入，支持更新与卸载。正文按需载入。
- **权限分级**：`read-only` / `strict`（默认）/ `permissive` / `full-access` 四档，越界调用逐次确认；无人值守模式下改为当场拒绝并说明。
- **MCP 扩展**：stdio 与 streamable HTTP 两种传输，接入现成 server 即得新工具，与内置工具同权。
- **本地优先**：数据全在 `~/.aquarius/`，profile 整目录隔离。卸载即删程序与该目录。
- **三种界面**：Gio 桌面窗口（默认）、bubbletea 终端界面、行式 repl。
- **平台支持**：Windows 为完整形态。Linux 与 macOS 可构建运行，界面为降级实现。
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

1. 首次启动会打开引导窗（只有这一个窗，保存后主窗才起来），填写服务地址、模型名与密钥。也可稍后在设置窗「模型服务」页，或直接编辑 `~/.aquarius/profiles/default/config.json`。
2. 密钥可写成 `secret:<名字>`，真实值存本机加密密钥库或同名环境变量，配置文件里只留引用。设置窗里直接填明文也行——保存时它会转入密钥库、config 只留引用；直接手工写进 config 的明文仍可用，但启动时会打印警告。
3. `/help` 列出全部命令。日常用法见[用户手册](docs/guide.md)，命令与参数逐条见[参考手册](docs/usage.md)。
4. 想加技能：设置窗「技能」页导入，或 `/skill install <路径或链接>`；`/skill list` 看已装清单。

数据在 `~/.aquarius/`（`-data` 可改位置）。根目录只存当前 profile 指针，主配置、会话、记忆与附件都在 `profiles/<name>/` 下。`--profile` 临时切换，或在设置窗改后重启生效。

## 文档

| 文档 | 内容 |
|---|---|
| [docs/guide.md](docs/guide.md) | **用户手册**：安装、对话、附件、语音、分支、设置窗逐页、故障排查 |
| [docs/usage.md](docs/usage.md) | 参考手册：命令与参数、内置工具、压缩、权限等级 |
| [docs/configuration.md](docs/configuration.md) | 配置参考：`config.json` 逐字段、覆盖顺序、密钥规则 |
| [docs/storage.md](docs/storage.md) | 数据布局与备份：目录树、会话文件、`.bak` 恢复 |

设计、决策、进度与开发指南等在 [docs/README.md](docs/README.md) 里按读者分列（文档地图）。
有冲突时以 [DESIGN.md](DESIGN.md) 与[决策记录](docs/decisions.md)为准。

## 状态

开发进行中，已落地的里程碑见 [docs/roadmap.md](docs/roadmap.md)，下一步计划见
[docs/plan.md](docs/plan.md)。

## 许可证

[Mozilla Public License 2.0](LICENSE)
