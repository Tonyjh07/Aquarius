# Aquarius 里程碑进度（落地档案）
<!-- docs-sync: 2026-10-25 @ 9c5f3aa -->

> **定位（2026-10-23 起）**：本文件是**进度档案**——只记「已经落地了什么、对应哪条决策」，
> 以及里程碑基线。**未来要做什么、按什么顺序、什么节奏**，一律看 [plan.md](plan.md)。
>
> 权威设计 = [DESIGN.md](../DESIGN.md)；逐条决策 = [decisions.md](decisions.md)（每条的动机/取舍/
> 否决方案都在那里，本文件不重复）。冲突时以二者为准。
>
> 为什么这样分：v0.1.0 之前，roadmap 同时承载「历史落地记录」与「未来工作」，已完成的
> S1–S14 占了大半篇幅，真正未排期的条目被埋在中间。现在职责拆开——档案归档案，规划归规划。

---

## 0. 里程碑基线

> 编号与内容以 [DESIGN.md](../DESIGN.md) §12 为准，这里只标状态。

| 里程碑 | 内容 | 状态 |
|---|---|---|
| M0 骨架 | go.mod、`domain/conversation` + 性质测试、`port` 全量接口、storejson、repl、openai 兼容适配器 | 已落地 |
| M1 树交互 | Revise(Fresh\|Carry)、Checkout/Branch/rm、golden 回放测试框架 | 已落地 |
| M2 工具与记忆 | ToolRunner、memory_*、file_*、think、`context_compact`、权限矩阵、三级 token 计数链、自动压缩 | 已落地 |
| M3 任务与多模态 | JobManager + job_* + term_exec、blobfs、Ingestor、输出器 notify | 已落地 |
| M4 MCP 与 TUI | mcpgate（stdio + streamable HTTP）、grant、`/plugin`、`/model`、TUI MVP、装饰器链 | 已落地 |
| M5 GUI 前端 | Gio 悬浮球 GUI、托盘/热键、三窗宿主、主题、设置窗 | 已落地（S1–S2b、S4–S5、S4a–S4d、S6、S7、S7c 均在其后陆续落地） |
| M6 底座与跨平台 | 配置底座（profile 目录化 + provider 列表化）、大文件拆分、设置窗渲染修复、GUI 跨平台抽象层、relation-map 对话树 UI | 已落地 |
| M11 屏幕捕获与语音 | `window_list`/`screen_capture`、转写与播报、`/mic`、语音设置页 | 已落地（S12/S8/S8b） |

**版本基线**：`v0.1.0` 已于 2026-10-23 发布（tag `v0.1.0` → `3c2767b`）。
当前开发分支 `feat/v0.2.0`：前半程（P0 守规 + P1 聚合根归位）已落地，后半程（P2 收口与拆包）
按 [plan.md](plan.md) 的「收口与拆包」批次进行。

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
| v0.2.0-P2 | 收口与拆包：前缀解析归一、`JobManager` 按消费方拆、提示词具名类型、`SessionDeps` 拆端口、`uigui` 拆包、设置逻辑归位、零碎真债（D217；批次 P2-1…P2-7） | 进行中 |

**`v0.2.0-P2` 已落地**（批次号固定，剩余项见 [plan.md](plan.md)「收口与拆包」）：

| # | 提交 | 主要结果 |
|---|---|---|
| P2-1 | `3a00d69 refactor: resolve identifiers through one prefix matcher` | 四处前缀解析（`ResolveJobID`/`ResolveWatchID`/`resolveNode`/`resolveConversation`）共用 `port.MatchPrefix`，措辞仍分层（工具面英文、命令面中文）；补匹配优先级与命中序单测（5 files, +109/−70，净减 34 行） |
| P2-2 | `7912324 refactor: split the job port by consumer` | `JobManager` 拆成 `JobStarter`/`JobRunner`/`JobReader`/`JobKiller`，组合体保留；各工具与 `/jobs` 只声明实际用到的方法；`requireJobs` 改泛型以保住入参的窄类型（7 files, +66/−18） |
| P2-3 | `27c39ad refactor: give prompt parts and messages named kinds and roles` | 文档笔 `d4b5aa2`（`DESIGN.md` §5.1 + D227）。`PromptPart.Kind` / `PromptMessage.Role` 由裸 `string` 换成 `port.PromptKind`/`PromptRole` 与 6 个常量；四家适配器的 `switch` 从字符串字面量改常量，协议字符串只在写请求体、读响应体处转；生产侧转换点收敛到 `wire.go`/`responses.go` 两处（21 files, +263/−240） |
| P2-4 | `9c5f3aa refactor: drop the last anonymous callback from the session deps` | 三笔：`159a005` 模型读侧收成 `ModelCatalog` + `ModelProbe`，四个启动期静态值合成 `ModelStatus` 一次取回；`b8555c2` 权限/思考/撤观察者/记忆编辑器收成 `PermissionSettings`/`ThinkingSettings`/`WatcherCanceller`/`MemoryOpener`；`9c5f3aa` 模型切换收成 `ModelSwitcher`（D228）。`SessionDeps` 字段 29→22，匿名回调 14→0，装配根行为不变（三笔合计 13 files, +563/−300） |

**`v0.2.0-P1` 的落笔顺序**（每笔独立过全门禁、可单独回退）：

| # | 提交 | 主要结果 |
|---|---|---|
| P1-1 | `06cb4e3 refactor: read the conversation tree through accessors` | 加 `NodeCount`/`ChildIDs`/`IDs`/`SubtreeSize`/`Edition`；`app` 生产代码直读清零；删掉 `app` 的 `subtreeSize` 副本（5 files, +278/−42） |
| P1-2 | `77b9e10 refactor: let the domain produce the tree view` | `View()`/`SiblingGroup`/`nodeLess` 归领域；`makeTreeSnapshot` 只做形状转换，`tree.go` 251→194 行（3 files, +176/−79） |
| P1-3 | `9ff9523 test: verify the whole tree after every change, behind a build tag` | `ValidateEveryChange` 开关 + 四条变更路径接入（含 `Revise` 的 carry/clone 提前返回分支）；`-tags aqvalidate` 下全量绿（4 files, +70） |
| P1-4 | `2f5f488 refactor: close the conversation tree behind the aggregate root` | 三张 map 私有；`Snapshot`/`Restore` 契约；`storejson` 自带 `wireTree`/`wireNode`；新增聚合根守门测试；测试直读改走访问器（33 files, +777/−393） |

> 里程碑与步骤是两套编号：里程碑（M<n>）是 `DESIGN.md` §12 的验收单位，
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

> 这些是早期阶段性问题，**全部已定**。保留在此是因为 `DESIGN.md` 与 `decisions.md` 里有
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

## 6. 每步通用执行清单

> 与 `AGENTS.md` 的硬性规则一致；新增任务一律按这套走。

1. 改 DESIGN/`docs/decisions.md`（新 Dn）先行 → 内部测试先红 → 实现转绿；
2. `gofmt -l .` 空、`go vet ./...` 过、`go test -race ./...` 全绿、`AQUARIUS_GUI_SMOKE=1` 冒烟 ok；
3. commit 英文祈使句、一个 commit 一件事、不 push（未获指令）；
4. 进入模型上下文的字符串英文，界面/注释中文；GUI 大改动手工验收（headless 测不了真窗）。

---

## 7. 已知缺陷档案

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