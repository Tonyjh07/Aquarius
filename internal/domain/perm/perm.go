// Package perm 定义权限等级与免确认矩阵的纯函数策略（DESIGN §9 / D22）。
//
// 两列分工、互不叠加（D22）：
//   - 文件类操作只看路径格：读全等级免确认；写看该格是否 rw（r 格写入 = 矩阵外 → 逐次确认）。
//   - 执行类工具只看工具列：Risk=Safe 免确认；Risk=Confirm 仅 full-access 免、其余逐次确认。
//   - 矩阵格 = 免确认范围；矩阵外一律 ask（Confirmer），无 deny 出口。
//
// 本包是领域级纯规则：零 IO、零端口依赖；执行接入在 adapter/toolrun（D25），
// 展示（/permission）与配置（config permissions.level）消费本包。
package perm

import (
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// Level 权限等级（config permissions.level 的合法取值）。
type Level string

const (
	// ReadOnly 特权目录 r、其他目录 r、工具 Confirm 逐次——读全免、写全要确认。
	ReadOnly Level = "read-only"
	// Strict 特权目录 rw、其他目录 r、工具 Confirm 逐次（默认，D22）。
	Strict Level = "strict"
	// Permissive 特权目录 rw、其他目录 rw、工具 Confirm 逐次。
	Permissive Level = "permissive"
	// FullAccess 全部 rw、工具全免确认。
	FullAccess Level = "full-access"
)

// DefaultLevel 首次/未配置时的默认等级（D22）。
const DefaultLevel = Strict

// Levels 全部合法等级（供 /permission 展示与校验）。
var Levels = []Level{ReadOnly, Strict, Permissive, FullAccess}

// Parse 解析等级名（大小写不敏感、去首尾空白）。
func Parse(s string) (Level, error) {
	v := Level(strings.ToLower(strings.TrimSpace(s)))
	for _, l := range Levels {
		if v == l {
			return l, nil
		}
	}
	return "", fmt.Errorf("perm: 未知权限等级 %q（可用：read-only / strict / permissive / full-access）", s)
}

// String 实现 fmt.Stringer。
func (l Level) String() string { return string(l) }

// Op 文件操作类型。
type Op int

const (
	OpRead Op = iota
	OpWrite
)

// String 实现 fmt.Stringer。
func (o Op) String() string {
	if o == OpWrite {
		return "write"
	}
	return "read"
}

// Decision 判定结果：Allow = 免确认执行；Ask = 经 Confirmer 逐次确认（矩阵外唯一出口，D22）。
type Decision int

const (
	// Allow 免确认执行。
	Allow Decision = iota
	// Ask 逐次确认。
	Ask
)

// String 实现 fmt.Stringer。
func (d Decision) String() string {
	if d == Ask {
		return "ask"
	}
	return "allow"
}

// File 路径格判定（文件类工具唯一判据，两列分工不叠加 Risk，D22）。
// inSandbox = 目标路径位于特权目录 <dataDir>/sandbox 内。
func (l Level) File(inSandbox bool, op Op) Decision {
	if op == OpRead {
		return Allow // 四档矩阵读格恒为 r：读全盘免确认
	}
	switch l {
	case Permissive, FullAccess:
		return Allow // 特权 rw + 其他 rw
	case Strict:
		if inSandbox {
			return Allow // 特权 rw；其他 r → 写在矩阵外
		}
		return Ask
	default: // ReadOnly：特权 r、其他 r，写一律矩阵外
		return Ask
	}
}

// Tool 执行类工具判定（term_exec/job_start 等的唯一判据，D22）：
// Safe 全等级免确认；Confirm 仅 full-access 免、其余逐次。
func (l Level) Tool(risk tool.Risk) Decision {
	if risk == tool.Safe {
		return Allow
	}
	if l == FullAccess {
		return Allow
	}
	return Ask
}

// Cell 矩阵格显示值。
type Cell string

const (
	// CellR 只读格。
	CellR Cell = "r"
	// CellRW 读写格。
	CellRW Cell = "rw"
)

// Matrix 返回等级的三格展示值（特权目录 / 其他目录 / 工具全免与否）。
// 展示值由 File/Tool 判定推导，与执行策略单一事实源不漂移。
func (l Level) Matrix() (sandbox, other Cell, toolsFree bool) {
	sandbox = CellR
	if l.File(true, OpWrite) == Allow {
		sandbox = CellRW
	}
	other = CellR
	if l.File(false, OpWrite) == Allow {
		other = CellRW
	}
	toolsFree = l.Tool(tool.Confirm) == Allow
	return sandbox, other, toolsFree
}
