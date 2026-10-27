package exchange

// golden_test.go 三种互通格式的产物 golden（DESIGN §5.16 验收口径）。
//
// 为什么锁字节而不是锁字段：这些文件是**给别人的程序读**的——字段名、嵌套形状、时间格式
// 全都由对方的规范决定，只有把整份产物钉住，才拦得住「我们这边顺手改了个键名、对方读不
// 出来」这类回归。树形与线性各一种，附件带着（aqx 是 ZIP，不比文本 golden，它的确定性
// 由 TestAQXDeterministic 守）。
//
// 首次生成/有意改动后重写：go test ./internal/adapter/exchange -run TestInteropFormatsGolden -update

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// updateGolden 为 true 时重写 golden 文件（只在确认产物变化是预期的时候用）。
var updateGolden = flag.Bool("update", false, "重写 golden 产物文件")

// goldenBase 验收树的固定时间原点：导出产物里带时间戳，不固定的树没法做 golden。
var goldenBase = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// goldenTree 与 fixture 同形（分叉、版本链、思考、工具、注入、附件），但节点 ID 与时间戳
// 都是固定值——同一棵「内容」的树，产物才可比。
//
// 为什么要连 ID 一起固定：fixture 走 Append/Revise，节点 ID 是 ULID（随进程与时间而变），
// 而 chatgpt 产物里带着 ID，不固定就没有 golden 可言。固定 ID 只是「把随机的部分换成
// 序号」，树的结构与内容仍是同一棵——往返、丢东西那些性质不受影响。
//
// 经 Snapshot/Restore 改写：节点创建后只读，测试也不能绕过这条不变量直接改树。
func goldenTree(t *testing.T, blobs port.AttachmentStore) *conversation.Conversation {
	t.Helper()
	snap := fixture(t, blobs).Snapshot()
	snap.CreatedAt, snap.UpdatedAt = goldenBase, goldenBase
	for i := range snap.Nodes {
		snap.Nodes[i].CreatedAt = goldenBase.Add(time.Duration(i) * time.Minute)
	}
	fixTreeIDs(&snap)
	c, err := conversation.Restore(snap)
	if err != nil {
		t.Fatalf("固定 ID 与时间戳后重建: %v", err)
	}
	return c
}

// fixTreeIDs 把快照里的 ID 换成 N1、N2……（Root 换成会话 ID 本身，Root ID 恒等会话是树
// 不变量）；边、父指针、Head 与版本链一起换。
func fixTreeIDs(s *conversation.Snapshot) {
	ids := make([]conversation.MessageID, 0, len(s.Nodes))
	for _, m := range s.Nodes {
		ids = append(ids, m.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	ren := make(map[conversation.MessageID]conversation.MessageID, len(ids))
	newRoot := conversation.MessageID(s.ID)
	for i, id := range ids {
		ren[id] = conversation.MessageID(fmt.Sprintf("N%d", i+1))
	}
	for _, m := range s.Nodes {
		if m.Role == conversation.RoleRoot {
			ren[m.ID] = newRoot
		}
	}
	for i := range s.Nodes {
		s.Nodes[i].ID = ren[s.Nodes[i].ID]
		if p := s.Nodes[i].Parent; p != "" {
			s.Nodes[i].Parent = ren[p]
		}
	}
	for i := range s.Edges {
		if s.Edges[i].Parent != "" {
			s.Edges[i].Parent = ren[s.Edges[i].Parent]
		}
		for j := range s.Edges[i].Childs {
			s.Edges[i].Childs[j] = ren[s.Edges[i].Childs[j]]
		}
	}
	if s.Head != "" {
		s.Head = ren[s.Head]
	}
	for i := range s.RevisedFrom {
		s.RevisedFrom[i].New = ren[s.RevisedFrom[i].New]
		s.RevisedFrom[i].Old = ren[s.RevisedFrom[i].Old]
	}
}

// pinLocalUTC 把「本地时区」钉成 UTC：golden 产物里有按本地时间打的时间串，若跟着
// 运行机器的时区走，同一份代码在不同机器上就会产出不同字节。
func pinLocalUTC(t *testing.T) {
	t.Helper()
	loc := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = loc })
}

// TestInteropFormatsGolden 三份互通产物逐字节锁定，且同一棵树导两次字节相同。
func TestInteropFormatsGolden(t *testing.T) {
	pinLocalUTC(t) // sillytavern 的时间串按本地时区打，golden 必须与运行机器的时区无关
	blobs := mustBlobs(t)
	ex := New(Options{Blobs: blobs})
	src := goldenTree(t, blobs)
	for _, name := range []string{"chatgpt", "openai", "sillytavern"} {
		name := name
		t.Run(name, func(t *testing.T) {
			var first bytes.Buffer
			rep, err := ex.Export(context.Background(), src, name, &first)
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			if rep.Bytes != int64(first.Len()) {
				t.Fatalf("回执字节数 = %d, 实际写出 %d", rep.Bytes, first.Len())
			}
			// 同一棵树导两次字节相同：产物里不该有 map 遍历序、当前时间之类的东西。
			var second bytes.Buffer
			if _, err := ex.Export(context.Background(), src, name, &second); err != nil {
				t.Fatalf("export#2: %v", err)
			}
			if !bytes.Equal(first.Bytes(), second.Bytes()) {
				t.Fatal("同一棵树导两次字节不同（产物里有非确定性内容）")
			}
			compareGolden(t, name, first.Bytes())

			// 产物还得能被自己认回来、读得进——golden 锁的是「别人读得懂」，不是一段死文本。
			format, ok := ex.Detect(name+"-golden", first.Bytes())
			if !ok || format.Name != name {
				t.Fatalf("产物认不出自己: %+v, %v", format, ok)
			}
			got, _, err := ex.Import(context.Background(), bytes.NewReader(first.Bytes()), format)
			if err != nil {
				t.Fatalf("读回自己的产物: %v", err)
			}
			if got.NodeCount() < 2 {
				t.Fatalf("读回的节点数 = %d，产物没有内容", got.NodeCount())
			}
		})
	}
}

// compareGolden 与 testdata/<name>.golden 比对；-update 时重写。
func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("建 testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("写 golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 golden %s 失败（首次生成: go test ./internal/adapter/exchange -run TestInteropFormatsGolden -update）: %v", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("%s 与 golden 不一致（确认无误后 -update 重写）:\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}
