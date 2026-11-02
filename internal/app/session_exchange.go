package app

// session_exchange.go `/export` 与 `/import` 的命令面（§5.16）。
//
// 命令面只做「文件与树之间的搬运」这一层：格式清单、保真边界、丢什么由端口与适配器给出
// （port.ExchangeFormats），这里负责挑路径、防覆盖、落了半截就删、以及把回执写成用户能读的
// 几行。导入落成**新**会话并清屏回放——与 `/switch` 同一口径，不覆盖既有会话。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// exportFlags /export 认识的 flag：格式名、目标文件、选择哪几个会话，都只认正文之前的一段。
var exportFlags = []flagDecl{
	{name: "--format", value: true},
	{name: "--path", value: true},
	{name: "--all"},
	{name: "--ids", value: true},
}

// exportUsage /export 的用法文案。
func exportUsage() string { return i18n.T("usage.export") }

// importUsage /import 的用法文案（路径含空格时用引号括起来，与其余命令同一口径）。
func importUsage() string { return i18n.T("usage.import") }

// importFlags /import 认识的 flag：只认路径之前的一段（`--pick 1,3 <文件>`）。
var importFlags = []flagDecl{{name: "--pick", value: true}}

// multiExportStem 多会话导出的缺省文件名（没有单一会话标题可用）；口径在端口，
// 界面批量导出的预填名读同一份。
const multiExportStem = port.MultiExportStem

// spacedPathHint 参数被空格切成了几段时的提示：命令面认引号，用户只是忘了括。
//
// 只说「用法错误」等于让用户去猜哪一步不对——Windows 上导出目录带空格是常态，
// 最常见的错法就是漏了引号。
func spacedPathHint() string { return i18n.T("exchange.spaced_hint") }

// execExport 实现 /export：把当前会话（或整库 / 选中的几个）写成一份文件。
func (s *Session) execExport(ctx context.Context, cmd port.Command) (string, error) {
	if s.exporter == nil {
		return "", errors.New(i18n.T("exchange.exporter_missing"))
	}
	flags, rest, err := scanFlags(cmd.Args, flagScan{pos: flagsLeading}, exportFlags)
	if err != nil {
		return "", errors.New(exportUsage())
	}
	if len(rest) > 0 {
		// 多出来的位置参数多半是路径被空格切开了（`--path C:\我的文档\x.aqx`）。
		return "", fmt.Errorf("%s\n%s", exportUsage(), spacedPathHint())
	}
	var (
		name  string
		want  string
		all   bool
		idSet []string
	)
	for _, f := range flags {
		switch f.name {
		case "--format":
			name = f.value
		case "--path":
			want = f.value
		case "--all":
			all = true
		case "--ids":
			idSet = append(idSet, f.value)
		}
	}
	if all && len(idSet) > 0 {
		return "", errors.New(i18n.T("exchange.all_and_ids"))
	}
	format, err := s.pickExportFormat(name)
	if err != nil {
		return "", err
	}
	if all && format.Name != "aqx" {
		// 整库动作只对 aqx 成立：其余格式的容器按别人的规范只能装一条会话链。
		return "", errors.New(i18n.Tf("exchange.format_single", "format", format.Name))
	}
	cs, err := s.exportTargets(ctx, all, idSet)
	if err != nil {
		return "", err
	}
	// 一个会话（缺省形态）用它自己的标题起名；--all / --ids 与多会话没有单一标题可用，
	// 取一个中性的缺省名（同一目录，同样走防覆盖后缀）。
	stem := multiExportStem
	if !all && len(idSet) == 0 && len(cs) == 1 {
		stem = port.FileStem(cs[0].Title)
	}
	path, err := s.exportPath(want, format, stem)
	if err != nil {
		return "", err
	}
	path, err = uniquePath(path)
	if err != nil {
		return "", err
	}
	rep, err := s.writeExport(ctx, path, format, cs)
	if err != nil {
		return "", err
	}
	return exportReceipt(format, path, rep), nil
}

// exportTargets 这次导出写哪几个会话：缺省当前会话，--all 整库（List 序 = 左栏顺序），
// --ids 只要列出来的（顺序照用户给的那串，写序即归档清单顺序）。
func (s *Session) exportTargets(ctx context.Context, all bool, idSet []string) ([]*conversation.Conversation, error) {
	if !all && len(idSet) == 0 {
		return []*conversation.Conversation{s.cur}, nil
	}
	var ids []conversation.ID
	if all {
		sums, err := s.store.List(ctx)
		if err != nil {
			return nil, wrapSession("err.list_conversations", err)
		}
		for _, sm := range sums {
			ids = append(ids, sm.ID)
		}
		if len(ids) == 0 {
			return nil, errors.New(i18n.T("exchange.no_conversations"))
		}
	} else {
		// 先全部解析、再去重：有一个 ID 解析不出就一个都不写（不静默跳过）。
		seen := make(map[conversation.ID]bool, len(idSet))
		for _, raw := range splitList(idSet) {
			id, err := s.resolveConversation(ctx, raw)
			if err != nil {
				return nil, err
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	out := make([]*conversation.Conversation, 0, len(ids))
	for _, id := range ids {
		if id == s.cur.ID {
			// 当前会话可能只改了内存（/edit 一类），取内存里这份才不丢改动。
			out = append(out, s.cur)
			continue
		}
		c, err := s.store.Load(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("session: %s: %w", i18n.Tf("err.load_conversation", "id", id), err)
		}
		out = append(out, c)
	}
	return out, nil
}

// splitList 把可重复、可逗号分隔的取值摊平（`--ids a,b` 与 `--ids a --ids b` 等价）。
func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// pickExportFormat 按 --format 取格式事实；缺省 aqx。只读那档与不认识的名字分别报因
// （都列出可写清单），不落到含糊的「未知格式」。
func (s *Session) pickExportFormat(name string) (port.ExchangeFormat, error) {
	formats := s.exporter.Formats()
	if name == "" {
		for _, f := range formats {
			if f.Name == "aqx" {
				return f, nil
			}
		}
		if len(formats) > 0 {
			return formats[0], nil
		}
		return port.ExchangeFormat{}, errors.New(i18n.T("exchange.no_formats"))
	}
	for _, f := range formats {
		if f.Name != name {
			continue
		}
		if f.NoExport {
			// 只读的那一档：读得进来但我们不产出第二套同义格式，说清该用哪一份代替。
			return port.ExchangeFormat{}, errors.New(i18n.Tf("exchange.format_readonly", "format", f.Name, "note", f.LossNote))
		}
		return f, nil
	}
	return port.ExchangeFormat{}, errors.New(i18n.Tf("exchange.unknown_format", "name", fmt.Sprintf("%q", name), "names", writableFormatNames(formats)))
}

// exportPath 目标路径：显式 --path 优先（给了目录就放缺省文件名）；否则落在导出目录。
func (s *Session) exportPath(want string, f port.ExchangeFormat, stem string) (string, error) {
	if stem == "" {
		stem = "aquarius-" + string(s.cur.ID)
	}
	if want != "" {
		if fi, err := os.Stat(want); err == nil && fi.IsDir() {
			return filepath.Join(want, stem+f.Ext), nil
		}
		return want, nil
	}
	if s.exportsDir == "" {
		return "", errors.New(i18n.T("exchange.no_exports_dir"))
	}
	if err := os.MkdirAll(s.exportsDir, 0o755); err != nil {
		return "", wrapSession("err.mkdir_export", err)
	}
	return filepath.Join(s.exportsDir, stem+f.Ext), nil
}

// writeExport 落盘：写一半失败就把半截文件删掉——留个残档比没有更坏（用户会以为备份成了）。
func (s *Session) writeExport(ctx context.Context, path string, f port.ExchangeFormat, cs []*conversation.Conversation) (port.ExchangeReport, error) {
	var rep port.ExchangeReport
	out, err := os.Create(path)
	if err != nil {
		return rep, wrapSession("err.create_export", err)
	}
	rep, err = s.exporter.Export(ctx, cs, f.Name, out)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path) // 半截文件不留
		return rep, wrapSession("err.export_failed", err)
	}
	return rep, nil
}

// execImport 实现 /import：认格式 → 读入（可只取其中几项）→ 全部落成新会话 → 切到第一个并回放。
func (s *Session) execImport(ctx context.Context, cmd port.Command) (string, error) {
	if s.importer == nil {
		return "", errors.New(i18n.T("exchange.importer_missing"))
	}
	flags, rest, err := scanFlags(cmd.Args, flagScan{pos: flagsLeading}, importFlags)
	if err != nil {
		return "", errors.New(importUsage())
	}
	if len(rest) != 1 {
		// 多于一个参数多半是路径被空格切开了：说清怎么括，而不是只说「用法错误」。
		if len(rest) > 1 {
			return "", fmt.Errorf("%s\n%s", importUsage(), spacedPathHint())
		}
		return "", errors.New(importUsage())
	}
	pick, err := parsePicks(flags)
	if err != nil {
		return "", err
	}
	path := rest[0]
	f, err := os.Open(path)
	if err != nil {
		return "", wrapSession("err.open_file", err)
	}
	defer f.Close()
	head, err := readHead(f)
	if err != nil {
		return "", err
	}
	format, ok := s.importer.Detect(filepath.Base(path), head)
	if !ok {
		return "", errors.New(i18n.Tf("exchange.unknown_import", "formats", importFormatNames()))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", wrapSession("err.read_file", err)
	}
	trees, rep, err := s.importer.Import(ctx, f, format, pick)
	if err != nil {
		return "", wrapSession("err.import_failed", err)
	}
	if len(trees) == 0 {
		return "", errors.New(i18n.T("exchange.empty_import"))
	}
	// 先全部自检并取好身份、再一次性落盘：第 3 个会话坏了，不该留下前 2 个已入库的半截状态。
	// 新会话 ID 与 /new 同源（端口只管「文件 → 树」，会话身份由这里分配）。Root 节点 ID
	// 恒等于会话 ID（domain 的构造口径），所以换 ID 要连 Root 和挂在它下面的边一起换。
	imported := make([]*conversation.Conversation, 0, len(trees))
	for _, c := range trees {
		c, err := remapConversationID(c, s.ids.ConversationID())
		if err != nil {
			return "", wrapSession("err.assign_import_id", err)
		}
		imported = append(imported, c)
	}
	saved := make([]conversation.ID, 0, len(imported))
	for _, c := range imported {
		if err := s.store.Save(ctx, c); err != nil {
			// 回滚本批已落盘的：半截导入比没导入更糟——用户不知道哪几个进来了。
			for _, id := range saved {
				_ = s.store.Remove(ctx, id)
			}
			return "", wrapSession("err.save_import", err)
		}
		saved = append(saved, c.ID)
	}
	s.cur = imported[0]
	// 会话已经落盘并切过来了：从这里往下是「界面刷新」，失败时不能把已完成的事实报成失败。
	// 用户要知道「导入成功了，但界面没刷干净」，否则会以为白导一次。
	if err := s.emitClear(ctx); err != nil {
		return "", wrapSession("exchange.refresh_failed", err)
	}
	if _, err := s.replayHistory(ctx, "replay.verb.imported"); err != nil {
		return "", wrapSession("exchange.replay_failed", err)
	}
	rep.Format = format.Name
	return importReceipt(path, format, rep), nil
}

// parsePicks 把 `--pick` 的取值解成序号清单：可重复、可逗号分隔（`--pick 1,3`）。
//
// 只在这里做「是不是正整数」的格式判断；「有没有这一项」要等读到清单才知道，由适配器报因
// 并附上清单——壳里拿不到清单，提前猜只会把越界报成格式错误。
func parsePicks(flags []cmdFlag) ([]int, error) {
	var raw []string
	for _, f := range flags {
		if f.name == "--pick" {
			raw = append(raw, f.value)
		}
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var out []int
	for _, v := range splitList(raw) {
		n, ok := parsePickIndex(v)
		if !ok {
			return nil, errors.New(i18n.Tf("exchange.bad_pick", "value", fmt.Sprintf("%q", v)))
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New(i18n.T("exchange.pick_missing"))
	}
	return out, nil
}

// parsePickIndex 序号字面量：正整数（0 起是另一种口径，写错就会导错文件里的另一项）。
func parsePickIndex(v string) (int, bool) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// remapConversationID 换会话 ID：Root 节点 ID 恒等于会话 ID（domain 的构造口径），所以
// Root 的 ID、它自己那条结构与挂在它上面的边要一起换，换完再走 Restore 的全树自检。
func remapConversationID(c *conversation.Conversation, id conversation.ID) (*conversation.Conversation, error) {
	snap := c.Snapshot()
	oldRoot := conversation.MessageID(snap.ID)
	newRoot := conversation.MessageID(id)
	snap.ID = id
	for i := range snap.Nodes {
		switch snap.Nodes[i].ID {
		case oldRoot:
			snap.Nodes[i].ID = newRoot
		default:
			if snap.Nodes[i].Parent == oldRoot {
				snap.Nodes[i].Parent = newRoot
			}
		}
	}
	for i := range snap.Edges {
		if snap.Edges[i].Parent == oldRoot {
			snap.Edges[i].Parent = newRoot
		}
		for j := range snap.Edges[i].Childs {
			if snap.Edges[i].Childs[j] == oldRoot {
				snap.Edges[i].Childs[j] = newRoot
			}
		}
	}
	if snap.Head == oldRoot {
		snap.Head = newRoot
	}
	for i := range snap.RevisedFrom {
		if snap.RevisedFrom[i].Old == oldRoot {
			snap.RevisedFrom[i].Old = newRoot
		}
		if snap.RevisedFrom[i].New == oldRoot {
			snap.RevisedFrom[i].New = newRoot
		}
	}
	out, err := conversation.Restore(snap)
	if err != nil {
		return nil, wrapSession("exchange.bad_structure", err)
	}
	return out, nil
}

// readHead 读文件头（Detect 只看形状）：不足 headBytes 不算错，短文件照样能认。
//
// 64 KB 而不是几百字节：外层数组/对象的前几百字节里常常连一个键都还没出现（ChatGPT 导出
// 先写长标题与时间戳），头部太短会把合法文件判成「认不出」。读 64 KB 的成本相对整份文件
// 可以忽略（Import 本来就要整份读进内存做上限检查）。
func readHead(r io.Reader) ([]byte, error) {
	head := make([]byte, headBytes)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, wrapSession("err.read_head", err)
	}
	return head[:n], nil
}

// headBytes 判定格式时读的文件头上限。
const headBytes = 64 << 10

// exportReceipt 导出回执：落在哪、什么格式、多少条、以及这次没带出去什么。
func exportReceipt(f port.ExchangeFormat, path string, rep port.ExchangeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", i18n.Tf("exchange.export.to", "path", path))
	fmt.Fprintf(&b, "%s\n", i18n.Tf("exchange.format",
		"name", f.Name, "label", f.Label, "fidelity", fidelityLine(f)))
	if len(rep.Entries) > 1 {
		fmt.Fprintf(&b, "%s\n", i18n.Tf("exchange.export.multi",
			"convs", len(rep.Entries), "nodes", rep.Nodes, "bytes", humanBytes(rep.Bytes)))
		for _, e := range rep.Entries {
			fmt.Fprintf(&b, "%s\n", i18n.Tf("exchange.entry", "title", entryTitle(e), "nodes", e.Nodes))
		}
	} else {
		fmt.Fprintf(&b, "%s", i18n.Tf("exchange.export.single",
			"nodes", rep.Nodes, "bytes", humanBytes(rep.Bytes)))
	}
	writeDropped(&b, rep.Dropped)
	return strings.TrimRight(b.String(), "\n")
}

// importReceipt 导入回执：落成哪个会话、什么格式、读进来多少条、这次没带进来什么。
//
// 多会话时内容行换成「已导入 N 个会话（M 条消息）」再逐条列「标题 · 条数」；只有一个会话
// 时维持三行老样子——不为多会话把最常见的单会话回执拖长。
func importReceipt(path string, f port.ExchangeFormat, rep port.ExchangeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", i18n.Tf("exchange.import.to", "path", path))
	fmt.Fprintf(&b, "%s\n", i18n.Tf("exchange.format",
		"name", f.Name, "label", f.Label, "fidelity", fidelityLine(f)))
	if len(rep.Entries) > 1 {
		fmt.Fprintf(&b, "%s\n", i18n.Tf("exchange.import.multi",
			"convs", len(rep.Entries), "nodes", rep.Nodes))
		for _, e := range rep.Entries {
			fmt.Fprintf(&b, "%s\n", i18n.Tf("exchange.entry", "title", entryTitle(e), "nodes", e.Nodes))
		}
	} else {
		fmt.Fprintf(&b, "%s", i18n.Tf("exchange.import.one", "nodes", rep.Nodes))
	}
	writeDropped(&b, rep.Dropped)
	return strings.TrimRight(b.String(), "\n")
}

// entryTitle 清单条目的显示名：标题为空时退回源 ID，免得列出一行空白。
func entryTitle(e port.ExchangeEntry) string {
	if e.Title != "" {
		return e.Title
	}
	if e.ID != "" {
		return e.ID
	}
	return i18n.Tf("exchange.entry.untitled", "index", e.Index)
}

// fidelityLine 这一份是有损还是无损；有损时把「丢什么」写在后面（用户据此判断要不要换格式）。
func fidelityLine(f port.ExchangeFormat) string {
	if !f.Lossy {
		return i18n.T("exchange.fidelity.lossless")
	}
	if f.LossNote == "" {
		return i18n.T("exchange.fidelity.lossy")
	}
	return i18n.Tf("exchange.fidelity.lossy_note", "note", f.LossNote)
}

// writeDropped 回执里逐项列出丢了什么；没有就整段不出现（不写「没有丢失」这种空话）。
func writeDropped(b *strings.Builder, dropped []string) {
	if len(dropped) == 0 {
		return
	}
	b.WriteString("\n" + i18n.T("exchange.dropped.head") + "\n")
	for _, d := range dropped {
		fmt.Fprintf(b, "%s\n", i18n.Tf("exchange.dropped.item", "item", d))
	}
}

// writableFormatNames 可写格式名清单（错误文案用，表序即用法顺序）。
func writableFormatNames(formats []port.ExchangeFormat) string {
	var names []string
	for _, f := range formats {
		if !f.NoExport {
			names = append(names, f.Name)
		}
	}
	return strings.Join(names, " / ")
}

// importFormatNames 可读格式名清单（认不出时告诉用户能喂什么）。
func importFormatNames() string {
	var names []string
	for _, f := range port.ExchangeFormats() {
		names = append(names, f.Name)
	}
	return strings.Join(names, " / ")
}

// uniquePath 不覆盖已有文件：重名就加 -2、-3……后缀。导出是「再存一份」，不是「改那一份」。
func uniquePath(path string) (string, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path, nil
	} else if err != nil {
		return "", wrapSession("err.stat_target", err)
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := os.Stat(cand); errors.Is(err, os.ErrNotExist) {
			return cand, nil
		}
	}
	return "", errors.New(i18n.T("exchange.too_many_names"))
}

// humanBytes 字节数的可读写法（导出回执里给用户看）。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KB", "MB", "GB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f TB", value/unit)
}
