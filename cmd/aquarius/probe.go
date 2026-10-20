package main

// 能力探测的装配根一半（D174）：挑 provider、造一次性客户端、跑三项探测、把判定明确的
// 结果写回 config。协议相关的部分（请求长什么样、什么算答上来了）在适配器里。
//
// 两条纪律：
//   - 探测**只读运行态**：不改 config 以外的任何东西，也不改 `unsupported_params`。
//     写回是唯一副作用，且要重启才生效（与 provider 编辑同口径——port.LLM 构造期固化）。
//   - 探测用该 provider 的**第一把钥匙**：探测不该扰动密钥池的冷却状态（S11-4）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/llm"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// probeTimeout 单项探测的时长上限。探测是真请求，卡久的服务端要主动放弃——
// 否则一次点击能挂几分钟。
const probeTimeout = 30 * time.Second

// probeCaps 探测顺序（与展示顺序一致）：工具、思考、图片。
var probeCaps = []string{port.CapTools, port.CapReasoning, port.CapVision}

// probeModel 对某模型发起三项能力探测并写回判定明确的结果（D174）。
// model 为空 = primary 的当前模型。
func (w *wiring) probeModel(ctx context.Context, model string) (app.ProbeReport, error) {
	p, name, err := w.probeTarget(model)
	if err != nil {
		return app.ProbeReport{}, err
	}
	return w.probeProvider(ctx, p, name)
}

// probeForSettings 设置窗「探测能力」按钮的数据面（S11-7③/D174）：指名 provider 名 +
// 模型名，走与 `/model probe` **同一条**写回逻辑（probeProvider），只是把结论包成设置窗
// 契约的形态（界面按行展示；不写会话输出，也不碰运行态）。
func (w *wiring) probeForSettings(ctx context.Context, provider, model string) (uigui.ProbeOutcome, error) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	p := w.cfg.Model.byName(provider)
	if p == nil {
		return uigui.ProbeOutcome{}, fmt.Errorf("provider %q 不在配置里（可能被外部改过）", provider)
	}
	if p.modelByName(model) == nil {
		return uigui.ProbeOutcome{}, fmt.Errorf("模型 %q 不在 provider %q 的 models 里；先保存把它声明进去再探测"+
			"（探测结果要写回该模型条目的能力声明）", model, provider)
	}
	report, err := w.probeProvider(ctx, p, model)
	return uigui.ProbeOutcome{
		Provider: report.Provider, Model: report.Model,
		Results: report.Results, Written: report.Written,
	}, err
}

// probeProvider 对确定 provider 下的某模型跑三项探测并写回（两种入口共用）。
func (w *wiring) probeProvider(ctx context.Context, p *providerConfig, name string) (app.ProbeReport, error) {
	client, err := w.probeClient(p)
	if err != nil {
		return app.ProbeReport{}, err
	}
	prober, ok := any(client).(port.Prober)
	if !ok {
		return app.ProbeReport{}, fmt.Errorf("provider %q 的协议暂不支持能力探测", p.Name)
	}

	report := app.ProbeReport{Provider: p.Name, Model: name}
	for _, capability := range probeCaps {
		pctx, cancel := context.WithTimeout(ctx, probeTimeout)
		res, perr := prober.Probe(pctx, name, capability)
		cancel()
		if perr != nil {
			if ctx.Err() != nil {
				return report, ctx.Err() // 用户自己撤了：带上已有结论返回
			}
			res = port.ProbeResult{Capability: capability, Verdict: port.ProbeInconclusive,
				Reason: "探测没跑起来：" + perr.Error()}
		}
		report.Results = append(report.Results, res)
	}

	written, werr := w.writeProbeCaps(p, name, report.Results)
	report.Written = written
	return report, werr
}

// probeTarget 定位要探测的模型条目（D174 口径四：度量单位是「模型条目」）。
// 只有声明过的模型才有地方写 caps，所以未声明的模型直接报因——悄悄探完不写回，
// 用户会以为已经改好了。
func (w *wiring) probeTarget(model string) (*providerConfig, string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		// 运行态优先：/model 热切换只动 Agent，不回写 w.cfg.Model.Name（那份是启动快照）。
		if agent := w.agentPtr.Load(); agent != nil {
			if cur := strings.TrimSpace(agent.CurrentModel()); cur != "" {
				return w.providerOf(cur)
			}
		}
		p := w.cfg.Model.primaryProvider()
		return p, w.cfg.Model.Name, nil
	}
	return w.providerOf(model)
}

// providerOf 找到声明了该模型的 provider（primary 优先），未声明则报因。
func (w *wiring) providerOf(model string) (*providerConfig, string, error) {
	if p := w.cfg.Model.primaryProvider(); p.modelByName(model) != nil {
		return p, model, nil
	}
	for i := range w.cfg.Model.Providers {
		if w.cfg.Model.Providers[i].modelByName(model) != nil {
			return &w.cfg.Model.Providers[i], model, nil
		}
	}
	return nil, "", fmt.Errorf("模型 %q 不在任何 provider 的 models 里；先在配置中声明它再探测"+
		"（探测结果要写回该模型条目的能力声明）", model)
}

// probeClient 造探测用的一次性客户端：第一把钥匙、不记录 unsupported_params（探测只读）。
func (w *wiring) probeClient(p *providerConfig) (*llm.Client, error) {
	// 探测只实现了 chat/completions 的问法（响应形状的判定逻辑一种协议一份）。别的协议的
	// 端点收到 chat 形状的请求只会回些含糊的错，结论会变成三条「无结论」——那比直接说
	// 「这个协议还问不了」更容易误导。
	if kind := p.protocolKind(); kind != kindOpenAIChat {
		return nil, fmt.Errorf("provider %q 的协议 %q 暂不支持能力探测（当前只测 %s）",
			p.Name, kind, kindOpenAIChat)
	}
	// note 传 nil：探测刻意发可能不被支持的参数，那些结论不该被记成「这个 provider
	// 不认该参数」——正式请求的剥离记录归正式请求。
	key, err := p.resolveKeyAt(context.Background(), 0, w.secrets, w.stderr)
	if err != nil {
		return nil, err
	}
	c, _, err := w.newLLMClient(p, key, nil)
	return c, err
}

// writeProbeCaps 把判定明确的几位合并进该模型条目并写回 config（D174 口径二）。
// 返回是否真的写回了（无明确结论且声明没变 = 不打扰文件）。
//
// 比较基准是**磁盘上的声明**而不是内存里的：同一进程里连探两次，第二次看到的应当是
// 上次写回的结果，否则每次都会「写回」一遍。内存那份不动——它驱动的是运行中的装配
// 口径（重启才生效），这里改了会让 /model 显示与实际发出去的请求不一致。
func (w *wiring) writeProbeCaps(p *providerConfig, model string, results []port.ProbeResult) (bool, error) {
	disk, err := loadConfig(w.cfgPath)
	if err != nil {
		return false, fmt.Errorf("读取配置: %w", err)
	}
	dp := disk.Model.byName(p.Name)
	if dp == nil {
		return false, errors.New("探测目标已不在配置里（配置被外部改过）")
	}
	entry := dp.modelByName(model)
	if entry == nil {
		return false, errors.New("探测目标已不在配置里（配置被外部改过）")
	}
	// 起点是**生效值**而不是空集：键缺失时按协议缺省，直接写探测结果会把另外几位
	// 从「缺省开」悄悄降成「关」。
	caps := dp.capsOf(*entry)
	definite := false
	for _, r := range results {
		switch r.Verdict {
		case port.ProbeSupported:
			definite = true
			setCap(&caps, r.Capability, true)
		case port.ProbeUnsupported:
			definite = true
			setCap(&caps, r.Capability, false)
		}
	}
	names := caps.CapNames()
	if !definite || sameCaps(entry.Caps, names) {
		return false, nil
	}
	err = persistConfig(w.cfgPath, func(generic map[string]any) {
		ps := providerSection(generic, p.Name)
		if ps == nil {
			return
		}
		models, _ := ps["models"].([]any)
		for _, mv := range models {
			pm, ok := mv.(map[string]any)
			if !ok {
				continue
			}
			if n, _ := pm["name"].(string); n == model {
				pm["caps"] = names // 总是写全（含空数组）：探测之后这几项就是显式声明了
				return
			}
		}
	})
	if err != nil {
		return false, fmt.Errorf("写回能力声明: %w", err)
	}
	return true, nil
}

// setCap 按能力名置位（未知名字忽略——探测只跑三项，不会到这里）。
func setCap(caps *port.ModelCaps, capability string, on bool) {
	switch capability {
	case port.CapTools:
		caps.Tools = on
	case port.CapReasoning:
		caps.Reasoning = on
	case port.CapVision:
		caps.Vision = on
	}
}

// sameCaps 声明的名字表与合并结果是否一致（nil 声明 ≠ 显式列表：写回会把缺省固化成
// 显式声明，所以内容相同但形态不同也算「变了」）。
func sameCaps(declared *[]string, names []string) bool {
	if declared == nil {
		return false
	}
	want := port.CapsFromNames(names)
	return port.CapsFromNames(*declared) == want && len(*declared) == len(names)
}
