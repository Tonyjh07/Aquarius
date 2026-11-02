package i18n

// zhTexts 界面文案母版（D275）：按区域拆在 zh_*.go 里，这里只做合并。
//
// 只增不改语义——意思变了就换 key，别在旧 key 上改文本，否则两份表里总有一份留着旧意思。
// 这里是全仓唯一允许出现用户面中文字面量的地方（I4 的扫描锁盯的就是这条）。
var zhTexts = mergeTables(
	zhBootTexts,
	zhCmdTexts,
	zhSessionTexts,
	zhExchangeTexts,
	zhErrTexts,
)
