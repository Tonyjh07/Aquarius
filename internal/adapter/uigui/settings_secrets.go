package uigui

// settings_secrets.go 密钥库页数据面与动作（D161 落点④）：消费方定义接口与快照类型，
// 装配根实现（本机加密密钥库 secretbin）。
//
// 与工具页的差别：这里全是**本地文件 IO**（微秒级，不涉及网络/插件拨号），故动作**同步**执行
// ——工具页那套旁路 goroutine + 结果通道是为「15s 拨号会冻窗」准备的，套用到密钥库只会把
// 简单的读写搬进更间接的地方（D162「边界」口径：重复出现第三处再收）。
//
// 安全口径（§9）：值永不回显——快照只带**名字 + 尾 4 位掩码**；界面输入框每次动作后清空；
// 状态行与错误串都不含值。

import (
	"gioui.org/layout"
	"gioui.org/widget/material"
)

// SecretsAdmin 密钥库管理面（装配根实现；线程安全——次窗 goroutine 调用）。
type SecretsAdmin interface {
	// Snapshot 原子快照（含 Revision，任何变更递增）：条目按名字排序，只给名字与掩码。
	Snapshot() SecretsSnapshot
	// Put 写入/覆盖一条密钥；值永不回显，同名即覆盖。
	Put(name, value string) error
	// Delete 删除一条；名字不存在报错（不静默成功）。
	Delete(name string) error
}

// SecretsSnapshot 密钥库页的原子快照。
type SecretsSnapshot struct {
	Revision int64
	Path     string        // 库文件路径（展示用，不含任何值）
	Entries  []SecretEntry // 按名字排序
}

// SecretEntry 一条密钥的展示项（值永不回显）。
type SecretEntry struct {
	Name string // 键名（`secret:<名>` 里的 <名>）
	Mask string // 掩码：•••••• + 尾 4 位（值短于 8 位则整条打码）
}

// syncSecretsSnapshot 密钥库快照同步（Revision 变了才重建行/按钮；同工具页口径）。
// 帧首调用一次（settingsFrame）；测试可直接调以铺好行表。
func (f *settingsForm) syncSecretsSnapshot() {
	if f.secrets == nil {
		return
	}
	cur := f.secrets.Snapshot()
	if cur.Revision != f.secretsRev {
		f.secretsRev = cur.Revision
		f.secretsSnap = cur
		// 快照变了 = 条目可能已被删：把行内二次确认的待删目标清掉，免得确认按钮
		// 指着一条已不在的条目（点下去只会报「已不在」）。
		if f.secretConfirm != "" && !secretsSnapHas(f.secretsSnap, f.secretConfirm) {
			f.secretConfirm = ""
		}
	}
}

// secretsSnapHas 快照里是否有这个名字。
func secretsSnapHas(s SecretsSnapshot, name string) bool {
	for _, e := range s.Entries {
		if e.Name == name {
			return true
		}
	}
	return false
}

// secretsAction 密钥库页动作分发（同步；帧首调用）。动作不接线时只置只读反馈。
func (u *UI) secretsAction(f *settingsForm, act string, name string) {
	if f.secrets == nil {
		f.secretsErr, f.secretsStatus = true, "密钥库未接线（只读占位）"
		return
	}
	switch act {
	case "put":
		n := f.secretName.Text()
		v := f.secretValue.Text()
		f.secretValue.SetText("") // 值永不回显：无论成败都不留在输入框里
		if n == "" {
			f.secretsErr, f.secretsStatus = true, "请填写密钥名"
			return
		}
		if v == "" {
			f.secretsErr, f.secretsStatus = true, "请填写密钥值（要清掉某条请用该行「删除」）"
			return
		}
		if err := f.secrets.Put(n, v); err != nil {
			f.secretsErr, f.secretsStatus = true, "存入失败："+err.Error()
			return
		}
		f.secretsErr, f.secretsStatus = false, "已存入密钥库："+n
		f.secretName.SetText("")
	case "delete":
		if err := f.secrets.Delete(name); err != nil {
			f.secretsErr, f.secretsStatus = true, "删除失败："+err.Error()
			return
		}
		f.secretsErr, f.secretsStatus = false, "已删除："+name
		f.secretConfirm = ""
	}
	f.syncSecretsSnapshot() // 动作后立刻重取快照（Revision 已递增），本帧即刷新列表
}

// secretStatusRow 密钥库页反馈行（错误红 / 普通灰）。
func (f *settingsForm) secretStatusRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	l := material.Body2(th, f.secretsStatus)
	if f.secretsErr {
		l.Color = paperOf().danger
	} else {
		l.Color = paperOf().text3
	}
	return l.Layout(gtx)
}
