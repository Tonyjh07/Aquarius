package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestAPIKeyNeverLeaksIntoErrorsOrTrace 硬性规则 8 的负向守门：密钥不出现在错误文本
// 与留档（WireTrace）里。
//
// 密钥在这条链路上的暴露面有两处：请求头的 Authorization（只在请求里，天然不进
// 响应留档），与错误信息——HTTP 状态错误会把服务端响应摘要拼进错误文本，URL 若带
// `?key=` 也会随端点一起进视图。这条测试用一把可识别的假密钥把「成功流 / 5xx / 429」
// 三条路径各走一遍，收集沿途所有能被用户或日志看到的字符串，逐一断言假密钥不在其中。
// 以后给错误信息加上下文（比如把请求 URL 拼进报错）时，这条测试会在密钥进用户视野
// 之前拦住。
func TestAPIKeyNeverLeaksIntoErrorsOrTrace(t *testing.T) {
	// 假密钥要够独特：任何片段化拼接（如 URL 查询串编码）都藏不住这把密钥。
	const key = "sk-AQUARIUS-TEST-SECRET-0123456789abcdef"

	mode := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "ok":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, sseScript)
		case "boom":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"internal explosion"}}`)
		case "ratelimit":
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
		default:
			t.Fatalf("未设定的路径: %s", mode)
		}
	}))
	defer srv.Close()

	var traces []port.WireTrace
	c, err := New(Config{
		BaseURL: srv.URL + "/v1/?key=" + key, // 查询串带密钥的端点形状（redactedURL 的防守对象）
		APIKey:  key,
		Trace:   func(tr port.WireTrace) { traces = append(traces, tr) },
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	type step struct {
		name   string
		status int
	}
	for _, st := range []step{{"ok", 200}, {"boom", 500}, {"ratelimit", 429}} {
		mode = st.name
		s, err := c.Generate(context.Background(), port.GenerateRequest{
			Model:    "m",
			Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}}},
		})
		if st.status == 200 {
			if err != nil {
				t.Fatalf("%s: generate: %v", st.name, err)
			}
			// 流里的每个增量文本也算「会被用户看到的字符串」。
			for {
				d, rerr := s.Recv()
				if rerr == io.EOF {
					break
				}
				if rerr != nil {
					t.Fatalf("%s: recv: %v", st.name, rerr)
				}
				if strings.Contains(d.Text, key) {
					t.Errorf("%s: 密钥出现在流式增量里", st.name)
				}
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: 期待报错，却是成功返回", st.name)
		}
		if strings.Contains(err.Error(), key) {
			t.Errorf("%s: 密钥出现在错误文本里: %q", st.name, err.Error())
		}
	}

	if len(traces) < 3 {
		t.Fatalf("只收到 %d 份留档（期待 >=3）：Trace 回调没接上，守门等于没跑", len(traces))
	}
	for i, tr := range traces {
		haystacks := []string{tr.Meta.URL, strings.Join(tr.Meta.Headers, "\n"), strings.Join(tr.Meta.Notes, "\n"), string(tr.Raw)}
		for _, h := range haystacks {
			if strings.Contains(h, key) {
				t.Errorf("留档 #%d 含密钥：URL=%q Headers=%v", i, tr.Meta.URL, tr.Meta.Headers)
			}
		}
	}
}
