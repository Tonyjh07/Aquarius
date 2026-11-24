package plugingo

import (
	"bytes"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// convert_io.go 摄取与输出两个面的转换（plugin-design.md §3.1）：输入侧把原始输入交给
// 插件，输出侧把已提交消息的正文副本交给插件。
//
// 分片按**正文投影**重述：`Kind=tool` 不进 v1——工具往返是内核协作面，对外播报与摄取
// 只用正文。丢工具分片不是精简，是让 v1 的 Part 只有一种含义。

// toV1RawInput 原始输入 → 插件看到的输入。File / Blob 简写原样保留（等价关系由
// AttachmentList 定义，转换层不替它展开：展开会让插件的回显与用户的输入对不上）。
func toV1RawInput(r port.RawInput) v1.RawInput {
	out := v1.RawInput{
		Kind:    r.Kind,
		Text:    r.Text,
		File:    r.File,
		Blob:    toV1BlobRef(r.Blob),
		Seconds: r.Seconds,
	}
	if len(r.Attachments) > 0 {
		out.Attachments = make([]v1.Attachment, 0, len(r.Attachments))
		for _, a := range r.Attachments {
			out.Attachments = append(out.Attachments, v1.Attachment{
				Path: a.Path,
				Blob: toV1BlobRef(a.Blob),
			})
		}
	}
	return out
}

// toPortIngestReport 插件的摄取结果 → 内核结果。插件交上来的分片种类不受限，
// tool 一类在这里被丢弃（v1 没给这个入口，出现了就是插件在造内核不认识的东西）。
func toPortIngestReport(rep v1.IngestReport) port.IngestReport {
	out := port.IngestReport{Note: rep.Note}
	out.Parts = make([]conversation.Part, 0, len(rep.Parts))
	for _, p := range rep.Parts {
		if part, ok := toPortPart(p); ok {
			out.Parts = append(out.Parts, part)
		}
	}
	return out
}

// toV1OutputRequest 已提交消息 → 输出请求。Parts 可能含思考分片，适配器自行筛选需要的
// 种类（对外播报正文的语义在插件侧）。
func toV1OutputRequest(req port.OutputRequest) v1.OutputRequest {
	out := v1.OutputRequest{
		MessageID: string(req.Message.ID),
		Role:      string(req.Message.Role),
	}
	out.Parts = make([]v1.Part, 0, len(req.Parts))
	for _, p := range req.Parts {
		if vp, ok := toV1Part(p); ok {
			out.Parts = append(out.Parts, vp)
		}
	}
	return out
}

// toV1Part 一个正文分片 → v1 的分片。工具分片返回 ok=false（不进 v1）。
func toV1Part(p conversation.Part) (v1.Part, bool) {
	switch p.Kind {
	case conversation.PartTool:
		return v1.Part{}, false
	case conversation.PartKind(""):
		return v1.Part{}, false
	}
	out := v1.Part{
		Kind:       v1.PartKind(p.Kind),
		Text:       p.Text,
		Ref:        toV1BlobRef(p.Ref),
		Transcript: p.Transcript,
		Signature:  bytes.Clone([]byte(p.Signature)),
	}
	if p.Server != nil {
		out.ServerRaw = bytes.Clone(p.Server.Raw)
	}
	return out, true
}

// toPortPart 反向：插件产出的分片回到树里。
//
// 服务端块只回填原文：v1 的 Part 只承诺 Raw 的保真，Tool / Kind / URL / Title 那几个
// 展示字段没有对位。摄取插件本来就不该造服务端块，这条路径实际只在「插件原样搬运」
// 时用得上，那时展示字段缺了也不影响回传。
func toPortPart(p v1.Part) (conversation.Part, bool) {
	if p.Kind == "" {
		return conversation.Part{}, false
	}
	out := conversation.Part{
		Kind:       conversation.PartKind(p.Kind),
		Text:       p.Text,
		Ref:        toPortBlobRef(p.Ref),
		Transcript: p.Transcript,
		Signature:  string(p.Signature),
	}
	if len(p.ServerRaw) > 0 {
		out.Server = &conversation.ServerPart{Raw: bytes.Clone(p.ServerRaw)}
	}
	return out, true
}
