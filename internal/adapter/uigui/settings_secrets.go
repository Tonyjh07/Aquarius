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
	"fmt"
	"strings"

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
	// Import 一键把「旧处的密钥」收进密钥库：config 里仍是明文的 api_key（收库后把 config
	// 改写成 `secret:<名>` 引用），以及「库里没有、只存在于同名环境变量里」的引用值。
	// 同步执行（本地小文件 IO + 一次配置改写）。**库解不开时拒绝**——写库会从空表起步，
	// 那等于把库里已有条目整份丢掉。返回结果只含键名与说明，永不含值。
	Import() (SecretsImportResult, error)
}

// SecretsImportResult 一键导入的结果（只报条数与键名，永不含值）。
type SecretsImportResult struct {
	// Imported 已存入密钥库的键名（按名字排序）。
	Imported []string
	// Rewritten 其中配置里原为明文、已改写为 `secret:` 引用的键名（按名字排序）。
	Rewritten []string
	// Skipped 两处（密钥库与环境变量）都取不到的引用名（按名字排序）——没有可收的来源，
	// 不是错误；列出来是让用户知道哪些引用还空着。
	Skipped []string
	// Failed 未能导入的说明（点名 provider 与引用名，不含值）。
	Failed []string
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
//
// 反馈两条槽：存入/删除用 `secretsStatus`（状态行在新增区之后），导入用 `importStatus`。
// 导入入口已从界面撤下（D206：功能保留、不给按钮），这条槽位同样保留——动作还有别处
// 可以直接调，反馈不该因为没有行渲染就丢掉。
func (u *UI) secretsAction(f *settingsForm, act string, name string) {
	if f.secrets == nil {
		// 反馈落在本动作自己的槽位上。
		if act == "import" {
			f.importErr, f.importStatus = true, "密钥库未接线（只读占位）"
		} else {
			f.secretsErr, f.secretsStatus = true, "密钥库未接线（只读占位）"
		}
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
	case "import":
		res, err := f.secrets.Import()
		if err != nil {
			f.importErr, f.importStatus = true, "导入失败："+err.Error()
			break
		}
		status, isErr := importSummary(res)
		f.importErr, f.importStatus = isErr, status
	}
	f.syncSecretsSnapshot() // 动作后立刻重取快照（Revision 已递增），本帧即刷新列表
}

// importSummary 一键导入的反馈文案（只报条数，永不含值）。**不列键名**：收到库里的名字
// 就在上方「已存密钥」列表里，重复列一遍只会把这条反馈撑成三行、在按钮右侧挤到窗底。
// 失败项一律红字呈现；无失败项时按普通灰呈现。
func importSummary(r SecretsImportResult) (string, bool) {
	var b strings.Builder
	switch len(r.Imported) {
	case 0:
		b.WriteString("没有需要导入的密钥")
	default:
		fmt.Fprintf(&b, "已导入 %d 条", len(r.Imported))
		if len(r.Rewritten) > 0 {
			fmt.Fprintf(&b, "（其中 %d 条配置里的明文已改为 secret: 引用）", len(r.Rewritten))
		}
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "；另有 %d 条两处都取不到，未导入", len(r.Skipped))
	}
	if len(r.Failed) > 0 {
		fmt.Fprintf(&b, "；%d 条失败：%s", len(r.Failed), strings.Join(r.Failed, "；"))
		return b.String(), true
	}
	return b.String(), false
}

// secretStatusRow 密钥库页反馈行（错误红 / 普通灰）；文案取 f.secretsStatus（存入/删除）。
func (f *settingsForm) secretStatusRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return f.secretStatusRowOf(gtx, th, f.secretsStatus, f.secretsErr)
}

// secretStatusRowOf 密钥库页反馈行本体（导入与存入/删除各自一条，故文案与错误态作参数）。
func (f *settingsForm) secretStatusRowOf(gtx layout.Context, th *material.Theme, text string, isErr bool) layout.Dimensions {
	l := material.Body2(th, text)
	if isErr {
		l.Color = paperOf().danger
	} else {
		l.Color = paperOf().text3
	}
	return l.Layout(gtx)
}
