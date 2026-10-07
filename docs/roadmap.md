# Aquarius 阶段规划（TODO 整理与分阶段路线）

> 来源：无对应 DESIGN 节——本文是**阶段规划与工作清单**（权威设计 = `DESIGN.md`，决策 = `docs/decisions.md`；
> 与二者冲突时以后者为准）。§5 决议在动手落码时按惯例仍需回写 DESIGN/decisions（先文档后代码）。

- 里程碑基线：**M0–M4 已落地**；当前推进 **M5 GUI**（S1/S1b/S1c/S2/S2b 已完成）。**M5 后续执行序重排（D109，2026-10-06）**：对话树 UI（S3）之前的日常体验与内核命令已落地 → **配置底座破坏性改造（S4+S5 合批，D110，已落地）→ 大文件拆分重构（S4a，纯代码组织，已落地 2026-10-07）→ GUI 跨平台抽象层（S4b，D111）→ 对话树 UI：relation-map（S3，D112）**，S6–S8 顺延，见 §2/§3
- 基线：`3be7c91`（D75 已合入，决策记录 D1–D75），2026-09-30
- 输入：用户 TODO 清单 10 大项 + 「UI增强与bug修复」7 小项 + 补充 3 项（响应式迁移、主窗口大小调整、消息区底部淡化区）+ 第二批补充 3 项（命令提示与补全、附件解析、预留按钮实装 → 合成 S2b 输入行阶段）
- 决策状态：**Q1–Q16 已全部拍板**（§5，2026-09-30）；**Q17 已拍板**（D105，2026-10-06：pdf/docx/xlsx/html 现成纯 Go 库、pptx/rtf 缓）；Q7（ASR/TTS 选型）留待动手前补充
- 前提：**单开发者、单用户、无生产数据 → 破坏性修改窗口开放**（见 §4）
- 规模记号：**S** = 半天~1 天，**M** = 2~4 天，**L** = 1 周+（含测试与文档）

---

## 1. 现状盘点（TODO → 已有基础 → 真实缺口）

| # | TODO 项 | 已有基础 | 关键缺口 | 规模 |
|---|---------|----------|----------|------|
| 1 | 设置界面，config.json 解析 | 设置窗核心档已落地（模型/权限/think/effort/hotkey/主题；`settingsSnapshot`/`persistSettingsTextKeys`/`applySettings` 写回 + 热生效，密钥只写不回显） | 全量 `fileConfig` 编辑（limits/output/system_prompt/mcpServers/plugins/…）；分组导航；未知键保留 | M |
| 2 | 欢迎界面，引导配置与教程 | `winWelcome` 独立窗壳已存在（D60 多窗宿主） | 数据面：首步引导（provider/key/model 三问）+ 操作教程页 + 「首次运行」判定（如 `ui.welcomed` 标记） | M |
| 3 | 会话管理界面（树状） | `ConversationStore.List` 已有；`/list` `/goto` `/branch` `/rm` 命令已有；winHistory 窗壳已存在 | ① **已落地（D75）**：切换到已有会话 + `/new` 后回放；仍缺**删除任意会话**命令（`Remove` 无命令调用者）与**改名非当前会话**（`/title` 只改当前）；② 会话树结构进 UI 的**只读数据面**（**已落地（D80/§7.5，前置 A）**：`port.TreeView` + app 原子发布快照——树状 UI 自绘可直接消费）；③ 树状 UI 自绘 | L |
| 4 | 消息气泡右键（编辑/重生成/复制） | **已落地（D92）**：右键手势（气泡底板矩形命中）+ TPM 菜单 + `/regen`（上游用户消息分叉重发）+ 编辑态（结构化 `/edit` 直达）+ 复制（选区优先/整条渲染文本）；`/edit` 补清屏回放 | 可增项：引用/查看原始块（roadmap 预留）；thinking/chip 块的右键 | M |
| 5 | 配置文件化（多配置独立与切换） | config 单文件单路径（数据目录 `config.json`），读/写/警告链路清晰 | config **schema 破坏性改造**（profile 目录布局或 `profiles/` 段）；加载/写回全链路；设置窗 profile 切换 UI；CLI 参数 | L |
| 6 | 模型与提供商管理 + 自动 fallback | `modelConfig` 单提供商；LLM 适配器（OpenAI 兼容）；重试装饰器（429 退避）已在 main 装配处 | provider 列表化 schema（破坏性，建议与 #5 同批）；fallback 链语义（错误分类 → 降级顺序 → 状态行提示）；设置窗管理页 | L |
| 7 | 工具与 MCP 管理 | plugin host 已支持运行时 `Enable/Disable/Status/Grant`（D31），`/plugin list\|enable\|disable` 已有，config `plugins`/`mcpServers` 段已有 | 设置窗数据面（列表、启停、连接状态、授权、日志）；内置工具**无**启停机制（现仅 MCP 插件可禁用）；跨窗通知（host 状态变化 → UI 刷新） | M |
| 8 | 系统提示词管理（人格组合、模块化注入） | `system_prompt` 单串 → persona 首节点快照（D20）+ 无 persona 时 config 兜底注入 | 模块库（命名片段）+ 组合规则；保持 D20 首节点快照（§5-Q4 已定）；prompt 装配点改造（app/prompt）；设置窗编辑器 | L |
| 9 | ASR/TTS 接入 | `port.Transcriber`/`port.Synthesizer` 契约**已定义**（`port/modality.go`）；`output.tts` 开关已在 config；§14 backlog 有整条目（D27 移入） | 适配器选型 spike（whisper-api / 本地 / 系统朗读）；麦克风采集（`Kind=mic` 摄取）；GUI 语音按钮；`/mic` 命令 | L |
| 10 | UI 增强与 bug 修复（7 小项） | 详见 §2-S1 拆解 | 每项独立、小步 | 见下 |
| 11 | 输入行体验（命令提示与补全、附件按钮与解析、输入框放大）（第二批补充，§5-Q14~Q16） | `help` 与动态命令面已有（含 `/mcp:*`）；ingestfile 管线 M3 端到端已通；D49 输入行两槽位已按稿预留（20dp 灰占位、不可点） | 补全浮层 + 命令清单**只读通道**（help 文本现居 app 层，铁律 5）；Win32 文件选择框；常见文档 → 文本解析件（纯 Go，选型 Q17）；展开按钮几何（与 1b/1c 协调） | M~L，见 §2-S2b |

**贯穿性前置（两处复用，建议先做）**：

- **A. 会话树只读数据面**：分叉编号/切换按钮（#10-7）与会话管理界面（#3）都要读树结构（兄弟节点、当前 Head、深度），Presenter 现在只给渲染块。需设计一个小的 UI 只读视图端口（铁律 5：UI 不得直读 store）。**已落地（D80/§7.5）**：`port.TreeView`（`Branches(id)` → 同级 ID 创建序 + 自身下标）+ app 侧不可变快照原子发布（UI 无锁读）；S3 可直接复用同一数据面。
- **B. Status 扩展**：logo tooltip（#10-5）要 profile、会话标题、模型、上下文使用率、用量 —— 现 `Status{Model,Level,Effort}` 需扩展（上下文使用率走 est token 计数链，用量走 `/usage` 数据源）。**已落地（D82/§7.6）**：`port.SessionFacts` 展示快照（app 侧原子发布、签名门控重算）+ `uigui.Status` 扩展 `Profile/Facts` 字段（装配根闭包组合）；1g 数据面就绪。

---

## 2. 分阶段路线（9 主步 + S1b/S1c/S2b/S4a/S4b 五个半步 / 对应里程碑 M6 起）
依赖顺序原则（**D109 重排，S4a 于 2026-10-06 增补**）：**先修用户天天看的（S1–S2b，已完成）→ 再动配置底座（S4+S5 破坏性集中一次）→ 先拆大文件（S4a，为抽平台层减负）→ 再抽 GUI 跨平台抽象层（S4b）→ 再长对话树 UI（S3 relation-map）→ 再叠生态与语音**。

### S1 · UI 增强与 bug 修复包（M6，P0，全部 S~M）

一次或两笔 commit，每小项独立可验收：

| 子项 | 现象/目标 | 改动面 | 备注 |
|------|-----------|--------|------|
| 1a 淡出区留白 | 消息区顶部预留 = 淡出带高的空白，淡出不再遮挡内容 | `window.go` 消息区布局 padding（或滚动视口 inset），与 `fadeBandDp` 同源取值 | 与 D54 揭示带、D62 合成共用常量，**单源**引用别写死第二份 |
| 1b 展开/收起动画时序 | **已拍板（D76）双间隙恒 12dp**：缩/长段胶囊左缘钉 logo+12、右缘 = send−12（send 全程同步移动），缩成 ⌀48 圆后与 send 同步平移、同时抵达 logo 合球；展开反向对称（瞬发段：分开 ≈15ms、合球 ≈32ms，接受） | `anim.go` 纯函数按 send 行程分段导出（`barP` 即 send 时钟，状态机零改动）+ `window.go` 先夹 send 后导胶囊 | 性质测试：双间隙恒等（含过冲）、里程碑（0/split/1）、split 连续、间距单调、两段区间不重叠 |
| 1c 展开完成后再淡入控件 | **已拍板（D77）**：输入栏阶段完成（bar 260ms 到位）即触发胶囊内容 180ms CSS ease 淡入（与消息揭示并行，不等 700ms 全完成）；收起淡出 180ms、自收起起点 140ms 处开始、**与消息区收起同一刻结束**；范围**仅胶囊内**（附件/占位符/展开槽/确认区），send 键与 logo 不参与 | `anim.go` `pillFade` 独立时间线（`stepExpand` 帧推 + ticker 续命）+ `inputBar` 组透明层 `PushOpacity` | 性质测试：淡出与消息阶段同刻、触发点、端点/单调、反向取消不闪隐 |
| 1d 启动闪窗 | **已拍板（D78）**：`onHWND` 挂接即 `SW_HIDE`（`revealPending` 武装，最早接管点）、合成/提交门放行启动期、**首帧 ULW 成功即揭示**（`ShowWindow + SetForegroundWindow` 激活前台）；首帧前呼出只置展开态不 ShowWindow；GPU 降级保持隐藏 | `win32`/`gui.go`：`revealPending` 状态机（`presentable` 门 + `presentMain` 成功后揭示 + `showMain` 呼出门 + `revealMain` 平台件） | 时序测试：门放行/首帧后揭示恰好一次/呼出被拦/降级不揭示；手工验收启动无闪窗 |
| 1e 工具调用确认 UI 优化 | **已落地（D86）**：确认态胶囊 = 原因编辑器 + [✗/✓/🔑] 三圆钮——拒绝带原因回传模型（`port.ConfirmAnswer{Allow, Reason}`，toolrun 回填 `user denied <name>: <原因>`）、🔑 = `/permission` 升一档 + 放行本次（仅工具确认且未到 full-access）、右圆停止键恢复可点 | `port/tool.go` + `toolrun`/`plugin/host`/`app`/`repl`/`tui` 适配 + `uigui` 确认态重排；**口径变更**：原「`port.Confirmer` 数据不动」随 D86 解除 | 概念稿先行（用户手绘）+ 两处口径拍板（提档语义 / 原因范围） |
| 1f 消息分叉切换按钮 | 消息下方显示当前分支编号（如 2/3）+ 左右切换 | **已落地（D80/D81）**：气泡下方 `◀ i/n ▶`（数据面 = 前置 A 的 `port.TreeView`）；点击经输入通道投递 `/goto <兄弟id>`，`/goto` 同时补齐清屏+回放（与 `/switch` 同口径）；实时路径分叉条修复 D87（回显块 commit 时回填节点 ID）+ D89（纯工具轮锚点回填 chip/思考块） | 与 S3 的树 UI 共用数据面（A 已就绪），树窗仍留后 |
| 1g logo tooltip 自定义显示项 | profile、会话标题、模型、上下文窗口使用率、用量 | **已落地（D82）**：logo 悬停多行事实卡（profile 占位 `default` 至 S4、会话标题+ID 前缀、模型/档位、上下文 `used/max（pct，精确|估算）`、累计与上轮用量）；数据面 = 前置 B 的 `port.SessionFacts` 快照，未就绪回退启动提示；`hoverTip` 泛化多行 `hoverCard` | 手势面零新增（悬停/拖动/右键三手势本就共存）；验收修复 D84 输入行带心跳 + D85 门控改 `WindowFromPoint` 直证（事件态不可靠） |
| 1h 消息区底部淡化区 + 留白 | 底部加**少量**淡出带（比顶部矮）与等高留白，底缘不再硬切 | `fade.go` 带底 `bandBottom` 机制已有（D54 揭示带在用）、`window.go` 布局 | **已拍板（D79）**：底带高 **12dp**（顶带 56dp 的 ~21%，取「几～十几 dp」薄带口径）+ 等高尾部留白；上滚离底后底缘内容渐隐更自然；带常驻视口底缘 + 等高底部留白，贴底锚定（D56）时最后一行在带外不受影响 |

**S1 出口**：三门禁 + GUI 冒烟全绿；逐项手工验收清单。

**状态（2026-10-01）**：**已完成**——1a–1h 全部落地（1a=D74、1b=D76、1c=D77、1d=D78、1e=D86、1f=D80/D81、1g=D82/D84/D85、1h=D79），三门禁 + GUI 冒烟全绿，1e 确认态经用户实测验收；留观项见 D85 后果②、D86 后果④。

### S1b · UI 响应式迁移 + 主窗口大小调整（M6–M7，P0/P1，M~L，**新增，已拍板**）

两者强关联：**响应式迁移是底座，窗口大小/缩放配置是消费方**。**已落地（D90，2026-10-01）**：
主窗布局本就由空间推导（骨架现状核实），迁移落点 = 几何单源化（`inputRowBandDp` 收编五处
重复组合式、内联字面量命名化）+ **Metric 咽喉点**（FrameEvent 入口 `PxPerDp ×= scale`、
`PxPerSp ×= scale × font/15`——布局站点与派生几何零改动自动一致）；三旋钮 `ui.scale`/
`ui.font_size`/`ui.window_width·height` 全热生效（设置窗三行，px 建窗在 HWND 挂接点补投）；
`restorePx`「窗高÷默认高」反推 DPI 治本为 `GetDpiForWindow` 直查；极矮窗淡出带夹
≤ transH/4；**实现实证修正预设**：球锚左定 → 停靠位与窗宽无关，无需重锚。

- **UI 响应式设计迁移**（**已定范围 = 主窗布局由空间推导**）：现状 = 布局几何大量写死 dp 常量（主窗固定 ~608×460、输入栏/转写区/状态行固定宽，D49 Figma canvas 1:1 口径）；目标 = 各区域宽高**由可用窗口空间推导**（min/max/比例约束），窗口尺寸变化时自适应重排；次窗不迁移。
  - 现有 headless 测试写死 `frameGtx` 尺寸的假设要一并梳理。
- **主窗口大小调整**（**已定 = 双旋钮、仅配置/设置，无直观拖拽**）：
  - `ui.scale`：**元素缩放**百分比（例 150% → 字号/圆钮/间距等元素大 50%）；
  - 窗口尺寸像素配置（例 304×920）：改**窗口大小**，布局按空间推导重排（更窄更长）；
  - 两项进 config + 设置窗（热生效或重启生效待实现时定）；**暂不做拖拽 resize/预设档 UI**（后续可作为增强）；尺寸/缩放记忆进 `gui_pos.json` 或 config（破坏性扩展，无迁移负担）。
  - 连带校验面：ULW 形状集与淡出带随尺寸重算；停靠（dock）与位置记忆；展开/收起动画几何；D71 滚轮钉点等行级手势不回归；125% DPI 缩放已在 headless spike 实测过（§15.6，spike 实测值 125%——本行原记 150% 系笔误，150% 仅出现在单测 Metric 档）。

### S1c · 消息内容跨行（跨块）选择（M6，P0，M，**新增**）

- 目标：拖选跨出单块——同消息多段落、**跨消息连续**（用户拍板：范围 = 整个转写区）；选中态 **Ctrl+C** 拼接复制（用户拍板），Esc/点击别处清除。
- 决策：**D91 已入档**（含 D66 后果③更正）：转写区观察者在 measure 前抢先 `pointer.GrabCmd`（Gio 按下冻结 handler + grab 先到先得）+ `Regions` 行几何二分落点/自绘高亮 + grab 瞬间 `FocusCmd{Tag:nil}` 焦点纪律 + `keyRects` 几何底座；双击/三击/shift 点击无 slop 不触发 grab、原生保留。
- 改动面：`uigui/select.go`（新：观察者/锚焦点状态机/`pointToCaret`/Ctrl+C 拼接）+ `window.go`（measure 捕获每块每行 Regions、`rowStyle` 可选块组手工纵排取块内偏移、消费者阶段挂钩、`paintRow` 自绘高亮）+ `model.go`（`model.clear` 同点清选 + 结构指纹自愈）。
- 测试：headless 拖拽序列（`frameGtx`+Router 既有模式）、`pointToCaret` 纯函数、`q.WriteClipboard()` 拼接断言、gap/chip 头不 grab 回归、双击选词后拖拽锚 = 词首、指纹变化自愈。
- 出口：三门禁 + GUI 冒烟全绿；手工验收 = 跨消息拖选/窗外回落/双击词拖扩/滚轮随选/分叉切换清选/Ctrl+C → 记事本粘贴/Esc 清除。
- 后续增量（不在本步）：shift+点击跨块扩展、拖拽贴边自动滚动、S2 右键菜单复制项复用本选态。
- **状态（2026-10-02）**：**代码完成**——headless 回放测试（拖选/清除源/锚点两路/几何/剪贴板断言）+ 三门禁（build/test/-race/vet/gofmt）全绿；GUI 冒烟与出口条目的手工验收待执行。**验收修正（2026-10-05，D91 更正）**：行带因 ascent/descent 取整可交叠 ~1px，`caretIn` 行内二分 `e` 门槛误用本行底会把下一行 rune 混入区间、谓词非单调，落点偶发漂到下一行（「有时无法跨行选择」）——门槛改取下一行顶；回归测试锁每视觉行左右缘落点。**后续增量已落地（2026-10-05）**：Shift+点击跨块扩选（D101：active 时保留锚、焦点跳点击处；无激活选区仍原生）+ 拖选贴边自动滚动（D102：视口上/下缘 28dp 带内按帧推进、端点随动、心跳唤帧、followTail 协同）；右键菜单复制项复用选态已随 D92 消化。

### S2 · 消息气泡右键（M6，P0，M）

- 目标：气泡右键 → 菜单：**编辑**（分叉新节点）、**重新生成**（分叉新节点）、**复制**、（可增：引用/查看原始块）。
- 路径：行命中（D63 行选/`selRows` 复用，D91 `keyRects` 可直接供给矩形）→ 武装-原位抬手（D72 手势同款）→ 菜单呈现 → `menuDispatch` 扩展分发到 `/edit`、`/branch`(+重发)、clipboard（复制项复用 D91 跨块选态：有选区 → 复制选区，否则整条）。
- 前置已定：菜单复用 D72 TPM 管线（§5-Q2）、重新生成 = 重发上游用户消息分叉（§5-Q3）。
- 测试：行命中与手势复现测试（headless）、分发注入 `inCh` 测试（同 `TestMenuDispatchNew` 模式）、`/edit`/`/branch` 内核测试已有。
- 改动面（落地口径，**D92 已入档**）：`window.go`（`bubbleRects` 气泡底板矩形随帧登记 + `bubbleRight` 手势 + 编辑态提示/取消）+ `model.go`（编辑态提交 = 结构化 `/edit` 命令直达、不回显）+ `shell_windows.go`（`bubbleMenuMsg` 呈现 + `menuDispatchBubble` 三项分发）+ `gui.go`（`editMsg`/`copyMsg`/`pendingCopy` 帧内落剪贴板）+ `session.go`（**新 `/regen`**：上游最近 user 消息 Revise Fresh 分叉重发 → 清屏回放 → Turn；**`/edit` 补清屏回放**——D81「转写区恒与 Head 一致」口径统一）。
- **状态（2026-10-02）**：**已落地（D92）**——headless 测试（矩形登记/手势状态机/复制口径/编辑态生命周期/分发注入 + `/regen` `/edit` 内核）+ 三门禁全绿；命中实现按**气泡底板矩形**（非 `keyRects` 行盒——padding 也是气泡、且行选键无块身份），「原位」= 按下与抬起落在同一气泡。**验收修正（D93）**：① 编辑用户消息（Fresh）即重新生成回答（此前 Head 悬在无回答节点上）；② `/regen` 对无回答的用户消息直接生成、不再造同文本冗余兄弟分支。**验收修正之二（D94）**：分叉条切换落到**版本末端**（`port.TreeView.Tail`，版本子树最新叶子）——切用户消息版本恢复整段旧问答（此前 `/goto` 停在消息节点上、回答不显示）。**增强项（2026-10-05，D97–D102，一次规划分项落地）**：① **分叉三方式编辑（D97）**——user/assistant 气泡菜单同集（编辑 / 编辑并转移历史 / 编辑并复制历史 / 重新生成 / 复制 / 引用 / 查看原文），`/edit` 新增 `--copy`=Clone（D96 命令面接线），Carry/Clone 与助手/系统修订一律不自动生成，user 修订附件分片保真；② **引用（D98）**——渲染文本以 `> ` 单行引用前缀追加进输入框（SingleLine 压平、所见即所发）；③ **查看原文（D99）**——只读次窗 winRaw（原子槽内容注入、单实例原地刷新）；④ **thinking/chip 右键（D100）**——思考块盖章 + menuable 扩展，chip 复制/JSON 原文；⑤ **S1c 增量（D101/D102）**——Shift+点击跨块扩选、拖选贴边自动滚动。手工验收清单：三方式编辑的树形状（分叉条版本数/旧分支内容）、助手消息编辑不触发生成、引用追加、查看原文窗（正文/思考/工具 JSON）、thinking/chip 右键、Shift 扩选、贴边滚动。

### S2b · 输入行体验：命令补全 + 预留按钮实装 + 附件（M6–M7，P1，M~L，**新增，已拍板**）

四项都落在输入胶囊这一行上（共享命中区、布局与动画几何），合成一个阶段；与 S2/S3 无强依赖、可并行：

| 子项 | 现象/目标 | 改动面 | 备注 |
|------|-----------|--------|------|
| 2b-1 命令提示与补全 | 输入 `/` → 命令补全浮层（§15.2 既有口径）：实时过滤、↑↓/回车选中、Esc 关闭；**只做 GUI**（§5-Q15），TUI 保持现状 | `window.go` 浮层绘制与命中；命令清单**只读通道**（help 文本现居 app 层，需经端口/事件暴露，铁律 5；含动态 `/mcp:*`） | 与 D72 右键手势、滚轮行级手势不抢事件；浮层期暂停输入行手势 |
| 2b-2 附件按钮实装 | 附件灰槽（20dp 不可点）→ 可点：系统文件选择框 → `UserInput.Raw{Kind:"file"}` → ingestfile 既有管线（M3 端到端已通） | `window.go` 图标槽命中/悬停态；`shell_windows.go` 文件对话框（对话框走 shell 线程，同 D72 菜单管线模式） | 槽位/几何 D49 稿已预留，本项「启用」它；多选与大小/类型限制实现时定 |
| 2b-3 附件解析（文档转文本） | 常见文档 → 提取文本入树（pdf/docx/html/…，§5-Q14 档位）；图片/音频仍走字节内联与既有路径 | `internal/adapter/ingestfile` 解析件（或新 adapter）+ 装配内联口径（§4.2）；**纯 Go 依赖（cgo 红线）** | 实现选型留 **Q17**（同 Q7 模式：动手前补）；超大文件截断/页数上限实现时定 |
| 2b-4 展开按钮（输入框放大）实装 | 展开灰槽（四角括号）→ 可点：输入框放大 | `window.go`/`anim.go`；**与 1b/1c 展开收起几何协调（S1 先落更稳）** | 放大形态实现时定（临时多行大编辑区 vs 常驻放大）；几何不变量测试同 1b 口径 |

**S2b 出口**：四子项各自可手工验收——补全浮层键流（含 `/mcp:*`）、文件选择 → 转写内联可见、文档转文本端到端、放大/还原不破胶囊动画；三门禁 + GUI 冒烟全绿。

**状态（2026-10-06）**：**已落地（D103–D106，一次规划分项落地）**——① **2b-1 命令补全（D103）**：`port.CommandCatalog` 只读清单端口（help 与补全同源、动态 `/mcp:*` 携描述）、`/` 词法相实时过滤浮层（↑↓/Enter/点击/Esc、超 8 行折叠）；② **2b-2 附件按钮（D104）**：附件槽实装 → shell 线程 `GetOpenFileNameW` 文件框 → **暂存随文发**（chip 取消/替换、`Raw{Kind:"file", Text}`、ingestfile 文本并入首分片）；③ **2b-3 文档转文本（D105/Q17）**：pdf（ledongthuc/pdf，版式碎片归一）/docx（godocx）/xlsx（excelize）/html（x/net/html）提取入树，解析失败回落占位，pptx/rtf 缓（gopptx Windows embed 构建损坏）；④ **2b-4 展开按钮（D106）**：胶囊原地增高 120dp 多行编辑（Enter 提交/Shift+Enter 换行/Esc 收起压平、圆钮随底缘对齐、D76 几何不动）。手工验收清单：输入 `/` 浮层键流（含动态命令）、附件选择→chip→随文提交→转写内联、四类真实文档转文本、展开/收起不破胶囊动画与 Esc 优先级。

### S3 · 会话管理界面：relation-map（M7，P0，L，**形态改定：relation-map，D112**）
> **执行序（D109）**：S3 **顺延至 S4（配置底座破坏性改造）+ S4a（大文件拆分）+ S4b（GUI 跨平台抽象层）之后**——它是 UI 消费者，落在前几者稳定的地基上（平台层就位后 relation-map 不必只写 Windows）。
1. **内核先行**（文档先行：DESIGN §7 + 新 Dn）：
   - **已完成（D75，`3be7c91`）**：`/switch <id前缀>` 切到既有会话 + `port.ClearEvent` 清屏（`/new` 同清）+ 切换后自动回放；
   - 仍缺：**删除任意会话**命令（`ConversationStore.Remove` 已有、无命令调用者）、**改名非当前会话**（`/title` 只改当前）——二次确认复用 `/rm` 口径。
2. **数据面（D112②）**：前置 A（树只读视图端口，D80）**扩展为整树快照**——`port.TreeView` 增 `Graph()`（节点集 `{ID, Parent, Role, CreatedAt, Snippet}` + 版本链映射 + 当前 Head），app 侧 `atomic.Pointer` 原子发布（复用 D80 发布点）。
3. **UI（D112①）**：winHistory 占位壳 → **relation-map 关系图**：节点 + 父子连线（版本链 `RevisedFrom`/`ClonedFrom` 虚线/异色区分）+ 当前 Head 路径高亮 + 当前节点可辨 + 点节点切会话 + 右键（改名/删除/复制 ID，复用 D72 TPM 管线）。**承接 D96 后果③**：Clone 拷贝子树挂新节点之下，graph 用版本链标注关系。
   - 落点：**纯 Gio 自绘**（分层布局、连线折线、hover 渐显操作、缩放/平移可选），无三方依赖。
4. 测试：命令层 golden/脚本流回放；数据面契约测试；UI headless 构建测试 + 手工验收；**图布局/大会话性能 spike（D112 后果②，落码前）**。

### S4 · 配置底座：全量设置窗 + 配置文件化（M8，P1，L，**破坏性集中点，执行序第一**）
> **执行序（D109）**：S4 **最先做**（与 S5 合批）——破坏性窗口开放，底座越晚做、上层功能越多越贵；schema 定案见 **D110**。
> **状态（2026-10-07）**：**①② 已落地（D110）**——根指针 + `profiles/<name>/` 整目录隔离、`--profile`、设置窗 Profile 区（新建/复制/切换）、全量分组页（输出/限额/提示词/MCP 只读/高级 raw JSON）、旧格式两级显式报因（`ErrLegacyConfig`，不迁移）+ GUI 双击启动错误经 Win32 消息框呈现（D110 修订④）；**③ 欢迎窗仍待做**。MCP 与插件的结构化编辑器暂由高级页 raw JSON 承载（Q10），后续可做专用表单。
把两件相关的事合一批做，避免 config schema 改两次（**D110 定案**）：

1. **配置文件化（#5）**：
   - 建议形态：`<data>/profiles/<name>/config.json` + `<data>/config.json` 为当前 profile 指针（或 `profiles` 段）——已定进程级切换 + 数据整目录隔离（§5-Q1）；
   - CLI `--profile <name>`（进程级）+ 设置窗显示当前 profile、可新建/复制/切换（切换 = 重启生效，避免运行态半套配置）；
   - 会话数据是否随 profile 隔离 → 一并拍板（建议：隔离，`profiles/<name>/` 整目录）。
2. **设置窗全量 config 解析（#1）**：
   - 分组页：模型 / 权限 / UI / 输出 / 限制 / 提示词 / MCP 与插件 / 高级（raw JSON）；
   - 泛键写回保留无关键（`persistSettingsTextKeys` 已有该口径，扩展即可）；
   - 未知字段不丢、解析失败不阻断开窗（已有 `readTextSettingsFallback` 口径）。
3. **欢迎窗（#2）** 跟在后面（复用设置组件）：首次运行 → provider/key/model 三问 → 教程页（快捷键、右键菜单、常用命令）→ 「已配置」标记进 config。

**S4 出口**：旧单文件 config **不做迁移器**（无生产数据），解析到旧格式给清晰提示即可。

### S5 · 模型与提供商管理 + 自动 fallback（M8，P1，L，**与 S4 合批，D110**）

- **执行序（D109）**：S5 与 S4 **同批做**（schema 一次改到位），是「配置底座破坏性改造」的组成部分——先于 S4b/S3 完成。
- **状态（2026-10-07）**：**已落地（D110② + 修订③）**——providers[]/primary/fallback schema 与启动校验、`decorate.Fallback`（重试在内降级在外、ErrTransient 判据、降级目标用其 models[0]、无粘态、NoticeEvent 一次性提示）、逐 provider 密钥解析与 unsupported_params 记录、设置窗 provider 列表编辑（全量应用 + 连通性测试）+ `/model` 展示当前 provider；测试 = 错误分类矩阵 + 顺序断言 + 双假 server e2e。**连通性测试与 fallback e2e 用 httptest 假 server（录制回放惯例）**。
- schema（**D110② 定案**）：`model.providers[] = {name, base_url, api_key(或 secret:), models[], tokenizer, unsupported_params[], 兼容性参数}` + `model.primary`/`fallback[]` 顺序（与 S4 同批，避免 schema 再动一次）。
- fallback 语义（§5-Q5 已定）：错误分类（429/5xx/网络/超时 → 降级；401/400 不降级、配置错误直接暴露；降级时状态行一次性提示）→ 与现有重试装饰器（main 装配处）分层：**同 provider 重试（退避）在内，跨 provider 降级在外**（横切铁律 9：装饰器叠在 main，不进业务代码、不进插件 API）。
- UI：设置窗 provider 列表（增删改、连通性「测试」按钮 → 走真实最小请求）、状态行显示当前生效 provider、降级时提示。
- 测试：录制流回放（LLM 适配器惯例）+ 错误分类矩阵 + fallback 顺序性质测试。

### S4a · 大文件拆分重构（M8，P1，M~L，**新增，2026-10-06，执行序第二**）
> **执行序**：当前进行中的任务（另一 agent，改动面含 `cmd/aquarius/main.go`、`uigui/gui.go` 等，与本步拆分对象重叠）**合入后即可开工**；**必须先于 S4b**——平台抽象层在拆薄后的文件上抽取，diff 可读、冲突面小。

- **性质**：纯代码组织重构，**不新增、不变更任何行为**——以同包内搬文件为主（unexported 标识符包内可见，零 import 改动），仅两个巨型函数抽方法。无 DESIGN 口径变更、不立新 Dn；若拆分中发现实现与文档不一致，先改文档再改代码（惯例）。
- **现状（非测试 Go 文件行数 Top）**：`uigui/window.go` **2791**（112 个顶层声明、十余主题混居）≫ `app/session.go` 1170（`execCommand` 单函数 401 行、23 case）≈ `cmd/aquarius/main.go` 889（`run` 单函数 553 行）> `app/agent.go` 829；其余 690~760：`uigui/{model,shell_windows,gui,select}.go`、`llm/openai.go`、`uitui/model.go`。
- **P0：`uigui/window.go` → 骨架 + 10 文件**（全部同包 `uigui` 纯搬移，一个 commit）：

| 新文件 | 迁入声明（现 window.go） | 约行数 |
|---|---|---|
| window.go（留骨架） | `point/rect/drawShape`、`onWindowThread`、`runWindow`、`frame`、`phase`、`requestMove`、`onHWND`（窗口事件循环） | ~250 |
| theme_font.go | `newTheme`、`monoFontFaces`、`pickMonoFace`、`mdHeadingSp`、`loadCJKFaces` | ~110 |
| winpos.go | `posRec`、`loadPos/savePos`、`commitWinGeom`、`applyConfiguredSize`、`clampWindowPx` | ~90 |
| layout.go | `UI.layout`、`bandHeightsClamped`、`layoutCollapsed`、`updateScroll` | ~170 |
| transcript.go | `transcript`、`selFor`、`measuredRow/measureRow/paintRow`、`rowStyle`、`mdBlockWidget` | ~360 |
| chip.go | `chipClick/chipIsOpen/chipHeaderText/chipCopyText/chipRaw`、`toolChipRow`、`statusChip` | ~210 |
| input.go | `inputBar`、`inputExtraDp/Px`、`inputRowRects`、`hoverTip/hoverCard`、`updateEditor/drawCaret/caretRect/caretLineH`、`updateReasonEditor/drawReasonCaret/confirmReason`、`submitEditor/cancelEdit/setExpanded` | ~420 |
| confirm_ui.go | `factsCard`、`pillContent/expandSlot/pillContentExpanded`、`confirmBtn`、`glyphDeny/Allow/Key`、`iconSlot`、`nextPermLevel/confirmElevatable/confirmElevate/confirmTipAt`、`shortID` | ~250 |
| attach.go | `attachSlot/attachChip/attachSlotRect`、`drawPaperclip`、`featherWidth`、`record` | ~120 |
| chrome.go | `drawMaximize/drawMinimize`、`cubicArc`、`actionCircle`、`logoRight/updateLogo/requestLogoMenu` | ~190 |
| branch.go | `blockView(+menuable/selCount)`、`branchStrip/branchStrips/branchClick/branchArrow/branchRow`、`gotoBranch` | ~190 |
| interact.go | `updateClicks/updateDrag`、`beginDrag/moveDrag/endDrag/clickHeld`、`flushCopy`、`frameItems/statusText`、`bubbleHit/bubbleRight/updateBubbleRight`、`bubbleMenuCtx/bubbleCtx/chipOf/requestBubbleMenu` | ~330 |

- **P1：`app/session.go` 先拆函数、再拆文件**——`execCommand` 保留 switch 分发壳，各 case 体抽 `execNew/execList/…/execEffort` 方法（golden 回放守门）；文件拆四份：`session.go`（结构 + `NewSession/Handle/runTurn/ingest/persist`，~350）、`session_cmd_tree.go`（new/list/switch/title/goto/branch/rm + `nodeSummary/branchLine/subtreeSize`）、`session_cmd_edit.go`（edit/regen/Carry/Clone + `editParts/parseEditArgs/resolveNode/upstreamUser/resolveConversation`）、`session_cmd_misc.go`（compact/permission/memory/usage/jobs/plugin/model/think/effort + `jobsReport/resolveJob/jobCommand/permissionReport`）。
- **P1：`cmd/aquarius/main.go` 的 `run()` 按装配段抽子函数**——`run` 留顺序骨架；新增 `bootstrap_profile.go`（profile 解析/特权目录/模板写入）、`assemble_ports.go`（附件库/记忆/notify/llm/装饰器链/工具）、`assemble_ui.go`（三种前端 + 设置窗 + logo 事实卡）；小端口适配器（`sessionTree/sessionCommands/envSecrets/levelHolder/systemClock/yesConfirmer/systemIDGen/terminalSuspend`）→ `main_adapters.go`，`pickEditor/openMemoryEditor` → `editor.go`；能并入已有 config.go/plugins.go 的不另开新文件。
- **P2（可选顺手）**：`llm/openai.go` → client / wire / stream 三份；`uigui/shell_windows.go` → tray / menu / filedlg 三份；`app/agent.go` → 核心循环 + `agent_compact.go` + `turnbuffer.go` + `agent_tools.go`。`uitui/model.go`、`uigui/gui.go`、`uigui/select.go`（内聚的选区子系统）、`plugin/host.go` 暂不动。
- **验证与节奏**：每个源文件一个 commit（`refactor: split … by concern`）；每步 `gofmt -l .` 空 + `go vet` + `go test ./...`（含 `-race`）全绿；app 侧拆函数重点看 golden 回放不变；window.go 拆完全量跑一次 uigui 测试（同包搬移不影响测试对未导出标识符的引用）+ GUI 冒烟收尾。
- **非目标**：不动包边界、命名与行为；不拆测试文件（`session_test.go` 1924、`main_test.go` 1535 留待按需）。
- **状态（2026-10-07）**：**已落地**（8 个 refactor commit）。实测口径修订：`window.go` 2791 → 骨架 322 + **11 文件**
  （theme_font/winpos/layout/transcript/chip/input/confirm_ui/attach/chrome/branch/interact；预研表"10 文件"为笔误）；
  `execCommand` 399 行 21 命名 case（分发壳 + execXxx 方法，先拆函数后拆文件）；`main.go` 973（D110 后）/`run()` 610 行 →
  wiring 装配态 + bootstrap_profile/assemble_ports/assemble_ui 三段，persist 闭包并 config.go（cfgWriter）、插件宿主并
  plugins.go；P2 = openai.go→client/wire/stream、shell_windows.go→shell/tray/menu/filedlg、agent.go→agent/compact/
  turnbuffer/tools。每步声明清单守恒核对 + 三门禁（含 -race）全绿。**P2 例外：`uigui/settings.go`（1134 行）未拆——
  用户拍板留待 S4c 修渲染时一并处理**。备注：`GOOS=linux` 全仓交叉编译受 gio/cgo 限制不可达（基线如此）；
  可用口径 = 非 gio 包 `CGO_ENABLED=0 GOOS=linux go build` 全绿。

### S4c · 设置窗渲染异常修复（M8，P1，M~L，**新增 2026-10-07，用户拍板暂缓**）
- **症状（用户验收截图，2026-10-07）**：设置次窗（winSettings）表单渲染破碎——多行行内容横向
  挤在同一行（标题/Profile/字段同行）、部分控件只剩灰色小条（文本未测出）、按钮渲染为整宽色带
  无文字、大片空白；滚动后出现整宽纯色横带。**用户确认 D60 设置窗首版即如此**（非 D110 引入——
  D110 仅改行内容与行数，渲染管线未动），历次手工验收均在「凑合能看」状态下通过。
- **怀疑方向（未验证）**：次窗独立 `material.Theme` 的字体/shaper 装配（文本测量的宽高返回
  近零 → 布局塌缩）；次窗 DPI/scale 取值（D90 元素缩放进次窗后 material 尺寸异常）；
  `winmgr.runSecondary` 的事件循环与主窗共用的输入选项差异。首步 = 真窗渲染冒烟
  （`winmgr_smoke_test` 扩展：截帧比对行序与控件宽度），再二分定位。
- **决策**：**暂缓**——用户拍板先记录不修（日常可用性尚可凑合）；排在 S4a（大文件拆分）之后
  或与其合并进行（拆 `settings.go` 时顺带整修渲染）。**S4a 落地时（2026-10-07）settings.go 未拆**
  （P2 范围用户拍板排除）——拆分与渲染修复一并归入本步。修好前 D110 的 GUI 侧验证由 headless
  测试等价承担（profile 切换/重启 e2e：`TestRunProfileSwitchRestartE2E` 等）。

### S4b · GUI 跨平台抽象层（M8，P1，L，**执行序第三，新增，D111**）
> **执行序（D109）**：紧随 S4a（大文件拆分，见上）之后、S3 之前——结构改动、不新增用户可见功能；做完后 S3 的 relation-map 与后续设置窗功能都不必只写 Windows。

- **动机（D111）**：§15.6 现状 = GUI 交互层仅 Windows 实测，非 Windows 全为 no-op 桩（`win32_other.go` / `winmgr_other.go` / `theme_other.go`）；与「轻量跨平台单二进制」首要原则冲突，且越晚抽、长在其上的功能越多、重写越贵。
- **改动面**：
  1. **平台接口**（建议 `uigui` 内平台子面或独立小包）：窗口句柄与帧事件、显隐/置顶/移动/尺寸、**像素提交**（ULW 或其降级）、托盘、全局快捷键、文件对话框、右键菜单（TPM）、系统深浅色、DPI 查询。
  2. **核心逻辑去平台化**：`uigui` 主体（布局/动画/状态机/命中/渲染状态机）不 import 平台实现；Windows 实现 = 现有 `win*_windows.go` 逻辑原样迁入。
  3. **非 Windows 降级实现（非桩）**：Gio 常规不透明窗渲染（无 ULW 半透明/羽化）、托盘/全局热键缺失（发降级 notice）、文件框经 portal/命令、深浅色经系统 API、DPI 经 Gio Metric。
- **范围**：抽层 + 非 Windows 编译通过 + Gio 常规窗可交互（**降级但非桩**）；**不追求三平台功能对等**（ULW 等价物/托盘/热键留后续）。
- **出口**：三门禁全绿（含非 Windows 交叉编译 `GOOS=linux go build ./...`）+ Windows 行为零回归（headless + 手工验收）；文档明示非 Windows 已知降级。
- **后续可选**：纯 Go CPU 光栅化自绘底座（换掉 Gio 渲染 + 各平台位图提交），本步不做。

### S6 · 工具与 MCP 管理（M9，P1，M）

- 设置窗「工具与 MCP」页：MCP 列表（状态：已连接/停用/重连中/授权拒绝）、启停开关（已有 host 能力）、授权 grant 管理、日志入口（`/plugin logs` 同源）。
- 内置工具启停：现无机制 → 需小改（config 或运行态白名单，经端口注入 ToolRunner；文档先行）。
- 跨窗刷新：host 状态变化 → 线程安全回调/注册表通知到设置窗（复用 winmgr 注册表）。
- 测试：假 MCP server（发现/调用/超时/崩溃重启/授权拒绝）已有测试基建，补 UI 数据面契约。

### S7 · 系统提示词管理：人格组合与模块化注入（M9，P1，L）

- 形态已定（§5-Q4：保持首节点快照），建议：**模块 = 命名片段**（数据目录 `prompts/*.md` 或 config 段）+ **人格 = 有序模块引用列表** + 全局兜底模块；装配点在 `app/prompt`。
- 与 D20 关系：会话创建时物化为 persona 首节点快照（改动只影响新会话）——保持「树内可 `/edit`、水位恒回传」不变。
- UI：设置窗模块库编辑 + 人格组装（勾选/排序/预览最终 system 全文）+ 当前生效人格指示。
- 测试：装配顺序/替换/兜底的纯函数测试 + persona 快照口径回归（性质测试不动）。

### S8 · ASR/TTS 接入（M10，P2，L，backlog D27；**选型未定——§5-Q7 暂留空，动手前先补决策**）

1. spike：适配器选型（whisper-api / whisper.cpp / 系统 SAPI 朗读 / 云 TTS），实测音质与依赖（cgo 红线！`go build` 单二进制无 cgo）；
2. `port.Transcriber`/`Synthesizer` 实现装配（横切同款：main 装配处）；
3. 麦克风采集 `Kind=mic` 摄取 + GUI 输入栏语音按钮（与附件按钮同排）+ 转写结果回填输入框；
4. `/mic` 命令（TUI 侧同源）、`output.tts` 播报输出器启用。

---

## 3. 依赖关系与建议排期（**执行序已按 D109 重排**）

```
【已完成】S1 修复包 → S1b 响应式 → S1c 跨块拖选 → S2 气泡右键 → S2b 输入行 → S4+S5 配置底座（D110）
                          （S1–S2b 与前置 A/B 数据面均已落地；S4①② 与 S5 已落地，S4③ 欢迎窗待做）
【暂缓】S4c 设置窗渲染异常（D60 首版即有，用户拍板记录暂缓——修好前 GUI 侧验收由 headless 等价承担）

【M5 后续执行序（D109，2026-10-06 重排）】
  ┌──────────────────────────────────────────────────────────────┐
  │ ① 破坏性改动（最先）                                          │
  │    S4 配置底座(profile 目录化 + 全量设置窗)  ┐ 合批，schema   │
  │    S5 模型 provider 列表化 + fallback        ┘ 一次改到位(D110)│
  └───────────────────────────┬──────────────────────────────────┘
                              ↓
  ┌──────────────────────────────────────────────────────────────┐
  │ ② 大文件拆分重构（S4a，纯代码组织、无行为变更）【已落地】      │
  │    window.go/session.go/main.go/openai.go 等按主题拆薄        │
  └───────────────────────────┬──────────────────────────────────┘
                              ↓
  ┌──────────────────────────────────────────────────────────────┐
  │ ③ GUI 跨平台抽象层（结构改动，D111）                          │
  │    S4b 平台接口 + Windows 迁入 + 非 Windows 降级实现（非桩）  │
  └───────────────────────────┬──────────────────────────────────┘
                              ↓
  ┌──────────────────────────────────────────────────────────────┐
  │ ④ 对话树 UI：relation-map（D112）                             │
  │    S3 内核补洞(/rm 任意会话·改名) + TreeView 整树快照 + 关系图 │
  └───────────────────────────┬──────────────────────────────────┘
                              ↓
  S6 工具/MCP 管理 · S7 提示词模块化 · S8 语音（可并行 spike）【顺延】
```
- **执行序原则（D109；S4a 2026-10-06 增补）**：**先动 schema（不依赖 UI 形态）→ 再拆大文件（S4a，纯代码组织，为抽平台层减负）→ 再抽平台层（结构改动、不新增用户可见功能）→ 再长 UI（消费者）**；串行，S6–S8 顺延。
- **破坏性改造集中在 S4+S5 一次做完**（config schema、数据目录、模型配置结构），避免来回改——这是**第一顺位**，因为破坏性窗口（无生产数据）越晚用越贵。
- **语音 spike** 可在任意间隙并行，不阻塞。
- **S2b 输入行**已落地（D103–D106，见 §2-S2b 状态）。
- 每步仍是：**文档先行 → 测试先红后绿 → 三门禁 + 冒烟 → 单事 commit**；里程碑进度只更新本文顶部基线行（步骤状态在 §2 各步）。

## 4. 本次开放的破坏性修改清单（授权范围）

无生产数据，以下可直接改、**不写迁移器**：

- config.json schema（新增/改名/重构段），旧文件解析失败给清晰报错即可；
- 数据目录布局（含 profile 目录化）；
- UI 几何、动画常量、菜单项文案与项序、快捷键默认值、托盘/logo 菜单项集合；
- 会话存储文件格式（如需），损坏即重建；
- 现有 GUI 内部结构（window.go 持续膨胀 → 可借机拆文件；**已立为 S4a，见 §2**，范围扩到全部大文件）。

**仍然不许动的红线**：`pluginapi/v1`（稳定级，尚未建、建后只增不改）；`domain` 节点不可变与 Revise/Carry 语义；流式不进领域；依赖方向 `adapter → port ← app → domain`；内置实现必须经端口；无工作区概念。

## 5. 设计决策记录（Q1–Q16 已全部拍板，2026-09-30；Q7/Q17 留待动手前）

| # | 问题 | 决议 |
|---|------|------|
| Q1 | profile 切换粒度与数据隔离 | **进程级**（`--profile` 启动参数 + 设置窗切换需重启）；会话/记忆数据随 profile **整目录隔离** |
| Q2 | 气泡右键菜单呈现 | **复用 D72 TPM 管线**（shell 线程 `TrackPopupMenu` + 共享 `menuDispatch`）；Gio 侧只做行命中 + 武装-原位抬手 |
| Q3 | 「重新生成」语义 | **重发上游用户消息**：取该分支上游最后一条用户消息，开同父兄弟节点重发（旧回答保留为历史分支）；编辑/重生成均分叉新节点 |
| Q4 | 人格模块化物化时机 | **保持首节点快照**（D20 不变：树内可 `/edit`、压缩水位恒回传）；设置窗明示「对新会话生效」 |
| Q5 | fallback 触发与提示 | **仅网络类降级**（429/5xx/网络/超时 → 下一 provider）；401/400 不降级（配置错误直接暴露）；降级时状态行一次性提示 |
| Q6 | tooltip 上下文使用率数据源 | **精确优先、est 兜底**（适配器有精确 tokenizer 用精确，否则 est 估算链）；分母 = `limits.max_context_tokens` |
| Q7 | ASR/TTS 选型 | **暂留空，未来决定**——S8 只保留里程碑占位，动手前先补选型决策 |
| Q8 | 「形裁成功前隐藏窗口」现象 | **启动时闪现未定制窗口** → 首帧内容/ULW 提交前保持隐藏，首帧就绪后再显示 |
| Q9 | 内置工具是否可禁用 | **可禁用**——工具管理页统一「内置 + 插件/MCP」两组开关（config 新增禁用清单） |
| Q10 | 设置窗 raw JSON 入口 | **保留**高级页直接编辑 config.json 原文（解析失败不阻断开窗、泛键保留已有） |
| Q11 | 主窗口大小调整方式 | **双旋钮、仅配置/设置，暂无直观拖拽**：`ui.scale` 元素缩放（150% = 元素大 50%）+ 窗口尺寸像素配置（304×920 = 更窄更长），布局随空间推导重排 |
| Q12 | UI 响应式迁移范围 | **主窗布局由可用空间推导**（min/max/比例约束），次窗不迁移；字号/控件基准不随窗口尺寸变，由 `ui.scale` 显式缩放 |
| Q13 | 底部淡化区 | **常驻视口底缘矮带 + 等高底部留白**（上滚离底后底缘渐隐更自然；贴底时最后一行在带外）。带高 = **12dp**（D79 实测定值，取「几～十几 dp」薄带，薄于原建议的顶部 `fadeBandDp` ~40% / 24~32dp） |
| Q14 | 「附件解析」档位 | **加常见文档转文本**：pdf/docx/html/… → 提取文本入树；图片/音频仍走字节内联与既有路径（仅接既有 ingestfile 无新增，故不在档） |
| Q15 | 命令提示与补全做哪些端 | **只做 GUI**（§15.2 浮层口径，含动态 `/mcp:*`）；TUI 保持无补全现状 |
| Q16 | 第二批补充 3 项的归置 | **合成一个「输入行」阶段 S2b**（命令补全 + 附件按钮/解析 + 展开按钮），不加长 S1；与 S2/S3 可并行 |
| Q17 | 文档转文本实现选型 | **已拍板（D105，2026-10-06，spike 实证）**：pdf=`ledongthuc/pdf`、docx=`gomutex/godocx`（MIT；fumiama/go-docx 系 AGPL 否决）、xlsx=`xuri/excelize/v2`、html=`golang.org/x/net/html`；pptx/rtf **缓**（gopptx Windows embed 构建损坏 / 无成熟纯 Go 库），回落二进制占位 |

## 6. 每步通用执行清单（沿用现状）

1. 改 DESIGN/`docs/decisions.md`（新 Dn）先行 → 内部测试先红 → 实现转绿；
2. `gofmt -l .` 空、`go vet ./...` 过、`go test -race ./...` 全绿、`AQUARIUS_GUI_SMOKE=1` 冒烟 ok；
3. commit 英文祈使句、`git commit -F temp/…`、一个 commit 一件事、不 push（未获指令）；
4. 进入模型上下文的字符串英文，界面/注释中文；GUI 大改动手工验收（headless 测不了真窗）。
