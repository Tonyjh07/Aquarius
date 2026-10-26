package uigui

// 字体集合共享的守卫（D240）。
//
// 背景：解析一份中文字体约 75～80MB，而解析结果（字体表与字形轮廓）在 ParseCollection
// 返回后完全只读。每个窗口各解析一遍是纯重复，改成进程内共享一份。这里守两件事：
//  1. 同一候选表只解析一次、各窗拿到的是**同一份底层数组**（真共享，不是「结果相同」）；
//  2. 交给 Shaper 的切片**容量被封住**，调用方 append 不会改写共享内容。
//
// 第 2 条不是洁癖：`text.NewShaper(text.WithCollection(...))` 收的是切片，若某处
// 顺手 `append` 就会在共享底层数组上就地写入，把别的窗口的字体集合改掉——那是很难
// 复现的渲染错乱。gofont.Collection 出于同样理由也做了封容量。
//
// 不需要真实字形栅格化，普通单测即可；无 CJK 系统字体时用 gofont 回落路径，断言同样成立。

import (
	"testing"
)

// TestSharedFacesSameCollection 同一候选表两次取用必须是同一份底层数组。
func TestSharedFacesSameCollection(t *testing.T) {
	cands := newPlatform(Options{}, true).SystemFontCandidates()
	a := sharedFaces(cands)
	b := sharedFaces(cands)
	if len(a) == 0 || len(b) == 0 {
		t.Fatal("字体集合为空（解析与回落都失败）")
	}
	if len(a) != len(b) {
		t.Fatalf("两次取用长度不同：%d vs %d", len(a), len(b))
	}
	// 比较首元素地址即可判定是否同一份底层数组（元素是值类型，非指针）。
	if &a[0] != &b[0] {
		t.Fatal("同一候选表拿到了两份字体集合：缓存没生效，每窗仍在重复解析")
	}
}

// TestSharedFacesCapacityClamped 共享集合的容量必须等于长度（append 会新分配）。
func TestSharedFacesCapacityClamped(t *testing.T) {
	cands := newPlatform(Options{}, true).SystemFontCandidates()
	faces := sharedFaces(cands)
	if cap(faces) != len(faces) {
		t.Fatalf("共享集合容量未封住：len=%d cap=%d（调用方 append 会改写共享底层数组）",
			len(faces), cap(faces))
	}
	// 真做一次 append，确认它另起了底层数组、原集合不受影响。
	before := len(faces)
	more := append(faces, faces[0])
	if len(faces) != before {
		t.Fatalf("append 改动了共享集合长度：%d → %d", before, len(faces))
	}
	if &more[0] == &faces[0] {
		t.Fatal("append 复用了共享底层数组")
	}
}

// TestSharedFacesDistinctCandidates 候选表不同则各存一档，不能串味。
//
// 生产所有窗口走同一个平台实例、候选表恒定，正常路径只有一份；这条守的是测试与多平台
// 场景：候选表内容变了就必须重新解析，否则会拿到别人的字体。
//
// 注意回落路径：候选全缺失时两边都回落到 gofont 的那份**进程级单例**，底层数组相同是
// 正确的（gofont.Collection 本身即共享返回）。所以这里断言的是**缓存分了档**，而不是
// 「两份解析结果的地址一定不同」。
func TestSharedFacesDistinctCandidates(t *testing.T) {
	sharedFaces([]string{"/nonexistent/a.ttf"})
	sharedFaces([]string{"/nonexistent/b.ttf"})

	faceCache.mu.Lock()
	n := len(faceCache.byKey)
	faceCache.mu.Unlock()
	if n < 2 {
		t.Fatalf("不同候选表只存了 %d 档：缓存键没区分内容", n)
	}

	// 同一组候选（顺序一致）再取一次，仍应命中同一档、不分出新档。
	before := n
	a := sharedFaces([]string{"/nonexistent/a.ttf"})
	if a == nil || len(a) == 0 {
		t.Fatal("候选缺失时应回落 gofont，不该为空")
	}
	faceCache.mu.Lock()
	after := len(faceCache.byKey)
	faceCache.mu.Unlock()
	if after != before {
		t.Fatalf("同一候选表第二次取用又多存了一档（%d → %d）", before, after)
	}
}

// TestNewThemeSharesFaces 两个主题必须共用一份字体集合（生产每窗各建主题的真实路径）。
func TestNewThemeSharesFaces(t *testing.T) {
	cands := newPlatform(Options{}, true).SystemFontCandidates()
	th1 := newTheme(cands)
	th2 := newTheme(cands)
	if th1.Shaper == nil || th2.Shaper == nil {
		t.Fatal("主题缺 Shaper")
	}
	if th1.Shaper == th2.Shaper {
		t.Fatal("两个主题共用了同一个 Shaper：每窗应各建一份（首次排版的数据按 Shaper 存）")
	}
	faces := sharedFaces(cands)
	if cap(faces) != len(faces) {
		t.Fatalf("newTheme 之后共享集合的容量被改动了：len=%d cap=%d", len(faces), cap(faces))
	}
}
