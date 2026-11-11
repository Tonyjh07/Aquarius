package decide

// System One 客户端（D281/D290）：`POST {base}/v1/systemone`，三题一次问完。
//
// 一份实现覆盖 Jev、Cloudflare clef-flash（Workers AI）、Ollama ≥ 0.35.1 与 Unsloth
// Decision API——它们认同一个 wire format，只换 base_url 与 model。**不是** OpenAI chat
// 形状：拿 `kind: openai-chat` 的服务指向这里，必然失败。
//
// 适配器只做三件事：拼 state、发一次、解析回来。阈值不在这里判（那是装饰器的事），
// 超时不在这里设（ctx 由调用方给），重试也没有——一次超时拖成两次只会更慢。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 三句题面是**契约**，不是配置（D290 代价 ④）：2026-11-11 那轮 92 次实测用的就是这三句，
// 换一个字分数就整体位移（`needs_human` 把 "look at" 换成 "must be confirmed"，拒绝侧
// 召回从 11/12 掉到 6/12）。要改题面，等于整组默认阈值作废、必须重标。
const (
	shouldAllowPrompt = "The assistant should run this tool call without asking the user first"
	riskPrompt        = "How risky is this tool call"
	needsHumanPrompt  = "A human should look at this tool call before it runs"
)

// riskCriteria 是 score 题的三档锚点。`risk` 的期望值落在 0–2，阈值 0.5 就落在低档之内。
// 这三句同样是契约的一部分：档位描述换了，同样的调用会落到另一档。
var riskCriteria = []string{
	"reversible, and confined to files the project already owns",
	"writes outside the working directory, or changes state at an external service",
	"irreversible, involves credentials, system settings, or production systems",
}

// state 固定六行（契约 §5.20）。空值留空行，不猜默认——模型看得见「没给」比看见一个
// 编出来的 medium 有用。
const stateTemplate = "origin: %s\ntool: %s\nop: %s\ntarget: %s\nrisk: %s\ntask: %s"

// Options 建客户端要的东西；都由装配根从 decide 段填好。
type Options struct {
	BaseURL   string
	Model     string
	Key       string       // 已解析的明文密钥；空 = 不带 Authorization
	TaskChars int          // 0 = state 里不带 task 行
	HTTP      *http.Client // 可注入；nil = 每次请求单独控时（见 Decide）
}

// Client System One 客户端。
type Client struct {
	url       string
	model     string
	key       string
	taskChars int
	http      *http.Client
}

// New 建客户端。baseURL 带不带 `/v1` 都认——两个地址都是用户会随手填的样子。
func New(opts Options) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("decide: no endpoint configured")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("decide: no model configured")
	}
	url := base + "/v1/systemone"
	if strings.HasSuffix(base, "/v1") {
		url = base + "/systemone"
	}
	c := &Client{
		url: url, model: strings.TrimSpace(opts.Model), key: opts.Key,
		taskChars: opts.TaskChars, http: opts.HTTP,
	}
	if c.http == nil {
		c.http = &http.Client{}
	}
	return c, nil
}

// request System One 请求体。
type request struct {
	Model     string              `json:"model"`
	State     string              `json:"state"`
	Questions map[string]question `json:"questions"`
}

type question struct {
	Type         string   `json:"type"`
	Instructions string   `json:"instructions"`
	Criteria     []string `json:"criteria,omitempty"`
}

// response 响应体。只取用得到的三处；`legend` / `confidence` / `probabilities` 留着
// 不解析——审计要的是判定用的那两个数，多存一份分布只会让日志更难读。
type response struct {
	Model   string            `json:"model"`
	Answers map[string]answer `json:"answers"`
}

// answer 一道题的答案。Noul / Score 用指针：`noul: 0` 与「没这一项」必须分得开，
// 后者是协议不合形状，要报错而不是当成「概率为零」。
type answer struct {
	Type  string   `json:"type"`
	Noul  *float64 `json:"noul"`
	Score *float64 `json:"score"`
}

// Decide 问一次。超时由 ctx 管（装饰器按 decide.timeout_ms 设好），这里不做二次限制，
// 也不重试。
func (c *Client) Decide(ctx context.Context, req port.DecideRequest) (port.Decision, error) {
	body := request{
		Model: c.model,
		State: c.state(req),
		Questions: map[string]question{
			"should_allow": {Type: "noul", Instructions: shouldAllowPrompt},
			"risk":         {Type: "score", Instructions: riskPrompt, Criteria: riskCriteria},
			"needs_human":  {Type: "noul", Instructions: needsHumanPrompt},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return port.Decision{}, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(raw))
	if err != nil {
		return port.Decision{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.key)
	}

	start := time.Now()
	resp, err := c.http.Do(hreq)
	if err != nil {
		return port.Decision{}, err
	}
	defer resp.Body.Close()
	// 响应体上限：正常只有几百字节。给到 1 MiB 是防一个坏端点上不封顶地灌。
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return port.Decision{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return port.Decision{}, fmt.Errorf("decide: %s: %s", resp.Status, snippet(payload))
	}
	var out response
	if err := json.Unmarshal(payload, &out); err != nil {
		return port.Decision{}, fmt.Errorf("decide: decode: %w", err)
	}
	dec, err := out.decision(c.model)
	if err != nil {
		return port.Decision{}, err
	}
	dec.Latency = time.Since(start)
	return dec, nil
}

// Warmup 发一次最小请求把连接或模型拉热。本机冷启动实测 6.6 s——不预热的话，服务刚
// 起后的第一次判定必然超时，而「第一次总是问用户」会变成固定行为。
//
// 失败不报错：预热本来就是尽力而为，真连不上时下一次判定会如实失败并上交。
func (c *Client) Warmup(ctx context.Context) {
	_, _ = c.Decide(ctx, port.DecideRequest{
		Origin: "toolrun", Tool: "warmup", Target: "warmup", Risk: "low", Task: "warmup",
	})
}

// state 拼固定六行。task 超过 taskChars 时截断——外部代理的任务描述可以很长，而这一段
// 只用来给判定提供上下文，前 80 字已经够。
func (c *Client) state(req port.DecideRequest) string {
	task := req.Task
	if c.taskChars <= 0 {
		task = ""
	} else if r := []rune(task); len(r) > c.taskChars {
		task = string(r[:c.taskChars])
	}
	return fmt.Sprintf(stateTemplate, oneLine(req.Origin), oneLine(req.Tool),
		oneLine(req.Op), oneLine(req.Target), oneLine(req.Risk), oneLine(task))
}

// oneLine 把换行压平：state 是逐行解析的，一个换行就能伪造出一行 `risk: low`。
func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.TrimSpace(s))
}

// decision 把三道题的答案折成端口值。缺题、题型不对、值没给，一律报错——「没判成」
// 必须走交回确认的那条路，不能悄悄变成 0。
func (r response) decision(fallbackModel string) (port.Decision, error) {
	allow, err := r.noul("should_allow")
	if err != nil {
		return port.Decision{}, err
	}
	human, err := r.noul("needs_human")
	if err != nil {
		return port.Decision{}, err
	}
	risk, err := r.score("risk")
	if err != nil {
		return port.Decision{}, err
	}
	model := strings.TrimSpace(r.Model)
	if model == "" {
		model = fallbackModel
	}
	return port.Decision{ShouldAllow: allow, Risk: risk, NeedsHuman: human, Model: model}, nil
}

func (r response) noul(id string) (float64, error) {
	a, ok := r.Answers[id]
	if !ok {
		return 0, fmt.Errorf("decide: answer %q missing", id)
	}
	if a.Noul == nil {
		return 0, fmt.Errorf("decide: answer %q has no noul value (type %q)", id, a.Type)
	}
	return *a.Noul, nil
}

func (r response) score(id string) (float64, error) {
	a, ok := r.Answers[id]
	if !ok {
		return 0, fmt.Errorf("decide: answer %q missing", id)
	}
	if a.Score == nil {
		return 0, fmt.Errorf("decide: answer %q has no score value (type %q)", id, a.Type)
	}
	return *a.Score, nil
}

// snippet 错误响应体前 200 字节；多行压成一行，免得一行日志被撑成一块。
func snippet(b []byte) string {
	s := oneLine(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
