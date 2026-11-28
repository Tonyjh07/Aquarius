package main

// 插件供给面的启停挂载（D356 批 2）：setTierOne 的启用分支逐面挂载、停用分支反序还原。
// 每面独立——单面拿不到实现（声明了但断言失败）只 warn 一行，不拖垮插件本体（面缺失
// 不等于插件失败，与工具面同一口径）。容器本体见 pluginports.go。

import (
	"context"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/outputtts"
	"github.com/Tonyjh07/Aquarius/internal/adapter/plugingo"
	"github.com/Tonyjh07/Aquarius/internal/logging"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// attachPluginPorts 启用后的逐面挂载（ctx 供拉插件模型清单）。
func (w *wiring) attachPluginPorts(ctx context.Context, name string) {
	inst, _, ok := w.plugins1.Active(name)
	if !ok {
		return
	}
	attached := 0 // 任意一面挂上就记账（还原日志只在挂过时打）
	// llm：模型清单拉到了才挂（清单是 /model 选到它的唯一途径）。
	if l, ok := inst.(v1.LanguageModel); ok && w.llmRoute != nil {
		ms, err := l.Models(ctx)
		names := make([]string, 0, len(ms))
		if err == nil {
			for _, m := range ms {
				names = append(names, m.Name)
			}
		}
		switch {
		case err != nil:
			w.warnPluginPort(name, "log.plugin.t1_llm_models_failed", "err", err.Error())
		case len(names) == 0:
			w.warnPluginPort(name, "log.plugin.t1_llm_models_failed", "err", "no models reported")
		default:
			w.llmRoute.attach(name, plugingo.NewLLM(l), names)
			attached++
			w.note(logging.LevelInfo, logging.Plugin, "log.plugin.t1_llm_attached",
				"log.act.ready", "name", name, "models", strings.Join(names, ", "))
		}
	}
	// ingest：集合尾位，内置优先（D356）。
	if ing, ok := inst.(v1.Ingestor); ok {
		w.pluginIngest().put(name, plugingo.NewIngestor(ing))
		attached++
		w.note(logging.LevelInfo, logging.Plugin, "log.plugin.t1_ingest_attached",
			"log.act.ready", "name", name)
	}
	// output：按名进扇出；Deliver 失败本就只记日志（§5.7）。
	if o, ok := inst.(v1.OutputAdapter); ok && w.presenter != nil {
		if w.pluginOuts == nil {
			w.pluginOuts = map[string]port.OutputAdapter{}
		}
		out := plugingo.NewOutput(o)
		w.pluginOuts[name] = out
		w.presenter.AddOutput(out)
		attached++
		w.note(logging.LevelInfo, logging.Plugin, "log.plugin.t1_output_attached",
			"log.act.ready", "name", name)
	}
	// transcribe：启用即接管，独占（第一个赢；第二个该面不挂）。
	if t, ok := inst.(v1.Transcriber); ok && w.asrRoute != nil {
		if owner := w.asrRoute.take(name, decorate.NewTranscriberRetry(plugingo.NewTranscriber(t))); owner != "" {
			w.slotTaken(name, "transcribe", owner)
		} else {
			if !w.asrRoute.builtinWired() {
				w.asr = w.asrRoute // 内置没配过：语音键显隐跟 w.asr，接管时要把它指到容器
			}
			attached++
			w.note(logging.LevelInfo, logging.Plugin, "log.plugin.t1_asr_takeover",
				"log.act.ready", "name", name)
		}
	}
	// synthesize：同识别槽位；用户开了播报但内置 TTS 没建时，现挂一路输出。
	if s, ok := inst.(v1.Synthesizer); ok && w.ttsRoute != nil {
		if owner := w.ttsRoute.take(name, decorate.NewSynthesizerRetry(plugingo.NewSynthesizer(s))); owner != "" {
			w.slotTaken(name, "synthesize", owner)
		} else {
			w.attachTTSOutput(name)
			attached++
			w.note(logging.LevelInfo, logging.Plugin, "log.plugin.t1_tts_takeover",
				"log.act.ready", "name", name)
		}
	}
	// secrets：兜底档，独占（第一个赢；密钥链只有一档插件位）。
	if sp, ok := inst.(v1.SecretProvider); ok && w.secrets != nil {
		if w.secretExtraOwner != "" {
			w.slotTaken(name, "secrets", w.secretExtraOwner)
		} else {
			w.secretExtraOwner = name
			w.secrets.SetExtra(plugingo.NewSecrets(sp))
			attached++
			w.note(logging.LevelInfo, logging.Plugin, "log.plugin.t1_secrets_takeover",
				"log.act.ready", "name", name)
		}
	}
	if attached > 0 {
		if w.pluginPortsAttached == nil {
			w.pluginPortsAttached = map[string]bool{}
		}
		w.pluginPortsAttached[name] = true
	}
}

// detachPluginPorts 停用前的逐面还原（在 Shutdown 之前调——适配器还会调实例方法，
// 停了之后再摘等于对着已停实例发调用）。
func (w *wiring) detachPluginPorts(name string) {
	if w.llmRoute != nil {
		w.llmRoute.detach(name)
	}
	if w.ingRoute != nil {
		w.ingRoute.remove(name)
	}
	if w.presenter != nil {
		if out, ok := w.pluginOuts[name]; ok {
			w.presenter.RemoveOutput(out.Name())
			delete(w.pluginOuts, name)
		}
	}
	if w.asrRoute != nil {
		w.asrRoute.release(name)
		if !w.asrRoute.builtinWired() && w.asr == w.asrRoute {
			w.asr = nil // 内置本来就没配：还原 = 语音键退回隐藏
		}
	}
	if w.ttsRoute != nil {
		w.ttsRoute.release(name)
		if w.pluginTTSOut && !w.ttsRoute.builtinWired() {
			// 那路输出是接管时现挂的：随接管一起撤（内置选型回来的情形走原 outputtts）。
			if w.ttsOut != nil && w.presenter != nil {
				w.presenter.RemoveOutput(w.ttsOut.Name())
			}
			w.ttsOut = nil
			w.pluginTTSOut = false
		}
	}
	if w.secretExtraOwner == name && w.secrets != nil {
		w.secretExtraOwner = ""
		w.secrets.SetExtra(nil)
	}
	if w.hasAttachedPort(name) {
		delete(w.pluginPortsAttached, name)
		w.note(logging.LevelInfo, logging.Plugin, "log.plugin.t1_port_released",
			"log.act.restored", "name", name)
	}
}

// attachTTSOutput 插件接管合成后，播报输出位缺失（内置 TTS 没配过）时现挂一路；
// 只在用户开了 output.tts 时做——没开播报就不该有播报路。
func (w *wiring) attachTTSOutput(name string) {
	if !w.cfg.Output.TTS || w.ttsOut != nil || w.presenter == nil || w.player == nil {
		return
	}
	out, err := outputtts.New(outputtts.Config{
		Synth:  w.ttsRoute,
		Player: w.player,
		Blobs:  w.blobs,
		Voice:  w.cfg.Audio.TTS.Voice,
		Design: w.cfg.Audio.TTS.Design,
		Style:  w.cfg.Audio.TTS.Style,
		Tags:   w.cfg.Audio.TTS.Tags,
		Log:    w.logFrom,
	})
	if err != nil {
		w.warnPluginPort(name, "log.plugin.t1_tts_out_failed", "err", err.Error())
		return
	}
	w.ttsOut = out
	w.pluginTTSOut = true
	w.presenter.AddOutput(out)
}

// hasAttachedPort 该插件是否真的挂过任意一面（还原日志只在挂过时打）。挂载在
// attachPluginPorts 里逐面记账（pluginPortsAttached）。
func (w *wiring) hasAttachedPort(name string) bool { return w.pluginPortsAttached[name] }

// slotTaken 独占面被别的插件占着：warn 一行，插件本体不受影响。
func (w *wiring) slotTaken(name, portName, owner string) {
	w.note(logging.LevelWarn, logging.Plugin, "log.plugin.t1_slot_taken",
		"log.act.keep_first", "name", name, "port", portName, "owner", owner)
}

// warnPluginPort 面挂载失败的统一落点。
func (w *wiring) warnPluginPort(name, event, action string, kv ...any) {
	w.note(logging.LevelWarn, logging.Plugin, event, action, append([]any{"name", name}, kv...)...)
}
