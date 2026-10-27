package port

import "testing"

// TestExchangeFormats 格式表自检（D242）：名字唯一、扩展名带点、有损格式必须写明丢什么、
// 只认不写那一档不能同时是无损。
//
// 这张表同时是命令面 `--format` 的取值来源与导入失败时的提示文案，所以「有损却没说丢什么」
// 属于表写错，不是文案问题——用户会以为导出的就是全部。
func TestExchangeFormats(t *testing.T) {
	formats := ExchangeFormats()
	if len(formats) < 4 {
		t.Fatalf("格式表只有 %d 条，四种格式至少要能选出 aqx/chatgpt/openai/sillytavern", len(formats))
	}
	names := map[string]bool{}
	for _, f := range formats {
		if f.Name == "" || f.Label == "" {
			t.Fatalf("格式缺名字或标签: %+v", f)
		}
		if names[f.Name] {
			t.Fatalf("格式名重复: %s", f.Name)
		}
		names[f.Name] = true
		if f.Ext == "" || f.Ext[0] != '.' {
			t.Fatalf("格式 %s 的扩展名要带点: %q", f.Name, f.Ext)
		}
		if f.Lossy && f.LossNote == "" {
			t.Fatalf("有损格式 %s 必须写明丢什么", f.Name)
		}
		if f.NoExport && !f.Lossy {
			t.Fatalf("只读格式 %s 不该是无损（无损意味着能原样回来）", f.Name)
		}
	}
	for _, want := range []string{"aqx", "chatgpt", "openai", "sillytavern", "openwebui"} {
		if !names[want] {
			t.Fatalf("格式表缺 %s", want)
		}
	}
	// 无损的那一档必须是 aqx：命令面缺省导出格式按名字选它（「可原样回来」是缺省的承诺）。
	for _, f := range formats {
		if f.Name == "aqx" && (f.Lossy || f.NoExport) {
			t.Fatalf("aqx 应是无损且可导出的: %+v", f)
		}
	}
}
