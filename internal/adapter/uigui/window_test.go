package uigui

import (
	"image"
	"testing"

	"gioui.org/unit"
)

// newShapeUI 形裁登记用的最小 UI（frameMetric 供淡出带剔除换算）。
func newShapeUI(pxPerDp float32) *UI {
	return &UI{frameMetric: unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp}}
}

// TestRecordClipsBandAndViewport 形裁登记（D44/§15.1）：淡出带内剔除、视口裁剪、
// 空矩形与零圆角不登记。
func TestRecordClipsBandAndViewport(t *testing.T) {
	u := newShapeUI(1) // Dp(56) = 56px 淡出带
	viewport := image.Rect(0, 0, 100, 300)

	// ① 完全在淡出带内 → 剔除（overlay 接管）。
	u.record(image.Rect(10, 0, 90, 50), 12, viewport)
	if len(u.shapes) != 0 {
		t.Fatalf("带内矩形不应登记: %+v", u.shapes)
	}
	// ② 跨带 → 顶边裁到带底。
	u.record(image.Rect(10, 20, 90, 120), 12, viewport)
	if len(u.shapes) != 1 || u.shapes[0].r.Min.Y != 56 {
		t.Fatalf("跨带矩形应裁到 y=56: %+v", u.shapes)
	}
	// ③ 带下、视口内 → 原样登记。
	u.record(image.Rect(8, 150, 92, 200), 12, viewport)
	if len(u.shapes) != 2 || u.shapes[1].r != image.Rect(8, 150, 92, 200) {
		t.Fatalf("视口内矩形应原样: %+v", u.shapes)
	}
	// ④ 视口外（滚动到上方）→ 裁空剔除。
	u.record(image.Rect(8, -60, 92, -10), 12, viewport)
	if len(u.shapes) != 2 {
		t.Fatalf("视口外不应登记: %+v", u.shapes)
	}
	// ⑤ 零圆角（如被裁成细条的异常态）→ 剔除。
	u.record(image.Rect(8, 150, 92, 200), 0, viewport)
	if len(u.shapes) != 2 {
		t.Fatalf("零圆角不应登记: %+v", u.shapes)
	}
}

// TestShapesEqual 形裁去重判定：相等才免重建。
func TestShapesEqual(t *testing.T) {
	a := []shapePhys{{x: 1, y: 2, w: 3, h: 4, ellipse: 8}}
	b := []shapePhys{{x: 1, y: 2, w: 3, h: 4, ellipse: 8}}
	if !shapesEqual(a, b) {
		t.Fatal("相同应相等")
	}
	if shapesEqual(a, nil) {
		t.Fatal("长度不同不应相等")
	}
	b[0].w = 9
	if shapesEqual(a, b) {
		t.Fatal("内容不同不应相等")
	}
	if !shapesEqual(nil, nil) {
		t.Fatal("双空应相等")
	}
}

// TestFrameItemsLive 帧内容（渲染视图）：定稿块 + 实时思考 + 流式草稿顺序。
func TestFrameItemsLive(t *testing.T) {
	u := &UI{}
	u.m = newModel(u)
	u.m.add(blockUser, "你好")
	u.m.think.WriteString("想")
	u.m.drafting = true
	u.m.draft.WriteString("答")

	items := u.frameItems()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	if items[0].kind != blockUser || items[0].live {
		t.Fatalf("items[0] = %+v", items[0])
	}
	if items[1].kind != blockThinking || !items[1].live {
		t.Fatalf("items[1] = %+v（实时思考应 live）", items[1])
	}
	if items[2].kind != blockAssistant || !items[2].live || items[2].text != "答" {
		t.Fatalf("items[2] = %+v（流式草稿应 live）", items[2])
	}
}
