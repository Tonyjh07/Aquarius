package pathres

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// mustResolve 解析测试路径（TempDir 在 macOS 上经 /var 链接，不解析会与判定结果对不上）。
func mustResolve(t *testing.T, p string) string {
	t.Helper()
	r, err := Resolve(p)
	if err != nil {
		t.Fatalf("resolve %s: %v", p, err)
	}
	return r
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
}

// TestWithinBasics 基本判据：根自身、子路径、边界陷阱（root2 不是 root 的子目录）、反向。
func TestWithinBasics(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	sibling := filepath.Join(base, "root2")
	mkdirs(t, root, sibling, filepath.Join(root, "a"), filepath.Join(sibling, "b"))

	r := mustResolve(t, root)
	rs := mustResolve(t, sibling)
	cases := []struct {
		name   string
		target string
		want   bool
	}{
		{"根自身", r, true},
		{"一级子目录", mustResolve(t, filepath.Join(root, "a")), true},
		{"尚不存在的子路径", mustResolve(t, filepath.Join(root, "new", "f.txt")), true},
		{"前缀相同的兄弟目录", mustResolve(t, filepath.Join(sibling, "b")), false},
		{"带尾分隔符的根", r + string(filepath.Separator), true},
	}
	for _, c := range cases {
		if got := Within(r, filepath.Clean(c.target)); got != c.want {
			t.Fatalf("%s: Within(%s, %s) = %v, want %v", c.name, r, c.target, got, c.want)
		}
	}
	if Within(mustResolve(t, filepath.Join(root, "a")), r) {
		t.Fatal("反向包含不该成立：子目录不能覆盖父目录")
	}
	if rs == "" || !Within(rs, mustResolve(t, filepath.Join(sibling, "b"))) {
		t.Fatal("兄弟根自身应包含其子路径")
	}
}

// TestResolveRetreats 带 .. 的路径按解析结果判定，不按字面。字面写法看起来在根内，
// 回退之后落在根外。
func TestResolveRetreats(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	mkdirs(t, root, filepath.Join(root, "a"))
	outside := filepath.Join(base, "outside")
	mkdirs(t, outside)

	escape := filepath.Join(root, "a", "..", "..", "outside", "f.txt")
	if got := mustResolve(t, escape); Within(mustResolve(t, root), got) {
		t.Fatalf("%s 回退后落在根内？resolved = %s", escape, got)
	}
}

// TestResolveSymlinks 链接不是词法问题：根内链到外部算外，外部链进根内算内，
// 悬空链接按目标（尚不存在）判定。本机无权限创建链接时跳过。
func TestResolveSymlinks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	mkdirs(t, root, outside)

	escape := filepath.Join(root, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Skipf("cannot create symlink here: %v", err)
	}
	enter := filepath.Join(base, "enter")
	if err := os.Symlink(root, enter); err != nil {
		t.Skipf("cannot create symlink here: %v", err)
	}
	dangle := filepath.Join(root, "dangle")
	if err := os.Symlink(filepath.Join(base, "gone"), dangle); err != nil {
		t.Skipf("cannot create symlink here: %v", err)
	}

	r := mustResolve(t, root)
	if Within(r, mustResolve(t, filepath.Join(escape, "f.txt"))) {
		t.Fatal("根内的链接指向外部，落点在外，不应算在根内")
	}
	if Within(r, mustResolve(t, filepath.Join(escape, "new", "f.txt"))) {
		t.Fatal("经链接逃逸的尚不存在路径不应算在根内")
	}
	if !Within(r, mustResolve(t, filepath.Join(enter, "f.txt"))) {
		t.Fatal("外部链接指向根内，落点在根内应命中")
	}
	if Within(r, mustResolve(t, dangle)) {
		t.Fatal("悬空链接指向外部，不应算在根内")
	}
}

// TestResolveJunctionWindows 目录 junction（mklink /J，无需管理员）不被 EvalSymlinks
// 解析，必须按 reparse point 识别——这是 D22 记过的绕过面，闸门同样要挡。
func TestResolveJunctionWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junction only exists on Windows")
	}
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	mkdirs(t, root, outside)
	escape := filepath.Join(root, "jescape")
	if err := exec.Command("cmd", "/c", "mklink", "/J", escape, outside).Run(); err != nil {
		t.Skipf("cannot create junction: %v", err)
	}
	if Within(mustResolve(t, root), mustResolve(t, filepath.Join(escape, "f.txt"))) {
		t.Fatal("根内 junction 指向外部，不应算在根内")
	}
	enter := filepath.Join(base, "jenter")
	if err := exec.Command("cmd", "/c", "mklink", "/J", enter, root).Run(); err != nil {
		t.Skipf("cannot create junction: %v", err)
	}
	if !Within(mustResolve(t, root), mustResolve(t, filepath.Join(enter, "f.txt"))) {
		t.Fatal("外部 junction 指向根内，落点在根内应命中")
	}
}

// TestWithinVolumes Windows 上跨盘符必然不在同一棵树；UNC 共享名不带盘符，也要分清。
func TestWithinVolumes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("volumes only meaningful on Windows")
	}
	if Within(`C:\root`, `D:\root\a`) {
		t.Fatal("跨盘符不应算在根内")
	}
	if Within(`\\srv\share\root`, `\\srv\other\root\a`) {
		t.Fatal("跨 UNC 共享不应算在根内")
	}
	if !Within(`C:\root`, `c:\ROOT\a`) {
		t.Fatal("Windows 上大小写不同仍是同一棵树")
	}
}

// TestResolveEmpty 空路径报错（调用方据此 fail closed），不静默当成当前目录。
func TestResolveEmpty(t *testing.T) {
	if _, err := Resolve(""); err == nil {
		t.Fatal("空路径应报错")
	}
	if _, err := Resolve("   "); err == nil {
		t.Fatal("全是空白的路径应报错")
	}
}

// TestRootSetLocate 根集的三个行为：去重、命中报出是哪个根、空集什么都不许。
func TestRootSetLocate(t *testing.T) {
	base := t.TempDir()
	work := filepath.Join(base, "work")
	extra := filepath.Join(base, "extra")
	other := filepath.Join(base, "other")
	mkdirs(t, work, extra, other)

	set, err := NewRootSet([]string{work, extra, work, "  ", filepath.Join(extra, "..", "extra")})
	if err != nil {
		t.Fatalf("new root set: %v", err)
	}
	if got := len(set.Roots()); got != 2 {
		t.Fatalf("roots = %v, want 2 (去重)", set.Roots())
	}
	resolved, root, err := set.Locate(filepath.Join(extra, "f.txt"))
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if root != mustResolve(t, extra) {
		t.Fatalf("root = %q, want %q", root, mustResolve(t, extra))
	}
	if resolved == "" {
		t.Fatal("命中的目标应回填解析后的路径")
	}
	if _, root, _ := set.Locate(filepath.Join(other, "f.txt")); root != "" {
		t.Fatalf("根外目标不该命中，got root = %q", root)
	}

	empty, err := NewRootSet(nil)
	if err != nil || !empty.Empty() {
		t.Fatalf("empty set = %v, %v", empty.Roots(), err)
	}
	if _, root, _ := empty.Locate(work); root != "" {
		t.Fatal("空根集不应命中任何目标")
	}
}
