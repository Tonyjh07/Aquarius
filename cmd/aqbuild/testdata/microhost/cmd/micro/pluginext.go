//go:build !tier1sample

package main

// externalFactories 宿主的中性接入口：默认构建里没有任何外部插件。
func externalFactories() []Factory { return nil }
