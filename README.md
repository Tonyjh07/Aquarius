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

开发中，里程碑 **M0 骨架**已落地（DESIGN §12）：会话树领域 + 性质测试、全量端口、
storejson 持久化、OpenAI 兼容流式适配器、Turn 循环、repl 界面与单二进制装配——
`go build ./cmd/aquarius` 即可跑通一轮纯文本对话。后续按里程碑推进：
M1 树交互 → M2 工具与记忆 → M3 任务与多模态 → M4 MCP 与 TUI。

## 文档

| 文档 | 内容 |
|---|---|
| [DESIGN.md](DESIGN.md) | **权威设计文档**：定位、领域模型（会话树/Revise）、端口设计、插件架构（MCP）、权限模型、里程碑与决策记录 |
| [AGENTS.md](AGENTS.md) | AI 编码代理协作指南：硬性规则、命令、测试与提交要求 |

## 快速开始

```bash
go build ./cmd/aquarius
./aquarius.exe      # 首次运行生成 ~/.aquarius/config.json
```

1. 编辑 `~/.aquarius/config.json`：`model.name` / `model.base_url`；
2. 密钥只经环境变量引用（禁止明文入配置）：配置里写 `secret:AQUARIUS_OPENAI_KEY`，
   同名环境变量存真实密钥；
3. 重新运行，直接对话；`/help` 查看命令，`/quit` 退出。

数据全部在 `~/.aquarius/`（`-data` 可指定目录），会话树为可直接查看的一树一 JSON。

## 许可证

[Mozilla Public License 2.0](LICENSE)
