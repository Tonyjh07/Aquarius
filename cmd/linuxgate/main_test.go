package main

import "testing"

// TestWSLPath Windows 盘符路径 → WSL 挂载路径（工具唯一有分支的纯逻辑）。
func TestWSLPath(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{`E:\Aquarius`, "/mnt/e/Aquarius", false},
		{`E:\`, "/mnt/e", false},
		{`c:\Users\yjh07\go\pkg\mod`, "/mnt/c/Users/yjh07/go/pkg/mod", false},
		{`E:\Aquarius\`, "/mnt/e/Aquarius", false}, // Clean 去尾分隔
		{`E:/Aquarius`, "/mnt/e/Aquarius", false},  // 正斜杠同解
		{`\\server\share\repo`, "", true},          // UNC 不在 WSL 挂载内
		{`relative\path`, "", true},                // 非盘符路径
		{``, "", true},
	}
	for _, c := range cases {
		got, err := wslPath(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("wslPath(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("wslPath(%q) 出错: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("wslPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestShellQuote 路径含空格/中文/单引号时的转义（WSL 内的 cd 参数）。
func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/mnt/e/Aquarius", "'/mnt/e/Aquarius'"},
		{"/mnt/e/My Repo", "'/mnt/e/My Repo'"},
		{"/mnt/e/it's", `'/mnt/e/it'\''s'`},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestIsWSLNoise 只滤 wsl.exe 的 localhost 代理告警，不误伤构建输出。
// 告警在管道里是 UTF-16 字节（字符间夹 NUL），中文平台下还混入非 UTF-8 字节。
func TestIsWSLNoise(t *testing.T) {
	utf16ish := "w\x00s\x00l\x00:\x00 \x00l\x00o\x00c\x00a\x00l\x00h\x00o\x00s\x00t\x00\n\x00"
	cases := []struct {
		in   string
		want bool
	}{
		{"wsl: 检测到 localhost 代理配置，但未镜像到 WSL。NAT 模式下的 WSL 不支持 localhost 代理。", true},
		{utf16ish, true},
		{"", false},
		{"internal/adapter/uigui/dock.go:297:9: undefined: windowFromPoint", false},
		{"# gioui.org/internal/vk", false},
		{"ok  \tgithub.com/Tonyjh07/Aquarius/internal/adapter/uigui", false},
	}
	for _, c := range cases {
		if got := isWSLNoise(c.in); got != c.want {
			t.Errorf("isWSLNoise(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
