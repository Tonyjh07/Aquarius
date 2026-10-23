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

// probeForSettings 设置窗「探测能力」按钮的数据面（S11-7③/D174；D181 修口径）：用**编辑区
// 当前值**（端点 / 协议 / 第一把钥匙）造一次性客户端——改了端点还没应用时，该测的是「将要
// 应用的那份」，而不是启动快照里那份。结论一律带回界面；写回 config 只在该模型**已经在
// 配置里**的时候发生，否则留给「应用 provider 变更」一并落盘（Pending）。
//
// 为什么不要求先保存：模型行是不是「已声明」由配置文件决定，而用户眼里表格里那一行就是
// 声明。以前直接报「不在 models 里」，用户无从下手——他并没有别的地方可以声明。
func (w *wiring) probeForSettings(ctx context.Context, patch uigui.ProviderPatch, model string) (uigui.ProbeOutcome, error) {
	provider := strings.TrimSpace(patch.Name)
	model = strings.TrimSpace(model)
	if provider == "" {
		return uigui.ProbeOutcome{}, errors.New("先填 provider 名再探测")
	}
	if model == "" {
		return uigui.ProbeOutcome{}, errors.New("先填模型名再探测")
	}
	if strings.TrimSpace(patch.BaseURL) == "" {
		return uigui.ProbeOutcome{}, fmt.Errorf("provider %q 的接口地址为空，先填 base_url 再探测", provider)
	}
	pc := w.providerFromPatch(patch)
	client, err := w.probeClient(pc)
	if err != nil {
		return uigui.ProbeOutcome{}, err
	}
	report, perr := w.probeWithClient(ctx, pc, client, model)
	out := uigui.ProbeOutcome{Provider: report.Provider, Model: report.Model, Results: report.Results}
	if perr != nil {
		return out, perr
	}
	// 写回的目标名：改名还没应用时，磁盘上那条还挂在 Orig 名下（写到新名字上会找不到）。
	diskName := strings.TrimSpace(patch.Orig)
	if diskName == "" {
		diskName = pc.Name
	}
	written, declared, werr := w.writeProbeCaps(diskName, model, report.Results)
	out.Written, out.Pending = written, !declared
	return out, werr
}

// providerFromPatch 把编辑区提交拼成一个临时 providerConfig（探测只用到名字 / 协议 /
// 端点 / 第一把钥匙 / 不认参数）。钥匙引用：编辑区填了就用填的（明文或 secret:），空则
// 回落落盘 config 里同一条 provider 的原引用（按 Orig 名——改名过程中引用不丢）。
func (w *wiring) providerFromPatch(patch uigui.ProviderPatch) *providerConfig {
	pc := &providerConfig{
		Name:              strings.TrimSpace(patch.Name),
		Kind:              strings.TrimSpace(patch.Kind),
		BaseURL:           strings.TrimSpace(patch.BaseURL),
		Tokenizer:         strings.TrimSpace(patch.Tokenizer),
		UnsupportedParams: splitCSV(patch.Unsupported),
	}
	ref := ""
	if len(patch.Keys) > 0 {
		ref = strings.TrimSpace(patch.Keys[0].Value)
	}
	if ref == "" {
		disk, err := loadConfig(w.cfgPath)
		if err == nil {
			name := strings.TrimSpace(patch.Orig)
			if name == "" {
				name = pc.Name
			}
			if p := disk.Model.byName(name); p != nil {
				if refs := p.keyRefs(); len(refs) > 0 {
					ref = refs[0]
				}
			}
		}
	}
	pc.Keys = []keyConfig{{APIKey: ref}}
	return pc
}

// probeProvider 对确定 provider 下的某模型跑三项探测并写回（`/model probe` 入口）。
func (w *wiring) probeProvider(ctx context.Context, p *providerConfig, name string) (app.ProbeReport, error) {
	client, err := w.probeClient(p)
	if err != nil {
		return app.ProbeReport{}, err
	}
	report, perr := w.probeWithClient(ctx, p, client, name)
	if perr != nil {
		return report, perr
	}
	written, declared, werr := w.writeProbeCaps(p.Name, name, report.Results)
	if werr != nil {
		return report, werr
	}
	if !declared {
		// 命令行入口的前提是「模型已在配置里声明」（probeTarget 校验过），到这里说明
		// 配置在同一进程里被外部改过——必须说出来，不能让用户以为结论已落盘。
		return report, fmt.Errorf("探测目标已不在配置里（配置被外部改过）")
	}
	report.Written = written
	return report, nil
}

// probeWithClient 用给定客户端跑三项探测（入口共用；协议相关部分在适配器里）。
func (w *wiring) probeWithClient(ctx context.Context, p *providerConfig, client *llm.Client, name string) (app.ProbeReport, error) {
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
	return report, nil
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
	// 不认该参数」——正式请求的剥离记录归正式请求。凭据同样按「草稿」解析：探测不该
	// 补发落盘形态警告，那条留给真实构造路径（D210）。
	key, err := resolvePatchKey(context.Background(), p.Name, p.keyRefs()[0], w.secrets)
	if err != nil {
		return nil, err
	}
	c, _, err := w.newLLMClient(p, key, nil)
	return c, err
}

// writeProbeCaps 把判定明确的几位合并进该模型条目并写回 config（D174 口径二）。
// 返回 written（是否真的写回了——无明确结论且声明没变 = 不打扰文件）与 declared
// （该模型条目在**磁盘配置**里是否存在；false = 还没应用，结论只能留在界面上）。
//
// 比较基准是**磁盘上的声明**而不是内存里的：同一进程里连探两次，第二次看到的应当是
// 上次写回的结果，否则每次都会「写回」一遍。内存那份不动——它驱动的是运行中的装配
// 口径（重启才生效），这里改了会让 /model 显示与实际发出去的请求不一致。
func (w *wiring) writeProbeCaps(providerName, model string, results []port.ProbeResult) (written, declared bool, err error) {
	disk, err := loadConfig(w.cfgPath)
	if err != nil {
		return false, false, fmt.Errorf("读取配置: %w", err)
	}
	dp := disk.Model.byName(providerName)
	if dp == nil {
		return false, false, nil
	}
	entry := dp.modelByName(model)
	if entry == nil {
		return false, false, nil
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
		return false, true, nil
	}
	err = persistConfig(w.cfgPath, func(generic map[string]any) {
		ps := providerSection(generic, providerName)
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
		return false, true, fmt.Errorf("写回能力声明: %w", err)
	}
	return true, true, nil
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
