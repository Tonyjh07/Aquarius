package acpgate

// 连通性探测（A1「测试连接」）：握手回执的字段与三类失败的分类。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestProbeHandshake 一次成功探测：协议版本、agentInfo、登录方式全来自握手回执。
func TestProbeHandshake(t *testing.T) {
	t.Parallel()
	m := newManager(t, "ok", func(o *Options) { o.Home = t.TempDir() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := m.Probe(ctx, "fake")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if info.ProtocolVersion != 1 {
		t.Fatalf("protocol = %d", info.ProtocolVersion)
	}
	if info.Title != "Fake Agent" || info.Version != "9.9" {
		t.Fatalf("agentInfo = %q %q", info.Title, info.Version)
	}
	// 已登录的代理也照报 authMethods（gemini-cli 这类）：探测只转述，不据此判「没登录」。
	if len(info.AuthMethods) != 1 || info.AuthMethods[0] != "Log in first" {
		t.Fatalf("authMethods = %v", info.AuthMethods)
	}
	// 握手学到的标题版本要回填清单（设置窗第二行直接有东西可显示）。
	list, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Title != "Fake Agent" {
		t.Fatalf("清单里的标题 = %+v", list)
	}
}

// TestProbeFailureClasses 三类失败分开报：找不到命令 / 起不来 / 握手失败（带 stderr 尾巴）。
func TestProbeFailureClasses(t *testing.T) {
	t.Parallel()
	m := newManager(t, "crash", func(o *Options) { o.Home = t.TempDir() })

	_, err := m.Probe(context.Background(), "ghost")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("未知名字应报错: %v", err)
	}

	missing := newManager(t, "ok", func(o *Options) {
		o.Agents[0].Command = "definitely-not-on-path-8c1f"
	})
	if _, err := missing.Probe(context.Background(), "fake"); !errors.Is(err, ErrProbeNotFound) {
		t.Fatalf("找不到命令应归 ErrProbeNotFound: %v", err)
	}

	// crash 模式：handshake 里对方直接退出，报因要带上它自己的 stderr 尾巴。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = m.Probe(ctx, "fake")
	if !errors.Is(err, ErrProbeHandshake) {
		t.Fatalf("应归 ErrProbeHandshake: %v", err)
	}
	if !strings.Contains(err.Error(), "boom on purpose") {
		t.Fatalf("报因应带对方 stderr 尾巴: %v", err)
	}
}

// TestProbeAllowsDisabled 停用的代理也允许探测：它是「这条配置跑不跑得通」的诊断，
// 不是「能不能派活」的承诺（pick 才会拒绝停用）。
func TestProbeAllowsDisabled(t *testing.T) {
	t.Parallel()
	m := newManager(t, "ok", func(o *Options) {
		o.Home = t.TempDir()
		o.Agents[0].Enabled = false
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := m.Probe(ctx, "fake"); err != nil {
		t.Fatalf("停用代理也应能探测: %v", err)
	}
}
