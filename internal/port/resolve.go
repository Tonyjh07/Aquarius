package port

import "strings"

// MatchPrefix 在一组候选标识里按「精确优先，其次唯一前缀」解析 arg。
//
// 这是任务、观察器、会话节点、会话四处共用的同一套解析策略（§2.1）；四处原本各写一遍，
// 差别只在失败时的措辞，所以措辞留在各层（工具面英文、命令面中文），这里只管匹配。
//
// arg 必须已 TrimSpace 且非空——空串会前缀命中每一个候选，那是调用方的输入校验，不是解析。
// ids 的顺序即命中顺序：调用方按 ID 升序传入，歧义提示才可读。
//
// 返回：ok = 解析成功，id 是精确命中或唯一前缀命中的那个；!ok 时 hits 是前缀命中集——
// 长度 0 = 无命中，多于 1 个 = 歧义（调用方据此提示加长前缀）。
func MatchPrefix[T ~string](ids []T, arg string) (id T, hits []string, ok bool) {
	for _, cand := range ids {
		if string(cand) == arg {
			return cand, nil, true
		}
	}
	for _, cand := range ids {
		if strings.HasPrefix(string(cand), arg) {
			hits = append(hits, string(cand))
		}
	}
	if len(hits) == 1 {
		return T(hits[0]), nil, true
	}
	var zero T
	return zero, hits, false
}
