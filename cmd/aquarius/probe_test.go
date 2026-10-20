package main

// 能力探测的装配根一半（D174）：定位模型条目、跑三项、只写回判定明确的几位。
// 全部用假 server，禁真实网络。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// probeCfg 写一份最小 v2 配置；baseURL 指向假服务。
func probeCfg(t *testing.T, baseURL, models string) (string, *fileConfig) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	raw := fmt.Sprintf(`{
  "model": {
    "providers": [{"name": "a", "base_url": %q, "keys": [{"api_key": "plain-key"}],
      "models": [%s]}],
    "primary": "a",
    "name": "m1"
  },
  "ui": {"kind": "repl"}
}`, baseURL, models)
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return path, cfg
}

// probeReply 假服务对三类探测请求的应答；空 = 用「支持」模板。
type probeReply struct {
	ToolsStatus  int
	Tools        string
	VisionStatus int
	Vision       string
	Reasoning    string
}

// probeServer 按请求体里的能力标记分流（工具清单 / 思考参数 / 否则算视觉）。
// 思考探测会发两发（reasoning_effort 与 enable_thinking 各一次），两者都答同一份。
func probeServer(t *testing.T, r probeReply) *httptest.Server {
	t.Helper()
	toolBody := r.Tools
	if toolBody == "" {
		toolBody = `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"probe_ping","arguments":"{}"}}]}}],"usage":{"prompt_tokens":20,"completion_tokens":4}}`
	}
	if r.ToolsStatus == 0 {
		r.ToolsStatus = http.StatusOK
	}
	reasonBody := r.Reasoning
	if reasonBody == "" {
		reasonBody = `{"choices":[{"message":{"content":"ok","reasoning_content":"hm"}}]}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if _, hasTools := body["tools"]; hasTools {
			w.WriteHeader(r.ToolsStatus)
			fmt.Fprint(w, toolBody)
			return
		}
		_, hasEffort := body["reasoning_effort"]
		_, hasThinking := body["enable_thinking"]
		if hasEffort || hasThinking {
			fmt.Fprint(w, reasonBody)
			return
		}
		w.WriteHeader(r.VisionStatus)
		fmt.Fprint(w, r.Vision)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestProbeWritesOnlyDefiniteVerdicts D174：支持→加上、明确不支持→去掉、无结论→不动；
// 起点是**生效值**（键缺失按协议缺省），否则会把缺省开的位悄悄关掉。
func TestProbeWritesOnlyDefiniteVerdicts(t *testing.T) {
	srv := probeServer(t, probeReply{
		VisionStatus: http.StatusBadRequest,
		Vision:       `{"error":{"message":"image input is not supported"}}`,
	})
	cfgPath, cfg := probeCfg(t, srv.URL, `{"name": "m1"}`) // 无 caps = openai-chat 缺省（工具+思考）
	w := &wiring{cfg: cfg, cfgPath: cfgPath, secrets: newSecretChain(nil, io.Discard), stderr: os.Stderr}

	report, err := w.probeModel(context.Background(), "")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if report.Provider != "a" || report.Model != "m1" || len(report.Results) != 3 || !report.Written {
		t.Fatalf("report = %+v", report)
	}
	byCap := map[string]port.ProbeVerdict{}
	for _, r := range report.Results {
		byCap[r.Capability] = r.Verdict
	}
	if byCap[port.CapTools] != port.ProbeSupported || byCap[port.CapReasoning] != port.ProbeSupported ||
		byCap[port.CapVision] != port.ProbeUnsupported {
		t.Fatalf("结论 = %+v", byCap)
	}

	// 写回：工具/思考保留、图片去掉；且固化为显式声明。
	after, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	entry := after.Model.Providers[0].modelByName("m1")
	if entry == nil || entry.Caps == nil {
		t.Fatalf("caps 未写回: %+v", after.Model.Providers[0].Models)
	}
	if got := after.Model.Providers[0].capsOf(*entry); got != (port.ModelCaps{Tools: true, Reasoning: true}) {
		t.Fatalf("caps = %+v (%v)", got, *entry.Caps)
	}

	// 再探一次：结论没变 → 不重复写文件（Written=false）。
	report, err = w.probeModel(context.Background(), "m1")
	if err != nil {
		t.Fatalf("probe again: %v", err)
	}
	if report.Written {
		t.Fatal("结论与现有声明一致时不该再写回")
	}
}

// TestProbeInconclusiveLeavesCapsAlone D174：全项无结论 = 一个字都不写。
func TestProbeInconclusiveLeavesCapsAlone(t *testing.T) {
	// 视觉探测也走「接受但没给正文」→ 三项全无结论。
	srv := probeServer(t, probeReply{
		Tools:     `{"choices":[{"message":{"content":"I cannot call tools."}}]}`,
		Reasoning: `{"choices":[{"message":{"content":"ok"}}]}`,
		Vision:    `{"choices":[{"message":{"content":""}}]}`,
	})
	cfgPath, cfg := probeCfg(t, srv.URL, `{"name": "m1", "caps": ["tools"]}`)
	w := &wiring{cfg: cfg, cfgPath: cfgPath, secrets: newSecretChain(nil, io.Discard), stderr: os.Stderr}

	before, _ := os.ReadFile(cfgPath)
	report, err := w.probeModel(context.Background(), "m1")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if report.Written {
		t.Fatal("无结论不该写回")
	}
	for _, r := range report.Results {
		// 工具探测这里是「被接受但没调用」，思考是「没思考内容」，图片是「正文为空」——
		// 都必须是「无结论」：探不通不等于不支持。
		if r.Verdict != port.ProbeInconclusive {
			t.Fatalf("%s 判成了 %v（应为无结论）", r.Capability, r.Verdict)
		}
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Fatal("无结论时 config 不该被改动")
	}
}

// TestProbeTargetResolution D174 口径四：度量单位是模型条目——未声明的不探（没地方写），
// fallback provider 的模型也能探。
func TestProbeTargetResolution(t *testing.T) {
	srv := probeServer(t, probeReply{VisionStatus: http.StatusBadRequest,
		Vision: `{"error":{"message":"image input is not supported"}}`})
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	raw := fmt.Sprintf(`{
  "model": {
    "providers": [
      {"name": "a", "base_url": %q, "models": [{"name": "m1"}]},
      {"name": "b", "base_url": %q, "models": [{"name": "mb"}]}
    ],
    "primary": "a",
    "fallback": [{"provider": "b"}],
    "name": "m1"
  },
  "ui": {"kind": "repl"}
}`, srv.URL, srv.URL)
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	w := &wiring{cfg: cfg, cfgPath: cfgPath, secrets: newSecretChain(nil, io.Discard), stderr: os.Stderr}

	if _, err := w.probeModel(context.Background(), "nope"); err == nil ||
		!strings.Contains(err.Error(), "不在任何 provider") {
		t.Fatalf("未声明模型 = %v, want 报因（要先声明才写得回）", err)
	}
	report, err := w.probeModel(context.Background(), "mb")
	if err != nil {
		t.Fatalf("probe fallback model: %v", err)
	}
	if report.Provider != "b" {
		t.Fatalf("provider = %q, want b", report.Provider)
	}
}

// TestProbeUnwiredProtocolFailsLoud 协议还没接入时探测必须报因：拿 chat/completions
// 去打别的规范的端点，会以「密钥无效」「模型不存在」之类的假象暴露，排查方向全错。
func TestProbeUnwiredProtocolFailsLoud(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	raw := `{
  "model": {
    "providers": [{"name": "a", "kind": "anthropic", "base_url": "http://127.0.0.1:1",
      "models": [{"name": "m1"}]}],
    "primary": "a",
    "name": "m1"
  },
  "ui": {"kind": "repl"}
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	w := &wiring{cfg: cfg, cfgPath: cfgPath, secrets: newSecretChain(nil, io.Discard), stderr: os.Stderr}
	if _, err := w.probeModel(context.Background(), ""); err == nil ||
		!strings.Contains(err.Error(), "anthropic") {
		t.Fatalf("err = %v, want 指名协议", err)
	}
	if _, _, err := w.newLLMClient(&cfg.Model.Providers[0], "", nil); err == nil ||
		!strings.Contains(err.Error(), "尚未接入") {
		t.Fatalf("err = %v, want 尚未接入（不能拿 chat 的请求去打别的规范）", err)
	}
}

// TestProbeClientIsReadOnly 探测不记 unsupported_params（那是正式请求的事），
// 也不写进 config 的 provider 段。
func TestProbeClientIsReadOnly(t *testing.T) {
	srv := probeServer(t, probeReply{VisionStatus: http.StatusBadRequest,
		Vision: `{"error":{"message":"image input is not supported"}}`})
	cfgPath, cfg := probeCfg(t, srv.URL, `{"name": "m1"}`)
	w := &wiring{cfg: cfg, cfgPath: cfgPath, secrets: newSecretChain(nil, io.Discard), stderr: os.Stderr}
	if _, err := w.probeModel(context.Background(), "m1"); err != nil {
		t.Fatalf("probe: %v", err)
	}
	var generic map[string]any
	data, _ := os.ReadFile(cfgPath)
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	p := providerSection(generic, "a")
	if _, has := p["unsupported_params"]; has {
		t.Fatal("探测不该写 unsupported_params")
	}
}
