package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestPluginHostGivesDataDirAndLocale 宿主真的把目录与语言交出去（D341）：契约里写的
// 「跨重启存活」「当前界面语言」只有在装配根填了值之后才成立。
func TestPluginHostGivesDataDirAndLocale(t *testing.T) {
	w := &wiring{profileDir: t.TempDir(), lang: i18n.LangEn}
	h := w.pluginHostFor(nil)("memplug")

	data := h.DataDir()
	if data == "" {
		t.Fatal("持久目录应已给出")
	}
	if fi, err := os.Stat(data); err != nil || !fi.IsDir() {
		t.Fatalf("持久目录应已建好：%v", err)
	}
	if !strings.HasPrefix(data, w.profileDir) {
		t.Fatalf("持久目录应在 profile 下，得到 %q", data)
	}
	if work := h.WorkDir(); work == "" || !strings.HasPrefix(work, w.profileDir) {
		t.Fatalf("临时目录应在 profile 下，得到 %q", work)
	}
	if got := h.Locale(); got != string(i18n.LangEn) {
		t.Fatalf("语言应取启动时生效的那一种，得到 %q", got)
	}
}

// TestPluginWorkDirClearedPerInstance 临时目录换实例就清空；持久目录不跟着清。
func TestPluginWorkDirClearedPerInstance(t *testing.T) {
	w := &wiring{profileDir: t.TempDir(), lang: i18n.LangZh}
	newHost := w.pluginHostFor(nil)
	first := newHost("p")

	stale := filepath.Join(first.WorkDir(), "half-written.idx")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(first.DataDir(), "mem.db")
	if err := os.WriteFile(kept, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := newHost("p")
	if second.WorkDir() != first.WorkDir() {
		t.Fatalf("同一实例的临时目录应稳定：%q vs %q", first.WorkDir(), second.WorkDir())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("新实例的临时目录应是空的，残留仍在：%v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("持久目录不应被清理：%v", err)
	}
}

// TestTakenNamesCoversBuiltinAndMCP 名字占用判定要覆盖内置工具与 config 里的 MCP server：
// 漏掉哪一边，撞名的插件都会被放进模型命名空间。
func TestTakenNamesCoversBuiltinAndMCP(t *testing.T) {
	w := &wiring{cfg: &fileConfig{MCPServers: map[string]plugin.MCPServer{"github": {}}}}
	taken := w.takenNames([]port.Tool{stubTool{name: "think"}})

	for _, name := range []string{"think", "github"} {
		if !taken(name) {
			t.Fatalf("%q 应判为已占用", name)
		}
	}
	if taken("memplug") {
		t.Fatal("没被占用的名字不该拒")
	}
}

// stubTool 只提供名字：占用判定只看 Name()。
type stubTool struct{ name string }

func (s stubTool) Spec() tool.Spec { return tool.Spec{Name: s.name} }

func (s stubTool) Execute(context.Context, tool.Call) (tool.Result, error) {
	return tool.Result{}, nil
}
