// Package arch 只承载架构守卫测试，不提供生产代码。
//
// 目录里必须有一个非测试 Go 文件：若只有 `_test.go`，`go build ./...` 会报
// 「no non-test Go files」并直接挂掉门禁。
package arch
