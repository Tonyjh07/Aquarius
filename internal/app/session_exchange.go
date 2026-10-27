package app

// session_exchange.go `/export` 与 `/import` 的命令面（DESIGN §5.16）。
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
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// exportFlags /export 认识的 flag：格式名与目标文件，都只认正文之前的一段。
var exportFlags = []flagDecl{{name: "--format", value: true}, {name: "--path", value: true}}

// exportUsage /export 的用法文案。
const exportUsage = "用法: /export [--format <名字>] [--path <文件>]（缺省 aqx 无损、落在导出目录）"

// importUsage /import 的用法文案。
const importUsage = "用法: /import <文件>（按扩展名与文件头自动认格式；落成一个新会话）"

// execExport 实现 /export：把当前会话写成一份文件。
func (s *Session) execExport(ctx context.Context, cmd port.Command) (string, error) {
	if s.exporter == nil {
		return "", errors.New("本构建未接入导出面（SessionDeps.Exporter）")
	}
	flags, rest, err := scanFlags(cmd.Args, flagScan{pos: flagsLeading}, exportFlags)
	if err != nil {
		return "", errors.New(exportUsage)
	}
	if len(rest) > 0 {
		return "", errors.New(exportUsage)
	}
	name, want := "", ""
	for _, f := range flags {
		switch f.name {
		case "--format":
			name = f.value
		case "--path":
			want = f.value
		}
	}
	format, err := s.pickExportFormat(name)
	if err != nil {
		return "", err
	}
	path, err := s.exportPath(want, format)
	if err != nil {
		return "", err
	}
	path, err = uniquePath(path)
	if err != nil {
		return "", err
	}
	rep, err := s.writeExport(ctx, path, format)
	if err != nil {
		return "", err
	}
	return exportReceipt(format, path, rep), nil
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
func (s *Session) exportPath(want string, f port.ExchangeFormat) (string, error) {
	base := port.FileStem(s.cur.Title)
	if base == "" {
		base = "aquarius-" + string(s.cur.ID)
	}
	if want != "" {
		if fi, err := os.Stat(want); err == nil && fi.IsDir() {
			return filepath.Join(want, base+f.Ext), nil
		}
		return want, nil
	}
	if s.exportsDir == "" {
		return "", errors.New("未配置导出目录（SessionDeps.ExportsDir），请用 --path 指定文件")
	}
	if err := os.MkdirAll(s.exportsDir, 0o755); err != nil {
		return "", fmt.Errorf("创建导出目录: %w", err)
	}
	return filepath.Join(s.exportsDir, base+f.Ext), nil
}

// writeExport 落盘：写一半失败就把半截文件删掉——留个残档比没有更坏（用户会以为备份成了）。
func (s *Session) writeExport(ctx context.Context, path string, f port.ExchangeFormat) (port.ExchangeReport, error) {
	var rep port.ExchangeReport
	out, err := os.Create(path)
	if err != nil {
		return rep, fmt.Errorf("创建导出文件: %w", err)
	}
	rep, err = s.exporter.Export(ctx, s.cur, f.Name, out)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path) // 半截文件不留
		return rep, fmt.Errorf("导出失败: %w", err)
	}
	return rep, nil
}

// execImport 实现 /import：认格式 → 读入 → 落成新会话 → 切过去并回放。
func (s *Session) execImport(ctx context.Context, cmd port.Command) (string, error) {
	if s.importer == nil {
		return "", errors.New("本构建未接入导入面（SessionDeps.Importer）")
	}
	if len(cmd.Args) != 1 {
		return "", errors.New(importUsage)
	}
	path := cmd.Args[0]
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
	c, rep, err := s.importer.Import(ctx, f, format)
	if err != nil {
		return "", fmt.Errorf("导入失败: %w", err)
	}
	// 新会话 ID 与 /new 同源（端口只管「文件 → 树」，会话身份由这里分配）。Root 节点 ID
	// 恒等于会话 ID（domain 的构造口径），所以换 ID 要连 Root 和挂在它下面的边一起换。
	c, err = remapConversationID(c, s.ids.ConversationID())
	if err != nil {
		return "", fmt.Errorf("分配导入会话的身份: %w", err)
	}
	if err := s.store.Save(ctx, c); err != nil {
		return "", fmt.Errorf("保存导入的会话: %w", err)
	}
	s.cancelWatchersFor(s.cur.ID) // 旧会话的后台任务报告不再属于这里
	s.cur = c
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "已导入"); err != nil {
		return "", err
	}
	rep.Format = format.Name
	return importReceipt(path, format, rep), nil
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
	fmt.Fprintf(&b, "内容: %d 条消息，%s", rep.Nodes, humanBytes(rep.Bytes))
	writeDropped(&b, rep.Dropped)
	return strings.TrimRight(b.String(), "\n")
}

// importReceipt 导入回执：落成哪个会话、什么格式、读进来多少条、这次没带进来什么。
func importReceipt(path string, f port.ExchangeFormat, rep port.ExchangeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "已导入 %s\n", path)
	fmt.Fprintf(&b, "格式: %s（%s）— %s\n", f.Name, f.Label, fidelityLine(f))
	fmt.Fprintf(&b, "内容: %d 条消息（已落成新会话）", rep.Nodes)
	writeDropped(&b, rep.Dropped)
	return strings.TrimRight(b.String(), "\n")
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
