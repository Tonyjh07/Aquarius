//go:build aqvalidate

package conversation

// ValidateEveryChange 打开「每次变更后整树自检」（用 `-tags aqvalidate` 构建）。
//
// 打开后 Append/Revise/Prune 返回前都会跑一遍 Validate：不变量一旦被某条变更打破，
// 出错点立刻落在那条变更上，而不是等到下一次读树才莫名其妙地崩。生产构建恒为 false
// （见 validate_off.go），自检零开销。
const ValidateEveryChange = true
