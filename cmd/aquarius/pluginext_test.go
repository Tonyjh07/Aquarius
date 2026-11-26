package main

import "testing"

// TestNoExternalPluginInDefaultBuild 发布产物里不能有任何外部插件。
//
// 这条断言看起来同义反复（函数体就是 return nil），它的用处在于**谁把硬编码的工厂写回
// pluginext.go 就会红**：默认构建多出一个插件，等于把它塞进所有人的二进制，而这件事
// 不该靠 review 记住。带插件的构建走 -overlay 换掉整个文件，不经过这里。
func TestNoExternalPluginInDefaultBuild(t *testing.T) {
	if fs := externalFactories(); len(fs) != 0 {
		t.Fatalf("默认构建里出现了 %d 个外部插件工厂；接入只能经 -overlay 注入", len(fs))
	}
}
