# 开发指南

> 本文汇编自 [AGENTS.md](../AGENTS.md)（规则原文以其为准）与 [DESIGN.md](../DESIGN.md)
> §11/§12；**冲突以前两者为准**。面向：后续贡献者与 AI 编码代理。

## 环境与门禁

```bash
go build ./cmd/aquarius        # 构建单二进制
go test ./...                  # 全量测试
go test -race ./...            # 竞态检测（合入前必过）
go test ./internal/domain/... -run TestProperty -count=50   # 性质测试加密跑
go vet ./...                   # 静态检查
gofmt -l .                     # 格式检查（应无输出）
```

- 语言约定：文档/注释中文；标识符、commit message 英文祈使句。
- **Windows 下 `-race` 需要 CGO + C 编译器**：本机已装 llvm-mingw 并
  `go env -w CGO_ENABLED=1 CC=<clang.exe 路径>`（用户级配置，不入库）。
- 全部测试**不起真实网络**：LLM 用脚本流/httptest 假 SSE 服务，存储用临时目录。

## 代码分层速查

```
cmd/aquarius      组装根（唯一知道"具体实现"的地方）
internal/app      agent(Turn 循环) · prompt(装配/水位/记忆注入) · session(命令) · est(token 计数链)
internal/port     端口 = 接口 + DTO（消费方定义、1–3 方法、ctx 首参）
internal/domain   conversation(树) · tool(值对象) · perm(权限矩阵) —— 零依赖
internal/adapter  llm(含 llm/tokenizer) / repl / storejson / memoryfs / toolbuiltin / toolrun
                  + M3：blobfs / jobproc / ingestfile / ingestclip / notify / atomicfile
                  （一个适配器一个目录）
```

接口由消费方定义；错误 `fmt.Errorf("...: %w")` 包装；值对象具名类型；
导出符号写注释；命名与 DESIGN §2 统一语言对齐（Revise/Carry/Fresh/Head/Part/Job）。

## 测试手段（四种，按层选）

| 层 | 手段 | 样板位置 |
|---|---|---|
| domain | **性质测试**：随机操作序列 → 三条不变量恒成立；快照比对证明不可变 | `domain/conversation/property_test.go` |
| app | **交互回放**：脚本流 LLM + 收集器 Presenter + 脚本队列 Prompter | `app/fakes_test.go`、`app/agent_test.go` |
| app | **golden 回放**：命令脚本 → 归一化 transcript（ID 归一为标签）与 `testdata/golden/` 基准比对；改语义后 `go test ./internal/app -run TestGoldenReplay -update` 重写基准并人工检视 | `app/golden_test.go` |
| adapter | **契约测试**：storejson/memoryfs 临时目录；LLM 用 httptest 假 SSE 录制流回放；toolrun 全等级矩阵表 + 脚本确认器 | `adapter/storejson/store_test.go`、`adapter/llm/openai_test.go`、`adapter/toolrun/runner_test.go` |
| tokenizer | **官方向量比对**：期望值由 Python `tokenizers` 一次性生成入库（`testdata/gen_*.py`）；fixture 全离线跑，真实 DeepSeek 词表向量需 `AQUARIUS_TOKENIZER_JSON=<路径>`（缺省跳过） | `adapter/llm/tokenizer/tokenizer_test.go` |
| e2e | **进程内装配回放**：`run(args, stdin, stdout, stderr)` + 假服务喂 stdin 断言 stdout（含工具确认 y/n、权限等级矩阵、M3 的 term_exec/job 链路） | `cmd/aquarius/main_test.go` |

修 bug 先写复现测试；新行为补测试。测试替身放测试文件内或 `internal/adapter/fake/`。

## How-to

### 新增一个端口适配器

1. 先在 DESIGN §5/§5.10 补契约与矩阵行（**先改文档再改代码**，§13 补决策如适用）；
2. `internal/port` 定义/复用接口（消费方定义、保持小）；
3. `internal/adapter/<name>/` 实现 + 同目录契约测试（临时目录 / 假 server）；
4. `cmd/aquarius` 装配注入；横切能力（重试/限流/截断）在装配根做装饰器，不进适配器。

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

1. **先改 DESIGN §9 矩阵表 + §13 决策**；
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

## 提交规范

- 祈使句英文：`feat:` / `fix:` / `test:` / `docs:` / `refactor:`；一个 commit 只做一件事。
- PR/提交说明写清：改了什么、对应 DESIGN 哪节/哪条决策（如 D22）、如何验证。
- **改 DESIGN 某节 → 同步检查 `docs/` 对应篇**（各篇头部标了来源节号）；
  README 状态节涉及进度也一并更新。
- 质量门禁全绿（含 `-race`）才算完成。
