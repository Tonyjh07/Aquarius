package pluginapi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// independence_test 稳定契约的独立性自查（plugin-design.md §1 方向规则的 v1 侧）：
// 本包不得 import internal/…（与内核类型解耦），不得 import 三方模块（零依赖契约，
// 插件作者 go get 本包时不应拖进任何传递依赖）。内核侧的对应规则（internal 不 import
// v1 之外还受 layerPluginAPI 依赖层约束）由 internal/arch 的 arch 测试守着。
func TestIndependence(t *testing.T) {
	// 标准库前缀白名单（go.mod 无 require，理论上不可能出现三方，这里再锁一道）。
	allowPrefix := []string{"time", "strings", "errors", "os", "regexp", "testing", "path/filepath", "fmt", "context", "io", "encoding/json", "sort", "slices", "reflect"}
	importRe := regexp.MustCompile(`(?m)^\s*"([a-zA-Z0-9_./\-]+)"\s*$`)
	return1 := regexp.MustCompile(`(?m)^\s*(?:\w[\w ]*,?\s*)?"([a-zA-Z0-9_./\-]+)"\s*$`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 3 {
		t.Fatalf("包内文件数异常（%d）：目录结构变了就核对这条测试", len(files))
	}
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range importRe.FindAllStringSubmatch(string(src), -1) {
			checkImport(t, f, m[1], allowPrefix)
		}
		// 多行 import 块的宽松兜底：任何以引号结尾的裸路径行。
		for _, m := range return1.FindAllStringSubmatch(string(src), -1) {
			checkImport(t, f, m[1], allowPrefix)
		}
	}
}

func checkImport(t *testing.T, file, path string, allow []string) {
	t.Helper()
	if strings.Contains(path, "internal/") || strings.HasPrefix(path, "internal") {
		t.Errorf("%s: 稳定契约不得依赖内核内部包: %q", file, path)
		return
	}
	if !strings.Contains(path, ".") {
		return // 标准库（无域名点号的路径）
	}
	for _, a := range allow {
		if path == a || strings.HasPrefix(path, a+"/") {
			return
		}
	}
	t.Errorf("%s: 稳定契约零依赖（只许标准库）: %q", file, path)
}
