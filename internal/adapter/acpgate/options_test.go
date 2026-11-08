package acpgate

import (
	"context"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// cat 取 category 的地址（协议里这一项是可选的）。
func cat(c acp.SessionConfigOptionCategory) *acp.SessionConfigOptionCategory { return &c }

// ungrouped 造一个不分组候选列表。
func ungrouped(values ...string) acp.SessionConfigSelectOptions {
	list := make(acp.SessionConfigSelectOptionsUngrouped, 0, len(values))
	for _, v := range values {
		list = append(list, acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(v), Name: v})
	}
	return acp.SessionConfigSelectOptions{Ungrouped: &list}
}

// selectOption 造一个 select 形状的选项。
func selectOption(id string, category *acp.SessionConfigOptionCategory, current string,
	opts acp.SessionConfigSelectOptions) acp.SessionConfigOption {
	return acp.SessionConfigOption{Select: &acp.SessionConfigOptionSelect{
		Id: acp.SessionConfigId(id), Name: id, Category: category,
		CurrentValue: acp.SessionConfigValueId(current), Options: opts, Type: "select",
	}}
}

// TestOptionOrder 顺序固定：模型先行，依赖模型的 thought_level 次之，模式再次，
// 认不出的殿后（按 id 升序）。map 无序，不排就是随机失败。
func TestOptionOrder(t *testing.T) {
	snap := []acp.SessionConfigOption{
		selectOption("mode", cat(acp.SessionConfigOptionCategoryMode), "default", ungrouped("default")),
		selectOption("thinking", cat(acp.SessionConfigOptionCategoryThoughtLevel), "off", ungrouped("off")),
		selectOption("model", cat(acp.SessionConfigOptionCategoryModel), "a/1", ungrouped("a/1")),
	}
	want := map[string]string{"variant": "x", "mode": "plan", "thinking": "high", "model": "b/2"}
	got := strings.Join(orderOptionIDs(snap, want), ",")
	if got != "model,thinking,mode,variant" {
		t.Fatalf("顺序 = %s", got)
	}
}

// TestSelectValuesFlattens 候选有两种形状（不分组、分组），展平后才能查。
func TestSelectValuesFlattens(t *testing.T) {
	grouped := acp.SessionConfigSelectOptionsGrouped{
		{Group: "g1", Name: "One", Options: []acp.SessionConfigSelectOption{
			{Value: "plain", Name: "Plain"},
		}},
		{Group: "g2", Name: "Two", Options: []acp.SessionConfigSelectOption{
			{Value: "fancy", Name: "Fancy"},
		}},
	}
	opt := selectOption("variant", nil, "plain", acp.SessionConfigSelectOptions{Grouped: &grouped})
	got := strings.Join(selectValues(opt.Select), ",")
	if got != "plain,fancy" {
		t.Fatalf("展平 = %s", got)
	}
}

// TestApplyOptionValidation 三项在发请求之前就该被挡住（所以 conn 传 nil 是安全的：
// 校验不过就不会走到那一步）。
func TestApplyOptionValidation(t *testing.T) {
	snap := []acp.SessionConfigOption{
		selectOption("model", cat(acp.SessionConfigOptionCategoryModel), "alpha/1", ungrouped("alpha/1", "beta/2")),
		{Boolean: &acp.SessionConfigOptionBoolean{Id: "verbose", Name: "Verbose", Type: "boolean"}},
	}
	cases := []struct {
		name    string
		id, val string
		want    string
	}{
		{"没有这一项", "nope", "x", `has no session option "nope"`},
		{"开关形状", "verbose", "true", "only sets options that pick from a list"},
		{"候选里没有", "model", "gamma/3", `cannot be "gamma/3"`},
	}
	for _, c := range cases {
		_, err := applyOneOption(context.Background(), nil, "", snap, c.id, c.val)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s：err = %v", c.name, err)
		}
	}
	// 未知 id 的报错要列出该代理实际有什么，否则用户只能猜。
	if _, err := applyOneOption(context.Background(), nil, "", snap, "nope", "x"); err == nil ||
		!strings.Contains(err.Error(), `"model"`) {
		t.Fatalf("应列出可用选项：%v", err)
	}
	// 候选里没有时要把可填的值列出来。
	if _, err := applyOneOption(context.Background(), nil, "", snap, "model", "gamma/3"); err == nil ||
		!strings.Contains(err.Error(), `"beta/2"`) {
		t.Fatalf("应列出候选值：%v", err)
	}
	// 已经是这个值：不发请求、不算失败。
	next, err := applyOneOption(context.Background(), nil, "", snap, "model", "alpha/1")
	if err != nil {
		t.Fatalf("已生效的值不该报错：%v", err)
	}
	if len(next) != len(snap) {
		t.Fatalf("跳过的项不该改动快照")
	}
}

// TestSessionOptionsApplied 配置里写死的选项按固定顺序设上，报告里能读出顺序。
func TestSessionOptionsApplied(t *testing.T) {
	m := newManager(t, "options", func(o *Options) {
		o.Home = t.TempDir()
		o.Agents[0].Options = map[string]string{"mode": "plan", "thinking": "high", "model": "beta/model-2"}
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "x"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	// thinking=high 只有在模型切到 beta 之后才在候选里：顺序错了这一步会失败。
	if got := strings.TrimSpace(run.Report); got != "sets: model=beta/model-2,thinking=high,mode=plan" {
		t.Fatalf("设置顺序不对：%q", got)
	}
	if run.StopReason != "end_turn" {
		t.Fatalf("stopReason = %q", run.StopReason)
	}
}

// TestSessionOptionsSkippedWhenEqual 已经是该值就不发请求（省一次往返，
// 也避开同一值再设一次的副作用）。
func TestSessionOptionsSkippedWhenEqual(t *testing.T) {
	m := newManager(t, "options", func(o *Options) {
		o.Home = t.TempDir()
		o.Agents[0].Options = map[string]string{"model": "alpha/model-1"}
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "x"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got := strings.TrimSpace(run.Report); got != "sets:" {
		t.Fatalf("不该发出请求：%q", got)
	}
}

// TestSessionOptionsReadBack 代理接受了设置却回报旧值：必须判失败。
//
// opencode 就是这样——模型在候选里，设它回 model not found；反过来也可能有代理
// 「收下但不动」。只看请求成功就下结论，用户会以为在用自己选的模型。
func TestSessionOptionsReadBack(t *testing.T) {
	m := newManager(t, "options-echobad", func(o *Options) {
		o.Home = t.TempDir()
		o.Agents[0].Options = map[string]string{"model": "beta/model-2"}
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "x"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, err = m.Wait(context.Background(), task.ID)
	if err == nil || !strings.Contains(err.Error(), "asked to be") ||
		!strings.Contains(err.Error(), "alpha/model-1") {
		t.Fatalf("错误要说清请求值与代理实际回报的值：%v", err)
	}
}

// TestSessionOptionsAgentRefuses 代理拒绝时，把它自己的话带出去（转述只会更糊）。
func TestSessionOptionsAgentRefuses(t *testing.T) {
	m := newManager(t, "options", func(o *Options) {
		o.Home = t.TempDir()
		// gamma/model-3 在候选里，但代理设不上——广告过不等于能用。
		o.Agents[0].Options = map[string]string{"model": "gamma/model-3"}
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "x"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, err = m.Wait(context.Background(), task.ID)
	if err == nil || !strings.Contains(err.Error(), "model not found") ||
		!strings.Contains(err.Error(), `"model"`) {
		t.Fatalf("错误要带上代理的原话并指明是哪一项：%v", err)
	}
}

// TestSessionOptionsGrouped 分组形状的候选也要能设（展平之后才找得到）。
func TestSessionOptionsGrouped(t *testing.T) {
	m := newManager(t, "options", func(o *Options) {
		o.Home = t.TempDir()
		o.Agents[0].Options = map[string]string{"variant": "fancy"}
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "x"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got := strings.TrimSpace(run.Report); got != "sets: variant=fancy" {
		t.Fatalf("分组候选没设上：%q", got)
	}
}
