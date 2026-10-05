package uigui

import (
	"encoding/json"
	"image"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// rectCenterF 矩形中心的指针坐标（气泡命中测试用）。
func rectCenterF(r image.Rectangle) f32.Point {
	return f32.Pt(float32(r.Min.X+r.Max.X)/2, float32(r.Min.Y+r.Max.Y)/2)
}

// rectCenterI 矩形中心的整数坐标（hitBubble 直测用）。
func rectCenterI(r image.Rectangle) image.Point {
	return image.Pt((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
}

// bubbleFrame 单帧布局（真实 router 事件源——Selectable 文本经布局就绪）。
func bubbleFrame(q *input.Router, u *UI) {
	gtx, ops := frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)
}

// TestBubbleRectsRegistered D92：paint 期逐行登记气泡底板矩形——带 ID 的 user/
// assistant 定稿块入列（assistant 复合行 = 单气泡携全部行选键），live 草稿/notice
// 无节点 ID 不登记（不进右键菜单）。
func TestBubbleRectsRegistered(t *testing.T) {
	u := newFrameUI()
	u.m.addMsg(blockUser, "问", "u1")
	u.m.addMsg(blockAssistant, "一\n\n二", "a1")
	u.m.addMsg(blockNotice, "提示", "")
	u.m.drafting = true
	u.m.draft.Reset()
	u.m.draft.WriteString("草稿")

	q := new(input.Router)
	bubbleFrame(q, u)

	if len(u.bubbleRects) != 2 {
		t.Fatalf("bubbleRects = %d 条, want 2（user+assistant）", len(u.bubbleRects))
	}
	ub, ab := u.bubbleRects[0], u.bubbleRects[1]
	if ub.id != "u1" || ub.kind != blockUser || ub.bi != 0 || ub.keyBase != 0 || ub.keyN != 1 {
		t.Fatalf("user hit = %+v", ub)
	}
	if ab.id != "a1" || ab.kind != blockAssistant || ab.bi != 1 || ab.keyN != 2 {
		t.Fatalf("assistant hit = %+v, want 复合行两键", ab)
	}
	frame := image.Rectangle{Max: image.Pt(winWidthDp, winHeightDp)}
	if !ub.rect.Min.In(frame) || !ub.rect.Max.In(frame) {
		t.Fatalf("user 气泡矩形越界: %v", ub.rect)
	}

	// 内容清空：矩形随帧复位。
	u.m.blocks = nil
	bubbleFrame(q, u)
	if len(u.bubbleRects) != 0 {
		t.Fatalf("清空后 bubbleRects = %d 条, want 0", len(u.bubbleRects))
	}
}

// TestBubbleRightGesture D92 右键手势（D72 logoRight 同款状态机）：Secondary 按下
// 武装、同气泡内抬起 = 请求菜单并携命中上下文；跨气泡抬起 / 气泡外按下 / 左键 /
// Cancel 均不请求。
func TestBubbleRightGesture(t *testing.T) {
	setup := func() (*UI, *input.Router, *int, **bubbleMenuCtx) {
		u := newFrameUI()
		u.m.addMsg(blockUser, "问", "u1")
		u.m.addMsg(blockAssistant, "答", "a1")
		q := new(input.Router)
		n := 0
		var got *bubbleMenuCtx
		u.bubbleMenuHook = func(c *bubbleMenuCtx) { n++; got = c }
		return u, q, &n, &got
	}
	bubbleFrameEvents := func(q *input.Router, u *UI, evs ...pointer.Event) {
		for _, e := range evs {
			q.Queue(e)
			bubbleFrame(q, u)
		}
	}
	secPress := func(p f32.Point) pointer.Event {
		return pointer.Event{Kind: pointer.Press, Position: p, Buttons: pointer.ButtonSecondary, PointerID: 7, Source: pointer.Mouse}
	}
	secRelease := func(p f32.Point) pointer.Event {
		return pointer.Event{Kind: pointer.Release, Position: p, PointerID: 7, Source: pointer.Mouse}
	}

	// ① 原位抬起：请求 1 次，上下文 = user 气泡（编辑预填原文 + 整条复制文本）。
	u, q, n, got := setup()
	bubbleFrame(q, u)
	pos := rectCenterF(u.bubbleRects[0].rect)
	bubbleFrameEvents(q, u, secPress(pos), secRelease(pos))
	if *n != 1 || *got == nil || (*got).id != "u1" || (*got).kind != blockUser || (*got).edit != "问" || (*got).copy != "问" {
		t.Fatalf("请求 = %d 次, ctx = %+v, want user 气泡上下文（edit/copy=问）", *n, *got)
	}

	// ② 跨气泡抬起（按下 user、抬起 assistant）：不请求。
	u, q, n, _ = setup()
	bubbleFrame(q, u)
	bubbleFrameEvents(q, u,
		secPress(rectCenterF(u.bubbleRects[0].rect)),
		secRelease(rectCenterF(u.bubbleRects[1].rect)))
	if *n != 0 {
		t.Fatalf("跨气泡抬起请求了 %d 次菜单", *n)
	}

	// ③ 气泡外（转写区空白）按下抬起：不请求。
	u, q, n, _ = setup()
	bubbleFrame(q, u)
	gap := f32.Pt(1, 1)
	bubbleFrameEvents(q, u, secPress(gap), secRelease(gap))
	if *n != 0 {
		t.Fatalf("气泡外按下请求了 %d 次菜单", *n)
	}

	// ④ 左键按下抬起 / 右键按下后 Cancel：不请求。
	u, q, n, _ = setup()
	bubbleFrame(q, u)
	pos = rectCenterF(u.bubbleRects[0].rect)
	bubbleFrameEvents(q, u,
		pointer.Event{Kind: pointer.Press, Position: pos, Buttons: pointer.ButtonPrimary, PointerID: 7, Source: pointer.Mouse},
		secRelease(pos),
		secPress(pos),
		pointer.Event{Kind: pointer.Cancel, Position: pos, PointerID: 7, Source: pointer.Mouse},
		secRelease(pos))
	if *n != 0 {
		t.Fatalf("左键/Cancel 请求了 %d 次菜单", *n)
	}
}

// TestBubbleCtxCopy D92/D97 复制口径：有选区 → 选区文本优先（D91 后果⑤：副键留
// 菜单复用选态）；无选区 → 整条气泡渲染文本（assistant 复合行按键区间拼接）；
// edit 预填 user/assistant 均携带（D97 开放助手编辑）。
func TestBubbleCtxCopy(t *testing.T) {
	u := newFrameUI()
	u.m.addMsg(blockUser, "问", "u1")
	u.m.addMsg(blockAssistant, "一\n\n二", "a1")
	q := new(input.Router)
	bubbleFrame(q, u)

	// 无选区：user 整条 = 原文本（含 edit 预填）；assistant 整条 = 复合行拼接（含 edit 预填）。
	cu := u.bubbleCtx(u.hitBubble(rectCenterI(u.bubbleRects[0].rect)))
	if cu.copy != "问" || cu.edit != "问" || cu.kind != blockUser || cu.id != "u1" {
		t.Fatalf("user ctx = %+v", cu)
	}
	ca := u.bubbleCtx(u.hitBubble(rectCenterI(u.bubbleRects[1].rect)))
	if ca.copy != "一\n二" || ca.edit != "一\n\n二" || ca.kind != blockAssistant || ca.id != "a1" {
		t.Fatalf("assistant ctx = %+v, want copy 一\\n二、edit 原文", ca)
	}

	// 有选区（assistant 第二键）：复制文本 = 选区，即便右键落在 user 气泡上。
	u.sel.active = true
	u.selSpansBuf = []selSpan{{key: u.bubbleRects[1].keyBase + 1, start: 0, end: 1}}
	cs := u.bubbleCtx(u.hitBubble(rectCenterI(u.bubbleRects[0].rect)))
	if cs.copy != "二" {
		t.Fatalf("选区优先 copy = %q, want 二", cs.copy)
	}
}

// TestBubbleRectsThinkingChip D100：思考块与工具 chip 进右键命中；ctx 按块角色组装
// 复制文本与查看原文内容（thinking = 思考原文标题、chip = 调用/结果 JSON）；两者菜单
// 项集 = 复制/查看原文两项。
func TestBubbleRectsThinkingChip(t *testing.T) {
	u := newFrameUI()
	u.m.addMsg(blockUser, "问", "u1")
	u.m.addMsg(blockThinking, "想一想", "a1")
	u.m.addToolCall(tool.Call{ID: "k1", Name: "think", Args: json.RawMessage(`{"q":1}`)})
	u.m.attachToolResult(tool.Result{CallID: "k1", OK: true, Output: "完毕"})
	q := new(input.Router)
	bubbleFrame(q, u)

	var thinkHit, chipHit *bubbleHit
	for i := range u.bubbleRects {
		switch u.bubbleRects[i].kind {
		case blockThinking:
			thinkHit = &u.bubbleRects[i]
		case blockTool:
			chipHit = &u.bubbleRects[i]
		}
	}
	if thinkHit == nil || chipHit == nil {
		t.Fatalf("bubbleRects = %+v, want thinking/chip 均登记", u.bubbleRects)
	}

	// thinking ctx：复制 = 思考文本；raw = 思考原文标题；无编辑预填。
	ct := u.bubbleCtx(u.hitBubble(rectCenterI(thinkHit.rect)))
	if ct.copy != "想一想" || ct.raw.title != "思考原文 · a1" || ct.raw.text != "想一想" || ct.edit != "" {
		t.Fatalf("thinking ctx = %+v", ct)
	}

	// chip ctx：复制 = 名+参数+结果（剔除 UI 修饰）；raw = JSON 内嵌参数原值。
	cc := u.bubbleCtx(u.hitBubble(rectCenterI(chipHit.rect)))
	if want := "think\n{\"q\":1}\n结果 ✓\n完毕"; cc.copy != want {
		t.Fatalf("chip copy = %q, want %q", cc.copy, want)
	}
	if cc.raw.title != "工具调用 · think" ||
		!strings.Contains(cc.raw.text, `"tool": "think"`) ||
		!strings.Contains(cc.raw.text, `"q": 1`) {
		t.Fatalf("chip raw = %+v", cc.raw)
	}

	// 项集：thinking/tool 两项（复制/查看原文）。
	for _, kind := range []blockKind{blockThinking, blockTool} {
		items := bubbleMenuItems(kind)
		if len(items) != 2 || items[0].label != "复制" || items[1].label != "查看原文" {
			t.Fatalf("%v 菜单项 = %+v, want 复制/查看原文", kind, items)
		}
	}
}
