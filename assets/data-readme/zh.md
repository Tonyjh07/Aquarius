# Aquarius 数据目录 / Data directory

## 中文

> 说明：本文件由程序生成，启动时按版本重写。想留自己的笔记，另建文件。
> 仓库是 <https://github.com/Tonyjh07/Aquarius>；磁盘布局的完整口径见
> <https://github.com/Tonyjh07/Aquarius/blob/main/docs/storage.md>，这里只是速查。

### 这是什么

Aquarius 的全部本地数据都放在这个目录：配置、会话树、记忆、附件、密钥、技能、插件、日志。
除密钥库外都是明文，编辑器可以直接打开修改。卸载 = 删程序 + 删这个目录。

### 顶层

| 名字 | 是什么 | 谁写 | 能删吗 |
|---|---|---|---|
| `config.json` | 只存一句「当前用哪个 profile」，不是配置本身 | 程序 | 别删；删了按 `default` 重建 |
| `profiles/` | 一个 profile 一个子目录，彼此完全隔离 | 程序 | 删掉等于丢掉那个 profile 的全部数据 |
| `README.md` | 本文件 | 程序 | 可以；下次启动重新生成 |

### 一个 profile 里有什么

下表里的 `<profile>` 指 `profiles/` 下的某个名字。

| 名字 | 是什么 | 谁写 | 能删吗 |
|---|---|---|---|
| `config.json` | 主配置：模型、权限、界面、语音、插件 | 设置窗 / 用户 | 别删；删了重建模板 |
| `sandbox/` | 助手的工作目录，写这里免确认 | 助手 | 可以 |
| `memories.md` | 全局记忆 | 助手 / 用户 | 可以；助手随之忘掉 |
| `conversations/` | 会话树，一个会话一个 JSON | 程序 | 可以；删了那个会话就没了 |
| `conversations/<id>.json.bak` | 上一代备份，默认留 3 代 | 程序 | 可以；删了没得回滚 |
| `conversations/<id>.memory.md` | 那个会话自己的记忆 | 助手 | 可以 |
| `jobs/` | 后台任务日志，一件活一个 `.log` | 程序 | 可以 |
| `logs/` | 运行日志，按天切、超量轮转 | 程序 | 可以 |
| `audit.log` | 审计：每次模型调用与工具调用一行 JSON | 程序 | 可以；删了查不到历史 |
| `attachments/` | 附件库，文件名是内容的 sha256 | 程序 | 可以；会话里的引用会断 |
| `exports/` | 会话导出的缺省落点 | 程序 | 可以 |
| `characters/` | 导入的角色卡，一个角色一个目录 | 程序 | 可以 |
| `skills/` | 技能目录，放进去就被认出来 | 用户 / 程序 | 可以；对应技能随之消失 |
| `skill-cache/`、`skill-installs.json` | 技能安装的检出缓存与台账 | 程序 | 可以；按台账重装 |
| `plugins/` | MCP 插件声明，一个插件一个 `plugin.json` | 设置窗 | 可以；插件随之消失 |
| `secrets.bin`、`secrets.key` | 本机密钥库：密文与加密密钥 | 程序 | 别删；密钥要全部重填 |
| `gui_pos.json`、`config.json.bak*` | 主窗位置记忆、配置的历史备份 | 程序 | 可以 |

### 常做的四件事

- **改配置**：开设置窗改（会校验），或直接改 `profiles/<名字>/config.json`；改完重启生效。
- **换 profile**：改根目录 `config.json` 里的名字，或带 `--profile <名字>` 启动；切换即换整目录。
- **备份**：复制整个数据目录；只备份会话就复制 `profiles/<名字>/conversations/`。
- **回滚一个会话**：
  1. 停掉程序。
  2. 把 `<id>.json.bak` 改名成 `<id>.json`。

### 别做的四件事

- **警告**：程序开着的时候手改 `conversations/*.json`，程序写回时会覆盖这些改动。
- **警告**：把密钥写进 `config.json`，那是明文文件。密钥放进密钥库或环境变量。
- **警告**：删掉 `secrets.bin` 之后，填过的密钥找不回来，只能全部重填。
- **说明**：`*.tmp` 是写了一半的临时文件，正常退出不会留下。
