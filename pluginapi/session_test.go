package pluginapi

import (
	"context"
	"testing"
)

// session_test 会话身份的 ctx 传递：只验「宿主写、插件读」这一条往返与零值语义。
func TestSessionRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		ctx    func() context.Context
		want   string
		wantOK bool
	}{
		{"没有注入", func() context.Context { return context.Background() }, "", false},
		{"注入了会话", func() context.Context { return WithSession(context.Background(), "s-1") }, "s-1", true},
		{"空串等于不注入", func() context.Context { return WithSession(context.Background(), "") }, "", false},
		{"后注入覆盖先注入", func() context.Context {
			return WithSession(WithSession(context.Background(), "old"), "new")
		}, "new", true},
		{"别的包的字符串键不算", func() context.Context {
			return context.WithValue(context.Background(), "session", "forged")
		}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := SessionFrom(c.ctx())
			if got != c.want || ok != c.wantOK {
				t.Fatalf("SessionFrom = (%q, %v)，要 (%q, %v)", got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TestSessionSurvivesChildContext ctx 派生（加超时、加取消）不得丢会话：宿主在入口
// 注入之后还要套超时与取消，丢了就等于漏注入。
func TestSessionSurvivesChildContext(t *testing.T) {
	ctx, cancel := context.WithCancel(WithSession(context.Background(), "s-2"))
	defer cancel()
	if got, ok := SessionFrom(ctx); !ok || got != "s-2" {
		t.Fatalf("取消上下文里的会话丢了：(%q, %v)", got, ok)
	}
}
