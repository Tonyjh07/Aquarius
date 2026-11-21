package app

// session_files.go 文档交给服务端解析（D315，DeepSeek Files API）：摄取产物里的结构化文档
// 在此上传、file_id 写回 doc 分片。门控三道闸——装配时接了 FilesUploader（`files.purpose`
// 非空）、附件库可用、当前降级链都认 `files` 能力位；任一不满足就原样返回（走本地提取）。
//
// 失败**不静默**也不重试：解析按页计费，重试等于替用户烧钱；失败原因按 D186 的部分成功规则
// 进 Note 随消息回执给用户，该分片保留本地提取文本（降级回落）。

import (
	"context"
	"io"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// fileServerMIMES 上传候选的 MIME 集合（D315 否决 1）：本地走「结构化文档解析」的文档类，
// 且在服务端 purpose 支持表内。纯文本、表格本地提得干净，图片走 vision，都不上传。
var fileServerMIMES = map[string]bool{
	"application/pdf":    true,
	"application/msword": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   true,
	"application/vnd.ms-powerpoint":                                             true,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true,
}

// uploadDocs 对这批分片里的结构化文档做服务端解析（D315）。返回追加的 Note（可能有多个
// 文件失败，逐文件一句）；没有可上传的或门控未开时返回空串。
func (s *Session) uploadDocs(ctx context.Context, parts []conversation.Part) string {
	if s.files == nil || s.filesPurpose == "" || s.blobs == nil ||
		!s.agent.cfg.ServerCaps.Files {
		return ""
	}
	var notes []string
	for i := range parts {
		p := &parts[i]
		if p.Kind != conversation.PartDoc || p.Ref == nil ||
			!fileServerMIMES[p.Ref.MIME] {
			continue
		}
		rc, err := s.blobs.Get(ctx, *p.Ref)
		if err != nil {
			notes = append(notes, i18n.Tf("files.note.read_failed", "name", p.Ref.Name,
				"err", err.Error()))
			continue
		}
		data, rerr := io.ReadAll(rc)
		_ = rc.Close()
		if rerr != nil {
			notes = append(notes, i18n.Tf("files.note.read_failed", "name", p.Ref.Name,
				"err", rerr.Error()))
			continue
		}
		id, err := s.files.Upload(ctx, p.Ref.Name, p.Ref.MIME, data, s.filesPurpose, s.filesTTL)
		if err != nil {
			// 明确报因（D315 决策 6），分片保留本地提取文本——降级回落但不说谎。
			notes = append(notes, i18n.Tf("files.note.upload_failed", "name", p.Ref.Name,
				"err", err.Error()))
			continue
		}
		p.FileID = id
	}
	if len(notes) == 0 {
		return ""
	}
	return strings.Join(notes, i18n.T("ingest.join"))
}

// filePurposeSupported 用途档取值校验（configuration.md 的词表；越界启动报错）。
func filePurposeSupported(v string) bool {
	switch v {
	case "", "text_recognition", "rich_layout_recognition":
		return true
	}
	return false
}
