package uigui

// memprofile_test.go 常驻内存的分阶段量化（诊断工具，非常规门禁）。
//
// 手测只能看到进程总量，说不清「谁占的」。这里用仓库已有的 headless 帧驱动，把每类负载
// 各自跑一遍——每跑完一步就强制 GC 一次，读 MemStats 的 HeapAlloc（**存活堆**，不是累计
// 分配），与上一步做差，并把整份堆快照落盘（pprof 可看函数/类型分布）。
//
// 启用（默认 skip，不拖慢门禁）：
//
//	set AQUARIUS_MEMPROFILE=1
//	go test ./internal/adapter/uigui/ -run TestMemProfile -v -timeout 30m
//
// 快照写在工作目录的 memprofile/ 下（AQ_MEMPROFILE_DIR 可改）：每步一份 heap.pprof、
// 一份 goro.pprof 与一行 memstats.log。看分布用：
//
//	go tool pprof -sample_index=inuse_space <dir>/<step>-heap.pprof
//	go tool pprof -sample_index=inuse_space -base <dir>/<prev>.pprof <dir>/<step>.pprof
//
// 口径：
//   - 每步先 runtime.GC() 两次再读 HeapAlloc——存活对象才是「占着不放」的部分；累计分配
//     （TotalAlloc）把瞬时垃圾也算进来，无法归因。
//   - **所有被测对象挂到 memRoot 保活**：Go 的变量活跃期到「最后一次使用」为止，不显式保活
//     的话前一个窗口在下一步 probe 之前就被回收了，读数会来回跳（踩过：118MB → 6MB）。
//   - 步骤是**累加**的（前一个窗口不关就开下一个），与真实使用一致；增量为相邻两步之差。
//   - 只覆盖 UI 状态机与离屏渲染，不含真窗口的 D3D11 交换链、托盘与热键——那部分是进程
//     RSS 里的非堆，pprof 看不到，读总量时要与这里的堆数字分开算。
//
// 两个用例分工：
//   - TestMemProfileStages  按「用户操作」分步，回答「哪类操作涨内存」。
//   - TestMemProfileFont    按「装配步骤」拆字体成本，回答「为什么涨」，并验证共享口径。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/text"

	"github.com/Tonyjh07/Aquarius/internal/adapter/llm/tokenizer"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// memRoot 保活所有测量对象（见文件头「口径」第二条）。包级变量，GC 视为根。
var memRoot []any

// memKeep 把对象挂到根上。
func memKeep(objs ...any) { memRoot = append(memRoot, objs...) }

// memProbe 强制 GC 后读一次存活堆。连续两次 GC：第一次可能只清掉上一轮的浮动垃圾。
func memProbe() (live, sys uint64, goros int) {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc, ms.Sys, runtime.NumGoroutine()
}

// memSnapshot 落一份堆与 goroutine 快照（pprof 用 inuse_space 看存活分布）。
func memSnapshot(t *testing.T, dir, stage string) {
	t.Helper()
	if f, err := os.Create(filepath.Join(dir, stage+"-heap.pprof")); err == nil {
		_ = pprof.WriteHeapProfile(f) // 内部会再触发一次 GC，取的是存活对象
		_ = f.Close()
	} else {
		t.Logf("写堆快照失败: %v", err)
	}
	if f, err := os.Create(filepath.Join(dir, stage+"-goro.pprof")); err == nil {
		_ = pprof.Lookup("goroutine").WriteTo(f, 0)
		_ = f.Close()
	}
}

// profileDir 快照目录（默认工作目录下的 memprofile/）。
func profileDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("AQ_MEMPROFILE_DIR")
	if dir == "" {
		dir = "memprofile"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建快照目录: %v", err)
	}
	return dir
}

// memStage 一个测量步骤的读数。
type memStage struct {
	name   string
	live   uint64 // 强制 GC 后的 HeapAlloc
	delta  int64  // 相对上一步
	sysMem uint64 // 运行时管理的总内存
	goros  int
}

// memTracker 逐步打点：每打一次点就落快照并记一行（日志 + t.Log 双写）。
type memTracker struct {
	t      *testing.T
	dir    string
	log    *os.File
	prev   uint64
	stages []memStage
}

func newMemTracker(t *testing.T, dir, logName string) *memTracker {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, logName))
	if err != nil {
		t.Fatalf("建日志: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return &memTracker{t: t, dir: dir, log: f}
}

// probe 打一个点（name 同时用作快照文件名，别带空格与斜杠）。
func (m *memTracker) probe(name string) memStage {
	m.t.Helper()
	live, sys, goros := memProbe()
	st := memStage{name: name, live: live, sysMem: sys, goros: goros, delta: int64(live) - int64(m.prev)}
	m.prev = live
	m.stages = append(m.stages, st)
	memSnapshot(m.t, m.dir, name)
	line := fmt.Sprintf("%-32s live=%7.1fMB  delta=%+7.1fMB  sys=%7.1fMB  goroutines=%d\n",
		name, float64(live)/(1<<20), float64(st.delta)/(1<<20), float64(sys)/(1<<20), goros)
	fmt.Fprint(m.log, line)
	m.t.Log(strings.TrimRight(line, "\n"))
	return st
}

// total 相对第一步的净增。
func (m *memTracker) total() int64 {
	if len(m.stages) == 0 {
		return 0
	}
	return int64(m.prev) - int64(m.stages[0].live)
}

// TestMemProfileStages 按用户操作分步，量「哪类操作涨内存」。
// 关键是**把建主题与建窗口状态分开打点**：否则「开一个窗口 +75MB」会被误读成窗口本身贵。
func TestMemProfileStages(t *testing.T) {
	if os.Getenv("AQUARIUS_MEMPROFILE") == "" {
		t.Skip("分阶段内存剖析（AQUARIUS_MEMPROFILE=1 启用，输出写 memprofile/）")
	}
	dir := profileDir(t)
	m := newMemTracker(t, dir, "memstats.log")

	m.probe("00-baseline")

	// 主窗：一个主题。
	thMain := testTheme()
	memKeep(thMain)
	m.probe("01-main-theme")

	// 主窗状态机（模型 + 输入框；不含离屏渲染缓冲）。
	u := newFrameUI()
	u.th = thMain
	u.inCh = make(chan port.UserInput, 8)
	u.inbox = make(chan uiMsg, 8)
	u.editor.SingleLine = true
	memKeep(u)
	m.probe("02-main-state")

	// 主窗首帧：含离屏全帧合成与**首次文字排版**（字体表的惰性解析在这一步发生）。
	q := new(input.Router)
	mainFrame := func() {
		gtx, ops := frameGtx(q.Source())
		u.frame(gtx, func() {})
		q.Frame(ops)
	}
	memKeep(q, mainFrame)
	mainFrame()
	m.probe("03-main-first-frame")

	// 四十轮对话（贴近真实历史的转写区；验证「内容本身贵不贵」）。
	for i := 0; i < 40; i++ {
		u.m.add(blockAssistant, "这是一段用于测量布局与字形缓存的回答正文，长度接近真实回复。")
		u.m.add(blockUser, "再问一个短一点的问题。")
	}
	mainFrame()
	m.probe("04-main-40-turns")

	// 历史窗：`newHistUI` 内部自带一份主题（与生产 winmgr 同构：每窗独立主题实例），
	// 所以这里不再另建，否则会重复计数。
	tree := fakeRelTree{g: relFixtureGraph(), texts: map[conversation.MessageID]string{}}
	for _, n := range relFixtureGraph().Nodes {
		tree.texts[n.ID] = relFixtureBody(n.ID)
	}
	hu := newHistUI(tree, fakeLister(nil))
	memKeep(hu)
	m.probe("05-history-theme")

	st := newHistoryState(hu)
	hq := new(input.Router)
	memKeep(st, hq)
	relGFrame(hq, hu, st)
	relDriveQuiet(hq, hu, st, relSettleFrames*3)
	m.probe("06-history-state+graph")

	// 关系图拖节点：一次按下 + 三十帧移动 + 抬起（真实拖拽分配路径）。
	if g := st.graph; g != nil && g.sol != nil && g.anchor >= 0 {
		sx, _ := relAnchorScreen(t, g)
		hq.Queue(relPointer(pointer.Press, sx, true))
		relGFrame(hq, hu, st)
		at := f32.Pt(sx.X, sx.Y)
		for i := 0; i < 30; i++ {
			at = f32.Pt(at.X+4, at.Y+2)
			hq.Queue(relPointer(pointer.Move, at, true))
			relGFrame(hq, hu, st)
		}
		hq.Queue(relPointer(pointer.Release, at, false))
		relGFrame(hq, hu, st)
		if g.dragNode >= 0 {
			t.Logf("拖拽后 dragNode 未清: %d", g.dragNode)
		}
	}
	m.probe("07-history-drag")

	// 设置窗：先主题，再表单与逐页翻页。
	thSet := testTheme()
	memKeep(thSet)
	m.probe("08-settings-theme")

	su := newHeadless(t, Options{
		Settings: func() SettingsSnapshot {
			return SettingsSnapshot{Permission: "full-access", Model: "deepseek-flash", Theme: "system", Scale: 1.0, FontSize: 15}
		},
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		Profiles:      &fakeProfiles{snap: ProfilesSnapshot{Current: "default"}},
	})
	sf := newSettingsForm(su)
	sq := new(input.Router)
	memKeep(su, sf, sq)
	settingsFrameOnce := func(page int) {
		sf.page = page
		gtx, ops := frameGtxSize(sq.Source(), 900, 700)
		settingsFrame(gtx, thSet, su, sf)
		sq.Frame(ops)
	}
	settingsFrameOnce(0)
	m.probe("09-settings-state")

	for p := 0; p < settingsPageCount; p++ { // 逐页翻一遍（行计划与控件重建）
		settingsFrameOnce(p)
	}
	m.probe("10-settings-all-pages")

	// 展开/收起：先主题（夹具自带），再十次往返。
	eu, eq := newExpandUI(t)
	memKeep(eu, eq)
	m.probe("11-expand-theme+state")
	for i := 0; i < 10; i++ {
		eu.setExpanded(true)
		expandFrame(eq, eu)
		eu.setExpanded(false)
		expandFrame(eq, eu)
	}
	m.probe("12-expand-toggle-10x")

	out := fmt.Sprintf("\ntotal growth = %+.1fMB (baseline %.1fMB -> final %.1fMB)\n",
		float64(m.total())/(1<<20), float64(m.stages[0].live)/(1<<20), float64(m.prev)/(1<<20))
	fmt.Fprint(m.log, out)
	t.Log(strings.TrimSpace(out))
}

// TestMemProfileFont 拆字体成本，验证共享口径。生产里每个顶层窗各调一次 newTheme，
// 所以这一步的增量就是「多开一个窗口的固定成本」。
//
//	A  只解析字体文件（opentype.ParseCollection）
//	B  在 A 之上建 Shaper（默认口径：另扫整机字体库）
//	C  在 B 之上首次排版（字体表的惰性解析在此发生）
//	D  第二个 Shaper **复用同一份字体集合**（多窗共享的唯一改动）
//	E  D 之上首次排版（验证惰性解析的数据也可共享）
//	F  另解析一份集合再建 Shaper（= 生产当前的每窗成本）
func TestMemProfileFont(t *testing.T) {
	if os.Getenv("AQUARIUS_MEMPROFILE") == "" {
		t.Skip("字体成本拆解（AQUARIUS_MEMPROFILE=1 启用）")
	}
	dir := profileDir(t)
	m := newMemTracker(t, dir, "font-cost.log")
	m.probe("00-baseline")

	cands := newPlatform(Options{}, true).SystemFontCandidates()
	t.Logf("系统字体候选 %d 个: %v", len(cands), cands)
	memKeep(cands)

	// A：只解析字体文件。
	faces := loadCJKFaces(cands)
	if len(faces) == 0 {
		t.Fatal("字体候选全部解析失败，实验前提不成立")
	}
	memKeep(faces)
	m.probe("A-parse-collection")

	// B：建 Shaper（默认口径，会再扫整机字体库——那是粗体面的来源）。
	sh1 := text.NewShaper(text.WithCollection(faces))
	memKeep(sh1)
	m.probe("B-shaper")

	// C：首次排版。
	var ops1 = layoutOpsFor(t, sh1)
	memKeep(ops1)
	m.probe("C-shaper-first-layout")

	// D：第二个 Shaper 复用同一份集合（多窗共享）。
	sh2 := text.NewShaper(text.WithCollection(faces))
	memKeep(sh2)
	m.probe("D-second-shaper-shared")

	// E：共享集合的第二个 Shaper 首次排版。
	ops2 := layoutOpsFor(t, sh2)
	memKeep(ops2)
	m.probe("E-second-shaper-first-layout")

	// F：另解析一份 + 建 Shaper = 生产当前「每窗一遍 newTheme」的成本。
	faces2 := loadCJKFaces(cands)
	memKeep(faces2)
	sh3 := text.NewShaper(text.WithCollection(faces2))
	memKeep(sh3)
	m.probe("F-second-window-full-cost")

	// G：tokenizer（真实配置里指向 testdata/tokenizer.json，6.07MB 文件）。
	tok, err := tokenizer.Load(filepath.Join("..", "..", "..", "internal", "adapter", "llm", "tokenizer", "testdata", "tokenizer.json"))
	if err != nil {
		t.Logf("tokenizer 未加载（跳过这一档）: %v", err)
	} else {
		memKeep(tok)
		m.probe("G-tokenizer")
	}

	// H：gofont 集合（CJK 字体缺失时的回退口径，也是等宽面的来源）。
	mono := monoFontFaces()
	memKeep(mono)
	m.probe("H-gofont-mono-faces")

	// I/J：`NoSystemFonts` 路线的真实收益。必须用**全新解析**的集合，否则前面几档已经
	// 把字体表解析进共享对象，差异会被稀释成 0（第一次量就踩了这个坑）。
	facesNoSys := loadCJKFaces(cands)
	memKeep(facesNoSys)
	m.probe("I-fresh-collection-for-nosys")
	shNoSys := text.NewShaper(text.WithCollection(facesNoSys), text.NoSystemFonts())
	memKeep(shNoSys)
	m.probe("J-nosys-shaper")
	ops3 := layoutOpsFor(t, shNoSys)
	memKeep(ops3)
	m.probe("K-nosys-first-layout")

	// L：与 J/K 对称的对照——全新集合 + 默认 Shaper + 首次排版（含整机字体扫描）。
	facesSys := loadCJKFaces(cands)
	memKeep(facesSys)
	m.probe("L-fresh-collection-for-default")
	shSys := text.NewShaper(text.WithCollection(facesSys))
	memKeep(shSys)
	m.probe("M-default-shaper")
	ops4 := layoutOpsFor(t, shSys)
	memKeep(ops4)
	m.probe("N-default-first-layout")

	t.Logf("明细写入 %s/font-cost.log", dir)
}

// layoutOpsFor 用给定 Shaper 排一段中英混排（触发字体表的惰性解析与实际字形取用）。
func layoutOpsFor(t *testing.T, sh *text.Shaper) *strings.Reader {
	t.Helper()
	sh.LayoutString(text.Parameters{Font: font.Font{Typeface: "Microsoft YaHei"}, PxPerEm: 20}, "共用的字体集合 sharing one font collection 123")
	r := strings.NewReader("")
	return r
}
