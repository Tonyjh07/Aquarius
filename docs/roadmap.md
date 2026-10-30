# Aquarius 里程碑进度（落地档案）
<!-- docs-sync: 2026-10-30 @ cc106aa -->

> **定位**：本文件是**进度档案**——只记「已经落地了什么、对应哪条决策」，以及里程碑基线。
> 只记过去，不写未来：**要做什么、按什么顺序**看 [plan.md](plan.md)。
>
> 内核规格 = [contracts.md](contracts.md)；逐条决策 = [decisions.md](decisions.md)（每条的动机/取舍/
> 否决方案都在那里，本文件不重复）。冲突时以二者为准。
>
> 规则也不在这里：硬性规则在 [AGENTS.md](../AGENTS.md)，门禁与工程准则在
> [development.md](development.md)。

---

## 0. 里程碑基线

里程碑是**验收单位**，编号从 M0 起（M7–M10 未启用，历史原因跳号）。下表同时是内容与验收标准的
唯一处：状态列只标是否已落地。

| 里程碑 | 内容 | 验收标准 | 状态 |
|---|---|---|---|
| M0 骨架 | go.mod、`domain/conversation` + 性质测试、`port` 全量接口、storejson、repl、openai 兼容适配器 | 二进制跑通一轮纯文本对话；会话树存取正确；性质测试全绿 | 已落地 |
| M1 树交互 | Revise(Fresh\|Carry)、Checkout/Branch/rm、golden 回放测试框架 | 回放测试覆盖 Revise 两模式与分支导航；Carry 边转移后后代字节不变 | 已落地 |
| M2 工具与记忆 | ToolRunner（确认/超时/裁剪）、memory_*、file_*、think、`context_compact`、权限矩阵执行接入、三级 token 计数链 + `/usage`、自动压缩轨、`/memory` 编辑器直开 | 模型可经工具读写记忆；Confirm 能拦住 `memory_write`；等级矩阵在工具链路生效；超阈值自动压缩跑通；`/usage` 展示精确/估算占用与实测累计；`/memory` 打开记忆文件 | 已落地 |
| M3 任务与多模态 | JobManager + job_* + term_exec、blobfs、Ingestor（文本/文件/剪贴板，程序化入口）、输出器 notify | `term_exec`/`job_start` 经 ToolRunner 确认链路跑通；job 后台跑 + `/jobs` 日志可查；文件/剪贴板输入 → 附件入库 → 装配内联字节端到端；notify 在提交时触发 | 已落地 |
| M4 MCP 与 TUI | mcpgate（stdio + streamable HTTP 双传输）+ grant + `/plugin`、`/model`、TUI MVP（bubbletea + glamour 轻 markdown；repl 保留为测试/e2e 后端）、装饰器链（重试/硬保底截断/审计） | stdio 与 streamable HTTP **各接一个现成 MCP server** 全链路可用（发现 → 授权 → 调用 → 结果回填）；崩溃重启与授权拒绝行为符合契约；TUI 完成一轮对话 + 工具 Confirm；重试/截断/审计在装配根生效 | 已落地 |
| M5 GUI 前端 | Gio 悬浮球 GUI：单组件悬浮球（logo 即球）→ 展开输入栏 → 转写浮层；流式 + 思考暗块定稿折叠 + 工具折叠 chip + 完整 markdown；Confirm 确认态、命令补全、附件选择框、停止键 / 排队输入；托盘常驻 + 右键/托盘菜单 + 全局快捷键（默认 Alt+A）+ 拖拽位置记忆；主题 = 品牌色 `#00AEEF` + 深/浅两版跟随系统；窗口管理：设置/会话历史/欢迎三窗 = 独立常规 OS 窗口 | 悬浮球展开输入栏完成一轮对话（流式 + 思考折叠 + markdown + 工具 chip + 状态行）；停止键取消本轮、排队输入、Confirm 拦截工具、命令补全含 `/mcp:*`；附件按钮 → 文件选择 → 入树内联展示；菜单切会话/主题/退出；托盘/右键可开三窗；设置窗改核心档写回 config 并热生效，密钥只写不回显；`go build ./cmd/aquarius` 仍单二进制（无 cgo）、全门禁通过、repl/tui 回归不受影响 | 已落地（S1–S2b、S4–S5、S4a–S4d、S6、S7、S7c 均在其后陆续落地） |
| M6 底座与跨平台 | **执行序**：① 配置底座破坏性改造（profile 目录化 + provider 列表化 + fallback）→ ② GUI 跨平台抽象层（平台接口 + `uigui` 去平台化 + 非 Windows 降级实现）→ ③ 对话树 UI（relation-map，形态规格见 [relation-map-design.md](relation-map-design.md)：整树快照 + 左栏会话 + 关系图自绘 + 内核删除任意会话/改名补洞） | 三门禁全绿；① 旧 config 报清晰提示、profile 切换重启生效且数据隔离；② `GOOS=linux go build ./...` 通过、非 Windows Gio 常规窗可交互（降级非桩）、Windows 行为零回归；③ relation-map 正确呈现树/版本链（层号色带、形状=Role、主干可辨，公理集自检清单逐条过）、左栏切会话、点节点 `/goto`、任意会话删除与改名可用 | 已落地 |
| M11 屏幕捕获与语音 | `port.Capture` + `screencap`（`PrintWindow`/`BitBlt`）；`window_list` / `screen_capture` 内置工具；`port.Transcriber`/`Synthesizer` 实现 + `micwin` 采集 + `audioplay` 播放；`/mic` 命令与 GUI 语音槽；设置窗「语音」页（含风格控制与试听） | 模型能列窗口、抓全屏、抓**被遮挡窗口**并正确描述画面；语音键说话 → 转写回填输入框 → 发送；`output.tts` 开启后回答自动播报，风格指令与标签实际改变音色表现；非 Windows 明示不支持；全门禁通过 | 已落地（S12/S8/S8b） |

**版本基线**：`v0.2.0` 已于 2026-10-26 发布（tag `v0.2.0`）。上一版 `v0.1.0` 发布于 2026-10-23
（tag `v0.1.0` → `3c2767b`）。v0.2.0 的 P0（守规）、P1（聚合根归位）与 P2（收口与归位 +
常驻内存优化，批次 P2-1…P2-10）**全部落地**；其中的 `uigui` 拆包已按实测评移入 backlog（D234）。

当前开发分支 `feat/v0.3.0`（能力扩张）：**命令注册表**（D245）、**会话进出**（D242）、
**命令提示参数相**（D250）、**aqx 多会话备份**（D249）、**角色卡只读导入**（D243/D247）与
**零碎项收口**（D252/D253/D254：提示词宏展开、provider 自定义请求头、会话树备份轮转）已全部落地，
v0.3.0 无余额（收口后另补一笔：备份代数在设置窗「常规」→「存储」可视可调，F-5）。
`aqx` 容器随之从 A-2 的单树形状换成「一份装一组会话」（`manifest.json` + `conversations/<i>/`），
**旧单树形状不再读**——`aqx` 未随任何版本发布，故不欠兼容。

---

## 1. 原始 TODO 盘点与落地状态

> 这 11 项是最初的 TODO 输入清单，**全部已落地**；保留此表是为了让
> `decisions.md` 里「roadmap 表 #n」这类历史引用仍有落点。各步详情见 §2。

| # | TODO 项 | 落地状态 |
|---|---|---|
| 1 | 设置界面 / config.json 解析 | 已落地（S4/D110 全量配置 + S4d/D142 分组导航 + 六页设置窗） |
| 2 | 欢迎界面、引导配置与教程 | 已落地（D141；D210 修订首跑必走装配前向导） |
| 3 | 会话管理界面（树状） | 已落地（D75 切换 + D80 树数据面 + S3/D112 relation-map） |
| 4 | 消息气泡右键（编辑/重生成/复制） | 已落地（D92–D100） |
| 5 | 配置文件化（多配置独立与切换） | 已落地（S4/D110 profile 目录化 + `--profile`） |
| 6 | 模型与提供商管理 + 自动 fallback | 已落地（S5/D110 provider 列表化 + S11/D173–D177 重构） |
| 7 | 工具与 MCP 管理 | 已落地（S6/D143） |
| 8 | 系统提示词管理（人格组合、模块化注入） | 已落地（S7/D144/D145） |
| 9 | ASR/TTS 接入 | 已落地（S8/D179） |
| 10 | UI 增强与 bug 修复（7 小项） | 已落地（S1，见 §2） |
| 11 | 输入行体验（补全、附件、放大） | 已落地（S2b/D103–D106） |

**贯穿性前置**（已完成）：A = 会话树只读数据面（D80 `port.TreeView`）；B = Status 扩展（D82 `port.SessionFacts`）。

---

## 2. 分步路线与落地档案

> 每步一行。子项编号（`S1-1f`、`S2b-1` 等）保留在原位，供 `decisions.md` 的历史引用落点；
> 具体实现细节、验收过程、实测数据全在对应决策条目里，不在此复述。

| 步 | 内容 | 状态 |
|---|---|---|
| S1 | UI 增强与 bug 修复包 | 已落地：1a D74 / 1b D76 / 1c D77 / 1d D78 / 1e D86 / 1f D80·D81·D87·D89 / 1g D82·D84·D85 / 1h D79 |
| S1b | UI 响应式迁移 + 主窗口大小调整 | 已落地（D90：几何单源化 + Metric 咽喉点 + 三旋钮热生效） |
| S1c | 消息内容跨行（跨块）选择 | 已落地（D91；后续增量 D101 Shift 扩选 / D102 贴边滚动） |
| S2 | 消息气泡右键（编辑/重生成/复制/引用/查看原文） | 已落地（D92–D100） |
| S2b | 输入行体验：命令补全 + 附件按钮 + 文档转文本 + 展开 | 已落地（S2b-1 D103 / S2b-2 D104 / S2b-3 D105·Q17 / S2b-4 D106） |
| S3 | 会话管理界面：relation-map | 已落地（D112 数据面 + D120 形态规格 + D121/D122/D124/D128 物理与交互；规格见 [relation-map-design.md](relation-map-design.md)） |
| S4 | 配置底座：全量设置窗 + 配置文件化 | 已落地（D110①② + D141③ 欢迎窗 + D210 修订） |
| S4a | 大文件拆分重构 | 已落地（8 个 refactor commit；2026-10-22 按需补齐测试文件拆分） |
| S4b | GUI 跨平台抽象层 | 已落地（D111 + D119 非 Windows 门禁口径） |
| S4c | 设置窗渲染异常修复 | 已落地（D113：`layout.List` 轴向不变量 + 离屏渲染回归） |
| S4d | 设置窗分组导航 | 已落地（D142：分页 + 左侧导航） |
| S4e | 设置窗枚举档改选择器 + 语音页按协议位收字段 | 已落地（D224/D225） |
| S5 | 模型与提供商管理 + 自动 fallback | 已落地（D110②：providers[]/primary/fallback + `decorate.Fallback`） |
| S6 | 工具与 MCP 管理 | 已落地（D143） |
| S7 | 系统提示词管理：人格模块树 + 独立页 | 已落地（S7a 内核 D144 / S7b 设置窗 D145） |
| S7c | 次窗换 Paper 主题 | 已落地（D148/D149） |
| S8 | ASR/TTS 接入 | 已落地（D179） |
| S8b | 语音协议位与多提供商：本地 whisper、OpenAI 转写、FunASR 原生、VITS、GPT-SoVITS | 已落地（D223） |
| S9 | 会话与注入内核改版 | 已落地（D150–D167） |
| S10 | 模型面与文件工具整治 | 已落地（D169–D172） |
| S11 | 模型与提供商重构 | 已落地（D173–D177） |
| S12 | 屏幕捕获 | 已落地（D178） |
| S13 | 对话交互补强（提问面板/无人值守/多附件/图片入口/编辑附件） | 已落地（D184–D188） |
| S14 | 技能（Agent Skills）读侧 + 安装面 | 已落地（D201/D202/D203；S14-1 读侧 / S14-2 安装面） |
| S15 | 生态兼容与轻度角色扮演（对照调查） | **未排期 → 已迁至 [plan.md](plan.md) 的角色扮演清单** |
| v0.2.0-P0 | 架构守卫测试（D211）、`/help` 入清单（D212）、展示文本归一（D213） | 已落地 2026-10-23 |
| v0.2.0-P1 | 会话聚合根归位：只读访问器与 `View()`、`ValidateEveryChange` 自检开关、三张 map 私有 + `Snapshot`/`Restore`、`storejson` 自带 wire DTO（D214/D215/D216） | 已落地 2026-10-23 |
| v0.2.0-P2 | 收口与归位 + 常驻内存优化：前缀解析归一、`JobManager` 按消费方拆、提示词具名类型、`SessionDeps` 拆端口、设置逻辑归位、`theme` 令牌数据化、零碎真债（D217；批次 P2-1…P2-7；`uigui` 拆包已移入 backlog，D234）、常驻内存（P2-8…P2-10） | 已落地 2026-10-26（随 v0.2.0 发布） |
| v0.3.0-C | 命令注册表：`/help`、补全与分派读同一张表，29 个 `switch` 分支收成 28 条记录（D245） | 已落地 2026-10-27 |
| v0.3.0-A | 会话进出：一份无损（`aqx`）+ 三份互通（`chatgpt` / `openai` / `sillytavern`）+ 一份只读（Open WebUI）、命令面、历史窗两入口（D242） | 已落地 2026-10-27 |
| v0.3.0-B | 命令提示：参数相浮层（空格切相、逐参数声明 `Params`、`Tab` 轮换 / `Enter` 恒提交）+ GUI 输入历史（D250） | 已落地 2026-10-29 |
| v0.3.0-E | aqx 多会话备份：一份归档装一组会话（`manifest.json` + `conversations/<i>/tree.json` + 跨会话共用 `blobs/`）、`/export --all\|--ids`、`/import --pick`、导入勾选面板、左栏批量选择（导出 / 删除）、批量 `/rmconv`（D249） | 已落地 2026-10-30 |
| v0.3.0-D | 角色卡只读导入：PNG（`tEXt` `ccv3`/`chara`）/ JSON / `.charx`（ZIP）解析、`@char.*` 条件内置模块、`{{char}}`/`{{user}}`/`{{date}}`/`{{original}}` 宏替换、`/character` 命令面、设置窗「角色」页、会话列表行头像（D243/D247） | 已落地 2026-10-30 |
| v0.3.0-F | 零碎项收口：非角色会话提示词也展开 `{{char}}`/`{{user}}`/`{{date}}`（无角色绑定时 `{{char}}` 保留字面量不删空）、provider 自定义请求头 `headers`（仅 `openai-chat`）、会话树备份轮转 N 代（`backup.keep` 默认 3、保存前完整性校验）（D252/D253/D254） | 已落地 2026-10-30 |

**`v0.2.0-P2` 已落地**（批次号固定；完工，无剩余项）：

| # | 提交 | 主要结果 |
|---|---|---|
| P2-1 | `3a00d69 refactor: resolve identifiers through one prefix matcher` | 四处前缀解析（`ResolveJobID`/`ResolveWatchID`/`resolveNode`/`resolveConversation`）共用 `port.MatchPrefix`，措辞仍分层（工具面英文、命令面中文）；补匹配优先级与命中序单测（5 files, +109/−70，净减 34 行） |
| P2-2 | `7912324 refactor: split the job port by consumer` | `JobManager` 拆成 `JobStarter`/`JobRunner`/`JobReader`/`JobKiller`，组合体保留；各工具与 `/jobs` 只声明实际用到的方法；`requireJobs` 改泛型以保住入参的窄类型（7 files, +66/−18） |
| P2-3 | `27c39ad refactor: give prompt parts and messages named kinds and roles` | 文档笔 `d4b5aa2`（[contracts.md](contracts.md) §5.1 + D227）。`PromptPart.Kind` / `PromptMessage.Role` 由裸 `string` 换成 `port.PromptKind`/`PromptRole` 与 6 个常量；四家适配器的 `switch` 从字符串字面量改常量，协议字符串只在写请求体、读响应体处转；生产侧转换点收敛到 `wire.go`/`responses.go` 两处（21 files, +263/−240） |
| P2-4 | `9c5f3aa refactor: drop the last anonymous callback from the session deps` | 三笔：`159a005` 模型读侧收成 `ModelCatalog` + `ModelProbe`，四个启动期静态值合成 `ModelStatus` 一次取回；`b8555c2` 权限/思考/撤观察者/记忆编辑器收成 `PermissionSettings`/`ThinkingSettings`/`WatcherCanceller`/`MemoryOpener`；`9c5f3aa` 模型切换收成 `ModelSwitcher`（D228）。`SessionDeps` 字段 29→22，匿名回调 14→0，装配根行为不变（三笔合计 13 files, +563/−300） |
| P2-7a | `40e0197 refactor: delete the unused relation-map variant` | 删掉 d127 对照档：全仓无测试覆盖它（`REL_PHYS` 在 `_test.go` 零命中，`step()` 首行就分派到源档），190 行在全部测试路径上不执行。删 `accumulate`、常量块、`step()` 分派、`colGap` 两档分支与只服务它的四个字段；两档差异写进 `relation-map-design.md`，原实现留 `relmap-d127` tag（5 files, +72/−335，`relmap_layout.go` 803→567、`relmap_layout_source.go` 441→357） |
| P2-5 | `07fef4b refactor: split the settings save flow into collect and commit` | 四笔：`d441b7c` 四个设置 DTO 搬进 `port`、`uigui` 留类型别名（D229，2 files, +140/−104，零字面量改动）；`b462ee0` 取值范围收成 20 个 `port.Settings*` 常量，提示文案由常量拼（D230，11 files, +146/−52，数值一处未变）；`6ff9242` 采样四段文本的解析收进 `port.ParseSamplingText`，界面前置校验与写回解析同一份实现（D231，6 files, +228/−110，删 `cmd` 侧重复解析 49 行）；`07fef4b` `saveSettings` 271 行拆成 `patch()` 采集校验 + `commitSettings()` 提交编排，报错返回零值 patch（D232，2 files, +190/−124） |
| P2-7b | `a3502d6 refactor: keep the theme tokens in one snapshot` | 文档笔 `c0e3023`（D233）。删掉 22 个包级颜色 `var` 与 `applyPalette`/`currentPalette` 的逐字段双向抄写，活动值只留一份 `atomic.Pointer[palette]` 快照，绘制点经 `tok()` 取结构体副本（形态照 `theme_paper.go` 的 `paperOf()`）。110 处绘制点改形态、配色值一处未变；原「同步完整性」测试换成单一来源 / 快照隔离 / 空快照回落三条（23 files, +182/−220） |
| P2-6 | 取消（2026-10-25） | 拆包移到 backlog，依据是本轮实测：三包合计只占 `uigui` 的 24%，搬完父包仍 1.5 万行、最大单文件不变；代价是 391 处按类型的字段改名、75 个白盒测试的夹具重写、装配根 261 处换限定名（D234）。已落的两笔前置（设置逻辑归位、`theme` 令牌数据化）保留——它们各自是净删，不因拆包取消而失去意义 |
| P2-7c | `dafa77f refactor: name the drain points and scan command flags once` | 文档笔 `0e98e5d`（D235）。注入排空的三个时机收成四个具名排空点（`drainBeforeInput`/`drainAfterTurn`/`drainOnWake`/`drainBeforeReplay`），「是否驱动一轮 / 是否逐条呈现」由一张口径表给出，调用点不再组合 `(driven, announce)` 两个布尔（那个从未被用过的组合随之消失）；三条命令的 flag 解析收成一份 `flagScan`，「位置」与「个数上限」两处真实差异显式声明，其余口径只写一遍（值型吃值、缺参报因、正文拼接）；顺带写明并测住 `/skill` 排空点的前提（队列里只会有本条技能正文，因为外部注入已在入口落地）。新增 `cmdflags_test.go`、`session_drain_test.go`，既有命令测试原样通过（12 files, +507/−81；新增测试 282 行，删三套手写解析循环与五处布尔传参） |
| P2-8 | `3599fd2 feat: hold the resident heap to 1.5x live and cap leaks at 768MiB` | 文档笔 `2ef7a9f`（D236）→ 修订笔 `1808f51`（D238）→ 归因 `memprofile_test.go`（D239）。初版是进程级堆软上限 256MiB：`main` 在装配前调一次 `debug.SetMemoryLimit`，`GOMEMLIMIT` 显式设置时不覆盖；不改 `GOGC`（3 files, +71/−0）。**真实 profile 复测推翻了初版前提**：空闲期与设计一致（工作集 323→329MB、存活堆 101→154MB、GC 约 120 秒一次），但压力操作（展开/收起、设置翻页、历史管理拖节点与切会话）把存活堆推到 454MB、工作集峰值 972.9MB、私有字节 1093.5MB——256MB 上限低于存活堆，会在这类路径上连续 GC；而空闲工作集里约 170MB 是非堆加字体，封堆对它无效。D238 改为 `GOGC=50`（摆动收到 1↔1.5 倍）+ 768MiB 只当泄漏保险，两个旋钮各自尊重同名环境变量（3 files, +88/−35）。**同一套压力操作复测**：空闲工作集 323→311MB、峰值 972.9→903.0MB、私有峰值 1093.5→1046.7MB、压力段存活堆 454→479MB（GC 目标 504→705MB）、全程 GC 14→30 次。改善幅度约 5–7%，低于预估——存活堆本身就涨了三倍，压倍数只压缩它上面那一层。**D239 归因**（新增 `memprofile_test.go`，环境变量门控、默认 skip）：按用户操作逐步强制 GC 后读存活堆并落 pprof，结论是**涨的全在字体**——每开一窗各建一份主题（解析字体文件 +75～80MB）并各自首次排版（字形轮廓 +37MB），而四十轮对话 +1.3MB、拖节点 30 帧 / 翻遍设置页 / 展开收起 10 次各 0–2.4MB。三窗合计约 336MB，与实测 454–479MB 同量级。字体侧再拆：第二个 Shaper 复用同一份集合只 +1.3MB（共享可行），`NoSystemFonts` 让首次排版那 37MB 变 +0.0MB（证明它来自整机字体库扫描） |
| P2-9 | `f10f3fb refactor: wake the docked heartbeat every 100ms` | 文档笔 `e2cf1c9`（D237）。停靠/收起态心跳 50ms → 100ms：节拍收成 `heartbeatInterval` 常量，判据 `heartbeatNeed` 一处不改（变的是「多久复评一次」）。代价明写：悬停召回、布防识别、tips 点亮的延迟上限从 ≤50ms 变为 ≤100ms（D84/D85 的延迟描述按新节拍读）；停靠/召回动画另有 16ms ticker，不受影响。绘制帧数减半，长期停留的停靠/输入状态直接省一半合成与提交（1 file, +10/−3） |
| P2-10 | `d1e7ae2 feat: parse the CJK faces once and share them across windows` | 文档笔 `9512eba`（D240）。D239 量出每窗各解析一遍同一份中文字体（75～80MB/窗），而解析结果在 `ParseCollection` 返回后完全只读，重复解析是纯浪费。改为进程内按候选表内容缓存一份（`sharedFaces`），交给 `NewShaper` 前**封容量**（防调用方 append 改写共享底层数组，`gofont.Collection` 同一条防护）；Shaper 仍每窗各建一份（首次排版要按 Shaper 解析字形轮廓），整机字体扫描按 D240 保持开启（CJK 粗体面的来源，D147）。**实测同一套量法前后对照：整轮存活堆 502.5MB → 281.9MB（−220.6MB）**——历史窗与设置窗主题各从 +74.9MB 降到 +1.4MB；主窗/历史窗/设置窗首排的 +37～80MB 不变（印证「按 Shaper 存」的预判）；展开夹具从 +112.4MB 降到 +38.8MB；四十轮对话、拖节点、翻页仍各 0–2.4MB。新增 `theme_font_shared_test.go` 四条守卫（同候选返回同一底层数组 / 容量封住 / 不同候选分档 / 两主题共享面但不共享 Shaper），`TestWeightTiers*` 两条像素守卫继续通过（粗体零退化），`-race` 与 linux 门禁全绿（2 files, +147/−6）。顺带修掉一处隐式竞态：`monoFace` 是包级单值，原来后建的窗会覆盖前一个窗的等宽面，共享后各窗取到同一个 |

**`v0.2.0-P1` 的落笔顺序**（每笔独立过全门禁、可单独回退）：

| # | 提交 | 主要结果 |
|---|---|---|
| P1-1 | `06cb4e3 refactor: read the conversation tree through accessors` | 加 `NodeCount`/`ChildIDs`/`IDs`/`SubtreeSize`/`Edition`；`app` 生产代码直读清零；删掉 `app` 的 `subtreeSize` 副本（5 files, +278/−42） |
| P1-2 | `77b9e10 refactor: let the domain produce the tree view` | `View()`/`SiblingGroup`/`nodeLess` 归领域；`makeTreeSnapshot` 只做形状转换，`tree.go` 251→194 行（3 files, +176/−79） |
| P1-3 | `9ff9523 test: verify the whole tree after every change, behind a build tag` | `ValidateEveryChange` 开关 + 四条变更路径接入（含 `Revise` 的 carry/clone 提前返回分支）；`-tags aqvalidate` 下全量绿（4 files, +70） |
| P1-4 | `2f5f488 refactor: close the conversation tree behind the aggregate root` | 三张 map 私有；`Snapshot`/`Restore` 契约；`storejson` 自带 `wireTree`/`wireNode`；新增聚合根守门测试；测试直读改走访问器（33 files, +777/−393） |

**`v0.3.0-C`（命令注册表）的落笔顺序**：

| # | 提交 | 主要结果 |
|---|---|---|
| C-1 | `77dedfe docs: give commands one table that carries both the metadata and the handler` | 文档笔：[contracts.md](contracts.md) §7.3 记录形状 + D245 + 开发指南「新增一个命令」收成一步（4 files, +95/−25） |
| C-2 | `5171297 refactor: dispatch commands from one table that also carries their metadata` | 29 分支 `switch` 收成 28 条 `commandEntry`（名字们 / 参数用例 / 描述 / 处理函数）；别名 = 同记录多名字；`/quit` 与 `/help` 都走处理函数、不留特殊分支；表在 `init()` 填（`/help` 自引用成环），之后只读；只吃参数的处理器经适配器接上。表自检 + 逐条分派 + 别名等价 + 未知命令报因取代原 AST 对账，`/help` 文案字节不变（3 files, +218/−191） |

**`v0.3.0-A`（会话进出）的落笔顺序**：

| # | 提交 | 主要结果 |
|---|---|---|
| A-1 | `05daf1d feat: add the session exchange port and the one format table behind it` | 端口 `SessionExporter`/`SessionImporter`、`ExchangeFormat`/`ExchangeReport`、五格式表（唯一事实源，有损必须写 `LossNote`）；装配前命令报「本构建未接入」（3 files, +142/−0） |
| A-2 | `c577378 feat: make aqx a lossless round trip over the on-disk tree shape` | `aqx` = ZIP（`manifest.json` + 与磁盘同形的 `tree.json` + `blobs/<sha256>`）；`storejson` 加 `EncodeTree`/`DecodeTree`；节点/边/版本链/Head/时间戳/分片/附件字节逐项往返等价，条目数与字节数上限、zip-slip 防护（5 files, +1010/−3） |
| A-3 | `6df01c2 feat: speak chatgpt's mapping tree for both export and import` | `mapping` + `current_node` 双向；源 ID 只建边、节点 ID 全重分配、根下补边、`Restore` 自检；空壳与装不下正文的消息跳过、子节点顺父接最近祖先（6 files, +922/−16） |
| A-4 | `f616eec feat: project the head path into the two linear exchange formats` | `openai` 与 `sillytavern` 双向：只取 Head 路径、只带正文、`system`/`inject` 降级 system、思考/工具/附件计入 `Dropped`、有旁支才写「旁支上的 N 条消息」（7 files, +891/−18） |
| A-5 | `7f1268c feat: read Open WebUI exports and guard every import the same way` | Open WebUI 两种导出形状只读接入；上限覆盖扁平格式；`Detect` 认 ZIP → `mapping` → `chat` → `.jsonl` → `messages`；map 来源按 ID 排序、边按父排序，同一文件导两次结构相同（6 files, +563/−8） |
| A-6 | `5e73c13 feat: give /export and /import their command surface` | `/export [--format] [--path]` 与 `/import <文件>` 各一条记录；缺省落 `<profile>/exports`、标题派生文件名、同名加 `-2`、写一半失败删半截；导入落新会话（ID 与 `/new` 同源分配、Root ID 连边一起换再自检），失败不动当前会话；回执两行 + 真丢了才列丢项（8 files, +803/−8） |
| A-7 | `f20eaee feat: put the exchange entries in the history window` | 历史窗左栏标题行「导入」「导出」两颗文字钮，只拿路径、选完各投一条命令、取消不投；`platform` 新增 `RequestOpenFile`/`RequestSaveFile`（Windows `GetSaveFileNameW`、非 Windows zenity/kdialog/osascript，保存框不设覆盖确认）；标题 → 文件名规则上移 `port.FileStem`（19 files, +913/−35） |
| A-8 | `5e4fb0f test: accept the exchange end to end and lock the interop output` | 三份互通 golden 锁字节（验收树连节点 ID 一起固定，否则 chatgpt 产物每次不同）+ 导两次字节相同 + 产物读得回；命令面 `/export` → `/import` 逐项等价（ID 抹平后比对），有损路径核对回执与节点数缩减（5 files, +589/−0） |

**`v0.3.0-E`（aqx 多会话备份）的落笔顺序**：

| # | 提交 | 主要结果 |
|---|---|---|
| E-1 | `feat: let one aqx archive carry a group of conversations` | 端口 `ExchangeEntry`/`ExchangeReport.Entries`/`ExchangeIndexer` 与两个新签名（导入收 `pick`、返回一组树）；`aqx` 容器写读换成 `manifest.json` + `conversations/<i>/tree.json` + 跨会话共用 `blobs/`（版本仍是 1）、`Index` 只读清单、`resolvePick` 越界并列清单、上限改按**每会话**卡；三份互通格式收到多于一个会话直接报错；导出仍只写当前会话、导入已落全部树并回滚 |
| E-2 | `feat: back up and restore groups of conversations from the command line` | `/export --all`（整库）与 `--ids <id>[,<id>…]`（互斥、`--format` 非 aqx 报错）、`/import --pick <序号>`；导入「先全部自检、再一次性落盘」（第 3 个坏则库里一个都不多）、切到第一个导入的会话并清屏回放；回执多会话逐条列标题与条数；命令表两条记录 |
| E-3 | `feat: delete several conversations behind one confirmation` | `/rmconv <id> [<id>…]`：先全部解析、再一次确认、最后才动库；删到当前会话时继任者跳过本批要删的；单个 ID 的用法与回执逐字不变 |
| E-4 | `feat: pick which conversations to import from an archive` | GUI 勾选面板（每行标题 + 条数、缺省全勾、只一项不弹）、`Options.Indexer` 注入、`modalActive` 认面板、`startImport` 分流（清单取不到也直接投，原因由命令面说） |
| E-5 | `feat: select conversations in the history list to export or delete them` | 左栏底部「选择」钮 + 行内自绘复选框（`widget.Bool` 嵌在行点击件里收不到指针事件）+ 动作条（全选 / 已选 N 个 / 取消 / 导出 / 删除）+ Esc 退出；导出 = 保存框只一档 aqx、ID 取按下那刻的快照 → `/export --ids`；删除 = `/rmconv`。**「导出全部会话」不单列：全选即整库** |
| E-6 | `docs: describe the multi-conversation backup and the selection mode` | 5 篇描述文档（contracts §5.16、decisions D249、gui-design、guide、plan 里这一节移走）+ roadmap 的档案条目与逐笔表 |

**`v0.3.0-D`（角色卡只读导入）的落笔顺序**：

| # | 提交 | 主要结果 |
|---|---|---|
| D-1 | `1a00ee9` | 端口 `CharacterInfo`/`CharacterProfile`/`BookEntry`/`CharacterCard` 与 `PromptSpec.Character`/`UserName`；装配根留空实现，命令先报「未接入」 |
| D-2 | `7bb9465` | `@char` 组与六个条件内置叶子（无卡 / 字段空 ⇒ 空模块、不计缺失）；`Compose` 先算 `U` 再展开卡文本；`{{char}}`/`{{user}}`/`{{date}}`/`{{original}}` 与 `<BOT>`/`<USER>`/`<START>` 宏替换器；不带卡输出逐字节不变（golden 锁） |
| D-3 | `48e6c69` | `adapter/characterfs` 读 V2/V3 纯 JSON 与 PNG（`tEXt` 关键字大小写不敏感、`ccv3` 优先、进场去 BOM）；字段归一、`data` 与顶层镜像的取舍、`nickname` 优先 |
| D-4 | `8e89f4b` | `.charx`（ZIP）：根 `card.json`、资产 URI 三形态、图标优选 `name=main`、zip-slip 与条目 / 展开门禁、原包留档；缺 `card.json` 报「这是发行包，请导入其中的 `.charx`」 |
| D-5 | `4ecbdf8` | 头像落盘写去 `tEXt` 重编码的像素 PNG（52 MB → 2.22 MiB 像素无损）；引 `x/image/webp` 认 webp 资产 |
| D-6 | `c39bf8e` | `<profile>/characters/<slug>/` 落位（`card.json` 逐字节 + `meta.json` 原子写 + `avatar.png` + 可选 `source.charx`）；slug 归一与大小写不敏感查重；`List` 坏条目带警告跳过、`meta.json` 缺失时从卡重建 |
| D-7 | `59bf9b0` | 卡片字段投影成会话文本：有效引用追加 `["@char.book","@char","@char.system"]`、`prefer_system_prompt` 两态（替换态仍带 `@env`）、`{{original}}` 两处展开、常驻世界书按 `constant && enabled` 过滤、`mes_example` 走注入节点 |
| D-8 | `5155c5c` | `Conversation.Character`/`Tail` 进 `Snapshot`/`Restore` 与落盘 wire（旧文件缺键 = 空）；装配末尾追加 `Tail`、config 开关控制；`Tail` 存宏替换后的终稿 |
| D-9 | `0b6f8fb` | `/character start` 走独立新建路径（不改被 `/new`/`/switch`/`/import` 共用的 `newConversation`）：投影 → 选开场白（合并列表，n 从 1 起）→ 建会话 → 落盘 → 清屏回放 |
| D-10 | `2cd2940` | 卡里 `data.assets` 条数与「我们不执行的机制」记账（`CharacterInfo.Assets`/`Unsupported`），回执据此分列 |
| D-11 | `ff4da71` | 导入回执四类（世界书 / 资产 / 不执行机制 / 只展示字段）+ 界面卡降级提示 + 超长开场白提示 |
| D-12 | `e83e461` | `app.CharacterAdmin` 与装配根实现（范式同 `app.SkillAdmin`）；同名卡不覆盖、删除二次确认 |
| D-13 | `bf3ce80` | `/character list|show|import|start|remove` 各一条命令表记录 |
| D-14 | `9be4b2e` | 设置窗「角色」页（列表 / 头像 / 导入 / 删除 / 开始对话，全部经 `/character` 命令通道，UI 不直连 `CharacterAdmin`） |
| D-15 | `d75e944` | 会话列表行左槽头像（共享 `avatarCache`，每窗各持一份）、`SessionSummary.Character`/`AvatarPath` 数据面；`@char.*` 内置清单标注「仅角色会话生效」 |
| D-16 | `9804618` | `prompts.user_name` 与 `characters` 段四开关落 config（默认开的开关用 `*bool` 区分「键缺失 = 开」与「显式 false」）；装配根接线 `CharacterOpts`/`ApplyPostHistory` |
| D-17 | `9000baa` | 端到端验收：三类容器各导一张真卡（V2 JSON / V3 PNG / V3 `.charx`）→ 开新会话 → 首轮请求逐段核对 → 压缩一次后示例被吸收而人格与常驻世界书仍在 → 删卡后照常装配 |
| D-18 | `docs: close out the character card batch in the archives` | plan 删「B 角色卡只读导入」节并改各处交叉语句 + roadmap 档案条目与逐笔表 + 文档头标记刷新 |

**`v0.3.0-F`（零碎项收口）的落笔顺序**：

| # | 提交 | 主要结果 |
|---|---|---|
| F-1 | `3a8918a feat: expand prompt macros in non-character sessions` | `PersonaComposer.expandPromptText` 在 `baseModules` 展开 `@default`（system_prompt）与全部自定义模块，`{{user}}`/`{{date}}`/`<USER>` 在**非角色会话**里也生效；`expandMacros` 在 `char == ""` 时 `{{char}}`/`<BOT>` **原样保留**（不删空）；补两条测试（3 files, +62/−5） |
| F-2 | `3abdce0 feat: send provider-defined headers on chat requests` | `llm.Config.Headers` + `Client.headers`：`New` 仅 `ProtoChat` 存，`post()` 在默认头与 `authorize` 之后落（同名以声明为准）；config `model.providers[].headers` 与装配根接线；两条测试 |
| F-3 | `fe825c7 feat: rotate conversation backups across N generations` | `storejson.NewWithKeep`/`Store.keep`/`DefaultKeep=1`、`backupPath`（第 1 代 `.bak`、更旧 `.bak.<i-1>`）、`rotate`、保存前 `rotatable` 完整性校验（坏文件不挤掉更旧的可用备份）、`Load` 按新到旧回退、`Remove` 清各代与遗留高代；config `backup.keep`（默认 3）与装配接线；六条轮转测试（4 files, +325/−25） |
| F-4 | `docs: close out the remaining v0.3.0 items in the archives` | 决策 D252–D254 + contracts §5.3/§5.4/§7.7 + configuration（`headers`/`backup.keep`）+ plan 删「零碎四项」节并改交叉语句 + roadmap 档案条目与逐笔表 + 文档头标记刷新 |
| F-5 | `cc106aa feat: expose conversation backup retention in settings` | `port.SettingsPatch`/`SettingsSnapshot` 加 `BackupKeep` 与范围常量 `SettingsBackupKeepMin/Max`（1–20）；uigui「常规」页新增「存储」区（`rowBackupKeep` + 单行编辑档，空 = 默认 3、越界就地拦）；cmd 侧 `textSettings.Backup` 读写 `backup.keep`（仅 `> 0` 才写键）；两条测试（uigui 行位/回填/越界、cmd 往返）（8 files） |
| F-6 | `docs: record the backup retention setting in the archives` | roadmap §0 补记 + F-5/F-6 逐笔表 + configuration「设置窗与 config 的关系」存储段 + gui-design 常规页行 + guide 10.1 表 + 文档头标记刷新 |

**文档整理**：`docs/decisions.md` 于 2026-10-29 压缩到一行一条结论——6,029 行 → 559 行，索引保留
D1–D249 全部编号（同主题的连续编号并成一行），够格的 51 条在「记录」里展开动机与用户拍掉的方案。
压缩前的全文（实现细节、当时的推演与否决项）原样进
[archive/decisions-full.md](archive/decisions-full.md)，要回补只从那里取。

> 里程碑与步骤是两套编号：里程碑（M<n>）是本文 §0 的验收单位，
> 步骤（S<n>）是当时的执行批次。多个步骤可以落在同一个里程碑里。

---

## 3. 依赖关系与执行序（历史记录）

> 这一节是已完成的执行序，保留是因为 `decisions.md` 里有「roadmap §3」这类引用。
> **未来顺序不在这里定**，见 [plan.md](plan.md)。

依赖顺序原则（D109 重排、S4a 增补）：**先修用户天天看的（S1–S2b）→ 再动配置底座（S4+S5 破坏性
集中一次）→ 先拆大文件（S4a，为抽平台层减负）→ 再抽 GUI 跨平台抽象层（S4b）→ 再长对话树 UI
（S3 relation-map）→ 再叠生态与语音**。原则是：先动 schema（不依赖 UI 形态）→ 再拆大文件（纯代码
组织）→ 再抽平台层（结构改动、不新增用户可见功能）→ 再长 UI（消费者）。

实际落地顺序：S1 → S1b → S1c → S2 → S2b → S4+S5（D110）→ S4a → S4c（D113）→ S4b（D111）→
S3（D112/D120）→ S4d（D142）→ S6（D143）→ S7（D144/D145）→ S7c（D148/D149）→ S9（D150–D167）→
S10（D169–D172）→ S11（D173–D177）→ S12（D178）→ S8（D179）→ S13（D184–D188）→ S14（D201–D203）。
每一步都是：**文档先行 → 测试先红后绿 → 三门禁 + 冒烟 → 单事 commit**。

---

## 4. 破坏性修改授权范围

> 本节仍有效：v0.1.0 之前无生产数据，破坏性窗口开放。

以下可直接改、**不写迁移器**：

- config.json schema（新增/改名/重构段），旧文件解析失败给清晰报错即可；
- 数据目录布局（含 profile 目录化）；
- UI 几何、动画常量、菜单项文案与项序、快捷键默认值、托盘/logo 菜单项集合；
- 会话存储文件格式（如需），损坏即重建；
- 现有 GUI 内部结构（大文件可借机拆分）。

**仍然不许动的红线**：`pluginapi/v1`（稳定级，尚未建、建后只增不改）；`domain` 节点不可变与
Revise/Carry 语义；流式不进领域；依赖方向 `adapter → port ← app → domain`；内置实现必须经端口；
无工作区概念。

> **v0.1.0 已公开发布**，此后破坏性改动需评估用户已有数据（`~/.aquarius/` 里的 profile、
> 会话与密钥库）。`plan.md` 里的任务默认按「不破坏既有数据」设计；确需破坏时先补迁移器或
> 在 plan.md 显式标注。

---

## 5. 设计决策记录（Q1–Q17，已全部拍板）

> 这些是早期阶段性问题，**全部已定**。保留在此是因为本文 §0 与 [decisions.md](decisions.md) 里有
> `§5-Q1`、`Q10`、`Q17` 这类引用。

| # | 问题 | 决议 |
|---|------|------|
| Q1 | profile 切换粒度与数据隔离 | **进程级**（`--profile` 启动参数 + 设置窗切换需重启）；会话/记忆数据随 profile **整目录隔离** |
| Q2 | 气泡右键菜单呈现 | **复用 D72 TPM 管线**（shell 线程 `TrackPopupMenu` + 共享 `menuDispatch`）；Gio 侧只做行命中 + 武装-原位抬手 |
| Q3 | 「重新生成」语义 | **重发上游用户消息**：取该分支上游最后一条用户消息，开同父兄弟节点重发（旧回答保留为历史分支）；编辑/重生成均分叉新节点 |
| Q4 | 人格模块化物化时机 | **保持首节点快照**（D20 不变：树内可 `/edit`、压缩水位恒回传）；设置窗明示「对新会话生效」 |
| Q5 | fallback 触发与提示 | **仅网络类降级**（429/5xx/网络/超时 → 下一 provider）；401/400 不降级（配置错误直接暴露）；降级时状态行一次性提示 |
| Q6 | tooltip 上下文使用率数据源 | **精确优先、est 兜底**（适配器有精确 tokenizer 用精确，否则 est 估算链）；分母 = `limits.max_context_tokens` |
| Q7 | ASR/TTS 选型 | **已定（D179 落地时选型，D223 扩展）**——缺省 MiMo chat-completions 音频；语音协议位另开本地 whisper / OpenAI 转写 / FunASR / VITS / GPT-SoVITS 五档 |
| Q8 | 「形裁成功前隐藏窗口」现象 | **启动时闪现未定制窗口** → 首帧内容/ULW 提交前保持隐藏，首帧就绪后再显示 |
| Q9 | 内置工具是否可禁用 | **可禁用**——工具管理页统一「内置 + 插件/MCP」两组开关（config 新增禁用清单） |
| Q10 | 设置窗 raw JSON 入口 | **保留**高级页直接编辑 config.json 原文（解析失败不阻断开窗、泛键保留已有） |
| Q11 | 主窗口大小调整方式 | **双旋钮、仅配置/设置，暂无直观拖拽**：`ui.scale` 元素缩放（150% = 元素大 50%）+ 窗口尺寸像素配置（304×920 = 更窄更长），布局随空间推导重排 |
| Q12 | UI 响应式迁移范围 | **主窗布局由可用空间推导**（min/max/比例约束），次窗不迁移；字号/控件基准不随窗口尺寸变，由 `ui.scale` 显式缩放 |
| Q13 | 底部淡化区 | **常驻视口底缘矮带 + 等高底部留白**（上滚离底后底缘渐隐更自然；贴底时最后一行在带外）。带高 = **12dp**（D79 实测定值） |
| Q14 | 「附件解析」档位 | **加常见文档转文本**：pdf/docx/html/… → 提取文本入树；图片/音频仍走字节内联与既有路径 |
| Q15 | 命令提示与补全做哪些端 | **只做 GUI**（§15.2 浮层口径，含动态 `/mcp:*`）；TUI 保持无补全现状 |
| Q16 | 第二批补充 3 项的归置 | **合成一个「输入行」阶段 S2b**（命令补全 + 附件按钮/解析 + 展开按钮），不加长 S1 |
| Q17 | 文档转文本实现选型 | **已拍板（D105）**：pdf=`ledongthuc/pdf`、docx=`gomutex/godocx`、xlsx=`xuri/excelize/v2`、html=`golang.org/x/net/html`；pptx/rtf 缓（回落二进制占位） |

---

## 6. 已知缺陷档案

> 已修的一行一条（根因与修法见对应决策）。**仍未修的列在 [plan.md](plan.md) 的「已知未修项」**，
> 本表只留档案。

| 缺陷 | 状态 |
|---|---|
| 托盘图标模糊（只取 16px 一档） | 已修（取 32px 档 + 双钉回归测试） |
| Windows 通知弹出 powershell 窗口 | 已修（D117：`CREATE_NO_WINDOW`） |
| D114 后 stdio MCP server 各开控制台窗 | 已修（D118：`hideStdioWindow`） |
| D114 后每个后台作业弹控制台窗 | 已修（D118：`jobproc.setupProc` 并入旗标） |
| 插件「丢文件即静默 exec」 | 已修（D138 信任门） |
| stdlib 8 条漏洞 | 已清零（D137：工具链升到 go1.27.2/1.27.3，`govulncheck` 报 0 个可达） |
| 安全待议① 读路径恒 Allow | 复核结论：**维持**（D22 的显式设计；收紧属设计变更，需单开决策） |