package main

// Factory 微型宿主的最小工厂面：只验注入机制，不验真实的插件契约。
// 放在无构建约束的文件里——被替换的 pluginext.go 不参与编译时，它仍然要在。
type Factory interface{ Name() string }
