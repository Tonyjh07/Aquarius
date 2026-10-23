//go:build !aqvalidate

package conversation

// ValidateEveryChange 生产构建恒为 false：整树自检是 O(n) 的全表扫描，
// 不该出现在每次追加的路径上。用 `-tags aqvalidate` 构建即可打开。
const ValidateEveryChange = false
