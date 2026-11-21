package storejson

// tag_test.go 会话标签与置顶的落盘行为（D313）：字段随会话写盘读回、缺键即零值
// （旧文件照读）、List 置顶在前并带出标签。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// TestTagsPinnedRoundTrip tags/pinned 随整树写盘读回，落盘 JSON 里两个键都在。
func TestTagsPinnedRoundTrip(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := conversation.New(conversation.ID("tagged"), "T")
	c.Tags = []string{"work", "urgent"}
	c.Pinned = true
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := s.Load(ctx, "tagged")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(back.Tags) != 2 || back.Tags[0] != "work" || back.Tags[1] != "urgent" || !back.Pinned {
		t.Fatalf("loaded tags/pinned = %v/%v, want [work urgent]/true", back.Tags, back.Pinned)
	}
	var raw struct {
		Tags   []string `json:"tags"`
		Pinned bool     `json:"pinned"`
	}
	if err := json.Unmarshal(mustReadConvFile(t, s, "tagged"), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw.Tags) != 2 || !raw.Pinned {
		t.Fatalf("on-disk tags/pinned = %v/%v", raw.Tags, raw.Pinned)
	}
}

// TestMissingTagsKeysReadAsZero 旧文件没有 tags/pinned 两个键：照常解码，零值。
func TestMissingTagsKeysReadAsZero(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := conversation.New(conversation.ID("old"), "O")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "hi"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	// 把两个键从落盘文件里抹掉，模拟旧版写出的文件。
	var m map[string]any
	if err := json.Unmarshal(mustReadConvFile(t, s, "old"), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	delete(m, "tags")
	delete(m, "pinned")
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "old.json"), data, 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	back, err := s.Load(ctx, "old")
	if err != nil {
		t.Fatalf("load old file: %v", err)
	}
	if len(back.Tags) != 0 || back.Pinned {
		t.Fatalf("old file decoded tags/pinned = %v/%v, want zero values", back.Tags, back.Pinned)
	}
}

// TestListPinnedFirstAndCarriesTags List：置顶排最前（组内仍按更新时间），摘要带标签。
func TestListPinnedFirstAndCarriesTags(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	older := conversation.New(conversation.ID("a-old"), "A")
	older.Tags = []string{"work"}
	older.Pinned = true
	if err := s.Save(ctx, older); err != nil {
		t.Fatalf("save a: %v", err)
	}
	fresh := conversation.New(conversation.ID("b-new"), "B")
	if _, err := fresh.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "hi"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Save(ctx, fresh); err != nil {
		t.Fatalf("save b: %v", err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
	// a-old 更早，但置顶把它顶到最前。
	if list[0].ID != "a-old" || !list[0].Pinned {
		t.Fatalf("first = %+v, want pinned a-old", list[0])
	}
	if len(list[0].Tags) != 1 || list[0].Tags[0] != "work" {
		t.Fatalf("summary tags = %v, want [work]", list[0].Tags)
	}
	if list[1].ID != "b-new" || list[1].Pinned || len(list[1].Tags) != 0 {
		t.Fatalf("second = %+v, want unpinned untagged b-new", list[1])
	}
}

// mustReadConvFile 读会话主文件的落盘字节（键存在性断言用）。
func mustReadConvFile(t *testing.T, s *Store, id string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil {
		t.Fatalf("read %s.json: %v", id, err)
	}
	return data
}
