// Package llm 实现 port.LLM 的 OpenAI 兼容适配器（Chat Completions + SSE 流式）。
//
// 兼容目标：OpenAI / DeepSeek / vLLM / Ollama 等提供 /chat/completions、stream=true SSE 的服务。
// 语义约定：
//   - Params.Temperature == 0 视作"不发送"（交由服务端默认值）；MaxTokens 同理，
//     两者与 Budget.MaxOutputTokens 冲突时以 Budget 为准。
//   - stream_options.include_usage 总是请求；思考参数 reasoning_effort / enable_thinking 按
//     Sampling 发送。服务端点名不认的参数（400）**同请求剥离重试一次**，成功后经
//     NoteUnsupported 记录进 config `model.unsupported_params`（D34，重启后直接省略）。
//   - 流末尾 usage 映射为 conversation.Usage（CostUSD 恒为 0：适配器不掌握单价，
//     成本核算由装饰器/上层按 ModelInfo 计算，D14）。
//   - 请求时长由调用方 ctx 控制，故默认 http.Client 不设 Timeout（流式不能设整体超时）。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/adapter/llm/tokenizer"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const chatPath = "/chat/completions"
const modelsPath = "/models"

var _ port.LLM = (*Client)(nil)

// Config 适配器配置。
type Config struct {
	BaseURL string // 如 https://api.openai.com/v1（末尾斜杠可省）
	APIKey  string // 空 = 不带 Authorization（本地服务）
	// Tokenizer 本地 tokenizer.json 路径（文件或目录；空 = 不启用精确计数，
	// app 回落通用估算——三级计数链 D26②/③）。
	Tokenizer string
	// UnsupportedParams 启动注入的"服务端已知不认"参数名（D34：由上一次 400 剥离
	// 成功后记录进 config，重启生效）；命中者编码时直接省略。
	UnsupportedParams []string
	// NoteUnsupported 剥离成功后的记录回调（装配根写回 config `model.unsupported_params`）；
	// nil = 只在本进程内记住。
	NoteUnsupported func(field string)
	// HTTPClient 为 nil 时使用无整体超时的默认客户端（由 ctx 控制时长）。
	HTTPClient *http.Client
}

// Client OpenAI 兼容生成客户端。
type Client struct {
	base string
	key  string
	hc   *http.Client
	tok  *tokenizer.Tokenizer // 配置了本地 tokenizer 时非 nil（D26②）
	mu   sync.Mutex           // 保护 omit（记录回调与编码并发取快照）
	omit map[string]bool      // 服务端已知不认的参数（启动注入 + 本次运行记录，D34）
	note func(field string)
}

var _ port.TokenCounter = (*Client)(nil)

// New 创建客户端并校验 BaseURL；配置了 Tokenizer 则加载（路径错误 fail-fast，
// 绝不静默降级成估算）。
func New(cfg Config) (*Client, error) {
	raw := strings.TrimSpace(cfg.BaseURL)
	if raw == "" {
		return nil, errors.New("llm: base_url 为空")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("llm: 非法 base_url %q", cfg.BaseURL)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	c := &Client{
		base: strings.TrimRight(raw, "/"),
		key:  strings.TrimSpace(cfg.APIKey),
		hc:   hc,
		omit: make(map[string]bool, len(cfg.UnsupportedParams)),
		note: cfg.NoteUnsupported,
	}
	for _, f := range cfg.UnsupportedParams {
		if f = strings.TrimSpace(f); f != "" {
			c.omit[f] = true
		}
	}
	if p := strings.TrimSpace(cfg.Tokenizer); p != "" {
		tok, terr := tokenizer.Load(p)
		if terr != nil {
			return nil, terr
		}
		c.tok = tok
	}
	return c, nil
}

// CountTokens port.TokenCounter（三级计数链②，D26）：本地 tokenizer 精确计数。
// 未配置时报错——app 据此回落通用估算③。
func (c *Client) CountTokens(_ context.Context, text string) (int, error) {
	if c.tok == nil {
		return 0, errors.New("llm: 未配置 model.tokenizer（回落通用估算）")
	}
	return c.tok.Count(text), nil
}

// Generate 发起流式生成，返回 SSE 生成流。
func (c *Client) Generate(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	if ctx == nil {
		return nil, errors.New("llm: nil context")
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, errors.New("llm: model 为空")
	}
	body, err := encodeChatRequest(req, true, c.omitCopy())
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)

	resp, err := c.post(streamCtx, chatPath, body)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		code := resp.StatusCode
		sn := readSnippet(resp.Body)
		_ = resp.Body.Close()
		resp = nil
		// D34：服务端点名不认的参数 → 本次剥离重试，成功后记录（装配根写回 config，
		// 以后启动直接省略）。stream_options 的兼容回退并入同一机制（历史口径：报错
		// 片段出现该名即剥，各家措辞不一）。
		if stripped := unsupportedFields(sn, req, true); len(stripped) > 0 {
			omit := c.omitCopy()
			for _, f := range stripped {
				omit[f] = true
			}
			include := !containsStripped(stripped, "stream_options")
			if plain, e := encodeChatRequest(req, include, omit); e == nil {
				resp2, e := c.post(streamCtx, chatPath, plain)
				if e != nil {
					// 第二次 post 的传输层错误不得被吞成首轮状态码（审查修复：
					// 瞬时断连会误判为 400 非瞬时，装饰器层就不再重试）——
					// 按 post 口径上抛（含 ErrTransient 标注）。
					cancel()
					return nil, e
				}
				if resp2.StatusCode == http.StatusOK {
					resp = resp2
					c.markUnsupported(stripped...)
				} else {
					code = resp2.StatusCode
					sn = readSnippet(resp2.Body)
					_ = resp2.Body.Close()
				}
			}
		}
		if resp == nil {
			cancel()
			if transientStatus(code) {
				// §10：429/408/5xx 标注瞬时错误，供装配根的重试装饰器限次退避。
				return nil, fmt.Errorf("llm: POST %s%s: 状态码 %d: %s: %w", c.base, chatPath, code, sn, port.ErrTransient)
			}
			return nil, fmt.Errorf("llm: POST %s%s: 状态码 %d: %s", c.base, chatPath, code, sn)
		}
	}
	return newStream(streamCtx, cancel, resp.Body), nil
}

// unsupportedFields 判定报错片段点名了哪些本次实际发送、但服务端不认的参数（D34）。
// 思考参数要求"字段名 + 未知参数措辞"同时出现——枚举值错误
// （如 "invalid value 'x' for reasoning_effort"）不匹配，照常报错、不掩盖配置问题；
// stream_options 保留历史的"仅出现即剥"口径（各家报错措辞差异大）。
func unsupportedFields(sn string, req port.GenerateRequest, includeUsage bool) []string {
	var out []string
	if includeUsage && strings.Contains(sn, "stream_options") {
		out = append(out, "stream_options")
	}
	if !unknownParamPhrase(sn) {
		return out
	}
	if req.Params.ReasoningEffort != "" && strings.Contains(sn, "reasoning_effort") {
		out = append(out, "reasoning_effort")
	}
	if req.Params.Thinking != nil && strings.Contains(sn, "enable_thinking") {
		out = append(out, "enable_thinking")
	}
	return out
}

// unknownParamPhrase 报错是否带"未知/不支持参数"措辞（中英常见措辞）。
func unknownParamPhrase(sn string) bool {
	s := strings.ToLower(sn)
	for _, kw := range []string{
		"unknown", "unrecognized", "unsupported", "not supported",
		"unexpected", "extra input", "not permitted", "not allowed",
		"不支持", "未支持", "无法识别",
	} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// containsStripped 剥离清单内是否含某字段。
func containsStripped(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}

// omitCopy 剥离名单快照（map 只在锁内增删，读侧拿副本，避免与记录回调竞争）。
func (c *Client) omitCopy() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]bool, len(c.omit))
	for k, v := range c.omit {
		out[k] = v
	}
	return out
}

// markUnsupported 剥离成功后登记新字段（进程内记忆 + 回调写回 config，D34）。
func (c *Client) markUnsupported(fields ...string) {
	c.mu.Lock()
	var fresh []string
	for _, f := range fields {
		if !c.omit[f] {
			c.omit[f] = true
			fresh = append(fresh, f)
		}
	}
	c.mu.Unlock()
	if c.note != nil {
		for _, f := range fresh {
			c.note(f)
		}
	}
}

// Models 拉取 /models 列表；能力与单价未知，仅填 Name（DESIGN §5.1）。
func (c *Client) Models(ctx context.Context) ([]port.ModelInfo, error) {
	if ctx == nil {
		return nil, errors.New("llm: nil context")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+modelsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("llm: 构造请求: %w", err)
	}
	c.authorize(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: GET %s: %w", modelsPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: GET %s: 状态码 %d: %s", modelsPath, resp.StatusCode, readSnippet(resp.Body))
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("llm: 解析 /models: %w", err)
	}
	out := make([]port.ModelInfo, 0, len(payload.Data))
	for _, d := range payload.Data {
		if d.ID != "" {
			out = append(out, port.ModelInfo{Name: d.ID})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// post 发送 POST 请求并附加公共头。
func (c *Client) post(ctx context.Context, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm: 构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	c.authorize(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		// 传输层失败（连接拒绝/超时/DNS 抖动）视为瞬时；ctx 已取消的不算（§10）。
		if ctx.Err() == nil {
			return nil, fmt.Errorf("llm: POST %s: %w: %w", path, err, port.ErrTransient)
		}
		return nil, fmt.Errorf("llm: POST %s: %w", path, err)
	}
	return resp, nil
}

// transientStatus 可重试的 HTTP 状态（限流/请求超时/服务端错误）。
func transientStatus(code int) bool {
	return code == http.StatusTooManyRequests ||
		code == http.StatusRequestTimeout ||
		code >= http.StatusInternalServerError
}

// authorize 附加 Authorization 头（有密钥才发）。
func (c *Client) authorize(req *http.Request) {
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
}

// ---------------------------------------------------------------------------
// 请求体编码
// ---------------------------------------------------------------------------

type chatRequest struct {
	Model         string             `json:"model"`
	Messages      []chatMessage      `json:"messages"`
	Stream        bool               `json:"stream"`
	StreamOptions *chatStreamOptions `json:"stream_options,omitempty"`
	Tools         []chatTool         `json:"tools,omitempty"`
	Temperature   float64            `json:"temperature,omitempty"` // 0 = 不发送
	MaxTokens     int                `json:"max_tokens,omitempty"`  // 0 = 不发送
	Stop          []string           `json:"stop,omitempty"`
	// ReasoningEffort 推理档位（D34；空 = 不发送）。
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// EnableThinking 思考开关（D34；dashscope 系）；nil = 不发送。
	EnableThinking *bool `json:"enable_thinking,omitempty"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"` // string 或 []chatContentPart；nil 时省略
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatContentPart struct {
	Type     string        `json:"type"` // "text" | "image_url"
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"` // data:<mime>;base64,<...>
}

type chatToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // 恒为 "function"
	Function chatFuncCall `json:"function"`
}

type chatFuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // 原始 JSON 字符串（模型产出）
}

type chatTool struct {
	Type     string       `json:"type"` // 恒为 "function"
	Function chatToolFunc `json:"function"`
}

type chatToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// encodeChatRequest 把 GenerateRequest 编码为 SSE 流式请求体。
// includeUsage=false 时省略 stream_options（兼容性回退用）；omit 命中的参数一律不发
// （D34：服务端已知不认的字段，启动注入或本次 400 记录）。nil omit 不省略。
func encodeChatRequest(req port.GenerateRequest, includeUsage bool, omit map[string]bool) ([]byte, error) {
	out := chatRequest{
		Model:    req.Model,
		Stream:   true,
		Messages: make([]chatMessage, 0, len(req.Messages)),
	}
	if includeUsage && !omit["stream_options"] {
		out.StreamOptions = &chatStreamOptions{IncludeUsage: true}
	}
	for _, m := range req.Messages {
		cm, err := toChatMessage(m)
		if err != nil {
			return nil, err
		}
		if cm.Role == "" {
			continue // toChatMessage 对"空内容且无调用"的消息返回零值表示跳过
		}
		out.Messages = append(out.Messages, cm)
	}
	if len(req.Tools) > 0 {
		out.Tools = toChatTools(req.Tools)
	}
	out.Temperature = req.Params.Temperature
	out.Stop = req.Params.Stop
	if req.Params.ReasoningEffort != "" && !omit["reasoning_effort"] {
		out.ReasoningEffort = req.Params.ReasoningEffort
	}
	if req.Params.Thinking != nil && !omit["enable_thinking"] {
		out.EnableThinking = req.Params.Thinking
	}
	// 本轮生成预算优先于采样参数（DESIGN §5.1 Budget）。
	out.MaxTokens = req.Budget.MaxOutputTokens
	if out.MaxTokens == 0 {
		out.MaxTokens = req.Params.MaxTokens
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("llm: 编码请求体: %w", err)
	}
	return data, nil
}

// toChatMessage 转换单条消息。
func toChatMessage(m port.PromptMessage) (chatMessage, error) {
	cm := chatMessage{Role: m.Role}
	switch m.Role {
	case "tool":
		cm.ToolCallID = m.CallID
		cm.Content = flattenText(m.Content)
		return cm, nil
	case "assistant":
		if len(m.ToolCalls) > 0 {
			for _, call := range m.ToolCalls {
				args := strings.TrimSpace(string(call.Args))
				if args == "" {
					args = "{}"
				}
				cm.ToolCalls = append(cm.ToolCalls, chatToolCall{
					ID:       string(call.ID),
					Type:     "function",
					Function: chatFuncCall{Name: call.Name, Arguments: args},
				})
			}
			if text := flattenText(m.Content); text != "" {
				cm.Content = text
			}
			return cm, nil
		}
	}
	content, err := buildContent(m.Content)
	if err != nil {
		return chatMessage{}, err
	}
	if s, ok := content.(string); ok && s == "" && m.Role != "tool" {
		// 空内容消息：不发（调用方已跳过，这里再兜一层）。
		return chatMessage{}, nil
	}
	cm.Content = content
	return cm, nil
}

// buildContent 全文本 → 纯字符串（最大兼容）；含图片 → content 分片数组（data URL 内联）。
func buildContent(parts []port.PromptPart) (any, error) {
	hasImage := false
	for _, p := range parts {
		if p.Kind == "image" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return flattenText(parts), nil
	}
	out := make([]chatContentPart, 0, len(parts))
	for _, p := range parts {
		if p.Kind == "image" {
			if len(p.Data) == 0 {
				return nil, errors.New("llm: 图片片段缺少数据（AttachmentStore 未提供字节）")
			}
			mime := p.MIME
			if mime == "" {
				mime = "image/png"
			}
			out = append(out, chatContentPart{
				Type:     "image_url",
				ImageURL: &chatImageURL{URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(p.Data)},
			})
			continue
		}
		out = append(out, chatContentPart{Type: "text", Text: p.Text})
	}
	return out, nil
}

// flattenText 拼接文本片段。
func flattenText(parts []port.PromptPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == "image" || p.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

// toChatTools 工具声明转换；空 Schema 补默认对象结构（部分服务强制要求 parameters）。
func toChatTools(specs []tool.Spec) []chatTool {
	out := make([]chatTool, 0, len(specs))
	for _, s := range specs {
		params := s.Schema
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, chatTool{
			Type: "function",
			Function: chatToolFunc{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  params,
			},
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// 流式解析
// ---------------------------------------------------------------------------

// stream SSE 生成流：逐行解析 data: 分片，[DONE] 或连接关闭 = 正常结束（io.EOF）。
type stream struct {
	ctx     context.Context
	cancel  context.CancelFunc
	body    io.ReadCloser
	scanner *bufio.Scanner
	done    bool
}

func newStream(ctx context.Context, cancel context.CancelFunc, body io.ReadCloser) *stream {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20) // 单条 SSE 分片上限 16MB
	return &stream{ctx: ctx, cancel: cancel, body: body, scanner: sc}
}

// Recv 返回下一个增量；io.EOF 表示正常结束。
func (s *stream) Recv() (port.Delta, error) {
	if s.done {
		return port.Delta{}, io.EOF
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue // 空行/注释
		}
		if !strings.HasPrefix(line, "data:") {
			continue // event:/id: 等行忽略
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			s.done = true
			return port.Delta{}, io.EOF
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return port.Delta{}, fmt.Errorf("llm: 解析流分片: %w（片段: %s）", err, snippet(payload, 200))
		}
		if chunk.Error != nil {
			return port.Delta{}, fmt.Errorf("llm: 服务端流式错误: %s", chunk.Error.Message)
		}
		d := chunk.toDelta()
		if d.Text == "" && len(d.ToolCalls) == 0 && d.Usage == nil {
			continue // finish_reason 等无增量分片
		}
		return d, nil
	}
	s.done = true
	if err := s.scanner.Err(); err != nil {
		// 父 ctx 取消优先报告 ctx 错误（连接层错误对调用方无意义）。
		if cerr := s.ctx.Err(); cerr != nil {
			return port.Delta{}, cerr
		}
		return port.Delta{}, fmt.Errorf("llm: 读取生成流: %w", err)
	}
	// 未见 [DONE] 即断流：多数兼容服务直接关连接，视作正常结束。
	return port.Delta{}, io.EOF
}

// Close 中断生成（等价 ctx 取消）。
func (s *stream) Close() error {
	s.done = true
	s.cancel()
	return s.body.Close()
}

// chatChunk SSE data 载荷（OpenAI chat.completion.chunk）。
type chatChunk struct {
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage"`
	Error   *chatError   `json:"error"`
}

type chatChoice struct {
	Delta struct {
		Content   string          `json:"content"`
		ToolCalls []chatCallDelta `json:"tool_calls"`
	} `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

type chatCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type chatError struct {
	Message string `json:"message"`
}

// toDelta 映射为端口增量（Index 分片聚合交给 app 层）。
func (ch chatChunk) toDelta() port.Delta {
	var d port.Delta
	if len(ch.Choices) > 0 {
		delta := ch.Choices[0].Delta
		d.Text = delta.Content
		for _, tc := range delta.ToolCalls {
			d.ToolCalls = append(d.ToolCalls, port.ToolCallDelta{
				Index:     tc.Index,
				ID:        tc.ID,
				Name:      tc.Function.Name,
				ArgsDelta: tc.Function.Arguments,
			})
		}
	}
	if ch.Usage != nil {
		u := conversation.Usage{
			InputTokens:  ch.Usage.PromptTokens,
			OutputTokens: ch.Usage.CompletionTokens,
		}
		d.Usage = &u
	}
	return d
}

// readSnippet 读取错误响应摘要并关闭 body。
func readSnippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 4<<10))
	return snippet(string(b), 500)
}

// snippet 截断字符串（按 rune，避免切坏 UTF-8）。
func snippet(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
