package decorate

// 无人值守装饰器（D185）：开关打开时「越界即拒」，而不是把人从别处叫回来点确认。
//
// 语义边界（口径一/二）：
//   - **拒绝而非放行**——权限矩阵原样，矩阵外的调用当场回绝并说明；模型自己改道。
//   - **只管模型发起的那条链**——装配根只把装饰器罩到 `toolrun` 上；`/rm`、`/rmconv`
//     等用户亲手输入的命令确认、插件能力授权照常询问（人就在键盘前）。
//   - **不抢占交互面**——不开窗、不闪、不进输入栏，纯粹是一个同步返回。
//
// 开关是**热态**：`on` 每次调用现问，所以 `/unattended on|off`、菜单勾选、设置窗开关
// 都即时生效，不需要重启也不需要重新包一层。

import (
	"context"
	"errors"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// UnattendedReason 无人值守下的拒绝原因（英文——进模型上下文；toolrun 会拼成
// `user denied <tool>: <reason>`）。措辞覆盖三件事：没人能批准、别原地重试、
// 在权限允许的范围内继续并把待批事项列出来——缺了任一条模型都会反复撞同一面墙。
const UnattendedReason = "unattended mode: nobody is available to approve this call. " +
	"Do not retry it; continue with what the current permission level allows and list " +
	"anything that still needs approval."

// errNoConfirmer 无人值守关闭且没有内层确认器时的兜底（fail-closed，不静默放行）。
var errNoConfirmer = errors.New("decorate: no confirmer is configured")

// unattendedConfirmer 确认端口的无人值守装饰器；on == nil 视为关闭。
type unattendedConfirmer struct {
	inner port.Confirmer
	on    func() bool
}

// Unattended 包装确认器：开启时立即返回「拒绝 + 原因」，不调内层、不留等待；
// 关闭时原样透传。ctx 取消优先于开关判定——用户中断的轮次不该变成一次「拒绝」
// 结论（toolrun 会把它归类为取消）。
func Unattended(inner port.Confirmer, on func() bool) port.Confirmer {
	return unattendedConfirmer{inner: inner, on: on}
}

func (u unattendedConfirmer) Confirm(ctx context.Context, prompt string) (port.ConfirmAnswer, error) {
	if err := ctx.Err(); err != nil {
		return port.ConfirmAnswer{}, err
	}
	if u.on != nil && u.on() {
		return port.ConfirmAnswer{Allow: false, Reason: UnattendedReason}, nil
	}
	if u.inner == nil {
		return port.ConfirmAnswer{}, errNoConfirmer
	}
	return u.inner.Confirm(ctx, prompt)
}

// unattendedAsker 提问端口的无人值守装饰器（口径六）。
type unattendedAsker struct {
	inner port.Asker
	on    func() bool
}

// UnattendedAsker 包装提问通道：开启时立即回填 `Away`（没人可问是事实，不是用户
// 选了「你自己看着办」——与 `Dismissed` 分开，回填模型的话术也不同）；关闭时透传。
// 提问同样不能把无人值守的轮次卡死在面板上，所以该装饰器与确认器同款热态。
func UnattendedAsker(inner port.Asker, on func() bool) port.Asker {
	return unattendedAsker{inner: inner, on: on}
}

func (u unattendedAsker) Ask(ctx context.Context, req port.AskRequest) (port.AskAnswer, error) {
	if err := ctx.Err(); err != nil {
		return port.AskAnswer{}, err
	}
	if u.on != nil && u.on() {
		return port.AskAnswer{Away: true}, nil
	}
	if u.inner == nil {
		return port.AskAnswer{}, errors.New("decorate: no asker is configured")
	}
	return u.inner.Ask(ctx, req)
}
