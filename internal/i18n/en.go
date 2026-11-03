package i18n

// enTexts 界面文案英文表（D275）：与 zh_*.go 同 key、同顺序、同占位符名——并排看能一眼
// 对齐。顺序与 key 都不许各自演化，改一边必须改另一边（同构测试守着）。
var enTexts = mergeTables(
	enBootTexts,
	enTreeTexts,
	enCmdTexts,
	enEditTexts,
	enSessionTexts,
	enAgentTexts,
	enExchangeTexts,
	enRuntimeTexts,
	enErrTexts,
	enPortTexts,
	enConfigTexts,
	enTuiTexts,
	enLogTexts,
	enGuiCommonTexts,
	enGuiSettingsTexts,
	enGuiPageCoreTexts,
	enGuiPageAdminTexts,
	enGuiWelcomeTexts,
	enGuiChatTexts,
	enCmdConfigTexts,
	enCmdAdminTexts,
)
