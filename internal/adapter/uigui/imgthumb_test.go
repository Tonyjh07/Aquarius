package uigui

// imgthumb_test.go D207 转写区图片缩略图与预览窗：数据面（纯图消息成块、分段图序、
// 缓存去重/墓碑/淘汰）、渲染面（气泡与 chip 展开体的图格、点击件）、预览窗状态
// （适配比例、平移夹取、光标锚定缩放）。真实次窗路径由手工验收覆盖（GUI 不进 CI）。

import (
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/op/paint"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// imgPart 造一段图片分片（已入库引用）。
func imgPart(hash, name string) conversation.Part {
	return conversation.Part{
		Kind: conversation.PartImage,
		Ref:  &conversation.BlobRef{Hash: hash, Name: name, MIME: "image/png"},
	}
}

// TestReplayPureImageMessages 纯图消息（有图无字）回放必须成块：块里没有正文但有图，
// 否则用户贴的图在历史里整条消失（D207）。
func TestReplayPureImageMessages(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleEvent(port.HistoryEvent{Message: conversation.Message{
		Role:    conversation.RoleUser,
		Content: []conversation.Part{imgPart("h1", "a.png")},
	}})
	m.handleEvent(port.HistoryEvent{Message: conversation.Message{
		ID:      "a1",
		Role:    conversation.RoleAssistant,
		Outcome: conversation.OutcomeDone,
		Content: []conversation.Part{imgPart("h2", "b.png")},
	}})

	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want 2（纯图 user/assistant 各一块）: %+v", len(m.blocks), m.blocks)
	}
	if b := m.blocks[0]; b.kind != blockUser || strings.TrimSpace(b.text) != "" || len(b.imgs) != 1 || b.imgs[0].blob.Hash != "h1" {
		t.Fatalf("blocks[0] = %+v, want 纯图 user 块", b)
	}
	if b := m.blocks[1]; b.kind != blockAssistant || strings.TrimSpace(b.text) != "" || b.id != "a1" ||
		len(b.imgs) != 1 || b.imgs[0].blob.Hash != "h2" {
		t.Fatalf("blocks[1] = %+v, want 纯图 assistant 块（带节点 ID）", b)
	}
}

// TestNodeSegmentsImages 分段口径：工具边界断段；段内图片按出现序收集，正文不再留
// 〔图片：…〕占位（图片由缩略图承载）。
func TestNodeSegmentsImages(t *testing.T) {
	segs := nodeSegments([]conversation.Part{
		{Kind: conversation.PartText, Text: "甲"},
		imgPart("h1", "a.png"),
		imgPart("h2", "b.png"),
		{Kind: conversation.PartTool, Tool: &conversation.ToolPart{CallID: "c", Name: "echo"}},
		{Kind: conversation.PartText, Text: "乙"},
		imgPart("h3", "c.png"),
	})
	if len(segs) != 2 {
		t.Fatalf("段数 = %d, want 2（工具边界断段）: %+v", len(segs), segs)
	}
	if segs[0].text != "甲" || strings.Contains(segs[0].text, "图片") {
		t.Fatalf("segs[0].text = %q, want 仅「甲」", segs[0].text)
	}
	if len(segs[0].imgs) != 2 || segs[0].imgs[0].blob.Hash != "h1" || segs[0].imgs[1].blob.Hash != "h2" {
		t.Fatalf("segs[0].imgs = %+v, want h1/h2 顺序", segs[0].imgs)
	}
	if segs[1].text != "乙" || len(segs[1].imgs) != 1 || segs[1].imgs[0].blob.Hash != "h3" {
		t.Fatalf("segs[1] = %+v, want 乙 + h3", segs[1])
	}
}

// TestApplyImgCache 缩略图缓存：首份为准（竞态重复回投不覆盖）、失败留墓碑（不再重试）、
// 超上限按插入序淘汰。
func TestApplyImgCache(t *testing.T) {
	u := newFrameUI()
	first := &thumbSrc{img: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	u.applyImg(imgMsg{key: "h:a", thumb: first})
	u.applyImg(imgMsg{key: "h:a", thumb: &thumbSrc{img: image.NewRGBA(image.Rect(0, 0, 2, 2))}})
	if got := u.imgLookup["h:a"]; got != first {
		t.Fatalf("重复回投应保首份: got %p want %p", got, first)
	}

	// 墓碑：解码失败也要占位，否则每帧重试一次 = 持续卡帧源。
	u.applyImg(imgMsg{key: "h:bad"})
	if got, ok := u.imgLookup["h:bad"]; !ok || got != nil {
		t.Fatalf("失败应留墓碑: ok=%v thumb=%v", ok, got)
	}
	// ensureImg 命中墓碑即返回，不再起后台解码。
	if got := u.ensureImg(imgRef{blob: &conversation.BlobRef{Hash: "bad"}}); got != nil {
		t.Fatalf("墓碑应返回 nil, got %+v", got)
	}
	if u.imgLoading["h:bad"] {
		t.Fatal("墓碑不该重新登记在途")
	}

	for i := 0; i < imgCacheLimit+8; i++ {
		u.applyImg(imgMsg{key: "h:k" + string(rune('a'+i))})
	}
	if n := len(u.imgLookup); n > imgCacheLimit {
		t.Fatalf("缓存条目 = %d, want ≤ %d", n, imgCacheLimit)
	}
	if len(u.imgOrder) > imgCacheLimit {
		t.Fatalf("插入序 = %d, want ≤ %d", len(u.imgOrder), imgCacheLimit)
	}
}

// TestOpenImageOpensPreviewWindow 点格子 → 内容入原子槽 + 开/聚焦预览次窗（单实例）。
func TestOpenImageOpensPreviewWindow(t *testing.T) {
	u := newFrameUI()
	var spawned []winKind
	var last *winHandle
	u.wins = newWinHost(func(k winKind) *winHandle {
		spawned = append(spawned, k)
		last = &winHandle{done: make(chan struct{})}
		return last
	})

	ref := imgRef{blob: &conversation.BlobRef{Hash: "h1", Name: "截图.png"}, name: "截图.png"}
	u.openImage(ref)
	if c := u.imageView.Load(); c == nil || c.title != "截图.png" || c.ref.blob.Hash != "h1" {
		t.Fatalf("预览内容 = %+v, want 截图.png/h1", c)
	}
	if len(spawned) != 1 || spawned[0] != winImage {
		t.Fatalf("开窗 = %v, want 一个 winImage", spawned)
	}
	u.openImage(ref) // 单实例：已开 = 聚焦（不新开）
	if len(spawned) != 1 {
		t.Fatalf("重开应聚焦而非新开: %v", spawned)
	}

	// 无寻址信息的引用不开窗（防御）。
	u.openImage(imgRef{})
	if len(spawned) != 1 {
		t.Fatalf("空引用不该开窗: %v", spawned)
	}
	close(last.done)
}

// TestImageViewFitZoomPan 预览窗缩放/平移：适配比例（小图不放大）、平移夹取、光标锚点。
func TestImageViewFitZoomPan(t *testing.T) {
	st := newImageViewState(nil, nil)
	st.img = image.NewRGBA(image.Rect(0, 0, 200, 100))
	st.pix = image.Pt(200, 100)
	st.viewport = image.Rect(0, 0, 400, 400)
	if got := st.fitScale(); got != 1 {
		t.Fatalf("适配比例 = %v, want 1（小图不放大）", got)
	}
	st.viewport = image.Rect(0, 0, 100, 100)
	if got, want := st.fitScale(), 0.5; math.Abs(got-want) > 1e-9 {
		t.Fatalf("适配比例 = %v, want %v", got, want)
	}

	// 适配态（zoom = 1）平移恒回中：小图不该被拖出视口。
	st.pan = imagePt(40, 40)
	st.clampPan()
	if st.pan != (f32.Point{}) {
		t.Fatalf("适配态平移 = %+v, want 零", st.pan)
	}

	// 放大后平移按「图像比视口多出来的那一半」夹取。
	st.img = image.NewRGBA(image.Rect(0, 0, 400, 400))
	st.pix, st.viewport, st.zoom = image.Pt(400, 400), image.Rect(0, 0, 200, 200), 2
	st.pan = imagePt(500, 500)
	st.clampPan()
	if st.pan.X != 100 || st.pan.Y != 100 {
		t.Fatalf("夹取后平移 = %+v, want (100,100)", st.pan)
	}

	// 光标锚点：光标在视口中心时缩放不改平移；偏离中心时以光标下的图点为不动点。
	st.pan, st.zoom = (f32.Point{}), 1
	st.zoomAt(imagePt(100, 100), -100) // 中心 → 放大
	if st.pan != (f32.Point{}) {
		t.Fatalf("中心锚点缩放平移 = %+v, want 零", st.pan)
	}
	if st.zoom <= 1 {
		t.Fatalf("zoom = %v, want > 1", st.zoom)
	}
	before := st.zoom
	st.pan = (f32.Point{})
	st.zoomAt(imagePt(150, 100), -100) // 光标在中心右侧 50px
	if st.pan.X >= 0 {
		t.Fatalf("光标右侧放大应把图往左推: pan = %+v", st.pan)
	}
	if st.zoom <= before {
		t.Fatalf("zoom = %v, want > %v", st.zoom, before)
	}

	// 上限夹取 8×。
	st.zoom = imgViewZoomMax
	st.zoomAt(imagePt(100, 100), -1000)
	if st.zoom != imgViewZoomMax {
		t.Fatalf("zoom = %v, want 夹到 %v", st.zoom, imgViewZoomMax)
	}
}

// TestImageViewFrame 帧级：空槽显示未选择态、内容变化才重解码（指针比较）、
// 错误文案可见（不崩）。
func TestImageViewFrame(t *testing.T) {
	u := newFrameUI()
	st := newImageViewState(nil, nil)
	q := new(input.Router)
	th := testTheme()
	frame := func() {
		gtx, ops := frameGtx(q.Source())
		imgViewFrame(gtx, th, u, st)
		q.Frame(ops)
	}

	frame()
	if st.applied != nil || st.img != nil {
		t.Fatalf("空槽应无内容: applied=%v img=%v", st.applied, st.img)
	}

	// 内容入槽后：sync 换图并起后台解码（不阻塞本帧）。
	c := &imageContent{title: "图.png", ref: imgRef{blob: &conversation.BlobRef{Hash: "h1", Name: "图.png"}}}
	u.imageView.Store(c)
	frame()
	if st.applied != c {
		t.Fatalf("applied = %v, want %v", st.applied, c)
	}
	// 本 UI 没装附件库 → 解码必然失败；收下结果后 settled 且留错误文案。
	select {
	case r := <-st.res:
		if st.applied != nil && r.key == st.applied.ref.key() {
			st.img, st.pix, st.err, st.settled = r.img, r.pix, r.err, true
		}
	default:
		t.Fatal("解码结果未回投")
	}
	if st.err == "" || !st.settled {
		t.Fatalf("未装附件库应落错误文案: err=%q settled=%v", st.err, st.settled)
	}
	frame() // 带错误态再画一帧（不崩）
}

// TestPreviewImageFrameDraws 预览窗真实解码 → 绘制路径：路径项读文件解码成功，
// 帧内滤镜/变换不 panic（含放大态与错误态）。真实次窗由手工验收覆盖。
func TestPreviewImageFrameDraws(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pic.png")
	if err := os.WriteFile(path, pngBytes(t, 32, 16, color.NRGBA{R: 0x33, G: 0x66, B: 0x99, A: 0xFF}), 0o600); err != nil {
		t.Fatal(err)
	}

	u := frameUIWithInbox()
	st := newImageViewState(nil, nil)
	q := new(input.Router)
	th := testTheme()
	frame := func() {
		gtx, ops := frameGtx(q.Source())
		imgViewFrame(gtx, th, u, st)
		q.Frame(ops)
	}

	u.imageView.Store(&imageContent{title: "pic.png", ref: imgRef{path: path, name: "pic.png"}})
	frame() // 起解码 goroutine
	deadline := time.Now().Add(2 * time.Second)
	for st.img == nil && st.err == "" && time.Now().Before(deadline) {
		frame()
		time.Sleep(5 * time.Millisecond)
	}
	if st.err != "" {
		t.Fatalf("解码失败: %s", st.err)
	}
	if st.img == nil || st.pix != image.Pt(32, 16) {
		t.Fatalf("解码结果 = img:%v pix:%v, want 32x16", st.img != nil, st.pix)
	}
	// 放大 + 平移态再画两帧（变换与 clip 的次序必须能连续出帧）。
	st.zoom = 4
	st.pan = imagePt(30, 12)
	frame()
	frame()
	// 换到不存在的路径：落错误文案，不崩。
	u.imageView.Store(&imageContent{title: "没了.png", ref: imgRef{path: filepath.Join(dir, "nope.png"), name: "没了.png"}})
	deadline = time.Now().Add(2 * time.Second)
	for st.err == "" && time.Now().Before(deadline) {
		frame()
		time.Sleep(5 * time.Millisecond)
	}
	if st.err == "" {
		t.Fatal("路径不存在应落错误文案")
	}
	frame()
}

// TestImageViewEscCloses Esc 关窗：焦点落到本窗输入区（首帧 FocusCmd），随后 Esc 被
// 消费并调用关闭动作。**注意帧序**：FocusCmd 在帧尾执行，下一帧才投 FocusEvent。
func TestImageViewEscCloses(t *testing.T) {
	u := frameUIWithInbox()
	closed := 0
	st := newImageViewState(func() { closed++ }, nil)
	st.img = image.NewRGBA(image.Rect(0, 0, 4, 4))
	st.imgOp = paint.NewImageOp(st.img)
	st.hasOp = true
	st.pix = image.Pt(4, 4)
	q := new(input.Router)
	th := testTheme()
	frame := func() {
		gtx, ops := frameGtx(q.Source())
		imgViewFrame(gtx, th, u, st)
		q.Frame(ops)
	}
	frame() // 执行 FocusCmd
	frame() // 收到 FocusEvent，键过滤生效
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	frame()
	if closed != 1 {
		t.Fatalf("Esc 关闭次数 = %d, want 1", closed)
	}
}

// TestTranscriptImageRows 帧级：纯图 user/assistant 行可布局（不 panic、登记气泡底板），
// chip 展开体的图格也登记点击件。
func TestTranscriptImageRows(t *testing.T) {
	u := frameUIWithInbox()
	u.m.blocks = []block{
		{kind: blockUser, imgs: []imgRef{{blob: &conversation.BlobRef{Hash: "h1", Name: "a.png"}, name: "a.png"}}},
		{kind: blockAssistant, id: "a1", imgs: []imgRef{{blob: &conversation.BlobRef{Hash: "h2", Name: "b.png"}, name: "b.png"}}},
		{kind: blockTool, chip: &toolChip{
			name: "screen_capture", done: true, ok: true, result: "ok",
			images: []tool.ImageRef{{Hash: "h3", MIME: "image/png", Name: "c.png"}},
		}},
	}
	u.chipOpen = map[int]bool{2: true} // 展开 chip：图格才铺开

	q := new(input.Router)
	gtx, ops := frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)

	if len(u.bubbleRects) < 2 {
		t.Fatalf("气泡底板 = %d, want ≥ 2（纯图行也要成气泡）", len(u.bubbleRects))
	}
	if len(u.imgClicks) < 3 {
		t.Fatalf("图片点击件 = %d, want ≥ 3（两行正文图 + chip 展开图）", len(u.imgClicks))
	}
	if c := u.imgClicks[imgClickKey{bi: 2, ii: 0}]; c == nil {
		t.Fatal("chip 展开体的图格未登记点击件")
	}

	// 纯图气泡不占行选键（没有可选文本）：分叉条的分叉序与后续行序号因此不漂移。
	items := u.frameItems()
	if n := items[0].selCount(); n != 0 {
		t.Fatalf("纯图 user 行的行选键 = %d, want 0", n)
	}
	if n := items[1].selCount(); n != 0 {
		t.Fatalf("纯图 assistant 行的行选键 = %d, want 0", n)
	}
	// 复制口径：没有可选文本 → 落到图片名（复制空串没有意义）。
	u2 := newFrameUI()
	u2.m.blocks = []block{{kind: blockUser, id: "u1", imgs: []imgRef{
		{blob: &conversation.BlobRef{Hash: "h1", Name: "a.png"}, name: "a.png"},
	}}}
	q2 := new(input.Router)
	gtx2, ops2 := frameGtx(q2.Source())
	u2.layout(gtx2)
	q2.Frame(ops2)
	if len(u2.bubbleRects) != 1 {
		t.Fatalf("气泡底板 = %d, want 1", len(u2.bubbleRects))
	}
	if got := u2.bubbleCtx(0).copy; got != "a.png" {
		t.Fatalf("纯图气泡复制 = %q, want 图片名 a.png", got)
	}
}

// TestChipHeaderShowsImageCount 折叠态也要看出这次调用带了图（否则图等于被藏起来）。
func TestChipHeaderShowsImageCount(t *testing.T) {
	c := &toolChip{name: "screen_capture", done: true, ok: true,
		images: []tool.ImageRef{{Hash: "h1"}, {Hash: "h2"}}}
	if got := chipHeaderText(c, false); !strings.Contains(got, "🖼2") {
		t.Fatalf("chip 头部 = %q, want 含图片数记号", got)
	}
	if got := chipHeaderText(&toolChip{name: "echo", done: true, ok: true}, false); strings.Contains(got, "🖼") {
		t.Fatalf("无图 chip 不该有图片记号: %q", got)
	}
}

// TestAttachToolResultKeepsImages 工具结果里的图进 chip（展开体渲染与点击都靠它）。
func TestAttachToolResultKeepsImages(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "screen_capture"}})
	m.handleEvent(port.ToolResultEvent{Result: tool.Result{
		CallID: "c1", OK: true, Output: "captured",
		Images: []tool.ImageRef{{Hash: "h1", MIME: "image/png", Name: "shot.png"}},
	}})
	if len(m.blocks) != 1 || m.blocks[0].chip == nil {
		t.Fatalf("blocks = %+v, want 一个 chip", m.blocks)
	}
	if imgs := m.blocks[0].chip.images; len(imgs) != 1 || imgs[0].Hash != "h1" {
		t.Fatalf("chip.images = %+v, want 一项 h1", imgs)
	}
}

// TestImageClickOpensPreview 点击链路（headless 帧）：格子点击件命中 → 内容入原子槽 +
// 开预览窗。指针路由与 Clickable 的帧序在 Gio 无窗口 Router 下不稳定，故用 Clickable
// 的程序化点击表达（同一 Clicked 通路），与分叉条点击测试同口径。
func TestImageClickOpensPreview(t *testing.T) {
	u := frameUIWithInbox()
	var spawned []winKind
	u.wins = newWinHost(func(k winKind) *winHandle {
		spawned = append(spawned, k)
		return &winHandle{done: make(chan struct{})}
	})
	u.m.blocks = []block{
		{kind: blockUser, imgs: []imgRef{
			{blob: &conversation.BlobRef{Hash: "h1", Name: "a.png"}, name: "a.png"},
			{blob: &conversation.BlobRef{Hash: "h2", Name: "b.png"}, name: "b.png"},
		}},
	}

	q := new(input.Router)
	frame := func() {
		gtx, ops := frameGtx(q.Source())
		u.layout(gtx)
		q.Frame(ops)
	}
	frame()

	u.imgClick(imgClickKey{bi: 0, ii: 1}).Click()
	frame()
	if c := u.imageView.Load(); c == nil || c.ref.blob == nil || c.ref.blob.Hash != "h2" {
		t.Fatalf("点击格 1 应打开 b.png(h2), got %+v", c)
	}
	if len(spawned) != 1 || spawned[0] != winImage {
		t.Fatalf("开窗 = %v, want 一个 winImage", spawned)
	}

	// chip 折叠时格子没铺开：即便点击件残留点击也不该响应。
	u.m.blocks = append(u.m.blocks, block{kind: blockTool, chip: &toolChip{
		name: "screen_capture", done: true, ok: true,
		images: []tool.ImageRef{{Hash: "h3", MIME: "image/png", Name: "c.png"}},
	}})
	u.imageView.Store(nil)
	frame()
	u.imgClick(imgClickKey{bi: 1, ii: 0}).Click()
	frame()
	if c := u.imageView.Load(); c != nil {
		t.Fatalf("折叠 chip 的图格不该响应点击, got %+v", c)
	}

	// 展开后响应。
	u.chipOpen = map[int]bool{1: true}
	frame()
	u.imgClick(imgClickKey{bi: 1, ii: 0}).Click()
	frame()
	if c := u.imageView.Load(); c == nil || c.ref.blob.Hash != "h3" {
		t.Fatalf("展开 chip 的图格应打开 c.png(h3), got %+v", c)
	}
}

// 小工具：避免在测试里逐处写 gioui 类型名。
func imagePt(x, y int) f32.Point { return f32.Pt(float32(x), float32(y)) }
