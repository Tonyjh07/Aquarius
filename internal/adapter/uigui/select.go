package uigui

import (
	"encoding/binary"
	"hash"
	"image"
	"image/color"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"gioui.org/io/clipboard"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// selTouchSlopDp 跨块拖选起手 slop（与 Gio gesture 的 touchSlop 同口径：欧氏距离，
// d² > slop² 才算拖动——小于此值留给块内原生点击/拖选）。
const selTouchSlopDp unit.Dp = 3

// selPoint 跨块选择端点：线性选键 + 键内 rune 偏移（D91 ② 的几何底座）。
type selPoint struct {
	key  int
	rune int
}

// selGeom 单选键几何（D91 ②）：每帧 paint 由量期记录译出落盘、消费者阶段读取
// （一帧陈旧可接受——按下命中与焦点跟随都基于上一帧几何）。origin = 键 widget 原点
// （窗口系）；rect = 各视觉行盒并集（窗口系，命中/最近判定）；lines = 各视觉行盒
// （窗口系、按行单调，caretIn 二分用——Regions 的行带 = 整行高，行内恒定）；
// nrunes = 键文本 rune 数（跨度夹取）。laid=false = 本帧未铺开（折叠 chip 体等），
// rect 空、不参与命中；结构指纹按 laid/text 逐键入哈希（fp 变即清选，D91 ④）。
type selGeom struct {
	laid   bool
	origin image.Point
	rect   image.Rectangle
	lines  []image.Rectangle
	nrunes int
}

// selSpan 键内选区跨度 [start,end)（rune 偏移，已按键文本夹取；空跨度已剔除）。
type selSpan struct {
	key        int
	start, end int
}

// selState 跨块拖选状态（D91）：观察者（&sel 作 event.Tag，窗口系坐标）+ 锚/焦点
// 模型。Press 定候选、越 slop 抢先 grab 激活、Drag 推焦点、Release 保持高亮、
// Cancel/Escape/新按压/焦点让渡放弃。仅事件循环 goroutine 读写。
type selState struct {
	// 候选期（Press 命中可选键、slop 未越——不打扰块内原生手势）。
	cand    bool
	press   image.Point
	candKey int
	pid     pointer.ID
	// 激活期（越 slop 抢先 grab 后；键面 Ctrl+C/Escape 此间生效）。
	active bool
	anchor selPoint
	focus  selPoint
	fp     uint64
	// pressedNow 本批（同帧）处理过 Press：widget 尚未消化按压 → 锚用几何值
	//（否则会读到陈旧选区）。异批激活时改用 widget 选区近端作锚（双击词锚）。
	pressedNow bool
}

// clear 放弃全部选态（新按压/键面/取消/结构自愈共用）。
func (s *selState) clear() { *s = selState{} }

// rowSel 行选几何挂接件（D91 ②）：measure 期逐键记录（块内偏移 + 行盒 + 文本），
// paint 期译成 selGeom 回写 keyRects 并绘制高亮。nil 挂接件 = 测试直通（不建行选
// 状态，sels 恒 nil、note 空转）。
type rowSel struct {
	u    *UI
	base int          // 线性键基址（transcript 按条目 selCount 累计）
	n    int          // 本条目键数（= selCount；折叠时部分键不铺开也占位防漂移）
	keys []selKeyGeom // 按键序（len ≤ n；未 note 的键 = 本帧未铺开）
}

// selKeyGeom 单键量期记录：off 相对行内容原点（bgRect.Min+pad），lines 相对键原点；
// nrunes/text 供 paint 期译几何与结构指纹（text 只入哈希，复制取 Text() 现值）。
type selKeyGeom struct {
	laid   bool
	off    image.Point
	lines  []widget.Region
	nrunes int
	text   string
}

// sel 行选状态取用（nil 挂接件返回 nil——material label 的 State 可空，不可选）。
func (r *rowSel) sel(k int) *widget.Selectable {
	if r == nil {
		return nil
	}
	return r.u.selFor(r.base + k)
}

// note 量期记录键几何（须在该键 widget Layout 之后调用——Regions 此刻才有效）。
// k 越界防御：键数以 selCount（r.n）为准。
func (r *rowSel) note(k int, off image.Point) {
	if r == nil || k < 0 || k >= r.n {
		return
	}
	for len(r.keys) <= k {
		r.keys = append(r.keys, selKeyGeom{})
	}
	s := r.u.selFor(r.base + k)
	txt := s.Text()
	n := utf8.RuneCountInString(txt)
	lines := s.Regions(0, n, nil)
	r.keys[k] = selKeyGeom{
		laid:   true,
		off:    off,
		lines:  append([]widget.Region(nil), lines...),
		nrunes: n,
		text:   txt,
	}
}

// vstack 手排纵列（D91 ②）：等价 layout.Flex{Axis: Vertical} 的 Rigid 路径——量期
// 即知每子项 y（Flex 的 Offset 在回放期才施加，量期不可知，键几何无法捕获）。
// 子项约束同 Flex（Min=(crossMin,0)、Max=(crossMax, remaining)），间距以 gap 递进，
// 返回 (maxCross, total)——与 Flex 的尺寸口径逐像素一致（既有行高 golden 为闸）。
type vstack struct {
	gtx       layout.Context
	y         int
	remaining int
	maxW      int
}

// beginVStack 以父约束起列（remaining 初值 = Flex 的 mainMax）。
func beginVStack(gtx layout.Context) *vstack {
	return &vstack{gtx: gtx, remaining: gtx.Constraints.Max.Y}
}

// gap 纵向间距（等价 Flex 的 Spacer 子项）。
func (s *vstack) gap(dp unit.Dp) { s.y += s.gtx.Dp(dp) }

// add 在当前 y 处布局子项并推进游标；返回子项原点相对列首的偏移。
func (s *vstack) add(w layout.Widget) image.Point {
	offY := s.y
	cs := s.gtx
	cs.Constraints.Min = image.Pt(cs.Constraints.Min.X, 0)
	if cs.Constraints.Max.Y > s.remaining {
		cs.Constraints.Max.Y = s.remaining
	}
	o := op.Offset(image.Pt(0, offY)).Push(s.gtx.Ops)
	d := w(cs)
	o.Pop()
	s.y += d.Size.Y
	if s.remaining -= d.Size.Y; s.remaining < 0 {
		s.remaining = 0
	}
	if d.Size.X > s.maxW {
		s.maxW = d.Size.X
	}
	return image.Pt(0, offY)
}

// dims 列尺寸（宽 = 最大子宽，高 = 各子累加）。
func (s *vstack) dims() layout.Dimensions {
	return layout.Dimensions{Size: image.Pt(s.maxW, s.y)}
}

// layout 排一个带行选高亮的文本控件（D91 ②③）：先 note 行盒（量期、Layout 刚跑完、
// Regions 缓存新鲜），再录文本宏 → 画高亮 → 回放文本。高亮先于文本落 ops
// （material 同序：选区底垫在字形下）；nil 挂接件直通（测试路径零开销）。
// off = 文本原点相对行内容原点（note → keyRects 窗口译算，vstack 子项须带块偏移）；
// inset = 文本原点相对**当前 ops 变换**的偏移（vstack 的 Offset 已在变换内 → 0；
// mdCode 文本在卡内衬处 → 内衬值——高亮盒按此加，勿与 off 混用）。
func (r *rowSel) layout(gtx layout.Context, w func(layout.Context) layout.Dimensions, k int, off, inset image.Point) layout.Dimensions {
	if r == nil {
		return w(gtx)
	}
	m := op.Record(gtx.Ops)
	d := w(gtx)
	txt := m.Stop()
	r.note(k, off)
	r.paint(gtx, k, inset)
	txt.Add(gtx.Ops)
	return d
}

// paint 画单键选区高亮（量期宏内、文本回放前；局部坐标 = 键原点系）。
// 无选态/本键不在跨度内/未铺开 → 零 ops。色 = material 选区同式 ContrastBg×0x60
// （f32color.MulAlpha 内部包不可引，按公式复刻：A×0x60/0xFF，RGB 不变）。
func (r *rowSel) paint(gtx layout.Context, k int, inset image.Point) {
	if r == nil || !r.u.sel.active {
		return
	}
	abs := r.base + k
	sp, ok := r.u.selSpanFor(abs)
	if !ok {
		return
	}
	s := r.u.selFor(abs)
	n := utf8.RuneCountInString(s.Text())
	if sp.start >= n {
		return
	}
	end := sp.end
	if end > n {
		end = n
	}
	if end <= sp.start {
		return
	}
	r.u.selScratch = s.Regions(sp.start, end, r.u.selScratch[:0])
	col := selColor(r.u.th.Palette.ContrastBg)
	for _, b := range r.u.selScratch {
		if b.Bounds.Empty() {
			continue
		}
		paint.FillShape(gtx.Ops, col, clip.Rect(b.Bounds.Add(inset)).Op())
	}
}

// selColor material 选区色公式（f32color.MulAlpha(c, 0x60) 复刻）。
func selColor(c color.NRGBA) color.NRGBA {
	c.A = uint8(uint32(c.A) * 0x60 / 0xFF)
	return c
}

// updateSel 跨块拖选观察者（D91 ①/④）：消费者阶段 pull（measure 之前——行 widget
// 的事件消费在其量期，观察者必先到；键面亦先于 updateEditor 抢 Ctrl+C）。
// fade pass 零 Source、收起态无热区 → 都不消费。
func (u *UI) updateSel(gtx layout.Context) {
	if u.inFadePass || u.collapsed {
		return
	}
	s := &u.sel
	s.pressedNow = false
	// 焦点纪律（D91 ③ 清除源）：编辑器/原因框获焦 = 用户要打字 → 放弃选区。
	if s.active && (gtx.Focused(&u.editor) || gtx.Focused(&u.reasonEd)) {
		u.clearSel()
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &u.sel,
			Kinds:  pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		pos := e.Position.Round()
		switch e.Kind {
		case pointer.Press:
			// 主键新按压（D91 ④ 清除源；副键留给 S2 右键菜单复用选态，D91 后果⑤）。
			// 触控不参与（保原生点按/滚动；v1 拖选限鼠标）。
			if !e.Buttons.Contain(pointer.ButtonPrimary) || e.Source != pointer.Mouse {
				continue
			}
			if s.active {
				u.clearSel()
			}
			if k, hit := u.keyHit(pos); hit {
				s.cand, s.press, s.candKey, s.pid = true, pos, k, e.PointerID
				s.pressedNow = true
			} else {
				s.cand = false
			}
		case pointer.Drag:
			if !s.cand || e.PointerID != s.pid {
				continue
			}
			if s.active {
				if p, ok := u.pointToCaret(pos); ok {
					s.focus = p
				}
				continue
			}
			// 候选 → 越 slop 激活（D91 ①：先到先得，块内 dragger 随后收 Cancel）。
			if e.Priority >= pointer.Grabbed {
				continue // 已有持有者（防御：同帧内不应发生）
			}
			d := pos.Sub(s.press)
			if slop := gtx.Dp(selTouchSlopDp); d.X*d.X+d.Y*d.Y <= slop*slop {
				continue
			}
			u.selActivate(gtx, e)
		case pointer.Release:
			if e.PointerID == s.pid {
				s.cand = false // 抬手结束指针跟踪；选区保持（armed）
			}
		case pointer.Cancel:
			if e.PointerID == s.pid {
				s.cand = false // 他者接管指针/系统取消：放弃候选，已建选区保留（自愈于下次主键）
			}
		}
	}
	if !s.active {
		u.selSpansBuf = nil
		return
	}
	// 结构指纹自愈（D91 ④）：键位/铺开/文本任一变化即清——选区不漂移到错块。
	if s.fp != u.keyFp {
		u.clearSel()
		return
	}
	// 键面条件 pull（D91 ④）：选区存续期独占 Ctrl+C（编辑器焦点过滤器此刻不匹配
	// ——grab 瞬间已清焦）；Escape 放弃。仅 Press 生效（Release 不重触发）。
	for {
		ev, ok := gtx.Event(
			key.Filter{Name: "C", Required: key.ModShortcut},
			key.Filter{Name: key.NameEscape},
		)
		if !ok {
			break
		}
		ke, ok := ev.(key.Event)
		if !ok || ke.State != key.Press {
			continue
		}
		switch ke.Name {
		case "C":
			u.selCopy(gtx)
		case key.NameEscape:
			u.clearSel()
		}
	}
	u.selSpansBuf = u.selSpans()
}

// selActivate 越 slop 激活（D91 ①②③）：抢先 grab + 清焦 + 定锚推焦点。
func (u *UI) selActivate(gtx layout.Context, e pointer.Event) {
	s := &u.sel
	gtx.Execute(pointer.GrabCmd{Tag: &u.sel, ID: e.PointerID})
	gtx.Execute(key.FocusCmd{Tag: nil}) // 焦点纪律：widget 失焦即不绘 caret/原生选区、其 Focus: 键过滤失效
	s.active = true
	s.fp = u.keyFp
	// 锚 = 起始键近端（D91 ②）：Press 已被 widget 消费 → 选区近端（双击选词后拖拽
	// 自动锚词首）；同批 Press 尚未消费 → 几何落点（widget 侧 Selection 还是旧值）。
	if s.pressedNow {
		if p, ok := u.pointToCaret(s.press); ok && p.key == s.candKey {
			s.anchor = p
		} else {
			s.anchor = selPoint{key: s.candKey}
		}
	} else {
		st, _ := u.selFor(s.candKey).Selection()
		s.anchor = selPoint{key: s.candKey, rune: st}
	}
	if p, ok := u.pointToCaret(e.Position.Round()); ok {
		s.focus = p
	} else {
		s.focus = s.anchor
	}
	u.selSpansBuf = u.selSpans()
}

// clearSel 放弃选区（清除源共用；D91 ④）。
func (u *UI) clearSel() {
	u.sel.clear()
	u.selSpansBuf = nil
}

// keyHit 主键按压候选：严格命中某键行盒并集（D91 ①——未命中即不接管，
// 块内空白处的拖拽仍归原生手势）。
func (u *UI) keyHit(p image.Point) (int, bool) {
	for i := range u.keyRects {
		if u.keyRects[i].laid && p.In(u.keyRects[i].rect) {
			return i, true
		}
	}
	return 0, false
}

// pointToCaret 窗口系坐标 → 跨块选择点（D91 ②）：块 rect 命中取之，块外取最近键
// （行缘夹取）；caretIn 在键内做视觉行 + 行内二分。
func (u *UI) pointToCaret(p image.Point) (selPoint, bool) {
	best, bestD := -1, 0
	for i := range u.keyRects {
		g := &u.keyRects[i]
		if !g.laid || g.rect.Empty() {
			continue
		}
		if p.In(g.rect) {
			return selPoint{key: i, rune: u.caretIn(i, p)}, true
		}
		if d := distRectSq(g.rect, p); best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	if best < 0 {
		return selPoint{}, false
	}
	return selPoint{key: best, rune: u.caretIn(best, p)}, true
}

// distRectSq 点到矩形轴向距离平方（负值区取 0）。
func distRectSq(r image.Rectangle, p image.Point) int {
	dx := max(max(r.Min.X-p.X, 0), p.X-r.Max.X)
	dy := max(max(r.Min.Y-p.Y, 0), p.Y-r.Max.Y)
	return dx*dx + dy*dy
}

// caretIn 键内坐标 → rune caret（D91 ② 两级二分）：先按行带（Regions 整行高、
// 行内恒定、行间单调）定位视觉行，再在行内对单 rune 盒（Regions(mid,mid+1) =
// 该 rune 的步进盒）二分；落点在前一 rune 盒中线取前界。越界夹到首/末行缘。
func (u *UI) caretIn(k int, p image.Point) int {
	g := &u.keyRects[k]
	if g.nrunes == 0 || len(g.lines) == 0 {
		return 0
	}
	s := u.selFor(k)
	scr := u.selScratch
	defer func() { u.selScratch = scr }()
	n := g.nrunes
	if nc := utf8.RuneCountInString(s.Text()); nc < n {
		n = nc // 流式缩水防越界（指纹下帧自愈）
	}
	if n == 0 {
		return 0
	}
	local := p.Sub(g.origin)
	box := func(m int) image.Rectangle {
		scr = s.Regions(m, m+1, scr[:0])
		if len(scr) == 0 {
			return image.Rectangle{}
		}
		return scr[0].Bounds
	}
	// ① 视觉行：首个行带底越过 p.Y 的行（行带落盘即窗口系，与 p 同系；越出底部取
	// 末行、顶部取首行）——定行后转 widget 系与 Regions 盒（widget 系）同系比较。
	li := sort.Search(len(g.lines), func(i int) bool { return g.lines[i].Max.Y > p.Y })
	if li == len(g.lines) {
		li = len(g.lines) - 1
	}
	line := g.lines[li].Sub(g.origin)
	// ② 行内 rune 区间 [f, e)：按行带单调二分（行带间不重叠、行内恒同）。
	f := sort.Search(n, func(m int) bool { return box(m).Min.Y >= line.Min.Y })
	if f >= n {
		return n
	}
	e := sort.Search(n, func(m int) bool { return box(m).Min.Y >= line.Max.Y })
	if e < f {
		e = f
	}
	// ③ 行内 x：首个步进盒左缘越过 local.X 的 rune；前一 rune 盒中线取界。
	r := f + sort.Search(e-f, func(j int) bool { return box(f+j).Min.X > local.X })
	if r > f {
		b := box(r - 1)
		if local.X < (b.Min.X+b.Max.X)/2 {
			r--
		}
	}
	if r > n {
		r = n
	}
	return r
}

// selSpanFor 本帧选区跨度按绝对键取用（measure/paint 期读消费者阶段预计算的缓冲）。
func (u *UI) selSpanFor(key int) (selSpan, bool) {
	for _, sp := range u.selSpansBuf {
		if sp.key == key {
			return sp, true
		}
	}
	return selSpan{}, false
}

// selSpans 当前选区逐键跨度（消费者阶段末算，从上帧 keyRects 取 rune 数）。
func (u *UI) selSpans() []selSpan {
	s := &u.sel
	if !s.active {
		return nil
	}
	return selSpansOf(s.anchor, s.focus, func(k int) int {
		if k >= 0 && k < len(u.keyRects) {
			return u.keyRects[k].nrunes
		}
		return 0
	})
}

// selSpansOf 锚/焦点 → 逐键 [lo,hi)（D91 ②：同键取区间、两键之间全选；
// 方向不敏感——反向拖选归一到 lo≤hi）。
func selSpansOf(a, f selPoint, nrunes func(key int) int) []selSpan {
	lo, hi := a, f
	if hi.key < lo.key || (hi.key == lo.key && hi.rune < lo.rune) {
		lo, hi = hi, lo
	}
	var out []selSpan
	for k := lo.key; k <= hi.key; k++ {
		n := nrunes(k)
		if n <= 0 {
			continue // 空键/未铺开键不贡献跨度（键间空隙照常跨过）
		}
		s0, e0 := 0, n
		if k == lo.key {
			s0 = min(max(lo.rune, 0), n)
		}
		if k == hi.key {
			e0 = min(max(hi.rune, 0), n)
		}
		if e0 > s0 {
			out = append(out, selSpan{key: k, start: s0, end: e0})
		}
	}
	return out
}

// selText 当前选区文本（D91/D92）：逐键 Text() rune 切片、键间 `\n` 拼接；空选区 = ""。
// selCopy 与气泡右键「复制选区」（D92 复用 D91 选态）共用此拼接口径。
func (u *UI) selText() string {
	var parts []string
	for _, sp := range u.selSpansBuf {
		r := []rune(u.selFor(sp.key).Text())
		if sp.start >= len(r) {
			continue
		}
		end := sp.end
		if end > len(r) {
			end = len(r)
		}
		if end <= sp.start {
			continue
		}
		parts = append(parts, string(r[sp.start:end]))
	}
	return strings.Join(parts, "\n")
}

// selCopy Ctrl+C（D91 ④）：选区文本写系统剪贴板。
func (u *UI) selCopy(gtx layout.Context) {
	t := u.selText()
	if t == "" {
		return
	}
	gtx.Execute(clipboard.WriteCmd{
		Type: "application/text", // Gio 自家 Selectable 复制同款 MIME
		Data: io.NopCloser(strings.NewReader(t)),
	})
}

// blockText 整条气泡的渲染文本（D92）：[base, base+n) 行选键逐键 Text()、键间
// `\n` 拼接（与 selCopy 同拼接口径；未铺开键无文本、跳过）。
func (u *UI) blockText(base, n int) string {
	var parts []string
	for k := base; k < base+n; k++ {
		if t := u.selFor(k).Text(); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

// writeKeyRects 量期记录 → 窗口系几何落盘（paint 期、行原点已定）+ 结构指纹流式
// 喂哈希（逐键 laid/text，行序即键序）。尾截由 transcript 收口（结构缩小时清尾）。
func (u *UI) writeKeyRects(rs *rowSel, content image.Point, h hash.Hash64) {
	for k := 0; k < rs.n; k++ {
		idx := rs.base + k
		for len(u.keyRects) <= idx {
			u.keyRects = append(u.keyRects, selGeom{})
		}
		g := &u.keyRects[idx]
		g.laid, g.rect, g.nrunes, g.lines = false, image.Rectangle{}, 0, g.lines[:0]
		var sk selKeyGeom
		if k < len(rs.keys) {
			sk = rs.keys[k]
		}
		if sk.laid {
			g.laid = true
			g.origin = content.Add(sk.off)
			g.nrunes = sk.nrunes
			for _, lb := range sk.lines {
				r := lb.Bounds.Add(g.origin)
				g.lines = append(g.lines, r)
				g.rect = g.rect.Union(r)
			}
			// 指纹 = 逐键 (laid, len(text), text)：长度前缀防拼接碰撞；
			// 铺开态（chip 开合）与文本（流式改文）任一变化即改值（D91 ④）。
			h.Write([]byte{1})
			var lb [8]byte
			binary.LittleEndian.PutUint64(lb[:], uint64(len(sk.text)))
			h.Write(lb[:])
			io.WriteString(h, sk.text)
		} else {
			h.Write([]byte{0})
		}
	}
}
