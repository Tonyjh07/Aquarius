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
const exportUsage = "用法: /export [--format <名字>] [--path <文件>] [--all | --ids <id>[,<id>…]]（缺省只导当前会话；aqx 无损、落在导出目录）"

// importUsage /import 的用法文案（路径含空格时用引号括起来，与其余命令同一口径）。
const importUsage = "用法: /import [--pick <序号>]… <文件>（按扩展名与文件头自动认格式；落成新会话）"

// importFlags /import 认识的 flag：只认路径之前的一段（`--pick 1,3 <文件>`）。
var importFlags = []flagDecl{{name: "--pick", value: true}}

// multiExportStem 多会话导出的缺省文件名（没有单一会话标题可用）；口径在端口，
// 界面批量导出的预填名读同一份。
const multiExportStem = port.MultiExportStem

// spacedPathHint 参数被空格切成了几段时的提示：命令面认引号，用户只是忘了括。
//
// 只说「用法错误」等于让用户去猜哪一步不对——Windows 上导出目录带空格是常态，
// 最常见的错法就是漏了引号。
const spacedPathHint = "路径里有空格时用引号括起来，例如 /import \"C:\\我的文档\\会话.aqx\""

// execExport 实现 /export：把当前会话（或整库 / 选中的几个）写成一份文件。
func (s *Session) execExport(ctx context.Context, cmd port.Command) (string, error) {
	if s.exporter == nil {
		return "", errors.New("本构建未接入导出面（SessionDeps.Exporter）")
	}
	flags, rest, err := scanFlags(cmd.Args, flagScan{pos: flagsLeading}, exportFlags)
	if err != nil {
		return "", errors.New(exportUsage)
	}
	if len(rest) > 0 {
		// 多出来的位置参数多半是路径被空格切开了（`--path C:\我的文档\x.aqx`）。
		return "", fmt.Errorf("%s\n%s", exportUsage, spacedPathHint)
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
		return "", errors.New("--all 与 --ids 只能给一个：要么整库，要么列出来的那几个会话")
	}
	format, err := s.pickExportFormat(name)
	if err != nil {
		return "", err
	}
	if all && format.Name != "aqx" {
		// 整库动作只对 aqx 成立：其余格式的容器按别人的规范只能装一条会话链。
		return "", fmt.Errorf("%s 这种格式一次只能写一个会话：整库备份请用 aqx（去掉 --format）", format.Name)
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
			return nil, fmt.Errorf("session: 列出会话: %w", err)
		}
		for _, sm := range sums {
			ids = append(ids, sm.ID)
		}
		if len(ids) == 0 {
			return nil, errors.New("库里没有会话可导出")
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
			return nil, fmt.Errorf("session: 载入会话 %s: %w", id, err)
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
		return port.ExchangeFormat{}, errors.New("导出面没有提供任何格式")
	}
	for _, f := range formats {
		if f.Name != name {
			continue
		}
		if f.NoExport {
			// 只读的那一档：读得进来但我们不产出第二套同义格式，说清该用哪一份代替。
			return port.ExchangeFormat{}, fmt.Errorf("%s 只认不写（%s）", f.Name, f.LossNote)
		}
		return f, nil
	}
	return port.ExchangeFormat{}, fmt.Errorf("不认识的导出格式 %q（可写：%s）", name, writableFormatNames(formats))
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
		return "", errors.New("未配置导出目录（SessionDeps.ExportsDir），请用 --path 指定文件")
	}
	if err := os.MkdirAll(s.exportsDir, 0o755); err != nil {
		return "", fmt.Errorf("创建导出目录: %w", err)
	}
	return filepath.Join(s.exportsDir, stem+f.Ext), nil
}

// writeExport 落盘：写一半失败就把半截文件删掉——留个残档比没有更坏（用户会以为备份成了）。
func (s *Session) writeExport(ctx context.Context, path string, f port.ExchangeFormat, cs []*conversation.Conversation) (port.ExchangeReport, error) {
	var rep port.ExchangeReport
	out, err := os.Create(path)
	if err != nil {
		return rep, fmt.Errorf("创建导出文件: %w", err)
	}
	rep, err = s.exporter.Export(ctx, cs, f.Name, out)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path) // 半截文件不留
		return rep, fmt.Errorf("导出失败: %w", err)
	}
	return rep, nil
}

// execImport 实现 /import：认格式 → 读入（可只取其中几项）→ 全部落成新会话 → 切到第一个并回放。
func (s *Session) execImport(ctx context.Context, cmd port.Command) (string, error) {
	if s.importer == nil {
		return "", errors.New("本构建未接入导入面（SessionDeps.Importer）")
	}
	flags, rest, err := scanFlags(cmd.Args, flagScan{pos: flagsLeading}, importFlags)
	if err != nil {
		return "", errors.New(importUsage)
	}
	if len(rest) != 1 {
		// 多于一个参数多半是路径被空格切开了：说清怎么括，而不是只说「用法错误」。
		if len(rest) > 1 {
			return "", fmt.Errorf("%s\n%s", importUsage, spacedPathHint)
		}
		return "", errors.New(importUsage)
	}
	pick, err := parsePicks(flags)
	if err != nil {
		return "", err
	}
	path := rest[0]
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("打开文件: %w", err)
	}
	defer f.Close()
	head, err := readHead(f)
	if err != nil {
		return "", err
	}
	format, ok := s.importer.Detect(filepath.Base(path), head)
	if !ok {
		return "", fmt.Errorf("认不出这份文件是什么格式（支持：%s）", importFormatNames())
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("读取文件: %w", err)
	}
	trees, rep, err := s.importer.Import(ctx, f, format, pick)
	if err != nil {
		return "", fmt.Errorf("导入失败: %w", err)
	}
	if len(trees) == 0 {
		return "", errors.New("导入失败: 文件里没有可导入的会话")
	}
	// 先全部自检并取好身份、再一次性落盘：第 3 个会话坏了，不该留下前 2 个已入库的半截状态。
	// 新会话 ID 与 /new 同源（端口只管「文件 → 树」，会话身份由这里分配）。Root 节点 ID
	// 恒等于会话 ID（domain 的构造口径），所以换 ID 要连 Root 和挂在它下面的边一起换。
	imported := make([]*conversation.Conversation, 0, len(trees))
	for _, c := range trees {
		c, err := remapConversationID(c, s.ids.ConversationID())
		if err != nil {
			return "", fmt.Errorf("分配导入会话的身份: %w", err)
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
			return "", fmt.Errorf("保存导入的会话: %w", err)
		}
		saved = append(saved, c.ID)
	}
	s.cancelWatchersFor(s.cur.ID) // 旧会话的后台任务报告不再属于这里
	s.cur = imported[0]
	// 会话已经落盘并切过来了：从这里往下是「界面刷新」，失败时不能把已完成的事实报成失败。
	// 用户要知道「导入成功了，但界面没刷干净」，否则会以为白导一次。
	if err := s.emitClear(ctx); err != nil {
		return "", fmt.Errorf("会话已导入并切换，但界面没能刷新: %w", err)
	}
	if _, err := s.replayHistory(ctx, "已导入"); err != nil {
		return "", fmt.Errorf("会话已导入并切换，但历史没能回放: %w", err)
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
			return nil, fmt.Errorf("--pick 只接受 1 起的序号（收到 %q）；多个序号写成 --pick 1,3", v)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("--pick 后面没给序号（写成 --pick 1,3）")
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
		return nil, fmt.Errorf("导入的会话结构不合法: %w", err)
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
		return nil, fmt.Errorf("读取文件头: %w", err)
	}
	return head[:n], nil
}

// headBytes 判定格式时读的文件头上限。
const headBytes = 64 << 10

// exportReceipt 导出回执：落在哪、什么格式、多少条、以及这次没带出去什么。
func exportReceipt(f port.ExchangeFormat, path string, rep port.ExchangeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "已导出到 %s\n", path)
	fmt.Fprintf(&b, "格式: %s（%s）— %s\n", f.Name, f.Label, fidelityLine(f))
	if len(rep.Entries) > 1 {
		fmt.Fprintf(&b, "内容: %d 个会话，%d 条消息，%s\n", len(rep.Entries), rep.Nodes, humanBytes(rep.Bytes))
		for _, e := range rep.Entries {
			fmt.Fprintf(&b, "- %s · %d 条\n", entryTitle(e), e.Nodes)
		}
	} else {
		fmt.Fprintf(&b, "内容: %d 条消息，%s", rep.Nodes, humanBytes(rep.Bytes))
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
	fmt.Fprintf(&b, "已导入 %s\n", path)
	fmt.Fprintf(&b, "格式: %s（%s）— %s\n", f.Name, f.Label, fidelityLine(f))
	if len(rep.Entries) > 1 {
		fmt.Fprintf(&b, "内容: 已导入 %d 个会话（%d 条消息）\n", len(rep.Entries), rep.Nodes)
		for _, e := range rep.Entries {
			fmt.Fprintf(&b, "- %s · %d 条\n", entryTitle(e), e.Nodes)
		}
	} else {
		fmt.Fprintf(&b, "内容: %d 条消息（已落成新会话）", rep.Nodes)
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
	return fmt.Sprintf("第 %d 个会话", e.Index)
}

// fidelityLine 这一份是有损还是无损；有损时把「丢什么」写在后面（用户据此判断要不要换格式）。
func fidelityLine(f port.ExchangeFormat) string {
	if !f.Lossy {
		return "无损，可原样导回"
	}
	if f.LossNote == "" {
		return "有损"
	}
	return "有损：" + f.LossNote
}

// writeDropped 回执里逐项列出丢了什么；没有就整段不出现（不写「没有丢失」这种空话）。
func writeDropped(b *strings.Builder, dropped []string) {
	if len(dropped) == 0 {
		return
	}
	b.WriteString("\n没有带过去的内容:\n")
	for _, d := range dropped {
		fmt.Fprintf(b, "- %s\n", d)
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
		return "", fmt.Errorf("检查目标文件: %w", err)
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := os.Stat(cand); errors.Is(err, os.ErrNotExist) {
			return cand, nil
		}
	}
	return "", errors.New("导出目录里同名文件太多，请用 --path 指定文件名")
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
