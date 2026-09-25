# AGENTS.md — Aquarius 项目协作指南

面向 AI 编码代理的操作说明。**动手前先读 `DESIGN.md`**（权威设计文档，中文）；
本文件只放干活需要的规则与命令。规则冲突时：用户显式指令 > 本文件 > 你的习惯做法。

## 项目概览

- **Aquarius**：面向个人的极简 AI 助手（对话优先、多模态出入、文档记忆、可插拔工具/模型）。
- **技术栈**：Go ≥ 1.22，单二进制交付（`go build ./cmd/aquarius`），无 cgo，纯 Go 依赖优先。
- **当前状态**：设计定稿、代码未动工。按 `DESIGN.md` §12 里程碑推进，**M0 先搭骨架**。
- **语言约定**：文档与注释用中文；标识符、commit message 用英文。

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
8. **密钥安全**：密钥只经 `port.Secrets` 按名取用；禁止写入配置明文、日志、会话树或附件。
9. **横切能力走装饰器**：重试/限流/截断/审计在 main 的装配处叠加端口装饰器，不进插件 API、不散落进业务代码。

## 常用命令

```bash
go build ./cmd/aquarius        # 构建二进制
go test ./...                  # 全量测试
go test -race ./...            # 竞态检测（合入前必过）
go test ./internal/domain/... -run TestProperty -count=50   # domain 性质测试加密跑
go vet ./...                   # 静态检查
gofmt -l .                     # 格式检查（应无输出）
```

## 代码风格

- `gofmt` 即风格；不引入额外格式化/lint 工具，除非用户要求。
- **接口由消费方定义、保持小**（1–3 个方法）；`context.Context` 作为外部调用的第一个参数。
- 错误用 `fmt.Errorf("...: %w", err)` 包装保留链路；不 panic 做控制流。
- 值对象用具名类型（如 `conversation.MessageID`），不裸用 `string`。
- 导出符号写注释；命名跟 `DESIGN.md` §2 统一语言对齐（如 Revise/Carry/Fresh/Head/Part/Job）。
- 每个端口的实现在 `internal/adapter/<name>/`，一个适配器一个目录；测试替身放测试文件内或 `internal/adapter/fake/`。

## 测试要求

- **改动 domain**：必须带/改性质测试。核心不变量三条（树合法、存在性引用、节点不可变）必须持续可证。
- **改动 app**：用脚本流 LLM + 收集器 Presenter + 脚本队列 Prompter 做交互回放（golden 测试），
  禁止测试里起真实网络。
- **改动 adapter**：跑该适配器的契约测试（临时目录/假 server）；LLM 适配器用录制流回放。
- **链路级验收**（如"摄取 → 附件入库 → 装配内联"）：可在测试中借用真实适配器（临时目录）串联，
  但**生产代码**的跨层直连仍被禁止（依赖铁律只约束非测试代码）。
- **改动 mcpgate/插件宿主**：用测试内假 MCP server（stdio）覆盖：发现、调用、超时、崩溃重启、授权拒绝。
- 新行为补测试，修 bug 先写复现测试。全部测试 + `go vet` + `gofmt` 通过才算完成。

## 提交与 PR

- Commit message（英文，祈使句）：`feat: ...` / `fix: ...` / `test: ...` / `docs: ...` / `refactor: ...`
- 一个 commit 只做一件事；PR 说明里写清：改了什么、对应 `DESIGN.md` 哪一节/哪条决策（如 D22）、如何验证。
- 若设计与 `DESIGN.md` 不一致：**先改 `DESIGN.md` 再改代码**，并在 §13 补决策记录。
- 改动 `DESIGN.md` 某节时，同步检查 `docs/` 对应篇（各篇头部标注了来源节号；冲突以 DESIGN 为准）；
  涉及里程碑进度时同步 README「状态」节（进度只维护这一处）。

## 目录速查

```
cmd/aquarius/        组装根（wiring：config → 插件/授权 → 端口装配含装饰器 → UI）
cmd/iconify/         开发工具：图标集生成（多尺寸 PNG / ICO / ICNS，纯 Go 无三方依赖）
assets/              源图标 icon.png 与生成物 icon/（README、Windows 资源嵌入共用）
pluginapi/v1/        对外稳定契约（Tier-1 插件唯一依赖；D29 后移出 M4）
internal/domain/     conversation（会话树/Part/Revise）、tool（Spec/Call/Result）、perm（权限矩阵）
internal/port/       llm/tool/store/memory/blob/modality/ingest/job/ui/misc
internal/app/        agent（Turn 循环）、prompt（装配/水位）、session（命令/摄取分派）、est（token 计数链）
internal/plugin/     registry、mcp（生命周期）、grant（授权）（M4 起建）
internal/adapter/    llm（含 llm/tokenizer 精确计数）、toolbuiltin（memory_*/file_*/think/
                     term_exec/job_*）、toolrun、storejson、memoryfs、repl、blobfs、jobproc、
                     ingestfile、ingestclip、notify、atomicfile
待建：                mcpgate、uitui、decorate（M4）；pluginapi/v1、plugingo（后移出 M4，D29）；
                     asr、tts（backlog，D27）
```
