package llm

// 能力探测（D174）的契约测试：三条最小请求各自怎么判定、以及「探不通 ≠ 不支持」。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// probeServer 起一个假服务：固定的状态码与响应体，记录**每次**请求体
// （思考探测会发两发，只看最后一发会误判）。
func probeServer(t *testing.T, status int, reply string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != chatPath {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// probeBody 标准 200 响应体（choices + usage）。
func probeBody(message string) string {
	return `{"choices":[{"message":` + message + `}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`
}

func TestProbeToolsSupported(t *testing.T) {
	srv, got := probeServer(t, http.StatusOK, probeBody(`{"content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"probe_ping","arguments":"{}"}}]}`))
	c, _ := New(Config{BaseURL: srv.URL})
	res, err := c.Probe(context.Background(), "m", port.CapTools)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != port.ProbeSupported {
		t.Fatalf("verdict = %v, reason = %s", res.Verdict, res.Reason)
	}
	if res.Capability != port.CapTools || res.Usage.InputTokens != 7 || res.Usage.OutputTokens != 3 {
		t.Fatalf("result = %+v", res)
	}
	if last := (*got)[len(*got)-1]; last["stream"] != false {
		t.Fatalf("探测应走非流式: %+v", last)
	}
	last := (*got)[len(*got)-1]
	if last["stream"] != false {
		t.Fatalf("探测应走非流式: %+v", last)
	}
	if _, has := last["stream_options"]; has {
		t.Fatal("探测不应发送 stream_options")
	}
	tools, _ := last["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %+v", last["tools"])
	}
}

// 模型被接受请求但只回文本 = 无结论（不能因此把工具位关掉）。
func TestProbeToolsSilentIsInconclusive(t *testing.T) {
	srv, _ := probeServer(t, http.StatusOK, probeBody(`{"content":"I cannot call tools."}`))
	c, _ := New(Config{BaseURL: srv.URL})
	res, err := c.Probe(context.Background(), "m", port.CapTools)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != port.ProbeInconclusive {
		t.Fatalf("verdict = %v", res.Verdict)
	}
}

// 400 点名工具 = 明确不支持。
func TestProbeToolsRejected(t *testing.T) {
	srv, _ := probeServer(t, http.StatusBadRequest, `{"error":{"message":"tools is not supported by this model"}}`)
	c, _ := New(Config{BaseURL: srv.URL})
	res, err := c.Probe(context.Background(), "m", port.CapTools)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != port.ProbeUnsupported {
		t.Fatalf("verdict = %v, reason = %s", res.Verdict, res.Reason)
	}
}

// TestProbeImageIsSolidRed 探测图必须真的是 probeVisionColor 指定的颜色：判定基准是
// 「认出了那个颜色」，图片颜色错了会让结论整体失真（且仅靠人眼复核不可靠）。
func TestProbeImageIsSolidRed(t *testing.T) {
	data, err := probeImage()
	if err != nil {
		t.Fatalf("probeImage: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	b := img.Bounds()
	if b.Dx() < 32 || b.Dy() < 32 {
		t.Fatalf("探测图太小（%dx%d）——太小有些端点会当成噪声", b.Dx(), b.Dy())
	}
	for _, pt := range []image.Point{b.Min, {X: b.Max.X - 1, Y: b.Max.Y - 1}, {X: b.Dx() / 2, Y: b.Dy() / 2}} {
		r, g, bl, _ := img.At(pt.X, pt.Y).RGBA()
		if r != 0xffff || g != 0 || bl != 0 {
			t.Fatalf("像素 %v = (%d,%d,%d)，应为纯红", pt, r>>8, g>>8, bl>>8)
		}
	}
}

func TestProbeVision(t *testing.T) {
	srv, got := probeServer(t, http.StatusOK, probeBody(`{"content":"Red"}`))
	c, _ := New(Config{BaseURL: srv.URL})
	res, err := c.Probe(context.Background(), "m", port.CapVision)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != port.ProbeSupported {
		t.Fatalf("verdict = %v, reason = %s", res.Verdict, res.Reason)
	}
	msgs, _ := (*got)[0]["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v", (*got)[0])
	}
	parts, _ := msgs[0].(map[string]any)["content"].([]any)
	if len(parts) != 2 || parts[1].(map[string]any)["type"] != "image_url" {
		t.Fatalf("content parts = %+v", parts)
	}
}

// TestProbeVisionVerdicts 视觉探测的四种结局：答对颜色 = 支持；答成别的颜色 / 明说看不到
// = 不支持；说不清 = 无结论。只要求「字段收下了」是不够的——那会把没解码图的端点判成支持。
func TestProbeVisionVerdicts(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  port.ProbeVerdict
	}{
		{"english", `{"content":"red"}`, port.ProbeSupported},
		{"capitalized", `{"content":"Red."}`, port.ProbeSupported},
		{"chinese", `{"content":"红色"}`, port.ProbeSupported},
		{"wrong color", `{"content":"blue"}`, port.ProbeUnsupported},
		{"blind", `{"content":"I cannot see any red in this image."}`, port.ProbeUnsupported},
		{"no image", `{"content":"There is no image attached."}`, port.ProbeUnsupported},
		{"evasive", `{"content":"It looks nice."}`, port.ProbeInconclusive},
		{"empty", `{"content":""}`, port.ProbeInconclusive},
		{"ambiguous", `{"content":"not red, it's blue"}`, port.ProbeInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := probeServer(t, http.StatusOK, probeBody(tc.reply))
			c, _ := New(Config{BaseURL: srv.URL})
			res, err := c.Probe(context.Background(), "m", port.CapVision)
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if res.Verdict != tc.want {
				t.Fatalf("verdict = %v, want %v（reply %s）", res.Verdict, tc.want, tc.reply)
			}
			if strings.TrimSpace(res.Reason) == "" {
				t.Fatal("结论必须带一句话说明")
			}
		})
	}
}

func TestProbeVisionRejected(t *testing.T) {
	srv, _ := probeServer(t, http.StatusBadRequest, `{"error":{"message":"image input is not supported"}}`)
	c, _ := New(Config{BaseURL: srv.URL})
	res, _ := c.Probe(context.Background(), "m", port.CapVision)
	if res.Verdict != port.ProbeUnsupported {
		t.Fatalf("verdict = %v, reason = %s", res.Verdict, res.Reason)
	}
}

func TestProbeReasoningSupported(t *testing.T) {
	srv, got := probeServer(t, http.StatusOK, probeBody(`{"content":"ok","reasoning_content":"thinking..."}`))
	c, _ := New(Config{BaseURL: srv.URL})
	res, err := c.Probe(context.Background(), "m", port.CapReasoning)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != port.ProbeSupported {
		t.Fatalf("verdict = %v, reason = %s", res.Verdict, res.Reason)
	}
	// 两套参数分别试（生态各认一套，一起发会互相拖累）：两次请求各带一个。
	if len(*got) != 2 {
		t.Fatalf("思考探测应发两发（各一套参数），实为 %d", len(*got))
	}
	if (*got)[0]["reasoning_effort"] != "low" {
		t.Fatalf("第一发应带 reasoning_effort: %+v", (*got)[0])
	}
	if _, has := (*got)[0]["enable_thinking"]; has {
		t.Fatalf("同一发不该同时带两套参数: %+v", (*got)[0])
	}
	if (*got)[1]["enable_thinking"] != true {
		t.Fatalf("第二发应带 enable_thinking: %+v", (*got)[1])
	}
	// 用量合计进结果（两次请求各花一点）。
	if res.Usage.InputTokens != 14 || res.Usage.OutputTokens != 6 {
		t.Fatalf("usage = %+v, want 两次累加", res.Usage)
	}
}

// TestProbeReasoningOneParamWorks 端点只认一套参数时不能误判成「不支持思考」：
// 一问法被点名拒绝、另一问法拿到思考内容 → 支持。
func TestProbeReasoningOneParamWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if _, has := body["reasoning_effort"]; has {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"Unrecognized request argument supplied: reasoning_effort"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok","reasoning_content":"hmm"}}]}`)
	}))
	t.Cleanup(srv.Close)
	c, _ := New(Config{BaseURL: srv.URL})
	res, err := c.Probe(context.Background(), "m", port.CapReasoning)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != port.ProbeSupported {
		t.Fatalf("verdict = %v, reason = %s（另一套参数可用就该算支持）", res.Verdict, res.Reason)
	}
}

// 两套参数都被点名拒绝 = 不支持思考。
func TestProbeReasoningBothRejected(t *testing.T) {
	srv, got := probeServer(t, http.StatusBadRequest,
		`{"error":{"message":"Unrecognized request argument supplied: reasoning_effort enable_thinking"}}`)
	c, _ := New(Config{BaseURL: srv.URL})
	res, err := c.Probe(context.Background(), "m", port.CapReasoning)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != port.ProbeUnsupported {
		t.Fatalf("verdict = %v, reason = %s", res.Verdict, res.Reason)
	}
	if len(*got) != 2 {
		t.Fatalf("应两套都试过，实为 %d 发", len(*got))
	}
	if !strings.Contains(res.Reason, "reasoning_effort") || !strings.Contains(res.Reason, "enable_thinking") {
		t.Fatalf("不支持的理由应写清两套都被拒：%s", res.Reason)
	}
}

// 一套被拒、另一套静默忽略 = 无结论（说不清到底有没有）。
func TestProbeReasoningSplitIsInconclusive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if _, has := body["reasoning_effort"]; has {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"Unrecognized request argument supplied: reasoning_effort"}}`)
			return
		}
		// enable_thinking 被接受，但没给思考内容（静默忽略）。
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(srv.Close)
	c, _ := New(Config{BaseURL: srv.URL})
	res, err := c.Probe(context.Background(), "m", port.CapReasoning)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != port.ProbeInconclusive {
		t.Fatalf("一套被拒、一套静默 = 无结论，实为 %v（%s）", res.Verdict, res.Reason)
	}
}

// vLLM 系把思考放在 `reasoning` 键：同样算支持。
func TestProbeReasoningAlternateKey(t *testing.T) {
	srv, _ := probeServer(t, http.StatusOK, probeBody(`{"content":"ok","reasoning":"hmm"}`))
	c, _ := New(Config{BaseURL: srv.URL})
	res, _ := c.Probe(context.Background(), "m", port.CapReasoning)
	if res.Verdict != port.ProbeSupported {
		t.Fatalf("verdict = %v", res.Verdict)
	}
}

// 参数被静默忽略 = 无结论；只有点名拒绝才写「不支持」。
func TestProbeReasoningVerdicts(t *testing.T) {
	cases := []struct {
		name   string
		status int
		reply  string
		want   port.ProbeVerdict
	}{
		{"silently ignored", http.StatusOK, probeBody(`{"content":"ok"}`), port.ProbeInconclusive},
		{"named rejection", http.StatusBadRequest,
			`{"error":{"message":"Unrecognized request argument supplied: reasoning_effort"}}`, port.ProbeUnsupported},
		{"enum error is not a capability verdict", http.StatusBadRequest,
			`{"error":{"message":"Invalid value 'low' for reasoning_effort"}}`, port.ProbeInconclusive},
		{"unrelated 400", http.StatusBadRequest, `{"error":{"message":"context length exceeded"}}`, port.ProbeInconclusive},
		{"5xx", http.StatusInternalServerError, `boom`, port.ProbeInconclusive},
		{"401", http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, port.ProbeInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := probeServer(t, tc.status, tc.reply)
			c, _ := New(Config{BaseURL: srv.URL})
			res, err := c.Probe(context.Background(), "m", port.CapReasoning)
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if res.Verdict != tc.want {
				t.Fatalf("verdict = %v, want %v (reason %s)", res.Verdict, tc.want, res.Reason)
			}
			if strings.TrimSpace(res.Reason) == "" {
				t.Fatal("结论必须带一句话说明")
			}
		})
	}
}

func TestProbeRejectsBadInput(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://127.0.0.1:1"})
	if _, err := c.Probe(nil, "m", port.CapTools); err == nil {
		t.Fatal("nil ctx 应报错")
	}
	if _, err := c.Probe(context.Background(), " ", port.CapTools); err == nil {
		t.Fatal("空模型名应报错")
	}
	if _, err := c.Probe(context.Background(), "m", "teleport"); err == nil {
		t.Fatal("未知能力应报错")
	}
}

// 传输层失败是「无结论」，不是错误（探测没跑通 ≠ 不支持）。
func TestProbeTransportIsInconclusive(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://127.0.0.1:1"})
	res, err := c.Probe(context.Background(), "m", port.CapTools)
	if err != nil {
		t.Fatalf("传输失败不应上抛: %v", err)
	}
	if res.Verdict != port.ProbeInconclusive {
		t.Fatalf("verdict = %v", res.Verdict)
	}
}

// 探测不写 unsupported_params（只读）：即使服务端点名拒绝思考参数，也不触发记录。
func TestProbeDoesNotRecordUnsupported(t *testing.T) {
	var noted []string
	srv, _ := probeServer(t, http.StatusBadRequest, `{"error":{"message":"Unrecognized request argument: reasoning_effort"}}`)
	c, _ := New(Config{BaseURL: srv.URL, NoteUnsupported: func(f string) { noted = append(noted, f) }})
	if _, err := c.Probe(context.Background(), "m", port.CapReasoning); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if len(noted) != 0 {
		t.Fatalf("探测不应记录 unsupported_params: %v", noted)
	}
}
