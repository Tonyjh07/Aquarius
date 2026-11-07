package main

// 设置窗「外部代理」页的管理面（D270）：命令行拆解 + 三条写回。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// TestSplitCommandLine 命令行拆解：空白切分、双引号成组、反斜杠只在引号内对 `"`/`\` 生效。
func TestSplitCommandLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		line    string
		command string
		args    []string
		wantErr bool
	}{
		{name: "空行", line: "   ", command: "", args: nil},
		{name: "命令加参数", line: "opencode acp", command: "opencode", args: []string{"acp"}},
		{name: "多余空白", line: "  opencode   acp  ", command: "opencode", args: []string{"acp"}},
		{
			name:    "带空格的路径", // Windows 路径在主流程里是常态，不能把反斜杠当转义吃掉
			line:    `"C:\Program Files\acp\agent.exe" --stdio`,
			command: `C:\Program Files\acp\agent.exe`,
			args:    []string{"--stdio"},
		},
		{
			name:    "引号内转义",
			line:    `agent "a \"quoted\" arg"`,
			command: "agent",
			args:    []string{`a "quoted" arg`},
		},
		{name: "引号未闭合", line: `agent "oops`, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			command, args, err := splitCommandLine(c.line)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q %q", command, args)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if command != c.command || !slices.Equal(args, c.args) {
				t.Fatalf("got %q %q, want %q %q", command, args, c.command, c.args)
			}
		})
	}
}

// TestCommandLineRoundTrip 展示形态能被拆回同样的命令（界面保存的就是它显示的那一行）。
func TestCommandLineRoundTrip(t *testing.T) {
	t.Parallel()
	command := `C:\Program Files\acp\agent.exe`
	args := []string{"acp", "--title", "my agent"}
	line := commandLineOf(command, args)
	gotCmd, gotArgs, err := splitCommandLine(line)
	if err != nil {
		t.Fatalf("err = %v（行 = %q）", err, line)
	}
	if gotCmd != command || !slices.Equal(gotArgs, args) {
		t.Fatalf("round trip: %q %q（行 = %q）", gotCmd, gotArgs, line)
	}
}

// delegateAdminFixture 一个只有 delegate 段的 profile：返回装配好的管理面与配置路径。
func delegateAdminFixture(t *testing.T, agentsJSON string) (*delegateAdmin, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"delegate":{"agents":` + agentsJSON + `}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("写配置: %v", err)
	}
	var cfg fileConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	return &delegateAdmin{w: &wiring{cfgPath: path, cfg: &cfg}}, path
}

// readDelegateAgents 从盘上读回 delegate.agents（验证写回的形态，而不是内存里的影子）。
func readDelegateAgents(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读配置: %v", err)
	}
	var doc struct {
		Delegate struct {
			Agents []map[string]any `json:"agents"`
		} `json:"delegate"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	return doc.Delegate.Agents
}

// TestDelegateAdminSetCommandLine 改命令行：写回 command/args，空参数时不留 "args": [] 噪音。
func TestDelegateAdminSetCommandLine(t *testing.T) {
	t.Parallel()
	admin, path := delegateAdminFixture(t, `[{"name":"a","command":"old","args":["x"]}]`)
	if err := admin.SetCommandLine("a", `"C:\tools\agent.exe" acp --stdio`); err != nil {
		t.Fatalf("set: %v", err)
	}
	agents := readDelegateAgents(t, path)
	if got := agents[0]["command"]; got != `C:\tools\agent.exe` {
		t.Fatalf("command = %v", got)
	}
	args, _ := agents[0]["args"].([]any)
	if len(args) != 2 || args[0] != "acp" || args[1] != "--stdio" {
		t.Fatalf("args = %v", args)
	}

	// 只给命令：args 键应被删掉（不是留一个空数组）。
	if err := admin.SetCommandLine("a", "plain"); err != nil {
		t.Fatalf("set: %v", err)
	}
	agents = readDelegateAgents(t, path)
	if _, ok := agents[0]["args"]; ok {
		t.Fatalf("无参数时不该留 args: %v", agents[0])
	}
	if admin.w.cfg.Delegate.Agents[0].Command != "plain" {
		t.Fatalf("内存里的命令没跟着走: %+v", admin.w.cfg.Delegate.Agents[0])
	}

	// 空命令行：拒绝（删代理请改文件）。
	if err := admin.SetCommandLine("a", "   "); err == nil {
		t.Fatal("空命令行应报错")
	}
	// 不存在的名字：报错而不是静默写空。
	if err := admin.SetCommandLine("nope", "x"); err == nil {
		t.Fatal("未知代理应报错")
	}
}

// TestDelegateAdminEnabledAndDefault 启停与缺省：缺省不能停用、停用不能设缺省。
func TestDelegateAdminEnabledAndDefault(t *testing.T) {
	t.Parallel()
	admin, path := delegateAdminFixture(t, `[
		{"name":"a","command":"a","default":true},
		{"name":"b","command":"b","enabled":false}
	]`)
	if err := admin.SetEnabled("a", false); err == nil {
		t.Fatal("缺省代理不该允许停用")
	}
	if err := admin.SetDefault("b"); err == nil {
		t.Fatal("停用的代理不该允许设为缺省")
	}
	if err := admin.SetEnabled("b", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := admin.SetDefault("b"); err != nil {
		t.Fatalf("default: %v", err)
	}
	agents := readDelegateAgents(t, path)
	if agents[0]["default"] != nil {
		t.Fatalf("旧缺省应被清掉: %v", agents[0])
	}
	if agents[1]["default"] != true {
		t.Fatalf("新缺省没写上: %v", agents[1])
	}
	// 快照跟着配置走：b 缺省且已启用。
	snap := admin.Snapshot()
	if len(snap.Agents) != 2 || !snap.Agents[1].Default || !snap.Agents[1].Enabled {
		t.Fatalf("快照 = %+v", snap.Agents)
	}
}

// TestDelegateAdminSnapshotAvailability 可用性按 PATH 现算；零代理给一句去哪写。
func TestDelegateAdminSnapshotAvailability(t *testing.T) {
	t.Parallel()
	admin, _ := delegateAdminFixture(t, `[
		{"name":"gone","command":"definitely-not-on-path-1f2a","enabled":true},
		{"name":"off","command":"whatever","enabled":false},
		{"name":"empty","command":""}
	]`)
	snap := admin.Snapshot()
	if snap.Agents[0].Available || !strings.Contains(snap.Agents[0].Note, "PATH") {
		t.Fatalf("gone = %+v", snap.Agents[0])
	}
	if snap.Agents[1].Available || snap.Agents[1].Note != i18n.T("cmd.delegate.note.disabled") {
		t.Fatalf("off = %+v", snap.Agents[1])
	}
	if snap.Agents[2].Available || !strings.Contains(snap.Agents[2].Note, "command") {
		t.Fatalf("empty = %+v", snap.Agents[2])
	}

	blank, _ := delegateAdminFixture(t, `[]`)
	if got := blank.Snapshot(); got.Note == "" || len(got.Agents) != 0 {
		t.Fatalf("零代理快照 = %+v, want 一句指引", got)
	}
}

// TestDelegateAdminAdd Add：数组级追加写盘 + 内存同步；重名/空名/空命令/未闭合引号全拦在界面。
func TestDelegateAdminAdd(t *testing.T) {
	t.Parallel()
	admin, path := delegateAdminFixture(t, `[{"name":"a","command":"a"}]`)
	if err := admin.Add("b", `"C:/tools/b.exe" acp --stdio`); err != nil {
		t.Fatalf("add: %v", err)
	}
	agents := readDelegateAgents(t, path)
	if len(agents) != 2 {
		t.Fatalf("agents = %v（want 两条）", agents)
	}
	if agents[1]["name"] != "b" || agents[1]["command"] != `C:/tools/b.exe` {
		t.Fatalf("新条目 = %v", agents[1])
	}
	args, _ := agents[1]["args"].([]any)
	if len(args) != 2 || args[0] != "acp" || args[1] != "--stdio" {
		t.Fatalf("新条目 args = %v", args)
	}
	if _, ok := agents[1]["enabled"]; ok {
		t.Fatalf("不该写 enabled（键缺失 = 启用）: %v", agents[1])
	}
	if got := admin.w.cfg.Delegate.Agents; len(got) != 2 || got[1].Name != "b" || got[1].Command != `C:/tools/b.exe` {
		t.Fatalf("内存影子 = %+v", got)
	}

	for _, c := range []struct{ name, line string }{
		{"", "x"},            // 空名
		{"  ", "x"},          // 只有空白
		{"a", "x"},           // 重名
		{"c", "   "},         // 空命令行
		{"c", `agent "oops`}, // 引号未闭合
	} {
		if err := admin.Add(c.name, c.line); err == nil {
			t.Fatalf("Add(%q, %q) 应报错", c.name, c.line)
		}
	}
	if got := len(readDelegateAgents(t, path)); got != 2 {
		t.Fatalf("拦下的新增不该写盘：agents 条数 = %d", got)
	}
}

// TestDelegateAdminRemove 删除一条：其余原样保留，内存跟着走；删不存在的报错。
func TestDelegateAdminRemove(t *testing.T) {
	t.Parallel()
	admin, path := delegateAdminFixture(t, `[
		{"name":"a","command":"a","args":["1"]},
		{"name":"b","command":"b","default":true},
		{"name":"c","command":"c","workdir":"/tmp"}
	]`)
	if err := admin.Remove("b"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	agents := readDelegateAgents(t, path)
	if len(agents) != 2 || agents[0]["name"] != "a" || agents[1]["name"] != "c" {
		t.Fatalf("删后 = %v", agents)
	}
	if agents[0]["command"] != "a" || agents[1]["workdir"] != "/tmp" {
		t.Fatalf("其余字段不该被动: %v", agents)
	}
	if got := admin.w.cfg.Delegate.Agents; len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("内存影子 = %+v", got)
	}
	if err := admin.Remove("nope"); err == nil {
		t.Fatal("删不存在的代理应报错")
	}
}

// TestDelegateAdminRename 改名：旧名消失、新名就位；重名被拦；改成同名是幂等成功。
func TestDelegateAdminRename(t *testing.T) {
	t.Parallel()
	admin, path := delegateAdminFixture(t, `[
		{"name":"a","command":"a"},
		{"name":"b","command":"b"}
	]`)
	if err := admin.Rename("a", "a2"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	agents := readDelegateAgents(t, path)
	if agents[0]["name"] != "a2" || agents[0]["command"] != "a" {
		t.Fatalf("改名后 = %v", agents[0])
	}
	if admin.w.cfg.Delegate.Agents[0].Name != "a2" {
		t.Fatalf("内存影子 = %+v", admin.w.cfg.Delegate.Agents[0])
	}
	if err := admin.Rename("a2", "b"); err == nil {
		t.Fatal("改成已有名字应报错")
	}
	if err := admin.Rename("a2", ""); err == nil {
		t.Fatal("空名字应报错")
	}
	if err := admin.Rename("a2", "a2"); err != nil {
		t.Fatalf("改成同名应幂等成功: %v", err)
	}
	if err := admin.Rename("ghost", "x"); err == nil {
		t.Fatal("改不存在的代理应报错")
	}
}

// TestDelegateAdminFields 字段级编辑：各键单独落盘，空值删键而不是留空数组/空表。
func TestDelegateAdminFields(t *testing.T) {
	t.Parallel()
	admin, path := delegateAdminFixture(t, `[{"name":"a","command":"a"}]`)
	root := t.TempDir()

	if err := admin.SetCommand("a", "  /opt/agent  "); err != nil {
		t.Fatalf("set command: %v", err)
	}
	if err := admin.SetArgs("a", []string{" acp ", "", " --stdio "}); err != nil {
		t.Fatalf("set args: %v", err)
	}
	if err := admin.SetEnv("a", map[string]string{"KEY": "secret:openai", "PLAIN": "x"}); err != nil {
		t.Fatalf("set env: %v", err)
	}
	if err := admin.SetWorkdir("a", root); err != nil {
		t.Fatalf("set workdir: %v", err)
	}
	if err := admin.SetRoots("a", []string{root, " " + root + " "}); err != nil {
		t.Fatalf("set roots: %v", err)
	}

	agents := readDelegateAgents(t, path)
	got := agents[0]
	if got["command"] != "/opt/agent" {
		t.Fatalf("command = %v（应去空白）", got["command"])
	}
	args, _ := got["args"].([]any)
	if len(args) != 2 || args[0] != "acp" || args[1] != "--stdio" {
		t.Fatalf("args = %v（应去掉空行）", args)
	}
	env, _ := got["env"].(map[string]any)
	if len(env) != 2 || env["KEY"] != "secret:openai" {
		t.Fatalf("env = %v", env)
	}
	if got["workdir"] != root {
		t.Fatalf("workdir = %v", got["workdir"])
	}
	roots, _ := got["roots"].([]any)
	if len(roots) != 1 || roots[0] != root {
		t.Fatalf("roots = %v（应去重）", roots)
	}
	if st := admin.w.cfg.Delegate.Agents[0]; st.Command != "/opt/agent" || len(st.Roots) != 1 || st.Env["KEY"] != "secret:openai" {
		t.Fatalf("内存影子 = %+v", st)
	}

	// 清空：args / env / workdir / roots 都该删键。
	if err := admin.SetArgs("a", nil); err != nil {
		t.Fatalf("clear args: %v", err)
	}
	if err := admin.SetEnv("a", nil); err != nil {
		t.Fatalf("clear env: %v", err)
	}
	if err := admin.SetWorkdir("a", "  "); err != nil {
		t.Fatalf("clear workdir: %v", err)
	}
	if err := admin.SetRoots("a", nil); err != nil {
		t.Fatalf("clear roots: %v", err)
	}
	got = readDelegateAgents(t, path)[0]
	for _, key := range []string{"args", "env", "workdir", "roots"} {
		if _, ok := got[key]; ok {
			t.Fatalf("清空后不该留 %s: %v", key, got)
		}
	}
}

// TestDelegateAdminFieldValidation 字段级编辑的校验：相对路径拦下，空命令拦下，未知名字拦下。
func TestDelegateAdminFieldValidation(t *testing.T) {
	t.Parallel()
	admin, path := delegateAdminFixture(t, `[{"name":"a","command":"a"}]`)
	if err := admin.SetWorkdir("a", "relative/dir"); err == nil {
		t.Fatal("相对 workdir 应报错")
	}
	if err := admin.SetRoots("a", []string{"relative"}); err == nil {
		t.Fatal("相对根集项应报错")
	}
	if err := admin.SetCommand("a", "   "); err == nil {
		t.Fatal("空命令应报错")
	}
	for _, err := range []error{
		admin.SetCommand("ghost", "x"),
		admin.SetArgs("ghost", nil),
		admin.SetEnv("ghost", nil),
		admin.SetWorkdir("ghost", ""),
		admin.SetRoots("ghost", nil),
	} {
		if err == nil {
			t.Fatal("未知代理应报错")
		}
	}
	agents := readDelegateAgents(t, path)
	if len(agents) != 1 || agents[0]["command"] != "a" {
		t.Fatalf("校验失败的调用不该改盘: %v", agents)
	}
}
