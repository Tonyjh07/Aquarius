package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestFamilyFromHost host 猜家族：后缀匹配必须带前导点。
func TestFamilyFromHost(t *testing.T) {
	cases := []struct{ base, want, why string }{
		{"https://api.deepseek.com/v1", "deepseek", "官方端点"},
		{"https://api.deepseek.com", "deepseek", "无路径"},
		{"api.deepseek.com/v1", "deepseek", "少 scheme 也认"},
		{"https://API.DeepSeek.COM/v1", "deepseek", "host 大小写不敏感"},
		{"https://notdeepseek.com/v1", "", "裸后缀会误判成同一家"},
		{"https://api.deepseek.com.evil.com/v1", "", "带点后缀才挡得住"},
		{"https://gw.mycorp.com/v1", "", "网关猜不出来"},
		{"", "", "空 base_url"},
		{"://bad", "", "解析不了就落空"},
	}
	for _, c := range cases {
		if got := familyFromHost(c.base); got != c.want {
			t.Fatalf("familyFromHost(%q) = %q, want %q（%s）", c.base, got, c.want, c.why)
		}
	}
}

// TestEndpointFamilyExplicitWins 显式 family 优先于 host；都没有才落空。
func TestEndpointFamilyExplicitWins(t *testing.T) {
	gw := providerConfig{BaseURL: "https://gw.mycorp.com/v1", Family: " DeepSeek "}
	if got := gw.endpointFamily(); got != familyDeepSeek {
		t.Fatalf("endpointFamily = %q, want %q（显式声明不该被 host 推翻）", got, familyDeepSeek)
	}
	official := providerConfig{BaseURL: "https://api.deepseek.com/v1"}
	if got := official.endpointFamily(); got != familyDeepSeek {
		t.Fatalf("endpointFamily = %q, want %q（键缺失按 host 猜）", got, familyDeepSeek)
	}
	none := providerConfig{BaseURL: "https://gw.mycorp.com/v1"}
	if got := none.endpointFamily(); got != "" {
		t.Fatalf("endpointFamily = %q, want 空", got)
	}
}

// TestServerCapsResolution 三层（显式 / 家族缺省 / 自动记录）的合成结果。
func TestServerCapsResolution(t *testing.T) {
	all := port.ServerCaps{Title: true, ReadURL: true, Files: true, WebSearch: true, Safety: true}
	list := func(names ...string) *[]string { return &names }
	cases := []struct {
		name string
		p    providerConfig
		want port.ServerCaps
	}{
		{"官方端点键缺失 = 全家桶", providerConfig{BaseURL: "https://api.deepseek.com/v1"}, all},
		{"网关键缺失 = 一项都不认", providerConfig{BaseURL: "https://gw.mycorp.com/v1"}, port.ServerCaps{}},
		{"网关 + 显式家族 = 全家桶", providerConfig{BaseURL: "https://gw.mycorp.com/v1", Family: "deepseek"}, all},
		{"显式空数组 = 一项都不认", providerConfig{BaseURL: "https://api.deepseek.com/v1", ServerCaps: list()}, port.ServerCaps{}},
		{
			"显式子集覆盖家族缺省",
			providerConfig{BaseURL: "https://api.deepseek.com/v1", ServerCaps: list("title", "files")},
			port.ServerCaps{Title: true, Files: true},
		},
		{
			"自动记录减掉不认项",
			providerConfig{BaseURL: "https://api.deepseek.com/v1", UnsupportedServerCaps: []string{"title", "bogus"}},
			port.ServerCaps{ReadURL: true, Files: true, WebSearch: true, Safety: true},
		},
	}
	for _, c := range cases {
		if got := c.p.serverCaps(); got != c.want {
			t.Fatalf("%s: serverCaps = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// serverCapTestConfig 一份能过 normalize 的最小 model 段。
func serverCapTestConfig(p providerConfig) *modelConfig {
	if p.Name == "" {
		p.Name = "p"
	}
	if p.BaseURL == "" {
		p.BaseURL = "https://api.deepseek.com/v1"
	}
	if len(p.Models) == 0 {
		p.Models = []modelEntry{{Name: "m"}}
	}
	return &modelConfig{Providers: []providerConfig{p}, Name: "m"}
}

// TestNormalizeValidatesServerCaps 词表外的家族名与能力名都启动即报。
func TestNormalizeValidatesServerCaps(t *testing.T) {
	bad := []struct {
		name string
		p    providerConfig
		want string
	}{
		{"家族拼错", providerConfig{Family: "deepseak"}, "family"},
		{"能力名拼错", providerConfig{ServerCaps: capList("titles")}, "server_caps"},
		{"记录里能力名拼错", providerConfig{UnsupportedServerCaps: []string{"nope"}}, "unsupported_server_caps"},
	}
	for _, c := range bad {
		err := serverCapTestConfig(c.p).normalize()
		if err == nil {
			t.Fatalf("%s: 应报错而不是静默关掉能力", c.name)
		}
		if c.want == "family" {
			if !strings.Contains(err.Error(), "deepseak") {
				t.Fatalf("%s: 报错应点名写错的家族：%v", c.name, err)
			}
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: 报错应点名字段 %s：%v", c.name, c.want, err)
		}
	}

	ok := serverCapTestConfig(providerConfig{
		Family:                " DEEPSEEK ",
		ServerCaps:            capList("Title", " FILES "),
		UnsupportedServerCaps: []string{"web_search"},
	})
	if err := ok.normalize(); err != nil {
		t.Fatalf("合法声明不该报错：%v", err)
	}
	if got := ok.Providers[0].endpointFamily(); got != familyDeepSeek {
		t.Fatalf("family 未归一为小写：%q", got)
	}
}

// TestNoteServerCapPersists 模拟一次 400：写回配置，之后解析出的能力里不再有它。
func TestNoteServerCapPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"model":{"providers":[{"name":"deepseek","kind":"openai-chat",` +
		`"base_url":"https://api.deepseek.com/v1","keys":[],"models":[{"name":"m"}]}],` +
		`"name":"m"}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("写配置: %v", err)
	}
	w, _ := testWiringLog(t)
	w.cfgPath = path

	w.noteServerCapFor("deepseek")("title")
	w.noteServerCapFor("deepseek")("title") // 幂等：同一项只记一次
	w.noteServerCapFor("nope")("files")     // 不存在的 provider：不炸、不建条目

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("读回配置: %v", err)
	}
	p := cfg.Model.byName("deepseek")
	if p == nil {
		t.Fatal("provider 丢了")
	}
	if got := p.UnsupportedServerCaps; len(got) != 1 || got[0] != "title" {
		t.Fatalf("unsupported_server_caps = %v, want [title]", got)
	}
	if p.serverCaps().Has(port.ServerCapTitle) {
		t.Fatal("记录过的能力仍会被发出去")
	}
	if !p.serverCaps().Has(port.ServerCapFiles) {
		t.Fatal("未记录的其它能力应保持可用")
	}

	// 写回的形态：数组里的字符串，不是嵌套对象（手工改配置时最不容易写错的那种）。
	var doc struct {
		Model struct {
			Providers []map[string]json.RawMessage `json:"providers"`
		} `json:"model"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件: %v", err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("解析写回: %v", err)
	}
	var list []string
	if err := json.Unmarshal(doc.Model.Providers[0]["unsupported_server_caps"], &list); err != nil {
		t.Fatalf("unsupported_server_caps 应是字符串数组：%v", err)
	}
}

// capList 造一个 server_caps 的指针声明（空调用 = 显式空数组）。
func capList(names ...string) *[]string {
	out := make([]string, len(names))
	copy(out, names)
	return &out
}

// TestChainServerCaps 降级链取交（D173 的 chainCaps 同规则）：一环不认就不发。
func TestChainServerCaps(t *testing.T) {
	both := modelConfig{
		Providers: []providerConfig{
			{Name: "a", BaseURL: "https://api.deepseek.com/v1"},
			{Name: "b", BaseURL: "https://api.deepseek.com/v1"},
		},
		Primary:  "a",
		Fallback: []fallbackEntry{{Provider: "b"}},
	}
	if caps := both.chainServerCaps(); !caps.Has(port.ServerCapTitle) {
		t.Fatal("两家都认时应保留 title")
	}

	mixed := both
	mixed.Providers = []providerConfig{
		{Name: "a", BaseURL: "https://api.deepseek.com/v1"},
		{Name: "b", BaseURL: "https://gw.mycorp.com/v1"}, // 猜不出家族 = 一项都不认
	}
	if caps := mixed.chainServerCaps(); caps.Has(port.ServerCapTitle) {
		t.Fatal("链上有一家不认时不该发")
	}

	// 空态：没有 provider 时一项都不假设。
	if caps := (&modelConfig{}).chainServerCaps(); caps != (port.ServerCaps{}) {
		t.Fatalf("空态应为空集，实际 %+v", caps)
	}
}

// TestTitleEnabledDefault 标题开关三态（D309）：键缺失 = 开，显式 false 才关。
func TestTitleEnabledDefault(t *testing.T) {
	var missing fileConfig
	if !missing.titleEnabled() {
		t.Fatal("键缺失应视为开")
	}
	on, off := true, false
	if !(&fileConfig{Title: titleConfig{Enabled: &on}}).titleEnabled() {
		t.Fatal("显式 true 应为开")
	}
	if (&fileConfig{Title: titleConfig{Enabled: &off}}).titleEnabled() {
		t.Fatal("显式 false 应为关")
	}
}

// TestReadURLEnabledDefault 服务端读链接开关（D310）：键缺失 = 开，显式 false 才关。
func TestReadURLEnabledDefault(t *testing.T) {
	var missing fileConfig
	if !missing.readURLEnabled() {
		t.Fatal("键缺失应视为开")
	}
	off := false
	if (&fileConfig{Web: webConfig{ReadURL: readURLConfig{Enabled: &off}}}).readURLEnabled() {
		t.Fatal("显式 false 应为关")
	}
}
