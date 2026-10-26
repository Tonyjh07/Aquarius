# 开发指南
<!-- docs-sync: 2026-10-26 @ 20cd9f6 -->

> 本文汇编自 [AGENTS.md](../AGENTS.md)（规则原文以其为准）与 [DESIGN.md](../DESIGN.md)
> §11/§12；**冲突以前两者为准**。面向：后续贡献者与 AI 编码代理。

## 环境与门禁

```bash
go build ./cmd/aquarius        # 构建单二进制
go test ./...                  # 全量测试
go test -race ./...            # 竞态检测（合入前必过）
go test -tags aqvalidate ./... # 变更后整树自检（改领域变更路径时必跑，D215）
go test ./internal/domain/... -run TestProperty -count=50   # 性质测试加密跑
go vet ./...                   # 静态检查
gofmt -l .                     # 格式检查（应无输出）
go run ./cmd/linuxgate         # 非 Windows 门禁（WSL 内 build + vet，见下）
```

- 语言约定：文档/注释中文；标识符、commit message 英文祈使句。
- **Windows 下 `-race` 需要 CGO + C 编译器**（`-race` 走 cgo，说"需要 C 工具链"没错）。
  装一个 MinGW 系的编译器（WinLibs 的 gcc 或 LLVM-MinGW 的 clang 都行），再把 Go **用户级**
  env 的 `CC` 指到它的可执行文件：`go env -w CC="…\bin\<编译器>.exe"`（写在 `go env GOENV`
  指向的文件里，**不入库**）。只把编译器加进 `PATH` 末尾**不够**：`PATH` 上常见的第一个
  `gcc` 是 Cygwin 的，Go 明确拒绝它——`runtime/cgo: #error "don't use the cygwin compiler to
  build native Windows programs; use MinGW instead"`——所以必须用 `CC` 显式指定。
  自检 `go env CC` 指向该编译器后，`go test -race ./...` 直接可用，无需临时环境变量。
  WinLibs 的安装与取路径：`winget install BrechtSanders.WinLibs.MCF.UCRT`、
  `(Get-Command gcc -All | ? { $_.Source -notmatch 'msys64' })[0].Source`。
- 全部测试**默认不起真实网络**：LLM 用脚本流/httptest 假 SSE 服务，存储用临时目录；
  门控的真实 MCP 验收（`AQUARIUS_E2E_REAL_MCP=1`，拉现成 npx server）除外，缺省 skip。

### 非 Windows 门禁（D119）

GUI（`uigui`）依赖 Gio，而 **Gio 在 Linux 上必须 cgo**（X11/Wayland/EGL/Vulkan/GL 全走
cgo；`CGO_ENABLED=0 GOOS=linux` 时 `gioui.org/internal/vk` 报「build constraints exclude
all Go files」，加 `-tags novulkan` 又栽在 `gioui.org/internal/gl` 的 `Functions` 无定义）。
所以**不存在**「Windows 上一条 `GOOS=linux go build ./...` 验完」的口径——Linux 目标需要
Linux 的 C 工具链与开发头。Windows 宿主的口径 = **在 WSL 里用原生 Linux 工具链跑**：

```bash
go run ./cmd/linuxgate          # 缺省：WSL 发行版 Ubuntu，目标 linux/amd64
go run ./cmd/linuxgate -distro <发行版> -proxy <模块代理>
```

工具做两件事（与本地门禁同口径）：`go build ./...` + `go vet ./...`。**vet 会一并类型检查
`_test.go`**，能挡住「测试文件依赖 Windows 专属符号」这类只在非 Windows 才暴露的缺口。
非 Windows 宿主（真 Linux/macOS）直接本地跑同一命令。本机已配成什么样（发行版名、工具链
位置、镜像与 DNS）记在不入库的 `AGENTS.local.md`。

WSL 里的一次性前置（Gio 的 cgo 依赖）：

```bash
sudo apt-get install -y golang-go xorg-dev libx11-xcb-dev libxkbcommon-dev \
    libxkbcommon-x11-dev libwayland-dev libegl1-mesa-dev libvulkan-dev
```

- 发行版自带的 `golang-go` 往往落后于 `go.mod` 要求，而工具刻意固定 `GOTOOLCHAIN=local`
  （不自动下载）。做法：把模块缓存里已下载的 linux 工具链解到用户级目录，再把它的 `bin`
  前置到 `~/.profile` 的 `PATH`（**不要 sudo**，`apt` 装不动工具链版本）。版本号随
  `go.mod` 一起升，`linuxgate` 报「requires go >= x.y.z」即为此。
- 受限网络：`proxy.golang.org` 与 `go.dev` 可能不通，apt 可换 `mirrors.aliyun.com`、
  Go 模块走 `goproxy.cn`（`cmd/linuxgate` 的 `-proxy` 缺省即此）。DNS 解析异常时改
  `/etc/resolv.conf` 后 `wsl --shutdown` 重启。具体镜像与 DNS 地址随网络环境变，
  不必写进本文。
- 工具缺省**共享 Windows 侧的 `GOMODCACHE`**（`-modcache` 可覆盖），避免在 WSL 内重下
  2GB 级依赖；`GOCACHE` 用 WSL 原生目录（构建缓存不跨系统复用）。
- 门禁只覆盖编译 + vet，**不跑 Linux 下的 GUI/交互**（WSL 无 X 显示）；非 Windows 的功能
  对等分级见 [gui-design.md](gui-design.md) §15.6 与 D111，平台层落地后按降级清单走查。
- Windows 行为回归仍以本地门禁 + `AQUARIUS_GUI_SMOKE=1` 冒烟为准，不要用 Linux 门禁替代。

## 代码分层速查

```
cmd/aquarius      组装根（唯一知道"具体实现"的地方）
internal/app      agent(Turn 循环) · prompt(装配/水位/记忆注入) · session(命令) · est(token 计数链)
internal/port     端口 = 接口 + DTO（消费方定义、1–3 方法、ctx 首参）
internal/domain   conversation(树) · tool(值对象) · perm(权限矩阵) —— 零依赖
internal/adapter  llm(含 llm/tokenizer) / repl / storejson / memoryfs / toolbuiltin / toolrun
                  + M3：blobfs / jobproc / ingestfile / ingestclip / notify / atomicfile
                  + M4：decorate(装饰器) / mcpgate(MCP→port) / uitui(TUI)
                  + M5：uigui(Gio 悬浮球 GUI) / uigui/platform(平台能力：纯 Go、不依赖 gio，
                        Windows 实测实现 + 非 Windows 降级实现，D111)
                  （一个适配器一个目录）
```

接口由消费方定义；错误 `fmt.Errorf("...: %w")` 包装；值对象具名类型；
导出符号写注释；命名与 DESIGN §2 统一语言对齐（Revise/Carry/Fresh/Head/Part/Job）。

`uigui/platform` 的分家口径（D111 修订）：平台能力只在 `platform` 包实现（`*_windows.go` /
`*_other.go` 同签名），`uigui` 侧声明所需接口并断言 `*platform.Plat` 满足——**少一个平台方法
就编译不过**。平台向 UI 回传只经 `platform.Host` 的动作枚举（菜单命令 ID / 显隐 / 退出 /
选中文件 / 系统主题变化），业务语义留在 `uigui`。headless 测试装假平台（`fakeplat_test.go`），
不触真 Win32/真外壳。

## 测试手段（四种，按层选）

| 层 | 手段 | 样板位置 |
|---|---|---|
| arch | **架构守卫**：`go/parser` 读全仓生产文件 import，断言分层方向与反向约束；另断言聚合根不导出 map 字段 | `internal/arch/arch_test.go` |
| arch | **注释守卫**：连续 `//` 注释去掉决策编号后不足 12 个可见字符即失败（编号可以当索引或溯源，不能代替说明） | `cmd/aquarius/comments_test.go` |
| domain | **性质测试**：随机操作序列 → 两条不变量恒成立；快照比对证明不可变 | `domain/conversation/property_test.go` |
| domain | **变更后自检**：`go test -tags aqvalidate ./...` 下每次变更末尾跑全树 `Validate`（生产构建零开销） | `domain/conversation/validate_tag_test.go` |
| app | **交互回放**：脚本流 LLM + 收集器 Presenter + 脚本队列 Prompter | `app/fakes_test.go`、`app/agent_test.go` |
| app | **golden 回放**：命令脚本 → 归一化 transcript（ID 归一为标签）与 `testdata/golden/` 基准比对；改语义后 `go test ./internal/app -run TestGoldenReplay -update` 重写基准并人工检视 | `app/golden_test.go` |
| adapter | **契约测试**：storejson/memoryfs 临时目录；LLM 用 httptest 假 SSE 录制流回放；toolrun 全等级矩阵表 + 脚本确认器 | `adapter/storejson/store_test.go`、`adapter/llm/openai_test.go`、`adapter/toolrun/runner_test.go` |
| tokenizer | **官方向量比对**：期望值由 Python `tokenizers` 一次性生成入库（`testdata/gen_*.py`）；fixture 全离线跑，真实 DeepSeek 词表向量需 `AQUARIUS_TOKENIZER_JSON=<路径>`（缺省跳过） | `adapter/llm/tokenizer/tokenizer_test.go` |
| e2e | **进程内装配回放**：`run(args, stdin, stdout, stderr)` + 假服务喂 stdin 断言 stdout（含工具确认 y/n、权限等级矩阵、M3 的 term_exec/job 链路） | `cmd/aquarius/main_test.go` |

修 bug 先写复现测试；新行为补测试。测试替身放测试文件内或 `internal/adapter/fake/`。

**测试必须能在干净环境里跑**：不设任何环境变量、不依赖本机特有的路径/密钥/服务，`go test ./...`
就该全绿。要某个环境变量，用例自己 `t.Setenv` 显式给（含显式置空断言「缺失路径」）。反例：
配置里漏写 `keys` 会让启动回落到 `secret:AQUARIUS_OPENAI_KEY`，在没设该变量的机器上直接
FAIL——这不是被测代码坏了，是测试自己没自足。同理，**测试替身里任何会让 handler panic、
连接中断的写法都要当失败处理**：探测把网络错误判成「无结论」，于是期望「无结论」的用例会
假绿（`probe_test.go` 的假服务曾用 `WriteHeader(0)`，视觉分支从没真正走到）。

## 工程准则与边界

> 这些是**长期有效的规则**，不是待办——所以留在本文，不进 [plan.md](plan.md)（那份文件会被
> 频繁重排，装不下「永远成立」的东西）。来源：v0.1.0 发布前的三方代码 review
> （`temp/review-feedback/1–3.md`）逐条核实后的取舍；被证伪的提议列在最后一节，不重复讨论。

### 沿用的经验

- **守门测试必须自检**。`TestGuardCatchesViolation` 不是摆设：判定函数喂正反例，才不会被
  「规则写错 / 扫描根找错 / 跳过规则吃掉整棵树」这类事故变成永远绿。
- **手抄的期望表是 bug 的温床**。命令清单那一次的根因不是漏了一行，而是测试自带一份硬编码
  清单，把错误固化成了断言。改成从源码反推之后，「只改一半」必然变红。
- **API 只为下一笔的删除而存在，就不值得单独提交**。原计划里「加访问器」与「迁回算法」是两笔，
  但中间那层 API 只服务于下一笔的删除，合并后历史反而更清楚。
- **导出 map = 约束作废**。结构表私有化之后「只有方法能改树」才是编译器保证的事；配套的
  arch 守卫（D216）是防止下次「就先导出一下方便」。
- **领域不该为持久化形状负责**。`storejson` 自带 wire DTO 之后，`.bak` 兼容成了适配器自己的
  验收项，domain 的 JSON tag 一并删掉——留着只会让人以为直接 Marshal 就能存整棵树。
- **同类改动按「测试也要改」估工期**。聚合根私有化那一笔，测试侧约 150 处
  `.Nodes`/`.Children`/`.RevisedFrom` 直读要改走访问器（同包测试只改字段名，跨包测试必须换 API），
  实际改动面远大于生产代码的 23 处。
- **每笔删除都要在提交说明里写出删除量**。追加容易验证（编译过、测试绿），删除很难验证，
  于是历史天然偏向只增不减；净删除不写出来就等于看不见。

### 边界（同类改动沿用）

不引入工作区/cwd/文件索引之类的抽象；`/rm` 的确认提示照旧走同一份子树统计数据，数据面不变；
测试文件不强制私有化（读公共访问器即可）。这些边界在 v0.2.0 的归位批次里同样成立过——
`uigui` 的文件组织、`SessionDeps` 拆分、`relmap` 两档去留都属于规划里写明范围的事，
不借「工程准则」之名顺手做。

### 已否决的提议（核实后的取舍，备查、不排期）

Review 里提出但**经核实不成立**的：

- 「`port` 里放业务纯函数（`ResolveJobID` 等）违规」——DESIGN §5 明文允许无状态纯函数助手；
  真问题是同一套前缀解析策略曾有 4 份实现，两份分别在 `port/job.go`、`port/watch.go`，另一份
  在 `app` 的手写循环里——它们已经合并成 `port.MatchPrefix`（v0.2.0 的 P2-1）。
- 「`mcpgate → plugin` 依赖方向错」——方向是对的（`plugin` 只依赖 `domain`/`port`，`mcpgate`
  是它的协议适配器）；[overview.md](overview.md) 依赖图已补上 `internal/plugin`。
- 「`PromptPart.Kind` 用裸 `string` 违规」——设计原本就这么定（DESIGN §5.1）。要改得先改设计，
  所以它排在 v0.2.0 的收口批次里，不是当下就能顺手换的。

## How-to

### 新增一个端口适配器

1. 先在 DESIGN §5/§5.11 补契约与矩阵行（**先改文档再改代码**，[decisions.md](decisions.md) 补决策如适用）；
2. `internal/port` 定义/复用接口（消费方定义、保持小）；
3. `internal/adapter/<name>/` 实现 + 同目录契约测试（临时目录 / 假 server）；
4. `cmd/aquarius` 装配注入；横切能力（重试（含退避）/硬保底截断/审计）在装配根做装饰器，不进适配器。

### 新增一个内置工具（M2 起）

1. DESIGN §4.3 工具表加行（名称/说明/Risk）；
2. 实现 `port.Tool`（`Spec()` + `Execute(ctx, call)`），放 `internal/adapter/toolbuiltin/`；
   **文件类**再实现 `port.FileTarget` 自申报 `(path, op)`（D25：ToolRunner 据此查
   路径格；不申报则按执行类看工具列 Risk）；
3. 在 `cmd/aquarius` 经 `toolbuiltin.New(...)` 产出、`toolrun.New/runner.Add` 注册——
   **禁止**在 app/domain 直连；确认/超时/裁剪由 ToolRunner 统一执行；
4. 契约测试覆盖：调用、参数解析、FileTarget 申报；Runner 侧补矩阵判定、确认拒绝、
   超时、结果截断（样板 `toolrun/runner_test.go`）。

### 新增一个斜杠命令

1. DESIGN §7.3 命令表加行；
2. `internal/app/session.go` 的 `execCommand` 加 case（含 `/help` 文案）；
   MCP prompts 类动态命令不进 switch——经 `SetDynamicCommands` 注册、default 分支查询
   （静态命令优先，§6.1 扩展点 #4）；
3. 命令输出走返回字符串（装配根交给 UI），轮次输出走 Presenter——app 不碰 IO；
4. `session_test.go` / `plugins_test.go` 补命令测试（注入替身直接构造 Session）。

### 新增一档权限等级

1. **先改 DESIGN §9 矩阵表 + [decisions.md](decisions.md) 决策**；
2. `internal/domain/perm`：`Levels` 切片 + `File`/`Tool` 判定 + `perm_test` 全表断言
   （`Matrix()` 展示由判定推导，勿硬编码第二份）；
3. config 模板与校验无需变（`Parse` 自动接受新枚举）。

### 更换项目图标与 Windows 资源

1. 源图放 `assets/icon.png`（512×512、正方形、透明底 PNG；白底图会把方角带进产物）；
2. `go run ./cmd/iconify` 重新生成 `assets/icon/`：面积平均缩放的 16–256 PNG、
   `aquarius.ico`、`aquarius.icns`（容器手写、纯 Go 无三方依赖，测试覆盖编解码）；
3. Windows 程序图标与版本信息：`go generate ./cmd/aquarius`——按 `cmd/aquarius/winres/winres.json`
   重生成 `rsrc_windows_<arch>.syso` 并随包提交（`go run ...@v0.3.3` 走模块缓存，不进 go.mod）。
   文件名构建约束保证这些对象只链入 Windows 目标，`GOOS=linux/darwin` 交叉编译不受影响；
   改版本号同步改 winres.json 的 `fixed`/`info` 两处。

## 发版构建

- **版本号只在一处注入**：`go build -ldflags "-X main.version=v0.1.0" -o aquarius.exe ./cmd/aquarius`
  （不注入时 `-version` 显示 `dev`）。`aquarius.exe -version` 打印版本、平台与 Go 版本后直接
  退出——发版冒烟与问题排查都用它。
- **Windows 资源版本**在 `cmd/aquarius/winres/winres.json`（`fixed` / `info` 两处），发版前与
  ldflags 同步改，再 `go generate ./cmd/aquarius` 重生成 `.syso` 并提交。
- **发布物** = 单个二进制 + `README.md` / `LICENSE` / `THIRD-PARTY-NOTICES.md`（MPL-2.0 与
  第三方署名义务）。依赖有增减时，先按 `THIRD-PARTY-NOTICES.md` 顶部命令核对清单再打包。
- **发版前**：全量门禁（含 `-race` 与 `linuxgate`）+ `govulncheck`，并从一个**干净 clone**
  构建一次（排除工作区未跟踪文件与本地 `go.work` 的影响），用 `-version` 确认注入生效。
- 发布说明放 `docs/releases/<tag>.md`，与 tag 一起提交；内容面向用户，不写内部决策编号。

## 文档头部标记（更新日期 + 代码基线）

`docs/` 各篇与 `README.md` 的首个 H1 下面有一行标记，记「这份文档对应哪份代码」：

```markdown
# 用户手册
<!-- docs-sync: 2026-10-21 @ f1b9ed1 -->
```

- 标记是 HTML 注释：源码里一眼可见，**渲染后不出现**——面向用户的文档不该露出提交号。
- 日期 = 该文档**最近一次内容改动**的日期；哈希 = 那次改动**之前**的提交（文档不改代码，
  指向代码基线才有意义）。
- 由 `cmd/docsync` 维护，不手写：

```bash
go run ./cmd/docsync                # 只刷新工作区里改过的文档（提交前跑）
go run ./cmd/docsync -commit HEAD   # 只刷新该提交改过的文档（提交后补；需 --amend 并回）
go run ./cmd/docsync -all           # 全部刷成今天 + HEAD
go run ./cmd/docsync -check         # 门禁：有文档缺标记即退出 1
```

- 没改过的文档保持旧标记不动（仍然准确），所以一次文档提交只会动到那几篇。
- 覆盖 `docs/**/*.md` 与根 `README.md`；`DESIGN.md`、`AGENTS.md` 不参与（与代码同批改，
  标记会立刻过时）。

## 提交规范

- 祈使句英文：`feat:` / `fix:` / `test:` / `docs:` / `refactor:`；一个 commit 只做一件事。
- 写文档、写面向人的注释时照[文档地图](README.md)里的「文风」：陈述事实、结论在前，不写总结段与客套话。
- PR/提交说明写清：改了什么、对应 DESIGN 哪节/哪条决策（如 D22）、如何验证。
- **改 DESIGN 某节 → 同步检查 `docs/` 对应篇**（各篇头部标了来源节号）；
  已落地的写进 `docs/roadmap.md`，还没做的写进 `docs/plan.md`（两者不混写）。
- 质量门禁全绿（含 `-race`）才算完成。
