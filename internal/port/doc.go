// Package port 定义内核与外部世界之间的全部端口：只有接口与 DTO，没有任何实现。
//
// 依赖方向：adapter → port ← app → domain（DESIGN §5）。
// port 是内核内部的缝，可随内核自由重构；与对外稳定的 pluginapi/v1 是两个独立稳定级、
// 类型互不引用，两侧由 adapter/plugingo 与 adapter/mcpgate 做防腐转换。
// 与领域同型的值对象（conversation.Usage、conversation.BlobRef、tool.Call 等）直接引用
// domain 类型，不在此重复定义（D17）。
package port
