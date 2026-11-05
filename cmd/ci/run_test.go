package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCleanEnvDropsProjectAndSecrets(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin",
		"AQUARIUS_UI_LANG=en",
		"AQUARIUS_OPENAI_KEY=sk-should-not-leak",
		"OPENAI_API_KEY=sk-also",
		"SystemRoot=C:\\Windows",
		"GOCACHE=/tmp/cache",
	}
	got := strings.Join(cleanEnv(environ), "\n")

	for _, gone := range []string{"AQUARIUS_UI_LANG", "AQUARIUS_OPENAI_KEY", "OPENAI_API_KEY"} {
		if strings.Contains(got, gone) {
			t.Errorf("%s 应被清掉：这一档就是用来暴露「测试依赖本机配好的密钥」的", gone)
		}
	}
	for _, kept := range []string{"PATH=", "SystemRoot=", "GOCACHE="} {
		if !strings.Contains(got, kept) {
			t.Errorf("%s 应保留：没有它 go 工具链跑不起来", kept)
		}
	}
}

func TestCleanEnvCaseInsensitiveOnWindows(t *testing.T) {
	// Windows 的环境变量名不区分大小写，白名单匹配必须折叠，否则会误删。
	got := cleanEnv([]string{"Path=C:\\bin", "temp=C:\\tmp"})
	if len(got) != 2 {
		t.Fatalf("大小写不同的同名变量被误删：%v", got)
	}
}

func TestWithEnvShadowed(t *testing.T) {
	base := []string{"AQUARIUS_UI_LANG=zh", "PATH=/usr/bin"}
	got := withEnv(base, []string{"AQUARIUS_UI_LANG=en"})

	var hits int
	for _, kv := range got {
		if strings.HasPrefix(kv, "AQUARIUS_UI_LANG=") {
			hits++
			if kv != "AQUARIUS_UI_LANG=en" {
				t.Errorf("同名键该被覆盖，得到 %q", kv)
			}
		}
	}
	if hits != 1 {
		t.Errorf("同名键应只剩一条，得到 %d 条", hits)
	}
}

func TestRunOnePassAndFail(t *testing.T) {
	// 用 go 自己的子命令当被测命令：它一定存在，输出也稳定。
	pass := runOne(context.Background(), job{
		name: "ok", timeout: time.Minute,
		steps: [][]string{{"go", "version"}},
	}, t.TempDir(), nil, false)
	if pass.Status != stPass {
		t.Fatalf("正常命令应判过，得到 %s（%s）", pass.Status, pass.Reason)
	}

	quiet := runOne(context.Background(), job{
		name: "quiet", timeout: time.Minute,
		steps: [][]string{{"go", "version"}}, verify: verifyQuiet,
	}, t.TempDir(), nil, false)
	if quiet.Status != stFail {
		t.Fatalf("有输出时 verifyQuiet 应判失败，得到 %s", quiet.Status)
	}
	if !strings.Contains(quiet.Output, "go version") {
		t.Errorf("失败时应把输出带回，得到 %q", quiet.Output)
	}
}

func TestRunOneSkipsWhenCapabilityMissing(t *testing.T) {
	caps := map[capability]capState{capNetwork: {ok: false, reason: "连不上"}}
	res := runOne(context.Background(), job{
		name: "net", timeout: time.Minute, requires: []capability{capNetwork},
		// 故意给一条会失败的命令：跳过的判定必须发生在起进程之前。
		steps: [][]string{{"go", "definitely-not-a-subcommand"}},
	}, t.TempDir(), caps, false)

	if res.Status != stSkip {
		t.Fatalf("缺能力应跳过而不是失败，得到 %s", res.Status)
	}
	if !strings.Contains(res.Reason, "连不上") {
		t.Errorf("跳过原因应带上探测结论，得到 %q", res.Reason)
	}
}

func TestRunOneOptionalDowngradesFailure(t *testing.T) {
	res := runOne(context.Background(), job{
		name: "vuln", timeout: time.Minute, optional: true,
		steps: [][]string{{"go", "definitely-not-a-subcommand"}},
	}, t.TempDir(), nil, false)

	if res.Status != stSkip {
		t.Fatalf("optional 的失败应降级为跳过，得到 %s", res.Status)
	}
}

func TestRunOneTimesOut(t *testing.T) {
	res := runOne(context.Background(), job{
		name: "slow", timeout: time.Nanosecond,
		steps: [][]string{{"go", "env"}},
	}, t.TempDir(), nil, false)
	if res.Status == stPass {
		t.Fatal("超时不该判过")
	}
	if res.Reason == "" {
		t.Error("超时要给出原因，否则用户以为测试挂了")
	}
}

func TestOutputTailKeepsEnd(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("行")
		b.WriteString(string(rune('0' + i%10)))
		b.WriteByte('\n')
	}
	got := outputTail(b.String(), false)
	if n := strings.Count(got, "\n"); n > 41 {
		t.Errorf("应只保留末尾 40 行，得到 %d 行", n)
	}
	if !strings.Contains(got, "前 60 行略") {
		t.Errorf("截断时要说清略了多少，得到 %q", got)
	}
	if full := outputTail(b.String(), true); strings.Count(full, "\n") < 99 {
		t.Error("-v 时应给完整输出")
	}
}

func TestSummarize(t *testing.T) {
	got := summarize([]jobResult{
		{Status: stPass}, {Status: stPass}, {Status: stFail}, {Status: stSkip},
	})
	if got.passed != 2 || got.failed != 1 || got.skipped != 1 {
		t.Fatalf("汇总不对：%+v", got)
	}
}

func TestParseGoEnvBothPlatformFormats(t *testing.T) {
	// Windows 的 `go env` 用 cmd 语法，其余平台用单引号。两种都得认，
	// 否则能力探测会把 CC 读成空、把 -race 静默跳过。
	win := "set CC=C:\\mingw\\bin\\clang.exe\r\nset CGO_ENABLED=1\r\n"
	nix := "CC='clang'\nCGO_ENABLED='1'\n"

	if got := parseGoEnv(win)["CC"]; got != `C:\mingw\bin\clang.exe` {
		t.Errorf("Windows 形态解析错：%q", got)
	}
	if got := parseGoEnv(win)["CGO_ENABLED"]; got != "1" {
		t.Errorf("Windows 形态的 CGO_ENABLED 解析错：%q", got)
	}
	if got := parseGoEnv(nix)["CC"]; got != "clang" {
		t.Errorf("单引号形态解析错：%q", got)
	}
	if len(parseGoEnv("")) != 0 {
		t.Error("空输入应得到空表")
	}
}
