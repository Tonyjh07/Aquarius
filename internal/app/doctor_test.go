package app

// doctor_test.go `/doctor`（§7.14/D306）：结论的排版规则与命令面的三个入口。
//
// 检查本身在装配根（config / 密钥库 / 会话文件 / 目录 / 卷），这里守的是这一层：
// 全绿只有一行、有问题时七项全列、修法只跟非正常项走、未知 ID 原样露出。

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeProbe 体检面的测试替身：结论自己给，检查一列都不真跑。
type fakeProbe struct{ checks []HealthCheck }

func (f fakeProbe) Probe(context.Context) []HealthCheck { return f.checks }

// TestRenderDoctorCleanIsOneLine 全绿只回一行：正常项没有可动作的信息（D306 决策 3）。
func TestRenderDoctorCleanIsOneLine(t *testing.T) {
	got := RenderDoctor([]HealthCheck{
		{ID: "config", State: HealthOK, Detail: "解析正常"},
		{ID: "disk", State: HealthOK, Detail: "可用 53.5 GB"},
	})
	if strings.Contains(got, "\n") {
		t.Fatalf("全绿应当只有一行，得到：%q", got)
	}
	if want := i18n.Tf("session.doctor.clean", "n", 2); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRenderDoctorListsEverything 有一项不正常时七项全列（含 normal 的那几项），
// 修法只跟非正常项走，注意也算「需要处理」。
func TestRenderDoctorListsEverything(t *testing.T) {
	fix := i18n.T("cmd.doctor.sessions_fix")
	checks := []HealthCheck{
		{ID: "config", State: HealthOK, Detail: "ok"},
		{ID: "secrets", State: HealthFail, Detail: i18n.Tf("cmd.doctor.secrets_bad", "err", "boom"), Fix: fix},
		{ID: "disk", State: HealthWarn, Detail: "2.0 GB"},
	}
	got := RenderDoctor(checks)
	lines := strings.Split(got, "\n")
	// 头 + 三项 + 一条修法
	if len(lines) != 5 {
		t.Fatalf("行数 = %d，得到：\n%s", len(lines), got)
	}
	if want := i18n.Tf("session.doctor.head", "n", 3, "bad", 2); lines[0] != want {
		t.Fatalf("首行 = %q，want %q", lines[0], want)
	}
	if !strings.Contains(got, healthCheckName("config")) || !strings.Contains(got, healthCheckName("disk")) {
		t.Fatalf("正常项被合并掉了：\n%s", got)
	}
	fixLabel := strings.TrimSpace(strings.SplitN(i18n.T("session.doctor.fix"), "{", 2)[0])
	if n := strings.Count(got, fixLabel); n != 1 {
		t.Fatalf("修法行 = %d，只有异常项该带修法：\n%s", n, got)
	}
	if !strings.Contains(got, fix) {
		t.Fatalf("修法没印出来：\n%s", got)
	}
}

// TestRenderDoctorUnknownID 未登记的检查项原样露出 ID：宁可看见一个丑标识，也不要空着。
func TestRenderDoctorUnknownID(t *testing.T) {
	got := RenderDoctor([]HealthCheck{{ID: "brand-new", State: HealthWarn, Detail: "d"}})
	if !strings.Contains(got, "brand-new") {
		t.Fatalf("未知 ID 没露出：%q", got)
	}
}

// TestExecDoctor 命令面：有参数报用法、没接面报未接入、接了就把结论原样排版出来。
func TestExecDoctor(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	ctx := context.Background()

	if _, err := s.execDoctor(ctx, port.Command{Name: "doctor", Args: []string{"now"}}); err == nil {
		t.Fatal("带参数应当报用法")
	}
	if _, err := s.execDoctor(ctx, port.Command{Name: "doctor"}); err == nil {
		t.Fatal("没接体检面时应当报未接入")
	}

	detail, fix := i18n.Tf("cmd.doctor.secrets_bad", "err", "boom"), i18n.T("cmd.doctor.mcp_fix")
	s.doctor = fakeProbe{checks: []HealthCheck{{ID: "mcp", State: HealthFail, Detail: detail, Fix: fix}}}
	got, err := s.execDoctor(ctx, port.Command{Name: "doctor"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(got, "boom") || !strings.Contains(got, fix) {
		t.Fatalf("结论没排出来：%q", got)
	}
}
