// Package localdep 是微型宿主的一个本地依赖：它让 go.mod 里存在一条**相对路径 replace**，
// 构建器把 modfile 搬到工作区之后若不改写成绝对路径，这条依赖就找不到了。
package localdep

// Name 供微型宿主的输出带上依赖来源，便于断言本地依赖确实参与了构建。
func Name() string { return "localdep" }
