//go:build !tier1sample

package main

import v1 "github.com/Tonyjh07/Aquarius/pluginapi"

// externalFactories 是外部插件进入二进制的唯一接入口；默认构建没有任何外部插件。
//
// Tier-1 插件在编译期就连进二进制（Go 没有可用的运行期代码加载），所以「装一个插件」就是
// 「拿一份带它的工厂列表重新构建」。构建器（cmd/aqbuild）用 -overlay 在编译单元里把这个
// 文件换成插件带来的一份同名实现：本仓库一个字节不落、go.mod 不动，插件作者也不必 fork
// 上游。换上去的那份带 `tier1sample` 构建约束，于是本文件自动退出编译，两份实现不会同时在场。
//
// 默认构建必须返回空：这是「发布产物不含任何第三方插件」的保证。往这里硬写一个工厂，等于
// 把它悄悄塞进所有人的二进制——pluginext_test.go 守着这一条。
func externalFactories() []v1.Factory { return nil }
