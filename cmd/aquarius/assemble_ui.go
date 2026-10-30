package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/characterfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/repl"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uitui"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// voiceInput 界面语音键（D179）：录一段 → 转写 → 回文本（不提交，界面回填输入框）。
func (w *wiring) voiceInput(ctx context.Context) (string, error) {
	if w.mic == nil {
		return "", errors.New("语音输入未装配")
	}
	text, _, err := w.mic.Listen(ctx, 0)
	return text, err
}

// voiceSlot 界面语音键槽（D179）：语音识别没装配就给 nil。
//
// 摄取器本身总会建出来（`/mic` 靠它报因，入口不该静默消失），所以「可有语音键」
// 不能拿摄取器是否为空来判——那样麦克风键会在一台根本没配语音的机器上照样亮着，
// 按下去只会得到「语音识别未配置」。界面据此保持发送键。
func (w *wiring) voiceSlot() func(context.Context) (string, error) {
	if w.asr == nil {
		return nil
	}
	return w.voiceInput
}

// previewVoice 设置窗「语音」页试听（D179）：用表单草稿的音色/描述/风格合成一句并
// 播放；服务与模型来自启动装配（这两项保存后重启才生效，与「模型服务」页同口径）。
func (w *wiring) previewVoice(req port.SpeechRequest) error {
	if w.ttsSynth == nil {
		return errors.New("语音合成未配置：先在「语音」页选服务与模型（保存后重启生效）")
	}
	if w.blobs == nil || w.player == nil {
		return errors.New("语音播放未装配")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ref, err := w.ttsSynth.Synthesize(ctx, req)
	if err != nil {
		return err
	}
	rc, err := w.blobs.Get(ctx, ref)
	if err != nil {
		return fmt.Errorf("读取合成音频：%w", err)
	}
	defer rc.Close()
	return w.player.Play(ctx, ref, rc)
}

// settingsSnapshot 设置窗快照（§15.7/D60-D61）：config 文本键（文件即事实源）+
// 运行态（等级活槽与 Agent 访问器，同状态行口径）。
func (w *wiring) settingsSnapshot() uigui.SettingsSnapshot {
	s := readTextSettings(w.cfgPath)
	s.Permission = w.lvl.Get().String()
	s.Unattended = w.unattended.Get()
	if agent := w.agentPtr.Load(); agent != nil {
		s.Model = agent.CurrentModel()
		s.Think = agent.ThinkOn()
		s.Effort = agent.CurrentEffort()
	}
	// S11-7①/D176 口径三：降级链取**启动时载入的** config（链改动与 provider 变更同
	// 口径——重启生效），并现算链窄于当前模型的警示。
	s.Fallback = fallbackLinksOf(w.cfg.Model.Fallback)
	s.FallbackNote = w.cfg.Model.narrowNote()
	return s
}

// applySettings 设置窗写回：单一 patch 回调——文本键泛键改写 + 内核命令计划
// （设置窗经输入通道与键入同路径串行执行，壳内不旁路）。
func (w *wiring) applySettings(p uigui.SettingsPatch) ([]port.Command, error) {
	// 联网与语音页填的明文密钥先转入密钥库、patch 里的 Key 换成引用（与 provider 页同一
	// 口径），再落盘——否则同一次「填明文」在设置窗不同页面会得到不同结果。
	if err := w.storeWebKeys(&p); err != nil {
		return nil, err
	}
	if err := w.storeAudioKeys(&p); err != nil {
		return nil, err
	}
	if err := persistSettingsTextKeys(w.cfgPath, p); err != nil {
		return nil, fmt.Errorf("写回 config: %w", err)
	}
	// D204：采样参数热生效——从刚写回的 config 现算一份快照换上去，下一次请求就用新值
	// （逐模型覆盖也跟着刷新；降级目标那份在装配时定，随重启生效）。
	if p.SamplingSet {
		w.refreshSampling()
	}
	// D179：音色与风格对此后的播报立即生效（服务与模型仍在启动装配时定，改动重启生效）。
	if w.ttsOut != nil {
		w.ttsOut.SetStyle(p.TTSVoice, p.TTSDesign, p.TTSStyle, p.TTSTags)
	}
	return settingsCommands(w.settingsSnapshot(), p), nil
}

// refreshSampling 重读 config 的 model 段、换掉采样快照（D204）。读失败/解析失败 = 保留
// 上一份——配置刚刚写成功，读不回来多半是外部并发改坏，不该让下一次请求发不出参数。
func (w *wiring) refreshSampling() {
	cfg, err := loadConfig(w.cfgPath)
	if err != nil {
		return
	}
	if err := cfg.Model.normalize(); err != nil {
		return
	}
	w.sampling.Store(cfg.Model.samplingTables())
}

// samplingFor Agent 每轮取采样参数的回调（D204）：按当前模型名在快照里查。
func (w *wiring) samplingFor(model string) port.Sampling {
	if t := w.sampling.Load(); t != nil {
		return t.For(model)
	}
	return w.cfg.Model.currentSampling()
}

// newUI 装配 UI 前端（D33/D43/D51）：Gio GUI（默认，§15）、bubbletea TUI
// （ui.kind=tui）或 repl（行式，测试/e2e 后端）——同权实现 uiFrontend，换壳不换核
// （D28 输出器装饰器自动继承）。TUI/GUI 状态行回调经 atomic 读 Agent（构造晚于
// UI 创建，事件循环并发读 → -race 必须）。
func (w *wiring) newUI(stdin io.Reader, stdout io.Writer) {
	switch w.cfg.UI.Kind {
	case "gui":
		// GUI（D43/§15）：悬浮窗事件循环自驱；位置记忆落 profile 内（§15.1）。
		// 不进 CI 图形路径（§15.5：GUI 测试全走 headless，门禁/e2e 仍 repl）。
		// 设置窗「角色」页只读数据面（§5.17）：角色卡目录晚于 UI 就绪，故现取。
		charView := newCharactersView(func() *characterfs.Store { return w.characters })
		ui := uigui.New(uigui.Options{
			Status: func() uigui.Status {
				if agent := w.agentPtr.Load(); agent != nil {
					st := uigui.Status{
						Model:      agent.CurrentModel(),
						Level:      w.lvl.Get().String(),
						Effort:     agent.CurrentEffort(), // D34：effort 档位（off 时为空）
						Unattended: w.unattended.Get(),    // D185：状态行常驻标记 + 菜单勾选源
						// D82：logo 事实卡（§15.1/S1-1g）——D110 起为真实 profile 名，
						// 会话事实取 app 侧发布快照（无锁原子读，§15.5 同口径）。
						Profile: w.profileName,
					}
					if sess := w.sessPtr.Load(); sess != nil {
						st.Facts = sess.Facts()
					}
					return st
				}
				return uigui.Status{}
			},
			Settings:            w.settingsSnapshot,
			ApplySettings:       w.applySettings,
			Profiles:            &profilesManager{dataDir: w.dir, current: w.profileName, profileCfg: w.cfgPath},                                                                                   // D110③：profile 区
			ProviderMgr:         &providerManager{cfgPath: w.cfgPath, secrets: w.secrets, bin: w.secretsBin, stderr: w.stderr, probe: w.probeForSettings, onStore: func() { w.secretsRev.Add(1) }}, // D110②：provider 编辑区（D161：明文入库；S9-6b/D163：落库递增快照 Revision；S11-7③：能力探测走 /model probe 同一写回）
			Tree:                sessionTree{p: &w.sessPtr},                                                                                                                                        // D80/§7.5：分叉条只读数据面
			Commands:            sessionCommands{p: &w.sessPtr},                                                                                                                                    // D103/S2b-1：补全浮层命令清单只读数据面
			Lister:              sessionList{p: &w.sessPtr},                                                                                                                                        // D120⑥：会话历史窗左栏只读数据面
			Indexer:             w.exchange,                                                                                                                                                        // D249：导入多会话归档时先取清单（勾选面板）
			PosFile:             filepath.Join(w.profileDir, "gui_pos.json"),                                                                                                                       // 位置记忆随 profile 走（D110 修订①）
			Hotkey:              w.cfg.UI.Hotkey,                                                                                                                                                   // 全局呼出快捷键（§15.1；空 = 默认 Alt+A）
			Theme:               w.cfg.UI.Theme,                                                                                                                                                    // 主题档 system|light|dark（§15.4/D61；空 = system）
			Scale:               w.cfg.UI.Scale,                                                                                                                                                    // 元素缩放倍率（D90/§15.8；0 = 1.0，UI 侧夹取）
			FontSize:            w.cfg.UI.FontSize,                                                                                                                                                 // 正文字号 sp（D90；0 = 15）
			WindowWidth:         w.cfg.UI.WindowWidth,                                                                                                                                              // 主窗像素尺寸（D90；0 = 缺省 608×460dp）
			WindowHeight:        w.cfg.UI.WindowHeight,
			Welcome:             &welcomeManager{cfgPath: w.cfgPath, secrets: w.secrets, bin: w.secretsBin, stderr: w.stderr, hotkey: w.cfg.UI.Hotkey, onStore: func() { w.secretsRev.Add(1) }}, // D141：欢迎 / 首次运行引导数据面（S9-6b/D163：明文落库递增快照 Revision）
			Tools:               &toolsAdmin{w: w},                                                                                                                                              // S6/D143：工具与 MCP 管理面（host/runner 由 wiring 后绑定，开窗时已就绪）
			Secrets:             &secretsAdmin{w: w},                                                                                                                                            // S9-6b/D161：密钥库管理面（secretsBin 由 bootstrap 填，开窗时已就绪）
			Skills:              &skillsAdmin{w: w},                                                                                                                                             // D202/D203：技能管理面（清单来自 skillfs.Catalog，安装走 skillfs.Installer）
			Characters:          charView.snapshot,                                                                                                                                              // §5.17：角色卡清单只读快照（动作经 /character 命令通道）
			PreviewPrompts:      w.previewPrompts,                                                                                                                                               // S7/D144：人格组合预览（当前表单 → 最终 system 文本）
			ApplyPromptTemplate: w.applyPromptTemplate,                                                                                                                                          // D154：人格模板窄写回（遮罩「默认模板」页签保存）
			Voice:               w.voiceSlot(),                                                                                                                                                  // D179：空输入时右钮 = 录音键（录→转写→回填，不自动发送）；识别未配置则为 nil（右钮保持发送）
			PreviewVoice:        w.previewVoice,                                                                                                                                                 // D179：设置窗「语音」页试听
			Blobs:               w.blobs,                                                                                                                                                        // D187：Ctrl+V 贴图直入附件库（nil = 禁用该入口）
			Version:             version,                                                                                                                                                        // 设置窗「关于」页的构建版本（-ldflags 注入；dev = 开发构建）
		})
		w.ui = ui
		// D141：首跑或 `ui.welcomed` 缺失（升级用户）→ 启动后自动开一次欢迎窗；完成或显式
		// 关闭即置位。刚做完装配前向导的情形标记已置位，不会二次打扰。
		if !w.cfg.UI.Welcomed {
			ui.OpenWelcome()
		}
	case "tui":
		w.ui = uitui.New(uitui.Options{
			In:  stdin,
			Out: stdout,
			Status: func() uitui.Status {
				if agent := w.agentPtr.Load(); agent != nil {
					return uitui.Status{
						Model:      agent.CurrentModel(),
						Level:      w.lvl.Get().String(),
						Effort:     agent.CurrentEffort(), // D34：effort 档位（off 时为空）
						Unattended: w.unattended.Get(),    // D185：状态行常驻标记
					}
				}
				return uitui.Status{}
			},
		})
	default:
		w.ui = repl.New(stdin, stdout)
	}
}
