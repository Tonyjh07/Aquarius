//go:build tier1sample

package main

type demoFactory struct{}

func (demoFactory) Name() string { return "demo" }

// externalFactories 注入进宿主的那份实现：带互斥约束，在插件仓库自己的构建里不参与。
func externalFactories() []Factory { return []Factory{demoFactory{}} }
