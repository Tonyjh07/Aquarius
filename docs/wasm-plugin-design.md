# Tier-3 WASM 插件（运行期装载）设计规格
<!-- docs-sync: 2026-11-28 @ d106c4f -->

> 定位：第三种插件形态——**进程内 wasm 沙箱、运行期从目录装载、不用重编宿主**。只提供工具面。
> 三级模型的总形状见 [contracts.md](contracts.md) §6.2；
> Tier-1 的完整契约见 [plugin-guide.md](plugin-guide.md)（作者面）与
> [plugin-design.md](plugin-design.md)（内核侧）；本文不重复也不改动它们的结论。
> 决策动机与否决见 [decisions.md](decisions.md) 的 D344。

## 1. 定位与范围

Tier-3 回答的是 Tier-1 答不了的那个问题：**插件作者怎么做到「不改上游仓库、不重编宿主」**。
答案是宿主里跑一个 wasm 运行时，插件是一个 `.wasm` 文件，放进目录就能用。

三句边界，先立住：

1. **装的是 wasm 模块，不是动态库**：wasip1 reactor（`GOOS=wasip1 GOARCH=wasm
   -buildmode=c-shared`），由 wazero 解释执行。宿主仍然无 cgo、单二进制。
2. **只有工具面**：一次调用、一个结果。没有命令面、记忆接管、面板 / UI、LLM /
   ASR / TTS 供给面、后台任务。
3. **沙箱是交付物本身，不是附加项**：只能碰自己的 `/data`，没有网络，没有密钥。

代价写在明处：Tier-3 的插件能力上限低，做到需要长驻状态、要联网、要接管记忆的事就
该换 Tier-1。Tier-3 换来的三件事是 Tier-1 给不了的——不用重编、插件作者不碰上游、默认隔离。

## 2. 为什么是 wasm

Go 没有可用的运行期代码加载。四条路里只有一条站得住（否决理由见 D344）：

| 路线 | 结论 |
|---|---|
| `plugin` 包 | 否：Windows 不支持、没有卸载、两边构建参数必须一致 |
| `-buildmode=c-shared` | 否：要 cgo、双 Go runtime 同进程、契约要跨 C ABI 重写 |
| 交给独立进程（MCP 已覆盖） | 不是「运行期装载」的答案，是另一个形态 |
| **wasm reactor（wazero）** | 是：纯 Go 解释器、Windows 可用、天然沙箱 |

代价要认：模块要重新定义一层 ABI（拿不到 Go 接口），解释执行比原生慢，单实例单线程。
这些与「少一点功能面」是同一笔账。

## 3. 宿主侧形状

- **运行时**：wazero（纯 Go，无 cgo）。进程内一份 `wazero.Runtime`，每个插件一个 module
  实例；`wasi_snapshot_preview1` 提供文件与随机数。
- **接入点**：与 Tier-1 同一个——`port.ToolSet`（SPEC：插件工具按轮给、按会话解析）。
  装配根把实例包成 `ToolSet` 交给 Runner，工具名形如 `w3:<插件名>:<工具名>`。前缀
  让审计归组与重名消解都有现成办法（Tier-2 用 `mcp:`，Tier-3 用 `w3:`）。
- **状态机**：与 Tier-1 同形（Inactive / Active / Draining），但少了 Host、迁移与名字
  占用判定——Tier-3 不接管任何内置面，没有「腾名字」这回事。

## 4. ABI v1

模块只认两件事：两个导出函数、一个宿主函数。冻结面就这么大，以后加 `op` 是加法。

**导出**

| 导出 | 签名 | 说明 |
|---|---|---|
| `aq_alloc` | `(size u32) -> ptr u32` | 宿主在写请求前调用；返回的缓冲必须由插件**挂住**到下一次调用（不能是局部 slice 的指针） |
| `aq_invoke` | `(ptr u32, len u32) -> u64` | 请求 JSON 在 `[ptr, ptr+len)`；返回值 `(ptr<<32)|len` 指向响应 JSON，同样由插件挂住 |

`_initialize` 由 `-buildmode=c-shared` 产出，宿主在实例化时显式调用
（wazero 的 `WithStartFunctions("_initialize")`）。

**宿主函数**（模块名 `aquarius`）

| 函数 | 签名 | 说明 |
|---|---|---|
| `log` | `(level u32, ptr u32, len u32) -> ()` | level：0 debug / 1 info / 2 warn / 3 error；进宿主日志，上下文 `plugin`，带插件名 |

v1 只有这一个宿主函数：不给密钥、不给确认、不给网络。需要用户确认的工具靠 `risk`
声明走宿主的确认链，插件自己不弹窗。

**请求 / 响应**（都是单个 JSON 对象）

```json
{"op": "manifest"}
{"op": "init", "api": 1, "plugin": {"name": "x", "version": "0.1.0",
                                    "data_dir": "/data", "locale": "zh"}}
{"op": "tools"}
{"op": "call", "tool": "t", "args": {}, "session": "<会话 ID>"}
```

响应统一信封：`{"ok": true, ...}` / `{"ok": false, "error": "人读的一句话"}`。

- `manifest` → `{"ok": true, "api": 1, "name": "...", "version": "..."}`；`api` 与宿主
  支持的最大版本不符即拒载。清单里的 `wasm.api`（§6）与这里自述的 `api` 必须一致——
  不一致说明两个文件不是同一次构建的产物，拒载并报因。
- `init` → `{"ok": true}`；插件在这里读 `data_dir` / `locale` 做一次准备。每个实例
  只调一次，重启用新实例。
- `tools` → `{"ok": true, "tools": [{"name": "t", "desc": "...", "params": {JSON Schema},
  "risk": "safe" | "confirm"}]}`。名字在插件内唯一，不必带前缀（宿主加）。
- `call` → `{"ok": true, "text": "..."}`；`ok:false` 直接成为工具失败的报因。

**失败分两类，处理不同**：插件回了 `ok:false` = 业务失败（结果回给模型）；trap、
超时、返回不是合法 JSON = 实例故障（结果回给模型 + 记一条日志 + 实例标记死亡）。

**资源上限**（常量，不进配置）：单实例内存 64 MiB、单次调用 30 秒。超时即关模块，
实例当场死，下一次调用重建。

## 5. 内存与生命周期纪律

wasm 线性内存是共享的，ABI 的稳定性一半靠这些纪律：

1. **缓冲必须挂住**：`aq_alloc` 与 `aq_invoke` 返回的指针，在宿主下一次调用前必须有效。
   局部 `make` 的 slice 返回指针会被 Go 回收，读出来是脏数据——这是实测踩过的坑，写在
   SDK 注释与文档里。
2. **一次一个请求**：实例是单线程的，宿主对每个实例串行化调用（互斥）。插件不需要
   处理并发。
3. **超时与崩溃都算死亡**：wazero 用 ctx 打断靠关模块实现，「打断」之后就不可复用。
   重建代价是一次实例化，比给半死实例擦屁股便宜。
4. **不设 `Shutdown` 导出**：卸载即 `Close` 模块，插件没有收尾机会。要落盘的写在
   每次 `call` 里完成（`/data` 是持久目录，关了也在）。

## 6. 发现与清单

与 Tier-2 同一个目录、同一个文件名，靠段名区分：

```
<profile>/plugins/<name>/plugin.json   {"name": "...", "wasm": {...}}
<profile>/plugins/<name>/plugin.wasm
```

```json
{
  "name": "unit-convert",
  "wasm": {"module": "plugin.wasm", "api": 1},
  "risk": "safe",
  "capabilities": ["tools"]
}
```

- `wasm` 段存在即 Tier-3；`mcp` 段存在即 Tier-2；两段同时存在 → 拒载并报因（一个
  目录只能是一种形态）。
- `module` 相对该目录，禁绝对路径与 `..`。
- 缺 `risk` 按发现源默认 **Confirm**（同 D328 的 plugins 目录规则）。
- 清单里的 `name`、模块 `manifest` 自述的 `name`、目录名三者必须一致：目录名是给人看的
  删除对象，不一致会让人删错。任何一处对不上即拒载并报因。
- Tier-2 的发现扫描遇到带 `wasm` 段的清单不当作坏文件（不报「plugin.json 解析失败」），
  静默跳过，交给 Tier-3 那一侧。

## 7. 沙箱与授权

1. **文件**：只把 `<profile>/plugin-data/<name>` 以 `/data` 挂进 guest，只读不到别处。
   插件拿到的 `data_dir` 就是 `/data`——插件看不到真实路径，宿主也不用把 `/data` 映射出去。
2. **网络**：没有。wasip1 无 socket，宿主也不注入。要联网的插件只能用 Tier-1 或 Tier-2。
3. **密钥**：不给。`aquarius.log` 之外没有宿主函数，密钥没有入口。
4. **确认链**：工具的 `risk` 为 `confirm` 时，宿主在调用前走自己的确认（与 Tier-1 的
   `Host.Confirm`、Tier-2 的工具风险同一条链），插件不参与。
5. **capabilities 仍是展示语义**：Tier-3 的隔离是运行时给的，不靠声明兑现；声明只用于
   `/plugin` 展示与审计归组。

## 8. 启停与运维

1. 状态键沿用 `plugins.<name>.enabled`（D31 语义）：键缺失 = 启用。
2. 启用 = 实例化 + `init` + 取 `tools` + 挂 `ToolSet`；停用反向：摘源 → `Close`。
   全程不重启进程。
3. 启动恢复照 Tier-1 的做法：启动期按 config 逐个启用，失败只记一行、不写盘。
4. 运行期故障（trap / 超时）→ 实例标记死亡并在下次启用时重建；重建限次 + 退避，
   数值沿用 Tier-2 的常量（3 次、1 秒起）。
5. `/plugin list` 里显示 `source: plugins`、`transport: wasm`；`/plugin logs` 与 Tier-1
   同一句实话：调用记在审计日志的工具名下，反查不到所属插件。
6. 设置窗的插件页只读展示，不提供安装入口（装插件 = 用户往目录里放文件）。

## 9. 版本纪律

1. **ABI v1 只增不改**：`op` 可以加，已有 `op` 的字段不改语义；不兼容的变更升
   `api: 2`，宿主按 `manifest.api` 分派并存。
2. 工具声明里的 `params` 用 JSON Schema，形状与内置工具的参数描述一致，不另立词表。
3. 文档与 SDK 是同一个 ABI 的两份表述：改一处必须改另一处（SDK 在
   `pluginapi/wasm/v1`，只依赖标准库）。

## 10. 能力边界清单

**能做**：任意「输入 → 输出」形状的工具；对着 `/data` 做小规模持久化；按 `locale`
出本地化文案；声明风险让宿主走确认。

**做不到（写死，不承诺）**：联网；读 `/data` 之外的任何路径；拿密钥；改会话树；
注入模型上下文；长驻后台任务（调用返回即结束，进程退出也不通知）；面板与 UI；
接管记忆与任何内置面。

**性能口径**：解释执行，工具调用是毫秒级开销。适合轻量计算与本地数据处理，不适合
大循环重度计算——那类工具应走 Tier-1。

## 11. 验收

形态是「往目录里放一个 `.wasm`，`/plugin enable` 之后模型能用上」：

1. **ABI 端到端**：测试内用 `go build` 编一个 testdata guest，装载、`init`、取工具、
   调用、错误回传、超时打断、崩溃重建各一条。
2. **沙箱**：guest 读写 `/data` 成功；越界路径失败；无网络（不注入，做不到即通过）。
3. **隔离**：两个 Tier-3 插件同名工具不互相覆盖（前缀区分）；一个插件崩溃不影响另一个。
4. **管理面**：`/plugin list` 能看到、启停即时生效、config 写回正确。
5. **回归**：默认构建不引入任何插件，`pluginapi/wasm` 的独立性测试守着该包只依赖标准库。

## 12. 决策索引

本文规格的动机与否决立为 [decisions.md](decisions.md) 的 D344，要点：

1. 运行期装载只有 wasm reactor 一条路（三条被否），代价是重造一层薄 ABI。
2. 只做工具面：命令面、记忆、UI、其他供给面全部不做。
3. 沙箱边界写死：`/data` + 无网络 + 无密钥。
4. 工具名 `w3:<插件>:<工具>`，与 `mcp:` 同一套命名纪律。
5. ABI v1 只有两个导出 + 一个宿主函数，`op` 只增不改。
