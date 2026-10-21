package llm

// 能力探测（D174）：对单个模型发一条最小请求，判断它到底收不收得下某项能力。
//
// 三条原则：
//   1. 探测**不**复用 Generate 的「剥离重试」路径——那会把「服务端点名拒绝」洗成一次
//      成功请求，探测就永远得不出「不支持」。
//   2. 只认正面证据与点名拒绝；其余（超时、5xx、静默忽略）一律无结论。宁可少写一位，
//      也不要把能用说成不能用。
//   3. 探测是只读的：不记录 unsupported_params（那是正式请求的事），不改配置。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.Prober = (*Client)(nil)

// probeMaxTokens 探测请求的输出上限：只要模型开个头就能判定，不必让它写作文。
//
// 别压得太狠：思考型模型会先把额度花在思考上，正文还没开始就被截断（`finish_reason`
// = "length"），于是「其实答上来了」被洗成空正文。64 曾在实测里正好踩中这种情况——
// 思考吃掉全部 64 tokens，正文为空，结论只能落成「无结论」。
const probeMaxTokens = 256

// probeVisionColor 探测图的主色（判定基准）。纯红：几乎所有视觉模型都会一口答对，
// 不会像浅色/灰那样答成 "light gray" 这种无法判定的词。
const probeVisionColor = "red"

// probeImageOnce 探测图只生成一次（多项探测共用同一张）。
var (
	probeImageOnce sync.Once
	probeImageData []byte
	probeImageErr  error
)

// probeImage 探测用图片：64×64 纯红 PNG。**在代码里画**而不是塞一段 base64——
// 图片内容必须与 probeVisionColor 一致，写死的字节没人能复核，改颜色时也不会报错。
func probeImage() ([]byte, error) {
	probeImageOnce.Do(func() {
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		draw.Draw(img, img.Bounds(),
			image.NewUniform(color.RGBA{R: 0xff, A: 0xff}), image.Point{}, draw.Src)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			probeImageErr = fmt.Errorf("llm: 生成探测图片: %w", err)
			return
		}
		probeImageData = buf.Bytes()
	})
	return probeImageData, probeImageErr
}

// probeText 探测用的用户消息：模型侧文案，一律英文（AGENTS：进入模型上下文用英文）。
const (
	probeToolsPrompt = "Call the probe_ping tool now. Do not reply with text."
	// probeVisionPrompt 要求一个词的颜色答案：能答对 = 真解码了图，不只是收下了字段。
	probeVisionPrompt    = "What color is this image? Answer with one word."
	probeReasoningPrompt = "Think briefly, then answer with the single word: ok"
)

// probeToolName 探测工具名：刻意与内置工具区分，避免模型凭记忆乱调。
const probeToolName = "probe_ping"

// Probe port.Prober：按能力构造最小请求并判定三态结论。
//
// 一项能力可能有**多种问法**：思考参数有 `reasoning_effort` 与 `enable_thinking` 两套
// （生态各认一套），一发全带上，端点拒绝任一个都会被判成「不支持思考」——可实际上另
// 一套可能正好能用。所以逐问法各试一次，再合并结论。
func (c *Client) Probe(ctx context.Context, model, capability string) (port.ProbeResult, error) {
	if ctx == nil {
		return port.ProbeResult{}, errors.New("llm: nil context")
	}
	if strings.TrimSpace(model) == "" {
		return port.ProbeResult{}, errors.New("llm: model 为空")
	}
	attempts, err := probeAttempts(model, capability)
	if err != nil {
		return port.ProbeResult{}, err
	}
	start := time.Now()
	outcomes := make([]probeOutcome, 0, len(attempts))
	for _, a := range attempts {
		out, err := c.probeOnce(ctx, capability, a)
		if err != nil {
			return port.ProbeResult{}, err
		}
		outcomes = append(outcomes, out)
	}
	merged := mergeProbeOutcomes(outcomes)
	res := port.ProbeResult{
		Capability: capability,
		Verdict:    merged.verdict,
		Reason:     merged.reason,
		Elapsed:    time.Since(start),
	}
	for _, o := range outcomes {
		res.Usage.InputTokens += o.usage.InputTokens
		res.Usage.OutputTokens += o.usage.OutputTokens
	}
	return res, nil
}

// probeAttempt 一次探测请求：label 说明这一发用的是什么问法（合并结论时要指出来）。
type probeAttempt struct {
	label string
	req   port.GenerateRequest
}

// probeOutcome 单次请求的判定与用量（合并前）。
type probeOutcome struct {
	label   string
	verdict port.ProbeVerdict
	reason  string
	usage   conversation.Usage
}

// probeOnce 发一次探测请求并判定。
func (c *Client) probeOnce(ctx context.Context, capability string, a probeAttempt) (probeOutcome, error) {
	out := probeOutcome{label: a.label}
	finish := func(v port.ProbeVerdict, reason string) (probeOutcome, error) {
		out.verdict, out.reason = v, reason
		return out, nil
	}
	body, err := encodeProbeRequest(a.req)
	if err != nil {
		return probeOutcome{}, err
	}
	resp, err := c.post(ctx, chatPath, body)
	if err != nil {
		if ctx.Err() != nil {
			return probeOutcome{}, ctx.Err() // 调用方自己撤的，不是探测结论
		}
		return finish(port.ProbeInconclusive, "请求没发出去："+err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		sn := readSnippet(resp.Body)
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return finish(port.ProbeInconclusive, fmt.Sprintf("鉴权失败（%d），换个密钥或检查 base_url 后重试：%s", resp.StatusCode, sn))
		case probeRejected(capability, sn):
			return finish(port.ProbeUnsupported, fmt.Sprintf("服务端拒绝（%d）：%s", resp.StatusCode, sn))
		default:
			return finish(port.ProbeInconclusive, fmt.Sprintf("服务端报错（%d）但没点名该项能力：%s", resp.StatusCode, sn))
		}
	}

	var payload probeResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return finish(port.ProbeInconclusive, "响应无法解析："+err.Error())
	}
	if payload.Usage != nil {
		out.usage = conversation.Usage{
			InputTokens:  payload.Usage.PromptTokens,
			OutputTokens: payload.Usage.CompletionTokens,
		}
	}
	if len(payload.Choices) == 0 {
		return finish(port.ProbeInconclusive, "响应里没有 choices")
	}
	msg := payload.Choices[0].Message
	switch capability {
	case port.CapTools:
		if len(msg.ToolCalls) > 0 {
			return finish(port.ProbeSupported, fmt.Sprintf("模型发起了工具调用（%s）", msg.ToolCalls[0].Function.Name))
		}
		return finish(port.ProbeInconclusive, "请求被接受，但模型没发起工具调用（可能只是没照着做）")
	case port.CapVision:
		text := strings.TrimSpace(msg.text())
		reason := strings.TrimSpace(msg.reasoning())
		switch visionVerdict(text) {
		case port.ProbeSupported:
			return finish(port.ProbeSupported, fmt.Sprintf("带图请求答对了颜色（%s）", snippet(text, 40)))
		case port.ProbeUnsupported:
			return finish(port.ProbeUnsupported, fmt.Sprintf("带图请求拿到了「%s」，不是图里的%s——图没被真正解码",
				snippet(text, 40), probeVisionColor))
		}
		// 正文没给出可判定的答案（含空正文）：退而看**否定证据**。思考型模型常把额度
		// 花在思考上、正文只剩一句 "Unknown"；而「图没送到」这句话多半出现在思考里。
		// 只采信「看不到 / 复述占位文本」这类否定说法——思考里蹦出个颜色词不能算答对。
		if visionSaysBlind(text) || visionSaysPlaceholder(text) {
			return finish(port.ProbeUnsupported,
				fmt.Sprintf("模型在回复里说图没送到（%s）——服务端把图片换成了占位文本",
					snippet(text, 60)))
		}
		if visionSaysBlind(reason) || visionSaysPlaceholder(reason) {
			return finish(port.ProbeUnsupported,
				fmt.Sprintf("模型在思考里说图没送到（%s）——服务端把图片换成了占位文本",
					snippet(reason, 60)))
		}
		if text == "" {
			if payload.Choices[0].FinishReason == "length" {
				return finish(port.ProbeInconclusive, "输出额度被思考用尽，没留下正文（提高额度后重试）")
			}
			return finish(port.ProbeInconclusive, "请求被接受，但回复为空")
		}
		return finish(port.ProbeInconclusive, fmt.Sprintf("回复没给出可判定的颜色：%s", snippet(text, 40)))
	case port.CapReasoning:
		if strings.TrimSpace(msg.reasoning()) != "" {
			return finish(port.ProbeSupported, fmt.Sprintf("按「%s」问法拿到了思考内容", a.label))
		}
		return finish(port.ProbeInconclusive, fmt.Sprintf("「%s」被接受但没给出思考内容（可能被静默忽略）", a.label))
	}
	return probeOutcome{}, fmt.Errorf("llm: 未知能力 %q", capability)
}

// mergeProbeOutcomes 合并同一能力的多次问法（D174 口径一：判定必须站得住）。
//
//   - 任一问法拿到正面证据 → 支持（能出思考就算有这项能力，别管是哪个参数起的作用）；
//   - 所有问法都被点名拒绝 → 不支持；
//   - 其余 → 无结论。宁可这轮白花几条 token，也不要把「一半参数不认」判成「没有这项能力」。
func mergeProbeOutcomes(outs []probeOutcome) probeOutcome {
	if len(outs) == 0 {
		return probeOutcome{verdict: port.ProbeInconclusive, reason: "没有可用的问法"}
	}
	if len(outs) == 1 {
		return outs[0]
	}
	supported, rejected := -1, 0
	reasons := make([]string, 0, len(outs))
	for i, o := range outs {
		switch o.verdict {
		case port.ProbeSupported:
			if supported < 0 {
				supported = i
			}
		case port.ProbeUnsupported:
			rejected++
		}
		reasons = append(reasons, fmt.Sprintf("%s：%s", o.label, o.reason))
	}
	switch {
	case supported >= 0:
		return probeOutcome{verdict: port.ProbeSupported, reason: outs[supported].reason}
	case rejected == len(outs):
		return probeOutcome{verdict: port.ProbeUnsupported, reason: strings.Join(reasons, "；")}
	default:
		return probeOutcome{verdict: port.ProbeInconclusive, reason: strings.Join(reasons, "；")}
	}
}

// probeAttempts 构造某项能力的全部问法。
func probeAttempts(model, capability string) ([]probeAttempt, error) {
	text := func(s string) []port.PromptPart {
		return []port.PromptPart{{Kind: "text", Text: s}}
	}
	base := func() port.GenerateRequest {
		return port.GenerateRequest{Model: model, Budget: port.TokenBudget{MaxOutputTokens: probeMaxTokens}}
	}
	switch capability {
	case port.CapTools:
		req := base()
		req.Messages = []port.PromptMessage{{Role: "user", Content: text(probeToolsPrompt)}}
		req.Tools = []tool.Spec{{
			Name:        probeToolName,
			Description: "Probe tool: takes no arguments, returns nothing. Call it to acknowledge.",
			Schema:      tool.NoArgsSchema,
		}}
		return []probeAttempt{{label: "工具清单", req: req}}, nil
	case port.CapVision:
		img, err := probeImage()
		if err != nil {
			return nil, err
		}
		req := base()
		req.Messages = []port.PromptMessage{{Role: "user", Content: []port.PromptPart{
			{Kind: "text", Text: probeVisionPrompt},
			{Kind: "image", MIME: "image/png", Data: img},
		}}}
		return []probeAttempt{{label: "带图请求", req: req}}, nil
	case port.CapReasoning:
		// 两套参数分别试：生态里各认一套（reasoning_effort 一派、enable_thinking 一派），
		// 一起发的话，写死的那一方被拒就会把能用的那方一起否掉。
		effort := base()
		effort.Messages = []port.PromptMessage{{Role: "user", Content: text(probeReasoningPrompt)}}
		effort.Params.ReasoningEffort = "low"
		on := true
		thinking := base()
		thinking.Messages = []port.PromptMessage{{Role: "user", Content: text(probeReasoningPrompt)}}
		thinking.Params.Thinking = &on
		return []probeAttempt{
			{label: "reasoning_effort", req: effort},
			{label: "enable_thinking", req: thinking},
		}, nil
	default:
		return nil, fmt.Errorf("llm: 未知能力 %q", capability)
	}
}

// encodeProbeRequest 探测请求体：非流式（一次读全，省掉 SSE 状态机），且不带
// stream_options（有些服务端点名不认它，探测不该被它的兼容问题带偏）。
func encodeProbeRequest(req port.GenerateRequest) ([]byte, error) {
	body, err := encodeChatRequest(req, false, nil)
	if err != nil {
		return nil, err
	}
	var generic map[string]any
	if err := json.Unmarshal(body, &generic); err != nil {
		return nil, fmt.Errorf("llm: 探测请求体解析: %w", err)
	}
	generic["stream"] = false
	delete(generic, "stream_options")
	return json.Marshal(generic)
}

// visionVerdict 视觉探测的判定：回复里认出了图里的颜色 = 支持；说成别的颜色、或明说
// 看不到图 = 不支持（端点收下了 image_url 却没真解码）；其余 = 无结论。
//
// 关键词按「英文/中文 + 词边界」匹配，不做语义理解：redness、colored 这类词不算答对，
// 否则一句 "I cannot see any red" 会被判成支持。
func visionVerdict(text string) port.ProbeVerdict {
	if text == "" {
		return port.ProbeInconclusive
	}
	// 「明说看不到」优先于颜色词：一句 "I cannot see any red" 里有 red，但它恰恰是没看到。
	if visionSaysBlind(text) {
		return port.ProbeUnsupported
	}
	saysRed := hasWord(text, probeVisionColor) || hasWord(text, "红色") || hasWord(text, "红")
	saysOther := visionSaysWrongColor(text)
	switch {
	case saysRed && !saysOther:
		return port.ProbeSupported
	case saysOther && !saysRed:
		// 答成别的颜色：说明它「看到了什么」，但不是这张图里的东西。
		return port.ProbeUnsupported
	case saysRed && saysOther:
		// 既提到目标色又提到别的色（"it's red, not blue" / "not red, it's blue"）：
		// 词面上无解，不必猜——无结论比猜错安全。
		return port.ProbeInconclusive
	}
	return port.ProbeInconclusive
}

// visionBlindPhrases 明确「看不到图」的说法（这比答错颜色更确定：它连图都没收到）。
var visionBlindPhrases = []string{
	"cannot see", "can't see", "can not see", "unable to see", "no image", "not an image",
	"no image provided", "image unsupported", "unsupported image", "without an image",
	"看不到", "看不见", "无法看到", "没有图", "不是图片", "未提供图片", "图片不支持",
}

// visionSaysBlind 回复是否明说看不到图片。
func visionSaysBlind(text string) bool {
	t := strings.ToLower(text)
	for _, p := range visionBlindPhrases {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

// visionPlaceholderPhrases 服务端用来顶替图片的占位文本（端点收下了请求，但把图片
// 换成了这串字递给模型——思考里往往会原样复述出来）。这是「图没送到」的直接证据。
var visionPlaceholderPhrases = []string{
	"[unsupported image]", "<unsupported image>", "[image omitted]", "<image omitted>",
	"[image not supported]", "unsupported image type",
}

// visionSaysPlaceholder 回复是否复述了服务端的图片占位文本。
func visionSaysPlaceholder(text string) bool {
	t := strings.ToLower(text)
	for _, p := range visionPlaceholderPhrases {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

// visionOtherColors 其它常见颜色词（答成这些 = 明显没看到这张图）。
var visionOtherColors = []string{
	"blue", "green", "yellow", "black", "white", "gray", "grey", "orange", "purple",
	"pink", "brown", "cyan", "magenta", "blue",
	"蓝", "绿", "黄", "黑", "白", "灰", "橙", "紫", "粉", "棕", "青",
}

// visionSaysWrongColor 回复是否明确说出了别的颜色。
func visionSaysWrongColor(text string) bool {
	for _, c := range visionOtherColors {
		if hasWord(text, c) {
			return true
		}
	}
	return false
}

// hasWord 词边界匹配（大小写不敏感）。中文没有词边界，这里退化为子串匹配——
// 颜色词短，误判风险可接受（"红" 出现在 "红色" 里正是我们想要的）。
func hasWord(text, word string) bool {
	t := strings.ToLower(text)
	w := strings.ToLower(word)
	if !isASCIIWord(w) {
		return strings.Contains(t, w)
	}
	idx := 0
	for {
		i := strings.Index(t[idx:], w)
		if i < 0 {
			return false
		}
		start := idx + i
		end := start + len(w)
		beforeOK := start == 0 || !isWordByte(t[start-1])
		afterOK := end == len(t) || !isWordByte(t[end])
		if beforeOK && afterOK {
			return true
		}
		idx = end
	}
}

// isASCIIWord 是否纯 ASCII 字母（决定用词边界还是子串匹配）。
func isASCIIWord(s string) bool {
	for _, r := range s {
		if r > 127 || (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return s != ""
}

// isWordByte ASCII 单词字符（字母数字下划线）。
func isWordByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// probeRejected 服务端报错正文是否点名了该项能力（= 明确的「不支持」证据）。// 判定口径按能力分开：工具看 tool/function，图片看 image/vision，思考看参数名。
func probeRejected(capability, snippet string) bool {
	s := strings.ToLower(snippet)
	switch capability {
	case port.CapTools:
		return strings.Contains(s, "tool") || strings.Contains(s, "function")
	case port.CapVision:
		return strings.Contains(s, "image") || strings.Contains(s, "vision") || strings.Contains(s, "图片")
	case port.CapReasoning:
		// 参数名 + 「未知/不支持」措辞同时出现才算——单纯枚举值错误不是能力问题。
		if !unknownParamPhrase(snippet) {
			return false
		}
		return strings.Contains(s, "reasoning_effort") || strings.Contains(s, "enable_thinking")
	}
	return false
}

// probeResponse 非流式响应里探测关心的字段（其余一概忽略）。
type probeResponse struct {
	Choices []struct {
		Message      probeMessage `json:"message"`
		FinishReason string       `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// probeMessage 响应里的 assistant 消息（只取探测用得上的几项）。
type probeMessage struct {
	Content          any             `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	Reasoning        string          `json:"reasoning"` // vLLM / GPT-OSS 键名（D42）
	ToolCalls        []probeToolCall `json:"tool_calls"`
}

// probeToolCall 工具调用（只要名字，用于把「支持」的理由写具体些）。
type probeToolCall struct {
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

// text 正文（部分服务把 content 写成数组，这里只取字符串形态）。
func (m probeMessage) text() string {
	switch v := m.Content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, part := range v {
			if mm, ok := part.(map[string]any); ok {
				if s, ok := mm["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
	}
	return ""
}

// reasoning 思考内容（两种生态键名取其一）。
func (m probeMessage) reasoning() string {
	if m.ReasoningContent != "" {
		return m.ReasoningContent
	}
	return m.Reasoning
}
