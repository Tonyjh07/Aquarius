package port

import (
	"context"
	"time"
)

// FileUploader 文档交给服务端解析的上传口（D315，DeepSeek Files API）。
//
// 消费方 = app.Session（结构化文档摄取时上传、把 file_id 写进 doc 分片）。与回收站同一姿态：
// 能力面为单一端点家族服务，不进 Ingestor 的方法面、也不做任何通用化抽象——DeepSeek 的
// purpose 取值就是它的契约，端口照实收发。
//
// 不做重试：服务端解析按页计费，重试等于替用户烧钱；失败由调用方明确报错（D315 决策 6）。
type FileUploader interface {
	// Upload 上传一份文件并等服务端解析完成（file_id 即刻可用）。
	//
	// name/mime/data 来自附件库的原文；purpose 是端点认的取值（配置 `files.purpose`）；
	// ttl ≤ 0 = 不传 expires_after（服务端永久保留），正数 = 服务端到点自动删除。
	// 超限（100 MiB 文档 / 64 MiB 图片）、上传超时（120s）与失败一律返回错误。
	Upload(ctx context.Context, name, mime string, data []byte, purpose string, ttl time.Duration) (fileID string, err error)
}
