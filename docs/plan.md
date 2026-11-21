# Aquarius 未来规划（docs/plan.md）
<!-- docs-sync: 2026-11-21 @ f8e3872 -->

> 定位：**只写还没做、将要做的事**——范围、顺序、节奏、验收。**做过的、怎么演进的，一律不写**
> （那是 [roadmap.md](roadmap.md) 的档案与 [decisions.md](decisions.md) 的决策记录）。逐条决策看
> [decisions.md](decisions.md)；内核规格看 [contracts.md](contracts.md)，冲突以后两者为准。
>
> 规则只有一条——**没写进这里的未来工作，不进代码；做完的，从这里删掉。**
>
> **这份文件会被反复重排，所以别的文档不要引用本文的节号**（节号一挪，引用就错位）。要指代
> 这里的事，就写条目的名字；正文的章节顺序只服务本文自己的可读性，随时可改。
> 长期有效的工程准则与边界不属于待办，所以不在本文：见 [development.md](development.md)
> 的「工程准则与边界」。

- 规模记号：**S** = 半天~1 天，**M** = 2~4 天，**L** = 1 周+（含测试与文档）。
- 版本记号：一个版本 = 一组完整的、可验证的收口；不把半截工作留成版本边界。
- 批次记号：`P<n>-<m>` 是执行批次里的条目号，跨文档引用写批次号（如 `P2-4`），不要用节号。

---

## 版本路线

> 已完成与已发布版本的记录在 [roadmap.md](roadmap.md)，这里只排还没做完的事。

成本规则：本节两张表按**一次会话内能做完**的实做节奏估——**S** ≈ 20 分钟、**M** ≈ 1 小时、
**L** ≈ 几小时。与本文开头的规模记号（人日规则）不是同一套，不要换算。
每条动手前先补 [contracts.md](contracts.md) 的对应节与决策编号（新增命令、新增工具都算），
本节只定范围与验收。

### `v0.9.0`（在排一条：DeepSeek 导入；回收站、`/fork`、标签置顶、人读导出、服务端文件解析、DeepSeek 语音后端、时间感知、前缀稳定性对比、PTC-1 与 PTC-2 已完成，见 [roadmap.md](roadmap.md)）

1. **DeepSeek 会话导入**（L，D321 已拍板）：按「会话导入：DeepSeek 导出」一节实做——路径级
   识别 + 流式解包、清单与 `--all`、100 条分块、sidecar 去重、附件默认带（`--no-files` 可关）；
   GUI 勾选面板的搜索框另批。验收：样例包全链路、全量包重导 0 新建、Ctrl-C 不留半个会话。

---

## 测试并行的剩余部分（backlog，未排期）

> v0.5.0 已把全量 `-race` 从 341.7 秒压到约 103 秒；逐包数字、串行判据与压测结论见
> [roadmap.md](roadmap.md) 的 `v0.5.0-E` 行。

**还能压什么**：其余小包，以及 `cmd/aquarius` 里剩下 98 个保持串行的顶层测试。

**先决条件**（都是产品代码改造，本版不做）：把语言槽从进程级收进实例、把 Windows 的
`consoleDetached` 改成原子量或移出 `run()`、把三个注入钩子改成实例字段。

**判据**：全量 `-race` 现停在约 103 秒，瓶颈是 `uigui` 的 62–64 秒与 `cmd/aquarius` 的
58–59 秒互抢 CPU；再降要动这两包的负载本身，不是再加 `t.Parallel()`。重启这一条前先量
一次当下墙钟，别沿用 11-05 的数字。

---

## 生态兼容与轻度角色扮演（backlog，未排期）

> 定位：个人 AI 助手 = 日常工作 + 轻度编码 + 轻度 RP + 兼容现有 AI 生态。
> 内核（会话树 / 压缩 / 记忆 / 工具 / MCP / 技能）已强于对照面，差距全在边缘。
> 动手前先按「先文档后代码」写成 [contracts.md](contracts.md) 对应节 + 决策编号。
>
> 下表的编号是原本的优先序，已完成项不再重复（角色卡批次见 [roadmap.md](roadmap.md) 的
> `v0.3.0-D`，世界书按关键词激活见 D294），跳过即可。

### 未排期

4. **多套用户人设**（M）：命名人设表 + 会话绑定，保持「首节点快照」语义。
5. **`cache_control` 断点**（M）：只在对应适配器做，不动配置面。
6. **会话全文搜索**（M）：扩会话列表端口的数据面，不改存储格式。
7. **MCP server 管理界面**（M）：设置窗「工具与 MCP」页可添加服务器（stdio 命令 / HTTP 地址 /
   环境变量 / 信任），现在只能手写配置。
8. **角色头像与会话背景**（M）：观感提升最大、成本最低；不做整套主题包（角色卡侧的头像
   读入与展示已随 `v0.3.0-D` 完成，这里只剩**会话背景**与主题化）。

---

## 会话导入：DeepSeek 导出（backlog，未排期）

> 定位：把 DeepSeek 网页端的导出存档搬进本仓。`/import` 现有的四档互通格式都面向「一次一条
> 会话」，DeepSeek 给的是**整库容器**，也是本仓第一个真实世界量级的导入：2274 个会话、56 MB
> 正文、377 MB 包体。**只读一档、不做导出**——官方只提供「导出账号下全部会话」，网页端没有
> 导入入口，写出一份没人能读的格式是空转。
>
> 本节按实做节奏估成本（S ≈ 20 分钟 / M ≈ 1 小时 / L ≈ 几小时），与文首的人日记号不是同一套。
> 动手前先补 [contracts.md](contracts.md) §5.16 的 `deepseek` 一节与决策编号（D311 起）。

### 格式事实

2026-11-20 实测一份常用账号的完整导出（压缩 377.7 MB / 2857 条目 / 解压 468.6 MB）：

| 项 | 值 |
|---|---|
| 会话 | 2274 个，与 `chat_session_index.json` 完全对应（`message_count` 全对，无空会话） |
| 消息 | 39026 条（user 16421 / assistant 22605），单会话最大 3418 条 |
| 正文 | 56.4 MB，单会话 p50 5 KB、p95 50 KB、最大 3.6 MB |
| 附件 | 包内 580 个实体、327.5 MB（jpg 323 / png 183 / webp 73 / gif 1），单文件最大 9.8 MB |
| 结构 | 无悬空父、无重复 ID、Head 全部存在、`pinned` 全 false、空标题 1 个 |
| 解析耗时 | 全部 2274 个 JSON 解析 1.1 秒（成本在附件拷贝与落盘，不在解析） |

```
deepseek_data-YYYY-MM-DD.zip
├─ chat_sessions/<uuid>.json          会话树（扁平消息表 + parent_id）
├─ chat_sessions/<uuid>_files/<物品 id>_<原名>
├─ chat_session_index.json            清单：id / title / pinned / message_count / 时间
├─ manifest.json                      {"export_format_version":3}
└─ user.json                          账号与 OAuth 资料 —— 不读、不落库、不进日志
```

会话 JSON：`id` / `title` / `pinned` / `current_message_id`（= Head）/ 两个时间 / `messages[]`；
消息：`id`（整数）/ `parent_id`（可空）/ `role`（`USER`|`ASSISTANT`）/ `status` /
`accumulated_token_usage` / `inserted_at` / `fragments[]`。

三条按代码写死的规则：

- **顺序只认 `parent_id` 与 `fragments` 数组序**。assistant 的 `inserted_at` 比与之对应的
  user 消息早几毫秒（生成前先落占位），按时间排会把整棵树排反。
- **多根 = 首条消息的版本组**：666 个会话有多条 `parent_id: null`，成因是首条消息被反复编辑
  （示例里 6 条根就是同一句话改了 6 遍）。全部挂到 Root 之下做兄弟，`materialize` 现有规则
  天然接得住（同一父的多个孩子即版本组），不另造分支。
- **索引与文件按一一对应处理**（官方导出如此），清单直接读 `chat_session_index.json`；个别
  条目缺文件时在回执里报，不当整份失败。

### 映射

| DeepSeek | 本仓 |
|---|---|
| `REQUEST` / `RESPONSE` | `text` 分片（user / assistant） |
| `THINK`（含 `elapsed_secs`） | `thinking` 分片；`elapsed_secs` 丢弃 |
| `TEMPLATE_RESPONSE` | `text` 分片（服务端固定话术，如实当正文） |
| `FILE` 且 `is_image` | `image` 分片 + `BlobRef`：字节按 `export_path` 从包里取，Hash 去重后入附件库 |
| `FILE` 且非图片（`export_path: null`） | 记 `Dropped`：这类有 498 个（txt / md / java / pdf / docx），源包不含字节 |
| `CONTENT_RISK` | 丢弃（「This response is AI-generated」界面横幅） |
| `TOOL_SEARCH` / `TOOL_OPEN` / `TOOL_FIND` / `PARTIAL_FILE_READ` / `READ_LINK` | 丢弃并按会话计数，回执写「搜索 N 次、读页 M 个」 |
| `status` | `FINISHED`→`done`；`INCOMPLETE`→`cancelled`；`TIMEOUT` / `CONTENT_FILTER`→`error` |

`accumulated_token_usage` 丢弃：语义是累计值，与本仓 `Usage` 的「本条」不同口径。
空节点（实测 10 条无任何分片）跳过，子节点顺父指针接到最近收下的祖先（沿用 `chatgpt` 的现有规则）。

**搜索轨迹的原文不保留**：`READ_LINK` 的 `results[].signature` 与 D310 的服务端块形状相似，
但那是网页 agent 在**那个会话里**的加密态，换个会话回传只会被拒；`TOOL_OPEN` 多数只有
`status` 与 `stage_id`，`url` / `title` 常为 null。留着没有可用性，只会让续写多出无法验证的块。

### 人格

导入的树**不建 persona 首节点**，`Character` 与 `Tail` 留空，与 `chatgpt` / `openwebui` /
`openai` 三档导入后的形态一致。装配走 D144 兜底：`path[1]` 不是 system 时用当前配置人格
（`app/agent.go` 的 `buildRequestTracked`）。好处是四种导入格式只有一种形态，不给 `deepseek`
单独造节点。

**还原不了的事实要写进回执与文档**：DeepSeek 不导出 system prompt，这些历史是在 DeepSeek 自己的默认
人格下生成的，续写时的风格可能跳一下；原始系统提示不可得，任何节点都补不出来。回执固定带
一行「历史由 DeepSeek 默认人格生成，续写按当前配置人格」。

### 四条工程改造

1. **路径级识别 + 流式解包**（四条里工作量最大的一条）：`Detect` 只看 64 KB 文件头，而 zip 的条目名在
   中央目录（文件末尾）。加一个可选接口（如 `DetectPath(path, head)`）：用 `ReaderAt` 读中央
   目录，见 `manifest.json` 里的 `export_format_version` 与 `chat_sessions/` 就判 deepseek；
   `Import` 对 `io.ReaderAt` 断言成功即走 `zip.NewReader` 流式解，失败回落「读进内存」的老路。
   `*os.File` 天然满足，端口签名不动。
2. **分块导入**：`pick` 本就是 1 起序号清单，`app` 按 100 条一块调 `Import` → 逐条 `Save`，
   块内失败只回滚本块。2274 条不再一次进内存，顺带得到进度行与可中断。
3. **清单与选择**：`/import <zip>` 不带 `--pick` 只列清单（序号 / 时间 / 标题 / 消息数），
   显式 `--all` 才全导——「塞 2274 条进左栏」不该是一条命令的默认结果。清单按 `updated_at`
   倒序显示，`--pick` 序号仍按清单序取。GUI 复用 aqx 的勾选面板，加搜索框。
4. **重复导不炸**：官方只能全量导出，重导是常态。profile 下 `import-sources/deepseek.json` 记
   `源会话 id → {conversation_id, updated_at}`；同 id 且 `updated_at` 未变跳过，变了导成**新
   会话**（本仓没有增量合并），回执写「跳过 N、另存 M」。这份文件不入库，删掉只是下次重导。

上限按实测重设：条目 10000、单会话 JSON 64 MB、**包体不设限**（流式读）、单附件 32 MB、
附件总量 512 MB。现有 `maxArchiveBytes = 256 MB` 会直接卡住 377 MB 的包——`Index` 那一步就报
「归档过大」。

### 已定的四条

1. **附件默认带**：会话完整性优先，`--no-files` 供省空间与提速。
2. **搜索轨迹全丢 + 计数**（理由见上）。
3. **`status` 映射进 `Outcome`**：给 `importedNode` 加一个 `Outcome` 字段，`materialize` 透传；
   解析阶段翻译源里的终态，不留给展示层猜。
4. **去重 sidecar 与导入同批做**：这份 sidecar 是「官方只能全量导出」的直接后果，晚做等于先教用户
   重导一遍 2274 条重复。

### 验收

- 全量导入 2274 个会话：无报错、无半截状态、`/list` 可滚；耗时以附件拷贝为主；
- 首条消息被编辑过的 666 个会话，导入后表现为版本组，左右切换能看全；42 个兄弟版本的那条
  会话，关系图与切版本正常；
- 580 个图片入库后能显示；498 个文档在回执里如实记「源包不含文件」；
- 同一份 zip 导第二次：0 新建、2274 跳过；改过一个会话后再导：1 新建、2273 跳过；
- 中断（Ctrl-C）后不留半个会话；
- 导入的会话续写正常，`/context` 里 system 段是当前配置人格，回执带「原始系统提示不可得」一行；
- `user.json` 的邮箱 / 手机 / 微信资料不出现在任何落盘文件与日志里。

### 边界

不做导出；不做增量合并（改了对话就另存一份新会话）；不保留搜索轨迹原文；不补 persona 节点；
`pinned` 无位可放，等「会话标签与置顶」那条做完再说。

---

## 语音前处理与说话人（backlog，未排期）

> VAD 已随 `v0.8.0` 交付（D295）：采集口改成推帧、`vadsilero` 逐帧判定并切段，
> 唤醒词与声纹的连续输入前置就此具备。本节只剩这两项，**各自还压着一条路径待拍板**。

### 唤醒词：待定，暂不排版本

**现状是两条路都不通**，原因不在代码而在分发许可与运行时：

1. **openWakeWord** 代码 Apache-2.0，但**预训练模型是 CC BY-NC-SA 4.0**（非商业 + 传染），
   不能随二进制分发，也不能靠用户「下载即用」绕过这条限制。
2. **microWakeWord**（OHF-Voice）代码与 `esphome/micro-wake-word-models` 都是 Apache-2.0，
   模型层面干净，但 microWakeWord 要 TFLite Micro 的 INT8 运行时；Go 侧只找到 cgo 的 `mattn/go-tflite`，
   没有纯 Go 的 INT8 TFLite 解释器。为唤醒词开 cgo 会破坏「无 cgo、单二进制」的交付规则。

**两条未解路径**（要先拍板走哪条，拍完再进本文排期）：

- **自训 Apache-2.0 模型**：用开放语料训 microWakeWord 形状的 INT8 模型，许可证可自行选定；
  代价是要补一个纯 Go 的 INT8 推理内核（只覆盖该模型用到的算子），属于「为一项功能写一个引擎」。
- **外置 openWakeWord 服务**：不进程内跑，唤醒词交给用户自己起的服务，内核只做 HTTP 客户端。
  与下文声纹同一形状，代价是「单二进制开箱可用」降级为「要另起一个服务」。

不进入任何版本的边界，也不列验收——**先定路径**。

### 声纹识别：外置服务 + 协议位，M

**模型选型**：3D-Speaker 的 CAM++（`speech_campplus_sv_zh_en_16k-common_advanced`，代码
Apache-2.0），80 维 fbank 输入、192 维 embedding，ONNX 体积 fp32 27MB / fp16 14MB / int8 8.2MB，
中文场景可用。WeSpeaker 代码同为 Apache-2.0，但 resnet34-voxceleb 等**权重**是 CC-BY-4.0，
若走 WeSpeaker 必须逐模型核对许可证。

**为什么不进程内跑**：三个纯 Go ONNX 引擎都不够格当生产依赖——`Kazuhito00/onnx-purego-interpreter`
自述比 ONNX Runtime 慢 5–30 倍且「不作为 ORT 替代」，`AdvancedClimateSystems/gonnx` 只到 opset 13
且 2024 年后未发版，`oramasearch/onnx-go` 走 Gorgonia、自述实验性；`shota3506/onnxruntime-purego`
虽免 cgo 但通过 purego 调原生共享库，仍需分发 `.so`/`.dll`。所以**结论是外置服务 + HTTP 适配器**，
不在进程里塞引擎。

**形状**：照既有语音端口的做法办——调用方（`app`）定义一个小端口（如「音频 → 说话人标签」），
配置面按**协议位**新增一个 kind（如 `audio.speaker.kind`，`campplus` / `http` 两档），适配器把 WAV
发给服务、换回 embedding 或「是否本人」的判定。配置取值仍沿用 `base_url` / `api_key` 显式字段优先、
缺的才借 `model.providers` 的既有顺序（D222）；分派仍是白名单 + 越界即报错（D223）。
**先补 [contracts.md](contracts.md) §5.6 与决策记录再动码。**

**用途**（决定验收怎么写，需产品上先定）：说话人分离（多段转写标注是谁）与「只认我的声音」的
入口门。**验收**取决于选哪个：分离版看同一段两人对话能否标对；门禁版看非本人声音是否被拒且
误拒率可接受。

### 与本仓工程准则的关系

- 不得引入工作区/文件索引之类的抽象，也不得让唤醒词或声纹进模型上下文——判定结果只作为
  「这段音频是谁 / 从哪开始」的元信息，附在附件或转写上。
- 唤醒词若最终选外置服务，`README` 的用户面措辞要写「需要额外跑一个服务」，不写内部编号。

---

## 程序化工具调用（PTC-3，backlog，未排期）

> PTC-1 与 PTC-2 已随 `v0.9.0` 交付（`tools.mode: native | ptc`、`run_code`、脚本内确认去重
> 与限额可配）；决策记录在 [decisions.md](decisions.md) 的 D319 / D320，规格在
> [contracts.md](contracts.md) §4.3，调研结论、引擎实测与首跑通过率在 [roadmap.md](roadmap.md)。

- **PTC-3**（S，带前置判据）：Anthropic 原生 `allowed_callers`——报文里真的出现 `caller`
  字段才做，否则登记不做。

---

## DeepSeek 全家桶：未排期的四项（backlog）

> 2026-11-18 按官方「构建 Chatbot」文档逐项对着本仓看过一遍。已排进 `v0.9.0` 的是端点能力声
> 明、用量分项、会话标题、阅读链接与文件输入（前四项已交付）；本节只留没排的，以及为什么
> 没排。

**服务端联网搜索与深度思考搜索**（L，未排期）
声明 `deepseek_web_search` 后，搜索与开页在模型推理内部完成，只有正文进上下文，回答带
`citations` 可展示来源；开思考时一轮最多约 20 次工具调用，这是本地工具循环给不了的体验。

不排的理由是服务端搜索跟树模型正面冲突三条：① 搜索要求把 `server_tool_use`、结果块与
`signature` 逐轮**原样回传**，而树可修订、可克隆、可压缩——压缩一次 `signature` 就没了（官
方说这不会让请求失败，只让读过的网页从上下文消失）；② 用过服务端搜索的回复**不能续写**
（400，得重发上次请求），而本仓有续写与修订；③ 声明搜索之后 `tool_choice` 不能强制，也不
能与其他服务端工具共存。

重启条件：先量一次「不接搜索多花了多少 token」，有数再评估（端点能力声明与用量分项都已就位，
发不发、省多少这两层不再挡路）。接之前还要处理两条语义差：开了这些能力，`temperature` / `top_p` /
`stop` 被服务端忽略（逐模型采样覆盖会静默失效，要能提示）；`max_tokens` 限的是**每一步推理**
而不是整轮，与 `TokenBudget.MaxOutputTokens` 的整轮语义对不上。

**内容安全 `safety_check`**（M，条件性）
`{"level": "high"}` 审输入与输出，输出被拒以 `stop_reason: refusal` 结束，输入命中直接返回
400。自用助手不需要；**对外提供服务时才是合规必需**，那时再排。接的话要处理三条：拒答后已经
流式下发的文本撤不回来（界面要能清掉已显示的回答，换成自己的拒答文案）；审核消耗计进输出，
并在 `usage` 里单列 `safety_tokens`；输入被拒仍按缓存命中价计费。

**TTS 的 WebSocket 流式合成**（M，未排期）
整段合成随 `v0.9.0` 的语音后端一起做；流式是第二步——`wss://api.deepseek.com/v1/tools/tts`
边发文字边收音频帧，于是长回答不必等全文合成完：第一句的音频就能先响。这一项与「暂缓事
项」里「语音后续」的 TTS 流式是同一条，动手时以本节为准。
前置是 `Synthesizer` 端口：现在返回一个 `BlobRef`，流式下要能交出音频流。`outputtts` 的播放
流程也要一起换——现在是落临时文件后异步播（D179），流式下要边收边放。

**三协议兼容回归**（S，未排期）
DeepSeek 现在同时提供 Messages（`/anthropic/v1/messages`）、Responses 与 Chat Completions
三套。可以把 DeepSeek 当第二家真后端跑 anthropic 协议的集成用例——不用 Anthropic 的 key
也能验证「Anthropic 适配器对非 Anthropic 厂商够不够兼容」。这是测试资产，不是功能，所以不
占版本位。

---

## 仍需人工拍板与已知未修项

### 待拍板（技术顺序已由本文定，只剩产品取舍）

- **唤醒词走哪条路**：两条都通不了现在这条道——开放模型的预训练权重不许随二进制分发，
  许可证干净的模型又缺纯 Go 的 INT8 运行时。要么自训模型并配套写一个极小的推理内核，
  要么做成外置服务（「开箱即用」降级为「另起一个服务」）。→ **唤醒词动手前拍板**，
  两条路径见上文「语音前处理与说话人」。
- **声纹识别的用途**：说话人分离（多段转写标注谁说的）与入口门（只认我的声音）是两种验收，
  先定要哪个再写代码。
- **破坏性窗口是否仍然开放**：应用已公开发布，用户可能已有 `~/.aquarius/` 数据。
  本文所有任务按「不破坏既有数据」设计；后续若必须改存储格式，先补迁移器并在本文标注。

### 已知未修项（留档，等条件成熟再排）

- **字体字距缺陷**（D156）：`T` 之后相邻字母被吃掉一个字距，根因在上游 Gio 的矢量字形路径计算，
  v0.10.3 与 main 均未修；本地绕不过（摘 `kern` 表无效、关系统字体才有效但会丢粗体）。
  等上游修，或评估自绘度量方案。
- **MCP 默认风险按来源分流**：配置里写的 server 保持 `safe`（用户显式信任），从插件目录发现的
  `plugin.json` 默认 `confirm`（来路不明）。与信任门同批评估。
- **MCP 提示文本冒充用户身份**：`RenderPrompt` 的结果直接走 `UserInput`。最小处置 = 打来源标记 +
  系统提示注明「以下来自外部服务，非用户亲述」。
- **欢迎窗第 1 页 200% 字号下语言选择要滚一下**（2026-11-04 离屏走查，用户拍板保持现状）：大字号档
  已压过 logo 与间距、页面可滚，但首屏只放得下 logo + 名字 + 标语；要「第一眼就能切语言」得再省掉
  logo 与标语，那是品牌取舍，不随 0.5.0 改。
- **通知改走自家进程气泡**：弃 `powershell` 子进程方案，复用托盘基建在本进程发气泡，
  顺带升级为系统 toast。零子进程、无冷启，但要消息泵 + WinRT 组件，留在界面批次一并做。

---

## 暂缓事项（backlog，未排期）

> 登记但没排期的事：记下来是为了不再重复讨论，不是承诺。排期时从本节挑走，做完即删。

- MCP sampling（server 借用宿主模型）
- **厂商私有思考块回传**：GPT-OSS/vLLM 系回传键名 `reasoning`（D42 现发 `reasoning_content`，
  vLLM 兼容两者）——接入此类提供商时由适配器做键名映射
- 非 GBK 的遗留代码页（CP437/latin-1 等）终端输出识别（D41 内容探测只有 UTF-8/GBK 两档，会误判成乱码中文）
- `file_read` 等文件文本入口的遗留编码解码（与 D41 同算法，终端之外的文本入口）
- MCP 插件主动健康检查与按需懒加载（当前：启动即连接 + 被动 `Wait` 感知，[contracts.md](contracts.md) §6.4 #4）
- **MCP 富内容回传**：工具结果里的 `ImageContent` 落附件库并回填 `tool.Result.Images`、
  `EmbeddedResource` 的文本展开、`ResourceLink` 可读一次——当前一律降级为 `[image content omitted]` 之类的占位
  （规则落定后再编号）
- **语音后续**：本地 ASR 引擎（whisper.cpp 类，需 cgo 或外挂二进制，与单二进制规则冲突；
  TTS 流式合成见上文「DeepSeek 全家桶」一节）；**导出文件输出器**（把每条回答镜像到文件，[contracts.md](contracts.md) §7.2 的
  `OutputAdapter` 形态）——与 `/export` 的按次整树导出是两件事，后者见 [contracts.md](contracts.md) §5.16
- **备份的后续**：增量与差量备份、归档加密（密钥库那条是另一件事）、把备份做成定时任务
- **Tier-1 Go 插件（D29 由 M4 移入）**：`pluginapi/v1` 独立 go.mod 契约 + `adapter/plugingo`
  编译期装载器（范围随后续里程碑定稿；M4 只做 Tier-2 MCP）
- **GUI 后续（M5/D43 之外，[gui-design.md](gui-design.md) 预留）**：悬浮球本体转常规窗口形态（D43 只预留设计
  不实现；功能窗用独立常规窗已入正册 D60）、多颜色主题预设（M5 只做品牌色
  `#00AEEF` + 深/浅两版）；转写气泡内嵌图片缩略图仍留在此处
- **语音输入与播报（D27 移入）**：TUI 侧拖拽/粘贴/语音按钮（M4 TUI 为 MVP 不含，D33）、
  导出文件输出器、本地 ASR 引擎与其他协议。
- **M2 审查遗留（P3，2026-09 评审；P2 的 trim 工具 schema 估算已修）**：
  - regexp2 `MatchTimeout` 与计数路径的 ctx 检查（模型可控输入的回溯爆炸防护）
  - Agent/Runner 共享可变状态的并发模型显式化（多路任务并存时共享实例的契约：`Confirmer`
    无任务标识、注入队列满时的重试、watcher 表；LLM 适配器 `NoteUnsupported` → config 写回
    那条 D34 竞争已由包级 `cfgWriteMu` 收口）
  - `think` 参数非空校验；压缩摘要流的 UI 标注（"正在生成摘要"以区别于回答流）；
    `/usage` "上轮实测"文案改"最近实测"
- 跨分支"摘抄"共享子树（DAG 化）
- Job 表持久化（SQLite）
- PDF/Office 等 Doc 提取器插件
- 图片 OCR/描述自动降级、音频直输模型（等模型能力普及）
- 长期记忆巩固（记忆 = 文档，无独立记忆系统；**上下文压缩摘要已入正册** [contracts.md](contracts.md) §4.1/§7.1，与记忆无关）
- Web UI、移动端输入方式（GUI 桌面前端已入正册 [gui-design.md](gui-design.md) / D43/M5）
- 向量检索记忆后端（作为 MemoryStore 插件）
- **生态兼容与轻度角色扮演**（2026-10-22 对照调查记录）：按「个人 AI 助手 = 日常工作 + 轻度编码 +
  轻度 RP + 兼容现有生态」的定位，与成熟角色扮演前端（角色卡 / 世界书 / 预设）的差距**全在边缘**，
  内核（会话树 / 压缩 / 记忆 / 工具 / MCP / 技能）已强于对照面。**只登记、未排期**，条目、做法要点与
  优先级见本文的「生态兼容与轻度角色扮演」清单（角色卡只读导入与会话导出**已完成**
  `v0.3.0-D` / `v0.3.0-A`，见 [contracts.md](contracts.md) §5.17/§5.16；**卡的常驻世界书条目已随 D247 进正册**，
  检索式激活已随 D294 交付）。
- **外部代理转交（ACP）的后续**（2026-10-27 调研记录，主线见 [contracts.md](contracts.md) §5.18/D244）：v1 只做
  全新会话、不写入磁盘、不转发 Aquarius 的 MCP server。登记但**未排期**的两件：① 会话内复用子代理
  （`session/resume` + `<profile>/delegate/` 句柄写入磁盘，端口复用位已留）；② 子代理转录按需写入磁盘
  供事后翻（不进上下文）。设置窗配置与 `fs` / `terminal` 申报已随 v0.6.0 发布
  （见 [roadmap.md](roadmap.md)）；**转接的并行**不单列——这项随后台化一起进主线（D261）。
  **明确不做**：内置代理预设、按 registry 自动安装与升级、ACP v2。
- **决策策略的两个阈值**（`auto` / `review`）：影子模式跑够样本后定，本文不预设。独立评测显示
  二值概率不能按 0.5 直接切（同一份数据上按训练集调阈值后 micro-F1 从 0.50 到 0.75），且零样本
  迁移到「工具调用审批」这类负载未被证明——有实测把正常工具输出判成注入、且错得最自信。
  **重启条件**：真机以 `decide.mode: "shadow"` 跑出一批样本再标定。
- **三条伪版本依赖**（2026-11-14 复核：都换不掉，等上游发 tag）：`go.mod` 里还剩三条直接依赖
  不是 tag——`lipgloss`、`ledongthuc/pdf`、`go-silerovad`。
  - `lipgloss`：上游最新 tag 是 `v1.1.0`，当前的 `v1.1.1-0.20250404…` 由 `glamour v1.0.0` 的
    `go.mod` 强制；换成 tag 会把 glamour 连带降到 `v0.9.1`，等于拿一个 tag 换另一个 tag 的降级。
  - `ledongthuc/pdf`：上游从无 tag。带 tag 的同 API 分支 `dslipak/pdf` 停在 `v0.0.2`（早年快照），
    换过去要回退近年修复；仍在维护的 `pdfcpu` 接口不同，得重写提取器。
  - `go-silerovad`（D295）：上游从无 tag，只能钉提交（`v0.0.0-20261009220311-dc87378735b7`）。
    升级要逐次读 diff——这个库承载语音分段的推理与权重，`Detector`/`Stream`/`Segmenter` 的行为变化
    会直接改分段结果，不能靠「跑一下还能编过」验收。
  - **判据**：见到任一条发 tag（或有安全通告）当天换上，并各跑一次绑定测试：lipgloss → TUI 观感，
    pdf → 文档转文本的 PDF 路径（选型见 [roadmap.md](roadmap.md) 的 Q17），
    silerovad → 分段回归（静音零段、单段、三段与停顿阈值）。
