# 文档地图与分工
<!-- docs-sync: 2026-11-05 @ 0233b77 -->

> 一页说清每篇文档**写给谁、管什么、谁是权威**。改文档之前先在这里找该篇的位置——
> 位置不清的文档会同时变成手册、历史档案和待办清单，最后没人读完。
>
> 落位与纪律（一个事实一处权威、先文档后代码、改完跑 `docsync`）在
> [AGENTS.md 的「文档纪律」](../AGENTS.md#文档纪律)，这里只给地图、不重复规则。文风见下文
> 「[文风](#文风)」。

## 用户面

读者 = 使用 Aquarius 的人（不要求懂编程）。这组文档**不得出现** `D<n>` / `§<n>` / `M0–M4`
这类内部编号——要解释原因就写行为与后果。

| 文档 | 管什么 | 权威性 |
|---|---|---|
| [guide.md](guide.md) | 用户手册：怎么装、怎么开口、怎么改、怎么找回来、出问题怎么办 | 白话版，只讲做法与现象；确切字段与逐条语义看参考类文档 |
| [usage.md](usage.md) | 参考手册：命令与参数、内置工具、常见问题 | 命令面速查 |
| [configuration.md](configuration.md) | 配置参考：`config.json` 逐字段、覆盖顺序、密钥规则 | **配置字段与权限矩阵的唯一详表**；设计理由不在这里 |
| [storage.md](storage.md) | 数据布局与备份：目录树、会话文件、`.bak` 恢复 | **磁盘形状的唯一说明**；与代码冲突时改本文 |
| [releases/](releases/) | 每版发布说明 | 只描述那一版的事实，不随后续版本修订 |

根目录 [README.md](../README.md) 同属用户面：功能清单 + 构建 + 文档索引，不承载设计与决策。

## 贡献者面

读者 = 改代码的人与 AI 编码代理。可以用内部编号，但**编号只能做索引或溯源、不能代替说明**——
去掉编号后这一段仍得是一句能读懂的话（句首当索引 `// D204：采样参数热生效，…`、句末当溯源
`// 权限判定：风险档与路径档各管一列（D22）` 都可以；`// D22 两列分工` 不行）。这条由
`cmd/aquarius/comments_test.go` 守着：一段连续注释去掉编号后不足 12 个可见字符即判失败。

| 文档 | 管什么 | 权威性 |
|---|---|---|
| [contracts.md](contracts.md) | **内核当前为真**的规格：领域模型、端口、插件宿主、应用层、权限与安全、取消与并发 | **内核事实的最终裁定**；与 decisions 同级 |
| [decisions.md](decisions.md) | 每条决策一行结论；够格展开的写动机与用户拍掉的方案 | 与 contracts 同级；**编号的唯一索引**在这里 |
| [roadmap.md](roadmap.md) | 已经完成了什么、对应哪条决策 | 进度档案，只记过去；计划不在本文 |
| [plan.md](plan.md) | 还没做的事、顺序与节奏 | 未来工作的唯一处；**工作稿，随时重排，别的文档不按节号引用 plan.md** |
| [overview.md](overview.md) | 定位、统一语言、限界上下文、依赖方向、目录与端口矩阵 | **定位与术语的唯一处**；一页速览 |
| [development.md](development.md) | 门禁、测试手段、工程准则与边界、How-to | 开发流程的唯一处 |
| [relation-map-design.md](relation-map-design.md) | 会话树关系图的形态规格（通道分配、行为契约） | uigui 实现规格；可被测试断言的部分以本文为准 |
| [gui-design.md](gui-design.md) | GUI 壳内实现规格：悬浮球形态、输入栏与转写、主题令牌、多窗、缩放、气泡菜单 | uigui 实现规格；`§15.x` 引用指本文 |
| [AGENTS.md](../AGENTS.md) | 硬性规则、文档纪律、常用命令、提交要求、本机环境该记什么 | **规则的唯一处**（development.md 是汇编，冲突以本文为准） |
| [THIRD-PARTY-NOTICES.md](../THIRD-PARTY-NOTICES.md) | 第三方组件的版本与许可证 | 再分发合规依据 |

本机环境（编译器绝对路径、WSL 发行版与已装的包、镜像与 DNS）记在仓库根目录的
`AGENTS.local.md`，该文件被 `.gitignore` 排除、不进版本库。仓库只写「需要配什么、为什么」；
具体配成了什么样属于那台机器——否则换个人 clone 下来读到的全是别人的路径。AGENTS.md 的
「本机环境」一节列了那份文件该记哪几项。

## 分工的边界

- **契约 vs 实现规格**：内核规格（领域模型、端口、应用层、权限与安全、并发）在
  [contracts.md](contracts.md)；单个适配器的形态规格（GUI 窗口与输入栏、关系图通道分配）各自成篇，
  与 contracts 同级但只管那一个适配器。
- **契约 vs 决策**：contracts 回答「现在什么是真的」，decisions 回答「为什么这么定、否决了什么」。
  contracts 不写演化史；decisions 不重复规格结论。
- **档案 vs 规划**：做过的进 roadmap，没做的进 plan，两者不混写。
- **手册 vs 参考**：guide 讲怎么做（场景、白话），usage / configuration 讲是什么（逐条、逐字段）。

## 节号对照

历史注释与文档里的裸 `§n` 按这张表解析（拆分前这些编号都指单一的总设计文档，2026-10-29 拆成下面各篇）。
代码注释沿用旧号，不改写；读者按表定位。

| 节号 | 现在在哪 |
|---|---|
| `§1`–`§3` | [overview.md](overview.md)（定位与设计原则、统一语言、限界上下文） |
| `§4`–`§7`、`§9`、`§10` | [contracts.md](contracts.md)（领域模型、端口、插件架构、应用层、权限与安全、取消与并发） |
| `§8` | [storage.md](storage.md)（磁盘形状）与 [configuration.md](configuration.md)（配置语义） |
| `§11` | [development.md](development.md)（测试策略与门禁） |
| `§12` | [roadmap.md](roadmap.md)（里程碑与验收标准） |
| `§14` | [plan.md](plan.md)（暂缓事项） |
| `§15` | [gui-design.md](gui-design.md)（GUI 形态规格） |

`D<n>` 编号不在此表内，唯一索引见 [decisions.md](decisions.md)。

## 文风

中文按 STC（简明技术性中文）写：词表、句长与四个场景的细则由 STC 规则给出
（见 [AGENTS.md](../AGENTS.md) 末尾的 STC 段）。本仓只登记项目例外与术语，见
[stc/terms.aquarius.yaml](stc/terms.aquarius.yaml)；逐篇的审阅结论与保留理由见
[stc/review.md](stc/review.md)。两条口径不随 STC 变：用户面文档不出现决策编号与节号；
文档分工与「一个事实一处权威」按本文的地图。

## 存档

`docs/archive/` 放**没人读但有用**的稿子：被后续决策取代、仍能解释「当时为什么这么选」的设计稿。
存档保持原样（含已经过时的结论），只在文件头标一行「已被谁取代」，**不随后续代码更新**；
外部文档也不再按名字引用存档内容。

存档页照旧带文档头标记：标记记的是「当时那份稿子是哪版代码的」，不表示内容被维护。

| 存档 | 原用途 | 被谁取代 |
|---|---|---|
| [archive/relation-map-physics-design.md](archive/relation-map-physics-design.md) | 关系图「全程物理动画」的力积分设计稿（边弹簧 + 阻尼 + 树锚的推导与实测） | 生产物理改采源算法（D128）；形态与现状见 [relation-map-design.md](relation-map-design.md) §5.4 |
| [archive/decisions-full.md](archive/decisions-full.md) | 压缩前的决策记录全文：实现细节、当时的推演与自问自答式的否决项 | 正文压缩后 [decisions.md](decisions.md) 只留一行结论与够格展开的决策；编号与结论仍以 decisions.md 为准 |

## 维护

- 新增文档前先问：读者是谁、新文档要管的事实有没有别处已经在管？有就先改那一处。
- 事实变了：改权威处，再按文件名搜一遍别处是否残留旧口径。
- 删一篇之前确认没有文档或代码注释按名字引用这篇文档；存档不删。
- **本机环境不进文档**：编译器路径、WSL 发行版、镜像与 DNS 这类随机器变的东西写
  `AGENTS.local.md`（不入库），仓库文档只写「需要配什么、为什么」。
- 改了 `docs/**/*.md` 或 `README.md` 之后跑 `go run ./cmd/docsync`（见 [development.md](development.md)）。
