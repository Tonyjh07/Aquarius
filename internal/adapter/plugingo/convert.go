// Package plugingo 是 Tier-1 插件的防腐层（plugin-design.md §5）：v1 契约与内核端口
// 之间的双向转换，加上把内核服务包成 v1 Host 的实现。
//
// 三条纪律：
//
//  1. 逐字段手写。不反射（字段名一改编译还过、运行时静默丢值）、不 JSON round-trip
//     （丢类型，也给每帧增量塞一次编码成本）。
//  2. 每个转换函数一个方向、一个名字。`toV1X` 是内核 → 插件，`toPortX` 是插件 → 内核；
//     不写「就地双向」的合并函数，免得读的人要判断当前在哪个方向。
//  3. 转换点以 docs/plugin-design.md §1.2 的映射表为准。port 侧改字段时先改表、再改这里；
//     表与代码不一致就是漏了某一侧。
//
// 插件交回的错误不分类：v1 没有错误词表，内核按「不可重试」处理（保守方向）。要表达
// 瞬时错误得先在 v1 加哨兵，那是新能力，不是转换问题。
package plugingo

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// toV1Risk 风险档：tool.Risk 是 iota 枚举，其整数值不是契约；v1 只认两个词。
func toV1Risk(r tool.Risk) v1.Risk {
	if r == tool.Confirm {
		return v1.RiskConfirm
	}
	return v1.RiskSafe
}

// toPortRisk 反向。未知取值按 confirm——插件写错一个词时宁可多问一次，不要默认放行。
func toPortRisk(r v1.Risk) tool.Risk {
	if r == v1.RiskSafe {
		return tool.Safe
	}
	return tool.Confirm
}

// toPortSession 把一个 v1 入口的 ctx 翻成内核 ctx：会话身份从 v1 的键搬到 port 的键。
// 每个插件入口适配函数都必须调它一次（D338）——漏了不会报错，只会让插件悄悄退回全局。
func toPortSession(ctx context.Context) context.Context {
	id, ok := v1.SessionFrom(ctx)
	if !ok || id == "" {
		return ctx
	}
	return port.WithSessionID(ctx, conversation.ID(id))
}

// toV1BlobRef 附件引用：结构体原样重述，不是字符串别名（§3.1）。
func toV1BlobRef(r *conversation.BlobRef) *v1.BlobRef {
	if r == nil {
		return nil
	}
	return &v1.BlobRef{Hash: r.Hash, MIME: r.MIME, Name: r.Name, Size: r.Size}
}

// toPortBlobRef 反向。
func toPortBlobRef(r *v1.BlobRef) *conversation.BlobRef {
	if r == nil {
		return nil
	}
	return &conversation.BlobRef{Hash: r.Hash, MIME: r.MIME, Name: r.Name, Size: r.Size}
}
