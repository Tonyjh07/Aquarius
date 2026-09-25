// Command aquarius 是 Aquarius 的单二进制入口：
// 装配 config → Secrets/LLM/Store 端口 → 会话 → REPL（DESIGN §8 布局 / §12 M0 验收）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/ingestclip"
	"github.com/Tonyjh07/Aquarius/internal/adapter/ingestfile"
	"github.com/Tonyjh07/Aquarius/internal/adapter/jobproc"
	"github.com/Tonyjh07/Aquarius/internal/adapter/llm"
	"github.com/Tonyjh07/Aquarius/internal/adapter/memoryfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/notify"
	"github.com/Tonyjh07/Aquarius/internal/adapter/repl"
	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolbuiltin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// minOutputReserve 硬保底截断为本轮输出预留的最小 token 数（DESIGN §7.1）。
const minOutputReserve = 1024

// run 程序主体（标准流可注入，便于端到端回放测试）。返回进程退出码。
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("aquarius", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "", "数据目录（默认 ~/.aquarius）")
	modelName := flags.String("model", "", "覆盖 model.name")
	baseURL := flags.String("base-url", "", "覆盖 model.base_url")
	autoYes := flags.Bool("yes", false, "跳过逐次确认（等价对每个确认回答 y；危险操作慎用）")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	// 数据目录（三级覆盖里的 flag 先于 env/config，DESIGN §8）。
	dir := *dataDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(stderr, "无法确定家目录: %v\n", err)
			return 1
		}
		dir = filepath.Join(home, ".aquarius")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "创建数据目录 %s: %v\n", dir, err)
		return 1
	}
	// 特权目录（D22）：<dataDir>/sandbox，权限矩阵的免确认 rw 格。
	sandboxDir := filepath.Join(dir, "sandbox")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "创建特权目录 %s: %v\n", sandboxDir, err)
		return 1
	}

	// 配置：不存在 = 首次运行，写模板并指引填写后重跑。
	cfgPath := filepath.Join(dir, "config.json")
	cfg, err := loadConfig(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		if werr := writeDefaultConfig(cfgPath); werr != nil {
			fmt.Fprintf(stderr, "生成配置失败: %v\n", werr)
			return 1
		}
		fmt.Fprintf(stdout, "已生成配置: %s\n请编辑 model.name / model.base_url，并设置密钥环境变量后重新运行。\n"+
			"  api_key 采用引用格式 secret:<环境变量名>，例如 secret:AQUARIUS_OPENAI_KEY 对应环境变量 AQUARIUS_OPENAI_KEY\n",
			cfgPath)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "读取配置失败: %v\n", err)
		return 1
	}

	// 环境变量与 flag 覆盖（flags > 环境变量 > config）。
	if v := os.Getenv("AQUARIUS_MODEL"); v != "" {
		cfg.Model.Name = v
	}
	if v := os.Getenv("AQUARIUS_BASE_URL"); v != "" {
		cfg.Model.BaseURL = v
	}
	if *modelName != "" {
		cfg.Model.Name = *modelName
	}
	if *baseURL != "" {
		cfg.Model.BaseURL = *baseURL
	}
	if cfg.Model.Name == "" || cfg.Model.BaseURL == "" {
		fmt.Fprintf(stderr, "model.name 与 model.base_url 不可为空\n")
		return 1
	}
	if cfg.UI.Kind == "" {
		cfg.UI.Kind = "repl"
	}
	if cfg.UI.Kind != "repl" {
		fmt.Fprintf(stderr, "ui.kind=%q 尚未支持（bubbletea TUI 见里程碑 M4）\n", cfg.UI.Kind)
		return 1
	}
	secretRef, err := secretName(cfg.Model.APIKey)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	apiKey, err := (envSecrets{}).Get(context.Background(), secretRef)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	// 权限等级（D22）：缺省 strict；非法值报因退出。
	level := perm.DefaultLevel
	if raw := strings.TrimSpace(cfg.Permissions.Level); raw != "" {
		level, err = perm.Parse(raw)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
	}

	// 权限等级活状态（D22 执行接入）：persistLevel 写回成功后同步，ToolRunner 经 func 读取
	//（REPL 单 goroutine 顺序调用，无并发）。
	lvl := &levelHolder{level: level}

	// /permission 写回 config（D22）：map 级重排保留其余配置键；成功后同步活等级。
	persistLevel := func(l perm.Level) error {
		data, rerr := os.ReadFile(cfgPath)
		if rerr != nil {
			return rerr
		}
		var generic map[string]any
		if rerr := json.Unmarshal(data, &generic); rerr != nil {
			return fmt.Errorf("解析 %s: %w", cfgPath, rerr)
		}
		perms, _ := generic["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
			generic["permissions"] = perms
		}
		perms["level"] = string(l)
		out, rerr := json.MarshalIndent(generic, "", "  ")
		if rerr != nil {
			return fmt.Errorf("编码 config: %w", rerr)
		}
		// 唯一临时文件 + 原子换入：无半截文件，覆盖沿用既有权限位（§14 遗留）。
		if rerr := atomicfile.WriteFile(cfgPath, append(out, '\n'), 0o644); rerr != nil {
			return fmt.Errorf("写入 %s: %w", cfgPath, rerr)
		}
		lvl.Set(l) // 工具链路活等级（D22 执行接入）
		return nil
	}

	// 端口装配：全部经端口契约注入（内置不享特权，D13）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store, err := storejson.New(filepath.Join(dir, "conversations"))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	// 附件库（DESIGN §4.2）：sha256 内容寻址存 <dir>/attachments；
	// 启动 GC 按全量会话引用清扫（引用收集不完整则跳过，宁可漏清不误删）。
	blobs, err := blobfs.New(filepath.Join(dir, "attachments"))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	runAttachmentGC(ctx, store, blobs, stderr)
	// 记忆（D23 布局）：全局 memories.md + 会话 <id>.memory.md（与会话树同目录）。
	mem, err := memoryfs.New(filepath.Join(dir, port.GlobalMemoryDoc), filepath.Join(dir, "conversations"))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	client, err := llm.New(llm.Config{
		BaseURL: cfg.Model.BaseURL, APIKey: apiKey, Tokenizer: cfg.Model.Tokenizer,
	})
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	ui := repl.New(stdin, stdout)
	// 输出器扇出（D28/D14）：Presenter 装饰器包住实际 UI，仅 output.notify 开启时装配；
	// app 与 repl 均不感知输出器，Deliver 失败只记日志（§5.7）。
	var outs []port.OutputAdapter
	if cfg.Output.Notify {
		outs = append(outs, notify.New(notifySenderImpl(runtime.GOOS)))
	}
	presenter := &outputsPresenter{
		inner: ui,
		outs:  outs,
		log:   func(err error) { fmt.Fprintf(stderr, "[输出器] %v\n", err) },
	}
	// 逐次确认（§5.10 Confirmer 矩阵）：默认走 REPL 交互；-yes 一律应 y。
	var confirmer port.Confirmer = ui
	if *autoYes {
		confirmer = yesConfirmer{}
	}
	// 内置工具 + ToolRunner 门面（D25：查找/权限判定/确认/超时/裁剪）。
	// 后台任务（DESIGN §5.8/D8）：日志落盘 <dir>/jobs，任务表内存态。
	jobs, err := jobproc.New(filepath.Join(dir, "jobs"))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	toolTimeout := time.Duration(cfg.Limits.ToolTimeoutSec) * time.Second
	if cfg.Limits.ToolTimeoutSec <= 0 {
		toolTimeout = 60 * time.Second
	}
	maxOutput := cfg.Limits.ToolOutputChars
	if maxOutput <= 0 {
		maxOutput = 20000
	}
	runner := toolrun.New(toolrun.Options{
		Tools: toolbuiltin.New(mem, func(name string) (string, bool) {
			p, err := mem.Path(name)
			return p, err == nil
		}, jobs),
		Confirmer:   confirmer,
		Level:       lvl.Get,
		SandboxPath: sandboxDir,
		Timeout:     toolTimeout,
		MaxOutput:   maxOutput,
	})
	// 横切装饰器链（D14/§10，装配根叠加）：审计记每次真实调用，重试包在审计之外
	// （一次用户可见的 Generate = 多条审计行），硬保底截断最内（发给服务端前裁到预算内）。
	audit, err := decorate.NewAudit(filepath.Join(dir, "audit.log"), 0)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer audit.Close()
	var counter port.TokenCounter
	if c, ok := any(client).(port.TokenCounter); ok {
		counter = c // 三级计数链②（D26）：裁剪估算与 Agent 内部共用同一 tokenizer
	}
	maxCtx := cfg.Limits.MaxContextTokens
	if maxCtx <= 0 {
		maxCtx = app.DefaultMaxContextTokens
	}
	fit := func(fctx context.Context, req port.GenerateRequest) (port.GenerateRequest, int) {
		reserve := req.Budget.MaxOutputTokens
		if reserve < minOutputReserve {
			reserve = minOutputReserve
		}
		kept, omitted := app.TrimOldest(fctx, counter, req.Messages, maxCtx-reserve)
		req.Messages = kept
		return req, omitted
	}
	var gen port.LLM = client
	gen = decorate.NewTruncate(gen, fit, func(omitted int) {
		_ = presenter.Emit(ctx, port.NoticeEvent{
			Text: fmt.Sprintf("硬保底截断：上下文超预算，已省略 %d 条最旧消息", omitted),
		})
	})
	gen = decorate.AuditLLM(gen, audit)
	gen = decorate.NewRetry(gen)
	auditedRunner := decorate.AuditTool(runner, audit)
	ids := systemIDGen{}
	agent, err := app.New(
		app.Deps{
			LLM: gen, UI: presenter, IDs: ids, Clock: systemClock{},
			Tools: auditedRunner, Memory: mem, Blobs: blobs,
		},
		app.Config{
			Model: cfg.Model.Name, System: cfg.SystemPrompt, MaxTurns: cfg.Limits.MaxTurns,
			CompactThreshold: cfg.Limits.CompactThreshold, MaxContextTokens: cfg.Limits.MaxContextTokens,
		},
	)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	runner.Add(agent.ContextCompactTool()) // 装配期注册（agent 依赖 runner，反向补注册）

	sess, err := app.NewSession(ctx, app.SessionDeps{
		Store:        store,
		Agent:        agent,
		IDs:          ids,
		Clock:        systemClock{},
		SystemPrompt: cfg.SystemPrompt,
		Level:        level,
		SandboxPath:  sandboxDir,
		PersistLevel: persistLevel,
		Confirmer:    confirmer,
		Jobs:         jobs,
		Ingestors:    []port.Ingestor{ingestfile.New(blobs), ingestclip.New(blobs)},
		UI:           presenter,
		OpenMemory:   openMemoryEditor(mem, stdin, stdout, stderr),
	})
	if err != nil {
		if ctx.Err() != nil {
			return 0 // 启动期 Ctrl+C：ctx 早于主循环创建，按取消干净收尾（不报错退出）
		}
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	if ctx.Err() != nil {
		return 0
	}

	ui.Say("Aquarius — 输入 /help 查看命令，/quit 退出；Ctrl+C 取消当前生成")
	for {
		ui.Prompt()
		in, err := ui.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				fmt.Fprintln(stdout)
				return 0
			}
			fmt.Fprintf(stderr, "读取输入失败: %v\n", err)
			return 1
		}
		out, herr := sess.Handle(ctx, in)
		if out != "" {
			ui.Say(out)
		}
		if errors.Is(herr, app.ErrQuit) {
			fmt.Fprintln(stdout)
			return 0
		}
		if herr != nil {
			_ = ui.Emit(ctx, port.ErrorEvent{Err: herr})
			if ctx.Err() != nil {
				fmt.Fprintln(stdout)
				return 0
			}
		}
	}
}

// envSecrets port.Secrets 的内置实现：按名读环境变量（矩阵 DESIGN §5.10）。
// 返回值只在进程内传递，不落日志、不进会话树。
type envSecrets struct{}

var _ port.Secrets = envSecrets{}

// levelHolder 权限等级活状态（D22 执行接入）：/permission 写回成功后 Set，
// ToolRunner 经 Get 读取。REPL 单 goroutine 顺序调用，无需加锁。
type levelHolder struct{ level perm.Level }

func (h *levelHolder) Get() perm.Level  { return h.level }
func (h *levelHolder) Set(l perm.Level) { h.level = l }

// pickEditor 选择系统编辑器（D24）：$VISUAL → $EDITOR → 平台默认（windows 记事本 / vi）。
func pickEditor(visual, editor, goos string) []string {
	if s := strings.TrimSpace(visual); s != "" {
		return strings.Fields(s)
	}
	if s := strings.TrimSpace(editor); s != "" {
		return strings.Fields(s)
	}
	if goos == "windows" {
		return []string{"notepad"}
	}
	return []string{"vi"}
}

// openMemoryEditor /memory 的装配实现（D24）：文档名 → 磁盘路径（缺失先建空文件）
// → 系统编辑器阻塞打开；保存后下次读取即生效。
// stdio 来自 run() 注入的三流——保持"标准流可注入"的 e2e 契约（不直连 os.Std*）。
func openMemoryEditor(mem *memoryfs.Store, stdin io.Reader, stdout, stderr io.Writer) func(name string) (string, error) {
	return func(name string) (string, error) {
		p, err := mem.Path(name)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return "", fmt.Errorf("创建记忆目录: %w", err)
			}
			if err := os.WriteFile(p, []byte{}, 0o644); err != nil {
				return "", fmt.Errorf("创建记忆文件 %s: %w", p, err)
			}
		}
		argv := pickEditor(os.Getenv("VISUAL"), os.Getenv("EDITOR"), runtime.GOOS)
		cmd := exec.Command(argv[0], append(argv[1:], p)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("运行编辑器 %s: %w", argv[0], err)
		}
		return p, nil
	}
}

func (envSecrets) Get(_ context.Context, name string) (string, error) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return "", fmt.Errorf("环境变量 %s 未设置（config 的 api_key 引用它）", name)
	}
	return v, nil
}

// systemClock port.Clock 的内置实现：系统时钟。
type systemClock struct{}

var _ port.Clock = systemClock{}

func (systemClock) Now() time.Time { return time.Now() }

// yesConfirmer port.Confirmer 的内置实现：-yes 下所有确认一律同意（§5.10 矩阵）。
type yesConfirmer struct{}

var _ port.Confirmer = yesConfirmer{}

func (yesConfirmer) Confirm(context.Context, string) (bool, error) { return true, nil }

// systemIDGen port.IDGen 的内置实现：复用 domain 的进程内单调 ULID。
type systemIDGen struct{}

var _ port.IDGen = systemIDGen{}

func (systemIDGen) ConversationID() conversation.ID   { return conversation.NewID() }
func (systemIDGen) MessageID() conversation.MessageID { return conversation.NewMessageID() }
func (systemIDGen) CallID() tool.CallID               { return tool.CallID(conversation.NewMessageID()) }
