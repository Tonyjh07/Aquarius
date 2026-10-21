package port

import (
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// TestRawInputAttachmentList D186 口径一：File / Blob 是单附件简写，归一化后并入列表；
// 顺序固定为「File → Attachments → Blob」（调用方按这个序生成分片）。
func TestRawInputAttachmentList(t *testing.T) {
	blob := &conversation.BlobRef{Hash: "h1", MIME: "image/png", Name: "p.png"}

	for _, tc := range []struct {
		name string
		raw  RawInput
		want []Attachment
	}{
		{"无附件", RawInput{Kind: "file"}, nil},
		{"File 简写", RawInput{Kind: "file", File: "/a.txt"},
			[]Attachment{{Path: "/a.txt"}}},
		{"Blob 简写", RawInput{Kind: "clipboard", Blob: blob},
			[]Attachment{{Blob: blob}}},
		{"列表原样", RawInput{Kind: "file", Attachments: []Attachment{{Path: "/b"}, {Path: "/c"}}},
			[]Attachment{{Path: "/b"}, {Path: "/c"}}},
		{"简写与列表混排", RawInput{Kind: "file", File: "/a", Text: "x",
			Attachments: []Attachment{{Path: "/b"}}, Blob: blob},
			[]Attachment{{Path: "/a"}, {Path: "/b"}, {Blob: blob}}},
		{"空白 File 不算附件", RawInput{Kind: "file", File: "   "}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.raw.AttachmentList()
			if len(got) != len(tc.want) {
				t.Fatalf("AttachmentList() = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i].Path != tc.want[i].Path || got[i].Blob != tc.want[i].Blob {
					t.Fatalf("第 %d 项 = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
			if (len(got) > 0) != tc.raw.HasAttachments() {
				t.Fatalf("HasAttachments() = %v, list = %+v", tc.raw.HasAttachments(), got)
			}
		})
	}
}

// TestRawInputSingleShortcutEquivalence D186：File 简写与「列表里只有这一项」等价
// （同一份端口结构体，两种写法不该有语义差）。
func TestRawInputSingleShortcutEquivalence(t *testing.T) {
	short := RawInput{Kind: "file", File: "/a.txt", Text: "t"}
	full := RawInput{Kind: "file", Attachments: []Attachment{{Path: "/a.txt"}}, Text: "t"}
	a, b := short.AttachmentList(), full.AttachmentList()
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("简写 = %+v, 列表 = %+v", a, b)
	}
}
