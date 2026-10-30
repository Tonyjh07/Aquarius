# AGENTS.md — Aquarius 项目协作指南

面向 AI 编码代理的操作说明。**动手前先读 [docs/README.md](docs/README.md)**（文档地图，含节号对照）
与 [docs/contracts.md](docs/contracts.md)（内核契约，权威，中文）；
本文件只放干活需要的规则与命令。规则冲突时：用户显式指令 > 本文件 > 你的习惯做法。
**每篇文档写给谁、管哪部分事实**见 [docs/README.md](docs/README.md)（文档地图），
要改文档先在那里找到它的位置。

## 项目概览

- **Aquarius**：面向个人的极简 AI 助手（对话优先、多模态出入、文档记忆、可插拔工具/模型）。
- **技术栈**：Go ≥ 1.27.3（D30 的 go-sdk 要求 1.25 → D137 为 x/net 漏洞 1.26 → 为清 stdlib 漏洞升到 1.27.2 → 同一理由升 1.27.3），单二进制交付（`go build ./cmd/aquarius`），无 cgo，纯 Go 依赖优先。
- **当前状态**：里程碑现状见 [docs/roadmap.md](docs/roadmap.md)（进度档案）；**接下来做什么见
  [docs/plan.md](docs/plan.md)**（规划的唯一维护处，工作稿：会频繁重排，**别的文档不要引用它的
  节号**）；内核契约见 [docs/contracts.md](docs/contracts.md)，暂缓事项见 plan.md 末节。工程经验与边界见
  [docs/development.md](docs/development.md) 的「工程准则与边界」。
  当前开发分支 `feat/v0.4.0`。
- **语言约定**：文档与注释用中文；标识符、commit message 用英文。**进入模型上下文的字符串**（system 提示、工具声明与参数描述、工具回填结果/错误）一律英文；仅面向用户的界面、命令输出与日志用中文。文档文风（陈述事实、结论在前、不写总结段与客套话）见 [docs/README.md](docs/README.md) 的「文风」。

## 本机环境

与这台机器绑定的东西——工具链绝对路径、WSL 发行版与已装的 apt 包、镜像与 DNS、临时目录——
写在 `AGENTS.local.md`，**不入库**（`.gitignore` 已排除）。它不是仓库规则的一部分：换一台机器
就没有这份文件，下面各节的命令照常跑，只是要先按 [docs/development.md](docs/development.md)
把工具链、WSL、模块代理配好。本文件只管「需要配什么、为什么」，配成了什么样放在本地那份。

`AGENTS.local.md` 建议只记：Go 用户级 env 的 `CC` 指向、WSL 发行版与一次性 apt 清单、
受限网络下可用的 apt 镜像与 DNS、代理工具的临时目录。

## 硬性规则（违反即错误，无例外）

1. **依赖方向**：`domain` 不得 import 任何端口、适配器或三方 SDK；只允许 `adapter → port ← app → domain`。
2. **节点不可变**：`conversation.Message` 创建后只读。用户"修改"永远走 `Revise`（创建同级新节点），
   `Carry` 模式用**边转移**实现（不许拷贝子树）。
3. **流式不进领域**：增量只流经 UI；Turn 结束一次性 Commit 不可变节点。
4. **两个稳定级**：`internal/port` 可自由重构；`pluginapi/v1` 只增不改（独立 go.mod，严格 semver），
   两者类型独立，禁止外部插件编译到内核类型上。
5. **内置不享特权**：内置工具/存储/模态适配器一律经端口契约接入，禁止在 app/domain 里直连具体实现。
6. **插件不得直接操作会话树**：一切经内核中转。
7. **无工作区概念**：不要引入项目根/cwd 工作区/文件索引/文件监听之类的抽象。
8. **密钥安全**：密钥优先经 `port.Secrets` 按名取用；`config model.api_key` 允许明文但启动必警告、空值回落 `secret:AQUARIUS_OPENAI_KEY`（D35）；密钥不写入日志、会话树或附件。
9. **横切能力走装饰器**：重试（含 429 退避）/截断/审计在 main 的装配处叠加端口装饰器，不进插件 API、不散落进业务代码。
10. **决策编号不进用户面**：`D<n>` / `§<n>` / 里程碑（`M0–M4`）/ 内部迭代标记（`S4c`、`S7b`、`Tier-1`）只属于
    `docs/contracts.md`、`docs/overview.md`、`docs/decisions.md`、`docs/roadmap.md`、`docs/plan.md`
    与**代码注释**。**禁止出现在**：
    - 界面文案（标签、提示、占位、按钮、空态、确认句、关于页）；
    - `port.CommandInfo.Desc` / `Usage`（`/help` 与命令浮层共用的用户面文本）；
    - 会进用户视野的日志与报错（CLI 的 `stderr`/`stdout`、`warnf`/`logf` 的面向用户行）；
    - 面向用户的 README / 示例配置说明。
    用户要读到的是**行为与理由**，不是内部编号（要写「服务端不认的参数会被剔除」，不要写
    「（D34）」）。确需溯源时在**注释**里写编号，用户面只留可理解的说明。新增字符串字面量前先自检；
    测试里锁断言用 `_test.go`（测试输出不面向用户）不受此限。
    **同一口径也管注释**：注释里的编号是溯源锚，不是结论——去掉编号后那一段仍得是一句能读懂的话
    （`// 权限判定：风险档与路径档各管一列（D22）` 可以，`// D22 两列分工` 不行）。
    `cmd/aquarius/comments_test.go` 守着这条：连续注释去掉编号后不足 12 个可见字符即失败。

## 常用命令

```bash
go build ./cmd/aquarius        # 构建二进制
go test ./...                  # 全量测试
go test -race ./...            # 竞态检测（合入前必过）
go test -tags aqvalidate ./... # 变更后整树自检（改领域变更路径时必跑，D215）
go test ./internal/domain/... -run TestProperty -count=50   # domain 性质测试加密跑
go vet ./...                   # 静态检查
gofmt -l .                     # 格式检查（应无输出）
go run ./cmd/linuxgate         # 非 Windows 门禁：WSL 内 linux 目标的 build + vet（D119）
go run ./cmd/docsync           # 刷新改动过的文档头「更新日期 + 代码基线」标记
go run golang.org/x/vuln/cmd/govulncheck@latest ./...   # 依赖漏洞体检（D137；需网络）
```

- **依赖漏洞体检**（D137）：`govulncheck` 报的是**可达**漏洞（按调用栈判定，不是简单版本比对），
  所以「升了 x/net 却仍报 stdlib」这类结论要以它的输出为准，别凭 CVE 列表猜。用法：改动
  `go.mod`（加/升依赖）后跑一次；`exit status 3` = 有发现，非工具故障。网络不可用时跳过并
  在 PR 说明里注明（不要因为连不上就算门禁失败）。

- **非 Windows 门禁**（`go run ./cmd/linuxgate`）：Gio 在 Linux 上必须 cgo，Windows 上不存在
  「一条 `GOOS=linux go build`」的口径——该工具经 WSL 用原生 Linux 工具链跑 `go build ./...`
  + `go vet ./...`（vet 覆盖 `_test.go`）。前置与网络注意事项见
  [docs/development.md](docs/development.md) 的"非 Windows 门禁"。

- **`-race` 在 Windows 需要 C 编译器**（`-race` 走 cgo）。装一个 MinGW 系的编译器（WinLibs 的
  gcc 或 LLVM-MinGW 的 clang 都行），再把 Go **用户级** env 的 `CC` 指过去
  （`go env -w CC="…\bin\<编译器>.exe"`，写入 `go env GOENV` 那个文件、**不进仓库**），
  之后 `go test -race ./...` 直接可用，不必每次设环境变量或改 `PATH`。
  只把编译器追加到 `PATH` 末尾不够：`PATH` 上常见的第一个 `gcc` 是 Cygwin 的，Go 明确拒绝它
  （`don't use the cygwin compiler to build native Windows programs; use MinGW instead`），
  必须用 `CC` 显式指定。自检 `go env CC`，排障细节见 [docs/development.md](docs/development.md)
  的「环境与门禁」；这台机器上配成了什么写在 `AGENTS.local.md`。

## 代码风格

- `gofmt` 即风格；不引入额外格式化/lint 工具，除非用户要求。
- **行尾一律 LF**，三处同口径：`.gitattributes`（`* text=auto eol=lf`）+ 仓库级 `core.autocrlf=input`、`core.eol=lf`
  （`.git/config`，不改全局）。核对用 `git ls-files --eol`：文本应全为 `i/lf w/lf`，二进制 `-text`。
  若 `gofmt -l .` 突然报出一片文件 = 工作副本被写成了 CRLF（checkout/rebase 老坑），
  `git checkout -- <file>` 让 git 按属性重写，**不要手改文件内容**；本仓无 `.bat/.cmd/.ps1/.sh`，无 CRLF 例外。
- **接口由消费方定义、保持小**（1–3 个方法）；`context.Context` 作为外部调用的第一个参数。
- 错误用 `fmt.Errorf("...: %w", err)` 包装保留链路；不 panic 做控制流。
- 值对象用具名类型（如 `conversation.MessageID`），不裸用 `string`。
- 导出符号写注释；命名跟 [docs/overview.md](docs/overview.md) 的统一语言对齐（如 Revise/Carry/Fresh/Head/Part/Job）。
- 每个端口的实现在 `internal/adapter/<name>/`，一个适配器一个目录；测试替身放测试文件内或 `internal/adapter/fake/`。

## 编辑 UTF-8 文件的纪律（严禁 PowerShell 替换）

本仓文本（代码注释、文档）以 **UTF-8 中文**为主。改这类文件时按下面来，违反的代价是**把
仓库内容写坏**：

1. **优先用专用编辑工具**（edit/write），不落 shell。
2. 必须走 shell 时，**用 Python**（`python` / `python3` / `py`，PATH 上哪个在就用哪个），
   读写都显式 `encoding="utf-8"`。**严禁用 PowerShell 的 `-replace` / here-string /
   重定向** 去改 UTF-8 文件：PowerShell 5.1 把无 BOM 的脚本按 ANSI 读，脚本里的中文字面量
   先烂一遍，轻则解析失败，重则**把乱码写回文件、静默损坏仓库**（2026-10-09 实测踩中：
   临时补丁脚本全 hang 在编码上，改也不是不改也不是）。
3. **一次性脚本不进仓库**：写到系统临时目录（本机常用哪个见 `AGENTS.local.md`），
   用完删；本仓只留 `.go` 与文档。
4. **写完自查**：`git diff --stat` 看改动面，`git diff` 抽查中文是否完好（注意终端显示本身
   也可能是乱码，以 `git diff` 内容为准）。发现乱码 `git checkout -- <file>` 回滚重做，
   **不要试图去"修那个乱码"**（越修越坏）。
5. 文件写入口径与既有文件一致：UTF-8 **无 BOM**、LF 行尾。

## 测试要求

- **改动 domain**：必须带/改性质测试。核心不变量两条（树合法、节点不可变）必须持续可证；
  改到变更路径（`AppendCommitted`/`Revise`/`Prune`/`InsertCommitted`）时，另跑
  `go test -tags aqvalidate ./...`（该构建下每次变更后自检整树，D215）。
- **改动 app**：用脚本流 LLM + 收集器 Presenter + 脚本队列 Prompter 做交互回放（golden 测试），
  禁止测试里起真实网络。
- **改动 adapter**：跑该适配器的契约测试（临时目录/假 server）；LLM 适配器用录制流回放。
- **链路级验收**（如"摄取 → 附件入库 → 装配内联"）：可在测试中借用真实适配器（临时目录）串联，
  但**生产代码**的跨层直连仍被禁止（依赖铁律只约束非测试代码）。
- **改动 mcpgate/插件宿主**：用测试内假 MCP server（stdio）覆盖：发现、调用、超时、崩溃重启、授权拒绝。
- 新行为补测试，修 bug 先写复现测试。全部测试 + `go vet` + `gofmt` 通过才算完成。

## 提交与 PR

- Commit message（英文，祈使句）：`feat: ...` / `fix: ...` / `test: ...` / `docs: ...` / `refactor: ...`
- 一个 commit 只做一件事；PR 说明里写清：改了什么、对应哪一篇的哪一节/哪条决策（如 D22）、如何验证。
- 若设计与文档不一致：**先改文档（对应篇目 + `docs/decisions.md`）再改代码**，并在 `docs/decisions.md` 补决策记录。
- 改动某篇某节时，按 [docs/README.md](docs/README.md) 的节号对照表检查别处引用是否还指得对；
  **已落地**的写进 `docs/roadmap.md`（进度档案），**还没做**的写进 `docs/plan.md`（规划唯一维护处），两者不混写。
- **改了 `docs/**/*.md` 或 `README.md` → 提交前跑 `go run ./cmd/docsync`**：它只刷新工作区里改过的
  文档，把头部标记（更新日期 + 代码基线哈希）刷成本次提交之前的状态。标记是 HTML 注释，渲染后
  不出现，别手写、别删。

### 发版与版本 tag

- **版本信息单独成笔、tag 打在它上面**：发版时把「Windows 资源版本 + 发布说明」收成一笔
  提交（如 `chore(release): v0.1.0`），**`vX.Y.Z` 版本 tag 指的就是这一笔**——不要打在之前
  任意一笔功能提交上，也不要让版本号散落在多笔里。
- 该笔通常包含：`docs/releases/vX.Y.Z.md`（发布说明）、`cmd/aquarius/winres/winres.json` 的
  `fixed` / `info` 两处版本（改了才动）、以及 `go generate ./cmd/aquarius` 重生成的
  `rsrc_windows_<arch>.syso`。三者口径必须一致（`-ldflags -X main.version`、资源版本、
  发布说明里的 `-version` 示例输出）。
- **tag 用 annotated**：`git tag -a vX.Y.Z -m "Aquarius vX.Y.Z"`，与提交说明同口径。
- 推送分支与 tag、创建 GitHub release 由**用户**执行；代理做完提交与本地门禁即可，不主动 push、
  不打 tag（除非用户明确要求）。
- 发版前门禁 + 构建核对见 `docs/development.md`「发版构建」；核对方式：用发版那组
  `-ldflags` 实际构建一次并跑 `-version`，确认输出与发布说明写的一致。

## 目录速查

```
cmd/aquarius/        组装根（wiring：config → 插件/授权 → 端口装配含装饰器 → UI）
cmd/iconify/         开发工具：图标集生成（多尺寸 PNG / ICO / ICNS，纯 Go 无三方依赖）
cmd/linuxgate/       开发工具：非 Windows 门禁（WSL 内跑 linux 目标 build + vet，D119）
cmd/docsync/         开发工具：维护文档头「更新日期 + 代码基线」标记
assets/              源图标 icon.png 与生成物 icon/（README、Windows 资源嵌入共用）
pluginapi/v1/        对外稳定契约（Tier-1 插件唯一依赖；D29 后移出 M4，目录见"待建"）
internal/domain/     conversation（会话树/Part/Revise）、tool（Spec/Call/Result）、perm（权限矩阵）
internal/port/       llm/tool/store/memory/blob/modality/ingest/job/ui/misc
internal/app/        agent（Turn 循环）、prompt（装配/水位）、session（命令/摄取分派）、
                     est（token 计数链）、plugins/model/compact_prompt（M4 命令面与压缩提示词）
internal/plugin/     config/session（声明与宿主契约）、host（发现合并/grant/生命周期，§6.4）
internal/adapter/    llm（含 llm/tokenizer 精确计数）、toolbuiltin（memory_*/file_*/think/sleep/
                     term_exec/job_*）、toolrun、storejson、memoryfs、repl、blobfs、jobproc、
                     ingestfile、ingestclip、notify、atomicfile、decorate（横切装饰器）、
                     mcpgate（MCP client → port 投影，D30）、uitui（bubbletea TUI，D33）、
                     uigui（Gio 悬浮球 GUI，D43/§15）、screencap、asrchat/asrwhisper/asropenai/
                     asrfunasr、mimotts/vitstts/gptsovitstts、zhconv（繁转简，内嵌 OpenCC 词典）、
                     outputtts、micwin、ingestmic、audioplay、secretbin、probe、skillfs、
                     webbrave/webchain/webdeepseek/webfetchlocal
待建：                pluginapi/v1、plugingo（后移出 M4，D29）
```
