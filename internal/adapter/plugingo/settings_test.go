package plugingo

import (
	"context"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// fakeSettings 只验转换与转发：记住最后一次提交，声明两张分区。
type fakeSettings struct {
	gotSection string
	gotValues  map[string]string
}

func (f *fakeSettings) Sections() []v1.SettingsSection {
	return []v1.SettingsSection{
		{
			ID: "recall", Title: "回忆", Desc: "决定往上下文里放多少",
			Rows: []v1.SettingsRow{
				{Key: "budget", Label: "预算", Kind: v1.FieldNumber, Value: "600", Help: "token 数"},
				{Key: "mode", Label: "方式", Kind: v1.FieldSelect, Value: "a", Values: []string{"a", "b"}},
				{Key: "weird", Label: "未知控件", Kind: v1.FieldKind("slider"), Value: "3"},
			},
		},
		{ID: "quiet", Title: "安静"},
	}
}

func (f *fakeSettings) ApplySettings(_ context.Context, patch v1.SettingsPatch) error {
	f.gotSection, f.gotValues = patch.SectionID, patch.Values
	return nil
}

func TestSettingsSectionsConvert(t *testing.T) {
	s := NewSettings("memory", &fakeSettings{})
	secs := s.Sections()
	if len(secs) != 2 {
		t.Fatalf("分区数 = %d, want 2", len(secs))
	}
	if secs[0].Plugin != "memory" || secs[0].ID != "recall" || secs[0].Title != "回忆" {
		t.Fatalf("分区没带上插件名：%+v", secs[0])
	}
	rows := secs[0].Rows
	if len(rows) != 3 {
		t.Fatalf("行数 = %d, want 3", len(rows))
	}
	if rows[0].Kind != port.PluginFieldNumber || rows[0].Value != "600" || rows[0].Help != "token 数" {
		t.Fatalf("数字行转换不对：%+v", rows[0])
	}
	if rows[1].Kind != port.PluginFieldSelect || len(rows[1].Values) != 2 {
		t.Fatalf("下拉行转换不对：%+v", rows[1])
	}
	// 未知控件种类回落到文本行：行还在、值还能看见，只是编辑方式降一档。
	if rows[2].Kind != port.PluginFieldText {
		t.Fatalf("未知种类应回落 text，得到 %q", rows[2].Kind)
	}
	if len(secs[1].Rows) != 0 {
		t.Fatalf("空分区不该凭空长出行：%+v", secs[1].Rows)
	}
}

func TestSettingsApplyPassesSectionAndValues(t *testing.T) {
	p := &fakeSettings{}
	s := NewSettings("memory", p)
	vals := map[string]string{"budget": "800", "mode": "b"}
	if err := s.Apply(context.Background(), "recall", vals); err != nil {
		t.Fatal(err)
	}
	if p.gotSection != "recall" {
		t.Fatalf("分区号 = %q, want recall", p.gotSection)
	}
	if len(p.gotValues) != 2 || p.gotValues["budget"] != "800" || p.gotValues["mode"] != "b" {
		t.Fatalf("值没原样传下去：%+v", p.gotValues)
	}
}
