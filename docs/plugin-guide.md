# 写一个进程内 Go 插件
<!-- docs-sync: 2026-11-28 @ 318d38f -->

> 读者：要给 Aquarius 写插件的人。假定你会写 Go，不必读过这个项目的源码。
> 读完能拿到什么：一个能被宿主认出来的插件、一个带这个插件的可执行文件，以及一份
> 「哪些事能做、哪些事契约不管」的清单。
>
> 本文只讲你要写什么、宿主在什么时刻来调你。宿主内部怎么把插件接进来、类型之间怎么
> 转换、装配顺序怎么排，都不在这篇里——那些是改宿主的人要看的。

本文这种插件在项目内部文档里也叫「Tier-1 插件」，与 MCP server（Tier-2）、
WASM 插件（Tier-3）并列。看到这三个词，按「进程内 Go / 进程外 MCP / 进程内 WASM」理解即可。

## 1. 先选形态

扩展 Aquarius 有三条路，选错会在半路撞墙：

| 形态 | 怎么装 | 能供给什么 | 什么时候选 |
|---|---|---|---|
| **进程内 Go 插件**（本文） | 编进宿主二进制 | §3 词表的供给面 + 界面贡献（面板/设置页/窗口） | 要接模型、记忆、输入输出，要贡献面板与设置页，要长驻状态 |
| **MCP server** | 写进配置或插件目录，运行期连接 | 工具、记忆只读投影、命令 | 服务已经存在，或者想用别的语言写 |
| **WASM 插件** | 放进插件目录，运行期装载 | 只有工具面 | 只提供工具，且不愿意让用户换二进制 |

判断顺序建议：能用 MCP 就用 MCP（生态成熟、不用碰 Go、装完即用）；只缺一个工具、
又要免安装，看 WASM；只有用到记忆接管、面板、模型这类宿主内部能力，才写本文这种插件。

代价先讲明白：进程内 Go 插件**没有沙箱**，与宿主同一个进程、同一份权限。这种插件技术上能
自己开网络、自己读写文件，契约允许、不审计、也不承诺（见「安全与授权」）。换来的是能力上限最高。

## 2. 最小插件：五件东西

一个能跑起来的插件仓库长这样：

```
myplug/
  go.mod              插件模块
  pluginext.go        接入口：告诉宿主「我这个插件怎么造」（构建时被覆盖进宿主）
  mypack/plugin.go    清单 + 工厂 + 实例
  mypack/tools.go     供给面实现（本示例是工具面）
  mypack/..._test.go  你自己的测试
```

### 2.1 go.mod

```go
module github.com/you/myplug

go 1.27.4

require github.com/Tonyjh07/Aquarius/pluginapi v0.0.0

replace github.com/Tonyjh07/Aquarius/pluginapi => ../Aquarius/pluginapi
```

`pluginapi` 是唯一的依赖入口，零传递依赖（只用标准库）。这个包单独一个 go.mod，
将来破坏性变更会开 `pluginapi/v2` 与 v1 并存——你的插件继续编 v1 就行。

`replace` 是因为契约包还没有独立的版本 tag：指到你本地的宿主仓库即可。宿主发布
带 tag 的契约包之后，去掉 `replace`、`require` 写具体版本。

### 2.2 接入口 `pluginext.go`

```go
//go:build tier1sample

package main

import (
	"github.com/Tonyjh07/Aquarius/pluginapi"
	"github.com/Tonyjh07/myplug/mypack"
)

func externalFactories() []pluginapi.Factory {
	return []pluginapi.Factory{mypack.New()}
}
```

三条硬性要求，缺一条构建就失败：

1. **`package main`**：这个文件会被覆盖进宿主的 `cmd/aquarius` 包里，那一包是 main。
2. **带构建约束**（上面是 `tier1sample`）：不带的话，你自己仓库里跑
   `go build ./...` 会报「package main 没有 func main」，测试也跑不动。
3. **函数名是 `externalFactories`**：宿主里留了一个同样签名的空实现，构建时被这份替换掉。

### 2.3 清单与实例

```go
package mypack

import (
	"context"

	"github.com/Tonyjh07/Aquarius/pluginapi"
)

func New() pluginapi.Factory { return factory{} }

type factory struct{}

func (factory) Manifest() pluginapi.Manifest {
	return pluginapi.Manifest{
		Name:     "myplug",
		API:      pluginapi.APIVersion,
		Provides: []string{"tool"},
	}
}

func (factory) Create(h pluginapi.Host) (pluginapi.Instance, error) {
	return &plugin{h: h}, nil
}

type plugin struct{ h pluginapi.Host }

func (p *plugin) Start() error { return nil }

func (p *plugin) Shutdown(ctx context.Context) error { return nil }

func (p *plugin) Tools(ctx context.Context) []pluginapi.ToolSpec {
	return []pluginapi.ToolSpec{{
		Name:        "echo",
		Description: "把输入原样回显",
		Schema:      []byte(`{"type":"object","properties":{"text":{"type":"string"}}}`),
		Risk:        pluginapi.RiskSafe,
	}}
}

func (p *plugin) Execute(ctx context.Context, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
	return pluginapi.ToolResult{OK: true, Output: string(call.Args)}, nil
}
```

供给面的方法**不挂在 `Instance` 上**：宿主按 `Manifest.Provides` 对实例做 type assertion
（声明 `llm` 就必须实现 `LanguageModel`）。一个插件可以同时供给多个面，`Instance` 只管生与死。

### 2.4 构建

在宿主仓库里跑：

```bash
go run ./cmd/aqbuild -plugin ../myplug
```

产物落在插件目录，名字是 `Aquarius-with-plugin.exe`。产物就是一个完整的宿主，只是多了你的插件。
插件默认禁用，在设置窗「插件」页或 `/plugin enable myplug` 里打开。

改完代码重新跑一次这条命令即可，宿主仓库不产生任何文件（`git status` 保持干净）。

## 3. 清单

`Manifest` 是编译进插件的自我声明，宿主启动时逐项校验：

| 字段 | 含义 |
|---|---|
| `Name` | 插件名，全局唯一。同时进工具名、命令名与私有目录名，所以不能带路径分隔符与 `.` / `..` |
| `API` | 编译时用的契约版本，填 `pluginapi.APIVersion`。宿主只认相等的值，不匹配即拒载 |
| `Capabilities` | 能力自述（如 `network`）。只作展示与审计归组，**不触发授权弹窗**（见「安全与授权」） |
| `Provides` | 供给面自述，宿主据此做「声明了就得实现」的自检 |
| `Locales` | 你自带的界面语种（`zh` / `en`）。空 = 不贡献设置页与面板 |

`Provides` 的词表：

```
llm  tool  command  memory  ingest  output
transcribe  synthesize  secrets  view  settings
```

写错一个词等于声明了宿主不认识的面，校验会拒载并报出是哪个词。

`Manifest()` 必须是不依赖实例状态的纯函数——校验发生在 `Create` 之前，那时还没有实例。

## 4. 供给面

供给面接口见 §3 的 Provides 词表（llm / tool / command / memory / ingest / output /
transcribe / synthesize / secrets 九个经典面，加 view / settings 两个界面贡献面，§6.2 /
§6.3），按需要实现其中几个，`Provides` 里声明哪几个就实现哪几个。

### 4.1 总则

- 一个插件可以实现多个面，比如「记忆 + 工具 + 面板 + 命令」是记忆插件的常规组合。
- 声明与实现不符不会拖到第一次调用：`Create` 之后、`Start` 之前宿主会自检，
  不符即拒载，报因进 `/plugin`。
- 供给面方法随时可能被调用，也随时可能收到 `Shutdown`——都要认 ctx（见「生命周期」）。

### 4.2 模型（`llm` → `LanguageModel`）

```go
type LanguageModel interface {
	Generate(ctx context.Context, req GenerateRequest) (Stream, error)
	Models(ctx context.Context) ([]ModelInfo, error)
}

type Stream interface {
	Recv() (Delta, error) // 流结束返回 io.EOF 或包装了 io.EOF 的错误
	Close() error
}
```

请求已经由宿主装配好：消息、工具清单、采样参数、预算都在 `GenerateRequest` 里，
你只负责按协议发出去、把增量读回来。`Generate` 返回 err 时 stream 恒为 nil。

几处必须照做：

- **`Usage.InputTokens` 是总输入**，含缓存读写两段。成本折算归宿主，你只报 token。
- **透传字节原样保真**：`Delta.ReasoningSignature`、`Delta.Server.Raw`、
  `PromptMessage.ServerBlocks`、`PromptPart.Data`（已解析的图片字节，不是路径）
  都不许解码后重新编码——协议要求的是原文。
- **`ServerTools` 非空时 `Tools` 必须为空**：协议里服务端工具与本地工具不能同时声明。
- **`Sampling` 的浮点参数是指针**：nil = 不发送，0 是「要确定性输出」这个有意义的取值。
- `Delta.Usage` 只在流末尾出现一次，其余增量上为 nil。
- `Delta.Title` 是服务端生成的会话标题，整串一次到达；拿不到就留空，是正常回落。

可选扩展 `TokenCounter`（精确计数）与 `Prober`（能力探测）见「可选扩展」。

### 4.3 工具（`tool` → `ToolProvider`）

```go
type ToolProvider interface {
	Tools(ctx context.Context) []ToolSpec
	Execute(ctx context.Context, call ToolCall) (ToolResult, error)
}
```

`Tools` **每一轮都会调用**，包括同一轮内工具循环的每一次。所以清单可以随你的状态变化
——「每个记忆点一个工具，调用即下钻」这类形态就靠这一点。也所以 `Tools` 必须便宜：
读自己的内存状态，别在这里做网络请求或全库扫描。

四条约束：

1. **名字文法 `[A-Za-z0-9_-]{1,64}`**，在当前会话可见集合内唯一。不合文法或同轮重名，
   整份清单被拒并报因（模型端点会 400，早点报好读）。中文标题要转成 ASCII 名，
   标题全文放进 `Description`——模型选工具读的是 name + description。
2. **同一轮内稳定**：模型这一轮看到的清单与本次 `Execute` 的查找要一致。
3. **不同会话可以同名**：名字按 ctx 里的会话解析，不需要全局唯一。与内置工具同名时
   以内置为准，你那条被丢弃。
4. **零工具不拒载**：声明了 `tool` 但这一轮一件都没有，是正常状态。

`ToolSpec.Schema` 是 JSON Schema 原文，透传不解读。`Risk` 只有 `safe` 与 `confirm`
两档；需要确认的动作走 `Host.Confirm`，**不要在 `Execute` 里自己问用户**——确认、超时、
结果裁剪都由宿主包在外面，你拿到的永远是已经裁决过的调用。

可选扩展 `Describer` 能把调用参数提炼成一句让用户看得懂的话，用于确认提示。

### 4.4 命令（`command` → `CommandProvider`）

```go
type CommandProvider interface {
	Commands() []CommandSpec
	Run(ctx context.Context, name string, args []string) (string, error)
}
```

- `CommandSpec.Names` 首名是主名，其余是别名；`Run` 收到的是其中之一，你按自己的表认。
- `Desc` 与 `Usage` 是用户面文本，别带内部编号。
- 命令进同一张命令表，`/help`、补全浮层、分派都读这张表。
- **撞名只丢那一条**：与内置命令或 MCP 命令同名时，你这条不出列，插件本身照常启用。
  丢的时候宿主记一条 warn，`/plugin` 的日志能解释「为什么我的 `/mem` 没出现」。
- 清单在实例生命周期内保持稳定；每次调用都重新取实例，不用缓存实例引用。

### 4.5 记忆（`memory` → `Memory`）

```go
type Memory interface {
	Recall(ctx context.Context, req MemoryRequest) (MemoryContext, error)
}
```

这是**完全替代**内置文档记忆，不是扩内置记忆。存哪（文件、SQLite、远端）、怎么组织
（文档、图层、向量、图）、怎么管理（工具、命令、面板）全归你，宿主一条都不假设。

五条后果：

1. **独占**：同一时刻至多一个 `provides: memory` 的插件启用。两个同时启用由宿主拒绝，
   没有「先声明的赢」。
2. **内置退出**：接管期间 `memory_list` / `memory_read` / `memory_search` / `memory_write`
   四件工具与 `/memory` 命令退出，MCP resources 不再投影记忆，内置记忆转只读。
   记忆的读写面由你自己提供——这正是要用 `tool` / `command` / `view` 的原因。
3. **`Recall` 返回的是 markdown 原文**：宿主原样放进上下文，不解读、不重排、不加标题。
   返回空 `Text` 表示本轮不注入，是正常状态。
4. **预算**：`MemoryRequest.BudgetTokens` 是软预算，超了宿主硬截断，截断点由宿主挑。
   所以要按这个数自己裁，别指望截得好看。
5. **迁移**：可选接口 `LegacyMemoryHost` 给你内置记忆的一次性只读快照（全局一份 +
   每个会话各一份），只有 `provides: memory` 的插件拿得到。搬不搬、搬之前要不要
   `Confirm` 由你决定，宿主不自动搬。

会话身份不在 `MemoryRequest` 里，从 ctx 拿（见「会话身份」）。

### 4.6 摄取与输出（`ingest` / `output`）

```go
type Ingestor interface {
	Accepts(raw RawInput) bool
	Ingest(ctx context.Context, raw RawInput) (IngestReport, error)
}

type OutputAdapter interface {
	Name() string
	Deliver(ctx context.Context, req OutputRequest) error
}
```

- 摄取器是降级链：多个按顺序问，第一个 `Accepts` 为真的接单。`Accepts` 必须是纯函数，
  宿主拿 `Accepts` 做路由。`Ingest` 报错则继续问下一个。
- 输出去的是**每条已提交消息的副本**，多路各自独立，一个失败不影响别的。`Parts` 可能
  含思考分片，按需自己筛。

附件上限有常量：`MaxAttachments`（单条消息件数）、`MaxImageBytes`（单张字节数）。

### 4.7 语音（`transcribe` / `synthesize`）

```go
type Transcriber interface {
	Transcribe(ctx context.Context, audio BlobRef, lang string) (string, error)
}

type Synthesizer interface {
	Synthesize(ctx context.Context, req SpeechRequest) (BlobRef, error)
}
```

音频字节始终走附件库，不传路径。`SpeechRequest` 的 `Voice` / `Design` / `Style` / `Tags`
都是配置项，不进模型上下文、不进会话树。

可选扩展 `SpeechSegmenter`：逐帧喂入、按说话切段。帧长固定（32ms 的 PCM16 单声道 16kHz），
不用自己再切帧。

### 4.8 密钥（`secrets` → `SecretProvider`）

```go
type SecretProvider interface {
	Get(ctx context.Context, name string) (string, error)
}
```

方向与 `Host.Secrets()` 相反：这里是宿主调你取密钥（keychain 类实现），那边是你请宿主
解析 `secret:<name>`。没有列举接口——能列出来的密钥清单本身就是泄密面。取不到要返回
错误，别用空串兼作失败（空串可能是合法值）。

### 4.9 可选扩展

独立接口，宿主对实例或 `Host` 句柄做 type assertion 探测，命中才用。不需要就是你不用实现，
实现了也不用申报。

| 接口 | 探测对象 | 作用 |
|---|---|---|
| `TokenCounter` | 实例 | 精确计数，覆盖宿主的估算链 |
| `Prober` | 实例 | 自行实测模型是否具备某项能力，三态结论 |
| `Describer` | 实例 | 把调用参数提炼成一句确认提示；纯函数，没有 ctx |
| `SpeechSegmenter` | 实例 | 逐帧切段 |
| `NoticePoster` | `Host` | 把轻量提示放进消息流；未命中就退回 `Notify` |
| `LegacyMemoryHost` | `Host` | 内置记忆一次性只读快照 |
| `UIEnviron` | `Host` | 宿主界面长什么样（见「想自己开窗口」） |

## 5. Host：你能从宿主拿到什么

`Create(h Host)` 给你的这个句柄是全部宿主服务：

```go
type Host interface {
	Logger() Logger
	Secrets() SecretResolver
	Blobs() BlobStore
	DataDir() string
	WorkDir() string
	Locale() string
	Confirm(ctx context.Context, req ConfirmRequest) (ConfirmAnswer, error)
	Notify(level, text string)
}
```

| 方法 | 用法与边界 |
|---|---|
| `Logger` | 运行日志出口。级别你给，上下文段宿主填成 `plugin:<name>`，事件与处置两句你写，kv 用「名, 值」且名固定英文。**不要自开日志文件** |
| `Secrets` | 按名解析，只给解析不给存储。明文不落到你的落盘面 |
| `Blobs` | 内容寻址附件库：`Put` / `Get` / `Stat`。没有 GC——只管写自己产出的字节 |
| `DataDir` | 插件私有持久目录，跨重启存活。库、索引、游标、迁移快照放这里；宿主不清理、不读内容、不做备份 |
| `WorkDir` | 插件私有临时目录，每次新建实例前清空。中间产物、解包目录放这里 |
| `Locale` | 当前界面语言，`zh` / `en`，整次运行内不变。空串 = 宿主没给，用你自己的缺省 |
| `Confirm` | 需要用户确认的动作必须走这里：权限矩阵、审计、无人值守语义都罩在这一层。带 ctx |
| `Notify` | 系统通知。消息流内的轻量提示用 `NoticePoster`（见「可选扩展」） |

两个目录的实际位置：`<profile>/plugin-data/<name>` 与 `<profile>/plugin-tmp/<name>`。
建不出来时给空串，插件按「不留存」降级，不因此拒载。

`Host` 只给这八项，**方法集会一直只加独立接口、不扩本体**——扩了会破坏所有实现方。
`Host` 句柄不跨实例使用：`Shutdown` 之后宿主把句柄置失效，再调直接报错。

### 会话身份：从 ctx 拿

工具执行、命令执行、面板渲染与动作、记忆注入，这四个面都可能需要知道「现在在哪个会话」。
身份只走 ctx，不在任何值类型里：

```go
sid, ok := pluginapi.SessionFrom(ctx) // 空串 = 不注入；ok=false = 这次调用没有会话
```

值类型（`ToolCall` / `ViewAction` / `MemoryRequest`）都不带会话字段，别去找。

## 6. 界面

### 6.1 原则：你交声明，不交像素

契约里没有任何 GUI 类型，也没有回调式绘制。你产出一棵语义组件树，图形界面按自己的
方式渲染，文本界面降级成对齐文本。同一份数据两个消费者。

好处是可预期的：宿主换界面技术栈你不用改，文本界面不是二等公民，你的插件也不会长得
跟别人不一样。

界面**不推送**：宿主打开视图时来取当前帧（`Render`），用户操作时把动作交回来
（`OnAction`），你按自己的状态算出新树返回。状态权威在你这边，宿主只持当前帧。

### 6.2 面板（`view` → `ViewProvider`）

```go
type ViewProvider interface {
	Views() []ViewSpec
	Render(ctx context.Context, viewID string) (Node, error)
	OnAction(ctx context.Context, act ViewAction) (Node, error)
}
```

组件词汇七种：`text` / `markdown` / `table` / `kv` / `list` / `form` / `buttons`。

```go
type Node struct {
	Kind     NodeKind
	Text     string
	Table    *TableSpec
	Items    []string
	Fields   []FormField
	Actions  []Action
	Children []Node
}
```

几点必须知道：

- **`Kind` 零值 = 纯容器**：自身不产生像素与文本行，只按序渲染 `Children`。
  要组合「一张表 + 表上的按钮」就用零值 `Kind`，别借 `list` 充当容器——那会多一行空列表。
- **`Children` 与任何 `Kind` 可以共存**：先画自身，再画子节点。
- **`Render` 会被频繁调用**：读自己的内存状态，别在里面做网络请求。
- **动作带全量表单值**：一次 `ViewAction` 同时带 `ActionID` 与可见表单的当前值。
  没有「只提交某个字段」的路径——要提交按钮，就在 `Actions` 里放一条。
- **文本界面只读**：`form` / `buttons` 照渲染（形状、当前值、编号），但不接交互，
  末行提示「操作请用命令」。想让文本界面也能改，就同时提供命令。
- **入口是命令**：`/plugin view <插件> [<面板ID>]`。只有一个面板时可以省略 ID。

`ViewSpec.Window` 可以申请尺寸（dp）：`Width` / `Height` / `MinWidth` / `MinHeight`，
零值 = 宿主缺省。宿主夹到 `[320,4096]`，最小尺寸不超过声明尺寸。窗口仍然是宿主的：
拿不到句柄，也没有「置顶 / 无边框 / 位置 / 父子窗」。文本界面忽略这些值。

### 6.3 设置页（`settings` → `SettingsSectionProvider`）

```go
type SettingsSectionProvider interface {
	Sections() []SettingsSection
	ApplySettings(ctx context.Context, patch SettingsPatch) error
}
```

你声明分区（标题、说明，以及这一段要显示的行），宿主按插件与分区渲染，保存时把**你这一段**的键回传。

- **一页统收、按分区保存**：底栏的「保存并应用」是全量内核配置，与你无关；每个分区
  自带保存按钮，只提交这一段的键。按 `SectionID` 认段，多给的键忽略。
- **值永远现取**：开窗与每次保存成功后各取一次快照，控件按行表重建。做归一（截断、
  夹取）时，界面显示的是生效值，不是用户刚敲进去的值。
- **未知控件种类回落到文本行**：旧宿主读到新种类时降一档显示当前值，不让整行消失。
- **位置由你声明**：`SettingsSection.Placement` 缺省内联（与插件清单同屏）；
  填 `pluginapi.SettingsPage` 表示这一段参数多、说明长，宿主把这一段放进只属于你这个插件的一页
  （从清单行的「设置…」进入，页内聚焦、带返回）。多少行算多是你的语义，宿主数不出来。
- **搬家是整块的**：任意一段声明为独立页，你这个插件的其余分区也一起搬过去，清单页
  不再留你的任何参数。半页内联、半页独立最糟——用户记不住哪个参数在哪一页。
- **管理归宿主**：清单（开关、状态、供给面摘要、失败原因）由宿主渲染，你只声明参数。
- 参数怎么落盘由你决定（常见是 `<DataDir>/settings.json`），宿主不解析这份文件。

### 6.4 通知

`Host.Notify` 发系统通知。消息流内的轻量提示用可选接口 `NoticePoster`，
文本界面降级为状态行。

### 6.5 文案

插件文案不进宿主的内核文案表。你自己带 `zh` / `en` 两份，在 `Manifest.Locales` 里声明，
运行时按 `Host.Locale()` 挑。没带宿主当前那种语言就用自己的缺省。

宿主只翻译自己渲染的壳（窗口标题模板、按钮语义字）。

### 6.6 想自己开窗口？

可以做，但代价自负。契约不提供这条路的接口，你可以直接 `import gio` 自己画——能编进
同一个二进制、能跑，但主题、字体回退、停用关窗、崩溃域全归你。`import gio` 还会让
「契约包零传递依赖」这条稳定性在你这个插件上失效：宿主升级 gio，你就得重编。

走这条路前可以先问宿主「现在长什么样」：可选接口 `UIEnviron` 对 `Host` 句柄做断言：

```go
type UIEnviron interface {
	Environ() UIEnvironment
}
```

快照含前端种类（`gui` / `tui` / `repl`）、深浅、`Scale`、六个语义色
（`BG` / `FG` / `Muted` / `Accent` / `Border` / `Sel`，RRGGBB）、正文与符号字体候选路径。

- **只读、不通知**：深浅色变了不推、不回调。要跟随就自己轮询。
- **没有句柄、没有事件、没有 channel**：只回答状态。
- **只给六个色**，其余是宿主的卡片版式，给出去就把版式变成契约了。
- **字体是 best-effort 路径**：可能空、可能变，缺字形要自己退到无衬线。
- **文本界面只回答种类**：终端背景是用户设的，宿主没有可信的深浅色，编一个比说
  「不知道」更坏。零值快照合法，你必须能退化。

## 7. 生命周期：会被反复启停

```
Registered → Validated → Stopped ──启用──> Active ──禁用──> Draining → Stopped
```

你看到的只有三个时刻：`Create`、`Start`、`Shutdown`。

- **`Create` 每次启用都会调**：重新启用永远是干净的新实例，你不用让初始化可重入。
  代价是全局状态随实例丢弃——这正是启停给用户的预期。
- **`Start` 要快**：同步返回。重活异步做，或首次调用时懒做。
- **任何时刻都可能收到 `Shutdown`**，包括供给面方法执行中。所以每个供给面方法都要认
  ctx 的取消。
- **`Shutdown` 返回即死透**：goroutine 收净、文件句柄落定，不接「复活后补一笔」。
- **没有暂停档**：临时静默 = 禁用再启用。
- **禁用会先进入 Draining**：宿主先摘掉你的工具、关掉你打开的面板窗、让进行中的一轮
  用旧模型跑完，然后等在途调用结束（限时，超时按失败结果补齐），最后才 `Shutdown`。

启停由用户在设置窗开关或 `/plugin enable|disable` 触发，改动立即生效、不重启。唯一
需要重启的是换二进制本身（新增或删除编进去的插件）。启动时按配置恢复启用状态；
一次启用失败只记一行，不写盘、不影响其他插件。

`/plugin list` 把进程内插件与 MCP 插件合成一张表；你这边的来源显示 `binary`、传输
显示 `in-process`。

## 8. 版本纪律：你的插件能一直编

契约包按「只增不改」演进，这条对你的意义是：升级宿主不用改你的代码。

1. 独立 go.mod，宿主对 `pluginapi` 的依赖是唯一入口，你 `go get` 这一个包就够。
2. 已发布字段不改语义与类型；新字段只加在结构体末尾，并注明零值语义。
3. 新方法只加在新的独立接口上（断言探测），不扩已发布的接口。
4. 接口签名冻结；要改就开 v2 包并存，宿主按 major 各自转换，v1 插件不必动。
5. 每个类型注释带一句兼容承诺：改这个类型会破坏谁的二进制。

你也可以自己留余地：`Manifest.API` 是硬断言，宿主升级到 `api: 2` 时你保持 `API: 1`
会拒载——那时按 v2 的说明改。

## 9. 安全与授权

1. **`Capabilities` 只是自述**：进程内插件没有隔离，宿主不为插件弹授权窗。信任由
   「插件被编进了这个二进制」承载，不是由声明承载。
2. **确认链是硬的**：需要用户确认的动作必须走 `Host.Confirm`。插件自造确认 UI 不算数。
3. **密钥只有一条路**：`Host.Secrets` 按名解析。别把明文写进配置文件或日志。
4. **契约外行为**：自开网络、自己读写文件系统——允许、不审计、不承诺。规格明写，
   不假装拦得住。出事的是你的插件，不是宿主。

## 10. 打包与分发

### 10.1 构建器（`cmd/aqbuild`）

参数不认识任何具体插件，只认路径、包名与 tag：

| 参数 | 缺省 | 作用 |
|---|---|---|
| `-plugin` | 必填 | 插件仓库根（含 go.mod） |
| `-hook` | `pluginext.go` | 接入口文件（相对 `-plugin`） |
| `-host` | `.` | 宿主仓库根 |
| `-target` | `cmd/aquarius/pluginext.go` | 被替换的宿主接入口（相对 `-host`）；文件不存在即报错 |
| `-pkg` | `./cmd/aquarius` | 要构建的包（相对 `-host`） |
| `-tag` | `tier1sample` | 互斥开关，与接入口文件里的构建约束成对 |
| `-o` | `<plugin>/Aquarius-with-plugin[.exe]` | 产物路径 |
| `-work` | 缓存目录 | modfile、go.sum 副本与 overlay 的落点 |
| `-bundle` | 无 | 随产物一起放的外部程序（相对 `-plugin`，可重复） |
| `-v` | 关 | 打每条 `go` 命令原文，排障用 |

构建过程不改宿主工作副本：所有中间文件落在你这边或缓存目录，跑完宿主 `git status`
一个字节都不变。

### 10.2 依赖冲突

你和宿主各自的依赖会在同一张模块图里解析，规则是 Go 的常规 MVS：

| 情况 | 结果 |
|---|---|
| 同一个模块，两边版本不同 | 取较高版本。**静默升版**：你写的版本可能被抬高，行为随之变化 |
| 同一模块的两个大版本（`/v2`） | 并存，互不干扰 |
| 你在 go.mod 里写 `replace` | 被忽略：构建读的是宿主那份依赖视图的副本 |
| 两边的 fork 模块路径不同 | 两个包都进二进制，同名类型互不兼容，编译期报错 |
| 两份 `pluginapi` | 构建器强制指向宿主自己那份，`Create(Host)` 签名才对得上 |

实践建议：插件依赖越少越好；要用宿主没有的库时，优先选不与宿主重叠的模块；
真撞上「必须不同版本」的库，考虑把那个库挪到「自带外部进程」那条路里。

### 10.3 自带外部进程

需要宿主没有的库（图表、音视频、某个语言的运行时），或要把一段可能崩的解析隔离出去，
可以自己拉起一个外部程序、自己定协议。**宿主不认识这个进程**：宿主看到的仍是普通插件，
工具面、面板、设置面全走现有契约。

`-bundle <相对插件根>` 把外部程序拷到产物旁边（可重复）。插件靠「自己这个 exe 在哪」
找那个程序，这是唯一不需要配置、也不依赖运行目录的定位方式。构建前就挡掉三种情况：
文件不在、给的是目录、与产物或其他文件同名。

四条硬要求，不是建议：

1. **子进程必须自保：stdin 读到 EOF 就退出。** 宿主崩溃、被任务管理器杀掉时你没机会跑
   `Shutdown`。父亲死了管道必断，这一条不依赖任何合作机制。只在 `Shutdown` 里 kill
   只覆盖「正常停用」一条路径。
2. **日志要接管**：子进程的 stdout / stderr 接进 `Host.Logger()`。不接的话出问题时
   两边都是空白。
3. **能力不透传**：子进程拿不到 `Secrets` / `Blobs` / `Confirm`。要用就你代取代转，
   因此你对子进程负全部信任责任。
4. **协议不外包**：握手、版本拒绝、重连、错误码全是你的事。宿主不检查协议合法性：
   协议由你定，宿主不认识。

该不该划进程边界，看一件事：**这个外部程序需不需要独立的生命周期与容错**。会卡、会崩、
要单独重启 → 值得划；只是当库用 → 直接编进来更简单。

## 11. 能力边界：能做、不能做、自己负责

**能做**：接新厂商的模型、精确计数、能力探测；任何形状的新工具与新命令；接管记忆
（换存储、换组织、换检索）；新输入摄取与新输出镜像；接新 ASR / TTS 引擎（外置服务
形状的唤醒词、声纹、说话人分离）；接 keychain；贡献设置页、面板与通知。

**契约明确排除**：

- **动会话树**：拿不到写面，也看不到对话内容。
- **注入模型上下文**：唯一注入点是记忆段的 `Memory.Recall`，没有 system prompt
  或消息流的注入接口。
- **替换或改造宿主界面**：不暴露 Gio、不改主题、不往消息流里塞东西。

**技术上可行、契约不承诺（自行负责）**：

- 自开网络、自己读写文件：不走审计、不走 `Host.Logger`，密钥自理。
- 自己拉起外部程序、自定协议：宿主不认识那个进程（见「自带外部进程」）。
- 自己拿 gio 开窗口：能跑，但主题、字体回退、停用关窗、崩溃域全归你（见「想自己开窗口」）。
- 后台长任务：生命周期只有 `Start` / `Shutdown` 两个锚点，没有任务台账，半截状态自理。
- 插件之间直接调用：没有寻址，只能经内核的工具注册表绕行。

**v1 先不做**：面板词汇表扩展（每加一种要两个渲染器跟上）；状态栏挂件位；消息流装饰
接口；进程内插件的运行期装载（要装完即用，走 WASM 那条路，但那条路只有工具面）。

## 12. 发布前自查

- [ ] `Manifest.Name` 唯一、能当目录名；`API` 用 `pluginapi.APIVersion`。
- [ ] `Provides` 里每一项都有对应实现；没有多余声明。
- [ ] 工具名满足 `[A-Za-z0-9_-]{1,64}`，同轮稳定；`Tools` 不做 IO。
- [ ] 所有需要确认的动作走 `Host.Confirm`；没有自造确认 UI。
- [ ] 没有自开日志文件；日志走 `Host.Logger`，kv 名用英文。
- [ ] 落盘只写 `DataDir` / `WorkDir`；不假设别的路径。
- [ ] 供给面方法认 ctx 取消；`Shutdown` 返回后没有残留 goroutine 与句柄。
- [ ] `Start` 同步返回、不阻塞。
- [ ] 界面文案自带 `zh` / `en`；`Node` 只产出词汇表里的种类。
- [ ] 声明了独立设置页的分区，参数确实多到值得占一页。
- [ ] 用 `-bundle` 时，子进程 stdin EOF 就退出、日志接进了宿主。
- [ ] 在干净仓库里跑一遍 `go run ./cmd/aqbuild -plugin ../myplug`，宿主 `git status` 干净。
