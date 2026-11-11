package perm

// 放行策略表（D280）：按 (来源, 工具) 存例外，默认一条不存。
//
// 四态里「禁用」不在本文件：它在**装配层**生效（工具定义不下发），与 tools.disabled
// 同源。这里只有判定层的三档。优先级：用户显式 > 矩阵 > 缺省 ask。
//
// 只存例外的理由：全表落盘时，用户改一次 permissions.level 就会撞上「配置里那二十条
// 还是老的」。改过的才写，没改的永远跟着矩阵走。

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// Mode 一条例外的档位（四态里落在判定层的三档）。
type Mode string

const (
	// ModeAsk 每次都问（可用来收紧矩阵判 Allow 的工具）。
	ModeAsk Mode = "ask"
	// ModeAssist 先问决策模型，拿不准再问用户；决策模型未接时等同 ask。
	ModeAssist Mode = "assist"
	// ModeAllow 免确认（可用来放宽矩阵判 Ask 的工具）。
	ModeAllow Mode = "allow"
)

// Modes 全部合法档位（供界面与校验，按从严到宽排列）。
var Modes = []Mode{ModeAsk, ModeAssist, ModeAllow}

// ParseMode 解析档位名（大小写不敏感、去首尾空白）。
func ParseMode(s string) (Mode, error) {
	v := Mode(strings.ToLower(strings.TrimSpace(s)))
	for _, m := range Modes {
		if v == m {
			return m, nil
		}
	}
	return "", errors.New(i18n.Tf("perm.err.unknown_mode", "mode", s))
}

// Origin 策略表的来源维。取值与 port.ConfirmOrigin 一致；本包不依赖 port。
type Origin string

const (
	// OriginTool 本地模型发起的工具调用（toolrun）。
	OriginTool Origin = "toolrun"
	// OriginAgent 外部代理经 ACP 发起的请求（acpgate）。
	OriginAgent Origin = "acpgate"
)

// Origins 全部合法来源。
var Origins = []Origin{OriginTool, OriginAgent}

// ParseOrigin 解析来源名（大小写不敏感、去首尾空白）。
func ParseOrigin(s string) (Origin, error) {
	v := Origin(strings.ToLower(strings.TrimSpace(s)))
	for _, o := range Origins {
		if v == o {
			return o, nil
		}
	}
	return "", errors.New(i18n.Tf("perm.err.unknown_origin", "origin", s))
}

// Override 一条例外。Tool 为空 = 该来源的全部请求（ACP 的 fs/terminal 回调没有
// 稳定的工具名，用空工具表达「这个来源整体」）。
//
// Dir 非空 = 这条只对**该目录内的目标**生效（D282 的「以后都允许」落在文件类工具时
// 就是这种形态）：`file_write` 一步放宽到全盘可写，是从「这个文件」跨到「任何文件」，
// 而用户点下去时想的是「我一直在同一个项目里干活」。
type Override struct {
	Origin Origin
	Tool   string
	Mode   Mode
	Dir    string
}

// AutoAllow 由一次确认生成「以后都允许」的例外（D282）：执行类落到工具级（命令内容
// 每次都不一样，没有可收窄的维度），文件类收窄到目标所在目录；没有目标路径的操作
// （执行类之外的少数工具）也只能落到工具级。
//
// 判定与文案共用这一个函数：界面上那句「以后这个目录里不再询问」与实际写进配置的
// 范围必须是同一个值，两处各算一遍迟早对不上。
func AutoAllow(origin Origin, tool, target string, hasTarget bool) Override {
	ov := Override{Origin: origin, Tool: tool, Mode: ModeAllow}
	if !hasTarget || IsExec(tool) {
		return ov
	}
	if dir := filepath.Dir(strings.TrimSpace(target)); dir != "" && dir != "." {
		ov.Dir = dir
	}
	return ov
}

// Overrides 例外表：只有用户改过的条目，顺序无意义。
type Overrides []Override

// Resolve 命中哪条例外，从具体到宽泛：目录级（目标在该目录内）> 工具级 > 来源级。
// 「外部代理整体要问，但这个代理的写文件放行」与「这个工具的写文件只在某个项目里
// 放行」都得能表达，所以按具体程度取，不是先到先得。
//
// target 为空（执行类没有目标路径）时目录级条目一律不命中——一条「某目录内放行」
// 的规则不该罩住没有路径的调用。
func (o Overrides) Resolve(origin Origin, tool, target string) (Override, bool) {
	var best Override
	var hasBest bool
	bestDir := -1
	var toolWide Override
	var hasToolWide bool
	var fallback Override
	var hasFallback bool
	for _, ov := range o {
		if ov.Origin != origin {
			continue
		}
		if ov.Tool == "" { // 该来源整体
			if !hasFallback {
				fallback, hasFallback = ov, true
			}
			continue
		}
		if ov.Tool != tool {
			continue
		}
		if ov.Dir == "" {
			if !hasToolWide {
				toolWide, hasToolWide = ov, true
			}
			continue
		}
		// 目录级：嵌套目录时取更深的那个（用户后来又给子目录单点了一次）。
		if target != "" && withinRoot(target, ov.Dir) && len(ov.Dir) > bestDir {
			best, hasBest, bestDir = ov, true, len(ov.Dir)
		}
	}
	switch {
	case hasBest:
		return best, true
	case hasToolWide:
		return toolWide, true
	}
	return fallback, hasFallback
}

// Apply 判定层的最终结论：例外覆盖矩阵，两个方向都允许（D280 第 2 条）。
// 没有命中例外时原样返回矩阵结论。target 只参与目录级例外的范围比较。
func (o Overrides) Apply(base Decision, origin Origin, tool, target string) Decision {
	ov, ok := o.Resolve(origin, tool, target)
	if !ok {
		return base
	}
	switch ov.Mode {
	case ModeAllow:
		return Allow
	case ModeAssist:
		return Assist
	default:
		return Ask
	}
}

// execToolNames 执行类工具（D280）：命令与任务内容都是模型现编的，按工具放行等于给
// 一个 shell。它们**可以**被设成自动放行（比 full-access 窄），但界面必须先标红并
// 二次确认——单条策略会被忘掉，全局面板有明确的心理预期，一条策略没有。
//
// 清单是代码常量而不是配置：这类判定跟着工具的语义走，不是用户可以调的旋钮。
var execToolNames = []string{"term_exec", "job_start"}

// IsExec 该工具是否执行类（命令/任务由模型现编）。
func IsExec(tool string) bool {
	for _, n := range execToolNames {
		if n == tool {
			return true
		}
	}
	return false
}
