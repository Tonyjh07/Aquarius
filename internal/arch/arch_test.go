package arch

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// layer 分层标签（DESIGN §2/§5 的依赖方向：adapter → port ← app → domain）。
//
// 只按目录前缀判定，不按包名——包名可能重名，目录路径唯一。
type layer string

const (
	layerDomain    layer = "domain"
	layerPort      layer = "port"
	layerApp       layer = "app"
	layerAdapter   layer = "adapter"
	layerPlugin    layer = "plugin"
	layerCmd       layer = "cmd"
	layerPluginAPI layer = "pluginapi"
	layerOther     layer = "other" // assets 等非分层包
)

// allowedTargets 每一层允许 import 的**内部**层。
//
// 键缺失 = 不作约束（cmd 是装配根，可 import 全部）。
//
// adapter 之间允许互相引用：`atomicfile`、`llm/tokenizer`、`uigui/platform` 这类
// 下层件本来就是被同层复用的（抽共用件正是消除重复的手段）。真正要禁的是
// **反向依赖**（uigui 被别人依赖）与**跨层**（app 摸 adapter），见 TestNoReverseDependency。
var allowedTargets = map[layer][]layer{
	layerDomain:    {layerDomain},
	layerPort:      {layerDomain},
	layerApp:       {layerDomain, layerPort},
	layerAdapter:   {layerDomain, layerPort, layerAdapter, layerPlugin, layerPluginAPI},
	layerPlugin:    {layerDomain, layerPort, layerPluginAPI},
	layerPluginAPI: {layerPluginAPI},
}

// skippedDirNames 不参与扫描的目录：本地脚手架（不入库）、工具目录、独立子模块。
var skippedDirNames = map[string]bool{
	".git":         true,
	".zcode":       true,
	".idea":        true,
	".vscode":      true,
	"temp":         true,
	"spike":        true,
	"vendor":       true,
	"node_modules": true,
	"testdata":     true,
}

// TestDependencyDirection 分层方向的守门人。
//
// 只约束**生产文件**：测试文件允许借用真实适配器做链路级验收（AGENTS.md「测试要求」），
// 那不是生产代码的跨层直连。
func TestDependencyDirection(t *testing.T) {
	root, modPath := moduleRoot(t)

	var bad []string
	scanned := 0
	walkProd(t, root, modPath, func(file, pkgPath string, imports []string) {
		src := classify(modPath, pkgPath)
		if src == layerOther {
			return
		}
		scanned++
		for _, imp := range imports {
			dst := classify(modPath, imp)
			if dst == layerOther || allows(src, dst) {
				continue
			}
			bad = append(bad, file+": "+string(src)+" 层不得 import "+string(dst)+" 层（"+imp+"）")
		}
	})

	if scanned == 0 {
		t.Fatal("没有扫到任何生产文件，守门测试等于没跑——检查扫描根与跳过规则")
	}
	sort.Strings(bad)
	for _, msg := range bad {
		t.Error(msg)
	}
}

// TestNoReverseDependency 两条现状约束：
//
//   - `cmd` 只进不出：装配根不能被别的包当库引用；
//   - `uigui` 不被其他包依赖：界面适配器一旦成为公共依赖，拆包就再也拆不动了。
func TestNoReverseDependency(t *testing.T) {
	root, modPath := moduleRoot(t)

	const cmdPrefix = "cmd/"
	const uiguiPrefix = "internal/adapter/uigui"

	walkProd(t, root, modPath, func(file, pkgPath string, imports []string) {
		for _, imp := range imports {
			// 只关心本模块内部目标。
			if !strings.HasPrefix(imp, modPath+"/") {
				continue
			}
			relTarget := strings.TrimPrefix(imp, modPath+"/")
			relSource := strings.TrimPrefix(pkgPath, modPath+"/")

			if strings.HasPrefix(relTarget, cmdPrefix) && !strings.HasPrefix(relSource, cmdPrefix) {
				t.Errorf("%s: %s 引用了装配根 %s（cmd 只进不出）", file, relSource, relTarget)
			}
			// uigui 只允许装配根（cmd）与自身内部使用；internal/* 引用它就是反向依赖。
			if strings.HasPrefix(relTarget, uiguiPrefix) &&
				!strings.HasPrefix(relSource, uiguiPrefix) &&
				!strings.HasPrefix(relSource, cmdPrefix) {
				t.Errorf("%s: %s 引用了 %s（界面适配器不能成为公共依赖）", file, relSource, relTarget)
			}
		}
	})
}

// TestGuardCatchesViolation 自检：故意造违规边，判定必须为假。
//
// 守门测试最大的失败模式是「永远绿」——规则写错、扫描根找错、跳过规则吃掉整棵树，
// 都会让它安静地什么都不做。这里直接对判定函数喂正反例。
func TestGuardCatchesViolation(t *testing.T) {
	cases := []struct {
		src, dst layer
		want     bool // true = 允许
	}{
		{layerApp, layerAdapter, false},
		{layerApp, layerPort, true},
		{layerApp, layerDomain, true},
		{layerPort, layerApp, false},
		{layerPort, layerAdapter, false},
		{layerDomain, layerPort, false},
		{layerDomain, layerAdapter, false},
		{layerDomain, layerDomain, true},
		{layerAdapter, layerAdapter, true},
		{layerAdapter, layerPort, true},
		{layerAdapter, layerApp, false},
		{layerPlugin, layerAdapter, false},
		{layerPlugin, layerDomain, true},
		{layerCmd, layerAdapter, true},
		{layerCmd, layerApp, true},
	}
	for _, c := range cases {
		if got := allows(c.src, c.dst); got != c.want {
			t.Errorf("allows(%s, %s) = %v, want %v", c.src, c.dst, got, c.want)
		}
	}
}

// allows 判定一条内部依赖边是否合法。
func allows(src, dst layer) bool {
	if src == layerCmd {
		return true // 装配根可以 import 全部
	}
	for _, ok := range allowedTargets[src] {
		if ok == dst {
			return true
		}
	}
	return false
}

// classify 把 import 路径映射到分层标签；非本模块内部包 = layerOther。
func classify(modPath, importPath string) layer {
	prefixes := []struct {
		prefix string
		l      layer
	}{
		{modPath + "/internal/domain", layerDomain},
		{modPath + "/internal/port", layerPort},
		{modPath + "/internal/app", layerApp},
		{modPath + "/internal/adapter", layerAdapter},
		{modPath + "/internal/plugin", layerPlugin},
		{modPath + "/cmd", layerCmd},
		{modPath + "/pluginapi", layerPluginAPI},
	}
	for _, p := range prefixes {
		if importPath == p.prefix || strings.HasPrefix(importPath, p.prefix+"/") {
			return p.l
		}
	}
	return layerOther
}

// walkProd 遍历全仓生产文件（跳过测试文件、脚手架与独立子模块）。
//
// file 是相对仓库根的斜杠路径；pkgPath 是该文件所属包的 import 路径。
func walkProd(t *testing.T, root, modPath string, fn func(file, pkgPath string, imports []string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skippedDirNames[d.Name()] {
				return filepath.SkipDir
			}
			// 自带 go.mod 的目录属于另一套依赖图（如 spike 下的试验模块）。
			if path != root {
				if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relDir, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		pkgPath := modPath
		if relDir != "." {
			pkgPath = modPath + "/" + filepath.ToSlash(relDir)
		}
		relFile, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		fn(filepath.ToSlash(relFile), pkgPath, importsOf(t, path))
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 %s 失败: %v", root, err)
	}
}

// importsOf 解析单个文件的 import 列表（只用 ImportsOnly，不建完整 AST）。
func importsOf(t *testing.T, path string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", path, err)
	}
	out := make([]string, 0, len(f.Imports))
	for _, spec := range f.Imports {
		p, unquoteErr := strconv.Unquote(spec.Path.Value)
		if unquoteErr != nil {
			t.Fatalf("%s: 无法解析 import 字面量 %s: %v", path, spec.Path.Value, unquoteErr)
		}
		out = append(out, p)
	}
	return out
}

// moduleRoot 从当前目录向上找 go.mod，返回仓库根与模块路径。
func moduleRoot(t *testing.T) (root, modPath string) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败: %v", err)
	}
	for {
		candidate := filepath.Join(dir, "go.mod")
		if data, readErr := os.ReadFile(candidate); readErr == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					return dir, strings.TrimSpace(strings.TrimPrefix(line, "module "))
				}
			}
			t.Fatalf("%s 缺 module 行", candidate)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("从 %s 向上没找到 go.mod", dir)
		}
		dir = parent
	}
}
