package uigui

// settings_page_skills.go 「技能」页（D202/D203；数据面与异步动作见 settings_skills.go）。
//
// 页面内容五块：
//  1. 已安装清单（名字 / 一句说明 / 来源种类与更新时间和目录）——管理副本才显示「更新」
//     与「卸载」，行内二次确认卸载；
//  2. 安装 / 导入（来源输入档 + 「选择文件…」「选择文件夹…」+「安装」）；
//  3. 技能目录（`config skills.dirs` 增删；兜底根 `<profile>/skills` 只读展示）；
//  4. 「重新扫描」（不做文件监听，硬性规则 7）；
//  5. 未加载的条目与原因（灰字，不进模型上下文）。
//
// 边界：只认用户给的路径或链接，不做仓库浏览（D203 口径三）。

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
)

// skillsPage 技能页模块。
type skillsPage struct{}

func (skillsPage) Key() string { return "技能" }

func (skillsPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{sec("已安装技能")}
	if f.skills == nil {
		return append(rows, rowDesc{kind: rowSkillEmpty, text: "未接线（只读占位）"})
	}
	if len(f.skillsSnap.Skills) == 0 {
		rows = append(rows, rowDesc{kind: rowSkillEmpty,
			text: "还没有技能——下面给一个 SKILL.md、文件夹、压缩包或 GitHub 链接即可装进来"})
	}
	for i := range f.skillsSnap.Skills {
		rows = append(rows, rowDesc{kind: rowSkillItem, field: i})
	}
	rows = append(rows,
		sec("安装 / 导入"),
		rowDesc{kind: rowSkillNote},
		rowDesc{kind: rowSkillSource},
		rowDesc{kind: rowSkillPick},
		sec("技能目录（按顺序查找，同名先命中者生效）"),
	)
	for i := range f.skillsSnap.Dirs {
		rows = append(rows, rowDesc{kind: rowSkillDir, field: i})
	}
	rows = append(rows, rowDesc{kind: rowSkillDirAdd}, rowDesc{kind: rowSkillRescan})
	if len(f.skillsSnap.Warnings) > 0 {
		rows = append(rows, sec("未加载的条目"))
		for i := range f.skillsSnap.Warnings {
			rows = append(rows, rowDesc{kind: rowSkillWarn, field: i})
		}
	}
	if f.skillsStatus != "" {
		rows = append(rows, rowDesc{kind: rowSkillStatus})
	}
	return rows
}

func (skillsPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	switch r.kind {
	case rowSkillItem:
		if r.field < 0 || r.field >= len(f.skillsSnap.Skills) {
			return layout.Dimensions{}, true
		}
		return f.skillItemRow(gtx, th, f.skillsSnap.Skills[r.field]), true
	case rowSkillEmpty:
		l := material.Body2(th, r.text)
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowSkillNote:
		l := material.Body2(th, "支持单文件 SKILL.md、技能文件夹、.zip 压缩包与 GitHub 链接（可写 #分支）。"+
			"装进来的是副本：更新会覆盖这份副本里的本地改动，卸载只删这份副本。"+
			"同名技能不覆盖——先卸载再装。")
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowSkillSource:
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "来源", editorBox(th, &f.skillSource,
					"绝对路径或 GitHub 链接（如 https://github.com/o/r 或 …#分支）"))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return paperButton(th, &f.skillInstall, "安装", paperBtnPrimary, f.skillWait == 0)(gtx)
				})
			}),
		), true
	case rowSkillPick:
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return paperButton(th, &f.skillPickFile, "选择文件…", paperBtnSecondary, true)(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return paperButton(th, &f.skillPickDir, "选择文件夹…", paperBtnSecondary, true)(gtx)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperMicroSp(th), "选中路径只填进上面的来源框，再点「安装」才落盘")
					l.Color = paperOf().text3
					return l.Layout(gtx)
				})
			}),
		), true
	case rowSkillDir:
		if r.field < 0 || r.field >= len(f.skillsSnap.Dirs) {
			return layout.Dimensions{}, true
		}
		return f.skillDirRow(gtx, th, f.skillsSnap.Dirs[r.field]), true
	case rowSkillDirAdd:
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "添加目录", editorBox(th, &f.skillDirInput, "额外技能根（绝对路径）"))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return paperButton(th, &f.skillDirAdd, "添加", paperBtnSecondary, true)(gtx)
				})
			}),
		), true
	case rowSkillRescan:
		return paperButton(th, &f.skillRescan, "重新扫描", paperBtnSecondary, f.skillWait == 0)(gtx), true
	case rowSkillWarn:
		if r.field < 0 || r.field >= len(f.skillsSnap.Warnings) {
			return layout.Dimensions{}, true
		}
		l := material.Body2(th, f.skillsSnap.Warnings[r.field])
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowSkillStatus:
		l := material.Body2(th, f.skillsStatus)
		if f.skillsErr {
			l.Color = paperOf().danger
		} else {
			l.Color = paperOf().text3
		}
		return l.Layout(gtx), true
	}
	return layout.Dimensions{}, false
}

func (skillsPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	for f.skillInstall.Clicked(gtx) {
		source := strings.TrimSpace(f.skillSource.Text())
		u.skillsAsync(f, func() (string, error) {
			status, err := f.skills.Install(source)
			if err != nil {
				return "", err
			}
			f.skillSource.SetText("")
			return status, nil
		})
	}
	for f.skillPickFile.Clicked(gtx) {
		u.plat.RequestPathPick(platform.PickSkillFile)
	}
	for f.skillPickDir.Clicked(gtx) {
		u.plat.RequestPathPick(platform.PickSkillDir)
	}
	for f.skillDirAdd.Clicked(gtx) {
		dir := strings.TrimSpace(f.skillDirInput.Text())
		u.skillsAsync(f, func() (string, error) {
			if err := f.skills.AddDir(dir); err != nil {
				return "", err
			}
			f.skillDirInput.SetText("")
			return "已添加技能目录（立即生效）", nil
		})
	}
	for f.skillRescan.Clicked(gtx) {
		u.skillsAsync(f, func() (string, error) {
			if err := f.skills.Rescan(); err != nil {
				return "", err
			}
			return "已重新扫描技能目录", nil
		})
	}
	// 按键分派：`up:` / `rm:` / `cancel:` / `confirm:` / `dir:`。
	for key, btn := range f.skillBtns {
		for btn.Clicked(gtx) {
			switch {
			case key == "dir:add":
				// 「＋ 添加技能目录」按钮已并入输入行，这里保留空分支防将来复用。
			case strings.HasPrefix(key, "up:"):
				name := strings.TrimPrefix(key, "up:")
				u.skillsAsync(f, func() (string, error) {
					return f.skills.Update(name)
				})
			case strings.HasPrefix(key, "rm:"):
				f.skillConfirm = strings.TrimPrefix(key, "rm:")
			case strings.HasPrefix(key, "cancel:"):
				f.skillConfirm = ""
			case strings.HasPrefix(key, "confirm:"):
				name := strings.TrimPrefix(key, "confirm:")
				f.skillConfirm = ""
				u.skillsAsync(f, func() (string, error) {
					if err := f.skills.Remove(name); err != nil {
						return "", err
					}
					return "已卸载技能 " + name, nil
				})
			case strings.HasPrefix(key, "dir:"):
				dir := strings.TrimPrefix(key, "dir:")
				u.skillsAsync(f, func() (string, error) {
					if err := f.skills.RemoveDir(dir); err != nil {
						return "", err
					}
					return "已移除技能目录 " + dir, nil
				})
			}
		}
	}
}

// FirstEditor 开窗焦点入来源档（导入是本页的主要动作）。
func (skillsPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.skillSource }

// skillItemRow 已装技能行：名字 + 说明 + 来源/更新时间/目录；右侧动作（更新 / 卸载）。
func (f *settingsForm) skillItemRow(gtx layout.Context, th *material.Theme, st SkillState) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return material.Body1(th, st.Name).Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, oneLineText(st.Description, 160))
						l.Color = paperOf().text
						return l.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, paperMicroSp(th), st.metaLine())
						l.Color = paperOf().text3
						return l.Layout(gtx)
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !st.Managed {
				return layout.Dimensions{}
			}
			if f.skillConfirm == st.Name {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, "确认？")
						l.Color = paperOf().danger
						return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, l.Layout)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return f.skillActionButton(gtx, th, "cancel:"+st.Name, "取消", paperBtnSecondary)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return f.skillActionButton(gtx, th, "confirm:"+st.Name, "确认卸载", paperBtnDanger)
					}),
				)
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !st.Updatable {
						return layout.Dimensions{}
					}
					return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return f.skillActionButton(gtx, th, "up:"+st.Name, "更新", paperBtnSecondary)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return f.skillActionButton(gtx, th, "rm:"+st.Name, "卸载", paperBtnDanger)
				}),
			)
		}),
	)
}

// skillDirRow 技能根一行：路径（兜底根注明不可删）+ 非兜底根的「移除」。
func (f *settingsForm) skillDirRow(gtx layout.Context, th *material.Theme, d SkillDir) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, d.skillDirLabel())
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if d.Fallback {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return f.skillActionButton(gtx, th, "dir:"+d.Path, "移除", paperBtnDanger)
			})
		}),
	)
}

// skillActionButton 技能页动作按钮（get-or-create 按 key 缓存）。
func (f *settingsForm) skillActionButton(gtx layout.Context, th *material.Theme, key, label string, kind paperBtnKind) layout.Dimensions {
	btn := f.skillBtns[key]
	if btn == nil {
		btn = new(widget.Clickable)
		f.skillBtns[key] = btn
	}
	return paperButton(th, btn, label, kind, true)(gtx)
}

// oneLineText 把说明压成一行并在超长时截断（回车会把行撑高，读起来像数据坏了）。
func oneLineText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}
