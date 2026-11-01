package uicommon

// keys_test.go 输入栏三键的写法（§5.19 / ui.keys）：解析、撞键、缺省。

import (
	"strings"
	"testing"
)

// TestParseBindingsDefaults 三个值都给空 = 缺省 Enter / Shift+Enter / Ctrl+Enter。
func TestParseBindingsDefaults(t *testing.T) {
	k, err := ParseKeyBindings("", "", "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := k.Send.String(); got != "Enter" {
		t.Fatalf("send = %q", got)
	}
	if got := k.Steer.String(); got != "Shift+Enter" {
		t.Fatalf("steer = %q", got)
	}
	if got := k.Newline.String(); got != "Ctrl+Enter" {
		t.Fatalf("newline = %q", got)
	}
	if k.Steer.Mods != ModShift || k.Newline.Mods != ModCtrl {
		t.Fatalf("修饰键位不对: %+v %+v", k.Steer, k.Newline)
	}
}

// TestParseBindingForms 修饰键组合、键名别名、单字符键都要能认。
func TestParseBindingForms(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Ctrl+Enter", "Ctrl+Enter"},
		{"ctrl + shift + enter", "Ctrl+Shift+Enter"},
		{"Alt+J", "Alt+J"},
		{"Meta+x", "Super+X"},
		{"esc", "Escape"},
		{"space", "Space"},
		{"tab", "Tab"},
		{"backspace", "Backspace"},
	} {
		b, err := ParseBinding(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got := b.String(); got != tc.want {
			t.Fatalf("%q → %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestParseBindingErrors 空、只有修饰键、坏修饰键、不认识的键名都要报错（启动即报因）。
func TestParseBindingErrors(t *testing.T) {
	for _, in := range []string{"", "  ", "Ctrl+", "Ctrl+Shift", "Hyper+Enter", "Ctrl+EnterX", "Ctrl++Enter"} {
		if _, err := ParseBinding(in); err == nil {
			t.Fatalf("%q 应报错", in)
		}
	}
}

// TestParseBindingsCollision 三键撞键 = 启动报错（同一个组合一个语义都定不下来）。
func TestParseBindingsCollision(t *testing.T) {
	_, err := ParseKeyBindings("Enter", "Enter", "Ctrl+Enter")
	if err == nil || !strings.Contains(err.Error(), "撞键") {
		t.Fatalf("err = %v, want 撞键", err)
	}
	if _, err := ParseKeyBindings("Enter", "Ctrl+Enter", "Ctrl+Enter"); err == nil {
		t.Fatal("steer 与 newline 相同应报错")
	}
}

// TestValidateEmptyBinding 手工搭的零值表也要被挡住（Validate 不只服务解析路径）。
func TestValidateEmptyBinding(t *testing.T) {
	k := DefaultKeyBindings()
	k.Steer = Binding{}
	if err := k.Validate(); err == nil {
		t.Fatal("缺键应报错")
	}
}
