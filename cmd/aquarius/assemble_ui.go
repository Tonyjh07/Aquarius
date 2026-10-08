package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/Tonyjh07/Aquarius/internal/adapter/repl"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uitui"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// settingsSnapshot 设置窗快照（§15.7/D60-D61）：config 文本键（文件即事实源）+
// 运行态（等级活槽与 Agent 访问器，同状态行口径）。
func (w *wiring) settingsSnapshot() uigui.SettingsSnapshot {
	s := readTextSettings(w.cfgPath)
	s.Permission = w.lvl.Get().String()
	if agent := w.agentPtr.Load(); agent != nil {
		s.Model = agent.CurrentModel()
		s.Think = agent.ThinkOn()
		s.Effort = agent.CurrentEffort()
	}
	return s
}

// applySettings 设置窗写回：单一 patch 回调——文本键泛键改写 + 内核命令计划
// （设置窗经输入通道与键入同路径串行执行，壳内不旁路）。
func (w *wiring) applySettings(p uigui.SettingsPatch) ([]port.Command, error) {
	if err := persistSettingsTextKeys(w.cfgPath, p); err != nil {
		return nil, fmt.Errorf("写回 config: %w", err)
	}
	return settingsCommands(w.settingsSnapshot(), p), nil
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
		w.ui = uigui.New(uigui.Options{
			Status: func() uigui.Status {
				if agent := w.agentPtr.Load(); agent != nil {
					st := uigui.Status{
						Model:  agent.CurrentModel(),
						Level:  w.lvl.Get().String(),
						Effort: agent.CurrentEffort(), // D34：effort 档位（off 时为空）
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
			Settings:      w.settingsSnapshot,
			ApplySettings: w.applySettings,
			Profiles:      &profilesManager{dataDir: w.dir, current: w.profileName, profileCfg: w.cfgPath}, // D110③：profile 区
			ProviderMgr:   &providerManager{cfgPath: w.cfgPath, stderr: w.stderr},                          // D110②：provider 编辑区
			Tree:          sessionTree{p: &w.sessPtr},                                                      // D80/§7.5：分叉条只读数据面
			Commands:      sessionCommands{p: &w.sessPtr},                                                  // D103/S2b-1：补全浮层命令清单只读数据面
			Lister:        sessionList{p: &w.sessPtr},                                                      // D120⑥：会话历史窗左栏只读数据面
			PosFile:       filepath.Join(w.profileDir, "gui_pos.json"),                                     // 位置记忆随 profile 走（D110 修订①）
			Hotkey:        w.cfg.UI.Hotkey,                                                                 // 全局呼出快捷键（§15.1；空 = 默认 Alt+A）
			Theme:         w.cfg.UI.Theme,                                                                  // 主题档 system|light|dark（§15.4/D61；空 = system）
			Scale:         w.cfg.UI.Scale,                                                                  // 元素缩放倍率（D90/§15.8；0 = 1.0，UI 侧夹取）
			FontSize:      w.cfg.UI.FontSize,                                                               // 正文字号 sp（D90；0 = 15）
			WindowWidth:   w.cfg.UI.WindowWidth,                                                            // 主窗像素尺寸（D90；0 = 缺省 608×460dp）
			WindowHeight:  w.cfg.UI.WindowHeight,
		})
	case "tui":
		w.ui = uitui.New(uitui.Options{
			In:  stdin,
			Out: stdout,
			Status: func() uitui.Status {
				if agent := w.agentPtr.Load(); agent != nil {
					return uitui.Status{
						Model:  agent.CurrentModel(),
						Level:  w.lvl.Get().String(),
						Effort: agent.CurrentEffort(), // D34：effort 档位（off 时为空）
					}
				}
				return uitui.Status{}
			},
		})
	default:
		w.ui = repl.New(stdin, stdout)
	}
}
