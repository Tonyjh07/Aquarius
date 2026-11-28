package main

// plugin_e2e_test.go 样例插件四层验收（plugin-design.md §6 口径：跑 aqbuild 得到带插件的
// 真二进制，不是进程内组装）：aqbuild 构建宿主 + aq-memory-plugin 的真 memplug，假 LLM
// 脚本流驱动，从 stdout、请求体与文件系统断言——
// ① 管理面：/plugin 列出 binary/in-process 的 memory 插件；
// ② 供给面 + Host 消费面：模型面出现 mem_* 工具声明、内置 memory_* 四工具撤下（接管），
//    mem_tree 调用回填成功、mem_write 落盘；
// ③ 命令面：/mem list 读回刚写的记忆点；
// ④ 生命周期：/quit 干净退出，重启后启用状态与记忆库都恢复。
// 样例仓库不在（独立仓库，不入宿主管辖）或 -short 时跳过。

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pluginE2ERepo 样例插件仓库根：宿主仓库的邻居（相对定位，不写死盘符）。
func pluginE2ERepo(t *testing.T) string {
	t.Helper()
	plug, err := filepath.Abs(filepath.Join("..", "..", "..", "aq-memory-plugin"))
	if err != nil {
		t.Skipf("解析样例插件路径: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plug, "go.mod")); err != nil {
		t.Skipf("样例插件仓库不在（%s），跳过真二进制验收", plug)
	}
	return plug
}

// pluginE2EBuild 用 aqbuild 构建真宿主+真插件二进制；宿主工作副本零改动是 aqbuild 的
// 承诺（plugin-guide.md §10.1），构建前后 git status 必须一致——顺手在这里锁住。
func pluginE2EBuild(t *testing.T, plug, out string) {
	t.Helper()
	repoRoot := "../.."
	gitStatus := func() string {
		cmd := exec.Command("git", "status", "--porcelain")
		cmd.Dir = repoRoot
		b, err := cmd.Output()
		if err != nil {
			t.Fatalf("git status: %v", err)
		}
		return string(b)
	}
	before := gitStatus()
	cmd := exec.Command("go", "run", "github.com/Tonyjh07/Aquarius/cmd/aqbuild", "-plugin", plug, "-o", out)
	cmd.Dir = repoRoot
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("aqbuild: %v: %s", err, b)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("产物不存在: %v", err)
	}
	if after := gitStatus(); after != before {
		t.Fatalf("aqbuild 改了宿主工作副本:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// pluginE2ERun 驱动构建出的二进制：stdin 全量喂入（repl 逐行消费），收 stdout，退出码必须 0。
func pluginE2ERun(t *testing.T, bin, dataDir, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := exec.Command(bin, "-data", dataDir)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.Env = append(os.Environ(), "AQ_E2E_KEY=test-key")
	if err := cmd.Run(); err != nil {
		t.Fatalf("运行二进制: %v\nstdout:\n%s", err, out.String())
	}
	return out.String()
}

// pluginE2EConfig 最小可运行 profile 配置，模型指到假 LLM；返回 profile 目录。
func pluginE2EConfig(t *testing.T, dir, llmURL string) string {
	t.Helper()
	cfg := fmt.Sprintf(`{
  "model": {"providers":[{"name":"openai-compatible","base_url":%q,"keys":[{"api_key":"secret:AQ_E2E_KEY"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui": {"kind":"repl"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, llmURL)
	prof := writeProfileLayout(t, dir, cfg)
	t.Setenv("AQ_E2E_KEY", "test-key")
	return prof
}
func TestPluginE2EFullChain(t *testing.T) {
	if testing.Short() {
		t.Skip("真二进制构建耗时，-short 跳过")
	}
	plug := pluginE2ERepo(t)
	bin := filepath.Join(t.TempDir(), "aquarius.exe")
	pluginE2EBuild(t, plug, bin)

	// 第一段：启用 → 工具调用（查树）→ 写入 → /mem 读回 → 退出。
	srv, reqs := rawScriptServer(t, [][]string{
		{toolCallData("c1", "mem_tree", `{}`)},
		{contentData("看过树了")},
		{toolCallData("c2", "mem_write", `{"title":"hello point","body":"first body"}`)},
		{contentData("写好了")},
	})
	dir := t.TempDir()
	prof := pluginE2EConfig(t, dir, srv.URL)

	stdin := "/plugin\n/plugin enable memory\n帮我看看记忆树\n帮我写个记忆点\n/mem list\n/quit\n"
	got := pluginE2ERun(t, bin, dir, stdin)

	// ① 管理面：binary/in-process + 供给面清单 + 启用生效。
	for _, want := range []string{"memory", "binary/in-process", "memory,tool,command,view,settings", "active"} {
		if !strings.Contains(got, want) {
			t.Fatalf("管理面 stdout 缺 %q:\n%s", want, got)
		}
	}
	// ② 供给面 + 接管：请求 1 的模型面有 mem_* 工具、内置 memory_* 四工具撤下。
	if !strings.Contains(string(reqs.at(0).body), "mem_tree") {
		t.Fatalf("请求 1 缺插件工具声明: %.400s", reqs.at(0).body)
	}
	for _, gone := range []string{"memory_list", "memory_read", "memory_search", "memory_write"} {
		if strings.Contains(string(reqs.at(0).body), gone) {
			t.Fatalf("接管后内置 %s 仍在模型面: %.400s", gone, reqs.at(0).body)
		}
	}
	// ② 回填：请求 2 是 mem_tree 的结果（脚本轮 2 答正文的前提）。
	if !strings.Contains(string(reqs.at(1).body), "mem_tree") {
		t.Fatalf("请求 2 缺 mem_tree 回填: %.400s", reqs.at(1).body)
	}
	if !strings.Contains(got, "[tool ok]") {
		t.Fatalf("工具执行回填不在 stdout:\n%s", got)
	}
	// ③ 命令面：/mem list 读回刚写的条目。
	if !strings.Contains(got, "hello point") {
		t.Fatalf("/mem list 缺刚写的记忆点:\n%s", got)
	}
	// ② 持久化：插件数据目录有记忆点（store/layer-N/<id>.md）与设置，启用状态写回 profile 配置。
	storeDir := filepath.Join(prof, "plugin-data", "memory", "store")
	points := 0
	err := filepath.WalkDir(storeDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			points++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("插件 store 目录: %v", err)
	}
	if points == 0 {
		t.Fatalf("store 无记忆点落盘: %s", storeDir)
	}
	if !strings.Contains(readPluginE2EFile(t, filepath.Join(prof, "config.json")), `"enabled": true`) {
		t.Fatalf("启用状态未写回 profile 配置")
	}

	// 第二段：重启——启用状态与记忆库都恢复（生命周期），mem_tree 再调一次。
	srv2, reqs2 := rawScriptServer(t, [][]string{
		{toolCallData("c1", "mem_tree", `{}`)},
		{contentData("restored tree")},
	})
	pluginE2EConfig(t, dir, srv2.URL)
	got2 := pluginE2ERun(t, bin, dir, "重启后再看看\n/quit\n")
	if !strings.Contains(got2, "restored tree") || !strings.Contains(got2, "hello point") {
		t.Fatalf("重启后恢复断言失败:\n%s", got2)
	}
	if !strings.Contains(string(reqs2.at(1).body), "hello point") {
		t.Fatalf("重启后 mem_tree 回填缺已有记忆点: %.400s", reqs2.at(1).body)
	}
}

func readPluginE2EFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读 %s: %v", p, err)
	}
	return string(b)
}
