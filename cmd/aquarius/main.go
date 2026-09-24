// Command aquarius 是 Aquarius 的单二进制入口：
// 装配 config → Secrets/LLM/Store 端口 → 会话 → REPL（DESIGN §8 布局 / §12 M0 验收）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/llm"
	"github.com/Tonyjh07/Aquarius/internal/adapter/repl"
	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run 程序主体（标准流可注入，便于端到端回放测试）。返回进程退出码。
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("aquarius", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "", "数据目录（默认 ~/.aquarius）")
	modelName := flags.String("model", "", "覆盖 model.name")
	baseURL := flags.String("base-url", "", "覆盖 model.base_url")
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

	// 端口装配：全部经端口契约注入（内置不享特权，D13）。
	store, err := storejson.New(filepath.Join(dir, "conversations"))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	client, err := llm.New(llm.Config{BaseURL: cfg.Model.BaseURL, APIKey: apiKey})
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	ui := repl.New(stdin, stdout)
	ids := systemIDGen{}
	agent, err := app.New(
		app.Deps{LLM: client, UI: ui, IDs: ids, Clock: systemClock{}},
		app.Config{Model: cfg.Model.Name, System: cfg.SystemPrompt, MaxTurns: cfg.Limits.MaxTurns},
	)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	sess, err := app.NewSession(ctx, app.SessionDeps{
		Store:        store,
		Agent:        agent,
		IDs:          ids,
		Clock:        systemClock{},
		SystemPrompt: cfg.SystemPrompt,
	})
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
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

// systemIDGen port.IDGen 的内置实现：复用 domain 的进程内单调 ULID。
type systemIDGen struct{}

var _ port.IDGen = systemIDGen{}

func (systemIDGen) ConversationID() conversation.ID   { return conversation.NewID() }
func (systemIDGen) MessageID() conversation.MessageID { return conversation.NewMessageID() }
func (systemIDGen) CallID() tool.CallID               { return tool.CallID(conversation.NewMessageID()) }
