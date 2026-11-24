package pluginapi

import "context"

// ingest.go 摄取与输出两个面（plugin-design.md §4）：输入侧决定「这段原始输入变成什么」，
// 输出侧决定「已提交的消息副本镜像到哪」。

// Ingestor 输入摄取：OCR、音频直输这类降级链扩展点。多个摄取器按注册顺序询问，
// 第一个 Accepts 为真的接单。
// 兼容承诺：改方法集会破坏所有声明 provides: ingest 的插件与输入链的转换。
type Ingestor interface {
	// Accepts 判断这一份原始输入归不归我处理。必须是纯函数：宿主会拿它做路由。
	Accepts(raw RawInput) bool
	// Ingest 把原始输入变成可入消息的正文分片。返回错误 = 这次摄取失败，
	// 宿主按降级链继续问下一个摄取器。
	Ingest(ctx context.Context, raw RawInput) (IngestReport, error)
}

// OutputAdapter 输出镜像：每条已提交消息的副本送到某处（首个实现是导出文件输出器）。
// 多路输出各自独立，一个适配器失败不影响其他适配器与主流程。
// 兼容承诺：改方法集会破坏所有声明 provides: output 的插件与输出扇出的转换。
type OutputAdapter interface {
	// Name 输出来源名（展示与审计归组用），在实例内稳定。
	Name() string
	// Deliver 送出一条。Parts 可能含思考分片——本接口服务的都是「对外播报正文」，
	// 适配器自行筛选需要的种类。
	Deliver(ctx context.Context, req OutputRequest) error
}
