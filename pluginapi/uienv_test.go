package pluginapi

import (
	"reflect"
	"testing"
)

// uienv_test.go 环境只读面的形状约束（D352）。
//
// 这不是功能测试，是把注释里的三条承诺变成会红的断言：接口只有「问一次、拿一份值」这件事，
// 没有订阅、没有回调、没有句柄。哪天有人想往快照里塞一个 channel 或 func，接口的承诺就
// 从「只读描述」变成「宿主负责你的生命周期」，那时应该显式改决策，而不是顺手加个字段。

// TestUIEnvironIsReadOnly 接口只有一个无参方法，返回的快照里没有可执行/可监听的东西。
func TestUIEnvironIsReadOnly(t *testing.T) {
	it := reflect.TypeOf((*UIEnviron)(nil)).Elem()
	if it.NumMethod() != 1 || it.Method(0).Name != "Environ" {
		t.Fatalf("UIEnviron 只应有 Environ 一个方法：%v", it)
	}
	m := it.Method(0).Type
	if m.NumIn() != 0 {
		t.Fatalf("Environ 不该带参数：%v", m)
	}
	if m.NumOut() != 1 {
		t.Fatalf("Environ 只应返回一份快照：%v", m)
	}
	assertPlainSnapshot(t, m.Out(0), "UIEnvironment")
}

// assertPlainSnapshot 递归检查快照只由值类型组成（结构体/切片/基础类型）。
func assertPlainSnapshot(t *testing.T, typ reflect.Type, path string) {
	t.Helper()
	switch typ.Kind() {
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			assertPlainSnapshot(t, f.Type, path+"."+f.Name)
		}
	case reflect.Slice, reflect.Array:
		assertPlainSnapshot(t, typ.Elem(), path+"[]")
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		// 值类型：可以进快照
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Interface,
		reflect.Ptr, reflect.UnsafePointer:
		t.Errorf("%s: 只读快照不得含 %v（句柄/回调/事件面）", path, typ.Kind())
	default:
		t.Errorf("%s: 未登记的字段类型 %v", path, typ.Kind())
	}
}

// TestFrontendVocabularyOnlyThree 前端取值就三个：多一个值插件就多一条分支，
// 而多出来的那个前端宿主自己都还没实现。
func TestFrontendVocabularyOnlyThree(t *testing.T) {
	got := map[string]bool{FrontendGUI: true, FrontendTUI: true, FrontendREPL: true}
	if len(got) != 3 {
		t.Fatalf("前端词表 = %v", got)
	}
	// 空串是「未知」，不能与具体前端撞车。
	if got[""] {
		t.Fatal("空串不该是一个前端")
	}
}
