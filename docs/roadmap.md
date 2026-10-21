# Aquarius 阶段规划（TODO 整理与分阶段路线）
<!-- docs-sync: 2026-10-21 @ 728c0de -->

> 来源：无对应 DESIGN 节——本文是**阶段规划与工作清单**（权威设计 = `DESIGN.md`，决策 = `docs/decisions.md`；
> 与二者冲突时以后者为准）。§5 决议在动手落码时按惯例仍需回写 DESIGN/decisions（先文档后代码）。

- 里程碑基线：**M0–M4 已落地**；当前推进 **M5 GUI**（S1/S1b/S1c/S2/S2b 已完成）。**M5 后续执行序重排（D109，2026-10-06）**：对话树 UI（S3）之前的日常体验与内核命令已落地 → **配置底座破坏性改造（S4+S5 合批，D110，已落地）→ 大文件拆分重构（S4a，纯代码组织，已落地 2026-10-07）→ 设置窗渲染异常修复（S4c，D113，已落地 2026-10-07）→ GUI 跨平台抽象层（S4b，D111，已落地 2026-10-07）→ 对话树 UI：relation-map（S3，D112；形态规格 D120，2026-10-08；**§10.3 落码全步已落地，见下 S3**）→ 设置窗分组导航（S4d，D142，已落地 2026-10-17）→ 工具与 MCP 管理页（S6，D143，已落地 2026-10-17）→ 人格模块树（S7，D144；内核 S7a 与设置窗独立页 S7b 已落地 2026-10-18，D145）→ 次窗换 Paper 主题（S7c，D148/D149，已落地 2026-10-18；主窗不动）→ **会话与注入内核改版（S9，D150–D167，已落地 2026-10-20）** → **模型面与文件工具整治（S10，D169–D172，已落地）** → **模型与提供商重构（S11，D173–D177，已落地）** → **屏幕捕获（S12，D178，已落地 2026-10-20）** → **语音输入与播报（S8，D179，适配器/命令/语音键/设置窗「语音」页已落地 2026-10-21）** → **对话交互补强（S13，D184–D188，口径已定 2026-10-21；①②③ 已落地，④⑤ 待落码）**，见 §2/§3

- 基线：`3be7c91`（D75 已合入，决策记录 D1–D75），2026-09-30
- 输入：用户 TODO 清单 10 大项 + 「UI增强与bug修复」7 小项 + 补充 3 项（响应式迁移、主窗口大小调整、消息区底部淡化区）+ 第二批补充 3 项（命令提示与补全、附件解析、预留按钮实装 → 合成 S2b 输入行阶段）
- 决策状态：**Q1–Q16 已全部拍板**（§5，2026-09-30）；**Q17 已拍板**（D105，2026-10-06：pdf/docx/xlsx/html 现成纯 Go 库、pptx/rtf 缓）；Q7（ASR/TTS 选型）留待动手前补充
- 前提：**单开发者、单用户、无生产数据 → 破坏性修改窗口开放**（见 §4）
- 规模记号：**S** = 半天~1 天，**M** = 2~4 天，**L** = 1 周+（含测试与文档）

---

## 1. 现状盘点（TODO → 已有基础 → 真实缺口）

| # | TODO 项 | 已有基础 | 关键缺口 | 规模 |
|---|---------|----------|----------|------|
| 1 | 设置界面，config.json 解析 | 设置窗核心档已落地（模型/权限/think/effort/hotkey/主题；`settingsSnapshot`/`persistSettingsTextKeys`/`applySettings` 写回 + 热生效，密钥只写不回显） | 全量 `fileConfig` 编辑（limits/output/system_prompt/mcpServers/plugins/…）；**分组导航已落地（S4d/D142：五页 + 左侧导航栏 + 全局底栏）**；未知键保留 | M |
| 2 | 欢迎界面，引导配置与教程 | `winWelcome` 独立窗壳已存在（D60 多窗宿主） | **已落地（D141）**：三步页（接入模型三问 + 教程 + 完成）+ `ui.welcomed` 标记 + 启动流接管（装配前独立向导窗）；入口在设置窗 | M |
| 3 | 会话管理界面（树状） | `ConversationStore.List` 已有；`/list` `/goto` `/branch` `/rm` 命令已有；winHistory 窗壳已存在 | ① **已落地（D75）**：切换到已有会话 + `/new` 后回放；仍缺**删除任意会话**命令（`Remove` 无命令调用者）与**改名非当前会话**（`/title` 只改当前）；② 会话树结构进 UI 的**只读数据面**（**已落地（D80/§7.5，前置 A）**：`port.TreeView` + app 原子发布快照——树状 UI 自绘可直接消费）；③ 树状 UI 自绘 | L |
| 4 | 消息气泡右键（编辑/重生成/复制） | **已落地（D92）**：右键手势（气泡底板矩形命中）+ TPM 菜单 + `/regen`（上游用户消息分叉重发）+ 编辑态（结构化 `/edit` 直达）+ 复制（选区优先/整条渲染文本）；`/edit` 补清屏回放 | 可增项：引用/查看原始块（roadmap 预留）；thinking/chip 块的右键 | M |
| 5 | 配置文件化（多配置独立与切换） | config 单文件单路径（数据目录 `config.json`），读/写/警告链路清晰 | config **schema 破坏性改造**（profile 目录布局或 `profiles/` 段）；加载/写回全链路；设置窗 profile 切换 UI；CLI 参数 | L |
| 6 | 模型与提供商管理 + 自动 fallback | `modelConfig` 单提供商；LLM 适配器（OpenAI 兼容）；重试装饰器（429 退避）已在 main 装配处 | provider 列表化 schema（破坏性，建议与 #5 同批）；fallback 链语义（错误分类 → 降级顺序 → 状态行提示）；设置窗管理页 | L |
| 7 | 工具与 MCP 管理 | plugin host 已支持运行时 `Enable/Disable/Status/Grant`（D31），`/plugin list\|enable\|disable` 已有，config `plugins`/`mcpServers` 段已有 | **已落地（S6/D143）**：设置窗「工具与 MCP」页（内置工具启停热生效 + 插件启停/逐能力授权撤销与预授权/撤销信任/日志入口）；`/plugin logs`；跨窗原子快照（`Revision`）+ 异步动作 | M |
| 8 | 系统提示词管理（人格组合、模块化注入） | `system_prompt` 单串 → persona 首节点快照（D20）+ 无 persona 时 config 兜底注入 | 模块库（命名片段）+ 组合规则；保持 D20 首节点快照（§5-Q4 已定）；prompt 装配点改造（app/prompt）；设置窗编辑器 | L |
| 9 | ASR/TTS 接入 | **已落地**（D179，2026-10-21）：`asrchat` / `mimotts` / `micwin` / `audioplay` / `/mic` / `output.tts` / GUI 语音键（空输入占用发送键位）/ 设置窗「语音」页（服务与模型、音色与风格、试听） | provider/模型/音色/模式/风格指令/标签 + 试听 | S |
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

### S3 · 会话管理界面：relation-map（M7，P0，L，**形态改定：relation-map，D112；形态规格 D120**）
> **执行序（D109）**：S3 **顺延至 S4（配置底座破坏性改造）+ S4a（大文件拆分）+ S4b（GUI 跨平台抽象层）之后**——它是 UI 消费者，落在前几者稳定的地基上（平台层就位后 relation-map 不必只写 Windows）。
> **形态规格（D120，2026-10-08） = [docs/relation-map-design.md](relation-map-design.md)**：L1 语义（结构根/锚点拆分、类别=Role、断言=父→子）+ L2 通道分配（层号深度色带 / 形状=Role / 直径=权重 / 描边=主干 / 版本链走线型）+ L3 行为（斥力+角度约束+层序投影、交互动词表、两态生命周期）+ 公理集 A1a/A1b/A2–A11 + 派生符号 + 自检清单。**该文是落码的验收依据**，逐条对应测试。
1. **内核先行**（文档先行：DESIGN §7 + 新 Dn）：
   - **已完成（D75，`3be7c91`）**：`/switch <id前缀>` 切到既有会话 + `port.ClearEvent` 清屏（`/new` 同清）+ 切换后自动回放；
   - **已完成（D120，`53874c3`）**：**删除任意会话**（`/rmconv <id前缀>`，`ConversationStore.Remove` 落盘；删除当前自动顺位到最近更新的剩余会话，无剩余则新建空会话 + 清屏回放）、**改名非当前会话**（`/title --id <前缀>`，首参位 flag 同 `/edit` 口径）。二次确认复用 `/rm` 口径。
2. **数据面（D112②/D120⑦）**：前置 A（树只读视图端口，D80）**扩展为整树快照**——`port.TreeView` 增 `Graph()`（`TreeGraph{Anchor, Nodes[], Total}` + `GraphNode{ID,Parent,Role,Snippet,CreatedAt,EdgeKind,SiblingIdx,SiblingCount}`），app 侧 `atomic.Pointer` 原子发布（复用 D80 发布点）。**左栏另立只读端口 `port.SessionLister`**（`List() []SessionSummary`，数据源已有 `ConversationStore.List`；D80/D103 同款反向端口）。`Level`/`Depth`/`InPath`/扇区/坐标/配色**不入端口**，全在 UI 侧派生。**已落地（`cf6ee83` + `internal/app/sessionlist.go`）**。
3. **UI（D112①/D120⑥，已落地 `a565ee2`+`ff50adb`）**：winHistory 占位壳 → **左栏会话选择 + 右栏 relation-map 关系图 + 状态栏（`共 N · 呈现 M`）**：
   - 右图：节点 + 父子连线（**父→子为向**；版本链走**线型**——D120③ 改判：D112 原记「虚线/异色」的**异色**因边色归 source 层号而取消）+ **当前 Head 主干描边**（A11）+ 点节点 `/goto` + 悬停一句话陈述 + 右键（改名/删除/复制 ID，复用 D72 TPM 管线）。**承接 D96 后果③**：Clone 拷贝子树挂新节点之下，graph 用版本链标注关系（v1 因 `KeepMode` 未持久化合并为一种线型，D120④）。
   - 左栏：会话列表（标题/时间/消息数，当前项高亮）；点击 = `/switch <id前缀>` → app 重建快照 → 右图重锚到新 Head。
   - 落点：**纯 Gio 自绘**（层号→颜色、斥力+角度约束+层序投影、视口裁剪、hover、滚轮缩放越界转平移），无三方依赖。
4. 测试：命令层 golden/脚本流回放；数据面契约测试；**布局 spike（D112 后果②，落码前）——50/200/500 节点单帧迭代与绘制耗时定档 `maxIter`/`hardCap`**；派生纯函数性质测试（A4/A5/A11 等）；UI headless 构建与手势测试 + 离屏渲染断言 + 手工验收。**已落地（`2d8befb` spike / `d350466` 派生 / `a565ee2`+`ff50adb` UI）——命令层 golden、`-race` 通过；离屏渲染断言见 uigui 测试。手工验收仍待。**
5. **状态（2026-10-08）**：**形态规格已定案（D120 + [docs/relation-map-design.md](relation-map-design.md)）**；核码未动——落码按 §10.3 序（内核补洞 → `Graph()` → `SessionLister` → 布局 spike → 派生纯函数 → 左栏/状态栏 → 右图 → 文档同步）。**已全部落地（2026-10-08）**：见下文各步状态。
   - **后续增强（2026-10-08 记录 → 2026-10-09 全部落地，D121）**：① **节点可拖**——按住节点拖动 = **临时固定**该节点于指针位置（每帧带该固定点松弛，邻居被斥力实时推开），**松开即恢复自由**、自然收敛（非持久固定，R3 交互态例外 + A12）。② **节点更大 + 内容入内**——标签画进节点卡内（卡底随层色着色），`relNodeBaseDp` 26→34。③ **斥力从卡片边缘起算**（卡片占位喂给求解器；相叠按穿透深度推开，R6）＋**废除角度硬投影**（改为兄弟槽位 + 水平受力 + 层序投影承担 180° 硬上限）。④ **开窗即适配整树**（A13，按卡片外接盒取最小 zoom，「回到当前」仍居中锚点 A8）。⑤ 主干可辨（A11）改由**边描边 + 本色加粗**承担（节点不再套白圈），分叉虚线外环**沿形状**走，版本下标 `i` 画进**节点图形内部**（`n` 暂不落）。规格改判见 [relation-map-design.md](relation-map-design.md) §3.1a/§5.1/§5.2，决策 [D121](decisions.md)。**实测反馈修订（2026-10-09）**：① **抓手半径放宽**（`relGrabSlop` 14dp，**按下那一刻**判定）——低 zoom 下卡片塌缩成小徽标，严格命中差几像素就落空，「拖节点」会静默退化成平移（实测报障）；抓手按下即定、拖动途中不改判（否则拖过节点会被半路劫走）。② **悬停 tooltip 暂缓**（实测「太大」）：`drawTooltip` 移除，`relStatementOf` 文案派生与其测试保留，UI 改小/改位置后再接回。**第二轮实测修订（2026-10-09，D122）**：③ **跟手拖拽**——抓取偏移逐帧衰减（~120ms），节点中心平滑滑到光标正下方（「光标与节点不许错位」）。④ **秒级入场动画**——开窗从根点逐层展开到收敛布局（`relEntryFrames`≈3s，ease-out，按下即打断；数据变更不入场动画）。⑤ **视场修复**——fit 下限提到标签可读下限（0.75，不再适配出全裸点）；fit 只在开窗首帧、数据变更不重置 zoom、拖后松手把树拉回视野；滚轮越界平移改定值步长 48px（一格 1600px 甩飞的元凶）；顺带修箭头/标签卡宽/拖拽 slop 的 `z` 混用与视口 clip。规格改判见 [relation-map-design.md](relation-map-design.md) §5.1–§5.4，决策 [D122](decisions.md)。**全程动画 spike（2026-10-09，D123）**：验证 200 节点全程动画 + 用户拖拽能否 60fps —— 入场动画帧 ≈0.52ms、拖拽帧 ≈0.49ms（预算 ~3%，裕量 >30×），**可行**（基准已固化 `relmap_anim_bench_test.go`）。迁移规划：rebuild 一律入场动画（去掉首帧限定）→ 用户操作不打断（删 press 取消）→ 动画期间拖拽 = 被拖节点单独 pin、其余继续播，结束无缝切 solver。规划见 [D123](decisions.md)，实施待排期。**常驻物理重构（2026-10-09，D124，取代 D123 入场动画口径）**：用户否掉「入场 lerp」——布局内核 `relmap_layout.go` 重写为**常驻质点-弹簧物理**（边弹簧父子等大反向 + 每节点静息锚 + 全对斥力排祖先 + 半隐式欧拉 + 每帧 6 子步稳定化），拖任一节点整树按物理规律动（动量经弹性网络传导），动能 < 阈值即静止停帧（R3）、任何交互自动唤醒；删除梯度求解与入场动画全部机制；`treeBox`/fit/recenter 改按**静息布局**基准（fit 仍仅开窗首帧、保持 zoom）。**实测**：200 节点纯物理步进 ≈0.10ms、全链路帧（6 子步+绘制）≈0.88ms（60fps 预算 ~5%）；链 120/随机 200/星 60/二叉树播种收敛 ≈176/99/117/84 帧，平衡态距静息 ≤~30dp；拖拽 ~95% 节点位移 >1dp、松手回平衡。决策 [D124](decisions.md)，物理设计稿 [docs/relation-map-physics-design.md](relation-map-physics-design.md)。**物理档转正（2026-10-12，D128）**：把 RSM v1.2 附录 B 的源实现力参数（`repulsion 900 / edgeLength 200 / gravity .1 / friction .2`）**与源实现的步进算法**整体搬进本内核并转正为**生产默认**（`relmap_layout_source.go`）——搬算法而非只搬参数，因为源实现的运动时长由结构算出：`friction` 同时是步长缩放器、每步 ×0.992 的衰减时钟、跌破 0.01 的停判据 ⇒ 步数恒 **373**（六档夹具全同，与规模无关，A15 兑现；RSM 档案实测 373×16ms = 5968ms）。D127 力积分档保留为 `AQUARIUS_REL_PHYS=d127` 对照回退（启动读一次）。**实测代价（待补清单，已登记 `docs/relation-map-design.md` §5.4 / decisions.md D128）**：① 层序 R4 不再由物理保证（3~46 处违反）；② 松手采纳槽位 A12 失效（弹簧拉向「距父 200dp」）；③ 无卡片接触推力 ⇒ R6 失守（面积重叠 0.5~0.97）；④ 判据是 friction 代理量（A17，判停时残差 880~8740dp）；⑤ `relBound = 20000` 小于合法深链跨度（≥124 条消息首帧钳塌）；⑥ 拖拽波及全图（Head 被带偏 113dp）。编码 D127 内核内部机制的两个测试已删，其余布局性质 + 全部帧层测试在源档下全绿。决策 [D128](decisions.md)。**四项补齐（2026-10-12，D129）**：① **时长配置化**——用户实测参照程序极端情况动画可达数十秒（原档案记的 ~6s 不是上限），取 `relSrcScheduleSteps = 900` 帧（15s 上限）为显式配置，衰减率由它反推，并叠加**逐帧位移早停**（0.3dp/帧）⇒ 「到点或到位都停、取先到者」。② **树形保持**（要求多方案比较）——五候选实测：弱弹簧/硬投影/限速投影（链上沿链累加、chain60 残留 27 处）/软锥形；采纳**结构锥形通道**（`relSrcConeK=2.0`，连续通道、被 friction 缩放 ⇒ 永不钳位、越接近满足推得越轻），六档夹具层序违反归 0 且单帧最大位移仅比基线多 ~25dp。③ **引力改均匀树锚**——源实现的收缩式引力把长链压成球（chain200 压到 40dp 列距、卡片全叠），改为「只平移不形变」后深链盒宽恢复到静息跨度的85~94%、残差降 5.6×。④ **视口有界滞后跟随**（「不要锁着节点/树，要缓慢跟随才看得到动」）——每帧软逼近 10% + 可见性安全网 + 近则精确落位 + 拖节点冻结；状态栏新增三个聚焦按钮（回到初始态 / 聚焦当前历史 / 聚焦当前节点，域为 Head 及父子一跳）。另补两项待办：`relBound` 改随静息跨度缩放（≥124 条消息首帧钳塌的 bug，chain200 残差 32,481→5,132dp）与卡片接触通道（R6 部分改善：star30 0.78→0.58、rand50 0.69→0.39，binary4/rand200 无改善）。**采纳槽位 A12 经用户拍板暂不修**。删 3 个口径失效的测试，新增 `relDriveQuiet`。决策 [D129](decisions.md)。**弹簧减半（2026-10-12，D130）**：用户目视反馈「弹簧有点长」——`relSrcEdgeLength` 200→100dp（层间距旋钮），结构锥形下限与静息列距随之联动（否则旋钮被锥形吃掉）；顺带给斥力累计加逐节点限幅 40dp（卡片堆叠时单帧甩 743dp 的抖动消失）。整棵树宽减半、链类残差从 784dp 降到 174dp。决策 [D130](decisions.md)。**zoom 放宽 + 松手不抢视场（2026-10-12，D131）**：用户报障「初始视角不适配、装不下所有节点；松手必回同一构图」——根因是 fit 用了标签可读下限 0.75 当地板、zoom 钳制 [0.5,1.5] 又挡一道、松手还无条件重定中。改为**按树的大小算 zoom**（四者取小：整树装进视口 / 过去部分装进基准位以左 / Head 卡装右侧余量 / 视高），钳制放宽到 **[0.1, 2.0]**，可读性交给字号钳 + 标签预算（低 zoom 标签全隐 = overview，只剩徽标与边）；松手**不再动视场**（D129 起不写回静息槽位 ⇒ 拖完自己收敛，D121 的顾虑已不成立）。决策 [D131](decisions.md)。**图节点右键并入气泡菜单（2026-10-12，D132）**：关系图节点右键不再自绘 Gio 浮层，改走**气泡菜单同款原生菜单**——项集 = 气泡七项（编辑/编辑并转移历史/编辑并复制历史/重新生成/复制/引用/查看原文）+ 分隔线 + 图侧三项（复制 ID/删除（/rm）/回到当前），手势也改「按下武装 + 原位抬起」。全文经新增只读端口 `port.TreeView.NodeText(id)` 取（摘要 40 字不能用来编辑）；root/system 与无正文节点收敛为只读档。决策 [D132](decisions.md)。**输入栏选区高亮可见（D133，2026-10-13）**：用户报障「输入栏不能拖选、拖动只移光标」——实测拖选通路一直是好的，缺的是**可见性**：可见像素来自淡出源合成，而它的 Source 是零值⇒ `gtx.Focused` 恒 false ⇒ Gio 编辑器的选区高亮（与 caret 同一门控）从不进位图（D62 只补了caret）。改为淡出源自绘选区（`Editor.Regions` + selection 色，文字底下），三处编辑器布局点统一接。决策 [D133](decisions.md)。**DIB 释放复核（D134，2026-10-13）**：外部审查报「主窗每次变尺寸泄漏一个 DIB」，经规模实测证伪（阳性对照 +146.9MB / 现有路径 +0.0MB；`SelectObject` 换选对内存 DC 失败故「先换选再删」是伪修法）——只采纳其成立的一半：**返回值查证 + 泄漏判据测试**。决策 [D134](decisions.md)。**揭示梯次跨 goroutine 读修掉（D135，2026-10-13）**：外部审查指出的第 2 条成立——梯次 goroutine 直读 u.fadeBuf 与帧循环的重分配+填充并发（既有字段层竞争，也有「提交正在被改写的位图」的内容层撕裂），而它能骗过 -race 是因为 representLadder 是包级var、被 newFrameUI 全局置 nil，该路径在所有测试里都被屏蔽。改为 spawn 前复制自洽快照（`fadeShot`）+ 梯次降为 UI 字段；新增并发压测（变异验证：换回直读即报两处 DATA RACE）。决策 [D135](decisions.md)。**工具收场轮的挂点回填（D136，2026-10-13）**：审查报的死分支成立——`flushedDrafts` 在回填判据之前就被 `[:0]` 清空，导致「助手说话 → 调工具 → 本轮收场于工具」这一常见形态永远拿不到 `msg.ID`，该 Turn 无分叉条挂点。改为清空前捕获末位索引；单测经变异验证（恒假判据下 ID=""）。决策 [D136](decisions.md)。**依赖漏洞治理（D137，2026-10-13）**：外部审查称「5 个可达漏洞」，用 govulncheck 实证为 **13 条**（x/net 4 条是审查漏报的 HTTP2 系）。已升：x/net 0.56→0.60、x/text 0.38→0.42、goldmark 1.7.13→1.7.17（真可达——uigui 直接 import goldmark）、x/image 0.38→0.43（生产不可达，仅 temp/ 一次性脚本能触到）。连带 go.mod 的 go 指针1.25→1.26（x/net 0.60 要求），破掉 D105「不升 1.26」的旧约定（已标注取代）。剩余 8 条全在标准库，需 Go 1.27.2 工具链，留待决定。决策 [D137](decisions.md)。**输入区根修（2026-10-09，D125）**：用户报障「点击整体右偏一个左栏宽」——右图的 `event.Op`/`gesture.Scroll` 挂在根区域（整窗、单位变换）上，而右图在 `layout.Flexed` 的左栏之后转置里绘制，指针坐标与绘制坐标相差一个左栏宽。根修 = `frame` 里把 `update` 整段包进**右图本栏 clip**（`clip.Rect(0,0,viewW,viewH)`）；一并修掉「输入区覆盖整窗 → 左栏点击被右图误收」。两栏回归测试 `TestRelGraphPointerAreaIsPane`（两栏夹具 `relPaneFrame`）。**物理改造 + 绘制修正（2026-10-09，D126）**：用户要求「不锚定节点，使之能自由移动，想个办法锚定树与视口」——废除每节点静息锚/根锚（节点可被拖到任何位置并留下），新增**树锚**（整树盒心 → 静息盒心的均匀外力 + 静止时平移钉死；均匀外力只平移、不变形）；长程斥力换成**卡片接触推力**（按轴隙、沿最小穿透轴推 ⇒ 静息布局即平衡态；长程斥力下平衡态比静息布局宽 30%~56%，按静息盒适配的视场装不下整树）；弹簧 0.25→6.0、子步 6→12（深链「手风琴模式」收敛时间 ∝n² + 枢纽模态 ω·dt<2 的稳定边界），`relSettleFrames` 400→900。附三项绘制实测修正：虚线方框直边贴 ±r（旧版画在 r−cr 上、虚线内缩一个角半径画进徽标）、版本下标与卡内标签改精确约束 + Center 排版（`Min=(0,0)` 时 Gio 的 Center 等于不平移，数字贴左上角）、标签多行占满卡片（≤3 行 + 显式行高，`HalfH` 按最坏缩放预留）。决策 [D126](decisions.md)。**第四轮实测修订（同日，用户实测「太晃」→「提高阻尼」）**：参数档位最终定为**按轴双弹簧 1.0/2.0 + 双轴阻尼 0.5/0.5 + 力限幅 `relPhysFMax=12`**（挡「VMax 钳位打败阻尼」极限环）——「约 10s 收敛」试验档（横向弹簧 0.012 + 阻尼 0.982）实测树来回摆十几下被否；软弹簧还连带两个真问题（势能面太平卡局部极小、接触力顶离静息槽 100+dp 使视场基准脱节），一并随加刚解决。同步落地：**松手采纳槽位**（`adoptSlot`：被拖节点当前位置写回静息布局，拖完留在原地不弹回，槽位按消息 ID 持久、rebuild 恢复）、**播种改向盒心收缩**（向根点收缩会让深链被树锚猛拽、平移过冲 4268dp）、**fit 回退改视图跟随锚点**（`fitFollow`：深链展开十几秒期间当前消息始终居中，用户平移即取消）、`snapToAnchor` 钉住期不平。**实测**：真实会话树收敛 40 帧/0.7s、单次轻过冲 ~55dp、平衡偏差 0.0dp；200 节点纯物理步进 0.23ms、物理仿真帧 0.77ms、拖拽帧 0.94ms（60fps 预算 ~5%）。细节见 [D126](decisions.md) 与本设计稿头部。**形态激进改判（2026-10-11，D127）**：源设计语言（RSM v1.2，星图语法：中心锚定/引力向心/斥力自由形）与树结构错配（A8 锚点从来不是视口中心、Depth 无视觉通道、A16 锚点被拖偏 672.8dp 实测）——经同树三形态离屏 spike（`temp/geom-*.png`）目视，用户拍板取**时间轴横树**：x = 层号×列距（时间向右，根在左、Head 在右端）、y = 兄弟/版本槽（版本上下分杈）；Head 改为**视场右基准**（`relAnchorDatumX`≈0.72，长会话触底时右基准 + 视图跟随）；物理内核（D126 全套）几何无关原样复用，仅弹簧轴向刚度互换（X 2.0/Y 1.0）与 adoptSlot 横带化。形态规格文档已按 D127 全量重写为 v2.0，落码序见其 §10.3。遗留三条运动差距待后续决策：A16 时长配置化（吸引子日程方案可做到与规模无关收敛）、A17 锚点免疫（钉住 Head + adopt 平移补偿，实测可归零）、A18 残差判据（动能 → 逐帧位移）。**消息查看/编辑遮罩层（2026-10-14，D139）**：承接 D132 留下的「弹窗形态」——**单击节点**在会话历史窗内开自绘遮罩（scrim + 居中卡片），`呈现` 页签 = 主窗同款 markdown 渲染只读预览、`源码` 页签 = 原始 markdown 多行编辑器，**应用方式在界面上三选一**（Fresh/`--keep`/`--copy`，默认 Fresh）→ 提交结构化 `Command{Name:"edit"}` 经输入通道，关遮罩 + 图重排 + 唤出主窗。隔离靠「遮罩开启时不取背景事件」（Gio 指针事件广播给全部命中 handler，画 Clickable 挡不住）；数据面仍只经 `port.TreeView.NodeText`，不动端口。**单击语义变更**：`/goto` 移进图右键菜单「在主窗定位」（并补唤出主窗——原先主窗收在球里时点了没反应）；菜单项集保留、编辑三项与「查看原文」语义改道为开遮罩。决策 [D139](decisions.md)。**视场契约 v2（2026-10-15，D140，用户截图报障）**：报「从初始到稳定末端节点完全不会动 + 舒展后部分节点出视口、下方大片空白」。根因两条：① `followAnchorStep` 的收敛判据把「距目标余量」误写成「本帧安全网位移量」且两轴用 `||` ⇒ 几乎每帧走「精确落位」分支，跟随实际退化成**硬锁**（D129 想要的「有界滞后、看得见动」从未生效）；② zoom 拿**静息盒**定基准，而源档物理平衡态的纵向比静息盒大 1.5~2.4×（`wide24x1` 反向 0.22×），且以 Head **一点**为锚（Head 在盒下端时整盒被顶出视口）。改法：跟随对象从「Head 一点」改为「**当前盒**」——V1a 盒内可见 / V1b 上下留白对称 / V1c 一维贴满 / V2 触底档（`relZoomMin`）只承诺 Head 在场 / V3 舒展期 zoom 单调不增、落定后放大一次填留白；P1 右基准位（`relAnchorDatumX`）与 P2 纵向**盒心**居中降为**偏好**（先钳进「盒在视口内」可行域）；状态栏与图菜单「回到当前」改名「**装下整树**」（`fitAll`：当前盒连 zoom 一起适配），两个聚焦域取景**也带 zoom 适配**（上限 `relFocusZoomMax = 4.0`）。实测（500×500）：`wide12x3` 出视口 27/38 → **0/38**、上留白 −461 → **0**；`fixture7` 上下 225/78 → **135/135**；`wide24x1` 151/238 → **79/79**。`relmap_layout*.go` 一行未动。决策 [D140](decisions.md)，规格同步 [relation-map-design.md](relation-map-design.md) A8/A13/§5.2。

### S4 · 配置底座：全量设置窗 + 配置文件化（M8，P1，L，**破坏性集中点，执行序第一**）
> **执行序（D109）**：S4 **最先做**（与 S5 合批）——破坏性窗口开放，底座越晚做、上层功能越多越贵；schema 定案见 **D110**。
> **状态（2026-10-07）**：**①② 已落地（D110）**——根指针 + `profiles/<name>/` 整目录隔离、`--profile`、设置窗 Profile 区（新建/复制/切换）、全量分组页（输出/限额/提示词/MCP 只读/高级 raw JSON）、旧格式两级显式报因（`ErrLegacyConfig`，不迁移）+ GUI 双击启动错误经 Win32 消息框呈现（D110 修订④）；**③ 欢迎窗已落地（D141，2026-10-16）**。MCP 与插件的结构化编辑器暂由高级页 raw JSON 承载（Q10），后续可做专用表单。
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
   - **已落地（D141，2026-10-16）**：三步页（接入模型 / 教程 / 完成）+ `ui.welcomed` 标记；**启动流接管** = 配置不完整（`secret:` 解析不出）且 `ui.kind=gui` 时，于内核装配**之前**先开独立向导窗，保存后同进程继续启动（`tui`/`repl` 保持 stdout 指引 + 退出）；**入口移入设置窗**（托盘菜单的「欢迎 / 首次运行引导」项删除；球右键菜单本就没有此项），另有「首跑 / 标记缺失」自动打开一次。

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
| input.go | `inputBar`、`inputExtraDp/Px`、`inputRowRects`、`hoverTip/hoverCard`、`updateEditor/drawCaret/drawEditorSelection/caretRect/caretLineH`、`updateReasonEditor/drawReasonCaret/confirmReason`、`submitEditor/cancelEdit/setExpanded` | ~420 |
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
  turnbuffer/tools。每步声明清单守恒核对 + 三门禁（含 -race）全绿。**P2 例外：`uigui/settings.go` 未拆**——
  S4a 时用户拍板留待 S4c 一并处理；**S4c（2026-10-07，D113）落地时仍未拆**（渲染修复不需要拆分），
  拆分按需留待。备注：`GOOS=linux` 全仓交叉编译受 gio/cgo 限制不可达（基线如此，实证与替代
  口径见 **D119**：非 Windows 门禁 = WSL 内原生 linux 工具链 `go build ./...` + `go vet ./...`）。

### S4c · 设置窗渲染异常修复（M8，P1，M~L，**新增 2026-10-07，已落地 D113**）
- **症状（用户验收截图，2026-10-07）**：设置次窗（winSettings）表单渲染破碎——多行行内容横向
  挤在同一行（标题/Profile/字段同行）、部分控件只剩灰色小条（文本未测出）、按钮渲染为整宽色带
  无文字、大片空白；滚动后出现整宽纯色横带。**用户确认 D60 设置窗首版即如此**（非 D110 引入——
  D110 仅改行内容与行数，渲染管线未动），历次手工验收均在「凑合能看」状态下通过。
- **归因（D113，实证非猜测）**：`settingsForm.list`（`material.List`）**未设 `layout.Axis`** →
  零值 `Horizontal` → `layout.List` 按**视口宽**（700px）而非视口高（775px）做填满预算
  （`nextDir` 的 `vsize` 取自主轴向约束），首行累计即超 700px → 迭代在第一行后终止
  （`Position.Count=1`）；内容仍按垂直堆叠绘制 → 只画出前三行、其余空白窗底。整宽纯色横带 =
  `providerApplyRow`/`actionsRow` 等 `layout.Flexed` 行拿到列表给的**横向无界**约束
  （`Max.X=1e6`）→ `editorBox` 宽 1e6、文字落视口外，只剩底色。主窗转写区**不用**
  `layout.List`（自绘纵向容器），设置窗是全工程唯一消费者，故 D60 起长期未被发现。
- **落地**：轴向在构造处定死（`newSettingsFormState`：`f.list.Axis = layout.Vertical`，属
  **构造不变量**，不依赖调用点补设）+ 表单构造拆为「快照无关骨架 / 快照填表」两段；新增离屏
  渲染回归测试 `TestSettingsFrameRowsFillViewport`（按真窗尺寸/缩放 700×775px @1.25 光栅化，
  断言窗体下半幅与底带确有内容——**已按「先复现再修」验证：还原修复行测试即红**）。
- **验收口径（避免误判）**：设置窗内容**不得只靠缩放截图目视验收**——缩略预览的重采样会把细笔画
  （如 `openai` 的 `p` 竖笔）弱化成"文字被裁"的假象（本步一度据此误判为遗留观感项）。判据 =
  **逐像素映射**（扫描取像的字符网格）＋ 离屏渲染断言：实测编辑框白底自 y=452 起、字墨自 y=468 起
  （上留白 16px = 8dp 内边距 × 1.25 + 基线间隙），文本完整无裁切。
- **验收补充修复（2026-10-07，D113 补充）**：上项只量了字墨**起点**、且测试帧为拉丁短词——用户实机
  复检（1.25 DPI，CJK 字面行高 ≈1.32em ≈ 25px > 定高框内容区 40−2×Dp(8) ≈ 20px）实证**编辑框
  文字底部裁切、标签「接口地址」折行**。三处裸 px 几何修复：`editorBox` 高 = max(Dp(fieldH),
  编辑器实测行高+2×留白)（Stack 自适应 + dp 下限，DPI/字体行高/后续字号缩放任一变化不再裁字）、
  `editorBoxH` 与 `fieldRow` 标签列改 `gtx.Dp()`；多多度几何回归测试钉死。
- **范围外（仍 P2）→ 已补齐（2026-10-07）**：`uigui/settings.go` 渲染修复落地后即行拆分（S4a 约定的
  时机）——拆为 `settings.go` 363（类型/状态/构造）+ `settings_actions.go` 319（保存/profile/provider/
  连通性动作）+ `settings_rows.go` 489（行计划与帧渲染），声明清单守恒、双渲染回归测试守门。

### S4d · 设置窗分组导航（P1，S，**已落地 2026-10-17，D142**）
> **执行序（2026-10-17 用户拍板）**：立为 S6（工具与 MCP 管理页）**之前**的一步——用户原话「先把设置窗分开几页，而非按组在同一个窗里一长串，现在这个效果不太好」。纯 UI 组织（渲染分页 + 导航状态）＋ 一处常态缺陷修复（模型名行），不改配置语义、不改 `SettingsPatch`/`ApplySettings` 契约。

- **改动面**：`settings.go`（页常量/页名、`pages [5]widget.List`、`page`、`navBtns`、`firstEditor`）；
  `settings_rows.go`（`pageRows(page)`、`rowSection`/`rowTitle` 收编、`navColumn`/`navItem`、`bottomBar`）；
  `winmgr.go`（几何 720×560 / min 620×420）。五页 = 常规 / 模型 / 对话 / 工具与 MCP / 高级
  （**S7b/D145 起六页**：「系统提示词」页插在「对话」之后；**S9-6b/D161 起七页**：「密钥库」页插在「模型」之后）。
- **顺带修复**：`f.model`（当前模型名）自 D110② 起填表/进 patch 却**无行渲染**（GUI 上无处可改，
  只能靠 `/model`）→ 「模型」页顶部补回 `rowModelName` 行。
- **验收**：逐页滚动无截断、切页不丢编辑、保存一次全量生效、125% DPI 下导航列不折行；
  headless 回归三条 = 行列表填充（新几何 900×700px @1.25）+ 逐页离屏有内容 + 导航点击切页。

### S4b · GUI 跨平台抽象层（M8，P1，L，**执行序第三，已落地 2026-10-07，D111**）
> **执行序（D109）**：紧随 S4a（大文件拆分，见上）之后、S3 之前——结构改动、不新增用户可见功能；做完后 S3 的 relation-map 与后续设置窗功能都不必只写 Windows。

- **动机（D111）**：§15.6 现状 = GUI 交互层仅 Windows 实测，非 Windows 全为 no-op 桩（`win32_other.go` / `winmgr_other.go` / `theme_other.go`）；与「轻量跨平台单二进制」首要原则冲突，且越晚抽、长在其上的功能越多、重写越贵。**开工前实测修正**：非 Windows 侧连「可构建」都不成立（`windowFromPoint`/`requestFileDlg` 无 `!windows` 实现，linux 直接编译失败）——已随 D119 补齐。
- **改动面**：
  1. **平台接口**（落位已定，见 **D111 修订**：独立小包 `internal/adapter/uigui/platform`，纯 Go 不依赖 gio）：窗口句柄与帧事件、显隐/置顶/移动/尺寸、**像素提交**（ULW 或其降级）、托盘、全局快捷键、文件对话框、右键菜单（TPM）、系统深浅色、DPI 查询。
  2. **核心逻辑去平台化**：`uigui` 主体（布局/动画/状态机/命中/渲染状态机）不 import 平台实现；Windows 实现 = 现有 `win*_windows.go` 逻辑原样迁入。
  3. **非 Windows 降级实现（非桩）**：Gio 常规不透明窗渲染（无 ULW 半透明/羽化）、托盘/全局热键缺失（发降级 notice）、文件框经 portal/命令、深浅色经系统 API、DPI 经 Gio Metric。
- **范围**：抽层 + 非 Windows 编译通过 + Gio 常规窗可交互（**降级但非桩**）；**不追求三平台功能对等**（ULW 等价物/托盘/热键留后续）。
- **落地（2026-10-07）**：① 平台面整体迁入 `uigui/platform`（窗口/几何/DPI/光标/显示器、整窗 ULW 像素管线、D78 隐藏与揭示、任务栏与置顶、托盘 + 全局热键 + 消息泵 + WM_CLOSE 子类化、原生菜单、文件框、次窗关闭/聚焦、快捷键解析与校验、深浅色、字体候选）；② `uigui` 侧改为**消费方接口 + `Host` 回调**（平台只回传动作枚举），中性策略/菜单项集与分发/跨窗注册表留在 `uigui`；③ 测试注入槽 `presentMain`/`revealMain`/`visMain`/`mainHWND` 退役 → **假平台注入**（`fakeplat_test.go`）＋确定性虚拟屏；平台包自带纯 Go 单测；④ 非 Windows 降级实现落细（`Decorated(true)`、跳过离屏合成、深浅色 `gsettings`/`defaults`、CJK 字体候选、`zenity`/`kdialog`/`osascript` 文件框、缺失能力**一次性**降级提示）。
- **出口核验**：三门禁全绿 + **非 Windows 门禁** `go run ./cmd/linuxgate` 绿（WSL 原生 linux 工具链 build + vet，口径见 **D119**；Windows 上不存在单命令 `GOOS=linux go build` 通路）+ Windows 行为零回归（headless/`-race`/真窗冒烟全绿；手工验收：悬浮球与输入栏收发、logo 右键菜单含权限二级菜单、托盘图标与菜单、设置窗渲染与滚动、Alt+A 呼出收起——用户确认正常）；非 Windows 已知降级已写入 DESIGN §15.6/§15.1（**交互未实测**，WSL 无 X 显示）。
- **后续可选**：纯 Go CPU 光栅化自绘底座（换掉 Gio 渲染 + 各平台位图提交），本步不做；非 Windows 的 ULW 等价物/托盘/全局热键（含 Windows 通知改进程内 `Shell_NotifyIconW` 气泡，见下方「已知缺陷」修法②）。

### 已知缺陷（2026-10-07 记录）

- **GUI：托盘图标模糊 → 已修（2026-10-13，用户报障「可以再清晰一倍」）**。根因不是图标源文件糊（`assets/icon/aquarius.ico` 里有 16/32/48/64/128/256 六档，`cmd/iconify` 逐档生成），而是 `newPlatform` 只取了 **16px 一档**喂 `CreateIconFromResourceEx`——平台拿到的是一张 16×16 单张位图，>100% DPI 的任务栏只能插值放大。修法 = 取 **32px 档**（4 倍像素量；shell 缩到 16 仍是干净下采样，125%/150% 下有原生像素可省一次插值）。回归测 `TestICOImageTrayAsset` 双钉：档位口径钉在字面量（改常量即红）+ 解析出的条目宽度必须真的等于该档（资产若被重新生成而丢档 ⇒ 立即红，不静默降级）。

- **GUI：Windows 通知弹出 powershell 窗口直至通知结束 → 已修（D117，2026-10-07）**。根因：Windows 端通知 = 拉起 `powershell` 子进程跑气泡脚本（`ShowBalloonTip(5000)` + `Start-Sleep -Seconds 6`，子进程存活 ≈6s——正是"直至通知结束"），而 **D114 双击启动 FreeConsole 后父进程无控制台可继承 → Windows 为子进程新开可见控制台**（D114 前继承的是已隐藏的自建控制台，不可见；终端启动场景同样会新开，只是原本不显眼）。修法① 已落地：`notifyWith` 分离启动前挂 `SysProcAttr{HideWindow: true, CreationFlags: CREATE_NO_WINDOW}`（`cmd/aquarius/notify_windows.go` / `notify_other.go` 桩），复现测试 `TestNotifyDetachedChildHidden` 钉住。
- **GUI：D114 后每个 stdio MCP server 各开一个控制台窗（长驻）→ 已修（D118，2026-10-07）**。`internal/adapter/mcpgate/gate.go` 的 `transportFor` 构造 `exec.Command` 无窗口压制，双击启动后 server 子进程各得一个可见控制台、活多久开多久。修法 = spawn 前挂 `CreationFlags |= CREATE_NO_WINDOW`（`proc_windows.go` / `proc_other.go` 桩，`hideStdioWindow`），**不挂 `HideWindow`**（不误伤 server 自己拉起的 GUI 子窗）。
- **GUI：D114 后每起一个后台作业弹一个控制台窗 → 已修（D118，2026-10-07）**。`job_start`/`term_exec` 经 `jobproc.setupProc`，Windows 分支原只设 `cmd /c` 的原始 `CmdLine`；现并入 `CreationFlags |= CREATE_NO_WINDOW`（同上单旗，`job` 跑的是用户命令，GUI 子进程照常可见）；输出全程写日志文件/缓冲，无控制台依赖。
- **安全待议（2026-10-13 外部审查，用户拍板维持/排队，D22 复核结论 + D138 排期）**：
  ① **读路径恒 Allow**（`perm.File` 对 `OpRead` 直接 Allow）——复核结论**维持**：D22 的显式设计
  （读无副作用）、提示注入风险的前提是模型已走到 file 工具且用户自己配的工具/模型；真要收紧是
  **设计变更**（读越出 sandbox 走 Ask 会让「全文可用 file_read」失效），需单开 D 决策并评估
  弹窗体验代价。
  ② **插件「丢文件即静默 exec」→ 已加信任门（2026-10-13，D138）**：`Host.grant` 此前对
  capabilities 为空的 plugin.json 直通 ⇒ 静默 exec。现由 `Host.trustGate` 补门：**插件目录发现**
  的条目首次连接前必须确认（提示含启动命令原文），指纹落 `State.Trusted`（config
  `plugins.<name>.trusted`，**随 profile 走**）；命令/args/url 一改就重新问。config 里写的
  server = 用户显式信任不过门（否则 MCP 日常天天弹窗）。
  ③ **MCP 默认 risk 按来源分流**：config 里写的 server 保持 `safe`（用户显式信任），**插件目录丢
  进来的 plugin.json 默认 `confirm`**（来路不明）。
  ④ **MCP prompt 文本升格为用户身份**（`RenderPrompt` 结果直接走 `UserInput`）：最小处置 = 打来源
  标记 + 系统提示注明「以下来自外部 MCP server，非用户亲述」。
  ⑤ **stdlib 8 条漏洞 → 已清零（2026-10-13，D137 收尾）**：工具链升 go1.27.2（go.mod 的 `go`
  指针随之到 1.27.2），`govulncheck ./...` 现在报 **0 个可达漏洞**。WSL 侧的坑同批修掉
  （linuxgate 硬编码 `GOTOOLCHAIN=local`，apt 的 go-1.26 不够 → 把模块缓存里的 linux 工具链
  解到 `~/sdk` 并前置 PATH）。残留 2 条均不可达，其中 GO-2026-5932（x/crypto）上游无修复版。

- **后续可选（修法②）**：弃子进程方案，复用主窗托盘基建（`Shell_NotifyIconW`）在自家进程发气泡，顺带升级过时的 balloon-tip 形态（Win10+ toast）——零子进程、无 powershell 冷启，但要消息泵 + 平台分支 + toast 侧 WinRT COM，留待 GUI 批次一并做。

- **GUI：T 之后的相邻字母间距被吃掉一个字距 → 已定位、先记录（D156，2026-10-18，用户报障）**。
  根因在上游 Gio：矢量字形路径把 X 偏移画反（`shape()` 取 `dot - offset`，同文件位图路径取
  `dot + offset`），而雅黑的老式 `kern` 回退会把一份字距放到后字形的 X 偏移上 ⇒ 被字距配对的
  后一字母之后那个间隙少掉整整一个 kern（实测：`Tool` 的两个 o 之间无间隙、`Terminal` 的 e↔r
  仅 2px；无偏移的 `oo` 对照 15px）。上游 v0.10.3 与 main 均未修，tracker 也无此票。
  **本地绕不掉**（摘我们加载那份面的 `kern`/`kerx` 表无效——整形走系统扫描那份面；叠加
  `text.NoSystemFonts()` 才有效但会丢粗体）。处置：用户拍板「小问题，先记录，未来想办法修」；
  修法排序与实测数据见 `docs/decisions.md` D156 与 `DESIGN.md` §15.4。

### S6 · 工具与 MCP 管理（M9，P1，M，**已落地 2026-10-17，D143**）

- 设置窗「工具与 MCP」页：内置工具启停（`tools.disabled`，**热生效**；`think` 草稿工具默认隐藏、
  `context_compact` 默认可见，禁用后果页面写明）+ 插件列表（状态/来源 config|插件目录/传输/能力授权）
  + 动作（启用/停用、逐能力撤销 / 手动预授权、撤销信任（仅插件目录来源）、打开日志）。
- 插件数据面：`host` 增 `GrantCap`/`RevokeGrant`（撤销 = 同时停用 + 清 granted，再启用重走确认）/
  `RevokeTrust`（清指纹 + 断开）；`/plugin logs [name]`（tail `audit.log`，无参给摘要）。
- 跨窗刷新：`host.OnChange` + 内置变更 → 装配根原子快照（含 `Revision`）；动作一律异步
  （`Host.Enable` 拨号 15s、确认弹窗在主窗——同步调必冻次窗）。
- 测试：假 MCP server（发现/调用/超时/崩溃重启/授权拒绝）已有测试基建 + 假 `ToolsAdmin` 的 UI
  数据面契约 + `toolrun` 禁用集 + host 新管理方法契约。

### S7 · 系统提示词管理：人格模块树 + 设置窗独立页（M9，P1，L；**已落地 2026-10-18，D144/D145**）

- **形态（D144）**：config `prompts` 段 = **模块树**（`modules: name → {text?, children?[]}`，组模块可多级
  嵌套）+ **有序顶层引用** `persona`；**内置只读模块** `@default`（默认人格，`system_prompt` 即其正文）/
  `@env`（环境信息组：`@env.platform`/`terminal`/`sandbox`/`timeout`/`date`，日期 = **会话创建当天**）。
  装配 = 深度优先递归展开（空组不出、缺名跳过、环引用截断），落点 `app/prompt` 的 `PersonaComposer`。
  保持 D20 首节点快照（改动只影响新建会话）。
- **拆两笔（S7a/S7b）**：
  - **S7a 内核与配置（已落地 2026-10-18）**：`app.PersonaComposer`（模块树递归展开 + 内置模块 + 时钟注入）
    + config `prompts` 段 + 装配根接线（Agent 兜底请求时展开 / Session 创建时展开）。
  - **S7b 设置窗人格模块树编辑器（已落地 2026-10-18）**：模块行（名 / 正文 / 子模块逗号分隔 / 删除）
    + 人格引用有序列表（上移 / 下移 / 移除，从模块库或内置清单添加）+ 内置只读模块可「引用」
    + 组合预览全文 + 「只对新会话生效，重启后应用」。**D145 起独立成「系统提示词」页**（页集
    五页 → 六页，「对话」页只留限额），保存仍是一次全量 config patch。
- **测试**：S7a = composer 纯函数（叶/组/多级嵌套/空组不出/缺名跳过/环引用截断/默认引用/回落内置默认/
  日期按注入时钟）；persona 快照口径回归；`cmd` 侧 config 段解析/往返 + E2E（模块树 → persona 首节点 →
  请求体）。S7b = 模块树编辑 / 预览 / patch 写回 + 页归属断言（提示词行只挂「系统提示词」页）+ 离屏渲染。

### S7c · 次窗换 Paper 主题（P1，M，**已落地 2026-10-18，D148/D149**）

> **范围（用户拍板）**：**主窗不动**，只换三个次窗（会话历史 + 关系图、欢迎三步、查看原文）
> 与设置窗。先出离屏样式图选方向（Paper / Dense / Glass 三选一），再落生产代码。

- **改动面**：新增 `theme_paper.go`（令牌 + 字级阶梯 + 按钮/复选框）+ `theme_paper_draw.go`
  （面板/阴影/卡片/下沉面）+ `settings_grid.go`（模块库三列网格与拖拽）+ `settings_dialog.go`
  （右键编辑弹窗）；改造 `settings_rows.go`（分组卡、导航选中态、底栏）、`history.go`、
  `welcome.go`、`rawview.go`、`overlay.go`、`relmap_frame.go`、`winmgr.go`、`theme.go`。
- **视觉口径**：双强调色分工（`brand` 只做标识、`accent` 承载白字）、四档圆角阶梯、
  按用途定的字级阶梯、分组卡（组界圆角 + 卡内分隔线）、语义色与第三档文字按 AA 定值。
  详见 `DESIGN.md` §15.4.1。
- **顺带修掉的三个真实缺陷**（都是取像才暴露、数据层测试全绿也发现不了）：
  ① 模块库网格被二次偏移推出视野（画面只剩标题）；② 一次空 `pointer.Cancel` 会抹掉
  别的磁贴的拖动状态（残影消失、松手不落位）；③ 查看原文的内容块只包住文字宽度。
- **测试**：`theme_paper_test.go`（AA 对比度 × 三种面、accent/brand 分工、圆角阶梯）；
  `settings_grid_test.go`（网格落在行坐标系内、Cancel 作用域）。离屏取像覆盖设置页六页
  浅/深两档、右键弹窗、网格与拖动残影、历史窗、欢迎三步、查看原文。
- **出口**：三门禁（`gofmt`/`vet`/`test`/`-race`）全绿；取像与选定方向一致；主窗零改动。

### S9 · 会话与注入内核改版（P1，L，**已落地 2026-10-20，D150–D167**）

> **来源**：用户 2026-10-18 一次性提出的 6 项功能 + 2 项缺陷（每条都在 `docs/decisions.md`
> 有决策记录）。**分阶段落码**，每步一笔 commit、三门禁全绿。

| 步 | 内容 | 决策 | 状态 |
|---|---|---|---|
| S9-0 | 设计落档（领域角色 / 装配投影 / 密钥方案 / 欢迎窗重排 / `InsertBefore`） | D150–D154 | 已完成 |
| S9-1 | 历史窗拖动少节点时动画停住 | — | 已完成 |
| S9-2 | 欢迎窗按模拟稿重排（编号圆徽 / 分组卡 / 底栏） | D153 | 已完成 |
| S9-3 | 设置窗与欢迎窗「获取模型」+ 候选 chip | D153 | 已完成 |
| S9-4 | 人格节点双模式编辑（本会话 Carry + 模板页签复用设置页编辑区 + 「应用到本会话」） | D154 | 已完成 |
| S9-4b | 会话行悬浮删除（复用 `/rmconv`）+ 二次确认跨窗同步弹窗（主窗确认态与历史窗弹窗同显） | D157 | 已完成 |
| S9-5 | `InsertBefore` + `/summary` + 右键「插入摘要」 | D152 | 已完成 |
| S9-5b | 历史窗去噪：标签不带角色前缀 / 图例去无标签空心圆 / 图侧菜单文案改判 | D158 | 已完成 |
| S9-5c | 历史窗：删「展开全部」+ 左栏可收起（展开 200dp / 窄轨 30dp） | D159 | 已完成 |
| S9-5d | 左栏标题行「＋」新建对话（投 `/new`） | D160 | 已完成 |
| S9-6 | 本机加密密钥库 + `secret:` 双查 | D151/D161 | 已完成（S9-6a 库+双查+写入；S9-6b 管理页） |
| S9-6a | 密钥库本体（DPAPI / AES-GCM）+ `secret:` 双查 + 界面明文入库改引用 | D151/D161 | 已完成 |
| S9-6a2 | 设置窗页面注册表骨架（六页原样搬入各自文件 + 注册表分派；纯重构，行为零变化） | D162 | 已完成 |
| S9-6b | 设置窗「密钥库」页（列出/新增覆盖/删除任意名字；按上一行的新契约原生写） | D161/D162 | 已完成 |
| S9-6c | 密钥库「导入旧密钥」：config 明文入库并改引用 + 只存在于环境变量的引用值抄入库 | D164 | 已完成 |
| S9-6d | 关系图节点右键「生成响应」（`/respond <id>`）+ 会话根删右键菜单、左键复制会话 ID + 可复用轻提示组件 | D165 | 已完成 |
| S9-7 | 注入节点（`RoleInject` + 投影 + `/inject` + 三角图例） | D150/D166 | 已完成 |
| S9-8 | 轮询器（`JobManager` watcher → 终态注入 + 空闲唤醒） | D150/D167 | 已完成 |

- **步序理由**：先把「看得到的错」修掉（S9-1/S9-2），再补几个不依赖内核的 UI 能力
  （S9-3/S9-4/S9-4b），然后动领域（S9-5），最后才上两个新内核机制（S9-6/S9-7/S9-8）——
  领域与装配的改动面最大，放最后一段，前面每步都能独立验证、独立回滚。
  **S9-6a2 插在 S9-6b 之前**（D162）：设置窗页数会持续增长，「密钥库」页按现机制要动 7 个
  共享大文件，先立页面注册表骨架（纯重构、行为零变化），新页就只花一个新文件 + 一行注册；
  骨架与密钥库页**各自独立过门禁**，两者独立可回滚。
- **出口（每一步）**：`gofmt -l .` 空、`go vet ./...`、`go test ./...`、`go test -race ./...`；
  UI 步另出离屏像素证据；内核步带领域性质测试/装配回放测试。**推送前须用户明确确认**。

### S10 · 模型面与文件工具整治（P1，L，已完成；D169–D172）

> **来源**：用户 2026-10-20 提出「允许 `file_read` 读图片（记录模型是否支持多模态，不支持则
> 明确报因）」，并批准把此前盘出的**必修项 + 四类问题**（描述与行为不符 / 文案漂移 /
> 信任边界 / 门禁缺口）合批处理。用户 2026-10-20 追加确认：文件工具按「分页 + 图片 + `file_edit`」
> 全做。**每步一笔 commit、三门禁全绿**；纯文案步不改变行为，靠门禁而非人眼把关。

| 步 | 内容 | 决策 | 状态 |
|---|---|---|---|
| S10-0 | 设计落档（§4.2/§4.3/§7.1/§9 + `model.providers[].vision`） | D169–D172 | 已完成 |
| S10-1 | 必修项收口：`file_read` 判定顺序 bug、内部符号与包名前缀、`Tool failed:` 叠层、`@env.timeout` 与 `job_start` 语义冲突 | D171 | 已完成 |
| S10-2 | 描述与行为对齐：确认措辞条件化、`sleep`/`context_compact` 措辞、上限数字由常量生成 | D171 | 已完成 |
| S10-3 | 文案一致性：压缩提示词去掉本产品不存在的概念、记忆块标题不点名工具、空态收敛到三种、不可信措辞单源化 | D171 | 已完成 |
| S10-4 | 门禁扩张：结果文本 ASCII 门禁（真跑工具）、常量与描述对账 | D171 | 已完成 |
| S10-5 | `file_read` 分页：`offset`/`limit`、行号前缀、续读标注、`file_list` 分页 | D169 | 已完成 |
| S10-6 | `file_read` 读图片：MIME 嗅探、附件库落盘、`tool.Result.Images`、装配投影为 user 消息、GC 扫工具结果 | D169 | 已完成 |
| S10-7 | 多模态能力位：`model.providers[].vision` → `ModelInfo`、不支持时明确报因、user 附件同步降级 | D169 | 已完成 |
| S10-8 | `file_edit`：精确子串替换、唯一性校验、三级匹配、保留换行与 BOM | D172 | 已完成 |
| S10-9 | 信任级显式化：`Injection.Trust`、围栏正文中和、常驻声明句、摘要不洗指令 | D170 | 已完成 |

- **步序理由**：先修**已经错的**（S10-1 的顺序 bug 会往上下文塞 4MB 乱码），再对齐描述
  （S10-2/S10-3 纯文案、零行为变化，但决定模型怎么用工具），然后立门禁（S10-4）——
  门禁必须在文件工具大改之前到位，否则 S10-5..S10-8 的新文案没人守。文件工具按「读 → 看 →
  能力位 → 写」递进：分页改动最小、收益最大；图片要动领域与装配；`file_edit` 独立新增、可单独回滚。
  信任级（S10-9）动 port 契约与投影，放最后——它改的是装配的核心不变量，前面每步都得先稳。
- **与既有批次的关系**：C 类（信任边界）此前建议「先改 DESIGN 再落码」，本批已把口径写进
  §7.1/§9 与 D170，S10-9 直接照文档落。E 类两项（`@env.date` 快照过期、空态收敛）分别
  并入 S10-3 与后续单议——`@env.date` 涉及「人格不可变 vs 日期新鲜」的取舍，需要单独拍板。
- **出口（每一步）**：同 S9（`gofmt` / `vet` / `test` / `test -race` / `linuxgate`）；
  领域改动另跑性质测试，装配改动另跑回放测试，图片与 GC 改动另跑临时目录链路验收。

### S11 · 模型与提供商重构（P1，XL，已完成；D173–D177）

> **来源**：用户 2026-10-20 提出「模型与提供商需要重构」，五项：设置界面一页塞不下、模型能力位
> （思考/图片/工具）要记进元信息、provider 下的模型与 API key 管理（429 限额 / 多账号各自充值）、
> OpenAI Responses 与 Anthropic 协议支持、自定义模型降级链。用户当日追加：**能力探测**——用三条
> 最小请求试出能力，由用户手动发起。
> 已确认的四个岔路：配置**破坏性升级 + 显式报错**（不写迁移器）、能力位缺省**按协议给**、
> 设置窗**新增「模型服务」页 + 折叠卡片**、密钥池**轮换 + 冷却**。

| 步 | 内容 | 决策 | 状态 |
|---|---|---|---|
| S11-0 | 设计落档（§4.2/§5.1/§8/§10/§15.7 + 四条决策记录） | D173–D176 | 已完成 |
| S11-1 | 配置 v2：`kind` / `keys[]` / `models[{name,caps}]` / `fallback[{provider,model}]`；旧形状显式报错与迁移指引；能力位解析（缺省按协议） | D173 | 已完成 |
| S11-2 | 能力位接线：整链取与、装配按有效能力裁剪（工具清单 / 思考参数 / 图片占位）、启动警告、`/model` 标注 | D173 | 已完成 |
| S11-2b | 模型自带上下文窗口：`models[].context_window`，装配预算取小（当前模型窗口 vs 全局上限）、`/model` 显示来源 | D177 | 已完成 |
| S11-3 | 能力探测：三条最小请求 + 「只写回判定明确的」+ `/model probe`（设置窗按钮随 S11-7 的模型行落地） | D174 | 已完成 |
| S11-4 | 密钥池：逐钥匙客户端 + `ErrQuota` + 轮换与冷却装饰器 | D173 | 已完成 |
| S11-5 | `openai-responses` 适配器（`input` items、`function_call_output`、SSE 事件族）；思考块签名贯通（`Part.Signature` → `PromptMessage.ReasoningSignature`，两协议共用） | D175 | 已完成 |
| S11-6 | `anthropic` 适配器（`system` 顶层、`tool_use`/`tool_result` 块；thinking 签名复用 S11-5 的贯通机制） | D175 | 已完成 |
| S11-7 | 设置窗：「模型服务」页（折叠卡片 / 密钥池 / 模型列表 + 每行「探测能力」）+「模型」页收薄（只留路由）。拆三笔：① 拆页 + 降级链列表 ② 折叠卡片 ③ 密钥池多把编辑 + 模型列表 | D176/D174 | 已完成 |
| S11-7b | 模型启用位 + 服务端清单自动登记 + 探测以编辑区为准（模型列表分页、行内「启用」、切页自动拉取） | D180/D181 | 已完成 |
| S11-8 | 模板、`docs/configuration.md`、`docs/usage.md`、迁移说明收尾 | — | 已完成 |
| S11-9 | 主窗转写体验：思考块定稿默认折叠（「▸ 已思考 N 秒」，点开才铺正文） | D182 | 已完成 |
| S11-9b | 主窗转写体验：气泡右键菜单加「生成响应」（有节点 ID 才给） | D183 | 已完成 |

- **步序理由**：schema 先行且独占一步——它决定后面所有步骤的形状，中途变一次就要全部返工。
  S11-2 接能力位（行为变化最大，但只依赖 S11-1 的解析结果，界面还是旧的也能跑）；S11-2b 把窗口
  并进同一个预算口径（三处消费者必须同源，晚做就会先出现「显示还有空间却发不出去」）；S11-3 探测
  排在接线之后，因为它要写回的正是 S11-2 消费的那套字段；S11-4 密钥池独立（只动装饰器与装配根，
  与协议无关）；S11-5/S11-6 各自独立、可单独回滚；**界面放最后**（S11-7）——前面几步期间设置窗
  暂时仍编辑旧字段（能编译、能跑），一次换掉比拆两半少返工。
- **与既有批次的关系**：D169 的 `model.providers[].vision` 由 S11-1 取代；§14 backlog 的
  「厂商私有思考块回传」由 S11-6 关闭；D110 修订③的「降级目标模型 = `models[0]`」由 S11-1
  的逐项 `{provider, model}` 取代（省略 `model` 时的缺省 = 该 provider 首个**启用**的模型，D180）。
- **出口（每一步）**：同 S9（`gofmt` / `vet` / `test` / `test -race` / `linuxgate`）；
  配置与装配步另跑旧形状报错测试与降级链回放，协议步用录制流回放，界面步出离屏像素证据。

### S8 · ASR/TTS 接入（P2，L，**已落地 2026-10-21，D179**）

1. ~~spike：适配器选型~~ → 定为**厂商适配器**：协议位 `chat-audio`（`/chat/completions` 携带音频），
   首发 MiMo（`mimo-v2.5-asr` + `mimo-v2.5-tts` 三种模型），纯 Go（`waveIn` / `PlaySound` 走 syscall，无 cgo）；
2. `port.Transcriber`/`Synthesizer` 实现装配：`asrchat` / `mimotts`，配置引用 provider，不进降级链；
3. 麦克风采集 `Kind=mic` 摄取（`micwin`）+ GUI 语音键（**空输入时占用发送键位**）+ 转写结果**回填输入框**；
4. `/mic` 命令（TUI 侧同源）、`output.tts` 播报输出器启用（整段合成后异步播放，停止键同时静音）；
5. 设置窗「语音」页：provider/模型/音色/音色描述/风格指令/风格标签 + 试听（草稿值即时生效，
   服务与模型随保存重启生效）。

### S12 · 屏幕捕获（P1，M，**已落地 2026-10-20，D178**）

1. `port.Capture` + `screencap`（Windows：`PrintWindow(PW_RENDERFULLCONTENT)`，黑图/失败回退区域 `BitBlt`；
   全屏 = 虚拟桌面；非 Windows 报不支持）；
2. 内置工具 `window_list`（Safe，顶层窗口 z 序、过滤本进程）与 `screen_capture`（Confirm，`target` = 全屏或句柄）；
3. 截图 → `AttachmentStore` → `tool.Result.Images`（复用 D169 通道），长边超 1600 按比例缩小；
4. 无 `vision` 能力时照常注册、执行报因；`docs/usage.md` 工具表与手工验收（遮挡窗口）。

### S13 · 对话交互补强（P1，L，**口径已定 2026-10-21，D184–D188；①②③ 已落地，④⑤ 待落码**）

用户一轮提的五件，按「先内核后入口」排：

1. **提问工具 `ask_user`**（D184，**已落地 2026-10-21**）：`port.Asker` + `port.Interactive`（不吃默认工具超时）+
   `toolbuiltin/askuser`（1–4 题、每题 0–9 项、答案对齐、放弃/无人值守文案）→ `uitui` / `repl`
   降级为编号选单 → `uigui` 输入栏展开态提问面板（多题页签、进度段、最小化、自由输入、键位）。
   `Esc` = 中断本轮，「忽略」= 放弃全部。**其余四项在它之后。**
2. **无人值守**（D185，**已落地 2026-10-21**）：`decorate.Unattended` + `UnattendedAsker`（只包
   `toolrun` 与 `ask_user` 那条链，用户键入的命令确认与插件授权照常问）；`permissions.unattended` +
   `/unattended [on|off]` + 球/托盘菜单勾选 + 设置窗「常规」页开关 + 状态行常驻标记；`-yes` 优先
   （不装装饰器、同开时启动告警并压掉活开关）。
3. **多附件**（D186，**已落地 2026-10-21**）：`port.RawInput.Attachments` 列表（`File`/`Blob` 保留为
   单附件简写，`AttachmentList()` 归一化）+ `port.MaxAttachments = 8`；`ingestfile` 逐项摄取、
   **允许部分成功**（失败项进 `Note`，全失败才报错）；GUI 暂存列表（展开态 chip 排超宽换行、逐项可删；
   收起态单项 chip / 汇总 `N 个文件`）+ 文件框 `OFN_ALLOWMULTISELECT`（单选/多选两种缓冲形态经
   `parseMultiSelect` 解析，非 Windows 的 zenity/kdialog/osascript 同开多选）。
4. **图片附件入口**（D187）：`Ctrl+V` 读剪贴板位图（`PNG` 优先、退 `CF_DIB`/`CF_DIBV5` 自转 PNG）+
   `WM_DROPFILES` 拖拽 + 暂存缩略图（像素预算门控）+ 单图 20MB；转写气泡内嵌缩略图另起一笔。
5. **编辑消息增删附件**（D188）：`/edit --attach/--drop/--drop-all`（文本可空、只对用户消息）+
   `port.TreeView.NodeAttachments` + GUI 编辑态附件 chip（可删可增，提交时翻成 Args tokens）。

验收：每项独立 commit 独立过门禁（`gofmt` / `go vet` / `go test` / `-race` / linuxgate /
domain 性质测试 / `docsync -check`）；面板与入口的 GUI 侧要有手工验收步骤写进 `docs/guide.md`。

---

## 3. 依赖关系与建议排期（**执行序已按 D109 重排**）

```
【已完成】S1 修复包 → S1b 响应式 → S1c 跨块拖选 → S2 气泡右键 → S2b 输入行 → S4+S5 配置底座（D110）
                          （S1–S2b 与前置 A/B 数据面均已落地；S4①② 与 S5 已落地，S4③ 欢迎窗已落地 D141）
【已完成】S4a 大文件拆分 → **S4c 设置窗渲染异常修复（D113）**——S4c 排在 S4b 之前落地（用户指定，
         不动文件组织，仅轴向不变量 + 离屏渲染回归防线；验收须逐像素映射，见 §2 验收口径）
【已完成】**S4b GUI 跨平台抽象层（D111）** → 平台面无 gio 依赖、非 Windows 降级实现（非桩），
         非 Windows 门禁 = WSL 原生 linux 工具链（D119）

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
  │ ③ GUI 跨平台抽象层（结构改动，D111）【已落地】                │
  │    S4b 平台接口 + Windows 迁入 + 非 Windows 降级实现（非桩）  │
  └───────────────────────────┬──────────────────────────────────┘
                              ↓
  ┌──────────────────────────────────────────────────────────────┐
  │ ④ 对话树 UI：relation-map（D112）                             │
  │    S3 内核补洞(/rm 任意会话·改名) + TreeView 整树快照 + 关系图 │
  │    （已落地：53874c3 / cf6ee83 / 2d8befb / d350466 /           │
  │     a565ee2 / ff50adb，2026-10-08）                            │
  └───────────────────────────┬──────────────────────────────────┘
                              ↓
  S6 工具/MCP 管理（已落地）· S7 人格模块树（S7a 内核已落地，S7b 待做）· S8 语音（可并行 spike）【顺延】
```
- **S3 形态规格已定案（D120，2026-10-08）**：[docs/relation-map-design.md](relation-map-design.md) 为落码验收依据——L1 语义（结构根/锚点拆分、类别=Role、断言=父→子）/ L2 通道分配（层号深度色带、形状=Role、直径=权重、描边=主干、版本链走线型）/ L3 行为（斥力+角度约束+层序投影、交互动词表、两态生命周期）+ 公理集 A1a/A1b/A2–A11 + 派生符号 + 自检清单。**改判 D112 两处口径**：版本链「异色」→ 只用线型（边色归 source 层号）；UI 由「单图」→「左栏会话选择 + 右图」。**§10.3 落码全步已落地（2026-10-08）**。
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
